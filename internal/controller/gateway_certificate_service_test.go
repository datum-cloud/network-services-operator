// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/validation/field"
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

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCA{cert: cert, key: key, pool: pool}
}

func (ca testCA) issue(t *testing.T, hostname string, notBefore, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func tlsListener(name gatewayv1.SectionName, hostname string) gatewayv1.Listener {
	return gatewayv1.Listener{
		Name:     name,
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
}

func dnsProvenDomain(namespace string) *networkingv1alpha.Domain {
	return &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "example.com"},
		Spec:       networkingv1alpha.DomainSpec{DomainName: "example.com"},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{
			{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
			{Type: networkingv1alpha.DomainConditionVerifiedDNS, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
		}},
	}
}

func certificateServiceGatewayConfig() config.GatewayConfig {
	return config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		DefaultListenerTLSSecretName:          "wildcard-test-suite-tls",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
		ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
			gatewayv1.AnnotationKey(certificateIssuerTLSOption): autoIssuerSentinel,
		},
		ClusterIssuerMap:   map[string]string{autoIssuerSentinel: "datum-gateway-http"},
		CertificateService: config.CertificateServiceConfig{Enabled: true, SecretNamespace: "certificates-system", VerifyChain: true},
	}
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
		hostname        = "*.shop.example.com"
		serviceNS       = "certificates-system"
		serviceSecret   = "svc-secret"
		upstreamCluster = "test"
	)
	certName := tlsCertificateName(gatewayName, listenerName)
	secretName := listenerCertificateSecretName(gatewayName, listenerName)
	testCfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	ca := newTestCA(t)

	now := time.Now()
	serviceCertPEM, serviceKeyPEM := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	projectCertPEM, projectKeyPEM := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	servingCertPEM, servingKeyPEM := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))

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
				Issuance:   certificatesv1alpha1.IssuanceModeDNS01,
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

	serving := func() []client.Object {
		return []client.Object{tlsSecret(downstreamNamespaceName, secretName, servingCertPEM, servingKeyPEM)}
	}

	type env struct {
		upstream   client.Client
		downstream client.Client
		result     Result
		reconciler *GatewayReconciler
	}

	tests := []struct {
		name              string
		upstreamObjects   func(gw *gatewayv1.Gateway) []client.Object
		upstreamCreate    func(obj client.Object) error
		downstreamObjects func() []client.Object
		serviceObjects    []client.Object
		serviceForbidden  bool
		assert            func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway)
	}{
		{
			name: "requests a DNS-01 TLSCertificate upstream and withholds the listener until it issues",
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				ctx := context.Background()
				var cert certificatesv1alpha1.TLSCertificate
				require.NoError(t, e.upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
				assert.Equal(t, []certificatesv1alpha1.DNSName{hostname}, cert.Spec.DNSNames)
				assert.Equal(t, certificatesv1alpha1.IssuanceModeDNS01, cert.Spec.Issuance)
				assert.Equal(t, secretName, cert.Spec.SecretName)
				assert.True(t, metav1.IsControlledBy(&cert, upstreamGateway), "TLSCertificate should be controlled by the upstream Gateway")

				var legacy cmv1.CertificateList
				require.NoError(t, e.downstream.List(ctx, &legacy))
				assert.Empty(t, legacy.Items, "no cert-manager Certificate is created for a wildcard")

				var routes gatewayv1.HTTPRouteList
				require.NoError(t, e.downstream.List(ctx, &routes, client.HasLabels{"meta.datumapis.com/http01-solver"}))
				assert.Empty(t, routes.Items, "a wildcard is never answered over HTTP")

				assert.Nil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "listener must be withheld until a certificate is issued")
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, gatewayutil.DefaultHTTPSListenerName), "shared wildcard listener is untouched")
				assert.Equal(t, time.Minute, e.result.RequeueAfter, "a listener waiting on issuance is looked at again soon")
			},
		},
		{
			name: "moves an existing request onto DNS-01",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Spec.Issuance = certificatesv1alpha1.IssuanceModeAuto
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var cert certificatesv1alpha1.TLSCertificate
				require.NoError(t, e.upstream.Get(context.Background(), client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
				assert.Equal(t, certificatesv1alpha1.IssuanceModeDNS01, cert.Spec.Issuance)
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

				health := e.reconciler.evaluateListenerCertHealth(ctx, e.upstream, e.downstream, downstreamNamespaceName, upstreamGateway, []string{hostname})
				status, gated := health[listenerName]
				require.True(t, gated)
				assert.True(t, status.healthy, "listener is admitted on the next pass: %s", status.message)
				assert.Empty(t, status.renewalBlocked)
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
				resolved := listenerCondition(t, upstreamGateway, listenerName, string(gatewayv1.ListenerConditionResolvedRefs))
				assert.Equal(t, metav1.ConditionFalse, resolved.Status)
				assert.Contains(t, resolved.Message, "names under datum.net are denied")
				assert.NotContains(t, resolved.Message, "keep trying")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRejected), 1.0)
			},
		},
		{
			name: "a request the API refuses tells the customer why instead of promising to keep trying",
			upstreamCreate: func(obj client.Object) error {
				if _, ok := obj.(*certificatesv1alpha1.TLSCertificate); !ok {
					return nil
				}
				return apierrors.NewInvalid(schema.GroupKind{Group: "certificates.miloapis.com", Kind: KindTLSCertificate}, certName,
					field.ErrorList{field.Forbidden(field.NewPath("spec", "dnsNames"), "names under datum.net are denied")})
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				blocked := listenerCondition(t, upstreamGateway, listenerName, listenerConditionCertificateIssuanceBlocked)
				assert.Equal(t, metav1.ConditionTrue, blocked.Status)
				assert.Contains(t, blocked.Message, "names under datum.net are denied")
				assert.NotContains(t, blocked.Message, "keep trying")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRefused), 1.0)
				assert.Equal(t, certificateServiceBackoffBase, e.result.RequeueAfter)
			},
		},
		{
			name: "never deletes a mismatched TLSCertificate another owner controls",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				other := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: "other-gw", UID: uuid.NewUUID()}}
				return []client.Object{newTLSCertificate(other, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Spec.DNSNames = []certificatesv1alpha1.DNSName{"*.previous.example.com"}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var cert certificatesv1alpha1.TLSCertificate
				require.NoError(t, e.upstream.Get(context.Background(), client.ObjectKey{Namespace: upstreamNamespace.Name, Name: certName}, &cert))
				assert.Equal(t, []certificatesv1alpha1.DNSName{"*.previous.example.com"}, cert.Spec.DNSNames, "someone else's TLSCertificate is left alone")
				assert.Equal(t, certificateServiceBackoffBase, e.result.RequeueAfter, "the clash is retried with backoff, not an error")
				resolved := listenerCondition(t, upstreamGateway, listenerName, string(gatewayv1.ListenerConditionResolvedRefs))
				assert.Contains(t, resolved.Message, "another resource holds the certificate request")
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
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonNamespaceRefused), 1.0)
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
				crt, key := ca.issue(t, "*.example.com", now.Add(-time.Hour), now.Add(60*24*time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.True(t, apierrors.IsNotFound(e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &corev1.Secret{})), "*.example.com does not cover *.shop.example.com")
			},
		},
		{
			name: "refuses a chain the configured roots do not trust and keeps the serving Secret",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.NotAfter = &metav1.Time{Time: now.Add(61 * 24 * time.Hour)}
				})}
			},
			downstreamObjects: serving,
			serviceObjects: func() []client.Object {
				crt, key := generateTLSKeyPair(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var secret corev1.Secret
				require.NoError(t, e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.Equal(t, servingCertPEM, secret.Data["tls.crt"], "an untrusted chain never replaces what serves")
				blocked := assertListenerRenewalBlocked(t, upstreamGateway, "did not pass our checks")
				assert.NotContains(t, blocked.Message, "keep trying")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonUntrustedChain), 1.0)
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
			downstreamObjects: serving,
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName), "the listener keeps serving")
				assertListenerRenewalBlocked(t, upstreamGateway, "names under datum.net are denied")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRejected), 1.0)
			},
		},
		{
			name: "a failed renewal order while the previous certificate serves is reported and counted",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.NotAfter = &metav1.Time{Time: now.Add(60 * 24 * time.Hour).Add(time.Second)}
					apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
						Type: certificatesv1alpha1.ConditionIssuing, Status: metav1.ConditionFalse, Reason: "IssuanceFailed", Message: "DNS problem: NXDOMAIN looking up TXT",
					})
				})}
			},
			downstreamObjects: serving,
			serviceObjects:    []client.Object{tlsSecret(serviceNS, serviceSecret, serviceCertPEM, serviceKeyPEM)},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName))
				assertListenerRenewalBlocked(t, upstreamGateway, "NXDOMAIN")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonIssuanceFailed), 1.0)
			},
		},
		{
			name: "a request stuck short of Ready with nothing serving is reported as blocked issuance",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Status.Conditions = []metav1.Condition{{
						Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "SecretConflict",
						Message: `Secret "x" exists and is not managed by this TLSCertificate.`, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Hour)),
					}}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				blocked := listenerCondition(t, upstreamGateway, listenerName, listenerConditionCertificateIssuanceBlocked)
				assert.Equal(t, metav1.ConditionTrue, blocked.Status)
				assert.Contains(t, blocked.Message, "is not managed by this TLSCertificate")
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonNotReady), 1.0)
				assert.Equal(t, 1.0, gaugeValue(t, certificateServiceListenerFailing, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonNotReady), "a lasting failure stays visible after its one count")
			},
		},
		{
			name: "waiting on the customer's DNS records is progress, not a failure",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					c.Status.Conditions = []metav1.Condition{
						{Type: certificatesv1alpha1.ConditionDNSDelegationReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: metav1.NewTime(now.Add(-48 * time.Hour))},
						{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: metav1.NewTime(now.Add(-48 * time.Hour))},
					}
				})}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				for _, ls := range upstreamGateway.Status.Listeners {
					if ls.Name == listenerName {
						assert.Nil(t, apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateIssuanceBlocked))
					}
				}
			},
		},
		{
			name: "a served certificate past its renewal point with no newer issuance is overdue",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.NotBefore = &metav1.Time{Time: now.Add(-80 * 24 * time.Hour)}
					c.Status.NotAfter = &metav1.Time{Time: now.Add(10 * 24 * time.Hour)}
				})}
			},
			downstreamObjects: func() []client.Object {
				crt, key := ca.issue(t, hostname, now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
				return []client.Object{tlsSecret(downstreamNamespaceName, secretName, crt, key)}
			},
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, listenerName))
				assertListenerRenewalBlocked(t, upstreamGateway, "has not been renewed")
				assert.Equal(t, 1.0, gaugeValue(t, certificateServiceListenerFailing, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRenewalOverdue))
				assert.GreaterOrEqual(t, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonRenewalOverdue), 1.0)
			},
		},
		{
			name: "a forbidden service-side read while the previous certificate serves is reported as a blocked renewal",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.NotAfter = &metav1.Time{Time: now.Add(90 * 24 * time.Hour)}
				})}
			},
			downstreamObjects: serving,
			serviceForbidden:  true,
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
				blocked := listenerCondition(t, upstreamGateway, listenerName, listenerConditionCertificateIssuanceBlocked)
				assert.Equal(t, metav1.ConditionTrue, blocked.Status)
				assert.Equal(t, listenerReasonIssuanceFailing, blocked.Reason)
				assert.Contains(t, blocked.Message, "keep trying")
			},
		},
		{
			name: "refuses expired service-side material and keeps the serving Secret",
			upstreamObjects: func(gw *gatewayv1.Gateway) []client.Object {
				return []client.Object{newTLSCertificate(gw, certName, func(c *certificatesv1alpha1.TLSCertificate) {
					readyStatus(c)
					c.Status.NotAfter = &metav1.Time{Time: now.Add(-time.Hour)}
				})}
			},
			downstreamObjects: serving,
			serviceObjects: func() []client.Object {
				crt, key := ca.issue(t, hostname, now.Add(-48*time.Hour), now.Add(-time.Hour))
				return []client.Object{tlsSecret(serviceNS, serviceSecret, crt, key)}
			}(),
			assert: func(t *testing.T, e env, upstreamGateway, downstreamGateway *gatewayv1.Gateway) {
				var secret corev1.Secret
				require.NoError(t, e.downstream.Get(context.Background(), client.ObjectKey{Namespace: downstreamNamespaceName, Name: secretName}, &secret))
				assert.Equal(t, servingCertPEM, secret.Data["tls.crt"], "a valid serving Secret is never replaced by material that fails validation")
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
					c.Spec.DNSNames = []certificatesv1alpha1.DNSName{"*.previous.example.com"}
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
				g.UID = uuid.NewUUID()
				g.Spec.Listeners = append(g.Spec.Listeners, tlsListener(listenerName, hostname))
			})

			upstreamObjects := []client.Object{
				dnsProvenDomain(upstreamNamespace.Name),
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

			upstreamBuilder := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(upstreamGateway, upstreamNamespace).
				WithObjects(upstreamObjects...).
				WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).
				WithStatusSubresource(upstreamObjects...)
			if tt.upstreamCreate != nil {
				upstreamBuilder = upstreamBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if err := tt.upstreamCreate(obj); err != nil {
							return err
						}
						return cl.Create(ctx, obj, opts...)
					},
				})
			}
			fakeUpstreamClient := upstreamBuilder.Build()

			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).
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

			ctx := log.IntoContext(context.Background(), logger)

			reconciler := &GatewayReconciler{
				mgr:                      &fakeMockManager{cl: fakeUpstreamClient},
				Config:                   testCfg,
				DownstreamCluster:        &fakeCluster{cl: fakeDownstreamClient},
				CertificateServiceReader: serviceBuilder.Build(),
				CertificateServiceRoots:  ca.pool,
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
				result:     result,
				reconciler: reconciler,
			}, updatedUpstream, downstreamGateway)
		})
	}
}

