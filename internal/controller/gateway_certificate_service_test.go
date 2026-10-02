// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		CertificateService: config.CertificateServiceConfig{Enabled: true},
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
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "tok-a", Key: "key-a", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: "evil.example.com", Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "tok-b", Key: "key-b", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeDNS01, Token: "tok-c", Key: "key-c", State: certificatesv1alpha1.ChallengeStatePending},
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "tok-d", Key: "key-d", State: certificatesv1alpha1.ChallengeStateInvalid},
					}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				served := tlsCertificateSolverName(certName, "tok-a")

				var route gatewayv1.HTTPRoute
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: served}, &route))
				assert.Equal(t, labelValueTrue, route.Labels[http01SolverLabel])
				assert.Equal(t, certName, route.Labels[tlsCertificateSolverLabel])
				require.Len(t, route.Spec.Rules, 1)
				require.Len(t, route.Spec.Rules[0].Matches, 1)
				assert.Equal(t, "/.well-known/acme-challenge/tok-a", ptr.Deref(route.Spec.Rules[0].Matches[0].Path.Value, ""))
				assert.Equal(t, gatewayv1.ObjectName(gatewayName), route.Spec.ParentRefs[0].Name)
				owner := metav1.GetControllerOf(&route)
				require.NotNil(t, owner)
				assert.Equal(t, KindGateway, owner.Kind)

				var filter envoygatewayv1alpha1.HTTPRouteFilter
				require.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: served}, &filter))
				assert.Equal(t, "key-a", ptr.Deref(filter.Spec.DirectResponse.Body.Inline, ""))

				for _, token := range []string{"tok-b", "tok-c", "tok-d"} {
					err := e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: tlsCertificateSolverName(certName, token)}, &gatewayv1.HTTPRoute{})
					assert.True(t, apierrors.IsNotFound(err), "no solver route for challenge %s", token)
				}
			},
		},
		{
			name: "removes solver routes once their challenge leaves status",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Status.Challenges = []certificatesv1alpha1.ACMEChallenge{
						{DNSName: hostname, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "tok-keep", Key: "key-keep", State: certificatesv1alpha1.ChallengeStateValid},
					}
				})}
			},
			downstreamObjects: func() []client.Object {
				gw := downstreamGatewaySeed()
				objs := make([]client.Object, 0, 5)
				objs = append(objs, gw)
				objs = append(objs, solverObjects(gw, "tok-keep")...)
				objs = append(objs, solverObjects(gw, "tok-old")...)
				return objs
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				assert.NoError(t, e.downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: tlsCertificateSolverName(certName, "tok-keep")}, &gatewayv1.HTTPRoute{}), "a Valid challenge keeps its answer until the service drops it")

				old := tlsCertificateSolverName(certName, "tok-old")
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
			name: "switches to the service when the cert-manager certificate is due for renewal",
			downstreamObjects: func() []client.Object {
				legacy := newLegacyCertificate(now.Add(-time.Hour))
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

			fakeServiceClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.serviceObjects...).
				Build()

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

func TestTLSCertificateName(t *testing.T) {
	long := tlsCertificateName("a-gateway-with-a-deliberately-very-long-name-for-this-test", "https-hostname-with-a-long-name-0")
	assert.LessOrEqual(t, len(long), 63)
	assert.Equal(t, "gw-https-0", tlsCertificateName("gw", "https-0"))
}
