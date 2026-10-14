// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

type Reader interface {
	ListNetworks(ctx context.Context, namespace string) ([]networkingv1alpha.Network, error)
	GetNetwork(ctx context.Context, namespace, name string) (*networkingv1alpha.Network, error)
	ListNetworkContexts(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkContext, error)
	ListNetworkBindings(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkBinding, error)
	ListSubnets(ctx context.Context, namespace string) ([]networkingv1alpha.Subnet, error)
	ListNetworkInterfaces(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkInterface, error)
	ListNetworkInterfaceClaims(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkInterfaceClaim, error)
	ListNetworkServices(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkService, error)
	GetNetworkService(ctx context.Context, namespace, name string) (*networkingv1alpha.NetworkService, error)
}

type ClientReader struct {
	Client client.Client
}

var _ Reader = (*ClientReader)(nil)

func NewClientReader(c client.Client) *ClientReader {
	return &ClientReader{Client: c}
}

func list[L client.ObjectList](ctx context.Context, c client.Client, l L, namespace, what string) error {
	if err := c.List(ctx, l, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing %s in %s: %w", what, namespace, err)
	}
	return nil
}

func (r *ClientReader) ListNetworks(ctx context.Context, namespace string) ([]networkingv1alpha.Network, error) {
	var l networkingv1alpha.NetworkList
	err := list(ctx, r.Client, &l, namespace, "networks")
	return l.Items, err
}

func (r *ClientReader) GetNetwork(ctx context.Context, namespace, name string) (*networkingv1alpha.Network, error) {
	var n networkingv1alpha.Network
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &n); err != nil {
		return nil, fmt.Errorf("getting network %s: %w", name, err)
	}
	return &n, nil
}

func (r *ClientReader) ListNetworkContexts(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkContext, error) {
	var l networkingv1alpha.NetworkContextList
	err := list(ctx, r.Client, &l, namespace, "network locations")
	return l.Items, err
}

func (r *ClientReader) ListNetworkBindings(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkBinding, error) {
	var l networkingv1alpha.NetworkBindingList
	err := list(ctx, r.Client, &l, namespace, "network attachments")
	return l.Items, err
}

func (r *ClientReader) ListSubnets(ctx context.Context, namespace string) ([]networkingv1alpha.Subnet, error) {
	var l networkingv1alpha.SubnetList
	err := list(ctx, r.Client, &l, namespace, "subnets")
	return l.Items, err
}

func (r *ClientReader) ListNetworkInterfaces(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkInterface, error) {
	var l networkingv1alpha.NetworkInterfaceList
	err := list(ctx, r.Client, &l, namespace, "network interfaces")
	return l.Items, err
}

func (r *ClientReader) ListNetworkInterfaceClaims(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkInterfaceClaim, error) {
	var l networkingv1alpha.NetworkInterfaceClaimList
	err := list(ctx, r.Client, &l, namespace, "network interface requests")
	return l.Items, err
}

func (r *ClientReader) ListNetworkServices(ctx context.Context, namespace string) ([]networkingv1alpha.NetworkService, error) {
	var l networkingv1alpha.NetworkServiceList
	err := list(ctx, r.Client, &l, namespace, "network services")
	return l.Items, err
}

func (r *ClientReader) GetNetworkService(ctx context.Context, namespace, name string) (*networkingv1alpha.NetworkService, error) {
	var s networkingv1alpha.NetworkService
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &s); err != nil {
		return nil, fmt.Errorf("getting network service %s: %w", name, err)
	}
	return &s, nil
}
