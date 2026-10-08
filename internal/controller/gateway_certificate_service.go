// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// +kubebuilder:rbac:groups=certificates.miloapis.com,resources=tlscertificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=certificates.miloapis.com,resources=tlscertificates/status,verbs=get

const KindTLSCertificate = "TLSCertificate"

// tlsCertificateMirrorAdmitDelay is how soon the listener is re-evaluated after
// its Secret lands downstream, since listener health was judged before the
// mirror in the same pass.
const tlsCertificateMirrorAdmitDelay = time.Second

const (
	certificateServiceBackoffBase = 5 * time.Second
	certificateServiceBackoffMax  = 5 * time.Minute

	// certificateServiceRecheck is how often a wildcard listener is looked at
	// again without any event, so a renewal that quietly stalls is noticed.
	certificateServiceRecheck = time.Hour

	// tlsCertificateIssueGrace is how long a TLSCertificate may stay not Ready
	// before it is reported as failing rather than in progress.
	tlsCertificateIssueGrace = time.Hour

	// renewalOverdueDivisor sets the share of a served certificate's lifetime
	// below which its renewal is overdue. The service renews with a third left.
	renewalOverdueDivisor = 4
)

var nonDNSLabelChars = regexp.MustCompile(`[^a-z0-9-]+`)

// tlsCertificateName names the listener's TLSCertificate: a readable prefix
// from the gateway and listener plus a hash of both, so "a-b"/"c" and "a"/"b-c"
// never meet, inside the service's 63 character cap and valid as a DNS label
// whatever the gateway was called.
func tlsCertificateName(gatewayName string, listenerName gatewayv1.SectionName) string {
	sum := sha256.Sum256([]byte(gatewayName + "/" + string(listenerName)))
	suffix := hex.EncodeToString(sum[:])[:10]

	prefix := nonDNSLabelChars.ReplaceAllString(strings.ToLower(gatewayName+"-"+string(listenerName)), "-")
	if max := 63 - len(suffix) - 1; len(prefix) > max {
		prefix = prefix[:max]
	}
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		return suffix
	}
	return prefix + "-" + suffix
}

// isSingleLabelWildcard reports whether a hostname is a wildcard over exactly
// one label, the only shape the certificate service is used for.
func isSingleLabelWildcard(hostname string) bool {
	base, ok := strings.CutPrefix(hostname, "*.")
	return ok && base != "" && !strings.Contains(base, "*")
}

// listenerWantsOwnCertificate reports whether a listener is one the controller
// issues a per-hostname certificate for, under either issuance path.
func (r *GatewayReconciler) listenerWantsOwnCertificate(l gatewayv1.Listener, claimedHostnames []string) (string, bool) {
	if l.TLS == nil || l.TLS.Options[certificateIssuerTLSOption] == "" || l.Hostname == nil {
		return "", false
	}
	hostname := string(*l.Hostname)
	if !slices.Contains(claimedHostnames, hostname) {
		return "", false
	}
	wildcardSuffix := "." + r.Config.Gateway.TargetDomain
	if r.Config.Gateway.HasDefaultListenerTLSSecret() &&
		(strings.HasSuffix(hostname, wildcardSuffix) || hostname == r.Config.Gateway.TargetDomain) {
		return "", false
	}
	return hostname, true
}

// listenerUsesCertificateService reports whether the certificate service, not
// cert-manager, issues for this listener: only wildcard hostnames, and only
// while the service is enabled. Every exact hostname stays on cert-manager.
func (r *GatewayReconciler) listenerUsesCertificateService(l gatewayv1.Listener, claimedHostnames []string) (string, bool) {
	if !r.Config.Gateway.CertificateService.Enabled {
		return "", false
	}
	hostname, wanted := r.listenerWantsOwnCertificate(l, claimedHostnames)
	if !wanted || !isSingleLabelWildcard(hostname) {
		return "", false
	}
	return hostname, true
}

// listenerIssuerResolvable applies the same rule the cert-manager path does to
// the certificate-issuer option: an `auto` that maps to nothing and inherits
// from no other listener leaves the listener un-programmed.
func (r *GatewayReconciler) listenerIssuerResolvable(upstreamGateway *gatewayv1.Gateway, l gatewayv1.Listener) bool {
	issuer := string(l.TLS.Options[certificateIssuerTLSOption])
	if mapped := r.Config.Gateway.ClusterIssuerMap[issuer]; mapped != "" {
		issuer = mapped
	}
	return issuer != autoIssuerSentinel || r.resolveAutoIssuer(upstreamGateway) != ""
}

// certificateServiceKey identifies one listener of one gateway. An empty
// listener stands for the gateway-wide cleanup.
type certificateServiceKey struct {
	gateway  types.UID
	listener gatewayv1.SectionName
}

