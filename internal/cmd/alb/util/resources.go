// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

func GetNetworkService(ctx context.Context, c client.Client, name string) (*networkingv1alpha.NetworkService, error) {
	service := &networkingv1alpha.NetworkService{}
	key := types.NamespacedName{Namespace: ResourceNamespace, Name: name}
	if err := c.Get(ctx, key, service); err != nil {
		switch {
		case apimeta.IsNoMatchError(err):
			return nil, NewCLIError(ExitUnavailable, "network services are not available in this project yet").
				WithFix("use --endpoint URL backends until the NetworkService API is enabled").
				WithCause(err)
		case apierrors.IsNotFound(err):
			return nil, NewCLIError(ExitNotFound, fmt.Sprintf("network service %q not found", name)).
				WithFix("create the network service first, then point the load balancer at it. This plugin never creates one.").
				WithCause(err)
		}
		return nil, ClassifyError(fmt.Errorf("getting network service %q: %w", name, err))
	}
	return service, nil
}

func EnsureNetworkServicePort(service *networkingv1alpha.NetworkService, port string) error {
	names := make([]string, 0, len(service.Spec.Ports))
	for _, p := range service.Spec.Ports {
		if p.Name == port {
			return nil
		}
		names = append(names, p.Name)
	}
	fix := "the service declares no named ports"
	if len(names) > 0 {
		fix = "declared ports: " + strings.Join(names, ", ")
	}
	return NewCLIError(ExitNotFound, fmt.Sprintf("network service %q has no port named %q", service.Name, port)).
		WithFix(fix)
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
