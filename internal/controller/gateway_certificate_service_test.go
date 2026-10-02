// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func newCertificateServiceTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, cmv1.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, certificatesv1alpha1.AddToScheme(testScheme))
	return testScheme
}

func TestEnsureDownstreamGatewayCertificateService(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()},
	}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)

	const (
		gatewayName     = "test-gw"
		listenerName    = gatewayv1.SectionName("https-hostname-0")
		hostname        = "custom.example.com"
		serviceNS       = "certificates-system"
		serviceSecret   = "svc-secret"
		upstreamCluster = "test"
	)
	certName := tlsCertificateName(gatewayName, listenerName)
	legacyName := listenerCertificateName(gatewayName, listenerName)
	secretName := listenerCertificateSecretName(gatewayName, listenerName)

	baseConfig := config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		DefaultListenerTLSSecretName:          "wildcard-test-suite-tls",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
		ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
			gatewayv1.AnnotationKey(certificateIssuerTLSOption): autoIssuerSentinel,
		},
		ClusterIssuerMap:   map[string]string{autoIssuerSentinel: "datum-gateway-http"},
		CertificateService: config.CertificateServiceConfig{Enabled: true, SecretNamespace: serviceNS},
	}
	testCfg := config.NetworkServicesOperator{Gateway: baseConfig}

	customListener := gatewayv1.Listener{
		Name:     listenerName,
		Protocol: gatewayv1.HTTPSProtocolType,
		Port:     DefaultHTTPSPort,
		Hostname: ptr.To(gatewayv1.Hostname(hostname)),
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{From: ptr.To(gatewayv1.NamespacesFromSame)},
		},
		TLS: &gatewayv1.ListenerTLSConfig{
			Mode: ptr.To(gatewayv1.TLSModeTerminate),
			Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey(certificateIssuerTLSOption): autoIssuerSentinel,
			},
		},
	}

	verifiedDomain := newDomain(upstreamNamespace.Name, hostname, func(d *networkingv1alpha.Domain) {
		d.Spec.DomainName = hostname
		apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
			Type:   networkingv1alpha.DomainConditionVerified,
			Status: metav1.ConditionTrue,
		})
	})

	now := time.Now()
	serviceCertPEM, serviceKeyPEM := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	projectCertPEM, projectKeyPEM := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	legacyCertPEM, legacyKeyPEM := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))

	tlsSecret := func(namespace, name string, certPEM, keyPEM []byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM},
		}
	}

	newTLSCertificate := func(owner *gatewayv1.Gateway, name string, mutate func(*certificatesv1alpha1.TLSCertificate)) *certificatesv1alpha1.TLSCertificate {
		cert := &certificatesv1alpha1.TLSCertificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: name, UID: uuid.NewUUID()},
			Spec: certificatesv1alpha1.TLSCertificateSpec{
				DNSNames:   []certificatesv1alpha1.DNSName{hostname},
				Issuance:   certificatesv1alpha1.IssuanceModeAuto,
				SecretName: secretName,
			},
		}
		if owner != nil {
			require.NoError(t, controllerutil.SetControllerReference(owner, cert, testScheme))
		}
		if mutate != nil {
			mutate(cert)
		}
		return cert
	}

	readyStatus := func(cert *certificatesv1alpha1.TLSCertificate) {
		cert.Status.NotBefore = &metav1.Time{Time: now.Add(-time.Hour)}
		cert.Status.NotAfter = &metav1.Time{Time: now.Add(60 * 24 * time.Hour)}
		cert.Status.SecretRef = &certificatesv1alpha1.SecretReference{Name: secretName}
		cert.Status.ServiceSecretRef = &certificatesv1alpha1.ServiceSecretReference{Namespace: serviceNS, Name: serviceSecret}
		apimeta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionTrue, Reason: "Accepted"})
		apimeta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued"})
	}

	legacyLabels := map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + upstreamCluster,
		downstreamclient.UpstreamOwnerGroupLabel:       gatewayv1.GroupName,
		downstreamclient.UpstreamOwnerKindLabel:        KindGateway,
		downstreamclient.UpstreamOwnerNameLabel:        gatewayName,
		downstreamclient.UpstreamOwnerNamespaceLabel:   upstreamNamespace.Name,
	}
	newLegacyCertificate := func(renewalTime time.Time) *cmv1.Certificate {
		return &cmv1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: legacyName, UID: uuid.NewUUID(), Labels: legacyLabels},
			Spec:       cmv1.CertificateSpec{SecretName: secretName, DNSNames: []string{hostname}},
			Status: cmv1.CertificateStatus{
				NotBefore:   &metav1.Time{Time: now.Add(-time.Hour)},
				NotAfter:    &metav1.Time{Time: now.Add(60 * 24 * time.Hour)},
				RenewalTime: &metav1.Time{Time: renewalTime},
				Conditions:  []cmv1.CertificateCondition{{Type: cmv1.CertificateConditionReady, Status: cmmeta.ConditionTrue}},
			},
		}
	}

	downstreamGatewaySeed := func() *gatewayv1.Gateway {
		return &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: downstreamNamespaceName,
				Name:      gatewayName,
				UID:       uuid.NewUUID(),
				Labels:    legacyLabels,
			},
			Spec: gatewayv1.GatewaySpec{GatewayClassName: "test-suite"},
		}
	}

	solverObjects := func(owner *gatewayv1.Gateway, token string) []client.Object {
		name := tlsCertificateSolverName(certName, token)
		labels := map[string]string{http01SolverLabel: labelValueTrue, tlsCertificateSolverLabel: certName}
		route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: name, Labels: labels}}
		filter := &envoygatewayv1alpha1.HTTPRouteFilter{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: name, Labels: labels}}
		require.NoError(t, controllerutil.SetControllerReference(owner, route, testScheme))
		require.NoError(t, controllerutil.SetControllerReference(owner, filter, testScheme))
		return []client.Object{route, filter}
	}

	type env struct {
		upstream   client.Client
		downstream client.Client
		service    client.Client
		result     Result
		reconciler *GatewayReconciler
	}

	tests := []struct {
		name              string
		upstreamObjects   func(gw *gatewayv1.Gateway) []client.Object
		downstreamObjects func() []client.Object
		serviceObjects    []client.Object
		serviceForbidden  bool
		assert            func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway)
	}{
		{
			name: "requests a TLSCertificate upstream and withholds the listener until it issues",
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				var cert certificatesv1alpha1.TLSCertificate
				require.NoError(t, e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
				assert.Equal(t, []certificatesv1alpha1.DNSName{hostname}, cert.Spec.DNSNames)
				assert.Equal(t, certificatesv1alpha1.IssuanceModeAuto, cert.Spec.Issuance)
				assert.Equal(t, secretName, cert.Spec.SecretName)
				assert.True(t, metav1.IsControlledBy(&cert, upstreamGateway), "TLSCertificate should be controlled by the upstream Gateway")

				var legacy cmv1.Certificate
				err := e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &legacy)
				assert.True(t, apierrors.IsNotFound(err), "no cert-manager Certificate should be created downstream")

				assert.Nil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "listener must be withheld until a certificate is issued")
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, gatewayutil.DefaultHTTPSListenerName), "shared wildcard listener is untouched")
			},
		},
		{
			name: "serves pending HTTP-01 challenges only for the hostname the listener claimed",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Status.Challenges = []certificatesv1alpha1.ACMEChallenge{
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "token-aaaaaaaaaaaaaa", Key: "key-a", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: "evil.example.com", Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "token-bbbbbbbbbbbbbb", Key: "key-b", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeDNS01, Token: "token-cccccccccccccc", Key: "key-c", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "token-dddddddddddddd", Key: "key-d", State: certificatesv1alpha1.ChallengeStateInvalid},
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "bad token/../x", Key: "key-e", State: certificatesv1alpha1.ChallengeStatePending},
					}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				served := tlsCertificateSolverName(certName, "token-aaaaaaaaaaaaaa")

				var route gatewayv1.HTTPRoute
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: served}, &route))
				assert.Equal(t, labelValueTrue, route.Labels[http01SolverLabel])
				assert.Equal(t, certName, route.Labels[tlsCertificateSolverLabel])
				require.Len(t, route.Spec.Rules, 1)
				require.Len(t, route.Spec.Rules[0].Matches, 1)
				assert.Equal(t, "/.well-known/acme-challenge/token-aaaaaaaaaaaaaa", ptr.Deref(route.Spec.Rules[0].Matches[0].Path.Value, ""))
				assert.Equal(t, gatewayv1.ObjectName(gatewayName), route.Spec.ParentRefs[0].Name)
				assert.Equal(t, []gatewayv1.Hostname{hostname}, route.Spec.Hostnames, "solver route is scoped to the claimed hostname")
				owner := metav1.GetControllerOf(&route)
				require.NotNil(t, owner)
				assert.Equal(t, KindGateway, owner.Kind)

				var filter envoygatewayv1alpha1.HTTPRouteFilter
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: served}, &filter))
				assert.Equal(t, "key-a", ptr.Deref(filter.Spec.DirectResponse.Body.Inline, ""))

				for _, token := range []string{"token-bbbbbbbbbbbbbb", "token-cccccccccccccc", "token-dddddddddddddd", "bad token/../x"} {
					err := e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: tlsCertificateSolverName(certName, token)}, &gatewayv1.HTTPRoute{})
					assert.True(t, apierrors.IsNotFound(err), "no solver route for challenge %s", token)
				}
				assert.NotEqual(t, certificateServiceBackoffBase, e.result.RequeueAfter, "a rejected token is not a failure of the step")
			},
		},
		{
			name: "removes solver routes once their challenge leaves status",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Status.Challenges = []certificatesv1alpha1.ACMEChallenge{
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "token-keepkeepkeepkeep", Key: "key-keep", State: certificatesv1alpha1.ChallengeStateValid},
					}
				})}
			},
			downstreamObjects: func() []client.Object {
				gw := downstreamGatewaySeed()
				objs := make([]client.Object, 0, 5)
				objs = append(objs, gw)
				objs = append(objs, solverObjects(gw, "token-keepkeepkeepkeep")...)
				objs = append(objs, solverObjects(gw, "token-oldoldoldoldold")...)
				return objs
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				assert.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: tlsCertificateSolverName(certName, "token-keepkeepkeepkeep")}, &gatewayv1.HTTPRoute{}), "a Valid challenge keeps its answer until the service drops it")

				old := tlsCertificateSolverName(certName, "token-oldoldoldoldold")
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: old}, &gatewayv1.HTTPRoute{})))
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: old}, &envoygatewayv1alpha1.HTTPRouteFilter{})))
			},
		},
		{
			name: "mirrors the service-side Secret when Ready and admits the listener",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{
					newTLSCertificate(gw, certName, readyStatus),
					tlsSecret(upstreamNamespace.Name, secretName, projectCertPEM, projectKeyPEM),
				}
			},
			serviceObjects: []client.Object{tlsSecret(serviceNS, serviceSecret, serviceCertPEM, serviceKeyPEM)},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				var mirror corev1.Secret
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &mirror))
				assert.Equal(t, corev1.SecretTypeTLS, mirror.Type)
				assert.Equal(t, serviceCertPEM, mirror.Data["tls.crt"], "edge material comes from the service-side Secret")
				assert.Equal(t, serviceKeyPEM, mirror.Data["tls.key"])
				assert.NotEqual(t, projectCertPEM, mirror.Data["tls.crt"], "the project copy must never reach the edge")
				assert.Equal(t, "cluster-"+upstreamCluster, mirror.Labels[downstreamclient.UpstreamOwnerClusterNameLabel])
				assert.Equal(t, upstreamNamespace.Name, mirror.Labels[downstreamclient.UpstreamOwnerNamespaceLabel])
				assert.Equal(t, KindGateway, mirror.Labels[downstreamclient.UpstreamOwnerKindLabel])

				assert.Equal(t, tlsCertificateMirrorAdmitDelay, e.result.RequeueAfter, "a fresh mirror asks for a prompt re-evaluation")

				health := e.reconciler.evaluateListenerTLSCertificateHealth(ctx, e.upstream, e.downstream, downstreamNamespaceName, upstreamGateway, []string{hostname})
				status, gated := health[listenerName]
				require.True(t, gated)
				assert.True(t, status.healthy, "listener is admitted on the next pass: %s", status.message)
				assert.Equal(t, secretName, status.secretName)
				require.NotNil(t, status.notAfter)
				assert.WithinDuration(t, now.Add(60*24*time.Hour), status.notAfter.Time, time.Minute)
			},
		},
		{
			name: "a rejected request withholds the listener and tells the customer why",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
						Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionFalse,
						Reason: "DeniedDomain", Message: "names under datum.net are denied",
					})
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.Nil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName))

				var found bool
				for _, ls := range upstreamGateway.Status.Listeners {
					if ls.Name != listenerName {
						continue
					}
					found = true
					resolved := apimeta.FindStatusCondition(ls.Conditions, string(gatewayv1.ListenerConditionResolvedRefs))
					require.NotNil(t, resolved)
					assert.Equal(t, metav1.ConditionFalse, resolved.Status)
					assert.Contains(t, resolved.Message, "names under datum.net are denied")
				}
				assert.True(t, found, "listener status should be reported")
			},
		},
		{
			name: "keeps a valid cert-manager certificate that is not due for renewal",
			downstreamObjects: func() []client.Object {
				return []client.Object{
					newLegacyCertificate(now.Add(30 * 24 * time.Hour)),
					tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM),
				}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				var certs certificatesv1alpha1.TLSCertificateList
				require.NoError(t, e.upstream.List(ctx, &certs, client.InNamespace(upstreamNamespace.Name)))
				assert.Empty(t, certs.Items, "no TLSCertificate is requested while the legacy certificate holds")

				assert.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &cmv1.Certificate{}), "legacy Certificate stays")
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "listener keeps serving on the legacy certificate")
			},
		},
		{
			name: "switches to the service ahead of cert-manager's renewal time",
			downstreamObjects: func() []client.Object {
				legacy := newLegacyCertificate(now.Add(24 * time.Hour))
				secret := tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)
				require.NoError(t, controllerutil.SetControllerReference(legacy, secret, testScheme))
				return []client.Object{legacy, secret}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &cmv1.Certificate{})), "legacy Certificate is retired so cert-manager does not renew it")

				var secret corev1.Secret
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.Equal(t, legacyCertPEM, secret.Data["tls.crt"], "the serving Secret is kept as-is until the service issues")
				assert.Equal(t, upstreamNamespace.Name, secret.Labels[downstreamclient.UpstreamOwnerNamespaceLabel])
				assert.Equal(t, labelValueTrue, secret.Labels[tlsCertificateManagedLabel], "the hand-over is recorded for rollback")
				var anchored bool
				for _, ref := range secret.OwnerReferences {
					if ref.Kind == "ConfigMap" {
						anchored = true
					}
				}
				assert.True(t, anchored, "Secret gains the gateway anchor so the Certificate's owner reference no longer takes it along")

				assert.NoError(t, e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &certificatesv1alpha1.TLSCertificate{}), "TLSCertificate is requested")
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "listener keeps serving on the retained Secret")
			},
		},
		{
			name: "does not retire a cert-manager certificate while cert-manager is renewing it",
			downstreamObjects: func() []client.Object {
				legacy := newLegacyCertificate(now.Add(-time.Hour))
				legacy.Status.Conditions = append(legacy.Status.Conditions, cmv1.CertificateCondition{Type: cmv1.CertificateConditionIssuing, Status: cmmeta.ConditionTrue})
				return []client.Object{legacy, tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				assert.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &cmv1.Certificate{}), "legacy Certificate stays while its renewal is in flight")
				var certs certificatesv1alpha1.TLSCertificateList
				require.NoError(t, e.upstream.List(ctx, &certs, client.InNamespace(upstreamNamespace.Name)))
				assert.Empty(t, certs.Items)

				strategy := downstreamclient.NewMappedNamespaceResourceStrategy(upstreamCluster, e.upstream, e.downstream)
				step, issues := e.reconciler.ensureListenerTLSCertificates(ctx, e.upstream, upstreamGateway, downstreamGateway, e.downstream, strategy, []string{hostname})
				assert.Empty(t, issues)
				assert.Equal(t, 10*time.Minute, step.RequeueAfter, "the hold is re-checked while cert-manager renews")
			},
		},
		{
			name: "never deletes a mismatched TLSCertificate another owner controls",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				other := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "other-gw", UID: uuid.NewUUID()}}
				return []client.Object{newTLSCertificate(other, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Spec.DNSNames = []certificatesv1alpha1.DNSName{"previous.example.com"}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var cert certificatesv1alpha1.TLSCertificate
				require.NoError(t, e.upstream.Get(context.Background(), client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
				assert.Equal(t, []certificatesv1alpha1.DNSName{"previous.example.com"}, cert.Spec.DNSNames, "someone else's TLSCertificate is left alone")
				assert.Equal(t, certificateServiceBackoffBase, e.result.RequeueAfter, "the clash is retried with backoff, not an error")
				assertListenerSaysKeepTrying(t, upstreamGateway)
			},
		},
		{
			name: "refuses a service-side Secret outside the service namespace",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.ServiceSecretRef.Namespace = "victim-namespace"
				})}
			},
			serviceObjects: []client.Object{tlsSecret("victim-namespace", serviceSecret, serviceCertPEM, serviceKeyPEM)},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &corev1.Secret{})))
				assertListenerSaysKeepTrying(t, upstreamGateway)
			},
		},
		{
			name: "refuses service-side material whose key does not match",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			serviceObjects: []client.Object{tlsSecret(serviceNS, serviceSecret, serviceCertPEM, projectKeyPEM)},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &corev1.Secret{})))
			},
		},
		{
			name: "refuses service-side material for another hostname",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			serviceObjects: func() []client.Object {
				crt, key := generateTLSKeyPair(t, "other.example.com", now.Add(-time.Hour), now.Add(60*24*time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &corev1.Secret{})))
			},
		},
		{
			name: "a rejection while the previous certificate serves is reported as a blocked renewal",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
						Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionFalse, Reason: "DeniedDomain", Message: "names under datum.net are denied",
					})
				})}
			},
			downstreamObjects: func() []client.Object {
				secret := tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)
				secret.Labels = map[string]string{tlsCertificateManagedLabel: labelValueTrue}
				return []client.Object{secret}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "the listener keeps serving")
				assertListenerRenewalBlocked(t, upstreamGateway, "names under datum.net are denied")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRejected), 1.0)
			},
		},
		{
			name: "a forbidden service-side read while the previous certificate serves is reported as a blocked renewal",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			downstreamObjects: func() []client.Object {
				return []client.Object{tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)}
			},
			serviceForbidden: true,
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "the listener keeps serving")
				assertListenerRenewalBlocked(t, upstreamGateway, "keep trying")
				assert.Equal(t, certificateServiceBackoffBase, e.result.RequeueAfter)
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed), 1.0)
			},
		},
		{
			name: "a forbidden service-side read with nothing serving is reported as blocked issuance",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			serviceForbidden: true,
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.Nil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName))
				assertListenerSaysKeepTrying(t, upstreamGateway)
				for _, ls := range upstreamGateway.Status.Listeners {
					if ls.Name != listenerName {
						continue
					}
					blocked := apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateIssuanceBlocked)
					require.NotNil(t, blocked)
					assert.Equal(t, metav1.ConditionTrue, blocked.Status)
					assert.Equal(t, listenerReasonIssuanceFailing, blocked.Reason)
					assert.Contains(t, blocked.Message, "keep trying")
				}
			},
		},
		{
			name: "refused material while the previous certificate serves is reported as a blocked renewal",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			downstreamObjects: func() []client.Object {
				return []client.Object{tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)}
			},
			serviceObjects: func() []client.Object {
				crt, key := generateTLSKeyPair(t, "other.example.com", now.Add(-time.Hour), now.Add(60*24*time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var secret corev1.Secret
				require.NoError(t, e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.Equal(t, legacyCertPEM, secret.Data["tls.crt"])
				assertListenerRenewalBlocked(t, upstreamGateway, "keep trying")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonMaterialRefused), 1.0)
			},
		},
		{
			name: "refuses expired service-side material and keeps the serving Secret",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, readyStatus)}
			},
			downstreamObjects: func() []client.Object {
				secret := tlsSecret(downstreamNamespaceName, secretName, legacyCertPEM, legacyKeyPEM)
				secret.Labels = map[string]string{tlsCertificateManagedLabel: labelValueTrue}
				return []client.Object{secret}
			},
			serviceObjects: func() []client.Object {
				crt, key := generateTLSKeyPair(t, hostname, now.Add(-48*time.Hour), now.Add(-time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var secret corev1.Secret
				require.NoError(t, e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.Equal(t, legacyCertPEM, secret.Data["tls.crt"], "a valid serving Secret is never replaced by material that fails validation")
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName))
			},
		},
		{
			name: "deletes the TLSCertificate of a listener that is gone and leaves others alone",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{
					newTLSCertificate(gw, tlsCertificateName(gatewayName, "https-hostname-9"), nil),
					newTLSCertificate(nil, "someone-elses-cert", nil),
				}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				assert.True(t, apierrors.IsNotFound(e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: tlsCertificateName(gatewayName, "https-hostname-9")}, &certificatesv1alpha1.TLSCertificate{})))
				assert.NoError(t, e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: "someone-elses-cert"}, &certificatesv1alpha1.TLSCertificate{}))
				assert.NoError(t, e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &certificatesv1alpha1.TLSCertificate{}))
			},
		},
		{
			name: "requests the TLSCertificate again when its immutable spec no longer matches",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Spec.DNSNames = []certificatesv1alpha1.DNSName{"previous.example.com"}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				err := e.upstream.Get(context.Background(), client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &certificatesv1alpha1.TLSCertificate{})
				assert.True(t, apierrors.IsNotFound(err), "mismatched TLSCertificate is deleted so the next pass requests it afresh")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = append(g.Spec.Listeners, customListener)
			})

			upstreamObjects := []client.Object{
				verifiedDomain.DeepCopy(),
				&gatewayv1.GatewayClass{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController("test")},
				},
			}
			if tt.upstreamObjects != nil {
				upstreamObjects = append(upstreamObjects, tt.upstreamObjects(upstreamGateway)...)
			}
			var downstreamObjects []client.Object
			if tt.downstreamObjects != nil {
				downstreamObjects = tt.downstreamObjects()
			}
			for _, obj := range append(append([]client.Object{}, upstreamObjects...), downstreamObjects...) {
				if obj.GetUID() == "" {
					obj.SetUID(uuid.NewUUID())
				}
				obj.SetCreationTimestamp(metav1.Now())
			}

			fakeUpstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(upstreamGateway, upstreamNamespace).
				WithObjects(upstreamObjects...).
				WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).
				WithStatusSubresource(upstreamObjects...).
				Build()

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(downstreamObjects...).
				WithStatusSubresource(&gatewayv1.Gateway{}, &cmv1.Certificate{}).
				Build()

			serviceBuilder := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.serviceObjects...)
			if tt.serviceForbidden {
				serviceBuilder = serviceBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return apierrors.NewForbidden(corev1.Resource("secrets"), serviceSecret, nil)
					},
				})
			}
			fakeServiceClient := serviceBuilder.Build()

			ctx := log.IntoContext(context.Background(), logger)

			reconciler := &GatewayReconciler{
				mgr:                      &fakeMockManager{cl: fakeUpstreamClient},
				Config:                   testCfg,
				DownstreamCluster:        &fakeCluster{cl: fakeDownstreamClient},
				CertificateServiceReader: fakeServiceClient,
			}
			downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(upstreamCluster, fakeUpstreamClient, fakeDownstreamClient)

			reconciler.prepareUpstreamGateway(upstreamGateway)
			result, downstreamGateway := reconciler.ensureDownstreamGateway(ctx, upstreamCluster, fakeUpstreamClient, upstreamGateway, downstreamStrategy)
			require.NoError(t, result.Err)
			_, err := result.Complete(ctx)
			require.NoError(t, err)

			updatedUpstream := &gatewayv1.Gateway{}
			require.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamGateway), updatedUpstream))

			tt.assert(t, env{
				upstream:   fakeUpstreamClient,
				downstream: fakeDownstreamClient,
				service:    fakeServiceClient,
				result:     result,
				reconciler: reconciler,
			}, updatedUpstream, downstreamGateway)
		})
	}
}

