// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func TestCertificateServiceWildcardHostname(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)

	const (
		gatewayName     = "s3"
		listenerName    = gatewayv1.SectionName("https-hostname-0")
		wildcard        = "*.s3.example.com"
		serviceNS       = "certificates-system"
		upstreamCluster = "test"
	)
	certName := tlsCertificateName(gatewayName, listenerName)
	secretName := listenerCertificateSecretName(gatewayName, listenerName)
	certUID := uuid.NewUUID()
	now := time.Now()

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)

	cfg := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
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
	}}

	dnsVerified := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "example.com"},
		Spec:       networkingv1alpha.DomainSpec{DomainName: "example.com"},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{
			{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
			{Type: networkingv1alpha.DomainConditionVerifiedDNS, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
		}},
	}

	newGatewayWithWildcard := func() *gatewayv1.Gateway {
		return newGateway(cfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
			g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
				Name:     listenerName,
				Protocol: gatewayv1.HTTPSProtocolType,
				Port:     DefaultHTTPSPort,
				Hostname: ptr.To(gatewayv1.Hostname(wildcard)),
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{From: ptr.To(gatewayv1.NamespacesFromSame)},
				},
				TLS: &gatewayv1.ListenerTLSConfig{
					Mode: ptr.To(gatewayv1.TLSModeTerminate),
					Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
						gatewayv1.AnnotationKey(certificateIssuerTLSOption): autoIssuerSentinel,
					},
				},
			})
		})
	}

	readyCertificate := func(owner *gatewayv1.Gateway, mutate func(*certificatesv1alpha1.TLSCertificate)) *certificatesv1alpha1.TLSCertificate {
		cert := &certificatesv1alpha1.TLSCertificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: certName, UID: certUID},
			Spec: certificatesv1alpha1.TLSCertificateSpec{
				DNSNames: []certificatesv1alpha1.DNSName{wildcard},
				Issuance: certificatesv1alpha1.IssuanceModeDNS01,
			},
		}
		require.NoError(t, controllerutil.SetControllerReference(owner, cert, testScheme))
		if mutate != nil {
			mutate(cert)
		}
		return cert
	}
	issued := func(cert *certificatesv1alpha1.TLSCertificate) {
		cert.Status.Issuance = certificatesv1alpha1.ChallengeTypeDNS01
		cert.Status.NotBefore = &metav1.Time{Time: now.Add(-time.Hour)}
		cert.Status.NotAfter = &metav1.Time{Time: now.Add(60 * 24 * time.Hour)}
		apimeta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionTrue, Reason: "Accepted"})
		apimeta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued"})
	}
	serviceMaterial := func(dnsName string) *corev1.Secret {
		crt, key := generateTLSKeyPair(t, dnsName, now.Add(-time.Hour), now.Add(60*24*time.Hour))
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: serviceNS, Name: certificatesv1alpha1.StoredSecretName(certUID)},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
		}
	}

	type outcome struct {
		upstream   client.Client
		downstream client.Client
		reconciler *GatewayReconciler
		gateway    *gatewayv1.Gateway
	}
	run := func(t *testing.T, certificate func(*gatewayv1.Gateway) *certificatesv1alpha1.TLSCertificate, service ...client.Object) outcome {
		t.Helper()
		upstreamGateway := newGatewayWithWildcard()
		upstreamObjects := []client.Object{
			upstreamGateway, upstreamNamespace.DeepCopy(), dnsVerified.DeepCopy(),
			&gatewayv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController("test")},
			},
		}
		if certificate != nil {
			upstreamObjects = append(upstreamObjects, certificate(upstreamGateway))
		}

		upstream := fake.NewClientBuilder().WithScheme(testScheme).
			WithObjects(upstreamObjects...).
			WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).
			Build()
		downstream := fake.NewClientBuilder().WithScheme(testScheme).
			WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).
			WithStatusSubresource(&gatewayv1.Gateway{}, &cmv1.Certificate{}).
			Build()
		reconciler := &GatewayReconciler{
			mgr:                      &fakeMockManager{cl: upstream},
			Config:                   cfg,
			DownstreamCluster:        &fakeCluster{cl: downstream},
			CertificateServiceReader: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(service...).Build(),
		}

		ctx := context.Background()
		reconciler.prepareUpstreamGateway(upstreamGateway)
		strategy := downstreamclient.NewMappedNamespaceResourceStrategy(upstreamCluster, upstream, downstream)
		result, _ := reconciler.ensureDownstreamGateway(ctx, upstreamCluster, upstream, upstreamGateway, strategy)
		require.NoError(t, result.Err)
		_, err := result.Complete(ctx)
		require.NoError(t, err)

		return outcome{upstream: upstream, downstream: downstream, reconciler: reconciler, gateway: upstreamGateway}
	}

	t.Run("requests one DNS-01 certificate for a DNS-proven wildcard alone", func(t *testing.T) {
		o := run(t, nil)

		var cert certificatesv1alpha1.TLSCertificate
		require.NoError(t, o.upstream.Get(context.Background(), client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
		assert.Equal(t, []certificatesv1alpha1.DNSName{wildcard}, cert.Spec.DNSNames, "only the wildcard, not its base: one label of coverage")
		assert.Equal(t, certificatesv1alpha1.IssuanceModeDNS01, cert.Spec.Issuance)

		var legacy cmv1.CertificateList
		require.NoError(t, o.downstream.List(context.Background(), &legacy))
		assert.Empty(t, legacy.Items, "cert-manager never orders for the wildcard")
	})

	t.Run("never answers over HTTP for a wildcard, whatever the challenge list says", func(t *testing.T) {
		o := run(t, func(gw *gatewayv1.Gateway) *certificatesv1alpha1.TLSCertificate {
			return readyCertificate(gw, func(c *certificatesv1alpha1.TLSCertificate) {
				c.Status.Challenges = []certificatesv1alpha1.ACMEChallenge{{
					DNSName: wildcard, Type: certificatesv1alpha1.ChallengeTypeHTTP01, Token: "token-wildwildwildwild", Key: "key", State: certificatesv1alpha1.ChallengeStatePending,
				}}
			})
		})

		var routes gatewayv1.HTTPRouteList
		require.NoError(t, o.downstream.List(context.Background(), &routes, client.InNamespace(downstreamNamespaceName), client.HasLabels{"meta.datumapis.com/http01-solver"}))
		assert.Empty(t, routes.Items, "an HTTP-01 route for a wildcard would answer for every name beneath it")
	})

	t.Run("mirrors a certificate that covers the wildcard and admits the listener", func(t *testing.T) {
		o := run(t, func(gw *gatewayv1.Gateway) *certificatesv1alpha1.TLSCertificate {
			return readyCertificate(gw, issued)
		}, serviceMaterial(wildcard))

		var mirror corev1.Secret
		require.NoError(t, o.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &mirror))

		health := o.reconciler.evaluateListenerCertHealth(context.Background(), o.upstream, o.downstream, downstreamNamespaceName, o.gateway, []string{wildcard})
		assert.True(t, health[listenerName].healthy, health[listenerName].message)
		assert.NotNil(t, gatewayutil.GetListenerByName(o.gateway.Spec.Listeners, listenerName))
	})

	t.Run("refuses a broader wildcard certificate that does not name the listener's wildcard", func(t *testing.T) {
		o := run(t, func(gw *gatewayv1.Gateway) *certificatesv1alpha1.TLSCertificate {
			return readyCertificate(gw, issued)
		}, serviceMaterial("*.example.com"))

		err := o.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &corev1.Secret{})
		assert.True(t, apierrors.IsNotFound(err), "*.example.com does not cover *.s3.example.com")
	})
}

func TestValidateIssuedMaterialCoversOneLabel(t *testing.T) {
	now := time.Now()
	crt, key := generateTLSKeyPair(t, "*.s3.example.com", now.Add(-time.Hour), now.Add(24*time.Hour))

	assert.NoError(t, validateIssuedMaterial(crt, key, "*.s3.example.com", now))
	assert.NoError(t, validateIssuedMaterial(crt, key, "bucket.s3.example.com", now))
	assert.Error(t, validateIssuedMaterial(crt, key, "a.bucket.s3.example.com", now), "a wildcard certificate covers one label only")

	exact, exactKey := generateTLSKeyPair(t, "bucket.s3.example.com", now.Add(-time.Hour), now.Add(24*time.Hour))
	assert.Error(t, validateIssuedMaterial(exact, exactKey, "*.s3.example.com", now))
}