func listenerCondition(t *testing.T, gateway *gatewayv1.Gateway, listener gatewayv1.SectionName, conditionType string) *metav1.Condition {
	t.Helper()
	for _, ls := range gateway.Status.Listeners {
		if ls.Name != listener {
			continue
		}
		condition := apimeta.FindStatusCondition(ls.Conditions, conditionType)
		require.NotNil(t, condition, "listener %s has no %s condition", listener, conditionType)
		return condition
	}
	t.Fatalf("no status for listener %s", listener)
	return nil
}

func assertListenerRenewalBlocked(t *testing.T, gateway *gatewayv1.Gateway, want string) *metav1.Condition {
	t.Helper()
	for _, ls := range gateway.Status.Listeners {
		blocked := apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateRenewalBlocked)
		if blocked == nil {
			continue
		}
		resolved := apimeta.FindStatusCondition(ls.Conditions, string(gatewayv1.ListenerConditionResolvedRefs))
		require.NotNil(t, resolved)
		assert.Equal(t, metav1.ConditionTrue, resolved.Status, "the listener itself stays resolved")
		assert.Equal(t, metav1.ConditionTrue, blocked.Status)
		assert.Equal(t, listenerReasonRenewalFailing, blocked.Reason)
		assert.Contains(t, blocked.Message, want)
		return blocked
	}
	t.Fatal("no listener reports a blocked renewal")
	return nil
}

