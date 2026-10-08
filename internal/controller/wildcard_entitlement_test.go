// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"

	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

type fakeWildcardEntitlements struct {
	entitled bool
	err      error
	calls    *int
}

func (f fakeWildcardEntitlements) WildcardEntitled(context.Context, string) (bool, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.entitled, f.err
}

func TestAllowanceBucketNameGolden(t *testing.T) {
	assert.Equal(t,
		"bucket-9afa87b0b033f2018cec1536192bb2cf2c5f6238cf8827f6a7406693e4aba975",
		allowanceBucketName(WildcardHostnamesResourceType, "Project", "my-project"),
	)
}

func TestBucketWildcardEntitlements(t *testing.T) {
	quotaScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(quotaScheme))
	require.NoError(t, quotav1alpha1.AddToScheme(quotaScheme))

	bucket := func(project string, available int64) *quotav1alpha1.AllowanceBucket {
		return &quotav1alpha1.AllowanceBucket{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "milo-system",
				Name:      allowanceBucketName(WildcardHostnamesResourceType, "Project", project),
			},
			Status: quotav1alpha1.AllowanceBucketStatus{Limit: 1, Available: available},
		}
	}

	tests := []struct {
		name    string
		objects []client.Object
		getErr  error
		want    bool
		wantErr bool
	}{
		{name: "granted bucket with capacity", objects: []client.Object{bucket("p1", 1)}, want: true},
		{name: "bucket with nothing available", objects: []client.Object{bucket("p1", 0)}, want: false},
		{name: "missing bucket", want: false},
		{name: "another project's bucket", objects: []client.Object{bucket("p2", 1)}, want: false},
		{name: "read error", getErr: apierrors.NewForbidden(quotav1alpha1.GroupVersion.WithResource("allowancebuckets").GroupResource(), "b", nil), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(quotaScheme).WithObjects(tt.objects...)
			if tt.getErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tt.getErr
					},
				})
			}
			got, err := NewWildcardEntitlementChecker(builder.Build()).WildcardEntitled(context.Background(), "p1")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWildcardEntitledMissingCheckerIsDeniedAndErrorsAreSurfaced(t *testing.T) {
	ctx := context.Background()

	got, err := (&GatewayReconciler{}).wildcardEntitled(ctx, "p")
	require.NoError(t, err)
	assert.False(t, got)

	_, err = (&GatewayReconciler{WildcardEntitlements: fakeWildcardEntitlements{entitled: true, err: errors.New("boom")}}).wildcardEntitled(ctx, "p")
	require.Error(t, err, "a read error is not a denial")

	got, err = (&GatewayReconciler{WildcardEntitlements: fakeWildcardEntitlements{entitled: true}}).wildcardEntitled(ctx, "p")
	require.NoError(t, err)
	assert.True(t, got)
}

type entitlementFixture struct {
	h            *certificateServiceHarness
	downstreamNS string
	secretName   string
	certName     string
	listener     gatewayv1.SectionName
	hostname     string
	gateway      *gatewayv1.Gateway
	ca           testCA
}

