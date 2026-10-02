// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
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

const tlsCertificateSolverLabel = "networking.datumapis.com/tlscertificate-solver"

// tlsCertificateManagedLabel marks a downstream Secret the certificate service
// path owns: either handed over from cert-manager at the switch or written by
// the mirror. The cert-manager path reads it to avoid reissuing on rollback.
const tlsCertificateManagedLabel = "networking.datumapis.com/certificate-service"

const KindTLSCertificate = "TLSCertificate"

// tlsCertificateMirrorAdmitDelay is how soon the listener is re-evaluated after
// its Secret lands downstream, since listener health was judged before the
// mirror in the same pass.
const tlsCertificateMirrorAdmitDelay = time.Second

// tlsCertificateSwitchLead is how far ahead of cert-manager's own renewal time
// a hostname moves to the service, so the two never order for the same name.
const tlsCertificateSwitchLead = 48 * time.Hour

const (
	certificateServiceBackoffBase = 5 * time.Second
	certificateServiceBackoffMax  = 5 * time.Minute
	legacyRecheckMax              = 6 * time.Hour
)

var acmeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

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

func tlsCertificateSolverName(certName, token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%s-acme-%s", certName, hex.EncodeToString(sum[:])[:10])
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

// certificateServiceBackoff is what the step remembers about a gateway whose
// last pass failed: how many in a row, when it may try again, and what it told
// the listeners, so an event-driven reconcile inside the window repeats the
// message rather than the calls.
type certificateServiceBackoff struct {
	attempts  int
	nextTry   time.Time
	message   string
	listeners []gatewayv1.SectionName
}

// certificateServiceRequeue records the outcome of a pass and hands back the
// delay before the next attempt, doubling per consecutive failure up to a cap,
// and forgets the gateway once a pass succeeds.
func (r *GatewayReconciler) certificateServiceRequeue(gateway types.UID, failed bool, now time.Time, issues map[gatewayv1.SectionName]string) time.Duration {
	if !failed {
		r.certificateServiceFailures.Delete(gateway)
		return 0
	}
	attempts := 1
	if previous, ok := r.certificateServiceFailures.Load(gateway); ok {
		attempts = previous.(certificateServiceBackoff).attempts + 1
	}
	delay := certificateServiceBackoffBase << (attempts - 1)
	if attempts > 10 || delay > certificateServiceBackoffMax {
		delay = certificateServiceBackoffMax
	}
	backoff := certificateServiceBackoff{attempts: attempts, nextTry: now.Add(delay)}
	for listener, message := range issues {
		backoff.listeners = append(backoff.listeners, listener)
		backoff.message = message
	}
	r.certificateServiceFailures.Store(gateway, backoff)
	return delay
}

// certificateServiceInBackoff reports whether the gateway's last failure is
// still cooling off, and if so the listeners it concerned and how long is left.
func (r *GatewayReconciler) certificateServiceInBackoff(gateway types.UID, now time.Time) (certificateServiceBackoff, time.Duration, bool) {
	previous, ok := r.certificateServiceFailures.Load(gateway)
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

const (
	certificateServiceReasonStepFailed       = "StepFailed"
	certificateServiceReasonNotOwned         = "NotOwned"
	certificateServiceReasonRejected         = "Rejected"
	certificateServiceReasonNamespaceRefused = "NamespaceRefused"
	certificateServiceReasonMaterialRefused  = "MaterialRefused"
	certificateServiceReasonTokenRefused     = "TokenRefused"
)

func certificateServiceUnavailableMessage(hostname string) string {
	return fmt.Sprintf("We couldn't request a TLS certificate for %s just now and will keep trying. HTTPS for this hostname stays as it is in the meantime.", hostname)
}

func certificateBeingReplacedMessage(hostname string) string {
	return fmt.Sprintf("The TLS certificate request for %s is being replaced. HTTPS for this hostname stays as it is in the meantime.", hostname)
}

// ensureListenerTLSCertificates is the certificate-service counterpart of
// ensureListenerCertificates: one TLSCertificate per custom hostname in the
// project control plane, its HTTP-01 challenges answered on the downstream
// gateway, and its issued Secret mirrored downstream under the name the
// listener references. Nothing here fails the gateway reconcile: a listener
// whose step failed keeps whatever Secret it has, the failure is returned as a
// message for its status, and the whole step retries with backoff.
func (r *GatewayReconciler) ensureListenerTLSCertificates(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	downstreamClient client.Client,
	downstreamStrategy downstreamclient.ResourceStrategy,
	claimedHostnames []string,
) (result Result, issues map[gatewayv1.SectionName]string) {
	logger := log.FromContext(ctx)
	now := time.Now()
	issues = make(map[gatewayv1.SectionName]string)
	desiredCerts := sets.New[string]()
	legacyKeep := sets.New[string]()
	liveSolvers := sets.New[string]()
	failed := false

	requeueSooner := func(d time.Duration) {
		if d > 0 && (result.RequeueAfter == 0 || d < result.RequeueAfter) {
			result.RequeueAfter = d
		}
	}
	fail := func(l gatewayv1.Listener, hostname string, err error) {
		logger.Error(err, "certificate service step failed", "listener", l.Name, "hostname", hostname)
		issues[l.Name] = certificateServiceUnavailableMessage(hostname)
		recordCertificateServiceFailure(upstreamGateway, l.Name, certificateServiceReasonStepFailed)
		failed = true
	}

	if backoff, remaining, cooling := r.certificateServiceInBackoff(upstreamGateway.UID, now); cooling {
		for _, listener := range backoff.listeners {
			issues[listener] = backoff.message
		}
		requeueSooner(remaining)
		return result, issues
	}

	for _, l := range upstreamGateway.Spec.Listeners {
		hostname, wanted := r.listenerWantsOwnCertificate(l, claimedHostnames)
		if !wanted || !r.listenerIssuerResolvable(upstreamGateway, l) {
			continue
		}

		legacyName := listenerCertificateName(upstreamGateway.Name, l.Name)
		certName := tlsCertificateName(upstreamGateway.Name, l.Name)
		secretName := listenerCertificateSecretName(upstreamGateway.Name, l.Name)

		legacy, err := r.ownedLegacyCertificate(ctx, downstreamClient, upstreamGateway, downstreamGateway, legacyName)
		if err != nil {
			fail(l, hostname, err)
			continue
		}
		if legacy != nil {
			if holds, recheck := legacyCertificateHolds(legacy, now); holds {
				legacyKeep.Insert(legacyName)
				requeueSooner(min(recheck, legacyRecheckMax))
				continue
			}
			logger.Info("legacy certificate nearing renewal, switching hostname to the certificate service", "certificate", legacyName, "hostname", hostname)
			if err := r.retireLegacyCertificate(ctx, downstreamStrategy, upstreamGateway, legacy, secretName); err != nil {
				fail(l, hostname, err)
				continue
			}
		}

		desiredCerts.Insert(certName)

		cert, state, err := r.ensureTLSCertificate(ctx, upstreamClient, upstreamGateway, certName, secretName, hostname)
		if err != nil {
			if errors.Is(err, errTLSCertificateNotOwned) {
				recordCertificateServiceFailure(upstreamGateway, l.Name, certificateServiceReasonNotOwned)
			}
			fail(l, hostname, err)
			continue
		}
		if state != tlsCertificateSettled {
			issues[l.Name] = certificateBeingReplacedMessage(hostname)
			failed = true
			continue
		}

		solvers, rejected, err := r.serveTLSCertificateChallenges(ctx, downstreamClient, downstreamGateway, cert, hostname)
		if err != nil {
			fail(l, hostname, err)
			continue
		}
		liveSolvers.Insert(solvers...)
		if rejected != "" {
			issues[l.Name] = rejected
			recordCertificateServiceFailure(upstreamGateway, l.Name, certificateServiceReasonTokenRefused)
		}

		mirrored, refusal, err := r.mirrorTLSCertificateSecret(ctx, downstreamStrategy, upstreamGateway, downstreamGateway, cert, secretName, hostname, now)
		if err != nil {
			fail(l, hostname, err)
			continue
		}
		if refusal.reason != "" {
			issues[l.Name] = refusal.message
			recordCertificateServiceFailure(upstreamGateway, l.Name, refusal.reason)
		}
		if mirrored {
			requeueSooner(tlsCertificateMirrorAdmitDelay)
		}
	}

	if err := r.deleteStaleTLSCertificates(ctx, upstreamClient, upstreamGateway, desiredCerts); err != nil {
		logger.Error(err, "failed to clean up TLSCertificates")
		failed = true
	}
	if err := r.deleteStaleLegacyCertificates(ctx, downstreamClient, upstreamGateway, downstreamGateway, legacyKeep); err != nil {
		logger.Error(err, "failed to clean up legacy Certificates")
		failed = true
	}
	if err := r.deleteStaleTLSCertificateSolvers(ctx, downstreamClient, downstreamGateway, liveSolvers); err != nil {
		logger.Error(err, "failed to clean up HTTP-01 solvers")
		failed = true
	}

	requeueSooner(r.certificateServiceRequeue(upstreamGateway.UID, failed, now, issues))

	return result, issues
}

var errTLSCertificateNotOwned = errors.New("TLSCertificate exists but is not controlled by this Gateway")

// mirrorRefusal says why issued material was not taken, for the customer and
// for the failure counter.
type mirrorRefusal struct {
	reason  string
	message string
}

func (m mirrorRefusal) String() string { return m.reason }

type tlsCertificateState int

const (
	tlsCertificateSettled tlsCertificateState = iota
	tlsCertificateReplacing
)

// ensureTLSCertificate creates the listener's TLSCertificate or confirms the
// existing one. dnsNames and secretName are immutable on the service's API, so
// one of ours that no longer matches is deleted and requested again once it is
// gone. One this gateway does not control is never touched.
func (r *GatewayReconciler) ensureTLSCertificate(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	certName, secretName, hostname string,
) (*certificatesv1alpha1.TLSCertificate, tlsCertificateState, error) {
	logger := log.FromContext(ctx)

	desiredSpec := certificatesv1alpha1.TLSCertificateSpec{
		DNSNames:   []certificatesv1alpha1.DNSName{certificatesv1alpha1.DNSName(hostname)},
		Issuance:   certificatesv1alpha1.IssuanceModeAuto,
		SecretName: secretName,
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

	if !slices.Equal(cert.Spec.DNSNames, desiredSpec.DNSNames) || cert.Spec.SecretName != desiredSpec.SecretName {
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

// serveTLSCertificateChallenges publishes every pending HTTP-01 challenge the
// service reports for the hostname this listener claimed and returns the solver
// names for every such challenge still in status, so a challenge that has moved
// on to Valid keeps its answer until the service drops it. A challenge for any
// other name, or with a token that is not an ACME token, is not ours to answer,
// whatever status says.
func (r *GatewayReconciler) serveTLSCertificateChallenges(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamGateway *gatewayv1.Gateway,
	cert *certificatesv1alpha1.TLSCertificate,
	hostname string,
) (live []string, rejected string, err error) {
	logger := log.FromContext(ctx)

	for _, challenge := range cert.Status.Challenges {
		if challenge.Type != certificatesv1alpha1.ChallengeTypeHTTP01 {
			continue
		}
		if challenge.DNSName != hostname {
			logger.Info("ignoring HTTP-01 challenge for a hostname this listener did not claim",
				"tlscertificate", cert.Name, "claimed", hostname, "challenge_dns_name", challenge.DNSName)
			continue
		}
		if !acmeTokenPattern.MatchString(challenge.Token) {
			logger.Info("ignoring HTTP-01 challenge whose token is not an ACME token", "tlscertificate", cert.Name, "hostname", hostname)
			rejected = certificateServiceUnavailableMessage(hostname)
			continue
		}
		name := tlsCertificateSolverName(cert.Name, challenge.Token)
		live = append(live, name)

		if challenge.State != certificatesv1alpha1.ChallengeStatePending {
			continue
		}

		err := ensureHTTP01SolverRoutes(ctx, downstreamClient, downstreamClient.Scheme(), downstreamGateway, downstreamGateway, http01SolverRoute{
			name:      name,
			token:     challenge.Token,
			key:       challenge.Key,
			hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			labels:    map[string]string{tlsCertificateSolverLabel: cert.Name},
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to serve HTTP-01 challenge for TLSCertificate %s: %w", cert.Name, err)
		}
	}

	return live, rejected, nil
}

// mirrorTLSCertificateSecret copies the service-side issued Secret, read with
// the operator's own credentials from the configured service namespace, into
// the downstream gateway namespace under the listener's secret name, stamped
// with the upstream-owner labels the federation policy selects. The material is
// parsed, matched, checked against the hostname and its expiry before it may
// replace what is serving; the project-namespace copy is never read.
func (r *GatewayReconciler) mirrorTLSCertificateSecret(
	ctx context.Context,
	downstreamStrategy downstreamclient.ResourceStrategy,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	cert *certificatesv1alpha1.TLSCertificate,
	secretName string,
	hostname string,
	now time.Time,
) (mirrored bool, refusal mirrorRefusal, err error) {
	logger := log.FromContext(ctx)

	if !apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) {
		return false, mirrorRefusal{}, nil
	}
	ref := cert.Status.ServiceSecretRef
	if ref == nil || ref.Name == "" {
		logger.Info("TLSCertificate is Ready without a service-side Secret reference", "tlscertificate", cert.Name)
		return false, mirrorRefusal{}, nil
	}
	if ref.Namespace != r.Config.Gateway.CertificateService.SecretNamespace {
		logger.Info("refusing service-side Secret outside the certificate service namespace",
			"tlscertificate", cert.Name, "namespace", ref.Namespace, "expected", r.Config.Gateway.CertificateService.SecretNamespace)
		return false, mirrorRefusal{reason: certificateServiceReasonNamespaceRefused, message: certificateServiceUnavailableMessage(hostname)}, nil
	}
	if r.CertificateServiceReader == nil {
		return false, mirrorRefusal{}, fmt.Errorf("certificate service enabled without a client for its cluster")
	}

	downstreamClient := downstreamStrategy.GetClient()
	mirror := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamGateway.Namespace,
			Name:      secretName,
		},
	}
	if err := downstreamClient.Get(ctx, client.ObjectKeyFromObject(mirror), mirror); client.IgnoreNotFound(err) != nil {
		return false, mirrorRefusal{}, fmt.Errorf("failed to get Secret %s: %w", secretName, err)
	}
	if mirrorHoldsIssuance(mirror, cert) {
		return false, mirrorRefusal{}, nil
	}

	var source corev1.Secret
	sourceKey := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	if err := r.CertificateServiceReader.Get(ctx, sourceKey, &source); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TLSCertificate is Ready but its service-side Secret is not readable yet", "tlscertificate", cert.Name, "secret", sourceKey)
			return false, mirrorRefusal{}, nil
		}
		return false, mirrorRefusal{}, fmt.Errorf("failed to get service-side Secret %s for TLSCertificate %s: %w", sourceKey, cert.Name, err)
	}

	if err := validateIssuedMaterial(source.Data["tls.crt"], source.Data["tls.key"], hostname, now); err != nil {
		logger.Info("refusing service-side Secret that does not hold a usable certificate for the hostname",
			"tlscertificate", cert.Name, "secret", sourceKey, "reason", err.Error())
		return false, mirrorRefusal{reason: certificateServiceReasonMaterialRefused, message: certificateServiceUnavailableMessage(hostname)}, nil
	}

	op, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, mirror, func() error {
		if mirror.CreationTimestamp.IsZero() {
			mirror.Type = corev1.SecretTypeTLS
		}
		if err := downstreamStrategy.SetControllerReference(ctx, upstreamGateway, mirror); err != nil {
			return fmt.Errorf("failed to set strategy reference on Secret %s: %w", secretName, err)
		}
		mirror.Labels[tlsCertificateManagedLabel] = labelValueTrue
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
		return false, mirrorRefusal{}, fmt.Errorf("failed to mirror Secret %s: %w", secretName, err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("issued Secret mirrored downstream", "secret", secretName, "operation", op)
	}

	return op != controllerutil.OperationResultNone, mirrorRefusal{}, nil
}

// mirrorHoldsIssuance reports whether the downstream Secret already carries the
// issuance the TLSCertificate describes, so the service cluster is not read
// again for it.
func mirrorHoldsIssuance(mirror *corev1.Secret, cert *certificatesv1alpha1.TLSCertificate) bool {
	if mirror.CreationTimestamp.IsZero() || mirror.Labels[tlsCertificateManagedLabel] != labelValueTrue || cert.Status.NotAfter == nil {
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

// ownedLegacyCertificate returns the listener's cert-manager Certificate from
// the downstream cluster when one exists and belongs to this gateway.
func (r *GatewayReconciler) ownedLegacyCertificate(
	ctx context.Context,
	downstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	certName string,
) (*cmv1.Certificate, error) {
	var legacy cmv1.Certificate
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamGateway.Namespace, Name: certName}, &legacy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Certificate %s: %w", certName, err)
	}
	if !certificateOwnedByGateway(&legacy, upstreamGateway, downstreamGateway) {
		return nil, nil
	}
	return &legacy, nil
}

// legacyHoldWhileIssuing bounds how long a cert-manager renewal in flight can
// defer the hand-over; past it the renewal is taken as stuck.
const legacyHoldWhileIssuing = 24 * time.Hour

// legacyHandOverFloor is the remaining lifetime below which the hand-over goes
// ahead whatever cert-manager is doing, so the service has time to issue.
const legacyHandOverFloor = 7 * 24 * time.Hour

// legacyCertificateHolds reports whether a cert-manager Certificate should keep
// its hostname for now: it is serving and not yet within the switch lead of its
// renewal time, or cert-manager is mid-renewal and must not be interrupted. A
// renewal that has failed, has been in flight for too long, or has too little
// lifetime left to wait for does not hold. The returned duration is when to
// look again.
func legacyCertificateHolds(cert *cmv1.Certificate, now time.Time) (bool, time.Duration) {
	if !certIsServing(cert, now) {
		return false, 0
	}
	if cert.Status.NotAfter != nil && cert.Status.NotAfter.Sub(now) < legacyHandOverFloor {
		return false, 0
	}
	for _, c := range cert.Status.Conditions {
		if c.Type != cmv1.CertificateConditionIssuing || c.Status != cmmeta.ConditionTrue {
			continue
		}
		if cert.Status.LastFailureTime != nil {
			return false, 0
		}
		if !c.LastTransitionTime.IsZero() && now.Sub(c.LastTransitionTime.Time) > legacyHoldWhileIssuing {
			return false, 0
		}
		return true, 10 * time.Minute
	}
	if cert.Status.RenewalTime == nil {
		return false, 0
	}
	switchAt := cert.Status.RenewalTime.Add(-tlsCertificateSwitchLead)
	if now.Before(switchAt) {
		return true, switchAt.Sub(now)
	}
	return false, 0
}

// retireLegacyCertificate hands the listener over to the service: the Secret
// gains the gateway's own owner so cert-manager's owner reference no longer
// takes it along and the hand-over label the cert-manager path honours on
// rollback, then the Certificate goes so cert-manager does not renew a
// hostname the service is about to issue for.
func (r *GatewayReconciler) retireLegacyCertificate(
	ctx context.Context,
	downstreamStrategy downstreamclient.ResourceStrategy,
	upstreamGateway *gatewayv1.Gateway,
	legacy *cmv1.Certificate,
	secretName string,
) error {
	downstreamClient := downstreamStrategy.GetClient()

	var secret corev1.Secret
	err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: legacy.Namespace, Name: secretName}, &secret)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("failed to get Secret %s: %w", secretName, err)
	default:
		if err := downstreamStrategy.SetControllerReference(ctx, upstreamGateway, &secret); err != nil {
			return fmt.Errorf("failed to set strategy reference on Secret %s: %w", secretName, err)
		}
		secret.Labels[tlsCertificateManagedLabel] = labelValueTrue
		if err := downstreamClient.Update(ctx, &secret); err != nil {
			return fmt.Errorf("failed to keep Secret %s past its Certificate: %w", secretName, err)
		}
	}

	if err := downstreamClient.Delete(ctx, legacy, client.Preconditions{UID: &legacy.UID}); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete legacy Certificate %s: %w", legacy.Name, err)
	}
	return nil
}