func assertListenerRenewalBlocked(t *testing.T, gateway *gatewayv1.Gateway, want string) {
	t.Helper()
	for _, ls := range gateway.Status.Listeners {
		if apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateRenewalBlocked) == nil {
			continue
		}
		resolved := apimeta.FindStatusCondition(ls.Conditions, string(gatewayv1.ListenerConditionResolvedRefs))
		require.NotNil(t, resolved)
		assert.Equal(t, metav1.ConditionTrue, resolved.Status, "the listener itself stays resolved")
		blocked := apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateRenewalBlocked)
		require.NotNil(t, blocked, "a blocked renewal is reported while the listener serves")
		assert.Equal(t, metav1.ConditionTrue, blocked.Status)
		assert.Equal(t, listenerReasonRenewalFailing, blocked.Reason)
		assert.Contains(t, blocked.Message, want)
		return
	}
	t.Fatal("no listener reports a blocked renewal")
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(labels...).Write(&m))
	return m.GetCounter().GetValue()
}

func assertListenerSaysKeepTrying(t *testing.T, gateway *gatewayv1.Gateway) {
	t.Helper()
	for _, ls := range gateway.Status.Listeners {
		resolved := apimeta.FindStatusCondition(ls.Conditions, string(gatewayv1.ListenerConditionResolvedRefs))
		if resolved == nil || resolved.Status != metav1.ConditionFalse {
			continue
		}
		assert.Contains(t, resolved.Message, "keep trying")
		return
	}
	t.Fatal("no listener reports an unresolved certificate")
}

