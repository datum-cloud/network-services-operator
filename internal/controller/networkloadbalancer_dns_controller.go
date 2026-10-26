// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	nlbDNSFinalizer   = "networking.datumapis.com/nlb-dns-withdrawal"
	nlbDNSRecordTTL   = int64(60)
	nlbDNSResync      = 10 * time.Minute
	nlbDNSRetry       = 30 * time.Second
	nlbDNSEndpointPfx = "nlb-"
)

var dnsEndpointGVK = schema.GroupVersionKind{Group: "externaldns.k8s.io", Version: versionV1Alpha1, Kind: "DNSEndpoint"}

// NetworkLoadBalancerDNSReconciler publishes each load balancer location's
// generated hostname as an external-dns DNSEndpoint on the hub, the same path
// a gateway's own names take.
type NetworkLoadBalancerDNSReconciler struct {
	HubCluster cluster.Cluster
	// PublishOnAllocation publishes an address as soon as it is allocated
	// instead of once it is announced, and leaves dnsPublished unset so that
	// address release never waits on DNS.
	PublishOnAllocation bool
	mgr                 mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancers/finalizers,verbs=update
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;watch;create;update;patch;delete

func (r *NetworkLoadBalancerDNSReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	return r.reconcileDNS(ctx, string(req.ClusterName), cl.GetClient(), req.NamespacedName)
}

func (r *NetworkLoadBalancerDNSReconciler) reconcileDNS(ctx context.Context, project string, projectClient client.Client, key client.ObjectKey) (ctrl.Result, error) {
	var lb networkingv1alpha.NetworkLoadBalancer
	if err := projectClient.Get(ctx, key, &lb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	deleting := !lb.DeletionTimestamp.IsZero()
	if deleting && !controllerutil.ContainsFinalizer(&lb, nlbDNSFinalizer) {
		return ctrl.Result{}, nil
	}

	strategy := downstreamclient.NewMappedNamespaceResourceStrategy(project, projectClient, r.HubCluster.GetClient())
	hubNamespace, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, lb.Namespace)
	if apierrors.IsNotFound(err) {
		if deleting {
			return ctrl.Result{}, r.release(ctx, projectClient, &lb)
		}
		return ctrl.Result{RequeueAfter: nlbDNSRetry}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve hub namespace: %w", err)
	}

	if deleting {
		if err := r.deleteEndpoint(ctx, hubNamespace, &lb); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.reportPublished(ctx, projectClient, &lb, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.release(ctx, projectClient, &lb)
	}

	if controllerutil.AddFinalizer(&lb, nlbDNSFinalizer) {
		if err := projectClient.Update(ctx, &lb); err != nil {
			return ctrl.Result{}, fmt.Errorf("hold load balancer until DNS withdrawal: %w", err)
		}
	}

	records, published := r.desiredRecords(&lb)
	if len(records) == 0 {
		if err := r.deleteEndpoint(ctx, hubNamespace, &lb); err != nil {
			return ctrl.Result{}, err
		}
	} else if err := r.applyEndpoint(ctx, hubNamespace, project, &lb, records); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reportPublished(ctx, projectClient, &lb, published); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: nlbDNSResync}, nil
}

type nlbDNSRecordKey struct {
	hostname   string
	recordType string
}

type nlbAddressKey struct {
	requestName, location string
	family                networkingv1alpha.IPFamily
	value                 string
}

func (r *NetworkLoadBalancerDNSReconciler) eligible(address networkingv1alpha.NetworkLoadBalancerAddressStatus) bool {
	if address.Value == "" {
		return false
	}
	if addressAnnounced(address) {
		return true
	}
	return r.PublishOnAllocation &&
		apimeta.IsStatusConditionTrue(address.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated)
}

func (r *NetworkLoadBalancerDNSReconciler) desiredRecords(lb *networkingv1alpha.NetworkLoadBalancer) (map[nlbDNSRecordKey][]string, map[nlbAddressKey]bool) {
	records := map[nlbDNSRecordKey][]string{}
	published := map[nlbAddressKey]bool{}
	for _, endpoint := range lb.Status.Endpoints {
		if endpoint.CanonicalHostname == "" {
			continue
		}
		for _, address := range endpoint.Addresses {
			if !r.eligible(address) {
				continue
			}
			recordType := "AAAA"
			if address.Family == networkingv1alpha.IPv4Protocol {
				recordType = "A"
			}
			key := nlbDNSRecordKey{hostname: endpoint.CanonicalHostname, recordType: recordType}
			if !slices.Contains(records[key], address.Value) {
				records[key] = append(records[key], address.Value)
			}
			if addressAnnounced(address) {
				published[nlbAddressKey{endpoint.RequestName, endpoint.Location, address.Family, address.Value}] = true
			}
		}
	}
	return records, published
}

func nlbDNSEndpointName(lb *networkingv1alpha.NetworkLoadBalancer) string {
	return nlbDNSEndpointPfx + string(lb.UID)
}

func (r *NetworkLoadBalancerDNSReconciler) applyEndpoint(ctx context.Context, namespace, project string, lb *networkingv1alpha.NetworkLoadBalancer, records map[nlbDNSRecordKey][]string) error {
	keys := make([]nlbDNSRecordKey, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b nlbDNSRecordKey) int {
		if c := strings.Compare(a.hostname, b.hostname); c != 0 {
			return c
		}
		return strings.Compare(a.recordType, b.recordType)
	})
	endpoints := make([]any, 0, len(keys))
	for _, key := range keys {
		targets := slices.Clone(records[key])
		slices.Sort(targets)
		values := make([]any, 0, len(targets))
		for _, target := range targets {
			values = append(values, target)
		}
		endpoints = append(endpoints, map[string]any{
			"dnsName":    key.hostname,
			"recordType": key.recordType,
			"recordTTL":  nlbDNSRecordTTL,
			"targets":    values,
		})
	}

	var endpoint unstructured.Unstructured
	endpoint.SetGroupVersionKind(dnsEndpointGVK)
	endpoint.SetNamespace(namespace)
	endpoint.SetName(nlbDNSEndpointName(lb))
	_, err := controllerutil.CreateOrUpdate(ctx, r.HubCluster.GetClient(), &endpoint, func() error {
		labels := endpoint.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[downstreamclient.UpstreamOwnerClusterNameLabel] = "cluster-" + strings.ReplaceAll(project, "/", "_")
		labels[downstreamclient.UpstreamOwnerNamespaceLabel] = lb.Namespace
		labels[downstreamclient.UpstreamOwnerNameLabel] = lb.Name
		labels[networkLoadBalancerOwnerLabel] = string(lb.UID)
		endpoint.SetLabels(labels)
		return unstructured.SetNestedSlice(endpoint.Object, endpoints, "spec", "endpoints")
	})
	if err != nil {
		return fmt.Errorf("publish load balancer DNS endpoint: %w", err)
	}
	return nil
}