// deleteStaleLegacyCertificates removes the gateway's cert-manager Certificates
// for listeners that no longer carry one, mirroring what the cert-manager path
// does for removed hostnames.
func (r *GatewayReconciler) deleteStaleLegacyCertificates(
	ctx context.Context,
	downstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	legacyKeep sets.Set[string],
) error {
	logger := log.FromContext(ctx)

	var certList cmv1.CertificateList
	if err := downstreamClient.List(ctx, &certList, client.InNamespace(downstreamGateway.Namespace)); err != nil {
		return fmt.Errorf("failed to list Certificates: %w", err)
	}

	for i := range certList.Items {
		cert := &certList.Items[i]
		if legacyKeep.Has(cert.Name) || !certificateOwnedByGateway(cert, upstreamGateway, downstreamGateway) {
			continue
		}
		logger.Info("deleting stale Certificate", "certificate", cert.Name)
		if err := downstreamClient.Delete(ctx, cert); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete stale Certificate %s: %w", cert.Name, err)
		}
	}

	return nil
}

func certificateOwnedByGateway(cert *cmv1.Certificate, upstreamGateway, downstreamGateway *gatewayv1.Gateway) bool {
	ownedByStrategy := cert.Labels[downstreamclient.UpstreamOwnerKindLabel] == KindGateway &&
		cert.Labels[downstreamclient.UpstreamOwnerNameLabel] == upstreamGateway.Name &&
		cert.Labels[downstreamclient.UpstreamOwnerNamespaceLabel] == upstreamGateway.Namespace
	return ownedByStrategy || metav1.IsControlledBy(cert, downstreamGateway)
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

func (r *GatewayReconciler) deleteStaleTLSCertificateSolvers(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamGateway *gatewayv1.Gateway,
	liveSolvers sets.Set[string],
) error {
	logger := log.FromContext(ctx)

	var routes gatewayv1.HTTPRouteList
	if err := downstreamClient.List(ctx, &routes,
		client.InNamespace(downstreamGateway.Namespace),
		client.HasLabels{tlsCertificateSolverLabel},
	); err != nil {
		return fmt.Errorf("failed to list TLSCertificate solver routes: %w", err)
	}

	for i := range routes.Items {
		route := &routes.Items[i]
		if liveSolvers.Has(route.Name) || !metav1.IsControlledBy(route, downstreamGateway) {
			continue
		}
		logger.Info("removing finished HTTP-01 solver", "solver", route.Name)
		if err := deleteHTTP01SolverRoutes(ctx, downstreamClient, route.Namespace, route.Name); err != nil {
			return err
		}
	}

	return nil
}

// cleanupCertificateServiceLeftovers runs on the cert-manager path for a
// gateway that was once on the service: it drops the solver routes and the
// TLSCertificates this gateway controls, so the service stops renewing names
// cert-manager has taken back. Nothing here fails the reconcile, and the
// project control plane is only asked about TLSCertificates when a handed-over
// Secret shows the gateway was ever on the service.
func (r *GatewayReconciler) cleanupCertificateServiceLeftovers(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	downstreamClient client.Client,
) {
	logger := log.FromContext(ctx)

	if err := r.deleteStaleTLSCertificateSolvers(ctx, downstreamClient, downstreamGateway, sets.New[string]()); err != nil {
		logger.Error(err, "failed to remove certificate service solvers after rollback")
	}

	var handedOver corev1.SecretList
	if err := downstreamClient.List(ctx, &handedOver,
		client.InNamespace(downstreamGateway.Namespace),
		client.MatchingLabels{
			tlsCertificateManagedLabel:                   labelValueTrue,
			downstreamclient.UpstreamOwnerKindLabel:      KindGateway,
			downstreamclient.UpstreamOwnerNameLabel:      upstreamGateway.Name,
			downstreamclient.UpstreamOwnerNamespaceLabel: upstreamGateway.Namespace,
		},
	); err != nil {
		logger.Error(err, "failed to list handed-over Secrets after rollback")
		return
	}
	if len(handedOver.Items) == 0 {
		return
	}

	if err := r.deleteStaleTLSCertificates(ctx, upstreamClient, upstreamGateway, sets.New[string]()); err != nil {
		logger.Error(err, "failed to remove TLSCertificates after rollback")
	}

	for i := range handedOver.Items {
		secret := &handedOver.Items[i]
		var legacy cmv1.Certificate
		if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: secret.Namespace, Name: secret.Name}, &legacy); err != nil || !certIsReady(&legacy) {
			continue
		}
		delete(secret.Labels, tlsCertificateManagedLabel)
		if err := downstreamClient.Update(ctx, secret); err != nil {
			logger.Error(err, "failed to clear hand-over marker after cert-manager retook the Secret", "secret", secret.Name)
		}
	}
}