func seriesInNamespace(t *testing.T, vec prometheus.Collector, namespace string) int {
	t.Helper()
	ch := make(chan prometheus.Metric, 1024)
	vec.Collect(ch)
	close(ch)
	count := 0
	for metric := range ch {
		var m dto.Metric
		require.NoError(t, metric.Write(&m))
		for _, label := range m.GetLabel() {
			if label.GetName() == jsonKeyNamespace && label.GetValue() == namespace {
				count++
			}
		}
	}
	return count
}

func gaugeValue(t *testing.T, vec *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(labels...).Write(&m))
	return m.GetGauge().GetValue()
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(labels...).Write(&m))
	return m.GetCounter().GetValue()
}

// certificateServiceHarness drives full gateway reconciles against fake
// clients that persist across passes, so a test can change the operator's
// config between them as a rollout would.
type certificateServiceHarness struct {
	t          *testing.T
	ctx        context.Context
	cfg        config.NetworkServicesOperator
	upstream   client.Client
	downstream client.Client
	service    client.Reader
	roots      *x509.CertPool
	gateway    client.ObjectKey
	namespace  string
	reconciler *GatewayReconciler
}

func (h *certificateServiceHarness) reconcile() (*gatewayv1.Gateway, *gatewayv1.Gateway, Result) {
	h.t.Helper()
	if h.reconciler == nil || h.reconciler.Config.Gateway.CertificateService.Enabled != h.cfg.Gateway.CertificateService.Enabled {
		h.reconciler = &GatewayReconciler{
			mgr:                      &fakeMockManager{cl: h.upstream},
			Config:                   h.cfg,
			DownstreamCluster:        &fakeCluster{cl: h.downstream},
			CertificateServiceReader: h.service,
			CertificateServiceRoots:  h.roots,
		}
	}
	var current gatewayv1.Gateway
	require.NoError(h.t, h.upstream.Get(h.ctx, h.gateway, &current))
	h.reconciler.prepareUpstreamGateway(&current)
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", h.upstream, h.downstream)
	result, downstreamGateway := h.reconciler.ensureDownstreamGateway(h.ctx, "test", h.upstream, &current, strategy)
	require.NoError(h.t, result.Err)
	_, err := result.Complete(h.ctx)
	require.NoError(h.t, err)
	require.NoError(h.t, h.upstream.Get(h.ctx, h.gateway, &current))

	var claims corev1.ConfigMapList
	require.NoError(h.t, h.downstream.List(h.ctx, &claims, client.InNamespace(h.cfg.Gateway.DownstreamHostnameAccountingNamespace)))
	for i := range claims.Items {
		stampCreated(h.t, h.ctx, h.downstream, &claims.Items[i])
	}
	var gateways gatewayv1.GatewayList
	require.NoError(h.t, h.downstream.List(h.ctx, &gateways, client.InNamespace(h.namespace)))
	for i := range gateways.Items {
		stampCreated(h.t, h.ctx, h.downstream, &gateways.Items[i])
	}
	var certs cmv1.CertificateList
	require.NoError(h.t, h.downstream.List(h.ctx, &certs, client.InNamespace(h.namespace)))
	for i := range certs.Items {
		stampCreated(h.t, h.ctx, h.downstream, &certs.Items[i])
	}
	var secrets corev1.SecretList
	require.NoError(h.t, h.downstream.List(h.ctx, &secrets, client.InNamespace(h.namespace)))
	for i := range secrets.Items {
		stampCreated(h.t, h.ctx, h.downstream, &secrets.Items[i])
	}
	var tlsCerts certificatesv1alpha1.TLSCertificateList
	require.NoError(h.t, h.upstream.List(h.ctx, &tlsCerts))
	for i := range tlsCerts.Items {
		stampCreated(h.t, h.ctx, h.upstream, &tlsCerts.Items[i])
	}
	return &current, downstreamGateway, result
}

