// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDeleteRefusesRoutesStillOwnedAfterExternalChanges(t *testing.T) {
	for name, edit := range map[string]func(*api.NetworkLoadBalancerRoute){
		"shared": func(r *api.NetworkLoadBalancerRoute) {
			r.Spec.ParentRefs = append(r.Spec.ParentRefs, api.NetworkLoadBalancerRouteParentRef{Name: "other", Listener: "control"})
		},
		"detached": func(r *api.NetworkLoadBalancerRoute) {
			r.Spec.ParentRefs = []api.NetworkLoadBalancerRouteParentRef{{Name: "other", Listener: "control"}}
		},
		"different manager": func(r *api.NetworkLoadBalancerRoute) {
			r.Labels["app.kubernetes.io/managed-by"] = "another-tool"
		},
	} {
		for _, dryRun := range []bool{false, true} {
			t.Run(name+map[bool]string{true: " dry run", false: " delete"}[dryRun], func(t *testing.T) {
				c := setup(t)
				_, err := execute(createArgs...)
				require.NoError(t, err)
				var r api.NetworkLoadBalancerRoute
				require.NoError(t, c.Get(context.Background(), key(spec.RouteName("media", "control")), &r))
				edit(&r)
				require.NoError(t, c.Update(context.Background(), &r))
				before := r.DeepCopy()
				args := []string{"delete", "media", "--yes", "--no-wait", "-o", "json"}
				if dryRun {
					args = append(args, "--dry-run")
				}
				out, err := execute(args...)
				require.ErrorContains(t, err, "cannot delete NLB")
				require.ErrorContains(t, err, r.Name)
				require.Empty(t, out, "must not promise preservation or deletion success")
				var n api.NetworkLoadBalancer
				require.NoError(t, c.Get(context.Background(), key("media"), &n))
				require.Nil(t, n.DeletionTimestamp)
				require.NoError(t, c.Get(context.Background(), key(r.Name), &r))
				require.Equal(t, before.Spec, r.Spec)
				require.Equal(t, before.OwnerReferences, r.OwnerReferences)
			})
		}
	}
}

func TestDeleteReportsOwnedAndTrulyExternalRoutes(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{true: "dry run", false: "delete"}[dryRun], func(t *testing.T) {
			c := setup(t)
			_, err := execute(createArgs...)
			require.NoError(t, err)
			external := &api.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: spec.Namespace}, Spec: api.NetworkLoadBalancerRouteSpec{ParentRefs: []api.NetworkLoadBalancerRouteParentRef{{Name: "media", Listener: "control"}, {Name: "other", Listener: "control"}}}}
			require.NoError(t, c.Create(context.Background(), external))
			args := []string{"delete", "media", "--yes", "--no-wait", "-o", "json"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			out, err := execute(args...)
			require.NoError(t, err)
			var result struct {
				OwnedRoutes     []string `json:"ownedRoutes"`
				PreservedRoutes []string `json:"preservedRoutes"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			require.Equal(t, []string{spec.RouteName("media", "control")}, result.OwnedRoutes)
			require.Equal(t, []string{"external"}, result.PreservedRoutes)
			var n api.NetworkLoadBalancer
			err = c.Get(context.Background(), key("media"), &n)
			if dryRun {
				require.NoError(t, err)
			} else {
				require.True(t, apierrors.IsNotFound(err))
			}
			var service api.NetworkService
			require.NoError(t, c.Get(context.Background(), key("media"), &service))
			require.NoError(t, c.Get(context.Background(), key("external"), external))
			// The fake client does not run garbage collection. This verifies the
			// deletion request and reporting, not removal of the owned route.
		})
	}
}

type routeChangeDuringDelete struct {
	client.Client
	change func()
	lists  int
}

func (c *routeChangeDuringDelete) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*api.NetworkLoadBalancerRouteList); ok {
		c.lists++
		if c.lists == 2 {
			c.change()
		}
	}
	return c.Client.List(ctx, list, opts...)
}

func TestDeleteRechecksRouteOwnershipBeforeDeleting(t *testing.T) {
	c := setup(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	wrapper := &routeChangeDuringDelete{Client: c, change: func() {
		var r api.NetworkLoadBalancerRoute
		require.NoError(t, c.Get(context.Background(), key(spec.RouteName("media", "control")), &r))
		r.Spec.ParentRefs = append(r.Spec.ParentRefs, api.NetworkLoadBalancerRouteParentRef{Name: "other", Listener: "control"})
		require.NoError(t, c.Update(context.Background(), &r))
	}}
	newClient = func(string) (client.Client, error) { return wrapper, nil }
	out, err := execute("delete", "media", "--yes", "--no-wait", "-o", "json")
	require.ErrorContains(t, err, "cannot delete NLB")
	require.Empty(t, out)
	var n api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
}
