// SPDX-License-Identifier: AGPL-3.0-only
package spec

import (
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasOwnerReferenceUsesResourceIdentity(t *testing.T) {
	n := &api.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "media", Namespace: Namespace, UID: "current-uid"}}
	original := NewRoute(n, api.NetworkLoadBalancerListener{Name: "control"}, api.NetworkLoadBalancerRouteBackend{})
	for name, tc := range map[string]struct {
		edit func(*api.NetworkLoadBalancerRoute)
		want bool
	}{
		"current owner": {func(*api.NetworkLoadBalancerRoute) {}, true},
		"another API version": {func(r *api.NetworkLoadBalancerRoute) {
			r.OwnerReferences[0].APIVersion = api.GroupVersion.Group + "/v1beta1"
		}, true},
		"old UID with same name": {func(r *api.NetworkLoadBalancerRoute) { r.OwnerReferences[0].UID = "old-uid" }, false},
		"missing UID":            {func(r *api.NetworkLoadBalancerRoute) { r.OwnerReferences[0].UID = "" }, false},
		"different name":         {func(r *api.NetworkLoadBalancerRoute) { r.OwnerReferences[0].Name = "other" }, false},
		"different namespace":    {func(r *api.NetworkLoadBalancerRoute) { r.Namespace = "other" }, false},
		"different kind":         {func(r *api.NetworkLoadBalancerRoute) { r.OwnerReferences[0].Kind = "NetworkService" }, false},
		"different group":        {func(r *api.NetworkLoadBalancerRoute) { r.OwnerReferences[0].APIVersion = "other.example/v1alpha" }, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := original.DeepCopy()
			tc.edit(r)
			require.Equal(t, tc.want, HasOwnerReference(r, n))
		})
	}
	r := original.DeepCopy()
	r.Spec.ParentRefs = append(r.Spec.ParentRefs, api.NetworkLoadBalancerRouteParentRef{Name: "other", Listener: "control"})
	require.True(t, HasOwnerReference(r, n))
	require.False(t, Owned(r, n), "shared route is not safe for convenience edits even though it still has a GC dependency")
}
