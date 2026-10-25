// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const (
	internalDNSFinalizer = "networking.datumapis.com/internal-dns-access"

	InternalDNSContextNameAnnotation        = "networking.datumapis.com/internal-dns-context-name"
	InternalDNSAccessNameAnnotation         = "networking.datumapis.com/internal-dns-access-name"
	InternalDNSServiceDestinationAnnotation = "networking.datumapis.com/internal-dns-service-destination"
	InternalDNSSuffixAnnotation             = "networking.datumapis.com/internal-dns-suffix"
	InternalDNSRegionAnnotation             = "networking.datumapis.com/internal-dns-region"

	internalDNSOwnerNetworkUIDAnnotation = "networking.datumapis.com/internal-dns-network-uid"
	internalDNSOwnerProjectUIDAnnotation = "networking.datumapis.com/internal-dns-project-uid"

	internalDNSRegionTopologyKey = "topology.datum.net/region"
	internalDNSRetryInterval     = 10 * time.Second
	// internalDNSResyncInterval backs up the resolver context and access
	// binding watches. A status change on either reaches the network contexts
	// it serves within the watch latency, so this only catches a missed event.
	internalDNSResyncInterval = time.Minute
	internalDNSDefaultDomain  = "datum.internal"
)

var (
	internalDNSContextGVK = schema.GroupVersionKind{
		Group: "dns.networking.miloapis.com", Version: "v1alpha1", Kind: "DNSResolverContext",
	}
	internalDNSAccessGVK = schema.GroupVersionKind{
		Group: "dns.networking.miloapis.com", Version: "v1alpha1", Kind: "DNSResolverAccessBinding",
	}
)

type ProjectIdentityResolver interface {
	ProjectUID(context.Context, string) (types.UID, error)
}

type APIProjectIdentityResolver struct {
	Reader client.Reader
}

func (r *APIProjectIdentityResolver) ProjectUID(ctx context.Context, name string) (types.UID, error) {
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Kind: "Project",
	})
	if err := r.Reader.Get(ctx, client.ObjectKey{Name: name}, project); err != nil {
		return "", err
	}
	if project.GetUID() == "" {
		return "", fmt.Errorf("project %q has no UID", name)
	}
	return project.GetUID(), nil
}

