// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

// protectionFor reports whether traffic through this load balancer is being
// inspected.
//
// Nothing on the load balancer says so, which is why this is a list-and-filter
// rather than a field read: a protection policy names what it guards, not the
// other way round. Creating a load balancer in the portal attaches one on a
// best-effort basis, so "no policy at all" is a real and reasonably common
// state that no condition anywhere reports.
func protectionFor(
	ctx context.Context,
	r Reader,
	namespace string,
	proxy *networkingv1alpha.HTTPProxy,
	d *Diagnosis,
) ProtectionView {
	policies, err := r.ListProtectionPolicies(ctx, namespace)
	if err != nil {
		d.Unread = append(d.Unread,
			"traffic protection policies could not be read, so whether traffic through this load balancer is inspected is unknown")
		return ProtectionView{}
	}

	for i := range policies {
		policy := &policies[i]
		if !spec.TPPTargetsProxy(policy, proxy.Name) {
			continue
		}
		return ProtectionView{
			Attached: true,
			Policy:   policy.Name,
			Mode:     spec.TPPMode(policy),
			Paranoia: spec.TPPParanoia(policy),
		}
	}
	return ProtectionView{}
}