// certificateServiceBackoff is what the step remembers about a listener whose
// last pass failed: how many in a row, when it may try again, and what it told
// the customer, so an event-driven reconcile inside the window repeats the
// message rather than the calls.
type certificateServiceBackoff struct {
	attempts int
	nextTry  time.Time
	issue    certificateServiceIssue
}

// certificateServiceRequeue records the outcome of a listener's pass and hands
// back the delay before the next attempt, doubling per consecutive failure up
// to a cap, and forgets the listener once a pass succeeds.
func (r *GatewayReconciler) certificateServiceRequeue(key certificateServiceKey, failed bool, now time.Time, issue certificateServiceIssue) time.Duration {
	if !failed {
		r.certificateServiceFailures.Delete(key)
		return 0
	}
	attempts := 1
	if previous, ok := r.certificateServiceFailures.Load(key); ok {
		attempts = previous.(certificateServiceBackoff).attempts + 1
	}
	delay := certificateServiceBackoffBase << (attempts - 1)
	if attempts > 10 || delay > certificateServiceBackoffMax {
		delay = certificateServiceBackoffMax
	}
	r.certificateServiceFailures.Store(key, certificateServiceBackoff{attempts: attempts, nextTry: now.Add(delay), issue: issue})
	return delay
}

// certificateServiceInBackoff reports whether the listener's last failure is
// still cooling off, and if so what it was told and how long is left.
func (r *GatewayReconciler) certificateServiceInBackoff(key certificateServiceKey, now time.Time) (certificateServiceBackoff, time.Duration, bool) {
	previous, ok := r.certificateServiceFailures.Load(key)
	if !ok {
		return certificateServiceBackoff{}, 0, false
	}
	backoff := previous.(certificateServiceBackoff)
	if !now.Before(backoff.nextTry) {
		return backoff, 0, false
	}
	return backoff, backoff.nextTry.Sub(now), true
}

func recordCertificateServiceFailure(gateway *gatewayv1.Gateway, listener gatewayv1.SectionName, reason string) {
	certificateServiceFailuresTotal.WithLabelValues(gateway.Namespace, gateway.Name, string(listener), reason).Inc()
}

// recordCertificateServiceState counts a TLSCertificate failure once per
// transition into it, since the same failure is read back on every pass for
// as long as it lasts. An empty reason means the listener is healthy.
func (r *GatewayReconciler) recordCertificateServiceState(gateway *gatewayv1.Gateway, listener gatewayv1.SectionName, reason string) {
	key := certificateServiceKey{gateway: gateway.UID, listener: listener}
	previous, _ := r.certificateServiceStates.Load(key)
	if reason != "" && previous != reason {
		recordCertificateServiceFailure(gateway, listener, reason)
	}
	if reason == "" {
		r.certificateServiceStates.Delete(key)
		return
	}
	r.certificateServiceStates.Store(key, reason)
}

const (
	certificateServiceReasonStepFailed      = "StepFailed"
	certificateServiceReasonNotOwned        = "NotOwned"
	certificateServiceReasonRefused         = "Refused"
	certificateServiceReasonRejected        = "Rejected"
	certificateServiceReasonIssuanceFailed  = "IssuanceFailed"
	certificateServiceReasonNotReady        = "NotReady"
	certificateServiceReasonRenewalOverdue  = "RenewalOverdue"
	certificateServiceReasonMaterialRefused = "MaterialRefused"
	certificateServiceReasonUntrustedChain  = "UntrustedChain"
)

func certificateServiceUnavailableMessage(hostname string) string {
	return fmt.Sprintf("We couldn't request a TLS certificate for %s just now and will keep trying. HTTPS for this hostname stays as it is in the meantime.", hostname)
}

func certificateRequestRefusedMessage(hostname, detail string) string {
	return fmt.Sprintf("A TLS certificate cannot be issued for %s: %s", hostname, detail)
}

func certificateRequestNotOwnedMessage(hostname string) string {
	return fmt.Sprintf("A TLS certificate cannot be requested for %s because another resource holds the certificate request this hostname would use.", hostname)
}

func certificateMaterialRefusedMessage(hostname string) string {
	return fmt.Sprintf("The TLS certificate issued for %s did not pass our checks and was not applied. HTTPS for this hostname stays as it is in the meantime.", hostname)
}

func certificateBeingReplacedMessage(hostname string) string {
	return fmt.Sprintf("The TLS certificate request for %s is being replaced. HTTPS for this hostname stays as it is in the meantime.", hostname)
}

func certificateRenewalOverdueMessage(hostname string, notAfter time.Time) string {
	return fmt.Sprintf("The TLS certificate for %s expires on %s and has not been renewed.", hostname, notAfter.UTC().Format(time.DateOnly))
}

func certificateNotIssuedMessage(hostname, detail string) string {
	if detail == "" {
		return fmt.Sprintf("The TLS certificate for %s has not been issued after more than an hour.", hostname)
	}
	return fmt.Sprintf("The TLS certificate for %s has not been issued: %s", hostname, detail)
}