type InternalDNSProjectReconciler struct {
	Config    config.InternalDNSConfig
	Projects  ProjectIdentityResolver
	Locations client.Reader
	Now       func() time.Time

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=locations.miloapis.com,resources=locations,verbs=get;list;watch
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsresolvercontexts;dnsresolveraccessbindings,verbs=get;list;watch;create;update;patch;delete

func (r *InternalDNSProjectReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	ctx = mccontext.WithCluster(ctx, req.ClusterName)
	return r.reconcile(ctx, cl.GetClient(), string(req.ClusterName), req.NamespacedName)
}

func (r *InternalDNSProjectReconciler) reconcile(
	ctx context.Context,
	cl client.Client,
	project string,
	key client.ObjectKey,
) (ctrl.Result, error) {
	networkContext := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, key, networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !networkContext.DeletionTimestamp.IsZero() {
		if err := r.reportTerminating(ctx, cl, networkContext); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(networkContext, internalDNSFinalizer) {
			if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(networkContext, internalDNSFinalizer)
			if err := cl.Update(ctx, networkContext); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	region, err := r.region(ctx, cl, networkContext)
	if err != nil {
		return ctrl.Result{}, err
	}
	previousRegion := networkContext.Annotations[InternalDNSRegionAnnotation]
	if previousRegion != "" && previousRegion != region {
		if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
			return ctrl.Result{}, err
		}
	}
	if previousRegion != region {
		patch := client.MergeFrom(networkContext.DeepCopy())
		if networkContext.Annotations == nil {
			networkContext.Annotations = map[string]string{}
		}
		if region == "" {
			delete(networkContext.Annotations, InternalDNSRegionAnnotation)
		} else {
			networkContext.Annotations[InternalDNSRegionAnnotation] = region
		}
		if err := cl.Patch(ctx, networkContext, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	network := &networkingv1alpha.Network{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: networkContext.Spec.Network.Name}, network); err != nil {
		return ctrl.Result{}, err
	}
	if network.Spec.DNS.Mode == networkingv1alpha.NetworkDNSModeDisabled {
		return ctrl.Result{}, r.disable(ctx, cl, networkContext, network)
	}
	if region == "" {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
	}

	if controllerutil.AddFinalizer(networkContext, internalDNSFinalizer) {
		if err := cl.Update(ctx, networkContext); err != nil {
			return ctrl.Result{}, err
		}
	}

	projectUID, err := r.Projects.ProjectUID(ctx, project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve project identity: %w", err)
	}
	consumerID := string(projectUID) + "/" + string(network.UID)
	dnsContext, err := r.ensureContext(ctx, cl, network, projectUID, consumerID)
	if err != nil {
		return ctrl.Result{}, err
	}
	suffix, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "suffix")
	zoneName, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "dnsZoneRef", "name")
	if dnsContext.GetUID() == "" || suffix != internalDNSDefaultDomain || zoneName == "" || !resolverContextReady(dnsContext) {
		return ctrl.Result{RequeueAfter: internalDNSResyncInterval}, nil
	}

	destination, err := internalDNSDestination(r.Config.ServicePrefix, dnsContext.GetUID())
	if err != nil {
		return ctrl.Result{}, err
	}
	binding, ready, err := r.ensureAccess(ctx, cl, network, projectUID, dnsContext, region, destination)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{RequeueAfter: internalDNSResyncInterval}, nil
	}

	if err := r.publishIntent(ctx, cl, networkContext, map[string]string{
		InternalDNSContextNameAnnotation:        dnsContext.GetName(),
		InternalDNSAccessNameAnnotation:         binding.GetName(),
		InternalDNSServiceDestinationAnnotation: destination,
		InternalDNSSuffixAnnotation:             suffix,
		InternalDNSRegionAnnotation:             region,
	}); err != nil {
		return ctrl.Result{}, err
	}

	// Network DNS mode and Location topology aren't watched, so keep checking
	// them at the short interval once access is in place.
	return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
}

func (r *InternalDNSProjectReconciler) reportTerminating(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	message := "NetworkContext is being deleted"
	waitingOn := slices.DeleteFunc(slices.Clone(networkContext.Finalizers), func(f string) bool {
		return f == internalDNSFinalizer
	})
	if len(waitingOn) > 0 {
		message += "; waiting on finalizers " + strings.Join(waitingOn, ", ")
	}
	if !apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkContextDNSReady,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.NetworkContextDNSReadyReasonTerminating,
		Message:            message,
		ObservedGeneration: networkContext.Generation,
	}) {
		return nil
	}
	return client.IgnoreNotFound(cl.Status().Update(ctx, networkContext))
}

