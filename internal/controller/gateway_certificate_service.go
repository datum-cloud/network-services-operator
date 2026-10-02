// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"go.datum.net/network-services-operator/internal/util/resourcename"
)

// +kubebuilder:rbac:groups=certificates.miloapis.com,resources=tlscertificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=certificates.miloapis.com,resources=tlscertificates/status,verbs=get

const tlsCertificateSolverLabel = "networking.datumapis.com/tlscertificate-solver"

const KindTLSCertificate = "TLSCertificate"

const tlsCertificateRecreateDelay = 5 * time.Second

// tlsCertificateMirrorAdmitDelay is how soon the listener is re-evaluated after
// its Secret lands downstream, since listener health was judged before the
// mirror in the same pass.
const tlsCertificateMirrorAdmitDelay = time.Second

// tlsCertificateName is the listener's TLSCertificate name. The service caps
// names at 63 characters, so a long gateway and listener pair is truncated and
// hashed where the cert-manager Certificate name was not.
func tlsCertificateName(gatewayName string, listenerName gatewayv1.SectionName) string {
	return resourcename.GetValidDNS1035Name(fmt.Sprintf("%s-%s", gatewayName, listenerName))
}

func tlsCertificateSolverName(certName, token string) string {
	sum := sha256.Sum256([]byte(token))
	return resourcename.GetValidDNS1123Name(fmt.Sprintf("%s-acme-%s", certName, hex.EncodeToString(sum[:])[:10]))
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

// ensureListenerTLSCertificates is the certificate-service counterpart of
// ensureListenerCertificates: one TLSCertificate per custom hostname in the
// project control plane, its HTTP-01 challenges answered on the downstream
// gateway, and its issued Secret mirrored downstream under the name the
// listener references.
func (r *GatewayReconciler) ensureListenerTLSCertificates(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	downstreamClient client.Client,
	downstreamStrategy downstreamclient.ResourceStrategy,
	claimedHostnames []string,
) (result Result) {
	logger := log.FromContext(ctx)
	now := time.Now()
	desiredCerts := sets.New[string]()
	legacyKeep := sets.New[string]()
	liveSolvers := sets.New[string]()

	for _, l := range upstreamGateway.Spec.Listeners {
		hostname, wanted := r.listenerWantsOwnCertificate(l, claimedHostnames)
		if !wanted {
			continue
		}

		legacyName := listenerCertificateName(upstreamGateway.Name, l.Name)
		certName := tlsCertificateName(upstreamGateway.Name, l.Name)
		secretName := listenerCertificateSecretName(upstreamGateway.Name, l.Name)

		legacy, err := r.ownedLegacyCertificate(ctx, downstreamClient, upstreamGateway, downstreamGateway, legacyName)
		if err != nil {
			result.Err = err
			return result
		}
		if legacy != nil && legacyCertificateHolds(legacy, now) {
			legacyKeep.Insert(legacyName)
			continue
		}
		if legacy != nil {
			logger.Info("legacy certificate due for renewal, switching hostname to the certificate service", "certificate", legacyName, "hostname", hostname)
			if err := r.retireLegacyCertificate(ctx, downstreamStrategy, upstreamGateway, legacy, secretName); err != nil {
				result.Err = err
				return result
			}
		}

		desiredCerts.Insert(certName)

		cert, recreating, err := r.ensureTLSCertificate(ctx, upstreamClient, upstreamGateway, certName, secretName, hostname)
		if err != nil {
			result.Err = err
			return result
		}
		if recreating {
			result.RequeueAfter = tlsCertificateRecreateDelay
			continue
		}

		solvers, err := r.serveTLSCertificateChallenges(ctx, downstreamClient, downstreamGateway, cert, hostname)
		if err != nil {
			result.Err = err
			return result
		}
		liveSolvers.Insert(solvers...)

		mirrored, err := r.mirrorTLSCertificateSecret(ctx, downstreamStrategy, upstreamGateway, downstreamGateway, cert, secretName)
		if err != nil {
			result.Err = err
			return result
		}
		if mirrored {
			result.RequeueAfter = tlsCertificateMirrorAdmitDelay
		}
	}

	if err := r.deleteStaleTLSCertificates(ctx, upstreamClient, upstreamGateway, desiredCerts); err != nil {
		result.Err = err
		return result
	}

	if err := r.deleteStaleLegacyCertificates(ctx, downstreamClient, upstreamGateway, downstreamGateway, legacyKeep); err != nil {
		result.Err = err
		return result
	}

	if err := r.deleteStaleTLSCertificateSolvers(ctx, downstreamClient, downstreamGateway, liveSolvers); err != nil {
		result.Err = err
		return result
	}

	return result
}

// ensureTLSCertificate creates the listener's TLSCertificate or confirms the
// existing one. dnsNames and secretName are immutable on the service's API, so
// one that no longer matches is deleted and requested again on the next pass.
func (r *GatewayReconciler) ensureTLSCertificate(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	certName, secretName, hostname string,
) (cert *certificatesv1alpha1.TLSCertificate, recreating bool, err error) {
	logger := log.FromContext(ctx)

	desiredSpec := certificatesv1alpha1.TLSCertificateSpec{
		DNSNames:   []certificatesv1alpha1.DNSName{certificatesv1alpha1.DNSName(hostname)},
		Issuance:   certificatesv1alpha1.IssuanceModeAuto,
		SecretName: secretName,
	}

	cert = &certificatesv1alpha1.TLSCertificate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: upstreamGateway.Namespace,
			Name:      certName,
		},
	}

	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(cert), cert)
	switch {
	case apierrors.IsNotFound(err):
		if err := controllerutil.SetControllerReference(upstreamGateway, cert, upstreamClient.Scheme()); err != nil {
			return nil, false, fmt.Errorf("failed to set controller reference on TLSCertificate %s: %w", certName, err)
		}
		cert.Spec = desiredSpec
		if err := upstreamClient.Create(ctx, cert); err != nil {
			return nil, false, fmt.Errorf("failed to create TLSCertificate %s: %w", certName, err)
		}
		logger.Info("TLSCertificate requested", "tlscertificate", certName, "hostname", hostname)
		return cert, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("failed to get TLSCertificate %s: %w", certName, err)
	}

	if !slices.Equal(cert.Spec.DNSNames, desiredSpec.DNSNames) || cert.Spec.SecretName != desiredSpec.SecretName {
		logger.Info("TLSCertificate no longer matches its listener, requesting it again", "tlscertificate", certName, "hostname", hostname)
		if err := upstreamClient.Delete(ctx, cert); client.IgnoreNotFound(err) != nil {
			return nil, false, fmt.Errorf("failed to delete TLSCertificate %s: %w", certName, err)
		}
		return nil, true, nil
	}

	if metav1.IsControlledBy(cert, upstreamGateway) && cert.Spec.Issuance == desiredSpec.Issuance {
		return cert, false, nil
	}

	if err := controllerutil.SetControllerReference(upstreamGateway, cert, upstreamClient.Scheme()); err != nil {
		return nil, false, fmt.Errorf("failed to set controller reference on TLSCertificate %s: %w", certName, err)
	}
	cert.Spec.Issuance = desiredSpec.Issuance
	if err := upstreamClient.Update(ctx, cert); err != nil {
		return nil, false, fmt.Errorf("failed to update TLSCertificate %s: %w", certName, err)
	}
	logger.Info("TLSCertificate reconciled", "tlscertificate", certName, "operation", "updated")
	return cert, false, nil
}