// handedOverSecretHealth reports whether the listener's downstream Secret was
// handed to or written by the certificate service and still holds a usable
// certificate for the hostname. renewalDue says the certificate is in the last
// third of its life, where cert-manager would renew it anyway.
func handedOverSecretHealth(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamNamespace string,
	secretName string,
	hostname string,
	now time.Time,
) (status listenerCertStatus, handedOver bool, renewalDue bool) {
	var secret corev1.Secret
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: secretName}, &secret); err != nil {
		return listenerCertStatus{}, false, false
	}
	if secret.Labels[tlsCertificateManagedLabel] != labelValueTrue {
		return listenerCertStatus{}, false, false
	}
	leaf, err := parseLeafCertificate(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil || leaf.VerifyHostname(hostname) != nil || now.Before(leaf.NotBefore) || !leaf.NotAfter.After(now.Add(listenerCertExpiryMargin)) {
		return listenerCertStatus{}, true, true
	}
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	renewalDue = !now.Before(leaf.NotAfter.Add(-lifetime / 3))
	return listenerCertStatus{healthy: true, secretName: secretName, notAfter: &metav1.Time{Time: leaf.NotAfter}}, true, renewalDue
}

// evaluateListenerTLSCertificateHealth is evaluateListenerCertHealth for the
// certificate-service path: conditions come from the upstream TLSCertificate,
// the served material from the mirrored downstream Secret.
func (r *GatewayReconciler) evaluateListenerTLSCertificateHealth(
	ctx context.Context,
	upstreamClient client.Client,
	downstreamClient client.Client,
	downstreamNamespace string,
	upstreamGateway *gatewayv1.Gateway,
	claimedHostnames []string,
) map[gatewayv1.SectionName]listenerCertStatus {
	logger := log.FromContext(ctx)
	health := make(map[gatewayv1.SectionName]listenerCertStatus)
	now := time.Now()

	clearListenerCertMetrics(upstreamGateway.Namespace, upstreamGateway.Name)

	for _, l := range upstreamGateway.Spec.Listeners {
		hostname, wanted := r.listenerWantsOwnCertificate(l, claimedHostnames)
		if !wanted {
			continue
		}

		status := r.listenerTLSCertificateHealth(ctx, upstreamClient, downstreamClient, downstreamNamespace, upstreamGateway, l.Name, hostname, now)
		health[l.Name] = status
		recordListenerCertHealth(logger, upstreamGateway, l.Name, hostname, status)
	}

	return health
}