func (r *InternalDNSProjectReconciler) region(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (string, error) {
	location := &locationsv1alpha1.Location{}
	reader := r.Locations
	if reader == nil {
		reader = cl
	}
	if err := reader.Get(ctx, client.ObjectKey{Name: networkContext.Spec.Location.Name}, location); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return location.Spec.Topology[internalDNSRegionTopologyKey], nil
}

func (r *InternalDNSProjectReconciler) ensureContext(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	projectUID types.UID,
	consumerID string,
) (*unstructured.Unstructured, error) {
	name := internalDNSResourceName(network.Name, network.UID)
	current := newInternalDNSObject(internalDNSContextGVK, network.Namespace, name)
	err := cl.Get(ctx, client.ObjectKeyFromObject(current), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		ownedNetwork := current.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation]
		existingConsumer, _, _ := unstructured.NestedString(current.Object, "spec", "consumerID")
		if ownedNetwork != string(network.UID) || existingConsumer != consumerID {
			return nil, fmt.Errorf("DNSResolverContext %s/%s belongs to another network lifetime", network.Namespace, name)
		}
		domain, _, _ := unstructured.NestedString(current.Object, "spec", "managedNamespace", "domainSuffix")
		enabled, _, _ := unstructured.NestedBool(current.Object, "spec", "managedNamespace", "enabled")
		if domain != internalDNSDefaultDomain || !enabled {
			return nil, fmt.Errorf("DNSResolverContext %s/%s does not request the network default domain", network.Namespace, name)
		}
		return current, nil
	}

	desired := newInternalDNSObject(internalDNSContextGVK, network.Namespace, name)
	desired.SetAnnotations(map[string]string{
		internalDNSOwnerNetworkUIDAnnotation: string(network.UID),
		internalDNSOwnerProjectUIDAnnotation: string(projectUID),
	})
	desired.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: networkingv1alpha.GroupVersion.String(), Kind: "Network", Name: network.Name,
		UID: network.UID, Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}})
	desired.Object["spec"] = map[string]any{
		"consumerID":       consumerID,
		"managedNamespace": map[string]any{"enabled": true, "domainSuffix": internalDNSDefaultDomain},
	}
	if err := cl.Create(ctx, desired); err != nil {
		return nil, err
	}
	return desired, nil
}

func resolverContextReady(obj *unstructured.Unstructured) bool {
	if !obj.GetDeletionTimestamp().IsZero() || obj.GetGeneration() < 1 {
		return false
	}
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		if condition["type"] == "Ready" && condition["status"] == "True" && condition["observedGeneration"] == obj.GetGeneration() {
			return true
		}
	}
	return false
}

func (r *InternalDNSProjectReconciler) ensureAccess(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	projectUID types.UID,
	dnsContext *unstructured.Unstructured,
	region string,
	destination string,
) (*unstructured.Unstructured, bool, error) {
	name := internalDNSRegionalName(dnsContext.GetName(), region)
	current := newInternalDNSObject(internalDNSAccessGVK, network.Namespace, name)
	err := cl.Get(ctx, client.ObjectKeyFromObject(current), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	if err == nil {
		if current.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) {
			return nil, false, fmt.Errorf("DNSResolverAccessBinding %s/%s belongs to another network lifetime", network.Namespace, name)
		}
		if !current.GetDeletionTimestamp().IsZero() {
			return current, false, nil
		}
		if accessBindingMatches(current, dnsContext, region, destination) {
			return current, bindingReady(current), nil
		}
		uid, resourceVersion := current.GetUID(), current.GetResourceVersion()
		return current, false, client.IgnoreNotFound(cl.Delete(ctx, current, client.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}))
	}
	current.Object["spec"] = map[string]any{
		"contextRef":         map[string]any{"name": dnsContext.GetName(), "uid": string(dnsContext.GetUID())},
		"region":             region,
		"destinationAddress": destination,
	}
	current.SetAnnotations(map[string]string{
		internalDNSOwnerNetworkUIDAnnotation: string(network.UID),
		internalDNSOwnerProjectUIDAnnotation: string(projectUID),
		InternalDNSRegionAnnotation:          region,
	})
	current.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: internalDNSContextGVK.GroupVersion().String(), Kind: internalDNSContextGVK.Kind,
		Name: dnsContext.GetName(), UID: dnsContext.GetUID(), Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}})
	if err := cl.Create(ctx, current); err != nil {
		return nil, false, err
	}
	return current, false, nil
}

func accessBindingMatches(binding *unstructured.Unstructured, dnsContext *unstructured.Unstructured, region, destination string) bool {
	contextName, _, _ := unstructured.NestedString(binding.Object, "spec", "contextRef", "name")
	contextUID, _, _ := unstructured.NestedString(binding.Object, "spec", "contextRef", "uid")
	boundRegion, _, _ := unstructured.NestedString(binding.Object, "spec", "region")
	boundDestination, _, _ := unstructured.NestedString(binding.Object, "spec", "destinationAddress")
	return contextName == dnsContext.GetName() && contextUID == string(dnsContext.GetUID()) && boundRegion == region && boundDestination == destination
}

