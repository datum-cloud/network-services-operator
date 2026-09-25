// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// Reader fetches the objects an Application Load Balancer is assembled from.
//
// It is deliberately read-only and deliberately narrow. The identity a read
// runs under is decided by whoever constructs the Reader, never buried in tool
// code: the server builds one per request from the caller's own credentials, so
// a tool call can never see more than the caller could see themselves.
//
// There is no Create, Update, Patch or Delete here, and adding one would be a
// reviewable diff rather than a quiet change inside a handler. Changing a load
// balancer goes through the assistant's own plan and apply path, which holds
// the one confirmation step every service shares.
type Reader interface {
	// ListProxies returns every load balancer in the namespace.
	ListProxies(ctx context.Context, namespace string) ([]networkingv1alpha.HTTPProxy, error)
	// GetProxy returns one load balancer by name.
	GetProxy(ctx context.Context, namespace, name string) (*networkingv1alpha.HTTPProxy, error)
	// ListDomains returns every Domain in the namespace. Domains are matched to
	// a hostname by suffix rather than fetched by name, so the whole list is
	// needed; there are few of them per project.
	ListDomains(ctx context.Context, namespace string) ([]networkingv1alpha.Domain, error)
	// GetNetworkService returns one network service named as an origin.
	GetNetworkService(ctx context.Context, namespace, name string) (*networkingv1alpha.NetworkService, error)
	// ListProtectionPolicies returns every traffic protection policy in the
	// namespace. Which one guards a load balancer is decided by its targetRefs,
	// so answering "is this one protected" means reading them all once.
	ListProtectionPolicies(ctx context.Context, namespace string) ([]networkingv1alpha.TrafficProtectionPolicy, error)
	// GetSecret returns one Secret, used only to read which usernames basic
	// auth accepts. Callers must never return its contents.
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
}

// ClientReader implements Reader against a controller-runtime client.
type ClientReader struct {
	Client client.Client
}

var _ Reader = (*ClientReader)(nil)

// NewClientReader returns a Reader backed by c. Every read is performed with
// whatever credentials c carries.
func NewClientReader(c client.Client) *ClientReader {
	return &ClientReader{Client: c}
}

func (r *ClientReader) ListProxies(ctx context.Context, namespace string) ([]networkingv1alpha.HTTPProxy, error) {
	var list networkingv1alpha.HTTPProxyList
	if err := r.Client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing load balancers in %s: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *ClientReader) GetProxy(ctx context.Context, namespace, name string) (*networkingv1alpha.HTTPProxy, error) {
	var p networkingv1alpha.HTTPProxy
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &p); err != nil {
		return nil, fmt.Errorf("getting load balancer %s: %w", name, err)
	}
	return &p, nil
}

func (r *ClientReader) ListDomains(ctx context.Context, namespace string) ([]networkingv1alpha.Domain, error) {
	var list networkingv1alpha.DomainList
	if err := r.Client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing domains in %s: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *ClientReader) GetNetworkService(ctx context.Context, namespace, name string) (*networkingv1alpha.NetworkService, error) {
	var s networkingv1alpha.NetworkService
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &s); err != nil {
		return nil, fmt.Errorf("getting network service %s: %w", name, err)
	}
	return &s, nil
}

func (r *ClientReader) ListProtectionPolicies(ctx context.Context, namespace string) ([]networkingv1alpha.TrafficProtectionPolicy, error) {
	var list networkingv1alpha.TrafficProtectionPolicyList
	if err := r.Client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing traffic protection policies in %s: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *ClientReader) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	var s corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &s); err != nil {
		return nil, fmt.Errorf("getting secret %s: %w", name, err)
	}
	return &s, nil
}
