// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Model write responses with nonzero, incrementing generations and subsequent
// controller status. This exercises command snapshot plumbing; it is not an
// API-server or forwarding integration test.
type readinessClient struct {
	*testClient
	superseded string
}

func readyConditions(g int64, kinds ...string) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, metav1.Condition{Type: kind, Status: metav1.ConditionTrue, ObservedGeneration: g})
	}
	return out
}

func (c *readinessClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	obj.SetGeneration(1)
	if err := c.testClient.Create(ctx, obj, opts...); err != nil {
		return err
	}
	return c.serve(ctx, obj)
}

func (c *readinessClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	if err := c.Client.Patch(ctx, obj, p, opts...); err != nil {
		return err
	}
	obj.SetGeneration(obj.GetGeneration() + 1)
	return c.serve(ctx, obj)
}

func (c *readinessClient) serve(ctx context.Context, obj client.Object) error {
	switch o := obj.(type) {
	case *api.NetworkLoadBalancer:
		o.Status.AddressRequests = nil
		o.Status.Endpoints = nil
		for _, request := range o.Spec.Exposure.Addresses {
			o.Status.AddressRequests = append(o.Status.AddressRequests, api.NetworkLoadBalancerAddressRequestStatus{Name: request.Name, ResolvedLocations: []string{"dfw"}})
			ep := api.NetworkLoadBalancerEndpointStatus{RequestName: request.Name, Location: "dfw", CanonicalHostname: "dfw.example.com"}
			for _, family := range request.Families {
				ep.Addresses = append(ep.Addresses, api.NetworkLoadBalancerAddressStatus{Family: family, Value: "2001:db8::1", Announced: true, DNSPublished: true, WithdrawalPending: true, Conditions: readyConditions(o.Generation, "Announced")})
			}
			for _, listener := range o.Spec.Listeners {
				ep.Listeners = append(ep.Listeners, api.NetworkLoadBalancerListenerStatus{Name: listener.Name, Conditions: readyConditions(o.Generation, "Programmed", "Announced")})
			}
			o.Status.Endpoints = append(o.Status.Endpoints, ep)
		}
	case *api.NetworkLoadBalancerRoute:
		o.Status.Parents = nil
		for _, p := range o.Spec.ParentRefs {
			o.Status.Parents = append(o.Status.Parents, api.NetworkLoadBalancerRouteParentStatus{Name: p.Name, Listener: p.Listener, Effective: o.Name != c.superseded, Conditions: readyConditions(o.Generation, "Accepted", "ResolvedRefs", "Programmed")})
		}
	}
	return c.Update(ctx, obj)
}

func setupReadiness(t *testing.T) *readinessClient {
	t.Helper()
	c := &readinessClient{testClient: setup(t)}
	newClient = func(string) (client.Client, error) { return c, nil }
	return c
}

func TestWaitCommandsUseSavedRouteIdentityAndGeneration(t *testing.T) {
	c := setupReadiness(t)
	args := append(slicesCopy(createArgs[:len(createArgs)-1]), "--timeout", "100ms")
	_, err := execute(args...)
	require.NoError(t, err)
	// A retry must use the stored route identity, including a nonzero generation.
	_, err = execute(args...)
	require.NoError(t, err)
	add := []string{"listener", "add", "media", "media", "--protocol", "udp", "--port-range", "10000-10127", "--network-service", "media", "--backend-port", "media", "--health-protocol", "tcp", "--health-port", "health", "--timeout", "100ms"}
	_, err = execute(add...)
	require.NoError(t, err)
	_, err = execute(add...)
	require.NoError(t, err)
	_, err = execute("listener", "update", "media", "media", "--port-range", "10000-10063", "--timeout", "100ms")
	require.NoError(t, err)
	var routes api.NetworkLoadBalancerRouteList
	require.NoError(t, c.List(context.Background(), &routes))
	for _, route := range routes.Items {
		require.NotEmpty(t, route.UID)
		require.Positive(t, route.Generation)
		_, err = execute("route", "update", route.Name, "--health-protocol", "tcp", "--health-port", "health", "--timeout", "100ms")
		require.NoError(t, err)
	}
}

func TestRouteUpdateDoesNotUseOtherEffectiveRoute(t *testing.T) {
	c := setupReadiness(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	var routes api.NetworkLoadBalancerRouteList
	require.NoError(t, c.List(context.Background(), &routes))
	require.Len(t, routes.Items, 1)
	target := routes.Items[0]
	c.superseded = target.Name
	other := target.DeepCopy()
	other.Name, other.UID, other.ResourceVersion = "older-external-route", "", ""
	other.OwnerReferences = nil
	require.NoError(t, c.Create(context.Background(), other))
	out, err := execute("route", "update", target.Name, "--health-protocol", "tcp", "--timeout", "20ms")
	require.ErrorContains(t, err, "not effective")
	require.NotContains(t, out, "Saved")
}

func TestEndpointsOnlyShowActualRemoval(t *testing.T) {
	c := setupReadiness(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	var n api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	for _, mode := range []string{"serving", "retiring", "deleting"} {
		t.Run(mode, func(t *testing.T) {
			copy := n.DeepCopy()
			if mode == "retiring" {
				copy.Status.Endpoints[0].Addresses[0].Retiring = true
			}
			if mode == "deleting" {
				now := metav1.Now()
				copy.DeletionTimestamp = &now
			}
			var out bytes.Buffer
			cmd := Command()
			cmd.SetOut(&out)
			require.NoError(t, printEndpoints(cmd, copy, copy.Status.Endpoints))
			row := strings.Fields(strings.Split(out.String(), "\n")[1])
			require.GreaterOrEqual(t, len(row), 8)
			want := "true"
			if mode == "serving" {
				want = "false"
			}
			require.Equal(t, want, row[7], out.String())
		})
	}
}

func TestWaitRejectsChangedOrReplacedLoadBalancer(t *testing.T) {
	c := setupReadiness(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	var n api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	var routes api.NetworkLoadBalancerRouteList
	require.NoError(t, c.List(context.Background(), &routes))
	require.Len(t, routes.Items, 1)
	for _, change := range []string{"identity", "generation"} {
		t.Run(change, func(t *testing.T) {
			expected := n.DeepCopy()
			if change == "identity" {
				expected.UID = "original-instance"
			} else {
				expected.Generation--
			}
			cmd := Command()
			cmd.SetContext(context.Background())
			waitFlags(cmd)
			err := waitReady(cmd, c, expected, "control", &routes.Items[0])
			require.ErrorContains(t, err, "changed while waiting")
		})
	}
}