func (r *InternalDNSProjectReconciler) disable(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	network *networkingv1alpha.Network,
) error {
	var errs []error
	if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
		errs = append(errs, err)
	}
	contextObj := newInternalDNSObject(internalDNSContextGVK, network.Namespace, internalDNSResourceName(network.Name, network.UID))
	if err := deleteOwnedDNSObject(ctx, cl, contextObj, string(network.UID)); err != nil {
		errs = append(errs, err)
	}
	if err := r.clearIntent(ctx, cl, networkContext); err != nil {
		errs = append(errs, err)
	}
	if controllerutil.RemoveFinalizer(networkContext, internalDNSFinalizer) {
		if err := cl.Update(ctx, networkContext); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func deleteOwnedDNSObject(ctx context.Context, cl client.Client, obj *unstructured.Unstructured, networkUID string) error {
	if err := cl.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if networkUID == "" || obj.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != networkUID {
		return fmt.Errorf("%s %s/%s belongs to another network lifetime", obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}
	uid, version := obj.GetUID(), obj.GetResourceVersion()
	return client.IgnoreNotFound(cl.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &version}))
}

func (r *InternalDNSProjectReconciler) cleanupRegion(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	region := networkContext.Annotations[InternalDNSRegionAnnotation]
	contextName := networkContext.Annotations[InternalDNSContextNameAnnotation]
	if region == "" || contextName == "" {
		return nil
	}
	var contexts networkingv1alpha.NetworkContextList
	if err := cl.List(ctx, &contexts, client.InNamespace(networkContext.Namespace), client.MatchingLabels{
		networkingv1alpha.NetworkUIDLabel: networkContext.Labels[networkingv1alpha.NetworkUIDLabel],
	}); err != nil {
		return err
	}
	for i := range contexts.Items {
		other := &contexts.Items[i]
		if other.UID == networkContext.UID || !other.DeletionTimestamp.IsZero() {
			continue
		}
		otherRegion := other.Annotations[InternalDNSRegionAnnotation]
		if otherRegion == "" {
			var err error
			otherRegion, err = r.region(ctx, cl, other)
			if err != nil {
				return err
			}
		}
		if otherRegion == region {
			return nil
		}
	}
	binding := newInternalDNSObject(internalDNSAccessGVK, networkContext.Namespace, internalDNSRegionalName(contextName, region))
	if err := deleteOwnedDNSObject(ctx, cl, binding, networkContext.Labels[networkingv1alpha.NetworkUIDLabel]); err != nil {
		return err
	}
	return nil
}

func (r *InternalDNSProjectReconciler) publishIntent(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	values map[string]string,
) error {
	patch := client.MergeFrom(networkContext.DeepCopy())
	if networkContext.Annotations == nil {
		networkContext.Annotations = map[string]string{}
	}
	changed := false
	for key, value := range values {
		if networkContext.Annotations[key] != value {
			networkContext.Annotations[key] = value
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return cl.Patch(ctx, networkContext, patch)
}

func (r *InternalDNSProjectReconciler) clearIntent(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	patch := client.MergeFrom(networkContext.DeepCopy())
	changed := false
	for _, key := range internalDNSIntentAnnotations() {
		if _, ok := networkContext.Annotations[key]; ok {
			delete(networkContext.Annotations, key)
			changed = true
		}
	}
	if networkContext.Spec.DNS != nil {
		networkContext.Spec.DNS = nil
		changed = true
	}
	if !changed {
		return nil
	}
	return cl.Patch(ctx, networkContext, patch)
}

func (r *InternalDNSProjectReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project identity resolver is required")
	}
	r.mgr = mgr
	watchOpts := []mcbuilder.WatchesOption{
		mcbuilder.WithEngageWithLocalCluster(false),
		mcbuilder.WithPredicates(internalDNSObjectChangedPredicate()),
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Watches(newInternalDNSObject(internalDNSContextGVK, "", ""), enqueueInternalDNSNetworkContexts, watchOpts...).
		Watches(newInternalDNSObject(internalDNSAccessGVK, "", ""), enqueueInternalDNSNetworkContexts, watchOpts...).
		Named("internaldns-project").
		Complete(r)
}

// enqueueInternalDNSNetworkContexts maps a resolver context or access binding
// to the network contexts of the network that owns it, so each setup step
// runs as soon as the one before it reports ready.
func enqueueInternalDNSNetworkContexts(
	clusterName multicluster.ClusterName,
	cl cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		return internalDNSNetworkContextRequests(ctx, cl.GetClient(), clusterName, obj)
	})
}