func TestTLSCertificateName(t *testing.T) {
	long := tlsCertificateName("a-gateway-with-a-deliberately-very-long-name-for-this-test", "https-hostname-with-a-long-name-0")
	assert.LessOrEqual(t, len(long), 63)
	assert.NotEqual(t, tlsCertificateName("a-b", "c"), tlsCertificateName("a", "b-c"), "gateway and listener never blur into each other")
	dotted := tlsCertificateName("my.dotted.gateway.name.that.is.long.enough.to.be.cut", "https-0")
	assert.Regexp(t, `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, dotted)
	assert.LessOrEqual(t, len(dotted), 63)
	assert.Equal(t, tlsCertificateName("gw", "https-0"), tlsCertificateName("gw", "https-0"))
	assert.True(t, strings.HasPrefix(tlsCertificateName("gw", "https-0"), "gw-https-0-"))
}

func TestCertificateServiceRequeueBackoff(t *testing.T) {
	r := &GatewayReconciler{}
	uid := types.UID("gw")
	now := time.Now()
	issues := map[gatewayv1.SectionName]string{"https-0": "trying", "https-1": "other"}
	assert.Equal(t, 5*time.Second, r.certificateServiceRequeue(uid, true, now, issues))
	kept, _, _ := r.certificateServiceInBackoff(uid, now)
	assert.Equal(t, "other", kept.issues["https-1"], "each listener keeps its own message")
	issues = map[gatewayv1.SectionName]string{"https-0": "trying"}
	assert.Equal(t, 10*time.Second, r.certificateServiceRequeue(uid, true, now, issues))
	assert.Equal(t, 20*time.Second, r.certificateServiceRequeue(uid, true, now, issues))
	for range 10 {
		r.certificateServiceRequeue(uid, true, now, issues)
	}
	assert.Equal(t, certificateServiceBackoffMax, r.certificateServiceRequeue(uid, true, now, issues))

	backoff, remaining, cooling := r.certificateServiceInBackoff(uid, now.Add(time.Minute))
	assert.True(t, cooling, "an event inside the window repeats the message instead of the calls")
	assert.Equal(t, map[gatewayv1.SectionName]string{"https-0": "trying"}, backoff.issues)
	assert.Equal(t, certificateServiceBackoffMax-time.Minute, remaining)
	_, _, cooling = r.certificateServiceInBackoff(uid, now.Add(certificateServiceBackoffMax))
	assert.False(t, cooling)

	assert.Zero(t, r.certificateServiceRequeue(uid, false, now, nil))
	assert.Equal(t, 5*time.Second, r.certificateServiceRequeue(uid, true, now, issues), "a success resets the backoff")
	_, _, cooling = r.certificateServiceInBackoff(types.UID("other"), now)
	assert.False(t, cooling)
}

func TestTLSCertificateNameGolden(t *testing.T) {
	for _, tt := range []struct{ gateway, listener, want string }{
		{"gw", "https-0", "gw-https-0-bb35a504d1"},
		{"a-b", "c", "a-b-c-4e84717d75"},
		{"a", "b-c", "a-b-c-b88f83c840"},
		{"my.dotted.gateway.name.that.is.long.enough.to.be.cut", "https-0", "my-dotted-gateway-name-that-is-long-enough-to-be-cut-c30f01e877"},
		{"a-gateway-with-a-deliberately-very-long-name-for-this-test", "https-hostname-with-a-long-name-0", "a-gateway-with-a-deliberately-very-long-name-for-thi-8c34270044"},
	} {
		assert.Equal(t, tt.want, tlsCertificateName(tt.gateway, gatewayv1.SectionName(tt.listener)))
	}
}

func TestValidateIssuedMaterialWildcard(t *testing.T) {
	now := time.Now()
	wildcardCrt, wildcardKey := generateTLSKeyPair(t, "*.example.com", now.Add(-time.Hour), now.Add(24*time.Hour))
	apexCrt, apexKey := generateTLSKeyPair(t, "example.com", now.Add(-time.Hour), now.Add(24*time.Hour))

	assert.NoError(t, validateIssuedMaterial(wildcardCrt, wildcardKey, "*.example.com", now))
	assert.NoError(t, validateIssuedMaterial(wildcardCrt, wildcardKey, "app.example.com", now))
	assert.Error(t, validateIssuedMaterial(wildcardCrt, wildcardKey, "example.com", now), "a wildcard does not cover the apex")
	assert.Error(t, validateIssuedMaterial(apexCrt, apexKey, "*.example.com", now), "an apex certificate does not cover a wildcard listener")
}

func TestLegacyCertificateHolds(t *testing.T) {
	now := time.Now()
	base := func() *cmv1.Certificate {
		return &cmv1.Certificate{Status: cmv1.CertificateStatus{
			NotBefore:   &metav1.Time{Time: now.Add(-30 * 24 * time.Hour)},
			NotAfter:    &metav1.Time{Time: now.Add(60 * 24 * time.Hour)},
			RenewalTime: &metav1.Time{Time: now.Add(-time.Hour)},
			Conditions:  []cmv1.CertificateCondition{{Type: cmv1.CertificateConditionReady, Status: cmmeta.ConditionTrue}},
		}}
	}
	issuing := func(since time.Time) cmv1.CertificateCondition {
		return cmv1.CertificateCondition{Type: cmv1.CertificateConditionIssuing, Status: cmmeta.ConditionTrue, LastTransitionTime: &metav1.Time{Time: since}}
	}

	cert := base()
	cert.Status.Conditions = append(cert.Status.Conditions, issuing(now.Add(-time.Hour)))
	holds, recheck := legacyCertificateHolds(cert, now)
	assert.True(t, holds, "a fresh renewal in flight is left to finish")
	assert.Equal(t, 10*time.Minute, recheck)

	cert = base()
	cert.Status.Conditions = append(cert.Status.Conditions, issuing(now.Add(-25*time.Hour)))
	holds, _ = legacyCertificateHolds(cert, now)
	assert.False(t, holds, "a renewal in flight for over a day is stuck")

	cert = base()
	cert.Status.Conditions = append(cert.Status.Conditions, issuing(now.Add(-time.Hour)))
	cert.Status.LastFailureTime = &metav1.Time{Time: now.Add(-10 * time.Minute)}
	holds, _ = legacyCertificateHolds(cert, now)
	assert.False(t, holds, "a renewal that has failed does not hold")

	cert = base()
	cert.Status.Conditions = append(cert.Status.Conditions, issuing(now.Add(-time.Hour)))
	cert.Status.NotAfter = &metav1.Time{Time: now.Add(5 * 24 * time.Hour)}
	holds, _ = legacyCertificateHolds(cert, now)
	assert.False(t, holds, "under a week of lifetime hands over regardless")

	cert = base()
	cert.Status.RenewalTime = &metav1.Time{Time: now.Add(10 * 24 * time.Hour)}
	holds, recheck = legacyCertificateHolds(cert, now)
	assert.True(t, holds)
	assert.InDelta(t, (10*24*time.Hour - tlsCertificateSwitchLead).Seconds(), recheck.Seconds(), 1)

	cert = base()
	cert.Status.Conditions = []cmv1.CertificateCondition{{Type: cmv1.CertificateConditionReady, Status: cmmeta.ConditionFalse, Reason: "Failed"}}
	holds, _ = legacyCertificateHolds(cert, now)
	assert.False(t, holds, "a certificate that is not serving never holds")
}

func TestCertificateServiceCRDAbsentDoesNotBlockGateway(t *testing.T) {
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	withoutCertificates := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(withoutCertificates))
	require.NoError(t, gatewayv1.Install(withoutCertificates))
	require.NoError(t, discoveryv1.AddToScheme(withoutCertificates))
	require.NoError(t, networkingv1alpha.AddToScheme(withoutCertificates))
	downstreamScheme := newCertificateServiceTestScheme(t)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	const hostname = "custom.example.com"
	testCfg := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		DefaultListenerTLSSecretName:          "wildcard-test-suite-tls",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ListenerTLSOptions:                    map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer"},
		CertificateService:                    config.CertificateServiceConfig{Enabled: true, SecretNamespace: "certificates-system"},
	}}
	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, "test-gw", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
			Name: "https-hostname-0", Protocol: gatewayv1.HTTPSProtocolType, Port: DefaultHTTPSPort,
			Hostname: ptr.To(gatewayv1.Hostname(hostname)),
			TLS: &gatewayv1.ListenerTLSConfig{Mode: ptr.To(gatewayv1.TLSModeTerminate), Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
			}},
		})
	})
	domain := newDomain(upstreamNamespace.Name, hostname, func(d *networkingv1alpha.Domain) {
		d.Spec.DomainName = hostname
		apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue})
	})
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}
	route := newHTTPRoute(upstreamNamespace.Name, "app", func(r *gatewayv1.HTTPRoute) {
		r.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(upstreamGateway.Name)}}
		r.Spec.Hostnames = []gatewayv1.Hostname{hostname}
	})
	for _, obj := range []client.Object{domain, gatewayClass, route} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}

	fakeUpstreamClient := fake.NewClientBuilder().WithScheme(withoutCertificates).
		WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass, route).
		WithStatusSubresource(upstreamGateway, route).Build()
	fakeDownstreamClient := fake.NewClientBuilder().WithScheme(downstreamScheme).WithStatusSubresource(&gatewayv1.Gateway{}).Build()

	reconciler := &GatewayReconciler{
		mgr:                      &fakeMockManager{cl: fakeUpstreamClient},
		Config:                   testCfg,
		DownstreamCluster:        &fakeCluster{cl: fakeDownstreamClient},
		CertificateServiceReader: fake.NewClientBuilder().WithScheme(downstreamScheme).Build(),
	}
	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)

	reconciler.prepareUpstreamGateway(upstreamGateway)
	result, downstreamGateway := reconciler.ensureDownstreamGateway(ctx, "test", fakeUpstreamClient, upstreamGateway, downstreamStrategy)
	require.NoError(t, result.Err, "a missing TLSCertificate API must not fail the gateway")
	_, err := result.Complete(ctx)
	require.NoError(t, err)
	assert.Equal(t, certificateServiceBackoffBase, result.RequeueAfter)

	require.NotNil(t, downstreamGateway)
	assert.NoError(t, fakeDownstreamClient.Get(ctx, client.ObjectKeyFromObject(downstreamGateway), &gatewayv1.Gateway{}), "the downstream gateway is still programmed")
	assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, gatewayutil.DefaultHTTPListenerName))

	var updated gatewayv1.Gateway
	require.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamGateway), &updated))
	assert.NotEmpty(t, updated.Status.Listeners, "gateway status is still written")
	assertListenerSaysKeepTrying(t, &updated)

	var downstreamRoutes gatewayv1.HTTPRouteList
	require.NoError(t, fakeDownstreamClient.List(ctx, &downstreamRoutes, client.InNamespace(downstreamGateway.Namespace)))
	assert.NotEmpty(t, downstreamRoutes.Items, "downstream routes are still reconciled")
}

func TestCertificateServiceRollbackKeepsHandedOverSecret(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	const hostname = "custom.example.com"
	const gatewayName = "test-gw"
	const listenerName = gatewayv1.SectionName("https-hostname-0")
	secretName := listenerCertificateSecretName(gatewayName, listenerName)
	legacyName := listenerCertificateName(gatewayName, listenerName)
	certName := tlsCertificateName(gatewayName, listenerName)

	testCfg := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		DefaultListenerTLSSecretName:          "wildcard-test-suite-tls",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ListenerTLSOptions:                    map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer"},
	}}

	now := time.Now()
	tests := []struct {
		name              string
		notBefore         time.Time
		notAfter          time.Time
		wantCertificate   bool
		wantListenerAdmit bool
	}{
		{name: "fresh handed-over Secret keeps serving and is not reissued", notBefore: now.Add(-24 * time.Hour), notAfter: now.Add(80 * 24 * time.Hour), wantCertificate: false, wantListenerAdmit: true},
		{name: "handed-over Secret in its renewal window gets a cert-manager Certificate again", notBefore: now.Add(-70 * 24 * time.Hour), notAfter: now.Add(20 * 24 * time.Hour), wantCertificate: true, wantListenerAdmit: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
				g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
					Name: listenerName, Protocol: gatewayv1.HTTPSProtocolType, Port: DefaultHTTPSPort,
					Hostname: ptr.To(gatewayv1.Hostname(hostname)),
					TLS: &gatewayv1.ListenerTLSConfig{Mode: ptr.To(gatewayv1.TLSModeTerminate), Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
						gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
					}},
				})
			})
			domain := newDomain(upstreamNamespace.Name, hostname, func(d *networkingv1alpha.Domain) {
				d.Spec.DomainName = hostname
				apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue})
			})
			gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}
			leftover := &certificatesv1alpha1.TLSCertificate{ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: certName}}
			require.NoError(t, controllerutil.SetControllerReference(upstreamGateway, leftover, testScheme))
			foreign := &certificatesv1alpha1.TLSCertificate{ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "foreign"}}

			downstreamGateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: gatewayName, UID: uuid.NewUUID(), Labels: map[string]string{
				downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-test",
				downstreamclient.UpstreamOwnerKindLabel:        KindGateway,
				downstreamclient.UpstreamOwnerNameLabel:        gatewayName,
				downstreamclient.UpstreamOwnerNamespaceLabel:   upstreamNamespace.Name,
			}}, Spec: gatewayv1.GatewaySpec{GatewayClassName: "test-suite"}}
			crt, key := generateTLSKeyPair(t, hostname, tt.notBefore, tt.notAfter)
			handedOver := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: secretName, Labels: map[string]string{
					tlsCertificateManagedLabel:                   labelValueTrue,
					downstreamclient.UpstreamOwnerKindLabel:      KindGateway,
					downstreamclient.UpstreamOwnerNameLabel:      gatewayName,
					downstreamclient.UpstreamOwnerNamespaceLabel: upstreamNamespace.Name,
				}},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{"tls.crt": crt, "tls.key": key},
			}
			solverName := tlsCertificateSolverName(certName, "token-leftoverleftover")
			solverRoute := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: solverName, Labels: map[string]string{tlsCertificateSolverLabel: certName}}}
			solverFilter := &envoygatewayv1alpha1.HTTPRouteFilter{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: solverName, Labels: map[string]string{tlsCertificateSolverLabel: certName}}}
			require.NoError(t, controllerutil.SetControllerReference(downstreamGateway, solverRoute, testScheme))
			require.NoError(t, controllerutil.SetControllerReference(downstreamGateway, solverFilter, testScheme))

			for _, obj := range []client.Object{domain, gatewayClass, leftover, foreign, handedOver, solverRoute, solverFilter} {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
			}
			downstreamGateway.SetCreationTimestamp(metav1.Now())

			fakeUpstreamClient := fake.NewClientBuilder().WithScheme(testScheme).
				WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass, leftover, foreign).
				WithStatusSubresource(upstreamGateway).Build()
			fakeDownstreamClient := fake.NewClientBuilder().WithScheme(testScheme).
				WithObjects(downstreamGateway, handedOver, solverRoute, solverFilter).
				WithStatusSubresource(&gatewayv1.Gateway{}, &cmv1.Certificate{}).Build()

			reconciler := &GatewayReconciler{
				mgr:               &fakeMockManager{cl: fakeUpstreamClient},
				Config:            testCfg,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}
			downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)

			reconciler.prepareUpstreamGateway(upstreamGateway)
			result, downstream := reconciler.ensureDownstreamGateway(ctx, "test", fakeUpstreamClient, upstreamGateway, downstreamStrategy)
			require.NoError(t, result.Err)
			_, err := result.Complete(ctx)
			require.NoError(t, err)

			certErr := fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &cmv1.Certificate{})
			if tt.wantCertificate {
				assert.NoError(t, certErr, "cert-manager takes the hostname back once the Secret is due for renewal")
			} else {
				assert.True(t, apierrors.IsNotFound(certErr), "a fresh handed-over Secret is not reissued on rollback")
			}
			assert.Equal(t, tt.wantListenerAdmit, gatewayutil.GetListenerByName(downstream.Spec.Listeners, listenerName) != nil)

			assert.True(t, apierrors.IsNotFound(fakeUpstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &certificatesv1alpha1.TLSCertificate{})), "the gateway's TLSCertificate is removed on rollback")
			assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: "foreign"}, &certificatesv1alpha1.TLSCertificate{}), "a TLSCertificate the gateway does not control stays")
			assert.True(t, apierrors.IsNotFound(fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: solverName}, &gatewayv1.HTTPRoute{})), "leftover solver routes are removed")
			assert.True(t, apierrors.IsNotFound(fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: solverName}, &envoygatewayv1alpha1.HTTPRouteFilter{})))

			if tt.wantCertificate {
				var legacy cmv1.Certificate
				require.NoError(t, fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: legacyName}, &legacy))
				legacy.Status.Conditions = []cmv1.CertificateCondition{{Type: cmv1.CertificateConditionReady, Status: cmmeta.ConditionTrue}}
				require.NoError(t, fakeDownstreamClient.Status().Update(ctx, &legacy))
				reconciler.cleanupCertificateServiceLeftovers(ctx, fakeUpstreamClient, upstreamGateway, downstreamGateway, fakeDownstreamClient)
				var secret corev1.Secret
				require.NoError(t, fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.NotContains(t, secret.Labels, tlsCertificateManagedLabel, "the hand-over marker goes once cert-manager has the Secret back")
			}
		})
	}
}

func TestCertificateServiceRenewalBlockedClearsOnRecovery(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "recovery", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	const hostname = "custom.example.com"
	const gatewayName = "recovery-gw"
	const listenerName = gatewayv1.SectionName("https-hostname-0")
	const serviceNS = "certificates-system"
	secretName := listenerCertificateSecretName(gatewayName, listenerName)
	certName := tlsCertificateName(gatewayName, listenerName)

	testCfg := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		DefaultListenerTLSSecretName:          "wildcard-test-suite-tls",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ListenerTLSOptions:                    map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer"},
		CertificateService:                    config.CertificateServiceConfig{Enabled: true, SecretNamespace: serviceNS},
	}}
	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
			Name: listenerName, Protocol: gatewayv1.HTTPSProtocolType, Port: DefaultHTTPSPort,
			Hostname: ptr.To(gatewayv1.Hostname(hostname)),
			TLS: &gatewayv1.ListenerTLSConfig{Mode: ptr.To(gatewayv1.TLSModeTerminate), Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
			}},
		})
	})
	domain := newDomain(upstreamNamespace.Name, hostname, func(d *networkingv1alpha.Domain) {
		d.Spec.DomainName = hostname
		apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue})
	})
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}

	now := time.Now()
	servingCrt, servingKey := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	issuedCrt, issuedKey := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(90*24*time.Hour))

	cert := &certificatesv1alpha1.TLSCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: certName},
		Spec:       certificatesv1alpha1.TLSCertificateSpec{DNSNames: []certificatesv1alpha1.DNSName{hostname}, Issuance: certificatesv1alpha1.IssuanceModeAuto, SecretName: secretName},
		Status: certificatesv1alpha1.TLSCertificateStatus{
			NotAfter:         &metav1.Time{Time: now.Add(90 * 24 * time.Hour)},
			ServiceSecretRef: &certificatesv1alpha1.ServiceSecretReference{Namespace: serviceNS, Name: "issued"},
			Conditions:       []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued"}},
		},
	}
	require.NoError(t, controllerutil.SetControllerReference(upstreamGateway, cert, testScheme))
	serving := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: secretName}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{"tls.crt": servingCrt, "tls.key": servingKey}}
	issued := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: serviceNS, Name: "issued"}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{"tls.crt": issuedCrt, "tls.key": issuedKey}}
	for _, obj := range []client.Object{domain, gatewayClass, cert, serving, issued} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}

	fakeUpstreamClient := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass, cert).
		WithStatusSubresource(upstreamGateway, cert).Build()
	fakeDownstreamClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(serving).WithStatusSubresource(&gatewayv1.Gateway{}).Build()

	forbidden := true
	serviceClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(issued).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if forbidden {
				return apierrors.NewForbidden(corev1.Resource("secrets"), key.Name, nil)
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}).Build()

	reconciler := &GatewayReconciler{
		mgr:                      &fakeMockManager{cl: fakeUpstreamClient},
		Config:                   testCfg,
		DownstreamCluster:        &fakeCluster{cl: fakeDownstreamClient},
		CertificateServiceReader: serviceClient,
	}
	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)
	reconciler.prepareUpstreamGateway(upstreamGateway)

	reconcile := func() *gatewayv1.Gateway {
		var current gatewayv1.Gateway
		require.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamGateway), &current))
		result, _ := reconciler.ensureDownstreamGateway(ctx, "test", fakeUpstreamClient, &current, downstreamStrategy)
		require.NoError(t, result.Err)
		_, err := result.Complete(ctx)
		require.NoError(t, err)
		require.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamGateway), &current))

		var claims corev1.ConfigMapList
		require.NoError(t, fakeDownstreamClient.List(ctx, &claims, client.InNamespace(testCfg.Gateway.DownstreamHostnameAccountingNamespace)))
		for i := range claims.Items {
			stampCreated(t, ctx, fakeDownstreamClient, &claims.Items[i])
		}
		var downstreamGateways gatewayv1.GatewayList
		require.NoError(t, fakeDownstreamClient.List(ctx, &downstreamGateways, client.InNamespace(downstreamNamespaceName)))
		for i := range downstreamGateways.Items {
			stampCreated(t, ctx, fakeDownstreamClient, &downstreamGateways.Items[i])
		}
		return &current
	}

	before := counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed)
	first := reconcile()
	assertListenerRenewalBlocked(t, first, "keep trying")
	assert.Equal(t, before+1, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed))

	forbidden = false
	reconciler.certificateServiceFailures.Delete(upstreamGateway.UID)
	second := reconcile()
	for _, ls := range second.Status.Listeners {
		if ls.Name == listenerName {
			assert.Nil(t, apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateRenewalBlocked), "a recovered renewal no longer reads as blocked")
		}
	}
	var mirror corev1.Secret
	require.NoError(t, fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &mirror))
	assert.Equal(t, issuedCrt, mirror.Data["tls.crt"], "the issued certificate replaces the serving one once readable")
	assert.Equal(t, before+1, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed), "a success adds nothing")

	removed := second.DeepCopy()
	removed.Spec.Listeners = removed.Spec.Listeners[:len(removed.Spec.Listeners)-1]
	reconciler.forgetRemovedListeners(removed)
	var m dto.Metric
	require.NoError(t, certificateServiceFailuresTotal.WithLabelValues(upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed).Write(&m))
	assert.Zero(t, m.GetCounter().GetValue(), "a removed listener's series is dropped")
}

func TestCertificateServiceRejectionCountedPerTransition(t *testing.T) {
	r := &GatewayReconciler{}
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "count", Name: "gw", UID: uuid.NewUUID()}}
	before := counterValue(t, certificateServiceFailuresTotal, "count", "gw", "https-0", certificateServiceReasonRejected)
	r.recordCertificateServiceRejection(gw, "https-0", true)
	r.recordCertificateServiceRejection(gw, "https-0", true)
	r.recordCertificateServiceRejection(gw, "https-0", true)
	assert.Equal(t, before+1, counterValue(t, certificateServiceFailuresTotal, "count", "gw", "https-0", certificateServiceReasonRejected))
	r.recordCertificateServiceRejection(gw, "https-0", false)
	r.recordCertificateServiceRejection(gw, "https-0", true)
	assert.Equal(t, before+2, counterValue(t, certificateServiceFailuresTotal, "count", "gw", "https-0", certificateServiceReasonRejected), "a new rejection after recovery counts again")
}

// stampCreated gives an object the fake client created a creation timestamp,
// which the real API server sets and the controller reads to tell a fresh
// object from an existing one.
func stampCreated(t *testing.T, ctx context.Context, cl client.Client, obj client.Object) {
	t.Helper()
	if created := obj.GetCreationTimestamp(); !created.IsZero() {
		return
	}
	obj.SetCreationTimestamp(metav1.Now())
	require.NoError(t, cl.Update(ctx, obj))
}
