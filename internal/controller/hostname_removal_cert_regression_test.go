package controller

import (
	"context"
	"testing"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// TestGatewayPrunesCertificateForRemovedHostname verifies that a per-hostname
// Certificate is created while its listener is present and pruned once the
// hostname (and its listener) is removed from the gateway.
func TestGatewayPrunesCertificateForRemovedHostname(t *testing.T) {
	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))
	ctx := log.IntoContext(context.Background(), logger)

	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	assert.NoError(t, cmv1.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()},
	}
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)

	testCfg := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:   "test-suite",
			TargetDomain:                 "test-suite.com",
			DefaultListenerTLSSecretName: "shared-tls",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
			},
		},
	}

	const hostname = "custom.example.com"
	certName := listenerCertificateName("test-gw", "https-hostname-0")

	customListener := gatewayv1.Listener{
		Name:     "https-hostname-0",
		Protocol: gatewayv1.HTTPSProtocolType,
		Port:     DefaultHTTPSPort,
		Hostname: ptr.To(gatewayv1.Hostname(hostname)),
		TLS: &gatewayv1.ListenerTLSConfig{
			Mode: ptr.To(gatewayv1.TLSModeTerminate),
			Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey(certificateIssuerTLSOption): "test-issuer",
			},
		},
	}

	upstreamGateway := newGateway(testCfg, upstreamNamespace.Name, "test-gw", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = append(g.Spec.Listeners, customListener)
	})
	upstreamGateway.SetUID(uuid.NewUUID())

	downstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw", Namespace: downstreamNamespaceName, UID: uuid.NewUUID()},
	}

	fakeUpstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(upstreamGateway, upstreamNamespace).
		Build()

	fakeDownstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamGateway).
		Build()

	reconciler := &GatewayReconciler{
		mgr:               &fakeMockManager{cl: fakeUpstreamClient},
		Config:            testCfg,
		DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, fakeDownstreamClient)

	result := reconciler.ensureListenerCertificates(ctx, upstreamGateway, downstreamGateway, fakeDownstreamClient, downstreamStrategy, []string{hostname})
	assert.NoError(t, result.Err)

	assert.NoError(t,
		fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: certName}, &cmv1.Certificate{}),
		"per-hostname Certificate should exist while the hostname is present",
	)

	upstreamGateway.Spec.Listeners = upstreamGateway.Spec.Listeners[:len(upstreamGateway.Spec.Listeners)-1]

	result = reconciler.ensureListenerCertificates(ctx, upstreamGateway, downstreamGateway, fakeDownstreamClient, downstreamStrategy, nil)
	assert.NoError(t, result.Err)

	err := fakeDownstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: certName}, &cmv1.Certificate{})
	assert.True(t, apierrors.IsNotFound(err), "per-hostname Certificate should be pruned after the hostname is removed")
}

// TestTrafficProtectionPolicyAcceptedAfterHostnameRemoval verifies that a policy
// stuck waiting on a per-hostname certificate becomes Accepted once the hostname
// (and its listener) is removed from the gateway.
func TestTrafficProtectionPolicyAcceptedAfterHostnameRemoval(t *testing.T) {
	eppOn := true
	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			ControllerName:               gatewayv1.GatewayController("test-controller"),
			TargetDomain:                 "example.com",
			DefaultListenerTLSSecretName: "shared-tls",
			DownstreamGatewayNamespace:   "envoy-gateway-system",
			DownstreamGatewayClassName:   "test-suite",
			EPPEmissionEnabled:           &eppOn,
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				gatewayv1.AnnotationKey("gateway.networking.datumapis.com/certificate-issuer"): gatewayv1.AnnotationValue("test"),
			},
		},
	}

	upstreamScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(upstreamScheme))
	assert.NoError(t, gatewayv1.Install(upstreamScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(upstreamScheme))

	downstreamScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(downstreamScheme))
	assert.NoError(t, envoygatewayv1alpha1.AddToScheme(downstreamScheme))
	assert.NoError(t, cmv1.AddToScheme(downstreamScheme))

	const (
		upstreamNS   = "default"
		nsUID        = "test-ns-uid"
		downstreamNS = "ns-" + nsUID
	)

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: upstreamNS, UID: nsUID},
	}

	gw := newGateway(operatorConfig, upstreamNS, "test-gw", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = append(g.Spec.Listeners, gatewayv1.Listener{
			Name:     "https-hostname-0",
			Protocol: gatewayv1.HTTPSProtocolType,
			Port:     443,
			Hostname: ptr.To(gatewayv1.Hostname("custom.example.com")),
			TLS:      &gatewayv1.ListenerTLSConfig{Mode: ptr.To(gatewayv1.TLSModeTerminate)},
		})
	})
	for i := range gw.Spec.Listeners {
		gw.Status.Listeners = append(gw.Status.Listeners, gatewayv1.ListenerStatus{
			Name: gw.Spec.Listeners[i].Name,
			Conditions: []metav1.Condition{{
				Type:               string(gatewayv1.ListenerConditionProgrammed),
				Status:             metav1.ConditionTrue,
				Reason:             "Programmed",
				LastTransitionTime: metav1.Now(),
			}},
		})
	}

	tpp := newTrafficProtectionPolicy(upstreamNS, "tpp-1", func(p *networkingv1alpha.TrafficProtectionPolicy) {
		p.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
			{LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{Kind: "Gateway", Name: "test-gw"}},
		}
	})

	pendingCert := newCertificateUnstructured(downstreamNS, "test-gw-https-hostname-0", false)

	fakeUpstreamClient := fake.NewClientBuilder().
		WithScheme(upstreamScheme).
		WithObjects(upstreamNamespace, gw, &tpp).
		WithStatusSubresource(gw, &tpp).
		Build()

	fakeDownstreamClient := fake.NewClientBuilder().
		WithScheme(downstreamScheme).
		WithObjects(pendingCert).
		Build()

	reconciler := &TrafficProtectionPolicyReconciler{
		mgr:               &fakeMockManager{cl: fakeUpstreamClient},
		DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
		Config:            operatorConfig,
	}

	ctx := context.Background()
	req := NamespaceReconcileRequest{Namespace: upstreamNS, ClusterName: "test-cluster"}

	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)

	stuck := &networkingv1alpha.TrafficProtectionPolicy{}
	assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(&tpp), stuck))
	if assert.Len(t, stuck.Status.Ancestors, 1) {
		cond := apimeta.FindStatusCondition(stuck.Status.Ancestors[0].Conditions, string(gatewayv1.PolicyConditionAccepted))
		if assert.NotNil(t, cond) {
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, string(PolicyReasonWaitingForCertificates), cond.Reason)
		}
	}

	assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(gw), gw))
	kept := gw.Spec.Listeners[:0]
	for _, l := range gw.Spec.Listeners {
		if l.Name != "https-hostname-0" {
			kept = append(kept, l)
		}
	}
	gw.Spec.Listeners = kept
	assert.NoError(t, fakeUpstreamClient.Update(ctx, gw))
	assert.NoError(t, fakeDownstreamClient.Delete(ctx, pendingCert))

	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)

	accepted := &networkingv1alpha.TrafficProtectionPolicy{}
	assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(&tpp), accepted))
	if assert.Len(t, accepted.Status.Ancestors, 1) {
		cond := apimeta.FindStatusCondition(accepted.Status.Ancestors[0].Conditions, string(gatewayv1.PolicyConditionAccepted))
		if assert.NotNil(t, cond) {
			assert.Equal(t, metav1.ConditionTrue, cond.Status, "policy should be Accepted after hostname removal")
		}
	}
}