func TestCertificateServiceLeavesExactHostnamesOnCertManager(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	const (
		gatewayName = "mixed-gw"
		serviceNS   = "certificates-system"
		exact       = "app.example.com"
		wildcard    = "*.shop.example.com"
	)
	exactListener := gatewayv1.SectionName("https-hostname-0")
	wildcardListenerName := gatewayv1.SectionName("https-hostname-1")
	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "mixed", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	ca := newTestCA(t)
	now := time.Now()

	cfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	upstreamGateway := newGateway(cfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
		g.UID = uuid.NewUUID()
		g.Spec.Listeners = append(g.Spec.Listeners, tlsListener(exactListener, exact), tlsListener(wildcardListenerName, wildcard))
	})
	domain := dnsProvenDomain(upstreamNamespace.Name)
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}
	for _, obj := range []client.Object{domain, gatewayClass} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}

	var tlsCertificateCreates, tlsCertificateDeletes atomic.Int32
	var requestedNames []string
	upstream := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass).
		WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if cert, ok := obj.(*certificatesv1alpha1.TLSCertificate); ok {
					tlsCertificateCreates.Add(1)
					for _, name := range cert.Spec.DNSNames {
						requestedNames = append(requestedNames, string(name))
					}
				}
				return cl.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*certificatesv1alpha1.TLSCertificate); ok {
					tlsCertificateDeletes.Add(1)
				}
				return cl.Delete(ctx, obj, opts...)
			},
		}).Build()
	certificateWrites := map[string]int{}
	countCertificate := func(obj client.Object, verb string) {
		if _, ok := obj.(*cmv1.Certificate); ok {
			certificateWrites[verb+" "+obj.GetName()]++
		}
	}
	downstream := fake.NewClientBuilder().WithScheme(testScheme).
		WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).
		WithStatusSubresource(&gatewayv1.Gateway{}, &cmv1.Certificate{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				countCertificate(obj, "create")
				return cl.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				countCertificate(obj, "delete")
				return cl.Delete(ctx, obj, opts...)
			},
		}).Build()

	issuedCrt, issuedKey := ca.issue(t, wildcard, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	service := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: serviceNS, Name: "issued"},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": issuedCrt, "tls.key": issuedKey},
	}).Build()

	h := &certificateServiceHarness{
		t: t, ctx: ctx, cfg: cfg, upstream: upstream, downstream: downstream, service: service, roots: ca.pool,
		gateway: client.ObjectKeyFromObject(upstreamGateway), namespace: downstreamNamespaceName,
	}

	exactCertificate := func() *cmv1.Certificate {
		var cert cmv1.Certificate
		require.NoError(t, downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: listenerCertificateName(gatewayName, exactListener)}, &cert))
		return &cert
	}

	h.reconcile()

	assert.Equal(t, []string{wildcard}, requestedNames, "only the wildcard is handed to the certificate service")
	assert.Equal(t, []string{exact}, exactCertificate().Spec.DNSNames, "the exact hostname gets its cert-manager Certificate exactly as before")
	assert.True(t, apierrors.IsNotFound(downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: listenerCertificateName(gatewayName, wildcardListenerName)}, &cmv1.Certificate{})),
		"cert-manager never orders for the wildcard while the service is on")
	exactName := listenerCertificateName(gatewayName, exactListener)
	require.Equal(t, 1, certificateWrites["create "+exactName])

	var cert certificatesv1alpha1.TLSCertificate
	require.NoError(t, upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: tlsCertificateName(gatewayName, wildcardListenerName)}, &cert))
	cert.Status = certificatesv1alpha1.TLSCertificateStatus{
		NotBefore:        &metav1.Time{Time: now.Add(-time.Hour)},
		NotAfter:         &metav1.Time{Time: now.Add(60 * 24 * time.Hour)},
		ServiceSecretRef: &certificatesv1alpha1.ServiceSecretReference{Namespace: serviceNS, Name: "issued"},
		Conditions:       []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued", LastTransitionTime: metav1.Now()}},
	}
	require.NoError(t, upstream.Status().Update(ctx, &cert))
	h.reconcile()
	_, downstreamGateway, _ := h.reconcile()
	assert.NotNil(t, gatewayutil.GetListenerByName(downstreamGateway.Spec.Listeners, wildcardListenerName), "the wildcard serves the service-issued certificate")

	var mirrored corev1.Secret
	require.NoError(t, downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: listenerCertificateSecretName(gatewayName, wildcardListenerName)}, &mirrored))
	require.Equal(t, issuedCrt, mirrored.Data["tls.crt"])

	h.cfg.Gateway.CertificateService.Enabled = false
	h.reconcile()
	assert.Zero(t, seriesInNamespace(t, certificateServiceListenerFailing, upstreamNamespace.Name), "nothing reports as failing once the service is off")
	h.cfg.Gateway.CertificateService.Enabled = true
	h.reconcile()

	assert.Equal(t, int32(1), tlsCertificateCreates.Load(), "turning the service off and on again requests nothing new")
	assert.Zero(t, tlsCertificateDeletes.Load(), "turning the service off withdraws no request")
	assert.Equal(t, []string{wildcard}, requestedNames, "an exact hostname is never handed to the service, whatever the flag did")
	assert.Equal(t, 1, certificateWrites["create "+exactName], "the exact hostname's Certificate is never replaced")
	assert.Zero(t, certificateWrites["delete "+exactName])
	assert.Equal(t, []string{exact}, exactCertificate().Spec.DNSNames)
	require.NoError(t, upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: tlsCertificateName(gatewayName, wildcardListenerName)}, &cert))
	assert.Equal(t, []certificatesv1alpha1.DNSName{wildcard}, cert.Spec.DNSNames)

	var stillMirrored corev1.Secret
	require.NoError(t, downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: listenerCertificateSecretName(gatewayName, wildcardListenerName)}, &stillMirrored))
	assert.Equal(t, issuedCrt, stillMirrored.Data["tls.crt"], "the issued certificate is still the one served")
}

