package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestProcessDownstreamHTTPRouteRules_BackendRefGroupIsSet(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))

	const (
		upstreamNamespace   = "default"
		downstreamNamespace = "ns-downstream"
		portName            = "http"
	)

	upstreamEndpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: upstreamNamespace, Name: "backend-0-0"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{{
			Name:     ptr.To(portName),
			Protocol: ptr.To(corev1.ProtocolTCP),
			Port:     ptr.To(int32(80)),
		}},
	}

	upstreamRoute := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: upstreamNamespace,
			Name:      "route",
			UID:       "00000000-0000-0000-0000-000000000001",
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						Weight: ptr.To(int32(1)),
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Group: ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
							Kind:  ptr.To(gatewayv1.Kind(KindEndpointSlice)),
							Name:  gatewayv1.ObjectName(upstreamEndpointSlice.Name),
							Port:  ptr.To(gatewayv1.PortNumber(80)),
						},
					},
				}},
			}},
		},
	}

	upstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: upstreamNamespace, Name: "gateway"},
	}
	downstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespace, Name: "gateway"},
	}

	upstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(upstreamEndpointSlice, upstreamGateway).
		Build()
	downstreamClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

	reconciler := &GatewayReconciler{
		mgr:               &fakeMockManager{cl: upstreamClient},
		Config:            config.NetworkServicesOperator{},
		DownstreamCluster: &fakeCluster{cl: downstreamClient},
	}

	rules, _, _, err := reconciler.processDownstreamHTTPRouteRules(
		context.Background(),
		upstreamClient,
		upstreamGateway,
		upstreamRoute,
		downstreamGateway,
		downstreamclient.NewMappedNamespaceResourceStrategy("test", upstreamClient, downstreamClient),
	)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].BackendRefs, 1)

	group := rules[0].BackendRefs[0].Group
	require.NotNil(t, group, "Group must be set, not left to the API server default")
	assert.Equal(t, gatewayv1.Group(""), *group)
	assert.Equal(t, gatewayv1.Kind(KindService), *rules[0].BackendRefs[0].Kind)
}