// certificateServiceIssue is what one listener's pass could not do: the
// counter reason and the message for the customer.
type certificateServiceIssue struct {
	reason  string
	message string
}

// ensureListenerTLSCertificates is the certificate-service counterpart of
// ensureListenerCertificates for wildcard listeners: one TLSCertificate each in
// the project control plane, issued over DNS-01, and its issued Secret
// mirrored downstream under the name the listener references. Nothing here
// fails the gateway reconcile: a listener whose step failed keeps whatever
// Secret it has, the failure is returned as a message for its status, and that
// listener alone retries with backoff.
func (r *GatewayReconciler) ensureListenerTLSCertificates(
	ctx context.Context,
	upstreamClusterName string,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy downstreamclient.ResourceStrategy,
	claimedHostnames []string,
) (result Result, issues map[gatewayv1.SectionName]certificateServiceIssue) {
	logger := log.FromContext(ctx)
	now := time.Now()
	issues = make(map[gatewayv1.SectionName]certificateServiceIssue)
	var entitled *bool
	var entitlementErr error
	failing := make(map[gatewayv1.SectionName]string)
	desiredCerts := sets.New[string]()

	requeueSooner := func(d time.Duration) {
		if d > 0 && (result.RequeueAfter == 0 || d < result.RequeueAfter) {
			result.RequeueAfter = d
		}
	}

	for _, l := range upstreamGateway.Spec.Listeners {
		hostname, ok := r.listenerUsesCertificateService(l, claimedHostnames)
		if !ok || !r.listenerIssuerResolvable(upstreamGateway, l) {
			continue
		}
		certName := tlsCertificateName(upstreamGateway.Name, l.Name)
		desiredCerts.Insert(certName)
		requeueSooner(certificateServiceRecheck)
		holdsCertificate, err := r.listenerHoldsWildcardCertificate(ctx, upstreamClient, upstreamGateway, certName, hostname)
		if err != nil {
			logger.Error(err, "failed to read TLSCertificate", "listener", l.Name, "hostname", hostname)
			issue := certificateServiceIssue{reason: certificateServiceReasonStepFailed, message: certificateServiceUnavailableMessage(hostname)}
			issues[l.Name] = issue
			failing[l.Name] = issue.reason
			recordCertificateServiceFailure(upstreamGateway, l.Name, issue.reason)
			requeueSooner(certificateServiceRecheck)
			continue
		}
		if !holdsCertificate {
			requeueSooner(wildcardEntitlementRecheck)
			if entitled == nil && entitlementErr == nil {
				allowed, err := r.wildcardEntitled(ctx, upstreamClusterName)
				if err != nil {
					logger.Error(err, "failed to read wildcard hostname entitlement, not requesting certificates", "project", upstreamClusterName)
					entitlementErr = err
				} else {
					entitled = &allowed
				}
			}
			if entitlementErr != nil {
				issue := certificateServiceIssue{reason: certificateServiceReasonStepFailed, message: certificateServiceUnavailableMessage(hostname)}
				issues[l.Name] = issue
				failing[l.Name] = issue.reason
				recordCertificateServiceFailure(upstreamGateway, l.Name, issue.reason)
				continue
			}
			if !*entitled {
				issues[l.Name] = certificateServiceIssue{reason: certificateServiceReasonWildcardNotEntitled, message: wildcardNotEntitledMessage(hostname)}
				continue
			}
		}

		key := certificateServiceKey{gateway: upstreamGateway.UID, listener: l.Name}
		if backoff, remaining, cooling := r.certificateServiceInBackoff(key, now); cooling {
			issues[l.Name] = backoff.issue
			failing[l.Name] = backoff.issue.reason
			requeueSooner(remaining)
			continue
		}

		mirrored, issue, err := r.ensureListenerTLSCertificate(ctx, upstreamClient, upstreamGateway, downstreamGateway, downstreamStrategy, l.Name, certName, hostname, now)
		if err != nil {
			logger.Error(err, "certificate service step failed", "listener", l.Name, "hostname", hostname)
			if issue == nil {
				issue = &certificateServiceIssue{reason: certificateServiceReasonStepFailed, message: certificateServiceUnavailableMessage(hostname)}
			}
		}
		if issue != nil {
			issues[l.Name] = *issue
			failing[l.Name] = issue.reason
			recordCertificateServiceFailure(upstreamGateway, l.Name, issue.reason)
			requeueSooner(r.certificateServiceRequeue(key, true, now, *issue))
			continue
		}
		r.certificateServiceRequeue(key, false, now, certificateServiceIssue{})
		if state, ok := r.certificateServiceStates.Load(key); ok {
			failing[l.Name] = state.(string)
		}
		if mirrored {
			requeueSooner(tlsCertificateMirrorAdmitDelay)
		}
	}

	cleanupKey := certificateServiceKey{gateway: upstreamGateway.UID}
	if _, remaining, cooling := r.certificateServiceInBackoff(cleanupKey, now); cooling {
		requeueSooner(remaining)
	} else if err := r.deleteStaleTLSCertificates(ctx, upstreamClient, upstreamGateway, desiredCerts); err != nil {
		logger.Error(err, "failed to clean up TLSCertificates")
		requeueSooner(r.certificateServiceRequeue(cleanupKey, true, now, certificateServiceIssue{}))
	} else {
		r.certificateServiceRequeue(cleanupKey, false, now, certificateServiceIssue{})
	}

	r.forgetRemovedListeners(upstreamGateway)
	publishCertificateServiceFailing(upstreamGateway, failing)

	return result, issues
}