func TestCertificateServiceOneFailingListenerDoesNotDelayOthers(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	const (
		gatewayName = "two-gw"
		serviceNS   = "certificates-system"
		failing     = "*.broken.example.com"
		healthy     = "*.shop.example.com"
	)
	failingListener := gatewayv1.SectionName("https-hostname-0")
	healthyListener := gatewayv1.SectionName("https-hostname-1")
	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "two", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	ca := newTestCA(t)
	now := time.Now()

	cfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	upstreamGateway := newGateway(cfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
		g.UID = uuid.NewUUID()
		g.Spec.Listeners = append(g.Spec.Listeners, tlsListener(failingListener, failing), tlsListener(healthyListener, healthy))
	})
	domain := dnsProvenDomain(upstreamNamespace.Name)
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}
	for _, obj := range []client.Object{domain, gatewayClass} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}

	failingName := tlsCertificateName(gatewayName, failingListener)
	upstream := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(upstreamGateway, upstreamNamespace, domain, gatewayClass).
		WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if obj.GetName() == failingName {
					return apierrors.NewServiceUnavailable("webhook unavailable")
				}
				return cl.Create(ctx, obj, opts...)
			},
		}).Build()
	downstream := fake.NewClientBuilder().WithScheme(testScheme).WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).WithStatusSubresource(&gatewayv1.Gateway{}).Build()
	issuedCrt, issuedKey := ca.issue(t, healthy, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	service := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: serviceNS, Name: "issued"},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": issuedCrt, "tls.key": issuedKey},
	}).Build()

	h := &certificateServiceHarness{
		t: t, ctx: ctx, cfg: cfg, upstream: upstream, downstream: downstream, service: service, roots: ca.pool,
		gateway: client.ObjectKeyFromObject(upstreamGateway), namespace: downstreamNamespaceName,
	}

	first, _, _ := h.reconcile()
	assert.Contains(t, listenerCondition(t, first, failingListener, listenerConditionCertificateIssuanceBlocked).Message, "keep trying")

	var cert certificatesv1alpha1.TLSCertificate
	require.NoError(t, upstream.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: tlsCertificateName(gatewayName, healthyListener)}, &cert), "the healthy wildcard is requested in the same pass")
	cert.Status = certificatesv1alpha1.TLSCertificateStatus{
		NotBefore:        &metav1.Time{Time: now.Add(-time.Hour)},
		NotAfter:         &metav1.Time{Time: now.Add(60 * 24 * time.Hour)},
		ServiceSecretRef: &certificatesv1alpha1.ServiceSecretReference{Namespace: serviceNS, Name: "issued"},
		Conditions:       []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued", LastTransitionTime: metav1.Now()}},
	}
	require.NoError(t, upstream.Status().Update(ctx, &cert))

	second, _, result := h.reconcile()
	_, _, cooling := h.reconciler.certificateServiceInBackoff(certificateServiceKey{gateway: upstreamGateway.UID, listener: failingListener}, time.Now())
	assert.True(t, cooling, "the failing listener is still backing off")

	var mirrored corev1.Secret
	require.NoError(t, downstream.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: listenerCertificateSecretName(gatewayName, healthyListener)}, &mirrored), "the healthy wildcard is mirrored while the other backs off")
	assert.Equal(t, issuedCrt, mirrored.Data["tls.crt"])
	assert.Contains(t, listenerCondition(t, second, failingListener, listenerConditionCertificateIssuanceBlocked).Message, "keep trying", "the failing listener keeps its message")
	assert.LessOrEqual(t, result.RequeueAfter, certificateServiceBackoffMax)
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