func internalDNSNetworkContextRequests(
	ctx context.Context,
	cl client.Client,
	clusterName multicluster.ClusterName,
	obj client.Object,
) []mcreconcile.Request {
	networkUID := obj.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation]
	if networkUID == "" {
		return nil
	}
	var contexts networkingv1alpha.NetworkContextList
	if err := cl.List(ctx, &contexts, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{
		networkingv1alpha.NetworkUIDLabel: networkUID,
	}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list network contexts for internal DNS object",
			"kind", obj.GetObjectKind().GroupVersionKind().Kind, "object", client.ObjectKeyFromObject(obj))
		return nil
	}
	requests := make([]mcreconcile.Request, 0, len(contexts.Items))
	for i := range contexts.Items {
		requests = append(requests, mcreconcile.Request{
			ClusterName: clusterName,
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&contexts.Items[i])},
		})
	}
	return requests
}

// internalDNSObjectChangedPredicate passes creation, deletion, and an update
// that changes what the project reconciler reads from a resolver context or
// access binding. Other status writes don't re-reconcile every network
// context on the network.
func internalDNSObjectChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj, okOld := e.ObjectOld.(*unstructured.Unstructured)
			newObj, okNew := e.ObjectNew.(*unstructured.Unstructured)
			if !okOld || !okNew {
				return true
			}
			return internalDNSObjectInputs(oldObj) != internalDNSObjectInputs(newObj)
		},
	}
}

type internalDNSObjectState struct {
	uid                types.UID
	generation         int64
	deleting           bool
	ownerNetworkUID    string
	ready              bool
	observedGeneration int64
	suffix             string
	zoneName           string
}

func internalDNSObjectInputs(obj *unstructured.Unstructured) internalDNSObjectState {
	observed, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
	suffix, _, _ := unstructured.NestedString(obj.Object, "status", "managedNamespace", "suffix")
	zoneName, _, _ := unstructured.NestedString(obj.Object, "status", "managedNamespace", "dnsZoneRef", "name")
	return internalDNSObjectState{
		uid:                obj.GetUID(),
		generation:         obj.GetGeneration(),
		deleting:           !obj.GetDeletionTimestamp().IsZero(),
		ownerNetworkUID:    obj.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation],
		ready:              resolverContextReady(obj),
		observedGeneration: observed,
		suffix:             suffix,
		zoneName:           zoneName,
	}
}

type InternalDNSReadinessReconciler struct {
	Config   config.InternalDNSConfig
	Projects ProjectClusterResolver
	Now      func() time.Time

	hub client.Client
}

