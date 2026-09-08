// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func GetHTTPProxy(ctx context.Context, c client.Client, name string) (*networkingv1alpha.HTTPProxy, error) {
	proxy := &networkingv1alpha.HTTPProxy{}
	key := types.NamespacedName{Namespace: ResourceNamespace, Name: name}
	if err := c.Get(ctx, key, proxy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, NewCLIError(ExitNotFound, fmt.Sprintf("application load balancer %q not found", name)).
				WithFix("list load balancers with:\n       datumctl alb list").
				WithCause(err)
		}
		return nil, ClassifyError(fmt.Errorf("getting application load balancer %q: %w", name, err))
	}
	return proxy, nil
}

func DryRunOpts(dryRun bool) []client.CreateOption {
	if !dryRun {
		return nil
	}
	return []client.CreateOption{client.DryRunAll}
}

func DryRunDeleteOpts(dryRun bool) []client.DeleteOption {
	if !dryRun {
		return nil
	}
	return []client.DeleteOption{client.DryRunAll}
}