func TestIsSingleLabelWildcard(t *testing.T) {
	assert.True(t, isSingleLabelWildcard("*.example.com"))
	assert.True(t, isSingleLabelWildcard("*.shop.example.com"))
	assert.False(t, isSingleLabelWildcard("app.example.com"))
	assert.False(t, isSingleLabelWildcard("*.*.example.com"))
	assert.False(t, isSingleLabelWildcard("*."))
	assert.False(t, isSingleLabelWildcard("a.*.example.com"))
}

func TestCertificateServiceRequeueBackoff(t *testing.T) {
	r := &GatewayReconciler{}
	key := certificateServiceKey{gateway: types.UID("gw"), listener: "https-0"}
	other := certificateServiceKey{gateway: types.UID("gw"), listener: "https-1"}
	now := time.Now()
	trying := certificateServiceIssue{reason: certificateServiceReasonStepFailed, message: "trying"}
	assert.Equal(t, 5*time.Second, r.certificateServiceRequeue(key, true, now, trying))
	assert.Equal(t, 10*time.Second, r.certificateServiceRequeue(key, true, now, trying))
	assert.Equal(t, 20*time.Second, r.certificateServiceRequeue(key, true, now, trying))
	for range 10 {
		r.certificateServiceRequeue(key, true, now, trying)
	}
	assert.Equal(t, certificateServiceBackoffMax, r.certificateServiceRequeue(key, true, now, trying))

	backoff, remaining, cooling := r.certificateServiceInBackoff(key, now.Add(time.Minute))
	assert.True(t, cooling, "an event inside the window repeats the message instead of the calls")
	assert.Equal(t, trying, backoff.issue)
	assert.Equal(t, certificateServiceBackoffMax-time.Minute, remaining)
	_, _, cooling = r.certificateServiceInBackoff(key, now.Add(certificateServiceBackoffMax))
	assert.False(t, cooling)
	_, _, cooling = r.certificateServiceInBackoff(other, now)
	assert.False(t, cooling, "another listener on the same gateway is not held back")

	assert.Zero(t, r.certificateServiceRequeue(key, false, now, certificateServiceIssue{}))
	assert.Equal(t, 5*time.Second, r.certificateServiceRequeue(key, true, now, trying), "a success resets the backoff")
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

func TestVerifyIssuedChain(t *testing.T) {
	now := time.Now()
	ca := newTestCA(t)
	other := newTestCA(t)
	crt, _ := ca.issue(t, "*.example.com", now.Add(-time.Hour), now.Add(24*time.Hour))
	selfSigned, _ := generateTLSKeyPair(t, "*.example.com", now.Add(-time.Hour), now.Add(24*time.Hour))

	assert.NoError(t, verifyIssuedChain(crt, "*.example.com", ca.pool, now))
	assert.Error(t, verifyIssuedChain(crt, "*.example.com", other.pool, now), "a chain from another CA is not trusted")
	assert.Error(t, verifyIssuedChain(selfSigned, "*.example.com", ca.pool, now), "a self-signed leaf is not trusted")
	assert.Error(t, verifyIssuedChain(crt, "*.other.com", ca.pool, now))
	assert.Error(t, verifyIssuedChain(nil, "*.example.com", ca.pool, now))
}

func TestTLSCertificateFailure(t *testing.T) {
	now := time.Now()
	old := metav1.NewTime(now.Add(-2 * tlsCertificateIssueGrace))
	fresh := metav1.NewTime(now.Add(-time.Minute))
	for _, tt := range []struct {
		name       string
		created    metav1.Time
		conditions []metav1.Condition
		want       string
	}{
		{name: "ready", created: old, conditions: []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue}}},
		{name: "rejected", created: fresh, conditions: []metav1.Condition{{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionFalse}}, want: certificateServiceReasonRejected},
		{name: "renewal order failed while ready", created: old, conditions: []metav1.Condition{
			{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue},
			{Type: certificatesv1alpha1.ConditionIssuing, Status: metav1.ConditionFalse, Reason: "IssuanceFailed"},
		}, want: certificateServiceReasonIssuanceFailed},
		{name: "fresh request with no status yet", created: fresh},
		{name: "request the service never engaged", created: old, want: certificateServiceReasonNotReady},
		{name: "pending past the grace", created: old, conditions: []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: old}}, want: certificateServiceReasonNotReady},
		{name: "pending within the grace", created: old, conditions: []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: fresh}}},
		{name: "waiting on the customer's records", created: old, conditions: []metav1.Condition{
			{Type: certificatesv1alpha1.ConditionDNSDelegationReady, Status: metav1.ConditionFalse, LastTransitionTime: old},
			{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: old},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cert := &certificatesv1alpha1.TLSCertificate{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: tt.created}}
			cert.Status.Conditions = tt.conditions
			got, _ := tlsCertificateFailure(cert, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRenewalOverdue(t *testing.T) {
	now := time.Now()
	leaf := func(age, left time.Duration) *x509.Certificate {
		return &x509.Certificate{NotBefore: now.Add(-age), NotAfter: now.Add(left)}
	}
	assert.False(t, renewalOverdue(leaf(10*24*time.Hour, 80*24*time.Hour), now))
	assert.False(t, renewalOverdue(leaf(65*24*time.Hour, 25*24*time.Hour), now), "inside the window the service renews in")
	assert.True(t, renewalOverdue(leaf(70*24*time.Hour, 20*24*time.Hour), now))
	assert.False(t, renewalOverdue(nil, now))
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
	const hostname = "*.shop.example.com"
	testCfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, "test-gw", func(g *gatewayv1.Gateway) {
		g.UID = uuid.NewUUID()
		g.Spec.Listeners = append(g.Spec.Listeners, tlsListener("https-hostname-0", hostname))
	})
	domain := dnsProvenDomain(upstreamNamespace.Name)
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
	fakeDownstreamClient := fake.NewClientBuilder().WithScheme(downstreamScheme).WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).WithStatusSubresource(&gatewayv1.Gateway{}).Build()

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
	assert.Contains(t, listenerCondition(t, &updated, "https-hostname-0", listenerConditionCertificateIssuanceBlocked).Message, "keep trying")

	var downstreamRoutes gatewayv1.HTTPRouteList
	require.NoError(t, fakeDownstreamClient.List(ctx, &downstreamRoutes, client.InNamespace(downstreamGateway.Namespace)))
	assert.NotEmpty(t, downstreamRoutes.Items, "downstream routes are still reconciled")
}