func (r *InternalDNSReadinessReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hubContext := &networkingv1alpha.NetworkContext{}
	if err := r.hub.Get(ctx, req.NamespacedName, hubContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	routing, err := resolveProjectRouting(ctx, r.hub, req.Namespace)
	if err != nil {
		var unresolved *projectUnresolvable
		if errors.As(err, &unresolved) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	projectCluster, err := r.Projects.ClusterForProject(ctx, routing.project)
	if err != nil {
		return ctrl.Result{}, err
	}
	projectClient := projectCluster.GetClient()
	projectContext := &networkingv1alpha.NetworkContext{}
	key := client.ObjectKey{Namespace: routing.projectNamespace, Name: hubContext.Name}
	if err := projectClient.Get(ctx, key, projectContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.publish(ctx, projectClient, projectContext, hubContext)
}

func (r *InternalDNSReadinessReconciler) publish(
	ctx context.Context,
	cl client.Client,
	projectContext *networkingv1alpha.NetworkContext,
	hubContext *networkingv1alpha.NetworkContext,
) (ctrl.Result, error) {
	if !projectContext.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	intent, err := internalDNSSettingsFromAnnotations(projectContext.Annotations, r.Config.Nameservers())
	if err != nil {
		condition := metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionFalse,
			Reason: networkingv1alpha.NetworkContextDNSReadyReasonPending, Message: err.Error(),
		}
		disabled, lookupErr := networkDNSDisabled(ctx, cl, projectContext)
		if lookupErr != nil {
			return ctrl.Result{}, lookupErr
		}
		if !disabled {
			return ctrl.Result{}, r.updateProject(ctx, cl, projectContext, nil, condition)
		}
		// Re-enabling DNS doesn't touch the context until access is ready,
		// so check again rather than wait for a hub event.
		condition.Reason = networkingv1alpha.NetworkContextDNSReadyReasonDisabled
		condition.Message = "DNS is turned off for this network"
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.updateProject(ctx, cl, projectContext, nil, condition)
	}

	access := hubContext.Status.DNSAccess
	hubCondition := apimeta.FindStatusCondition(hubContext.Status.Conditions, networkingv1alpha.NetworkContextDNSReady)
	ready := hubCondition != nil && hubCondition.Status == metav1.ConditionTrue
	if !ready || !dnsAccessMatches(intent, access) || access == nil || access.ServiceDestination != projectContext.Annotations[InternalDNSServiceDestinationAnnotation] {
		condition := metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionUnknown,
			Reason:  networkingv1alpha.NetworkContextDNSReadyReasonNetworkPathNotReady,
			Message: "Current network programming health is unavailable",
		}
		// Pass on the cell's reason when nothing is attached, so an idle network
		// doesn't look like a programming failure. Other cell reasons describe
		// cell internals or a lagging copy, so they stay generic here.
		if hubCondition != nil && hubCondition.Reason == networkingv1alpha.NetworkContextDNSReadyReasonNoAttachments {
			condition.Status = hubCondition.Status
			condition.Reason = hubCondition.Reason
			condition.Message = hubCondition.Message
		}
		err := r.updateProject(ctx, cl, projectContext, retainedDNSSettings(projectContext.Spec.DNS, intent), condition)
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, err
	}
	err = r.updateProject(ctx, cl, projectContext, intent, metav1.Condition{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkContextDNSReadyReasonReady,
		Message: "DNS configuration and private network programming are current",
	})
	return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, err
}

func (r *InternalDNSReadinessReconciler) updateProject(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	settings *networkingv1alpha.NetworkDNSSettings,
	condition metav1.Condition,
) error {
	var errs []error
	if !equalDNSSettings(networkContext.Spec.DNS, settings) {
		patch := client.MergeFrom(networkContext.DeepCopy())
		networkContext.Spec.DNS = settings
		if err := cl.Patch(ctx, networkContext, patch); err != nil {
			return err
		}
	}
	condition.ObservedGeneration = networkContext.Generation
	if apimeta.SetStatusCondition(&networkContext.Status.Conditions, condition) {
		if err := cl.Status().Update(ctx, networkContext); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *InternalDNSReadinessReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project cluster resolver is required")
	}
	r.hub = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}).
		Named("internaldns-readiness").
		Complete(r)
}