func (r *GatewayReconciler) listenerHoldsWildcardCertificate(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	certName, hostname string,
) (bool, error) {
	var cert certificatesv1alpha1.TLSCertificate
	err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: certName}, &cert)
	switch {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to get TLSCertificate %s: %w", certName, err)
	}
	return metav1.IsControlledBy(&cert, upstreamGateway) &&
		cert.DeletionTimestamp.IsZero() &&
		slices.Equal(cert.Spec.DNSNames, []certificatesv1alpha1.DNSName{certificatesv1alpha1.DNSName(hostname)}), nil
}

// publishCertificateServiceFailing replaces the gateway's failing-listener
// series with the listeners failing now, so one that recovered or left the
// service stops reporting.
func publishCertificateServiceFailing(upstreamGateway *gatewayv1.Gateway, failing map[gatewayv1.SectionName]string) {
	clearCertificateServiceFailing(upstreamGateway)
	for listener, reason := range failing {
		certificateServiceListenerFailing.WithLabelValues(upstreamGateway.Namespace, upstreamGateway.Name, string(listener), reason).Set(1)
	}
}

func clearCertificateServiceFailing(upstreamGateway *gatewayv1.Gateway) {
	certificateServiceListenerFailing.DeletePartialMatch(prometheus.Labels{jsonKeyNamespace: upstreamGateway.Namespace, jsonKeyName: upstreamGateway.Name})
}

// ensureListenerTLSCertificate does one listener's pass: request or confirm
// its TLSCertificate, then mirror what the service issued. An issue says what
// the customer is told; an error alone is a transient step failure.
func (r *GatewayReconciler) ensureListenerTLSCertificate(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy downstreamclient.ResourceStrategy,
	listenerName gatewayv1.SectionName,
	certName string,
	hostname string,
	now time.Time,
) (bool, *certificateServiceIssue, error) {
	secretName := listenerCertificateSecretName(upstreamGateway.Name, listenerName)

	cert, state, err := r.ensureTLSCertificate(ctx, upstreamClient, upstreamGateway, certName, hostname)
	switch {
	case errors.Is(err, errTLSCertificateNotOwned):
		return false, &certificateServiceIssue{reason: certificateServiceReasonNotOwned, message: certificateRequestNotOwnedMessage(hostname)}, err
	case err != nil:
		if detail, refused := requestRefusal(err); refused {
			return false, &certificateServiceIssue{reason: certificateServiceReasonRefused, message: certificateRequestRefusedMessage(hostname, detail)}, err
		}
		return false, nil, err
	case state != tlsCertificateSettled:
		return false, &certificateServiceIssue{reason: certificateServiceReasonStepFailed, message: certificateBeingReplacedMessage(hostname)}, nil
	}

	return r.mirrorTLSCertificateSecret(ctx, downstreamStrategy, upstreamGateway, downstreamGateway, cert, secretName, hostname, now)
}

// requestRefusal reports whether the API refused the request itself rather
// than failing to process it, and the reason it gave.
func requestRefusal(err error) (string, bool) {
	if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) && !apierrors.IsBadRequest(err) {
		return "", false
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) && status.Status().Message != "" {
		return status.Status().Message, true
	}
	return err.Error(), true
}

// forgetRemovedListeners drops the failure series and tracker entries of
// listeners the gateway no longer has, so a removed hostname stops reporting.
func (r *GatewayReconciler) forgetRemovedListeners(upstreamGateway *gatewayv1.Gateway) {
	current := sets.New[gatewayv1.SectionName]()
	for _, l := range upstreamGateway.Spec.Listeners {
		current.Insert(l.Name)
	}
	if previous, ok := r.certificateServiceListeners.Load(upstreamGateway.UID); ok {
		for listener := range previous.(sets.Set[gatewayv1.SectionName]) {
			if current.Has(listener) {
				continue
			}
			certificateServiceFailuresTotal.DeletePartialMatch(prometheus.Labels{
				jsonKeyNamespace: upstreamGateway.Namespace, jsonKeyName: upstreamGateway.Name, metricLabelListener: string(listener),
			})
			key := certificateServiceKey{gateway: upstreamGateway.UID, listener: listener}
			r.certificateServiceStates.Delete(key)
			r.certificateServiceFailures.Delete(key)
		}
	}
	r.certificateServiceListeners.Store(upstreamGateway.UID, current)
}