func newEntitlementFixture(t *testing.T, hostname string, withIssuance bool, checker WildcardEntitlementChecker) *entitlementFixture {
	t.Helper()
	testScheme := newCertificateServiceTestScheme(t)
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "entitle", UID: uuid.NewUUID()}}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)
	const gatewayName = "entitle-gw"
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

	objects := []client.Object{upstreamGateway, upstreamNamespace, domain, gatewayClass}
	var downstreamObjects []client.Object
	var serviceObjects []client.Object
	if withIssuance {
		now := time.Now()
		crt, key := ca.issue(t, hostname, now.Add(-time.Hour), now.Add(60*24*time.Hour))
		cert := &certificatesv1alpha1.TLSCertificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace.Name, Name: certName, UID: uuid.NewUUID()},
			Spec:       certificatesv1alpha1.TLSCertificateSpec{DNSNames: []certificatesv1alpha1.DNSName{certificatesv1alpha1.DNSName(hostname)}, Issuance: certificatesv1alpha1.IssuanceModeDNS01},
			Status: certificatesv1alpha1.TLSCertificateStatus{
				NotBefore:  &metav1.Time{Time: now.Add(-time.Hour)},
				NotAfter:   &metav1.Time{Time: now.Add(60 * 24 * time.Hour)},
				Conditions: []metav1.Condition{{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Issued"}},
			},
		}
		require.NoError(t, controllerutil.SetControllerReference(upstreamGateway, cert, testScheme))
		objects = append(objects, cert)
		downstreamObjects = append(downstreamObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: secretName},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
		})
		serviceObjects = append(serviceObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: serviceNS, Name: certificatesv1alpha1.StoredSecretName(cert.UID)},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
		})
	}
	for _, obj := range append(append([]client.Object{}, objects...), downstreamObjects...) {
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}
		obj.SetCreationTimestamp(metav1.Now())
	}

	upstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objects...).WithStatusSubresource(upstreamGateway, &certificatesv1alpha1.TLSCertificate{}).Build()
	downstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(downstreamObjects...).WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc("default")).WithStatusSubresource(&gatewayv1.Gateway{}).Build()
	service := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(serviceObjects...).Build()

	h := &certificateServiceHarness{
		t: t, ctx: ctx, cfg: testCfg, upstream: upstream, downstream: downstream, service: service, roots: ca.pool,
		gateway: client.ObjectKeyFromObject(upstreamGateway), namespace: downstreamNamespaceName,
	}
	h.reconciler = &GatewayReconciler{
		mgr:                      &fakeMockManager{cl: upstream},
		Config:                   testCfg,
		DownstreamCluster:        &fakeCluster{cl: downstream},
		CertificateServiceReader: service,
		WildcardEntitlements:     checker,
		CertificateServiceRoots:  ca.pool,
	}
	return &entitlementFixture{h: h, downstreamNS: downstreamNamespaceName, secretName: secretName, certName: certName, listener: listenerName, hostname: hostname, gateway: upstreamGateway, ca: ca}
}

func (f *entitlementFixture) tlsCertificateExists() bool {
	err := f.h.upstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.gateway.Namespace, Name: f.certName}, &certificatesv1alpha1.TLSCertificate{})
	return err == nil
}

func (f *entitlementFixture) secretExists() bool {
	err := f.h.downstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.downstreamNS, Name: f.secretName}, &corev1.Secret{})
	return err == nil
}

func (f *entitlementFixture) assertNotEntitled(t *testing.T, gateway *gatewayv1.Gateway) {
	t.Helper()
	blocked := listenerCondition(t, gateway, f.listener, listenerConditionCertificateIssuanceBlocked)
	assert.Equal(t, metav1.ConditionTrue, blocked.Status)
	assert.Equal(t, certificateServiceReasonWildcardNotEntitled, blocked.Reason)
	assert.Contains(t, blocked.Message, "Wildcard hostnames are not enabled for this project")
	assert.Contains(t, blocked.Message, f.hostname)
	for _, ls := range gateway.Status.Listeners {
		if ls.Name == f.listener {
			assert.Nil(t, apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateRenewalBlocked))
		}
	}
}

func TestWildcardHostnameDeniedDoesNotCreateTLSCertificate(t *testing.T) {
	tests := []struct {
		name    string
		checker WildcardEntitlementChecker
	}{
		{name: "no available allowance", checker: fakeWildcardEntitlements{entitled: false}},
		{name: "no checker configured", checker: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newEntitlementFixture(t, "*.shop.example.com", false, tt.checker)

			gateway, _, result := f.h.reconcile()

			assert.False(t, f.tlsCertificateExists(), "no TLSCertificate is requested for a project that is not entitled")
			assert.False(t, f.secretExists())
			f.assertNotEntitled(t, gateway)
			assert.LessOrEqual(t, result.RequeueAfter, wildcardEntitlementRecheck, "a grant is noticed without a spec edit")
			assert.Positive(t, result.RequeueAfter)
		})
	}
}

