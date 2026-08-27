// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// EdgeReachabilityReconciler records, per project namespace, the workload
// addresses that are behind an HTTPProxy, so that what a cell carries to the
// edge is the workloads the edge actually serves rather than every workload the
// project runs.
//
// It runs where both halves of the question are answerable: a project control
// plane holds the proxies, the services, and the interfaces a service selects.
// The answer is written to the federation hub, which is the only plane a cell
// can also read.
//
// The record is written per namespace rather than per service so that a reader
// can tell "the control plane says nothing is published here" from "the control
// plane has not answered". Those are the same absence when the answer is a set
// of objects, and they call for opposite behaviour at the edge.
type EdgeReachabilityReconciler struct {
	// DownstreamCluster is the federation hub the records are written to.
	DownstreamCluster cluster.Cluster

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=edgereachabilities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch

func (r *EdgeReachabilityReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.record(ctx, string(req.ClusterName), cl.GetClient(), req.Namespace)
}

func (r *EdgeReachabilityReconciler) record(
	ctx context.Context,
	clusterName string,
	cl client.Client,
	namespace string,
) error {
	logger := log.FromContext(ctx)

	addresses, err := r.publishedAddresses(ctx, cl, namespace)
	if err != nil {
		return err
	}

	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(clusterName, cl, r.DownstreamCluster.GetClient())

	hubNamespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed resolving the hub namespace for %q: %w", namespace, err)
	}

	record := &networkingv1alpha.EdgeReachability{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hubNamespace,
			Name:      networkingv1alpha.EdgeReachabilityName,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.DownstreamCluster.GetClient(), record, func() error {
		record.Spec.Addresses = addresses
		return nil
	})
	if err != nil {
		// The hub namespace is made by the federation that carries a project's
		// work out. Nothing here should invent one, and a namespace that is not
		// there yet holds nothing to withdraw.
		if apierrors.IsNotFound(err) {
			logger.Info("the hub has no namespace to record edge reachability in yet",
				"namespace", namespace)
			return nil
		}
		return fmt.Errorf("failed recording edge reachability for %q: %w", namespace, err)
	}

	return nil
}

// publishedAddresses is every workload address a proxy in this namespace sends
// traffic to. A proxy naming a service resolves through the same selector the
// service's membership is resolved through, so the answer here and the endpoints
// the proxy is programmed with cannot disagree.
func (r *EdgeReachabilityReconciler) publishedAddresses(
	ctx context.Context,
	cl client.Client,
	namespace string,
) ([]string, error) {
	logger := log.FromContext(ctx)

	var proxies networkingv1alpha.HTTPProxyList
	if err := cl.List(ctx, &proxies, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed listing http proxies in %q: %w", namespace, err)
	}

	seen := map[string]struct{}{}
	var addresses []string
	add := func(address string) {
		if address == "" {
			return
		}
		if _, ok := seen[address]; ok {
			return
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}

	services := map[string]struct{}{}
	instanceBackends := map[string]struct{}{}

	for i := range proxies.Items {
		proxy := &proxies.Items[i]
		if !proxy.DeletionTimestamp.IsZero() {
			continue
		}
		for _, rule := range proxy.Spec.Rules {
			for _, backend := range rule.Backends {
				if backend.NetworkService != nil {
					services[backend.NetworkService.Name] = struct{}{}
				}
				if backend.Instance != nil {
					instanceBackends[backend.Instance.Name] = struct{}{}
				}
			}
		}
	}

	for name := range services {
		var service networkingv1alpha.NetworkService
		key := client.ObjectKey{Namespace: namespace, Name: name}
		if err := cl.Get(ctx, key, &service); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed getting network service %q: %w", name, err)
		}

		selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
		if err != nil {
			logger.Info("not recording the members of a network service whose selector cannot be evaluated",
				"namespace", namespace, jsonKeyName, name)
			continue
		}

		members, err := matchingInterfaces(ctx, cl, namespace, selector)
		if err != nil {
			return nil, fmt.Errorf("failed resolving the members of network service %q: %w", name, err)
		}

		for i := range members {
			for _, address := range interfaceBackhaulAddresses(&members[i]) {
				add(address)
			}
		}
	}

	for name := range instanceBackends {
		var slice discoveryv1.EndpointSlice
		key := client.ObjectKey{Namespace: namespace, Name: name}
		if err := cl.Get(ctx, key, &slice); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed getting instance backend endpointslice %q: %w", name, err)
		}
		for _, endpoint := range slice.Endpoints {
			for _, address := range endpoint.Addresses {
				add(address)
			}
		}
	}

	slices.Sort(addresses)
	return addresses, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EdgeReachabilityReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.DownstreamCluster == nil {
		return errors.New("a downstream cluster is required")
	}

	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.HTTPProxy{}).
		Watches(&networkingv1alpha.NetworkService{}, enqueueEdgeReachabilityForNamespace()).
		Watches(&networkingv1alpha.NetworkInterface{}, enqueueEdgeReachabilityForNamespace()).
		Named("edgereachability").
		Complete(r)
}

// enqueueEdgeReachabilityForNamespace enqueues the one record a namespace
// holds. Reconcile reads the namespace off the request and recomputes the whole
// answer, so the name carried here only has to be stable.
func enqueueEdgeReachabilityForNamespace() func(
	clusterName multicluster.ClusterName,
	cl cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return func(clusterName multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
			if obj.GetNamespace() == "" {
				return nil
			}
			return []mcreconcile.Request{{
				ClusterName: clusterName,
				Request: ctrl.Request{NamespacedName: client.ObjectKey{
					Namespace: obj.GetNamespace(),
					Name:      networkingv1alpha.EdgeReachabilityName,
				}},
			}}
		})
	}
}
