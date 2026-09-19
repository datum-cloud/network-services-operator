// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// exposure stands a project control plane and the federation hub on two API
// servers, which is what they are: the control plane holds the proxies and the
// interfaces, and the hub is the only plane a cell can also read.
type exposure struct {
	t   *testing.T
	ctx context.Context

	project client.Client
	hub     client.Client

	projectNamespace string
	hubNamespace     string

	reconciler *EdgeReachabilityReconciler
}

func newExposure(t *testing.T) *exposure {
	t.Helper()

	projectPlane, hubPlane := startPlanes(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{}
	namespace.Name = "proj-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, projectPlane.Create(ctx, namespace))

	hubNamespace := &corev1.Namespace{}
	hubNamespace.Name = "ns-" + string(namespace.UID)
	require.NoError(t, hubPlane.Create(ctx, hubNamespace))

	return &exposure{
		t:                t,
		ctx:              ctx,
		project:          projectPlane,
		hub:              hubPlane,
		projectNamespace: namespace.Name,
		hubNamespace:     hubNamespace.Name,
		reconciler: &EdgeReachabilityReconciler{
			DownstreamCluster: &hubFakeCluster{scheme: hubPlane.Scheme(), client: hubPlane},
		},
	}
}

func (e *exposure) record() {
	e.t.Helper()
	require.NoError(e.t, e.reconciler.record(e.ctx, "cluster-"+testProject, e.project, e.projectNamespace))
}

func (e *exposure) recorded() (*networkingv1alpha.EdgeReachability, bool) {
	e.t.Helper()

	var record networkingv1alpha.EdgeReachability
	err := e.hub.Get(e.ctx, client.ObjectKey{
		Namespace: e.hubNamespace,
		Name:      networkingv1alpha.EdgeReachabilityName,
	}, &record)
	if err != nil {
		return nil, false
	}
	return &record, true
}

func (e *exposure) interfaceHolding(name, address string, labels map[string]string) {
	e.t.Helper()

	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e.projectNamespace,
			Name:      name,
			Labels:    labels,
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "default"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{
				Address: address + "/128",
				Family:  networkingv1alpha.IPv6Protocol,
				Primary: true,
			}},
		},
	}
	require.NoError(e.t, e.project.Create(e.ctx, iface))

	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
	require.NoError(e.t, e.project.Status().Update(e.ctx, iface))
}

func (e *exposure) service(name string, matchLabels map[string]string) {
	e.t.Helper()

	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: e.projectNamespace, Name: name},
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: matchLabels},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
	require.NoError(e.t, e.project.Create(e.ctx, service))
}

func (e *exposure) proxyBackedByWeb() {
	e.t.Helper()

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: e.projectNamespace, Name: "site"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					NetworkService: &networkingv1alpha.NetworkServiceBackendRef{
						Name: "web",
						Port: "http",
					},
				}},
			}},
		},
	}
	require.NoError(e.t, e.project.Create(e.ctx, proxy))
}

// A project with workloads and no proxy is the ordinary case, and it is the one
// that put every tenant's pods on every edge. The record has to say so rather
// than say nothing.
func TestAProjectWithNoProxyRecordsAnEmptyAnswer(t *testing.T) {
	e := newExposure(t)
	e.interfaceHolding("web-0", "fd20:0:2::1:0:0", map[string]string{"compute.datumapis.com/workload-name": "web"})

	e.record()

	record, found := e.recorded()
	require.True(t, found, "an answer of none is still an answer")
	require.Empty(t, record.Spec.Addresses)
}