func (r *NetworkLoadBalancerDNSReconciler) deleteEndpoint(ctx context.Context, namespace string, lb *networkingv1alpha.NetworkLoadBalancer) error {
	var endpoint unstructured.Unstructured
	endpoint.SetGroupVersionKind(dnsEndpointGVK)
	endpoint.SetNamespace(namespace)
	endpoint.SetName(nlbDNSEndpointName(lb))
	if err := r.HubCluster.GetClient().Delete(ctx, &endpoint); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("withdraw load balancer DNS endpoint: %w", err)
	}
	return nil
}

func (r *NetworkLoadBalancerDNSReconciler) reportPublished(ctx context.Context, cl client.Client, lb *networkingv1alpha.NetworkLoadBalancer, published map[nlbAddressKey]bool) error {
	before := lb.DeepCopy()
	changed := false
	for i := range lb.Status.Endpoints {
		endpoint := &lb.Status.Endpoints[i]
		for j := range endpoint.Addresses {
			address := &endpoint.Addresses[j]
			want := published[nlbAddressKey{endpoint.RequestName, endpoint.Location, address.Family, address.Value}]
			if address.DNSPublished != want {
				address.DNSPublished = want
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := cl.Status().Patch(ctx, lb, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("report load balancer DNS publication: %w", err)
	}
	return nil
}

func (r *NetworkLoadBalancerDNSReconciler) release(ctx context.Context, cl client.Client, lb *networkingv1alpha.NetworkLoadBalancer) error {
	if !controllerutil.RemoveFinalizer(lb, nlbDNSFinalizer) {
		return nil
	}
	if err := cl.Update(ctx, lb); err != nil {
		return fmt.Errorf("release load balancer after DNS withdrawal: %w", err)
	}
	return nil
}

func (r *NetworkLoadBalancerDNSReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancer{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkloadbalancerdns").
		Complete(r)
}
