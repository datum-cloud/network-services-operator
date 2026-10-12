// SPDX-License-Identifier: AGPL-3.0-only

package nlbhealth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	ProbeTimeout          = 2 * time.Second
	ProbeInterval         = 5 * time.Second
	ResultLifetime        = 10 * time.Second
	HealthyAfterSuccesses = 2
)

type Prober struct {
	DialContext func(context.Context, string, string, string) (net.Conn, error)
	RootCAs     *x509.CertPool
}

func (p Prober) Probe(ctx context.Context, fabricVPCID string, target networkingv1alpha.NetworkLoadBalancerBackendHealthTarget) error {
	if p.DialContext == nil || fabricVPCID == "" {
		return errors.New("VPC-aware probe dialer is not configured")
	}
	address, err := netip.ParseAddr(target.Address)
	if err != nil || !address.Is6() || address.Is4In6() || address.IsUnspecified() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return errors.New("target must be a routable IPv6 unicast address")
	}
	if target.Probe.Port < 1 || target.Probe.Port > 65535 {
		return errors.New("invalid probe port")
	}

	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	endpoint := net.JoinHostPort(address.String(), strconv.Itoa(int(target.Probe.Port)))
	dial := func(ctx context.Context, network, endpoint string) (net.Conn, error) {
		return p.DialContext(ctx, fabricVPCID, network, endpoint)
	}

	switch target.Probe.Protocol {
	case networkingv1alpha.NetworkLoadBalancerProbeTCP:
		conn, err := dial(ctx, "tcp", endpoint)
		if err != nil {
			return err
		}
		return conn.Close()
	case networkingv1alpha.NetworkLoadBalancerProbeHTTP, networkingv1alpha.NetworkLoadBalancerProbeHTTPS:
		path := target.Probe.Path
		if path == "" {
			path = "/"
		}
		if path[0] != '/' {
			return errors.New("probe path must begin with /")
		}
		scheme := "http"
		if target.Probe.Protocol == networkingv1alpha.NetworkLoadBalancerProbeHTTPS {
			scheme = "https"
		}
		transport := &http.Transport{
			Proxy:             nil,
			DialContext:       dial,
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.RootCAs, ServerName: target.Probe.ServerName},
		}
		defer transport.CloseIdleConnections()
		requestURL := url.URL{Scheme: scheme, Host: endpoint, Path: path}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return err
		}
		if target.Probe.ServerName != "" {
			request.Host = target.Probe.ServerName
		}
		response, err := (&http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}).Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("probe returned HTTP %d", response.StatusCode)
		}
		return nil
	default:
		return errors.New("unsupported probe protocol")
	}
}

func TargetFingerprint(target networkingv1alpha.NetworkLoadBalancerBackendHealthTarget) string {
	encoded, _ := json.Marshal(target)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func NextTargetStatus(
	health *networkingv1alpha.NetworkLoadBalancerBackendHealth,
	target networkingv1alpha.NetworkLoadBalancerBackendHealthTarget,
	previous *networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus,
	probeErr error,
	now time.Time,
) networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus {
	fingerprint := TargetFingerprint(target)
	status := networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus{
		Name:               target.Name,
		ObservedGeneration: health.Generation,
		RouteUID:           health.Spec.RouteRef.UID,
		RouteGeneration:    health.Spec.RouteRef.Generation,
		TargetFingerprint:  fingerprint,
		LastProbeTime:      metav1.NewTime(now),
		ValidUntil:         metav1.NewTime(now),
	}
	if probeErr != nil {
		status.Reason = "ProbeFailed"
		return status
	}
	if previous != nil && previous.ObservedGeneration == health.Generation &&
		previous.RouteUID == health.Spec.RouteRef.UID && previous.RouteGeneration == health.Spec.RouteRef.Generation &&
		previous.TargetFingerprint == fingerprint && previous.ValidUntil.After(now) && !previous.LastProbeTime.After(now) {
		status.ConsecutiveSuccesses = previous.ConsecutiveSuccesses
	}
	if status.ConsecutiveSuccesses < HealthyAfterSuccesses {
		status.ConsecutiveSuccesses++
	}
	status.Healthy = status.ConsecutiveSuccesses >= HealthyAfterSuccesses
	if status.Healthy {
		status.Reason = "Healthy"
	} else {
		status.Reason = "Warming"
	}
	status.ValidUntil = metav1.NewTime(now.Add(ResultLifetime))
	return status
}

func HealthyTargets(health *networkingv1alpha.NetworkLoadBalancerBackendHealth, now time.Time) []networkingv1alpha.NetworkLoadBalancerBackendHealthTarget {
	condition := apimeta.FindStatusCondition(health.Status.Conditions, "ProbesEvaluated")
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != health.Generation {
		return nil
	}
	byName := make(map[string]networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus, len(health.Status.Targets))
	for _, status := range health.Status.Targets {
		byName[status.Name] = status
	}
	var eligible []networkingv1alpha.NetworkLoadBalancerBackendHealthTarget
	for _, target := range health.Spec.Targets {
		status, found := byName[target.Name]
		if !found || !status.Healthy || status.ObservedGeneration != health.Generation ||
			status.RouteUID != health.Spec.RouteRef.UID || status.RouteGeneration != health.Spec.RouteRef.Generation ||
			status.TargetFingerprint != TargetFingerprint(target) || status.LastProbeTime.IsZero() ||
			status.LastProbeTime.After(now) || !status.ValidUntil.After(now) ||
			status.ValidUntil.Time.Sub(status.LastProbeTime.Time) > ResultLifetime {
			continue
		}
		eligible = append(eligible, target)
	}
	return eligible
}