func TestCertificateServiceRenewalBlockedClearsOnRecovery(t *testing.T) {
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "recovery", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	const hostname = "*.shop.example.com"
	const gatewayName = "recovery-gw"
	const listenerName = gatewayv1.SectionName("https-hostname-0")
	const serviceNS = "certificates-system"
	secretName := listenerCertificateSecretName(gatewayName, listenerName)
	certName := tlsCertificateName(gatewayName, listenerName)
	ca := newTestCA(t)

	testCfg := config.NetworkServicesOperator{Gateway: certificateServiceGatewayConfig()}
	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, gatewayName, func(g *gatewayv1.Gateway) {
		g.UID = uuid.NewUUID()
		g.Spec.Listeners = append(g.Spec.Listeners, tlsListener(listenerName, hostname))
	})
	domain := dnsProvenDomain(upstreamNamespace.Name)
	gatewayClass := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: gatewayv1.GatewayClassSpec{ControllerName: "test"}}

	now := time.Now()
	servingCrt, servingKey := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
	issuedCrt, issuedKey := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(90*24*time.Hour))

	cert := &certificatesv1alpha1.TLSCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: certName},
		Spec:       certificatesv1alpha1.TLSCertificateSpec{DNSNames: []certificatesv1alpha1.DNSName{hostname}, Issuance: certificatesv1alpha1.IssuanceModeDNS01, SecretName: secretName},
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
	fakeDownstreamClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(serving).WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).WithStatusSubresource(&gatewayv1.Gateway{}).Build()

	forbidden := true
	serviceClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(issued).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if forbidden {
				return apierrors.NewForbidden(corev1.Resource("secrets"), key.Name, nil)
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}).Build()

	h := &certificateServiceHarness{
		t: t, ctx: ctx, cfg: testCfg, upstream: fakeUpstreamClient, downstream: fakeDownstreamClient, service: serviceClient, roots: ca.pool,
		gateway: client.ObjectKeyFromObject(upstreamGateway), namespace: downstreamNamespaceName,
	}

	before := counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed)
	first, _, _ := h.reconcile()
	assertListenerRenewalBlocked(t, first, "keep trying")
	assert.Equal(t, before+1, counterValue(t, certificateServiceFailuresTotal, upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed))

	forbidden = false
	h.reconciler.certificateServiceFailures.Delete(certificateServiceKey{gateway: upstreamGateway.UID, listener: listenerName})
	second, _, _ := h.reconcile()
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
	h.reconciler.forgetRemovedListeners(removed)
	var m dto.Metric
	require.NoError(t, certificateServiceFailuresTotal.WithLabelValues(upstreamNamespace.Name, gatewayName, string(listenerName), certificateServiceReasonStepFailed).Write(&m))
	assert.Zero(t, m.GetCounter().GetValue(), "a removed listener's series is dropped")
}