// forgetGateway drops everything the certificate service step remembers about
// a gateway that is gone.
func (r *GatewayReconciler) forgetGateway(upstreamGateway *gatewayv1.Gateway) {
	r.certificateServiceStates.Range(func(k, _ any) bool {
		if k.(certificateServiceKey).gateway == upstreamGateway.UID {
			r.certificateServiceStates.Delete(k)
		}
		return true
	})
	r.certificateServiceFailures.Range(func(k, _ any) bool {
		if k.(certificateServiceKey).gateway == upstreamGateway.UID {
			r.certificateServiceFailures.Delete(k)
		}
		return true
	})
	r.certificateServiceListeners.Delete(upstreamGateway.UID)
	certificateServiceFailuresTotal.DeletePartialMatch(prometheus.Labels{jsonKeyNamespace: upstreamGateway.Namespace, jsonKeyName: upstreamGateway.Name})
	clearCertificateServiceFailing(upstreamGateway)
}

var errTLSCertificateNotOwned = errors.New("TLSCertificate exists but is not controlled by this Gateway")

type tlsCertificateState int

const (
	tlsCertificateSettled tlsCertificateState = iota
	tlsCertificateReplacing
)

// ensureTLSCertificate creates the listener's TLSCertificate or confirms the
// existing one. dnsNames is immutable on the service's API, so one of ours that
// no longer matches is deleted and requested again once it is gone. One this
// gateway does not control is never touched.
func (r *GatewayReconciler) ensureTLSCertificate(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	certName, hostname string,
) (*certificatesv1alpha1.TLSCertificate, tlsCertificateState, error) {
	logger := log.FromContext(ctx)

	desiredSpec := certificatesv1alpha1.TLSCertificateSpec{
		DNSNames: []certificatesv1alpha1.DNSName{certificatesv1alpha1.DNSName(hostname)},
		Issuance: certificatesv1alpha1.IssuanceModeDNS01,
	}

	cert := &certificatesv1alpha1.TLSCertificate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: upstreamGateway.Namespace,
			Name:      certName,
		},
	}

	err := upstreamClient.Get(ctx, client.ObjectKeyFromObject(cert), cert)
	switch {
	case apierrors.IsNotFound(err):
		if err := controllerutil.SetControllerReference(upstreamGateway, cert, upstreamClient.Scheme()); err != nil {
			return nil, tlsCertificateSettled, fmt.Errorf("failed to set controller reference on TLSCertificate %s: %w", certName, err)
		}
		cert.Spec = desiredSpec
		if err := upstreamClient.Create(ctx, cert); err != nil {
			return nil, tlsCertificateSettled, fmt.Errorf("failed to create TLSCertificate %s: %w", certName, err)
		}
		logger.Info("TLSCertificate requested", "tlscertificate", certName, "hostname", hostname)
		return cert, tlsCertificateSettled, nil
	case err != nil:
		return nil, tlsCertificateSettled, fmt.Errorf("failed to get TLSCertificate %s: %w", certName, err)
	}

	if !metav1.IsControlledBy(cert, upstreamGateway) {
		return nil, tlsCertificateSettled, fmt.Errorf("%w: %s", errTLSCertificateNotOwned, certName)
	}

	if !cert.DeletionTimestamp.IsZero() {
		return cert, tlsCertificateReplacing, nil
	}

	if !slices.Equal(cert.Spec.DNSNames, desiredSpec.DNSNames) {
		logger.Info("TLSCertificate no longer matches its listener, requesting it again", "tlscertificate", certName, "hostname", hostname)
		if err := upstreamClient.Delete(ctx, cert, client.Preconditions{UID: &cert.UID}); client.IgnoreNotFound(err) != nil {
			return nil, tlsCertificateSettled, fmt.Errorf("failed to delete TLSCertificate %s: %w", certName, err)
		}
		return cert, tlsCertificateReplacing, nil
	}

	if cert.Spec.Issuance == desiredSpec.Issuance {
		return cert, tlsCertificateSettled, nil
	}

	cert.Spec.Issuance = desiredSpec.Issuance
	if err := upstreamClient.Update(ctx, cert); err != nil {
		return nil, tlsCertificateSettled, fmt.Errorf("failed to update TLSCertificate %s: %w", certName, err)
	}
	logger.Info("TLSCertificate reconciled", "tlscertificate", certName, "operation", "updated")
	return cert, tlsCertificateSettled, nil
}