func TestWildcardHostnameGrantTakesEffectWithoutSpecEdit(t *testing.T) {
	entitled := &mutableEntitlement{}
	f := newEntitlementFixture(t, "*.shop.example.com", false, entitled)

	gateway, _, _ := f.h.reconcile()
	f.assertNotEntitled(t, gateway)
	assert.False(t, f.tlsCertificateExists())

	entitled.value = true
	gateway, _, _ = f.h.reconcile()

	assert.True(t, f.tlsCertificateExists(), "the grant lets the certificate be requested")
	for _, ls := range gateway.Status.Listeners {
		if ls.Name == f.listener {
			if blocked := apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateIssuanceBlocked); blocked != nil {
				assert.NotEqual(t, certificateServiceReasonWildcardNotEntitled, blocked.Reason)
			}
		}
	}
}

type mutableEntitlement struct{ value bool }

func (m *mutableEntitlement) WildcardEntitled(context.Context, string) (bool, error) {
	return m.value, nil
}

func (f *entitlementFixture) assertServing(t *testing.T, gateway, downstreamGateway *gatewayv1.Gateway) {
	t.Helper()
	assert.True(t, f.tlsCertificateExists(), "the TLSCertificate is kept")
	assert.True(t, f.secretExists(), "the mirrored Secret keeps serving")
	assert.Contains(t, listenerNames(downstreamGateway), f.listener, "the listener keeps serving downstream")
	for _, ls := range gateway.Status.Listeners {
		if ls.Name != f.listener {
			continue
		}
		for _, c := range ls.Conditions {
			assert.NotEqual(t, certificateServiceReasonWildcardNotEntitled, c.Reason, "a serving certificate is not reported as not entitled")
		}
		assert.Nil(t, apimeta.FindStatusCondition(ls.Conditions, listenerConditionCertificateIssuanceBlocked))
	}
}

func TestWildcardGrantRemovalKeepsServingCertificate(t *testing.T) {
	entitled := &mutableEntitlement{value: true}
	f := newEntitlementFixture(t, "*.shop.example.com", true, entitled)

	f.h.reconcile()
	require.True(t, f.tlsCertificateExists())

	entitled.value = false
	for range 3 {
		gateway, downstreamGateway, _ := f.h.reconcile()
		f.assertServing(t, gateway, downstreamGateway)
	}
}

func TestWildcardGrantRemovalStillMirrorsRenewedCertificate(t *testing.T) {
	entitled := &mutableEntitlement{value: true}
	f := newEntitlementFixture(t, "*.shop.example.com", true, entitled)
	f.h.reconcile()

	var before corev1.Secret
	require.NoError(t, f.h.downstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.downstreamNS, Name: f.secretName}, &before))

	var cert certificatesv1alpha1.TLSCertificate
	require.NoError(t, f.h.upstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.gateway.Namespace, Name: f.certName}, &cert))
	now := time.Now()
	crt, key := f.ca.issue(t, f.hostname, now.Add(-time.Minute), now.Add(90*24*time.Hour))
	cert.Status.NotAfter = &metav1.Time{Time: now.Add(90 * 24 * time.Hour)}
	require.NoError(t, f.h.upstream.Status().Update(f.h.ctx, &cert))
	require.NoError(t, f.h.service.(client.Client).Update(f.h.ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "certificates-system", Name: certificatesv1alpha1.StoredSecretName(cert.UID)},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
	}))

	entitled.value = false
	f.h.reconcile()

	var after corev1.Secret
	require.NoError(t, f.h.downstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.downstreamNS, Name: f.secretName}, &after))
	assert.Equal(t, crt, after.Data["tls.crt"], "a renewal is still delivered after the grant is removed")
	assert.NotEqual(t, before.Data["tls.crt"], after.Data["tls.crt"])
}

func TestWildcardGrantRemovalNeverConsultsEntitlementForServingCertificate(t *testing.T) {
	calls := 0
	f := newEntitlementFixture(t, "*.shop.example.com", true, fakeWildcardEntitlements{entitled: false, calls: &calls})

	gateway, downstreamGateway, _ := f.h.reconcile()

	f.assertServing(t, gateway, downstreamGateway)
	assert.Zero(t, calls)
}