func networkDNSDisabled(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (bool, error) {
	network := &networkingv1alpha.Network{}
	key := client.ObjectKey{Namespace: networkContext.Namespace, Name: networkContext.Spec.Network.Name}
	if err := cl.Get(ctx, key, network); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return network.Spec.DNS.Mode == networkingv1alpha.NetworkDNSModeDisabled, nil
}

func internalDNSSettingsFromAnnotations(
	annotations map[string]string,
	nameservers []string,
) (*networkingv1alpha.NetworkDNSSettings, error) {
	contextName := annotations[InternalDNSContextNameAnnotation]
	accessName := annotations[InternalDNSAccessNameAnnotation]
	if contextName == "" || accessName == "" {
		return nil, errors.New("DNS access intent is incomplete")
	}
	settings := &networkingv1alpha.NetworkDNSSettings{
		ContextRef:       networkingv1alpha.NetworkDNSObjectReference{Name: contextName},
		AccessBindingRef: networkingv1alpha.NetworkDNSObjectReference{Name: accessName},
		Nameservers:      slices.Clone(nameservers),
	}
	if suffix := annotations[InternalDNSSuffixAnnotation]; suffix != "" {
		settings.Searches = []string{suffix}
	}
	return settings, nil
}

func dnsAccessMatches(settings *networkingv1alpha.NetworkDNSSettings, access *networkingv1alpha.NetworkDNSAccessStatus) bool {
	return settings != nil && access != nil &&
		settings.ContextRef == access.ContextRef &&
		settings.AccessBindingRef == access.AccessBindingRef &&
		sameNameserverSet(settings.Nameservers, access.Nameservers)
}

func sameNameserverSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, nameserver := range a {
		if !slices.Contains(b, nameserver) {
			return false
		}
	}
	return true
}

func equalDNSSettings(a, b *networkingv1alpha.NetworkDNSSettings) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ContextRef == b.ContextRef && a.AccessBindingRef == b.AccessBindingRef &&
		strings.Join(a.Nameservers, "\x00") == strings.Join(b.Nameservers, "\x00") &&
		strings.Join(a.Searches, "\x00") == strings.Join(b.Searches, "\x00")
}

func retainedDNSSettings(applied, intent *networkingv1alpha.NetworkDNSSettings) *networkingv1alpha.NetworkDNSSettings {
	if applied == nil || intent == nil ||
		applied.ContextRef != intent.ContextRef || applied.AccessBindingRef != intent.AccessBindingRef ||
		!slices.Equal(applied.Searches, intent.Searches) {
		return nil
	}
	var nameservers []string
	for _, nameserver := range applied.Nameservers {
		if slices.Contains(intent.Nameservers, nameserver) {
			nameservers = append(nameservers, nameserver)
		}
	}
	if len(nameservers) == 0 {
		return nil
	}
	retained := applied.DeepCopy()
	retained.Nameservers = nameservers
	return retained
}

func internalDNSDestination(prefixText string, uid types.UID) (string, error) {
	prefix, err := netip.ParsePrefix(prefixText)
	if err != nil {
		return "", err
	}
	bytes := prefix.Masked().Addr().As16()
	digest := sha256.Sum256([]byte(uid))
	copy(bytes[8:], digest[:8])
	address := netip.AddrFrom16(bytes)
	if !prefix.Contains(address) {
		return "", fmt.Errorf("allocated DNS destination %s falls outside %s", address, prefix)
	}
	return address.String(), nil
}

func internalDNSResourceName(network string, uid types.UID) string {
	return boundedDNSName("dns-"+network, string(uid))
}

func internalDNSRegionalName(contextName, region string) string {
	return boundedDNSName(contextName+"-"+region, contextName+"/"+region)
}

func boundedDNSName(prefix, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:6])
	maxPrefix := 253 - len(suffix) - 1
	prefix = strings.Trim(prefix, "-")
	if len(prefix) > maxPrefix {
		prefix = strings.TrimRight(prefix[:maxPrefix], "-")
	}
	return prefix + "-" + suffix
}

func bindingReady(binding *unstructured.Unstructured) bool {
	observed, found, _ := unstructured.NestedInt64(binding.Object, "status", "observedGeneration")
	return found && observed == binding.GetGeneration() && resolverContextReady(binding)
}

func newInternalDNSObject(gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func internalDNSIntentAnnotations() []string {
	return []string{
		InternalDNSRegionAnnotation,
		InternalDNSContextNameAnnotation,
		InternalDNSAccessNameAnnotation,
		InternalDNSServiceDestinationAnnotation,
		InternalDNSSuffixAnnotation,
	}
}

func boolPtr(value bool) *bool { return &value }