// servingSecretHealth is listenerSecretHealth plus a check that the leaf
// actually covers the hostname, so a listener whose hostname changed cannot
// keep serving the previous name's certificate.
func servingSecretHealth(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamNamespace string,
	secretName string,
	hostname string,
	now time.Time,
) listenerCertStatus {
	status := listenerSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, now)
	if !status.healthy {
		return status
	}
	var secret corev1.Secret
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: secretName}, &secret); err != nil {
		return listenerCertStatus{reason: gatewayv1.ListenerReasonInvalidCertificateRef, message: certMissingMessage(hostname), pending: true, secretName: secretName}
	}
	if err := validateIssuedMaterial(secret.Data["tls.crt"], secret.Data["tls.key"], hostname, now); err != nil {
		return listenerCertStatus{reason: gatewayv1.ListenerReasonInvalidCertificateRef, message: certMissingMessage(hostname), secretName: secretName}
	}
	return status
}

// listenerTLSCertificateHealth keeps a listener serving whenever its downstream
// Secret holds a usable certificate, whatever the TLSCertificate is doing: a
// renewal in flight or a first issuance replacing a legacy certificate must
// not take HTTPS down. Only when nothing usable is downstream do the
// TLSCertificate's conditions decide what the customer is told.
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

	secretStatus := servingSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, now)

	var cert certificatesv1alpha1.TLSCertificate
	if err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: certName}, &cert); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get listener TLSCertificate", "tlscertificate", certName)
		}
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

	rejected := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionAccepted)
	if rejected != nil && rejected.Status != metav1.ConditionFalse {
		rejected = nil
	}

	if secretStatus.healthy {
		if rejected != nil {
			secretStatus.renewalBlocked = tlsCertificateRejectedMessage(hostname, rejected.Message)
			recordCertificateServiceFailure(upstreamGateway, listenerName, certificateServiceReasonRejected)
		}
		return secretStatus
	}

	if accepted := rejected; accepted != nil {
		return listenerCertStatus{
			reason:     gatewayv1.ListenerReasonInvalidCertificateRef,
			message:    tlsCertificateRejectedMessage(hostname, accepted.Message),
			secretName: secretName,
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