// mirrorTLSCertificateSecret copies the service-side Secret holding the issued
// key pair, read with the operator's own credentials from the configured
// service namespace under the name the TLSCertificate's UID derives, into the
// downstream gateway namespace under the listener's secret name, stamped with
// the upstream-owner labels the federation policy selects. The material is
// parsed, matched, checked against the hostname, its expiry and, when
// configured, the trusted roots before it may replace what is serving.
func (r *GatewayReconciler) mirrorTLSCertificateSecret(
	ctx context.Context,
	downstreamStrategy downstreamclient.ResourceStrategy,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	cert *certificatesv1alpha1.TLSCertificate,
	secretName string,
	hostname string,
	now time.Time,
) (bool, *certificateServiceIssue, error) {
	logger := log.FromContext(ctx)

	if !apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) {
		return false, nil, nil
	}
	if r.CertificateServiceReader == nil {
		return false, nil, fmt.Errorf("certificate service enabled without a client for its cluster")
	}
	roots, err := r.certificateServiceRoots()
	if err != nil {
		return false, nil, fmt.Errorf("failed to load trusted roots: %w", err)
	}

	downstreamClient := downstreamStrategy.GetClient()
	mirror := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamGateway.Namespace,
			Name:      secretName,
		},
	}
	if err := downstreamClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror); client.IgnoreNotFound(err) != nil {
		return false, nil, fmt.Errorf("failed to get Secret %s: %w", secretName, err)
	}
	if mirrorHoldsIssuance(mirror, cert) {
		return false, nil, nil
	}

	var source corev1.Secret
	sourceKey := client.ObjectKey{
		Namespace: r.Config.Gateway.CertificateService.SecretNamespace,
		Name:      certificatesv1alpha1.StoredSecretName(cert.UID),
	}
	if err := r.CertificateServiceReader.Get(ctx, sourceKey, &source); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TLSCertificate is Ready but its service-side Secret is not readable yet", "tlscertificate", cert.Name, "secret", sourceKey)
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("failed to get service-side Secret %s for TLSCertificate %s: %w", sourceKey, cert.Name, err)
	}

	if err := validateIssuedMaterial(source.Data["tls.crt"], source.Data["tls.key"], hostname, now); err != nil {
		logger.Info("refusing service-side Secret that does not hold a usable certificate for the hostname",
			"tlscertificate", cert.Name, "secret", sourceKey, "reason", err.Error())
		return false, &certificateServiceIssue{reason: certificateServiceReasonMaterialRefused, message: certificateMaterialRefusedMessage(hostname)}, nil
	}
	if roots != nil {
		if err := verifyIssuedChain(source.Data["tls.crt"], hostname, roots, now); err != nil {
			logger.Info("refusing service-side Secret whose chain is not trusted",
				"tlscertificate", cert.Name, "secret", sourceKey, "reason", err.Error())
			return false, &certificateServiceIssue{reason: certificateServiceReasonUntrustedChain, message: certificateMaterialRefusedMessage(hostname)}, nil
		}
	}

	op, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, mirror, func() error {
		if mirror.CreationTimestamp.IsZero() {
			mirror.Type = corev1.SecretTypeTLS
		}
		if err := downstreamStrategy.SetControllerReference(ctx, upstreamGateway, mirror); err != nil {
			return fmt.Errorf("failed to set strategy reference on Secret %s: %w", secretName, err)
		}
		mirror.Data = map[string][]byte{
			"tls.crt": source.Data["tls.crt"],
			"tls.key": source.Data["tls.key"],
		}
		if ca := source.Data["ca.crt"]; len(ca) > 0 {
			mirror.Data["ca.crt"] = ca
		}
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("failed to mirror Secret %s: %w", secretName, err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("issued Secret mirrored downstream", "secret", secretName, "operation", op)
	}

	return op != controllerutil.OperationResultNone, nil, nil
}

// certificateServiceRoots returns the roots an issued chain must verify
// against, or nil when chain verification is off.
func (r *GatewayReconciler) certificateServiceRoots() (*x509.CertPool, error) {
	if !r.Config.Gateway.CertificateService.VerifyChain {
		return nil, nil
	}
	if r.CertificateServiceRoots != nil {
		return r.CertificateServiceRoots, nil
	}
	return x509.SystemCertPool()
}

// mirrorHoldsIssuance reports whether the downstream Secret already carries the
// issuance the TLSCertificate describes, so the service cluster is not read
// again for it.
func mirrorHoldsIssuance(mirror *corev1.Secret, cert *certificatesv1alpha1.TLSCertificate) bool {
	if mirror.CreationTimestamp.IsZero() || cert.Status.NotAfter == nil {
		return false
	}
	leaf, err := parseLeafCertificate(mirror.Data["tls.crt"], mirror.Data["tls.key"])
	if err != nil {
		return false
	}
	return leaf.NotAfter.Truncate(time.Second).Equal(cert.Status.NotAfter.Truncate(time.Second))
}