func TestOnlyTheMembersOfAProxiedServiceAreRecorded(t *testing.T) {
	e := newExposure(t)
	e.interfaceHolding("web-0", "fd20:0:2::1:0:0", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.interfaceHolding("batch-0", "fd20:0:2::2:0:0", map[string]string{"compute.datumapis.com/workload-name": "batch"})
	e.service("web", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.service("batch", map[string]string{"compute.datumapis.com/workload-name": "batch"})
	e.proxyBackedByWeb()

	e.record()

	record, found := e.recorded()
	require.True(t, found)
	require.Equal(t, []string{"fd20:0:2::1:0:0"}, record.Spec.Addresses,
		"a service nothing proxies puts nothing on an edge")
}

// The address is what a cell joins on, so a prefix length carried into the
// record would match nothing and withdraw a pod that is serving.
func TestARecordedAddressCarriesNoPrefixLength(t *testing.T) {
	e := newExposure(t)
	e.interfaceHolding("web-0", "fd20:0:2::1:0:0", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.service("web", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.proxyBackedByWeb()

	e.record()

	record, _ := e.recorded()
	require.Equal(t, []string{"fd20:0:2::1:0:0"}, record.Spec.Addresses)
}

// A selector narrowed, or a workload scaled in, has to take the address back
// out. A record that only ever grew would keep every pod a project had ever run
// reachable from every edge.
func TestARecordFollowsASelectorThatStopsMatching(t *testing.T) {
	e := newExposure(t)
	e.interfaceHolding("web-0", "fd20:0:2::1:0:0", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.service("web", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.proxyBackedByWeb()

	e.record()
	record, _ := e.recorded()
	require.Len(t, record.Spec.Addresses, 1)

	var iface networkingv1alpha.NetworkInterface
	require.NoError(t, e.project.Get(e.ctx, client.ObjectKey{
		Namespace: e.projectNamespace, Name: "web-0",
	}, &iface))
	iface.Labels["compute.datumapis.com/workload-name"] = "retired"
	require.NoError(t, e.project.Update(e.ctx, &iface))

	e.record()

	record, _ = e.recorded()
	require.Empty(t, record.Spec.Addresses)
}

// An interface no workload holds is retired capacity. Nothing answers on its
// addresses, so no edge needs a route to them.
func TestAnUnheldInterfaceIsNotRecorded(t *testing.T) {
	e := newExposure(t)
	e.service("web", map[string]string{"compute.datumapis.com/workload-name": "web"})
	e.proxyBackedByWeb()

	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e.projectNamespace,
			Name:      "web-0",
			Labels:    map[string]string{"compute.datumapis.com/workload-name": "web"},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "default"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{
				Address: "fd20:0:2::1:0:0/128",
				Family:  networkingv1alpha.IPv6Protocol,
				Primary: true,
			}},
		},
	}
	require.NoError(t, e.project.Create(e.ctx, iface))

	e.record()

	record, _ := e.recorded()
	require.Empty(t, record.Spec.Addresses)
}

// A proxy naming a pod's slice directly reaches that pod without a service, and
// dropping it out of the record would black-hole a backend that works today.
func TestAnInstanceBackendIsRecorded(t *testing.T) {
	e := newExposure(t)

	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: e.projectNamespace, Name: "pod-0"},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"fd20:0:2::7:0:0"},
		}},
	}
	require.NoError(t, e.project.Create(e.ctx, slice))

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: e.projectNamespace, Name: "site"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					Instance: &networkingv1alpha.InstanceBackendRef{
						Name: "pod-0",
						Port: 8080,
					},
				}},
			}},
		},
	}
	require.NoError(t, e.project.Create(e.ctx, proxy))

	e.record()

	record, _ := e.recorded()
	require.Equal(t, []string{"fd20:0:2::7:0:0"}, record.Spec.Addresses)
}

// The hub namespace is made by the federation that carries a project's work
// out. A namespace that is not there yet holds nothing to withdraw, and
// inventing one would leave an object nothing collects.
func TestNoRecordIsWrittenWithoutAHubNamespace(t *testing.T) {
	e := newExposure(t)

	orphan := &corev1.Namespace{}
	orphan.Name = "proj-unfederated-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, e.project.Create(e.ctx, orphan))

	require.NoError(t, e.reconciler.record(e.ctx, "cluster-"+testProject, e.project, orphan.Name))

	var record networkingv1alpha.EdgeReachability
	err := e.hub.Get(e.ctx, client.ObjectKey{
		Namespace: "ns-" + string(orphan.UID),
		Name:      networkingv1alpha.EdgeReachabilityName,
	}, &record)
	require.Error(t, err)
}