// serveTLSCertificateChallenges publishes every pending HTTP-01 challenge the
// service reports for the hostname this listener claimed and returns the solver
// names for every such challenge still in status, so a challenge that has moved
// on to Valid keeps its answer until the service drops it. A challenge for any
// other name is not ours to answer, whatever status says.
func (r *GatewayReconciler) serveTLSCertificateChallenges(
	ctx context.Context,
	downstreamClient client.Client,
	downstreamGateway *gatewayv1.Gateway,
	cert *certificatesv1alpha1.TLSCertificate,
	hostname string,
) ([]string, error) {
	logger := log.FromContext(ctx)
	var live []string

	for _, challenge := range cert.Status.Challenges {
		if challenge.Type != certificatesv1alpha1.ChallengeTypeHTTP01 {
			continue
		}
		if challenge.DNSName != hostname {
			logger.Info("ignoring HTTP-01 challenge for a hostname this listener did not claim",
				"tlscertificate", cert.Name, "claimed", hostname, "challenge_dns_name", challenge.DNSName)
			continue
		}
		name := tlsCertificateSolverName(cert.Name, challenge.Token)
		live = append(live, name)

		if challenge.State != certificatesv1alpha1.ChallengeStatePending {
			continue
		}

		err := ensureHTTP01SolverRoutes(ctx, downstreamClient, downstreamClient.Scheme(), downstreamGateway, downstreamGateway, http01SolverRoute{
			name:   name,
			token:  challenge.Token,
			key:    challenge.Key,
			labels: map[string]string{tlsCertificateSolverLabel: cert.Name},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to serve HTTP-01 challenge for TLSCertificate %s: %w", cert.Name, err)
		}
	}

	return live, nil
}

// mirrorTLSCertificateSecret copies the service-side issued Secret, read with
// the operator's own credentials, into the downstream gateway namespace under
// the listener's secret name, stamped with the upstream-owner labels the
// federation policy selects. The project-namespace copy is the tenant's and is
// never what reaches an edge.
func (r *GatewayReconciler) mirrorTLSCertificateSecret(
	ctx context.Context,
	downstreamStrategy downstreamclient.ResourceStrategy,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
	cert *certificatesv1alpha1.TLSCertificate,
	secretName string,
) (mirrored bool, err error) {
	logger := log.FromContext(ctx)

	if !apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) {
		return false, nil
	}
	if cert.Status.ServiceSecretRef == nil || cert.Status.ServiceSecretRef.Name == "" {
		logger.Info("TLSCertificate is Ready without a service-side Secret reference", "tlscertificate", cert.Name)
		return false, nil
	}
	if r.CertificateServiceReader == nil {
		return false, fmt.Errorf("certificate service enabled without a client for its cluster")
	}

	var source corev1.Secret
	sourceKey := client.ObjectKey{Namespace: cert.Status.ServiceSecretRef.Namespace, Name: cert.Status.ServiceSecretRef.Name}
	if err := r.CertificateServiceReader.Get(ctx, sourceKey, &source); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TLSCertificate is Ready but its service-side Secret is not readable yet", "tlscertificate", cert.Name, "secret", sourceKey)
			return false, nil
		}
		return false, fmt.Errorf("failed to get service-side Secret %s for TLSCertificate %s: %w", sourceKey, cert.Name, err)
	}
	if len(source.Data["tls.crt"]) == 0 || len(source.Data["tls.key"]) == 0 {
		logger.Info("service-side Secret holds no certificate material yet", "tlscertificate", cert.Name, "secret", sourceKey)
		return false, nil
	}

	mirror := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamGateway.Namespace,
			Name:      secretName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, downstreamStrategy.GetClient(), mirror, func() error {
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
		return false, fmt.Errorf("failed to mirror Secret %s: %w", secretName, err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("issued Secret mirrored downstream", "secret", secretName, "operation", op)
	}

	return op != controllerutil.OperationResultNone, nil
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

// legacyCertificateHolds reports whether a cert-manager Certificate is serving
// and not yet due for renewal, so switching it to the service now would issue a
// certificate nobody needs yet.
func legacyCertificateHolds(cert *cmv1.Certificate, now time.Time) bool {
	return certIsServing(cert, now) && cert.Status.RenewalTime != nil && cert.Status.RenewalTime.After(now)
}

// retireLegacyCertificate hands the listener over to the service: the Secret
// gains the gateway's own owner so cert-manager's owner reference no longer
// takes it along, then the Certificate goes so cert-manager does not renew a
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
		if err := downstreamClient.Update(ctx, &secret); err != nil {
			return fmt.Errorf("failed to keep Secret %s past its Certificate: %w", secretName, err)
		}
	}

	if err := downstreamClient.Delete(ctx, legacy); client.IgnoreNotFound(err) != nil {
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
		if desiredCerts.Has(cert.Name) || !metav1.IsControlledBy(cert, upstreamGateway) {
			continue
		}
		logger.Info("deleting stale TLSCertificate", "tlscertificate", cert.Name)
		if err := upstreamClient.Delete(ctx, cert); client.IgnoreNotFound(err) != nil {
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

	secretStatus := listenerSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, now)

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

	if secretStatus.healthy {
		if apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) && cert.Status.NotAfter != nil {
			secretStatus.notAfter = cert.Status.NotAfter
		}
		return secretStatus
	}

	if accepted := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionAccepted); accepted != nil && accepted.Status == metav1.ConditionFalse {
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