func parseLeafCertificate(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	keyPair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if keyPair.Leaf != nil {
		return keyPair.Leaf, nil
	}
	return x509.ParseCertificate(keyPair.Certificate[0])
}

// validateIssuedMaterial accepts only a matching key pair whose leaf covers the
// hostname and is valid now.
func validateIssuedMaterial(certPEM, keyPEM []byte, hostname string, now time.Time) error {
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return fmt.Errorf("secret holds no certificate material")
	}
	leaf, err := parseLeafCertificate(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("certificate and key do not form a key pair: %w", err)
	}
	if err := leaf.VerifyHostname(hostname); err != nil {
		return fmt.Errorf("certificate does not cover %s: %w", hostname, err)
	}
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("certificate is not valid until %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if !leaf.NotAfter.After(now.Add(listenerCertExpiryMargin)) {
		return fmt.Errorf("certificate expired at %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

// verifyIssuedChain checks that the PEM chain, leaf first, builds to one of
// the trusted roots for server authentication on the hostname.
func verifyIssuedChain(certPEM []byte, hostname string, roots *x509.CertPool, now time.Time) error {
	var chain []*x509.Certificate
	for rest := certPEM; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse certificate chain: %w", err)
		}
		chain = append(chain, c)
	}
	if len(chain) == 0 {
		return fmt.Errorf("secret holds no certificate")
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{
		DNSName:       hostname,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

// deleteStaleTLSCertificates removes TLSCertificates this gateway controls that
// no listener wants any more. Ownership is the controller reference's UID, so a
// namesake another gateway owns is left alone.
func (r *GatewayReconciler) deleteStaleTLSCertificates(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	desiredCerts sets.Set[string],
) error {
	logger := log.FromContext(ctx)

	var certs certificatesv1alpha1.TLSCertificateList
	if err := upstreamClient.List(ctx, &certs, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		return fmt.Errorf("failed to list TLSCertificates: %w", err)
	}

	for i := range certs.Items {
		cert := &certs.Items[i]
		if desiredCerts.Has(cert.Name) || !metav1.IsControlledBy(cert, upstreamGateway) || !cert.DeletionTimestamp.IsZero() {
			continue
		}
		logger.Info("deleting stale TLSCertificate", "tlscertificate", cert.Name)
		if err := upstreamClient.Delete(ctx, cert, client.Preconditions{UID: &cert.UID}); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete stale TLSCertificate %s: %w", cert.Name, err)
		}
	}

	return nil
}

// servingSecretHealth is listenerSecretHealth plus a check that the leaf
// actually covers the hostname, so a listener whose hostname changed cannot
// keep serving the previous name's certificate. It also returns the served
// leaf when there is one.
func servingSecretHealth(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamNamespace string,
	secretName string,
	hostname string,
	now time.Time,
) (listenerCertStatus, *x509.Certificate) {
	status := listenerSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, now)
	if !status.healthy {
		return status, nil
	}
	var secret corev1.Secret
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: secretName}, &secret); err != nil {
		return listenerCertStatus{reason: gatewayv1.ListenerReasonInvalidCertificateRef, message: certMissingMessage(hostname), pending: true, secretName: secretName}, nil
	}
	if err := validateIssuedMaterial(secret.Data["tls.crt"], secret.Data["tls.key"], hostname, now); err != nil {
		return listenerCertStatus{reason: gatewayv1.ListenerReasonInvalidCertificateRef, message: certMissingMessage(hostname), secretName: secretName}, nil
	}
	leaf, err := parseLeafCertificate(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil {
		return status, nil
	}
	return status, leaf
}

// renewalOverdue reports whether a served certificate is well past the point
// it should have been replaced, whatever the TLSCertificate's status says.
func renewalOverdue(leaf *x509.Certificate, now time.Time) bool {
	if leaf == nil {
		return false
	}
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(now) < lifetime/renewalOverdueDivisor
}

// tlsCertificateFailure says why a TLSCertificate is not producing a usable
// certificate, as a counter reason and the service's own explanation, or ""
// while it is healthy, still within its grace, or waiting on DNS records the
// customer has to publish.
func tlsCertificateFailure(cert *certificatesv1alpha1.TLSCertificate, now time.Time) (string, string) {
	if accepted := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionAccepted); accepted != nil && accepted.Status == metav1.ConditionFalse {
		return certificateServiceReasonRejected, accepted.Message
	}
	if !cert.DeletionTimestamp.IsZero() {
		return "", ""
	}
	if issuing := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionIssuing); issuing != nil &&
		issuing.Status == metav1.ConditionFalse && issuing.Reason == "IssuanceFailed" {
		return certificateServiceReasonIssuanceFailed, issuing.Message
	}
	ready := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionReady)
	if ready != nil && ready.Status == metav1.ConditionTrue {
		return "", ""
	}
	if delegation := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionDNSDelegationReady); delegation != nil && delegation.Status == metav1.ConditionFalse {
		return "", ""
	}
	since := cert.CreationTimestamp.Time
	detail := ""
	if ready != nil {
		since = ready.LastTransitionTime.Time
		detail = ready.Message
	}
	if since.IsZero() || now.Sub(since) < tlsCertificateIssueGrace {
		return "", ""
	}
	return certificateServiceReasonNotReady, detail
}