func TestCertificateServiceStateCountedPerTransition(t *testing.T) {
	r := &GatewayReconciler{}
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "count", Name: "gw", UID: uuid.NewUUID()}}
	rejected := func() float64 {
		return counterValue(t, certificateServiceFailuresTotal, "count", "gw", "https-0", certificateServiceReasonRejected)
	}
	overdue := func() float64 {
		return counterValue(t, certificateServiceFailuresTotal, "count", "gw", "https-0", certificateServiceReasonRenewalOverdue)
	}
	before, beforeOverdue := rejected(), overdue()
	r.recordCertificateServiceState(gw, "https-0", certificateServiceReasonRejected)
	r.recordCertificateServiceState(gw, "https-0", certificateServiceReasonRejected)
	r.recordCertificateServiceState(gw, "https-0", certificateServiceReasonRejected)
	assert.Equal(t, before+1, rejected())
	r.recordCertificateServiceState(gw, "https-0", "")
	r.recordCertificateServiceState(gw, "https-0", certificateServiceReasonRejected)
	assert.Equal(t, before+2, rejected(), "a new failure after recovery counts again")
	r.recordCertificateServiceState(gw, "https-0", certificateServiceReasonRenewalOverdue)
	assert.Equal(t, beforeOverdue+1, overdue(), "a different failure counts under its own reason")
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