func TestWildcardGrantRemovalWithoutCertificateStillBlocksThenGrantIssues(t *testing.T) {
	entitled := &mutableEntitlement{}
	f := newEntitlementFixture(t, "*.shop.example.com", false, entitled)

	gateway, _, _ := f.h.reconcile()
	f.assertNotEntitled(t, gateway)

	entitled.value = true
	f.h.reconcile()
	assert.True(t, f.tlsCertificateExists())

	entitled.value = false
	gateway, _, _ = f.h.reconcile()
	assert.True(t, f.tlsCertificateExists(), "once requested the certificate stays")
	for _, ls := range gateway.Status.Listeners {
		for _, c := range ls.Conditions {
			assert.NotEqual(t, certificateServiceReasonWildcardNotEntitled, c.Reason)
		}
	}
}

func TestWildcardGrantRemovalDoesNotKeepCertificateForChangedHostname(t *testing.T) {
	entitled := &mutableEntitlement{value: true}
	f := newEntitlementFixture(t, "*.shop.example.com", true, entitled)
	f.h.reconcile()

	var cert certificatesv1alpha1.TLSCertificate
	require.NoError(t, f.h.upstream.Get(f.h.ctx, client.ObjectKey{Namespace: f.gateway.Namespace, Name: f.certName}, &cert))
	cert.Spec.DNSNames = []certificatesv1alpha1.DNSName{"*.other.example.com"}
	require.NoError(t, f.h.upstream.Update(f.h.ctx, &cert))

	entitled.value = false
	gateway, _, _ := f.h.reconcile()

	f.assertNotEntitled(t, gateway)
}

func TestExactHostnamesNeverConsultEntitlement(t *testing.T) {
	calls := 0
	f := newEntitlementFixture(t, "app.example.com", false, fakeWildcardEntitlements{entitled: false, calls: &calls})

	f.h.reconcile()

	assert.Zero(t, calls)
	assert.False(t, f.tlsCertificateExists(), "exact hostnames stay on cert-manager")
}

func TestWildcardEntitlementReadErrorLeavesServingCertificateAlone(t *testing.T) {
	f := newEntitlementFixture(t, "*.shop.example.com", true, fakeWildcardEntitlements{err: errors.New("milo unavailable")})
	require.True(t, f.tlsCertificateExists())
	require.True(t, f.secretExists())

	gateway, downstreamGateway, result := f.h.reconcile()

	assert.True(t, f.tlsCertificateExists(), "an outage reading the entitlement must not delete the certificate")
	assert.True(t, f.secretExists(), "nor the mirrored Secret that serves it")
	for _, ls := range gateway.Status.Listeners {
		for _, c := range ls.Conditions {
			assert.NotEqual(t, certificateServiceReasonWildcardNotEntitled, c.Reason, "the customer is not told they lost access")
		}
	}
	assert.Contains(t, listenerNames(downstreamGateway), f.listener, "the listener keeps serving downstream")
	assert.Positive(t, result.RequeueAfter, "the check is retried")
}

func TestWildcardEntitlementReadErrorDoesNotIssueNewCertificate(t *testing.T) {
	f := newEntitlementFixture(t, "*.shop.example.com", false, fakeWildcardEntitlements{err: errors.New("milo unavailable")})

	gateway, _, result := f.h.reconcile()

	assert.False(t, f.tlsCertificateExists(), "unknown entitlement does not grant access")
	assert.False(t, f.secretExists())
	for _, ls := range gateway.Status.Listeners {
		for _, c := range ls.Conditions {
			assert.NotEqual(t, certificateServiceReasonWildcardNotEntitled, c.Reason)
		}
	}
	assert.Positive(t, result.RequeueAfter)
}

func listenerNames(g *gatewayv1.Gateway) []gatewayv1.SectionName {
	names := make([]gatewayv1.SectionName, 0, len(g.Spec.Listeners))
	for _, l := range g.Spec.Listeners {
		names = append(names, l.Name)
	}
	return names
}