// listenerTLSCertificateHealth keeps a listener serving whenever its downstream
// Secret holds a usable certificate, whatever the TLSCertificate is doing. A
// replacement that is failing, stalled or overdue is reported as a blocked
// renewal while the listener serves, and as blocked issuance when nothing does,
// and counted once per transition.
func (r *GatewayReconciler) listenerTLSCertificateHealth(
	ctx context.Context,
	upstreamClient client.Client,
	downstreamClient client.Client,
	downstreamNamespace string,
	upstreamGateway *gatewayv1.Gateway,
	listenerName gatewayv1.SectionName,
	hostname string,
	now time.Time,
) listenerCertStatus {
	logger := log.FromContext(ctx)

	certName := tlsCertificateName(upstreamGateway.Name, listenerName)
	secretName := listenerCertificateSecretName(upstreamGateway.Name, listenerName)

	secretStatus, leaf := servingSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, now)

	var cert certificatesv1alpha1.TLSCertificate
	if err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: certName}, &cert); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get listener TLSCertificate", "tlscertificate", certName)
		}
		reason := ""
		if secretStatus.healthy && renewalOverdue(leaf, now) {
			reason = certificateServiceReasonRenewalOverdue
			secretStatus.renewalBlocked = certificateRenewalOverdueMessage(hostname, leaf.NotAfter)
		}
		r.recordCertificateServiceState(upstreamGateway, listenerName, reason)
		if secretStatus.healthy {
			return secretStatus
		}
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    certIssuanceFailingMessage(hostname),
			pending:    true,
			secretName: secretName,
		}
	}

	reason, detail := tlsCertificateFailure(&cert, now)
	if reason == "" && secretStatus.healthy && renewalOverdue(leaf, now) {
		reason = certificateServiceReasonRenewalOverdue
	}
	r.recordCertificateServiceState(upstreamGateway, listenerName, reason)

	if secretStatus.healthy {
		switch reason {
		case "":
		case certificateServiceReasonRenewalOverdue:
			secretStatus.renewalBlocked = certificateRenewalOverdueMessage(hostname, leaf.NotAfter)
		case certificateServiceReasonRejected:
			secretStatus.renewalBlocked = tlsCertificateRejectedMessage(hostname, detail)
		default:
			secretStatus.renewalBlocked = certificateNotIssuedMessage(hostname, detail)
		}
		return secretStatus
	}

	switch reason {
	case certificateServiceReasonRejected:
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    tlsCertificateRejectedMessage(hostname, detail),
			secretName: secretName,
		}
	case certificateServiceReasonIssuanceFailed, certificateServiceReasonNotReady:
		return listenerCertStatus{
			reason:          gatewayv1.ListenerReasonInvalidCertificateRef,
			message:         certificateNotIssuedMessage(hostname, detail),
			pending:         true,
			issuanceBlocked: true,
			secretName:      secretName,
		}
	}

	if !apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) {
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    certIssuanceFailingMessage(hostname),
			pending:    true,
			secretName: secretName,
		}
	}

	if cert.Status.NotBefore != nil && cert.Status.NotBefore.After(now) {
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    certNotYetValidMessage(hostname),
			secretName: secretName,
		}
	}
	if cert.Status.NotAfter != nil && !cert.Status.NotAfter.After(now.Add(listenerCertExpiryMargin)) {
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    certExpiredMessage(hostname),
			notAfter:   cert.Status.NotAfter,
			secretName: secretName,
		}
	}

	secretStatus.pending = true
	return secretStatus
}

func tlsCertificateRejectedMessage(hostname, detail string) string {
	if detail == "" {
		return certIssuanceFailingMessage(hostname)
	}
	return fmt.Sprintf("We couldn't issue a TLS certificate for %s, so HTTPS for this hostname is unavailable: %s", hostname, detail)
}

// listGatewaysForTLSCertificateFunc enqueues the Gateway that controls a
// TLSCertificate, in the cluster the TLSCertificate lives in.
func (r *GatewayReconciler) listGatewaysForTLSCertificateFunc(clusterName multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
		owner := metav1.GetControllerOf(obj)
		if owner == nil || owner.Kind != KindGateway {
			return nil
		}
		return []mcreconcile.Request{{
			ClusterName: clusterName,
			Request: reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: owner.Name},
			},
		}}
	})
}
