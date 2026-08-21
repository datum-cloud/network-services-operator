// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const (
	locationPublisherFieldManager = "network-services-operator-location-publisher"

	// LocationPublisherManagedByLabel marks every hub object this publisher
	// owns. The publisher only ever deletes objects carrying it.
	LocationPublisherManagedByLabel = "networking.datumapis.com/published-by"

	// LocationPublisherManagedByValue is the value carried by every published
	// object.
	LocationPublisherManagedByValue = "location-publisher"

	// LocationRemovalOverrideAnnotation, set to "true" on a retained hub copy,
	// lets the publisher delete that copy without checking the removal guard.
	LocationRemovalOverrideAnnotation = "networking.datumapis.com/removal-override"

	// LocationRemovalBlockedAnnotation records why a copy is being retained.
	LocationRemovalBlockedAnnotation = "networking.datumapis.com/removal-blocked"

	removalBlockedRequeue = 5 * time.Minute
	cacheSyncWaitTimeout  = 30 * time.Second
)

var (
	karmadaClusterGVK = schema.GroupVersionKind{
		Group:   "cluster.karmada.io",
		Version: "v1alpha1",
		Kind:    "Cluster",
	}
	karmadaClusterListGVK = schema.GroupVersionKind{
		Group:   "cluster.karmada.io",
		Version: "v1alpha1",
		Kind:    "ClusterList",
	}
	clusterPropagationPolicyGVK = schema.GroupVersionKind{
		Group:   "policy.karmada.io",
		Version: "v1alpha1",
		Kind:    "ClusterPropagationPolicy",
	}
	clusterPropagationPolicyListGVK = schema.GroupVersionKind{
		Group:   "policy.karmada.io",
		Version: "v1alpha1",
		Kind:    "ClusterPropagationPolicyList",
	}
)

type removalDecision struct {
	allowed bool
	reason  string
	message string
}

// LocationPublisherReconciler publishes one ServingLocation and one
// ClusterPropagationPolicy per source Location onto the federation hub.
type LocationPublisherReconciler struct {
	Config config.NetworkServicesOperator

	// SourceCluster holds the Location records.
	SourceCluster cluster.Cluster

	// HubCluster holds the published copies.
	HubCluster cluster.Cluster

	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=locations,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=servinglocations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.karmada.io,resources=clusterpropagationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.karmada.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *LocationPublisherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("location", req.Name))

	var location networkingv1alpha.Location
	err := r.SourceCluster.GetClient().Get(ctx, client.ObjectKey{Name: req.Name}, &location)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed reading location %q: %w", req.Name, err)
	}

	var (
		result    ctrl.Result
		actionErr error
	)
	if err == nil {
		result, actionErr = r.publish(ctx, &location)
	} else {
		result, actionErr = r.remove(ctx, req.Name)
	}
	r.refreshFleetMetrics(ctx)
	return result, actionErr
}

func (r *LocationPublisherReconciler) publish(
	ctx context.Context,
	location *networkingv1alpha.Location,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cityCode := strings.TrimSpace(location.Spec.Topology[networkingv1alpha.TopologyCityCodeKey])
	if cityCode == "" {
		locationPublishMismatch.WithLabelValues(location.Name, "MissingCityCode").Set(1)
		r.event(location, "Warning", "MissingCityCode",
			fmt.Sprintf("Location %q carries no %s, so it cannot be published",
				location.Name, networkingv1alpha.TopologyCityCodeKey))
		logger.Info("refusing to publish a location with no city code")
		return ctrl.Result{}, nil
	}
	locationPublishMismatch.DeleteLabelValues(location.Name, "MissingCityCode")

	published, err := r.readPublished(ctx, location.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if published != nil && !r.ownsObject(published) {
		locationPublisherConflictsTotal.WithLabelValues(location.Name, "UnlabelledHubObject").Inc()
		r.event(location, "Warning", "UnlabelledHubObject",
			fmt.Sprintf("ServingLocation %q on the hub carries no %s label and is not owned by this publisher",
				location.Name, LocationPublisherManagedByLabel))
		return ctrl.Result{}, nil
	}
	r.reportForeignManagers(location, published)

	publishedAt := metav1.NewTime(time.Now().UTC())
	if published != nil && publishedContentEqual(published, location, cityCode) {
		publishedAt = published.Spec.Source.PublishedAt
	}

	desired := &networkingv1alpha.ServingLocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   location.Name,
			Labels: map[string]string{LocationPublisherManagedByLabel: LocationPublisherManagedByValue},
		},
		Spec: networkingv1alpha.ServingLocationSpec{
			Topology: location.Spec.Topology,
			Source: networkingv1alpha.ServingLocationSource{
				Generation:  location.Generation,
				PublishedAt: publishedAt,
			},
		},
	}
	desired.SetGroupVersionKind(networkingv1alpha.GroupVersion.WithKind("ServingLocation"))

	if err := r.HubCluster.GetClient().Patch(ctx, desired, client.Apply, //nolint:staticcheck // SA1019: the typed Apply API needs a generated ApplyConfiguration this type has none of
		client.FieldOwner(locationPublisherFieldManager), client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed publishing serving location %q: %w", location.Name, err)
	}

	if err := r.applyPropagationPolicy(ctx, location.Name); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.clearBlocked(ctx, published); err != nil {
		return ctrl.Result{}, err
	}

	locationPublishTimestamp.WithLabelValues(location.Name).Set(float64(publishedAt.Unix()))
	locationRemovalBlocked.DeletePartialMatch(map[string]string{metricLabelLocation: location.Name})
	r.refreshMatchedClusters(ctx, location.Name)

	return ctrl.Result{RequeueAfter: r.Config.LocationPublisher.SafetyResyncPeriod.Duration}, nil
}

func (r *LocationPublisherReconciler) remove(ctx context.Context, name string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	published, err := r.readPublished(ctx, name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if published == nil {
		locationPublishTimestamp.DeleteLabelValues(name)
		locationRemovalBlocked.DeletePartialMatch(map[string]string{metricLabelLocation: name})
		locationMatchedClusters.DeleteLabelValues(name)
		locationPublishMismatch.DeletePartialMatch(map[string]string{metricLabelLocation: name})
		return ctrl.Result{}, nil
	}
	if !r.ownsObject(published) {
		locationPublisherConflictsTotal.WithLabelValues(name, "UnlabelledHubObject").Inc()
		logger.Info("refusing to remove an unlabelled hub object")
		return ctrl.Result{}, nil
	}

	if err := r.pruneAllowed(ctx); err != nil {
		logger.Info("prune is disabled", "reason", err.Error())
		return ctrl.Result{RequeueAfter: r.blockedRequeue()}, nil
	}

	if published.Annotations[LocationRemovalOverrideAnnotation] != "true" {
		decision, err := r.evaluateRemovalGuard(ctx, name)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !decision.allowed {
			locationRemovalBlocked.WithLabelValues(name, decision.reason).Set(1)
			if err := r.recordBlocked(ctx, published, decision); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("removal blocked, retaining the published copy",
				"reason", decision.reason, "message", decision.message)
			return ctrl.Result{RequeueAfter: r.blockedRequeue()}, nil
		}
	}

	if err := r.HubCluster.GetClient().Delete(ctx, published); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed removing serving location %q: %w", name, err)
	}
	if err := r.deletePropagationPolicy(ctx, name); err != nil {
		return ctrl.Result{}, err
	}

	locationPublishTimestamp.DeleteLabelValues(name)
	locationRemovalBlocked.DeletePartialMatch(map[string]string{metricLabelLocation: name})
	locationMatchedClusters.DeleteLabelValues(name)
	locationPublishMismatch.DeletePartialMatch(map[string]string{metricLabelLocation: name})

	return ctrl.Result{}, nil
}

func (r *LocationPublisherReconciler) blockedRequeue() time.Duration {
	resync := r.Config.LocationPublisher.SafetyResyncPeriod.Duration
	if resync > 0 && resync < removalBlockedRequeue {
		return resync
	}
	return removalBlockedRequeue
}

func (r *LocationPublisherReconciler) pruneAllowed(ctx context.Context) error {
	syncCtx, cancel := context.WithTimeout(ctx, cacheSyncWaitTimeout)
	defer cancel()

	if !r.SourceCluster.GetCache().WaitForCacheSync(syncCtx) {
		return errors.New("the source cache is not synced")
	}
	if !r.HubCluster.GetCache().WaitForCacheSync(syncCtx) {
		return errors.New("the hub cache is not synced")
	}

	var locations networkingv1alpha.LocationList
	if err := r.SourceCluster.GetClient().List(ctx, &locations); err != nil {
		return fmt.Errorf("failed listing source locations: %w", err)
	}
	if len(locations.Items) > 0 {
		return nil
	}

	held, err := r.listPublished(ctx)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		locationPublisherConflictsTotal.WithLabelValues("", "EmptySourceList").Inc()
		return fmt.Errorf("the source list returned zero locations while %d published copies are held", len(held))
	}
	return nil
}

func (r *LocationPublisherReconciler) evaluateRemovalGuard(
	ctx context.Context,
	name string,
) (removalDecision, error) {
	clusters := &unstructured.UnstructuredList{}
	clusters.SetGroupVersionKind(karmadaClusterListGVK)
	if err := r.HubCluster.GetAPIReader().List(ctx, clusters); err != nil {
		return removalDecision{}, fmt.Errorf("failed listing hub clusters: %w", err)
	}

	var labelled, unlabelled []string
	for i := range clusters.Items {
		item := &clusters.Items[i]
		if !karmadaClusterReady(item) {
			continue
		}
		value, ok := item.GetLabels()[networkingv1alpha.ServingLocationTopologyLabel]
		switch {
		case value == name && ok:
			labelled = append(labelled, item.GetName())
		case !ok || value == "":
			unlabelled = append(unlabelled, item.GetName())
		}
	}
	sort.Strings(labelled)
	sort.Strings(unlabelled)

	if len(labelled) > 0 {
		return removalDecision{
			reason: "ClustersStillServing",
			message: fmt.Sprintf("clusters still labelled for this location: %s",
				strings.Join(labelled, ", ")),
		}, nil
	}
	if len(unlabelled) > 0 {
		return removalDecision{
			reason: "FleetNotFullyLabelled",
			message: fmt.Sprintf("no cluster claims this location, but %d Ready cluster(s) carry no location label at all: %s",
				len(unlabelled), strings.Join(unlabelled, ", ")),
		}, nil
	}
	return removalDecision{allowed: true}, nil
}

func karmadaClusterReady(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if condition["type"] == "Ready" && condition["status"] == string(metav1.ConditionTrue) {
			return true
		}
	}
	return false
}

func (r *LocationPublisherReconciler) recordBlocked(
	ctx context.Context,
	published *networkingv1alpha.ServingLocation,
	decision removalDecision,
) error {
	message := decision.reason + ": " + decision.message
	if published.Annotations[LocationRemovalBlockedAnnotation] == message {
		return nil
	}

	patch := client.MergeFrom(published.DeepCopy())
	if published.Annotations == nil {
		published.Annotations = map[string]string{}
	}
	published.Annotations[LocationRemovalBlockedAnnotation] = message
	if err := r.HubCluster.GetClient().Patch(ctx, published, patch); err != nil {
		return fmt.Errorf("failed recording the blocked removal: %w", err)
	}
	return nil
}

func (r *LocationPublisherReconciler) clearBlocked(
	ctx context.Context,
	published *networkingv1alpha.ServingLocation,
) error {
	if published == nil {
		return nil
	}
	if _, blocked := published.Annotations[LocationRemovalBlockedAnnotation]; !blocked {
		return nil
	}

	patch := client.MergeFrom(published.DeepCopy())
	delete(published.Annotations, LocationRemovalBlockedAnnotation)
	if err := r.HubCluster.GetClient().Patch(ctx, published, patch); err != nil {
		return fmt.Errorf("failed clearing the blocked removal: %w", err)
	}
	return nil
}

// applyPropagationPolicy carries a location's own objects to the cells serving
// it, and to no others. A network's presence and the range it is addressed from
// belong to one location, so they are selected by the location they name rather
// than fleet-wide: a cell serving nowhere would otherwise be given every
// project's addressing.
func (r *LocationPublisherReconciler) applyPropagationPolicy(ctx context.Context, name string) error {
	locationScoped := func(kind string) map[string]any {
		return map[string]any{
			"apiVersion": networkingv1alpha.GroupVersion.String(),
			"kind":       kind,
			"labelSelector": map[string]any{
				"matchLabels": map[string]any{
					networkingv1alpha.LocationLabel: name,
				},
			},
		}
	}

	policy := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"conflictResolution": "Overwrite",
			"resourceSelectors": []any{
				map[string]any{
					"apiVersion": networkingv1alpha.GroupVersion.String(),
					"kind":       "ServingLocation",
					"name":       name,
				},
				locationScoped("NetworkContext"),
				locationScoped("Subnet"),
			},
			"placement": map[string]any{
				"clusterAffinity": map[string]any{
					"labelSelector": map[string]any{
						"matchLabels": map[string]any{
							networkingv1alpha.ServingLocationTopologyLabel: name,
						},
					},
				},
			},
		},
	}}
	policy.SetGroupVersionKind(clusterPropagationPolicyGVK)
	policy.SetName(locationPropagationPolicyName(name))
	policy.SetLabels(map[string]string{LocationPublisherManagedByLabel: LocationPublisherManagedByValue})

	if err := r.HubCluster.GetClient().Patch(ctx, policy, client.Apply, //nolint:staticcheck // SA1019: the typed Apply API needs a generated ApplyConfiguration this unstructured policy has none of
		client.FieldOwner(locationPublisherFieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed applying the propagation policy for %q: %w", name, err)
	}
	return nil
}

func (r *LocationPublisherReconciler) deletePropagationPolicy(ctx context.Context, name string) error {
	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(clusterPropagationPolicyGVK)
	policy.SetName(locationPropagationPolicyName(name))

	if err := r.HubCluster.GetClient().Delete(ctx, policy); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed removing the propagation policy for %q: %w", name, err)
	}
	return nil
}

func locationPropagationPolicyName(name string) string {
	return "location-" + name
}

func (r *LocationPublisherReconciler) readPublished(
	ctx context.Context,
	name string,
) (*networkingv1alpha.ServingLocation, error) {
	var published networkingv1alpha.ServingLocation
	err := r.HubCluster.GetClient().Get(ctx, client.ObjectKey{Name: name}, &published)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed reading the published copy of %q: %w", name, err)
	}
	return &published, nil
}

func (r *LocationPublisherReconciler) listPublished(
	ctx context.Context,
) ([]networkingv1alpha.ServingLocation, error) {
	var list networkingv1alpha.ServingLocationList
	if err := r.HubCluster.GetClient().List(ctx, &list,
		client.MatchingLabels{LocationPublisherManagedByLabel: LocationPublisherManagedByValue}); err != nil {
		return nil, fmt.Errorf("failed listing published copies: %w", err)
	}
	return list.Items, nil
}

func (r *LocationPublisherReconciler) ownsObject(obj client.Object) bool {
	return obj.GetLabels()[LocationPublisherManagedByLabel] == LocationPublisherManagedByValue
}

func (r *LocationPublisherReconciler) reportForeignManagers(
	location *networkingv1alpha.Location,
	published *networkingv1alpha.ServingLocation,
) {
	if published == nil {
		return
	}
	for _, entry := range published.ManagedFields {
		if entry.Manager == locationPublisherFieldManager || entry.Manager == "" {
			continue
		}
		if entry.Operation != metav1.ManagedFieldsOperationApply &&
			entry.Operation != metav1.ManagedFieldsOperationUpdate {
			continue
		}
		if !ownsPublishedSpec(entry) {
			continue
		}
		locationPublisherConflictsTotal.WithLabelValues(location.Name, "ForeignFieldManager").Inc()
		r.event(location, "Warning", "ForeignFieldManager",
			fmt.Sprintf("ServingLocation %q carries spec fields owned by %q; this publisher forces them back",
				published.Name, entry.Manager))
	}
}

func ownsPublishedSpec(entry metav1.ManagedFieldsEntry) bool {
	if entry.FieldsV1 == nil {
		return false
	}
	var owned map[string]any
	if err := json.Unmarshal(entry.FieldsV1.GetRawBytes(), &owned); err != nil {
		return false
	}
	_, ownsSpec := owned["f:spec"]
	return ownsSpec
}

func (r *LocationPublisherReconciler) refreshMatchedClusters(ctx context.Context, name string) {
	clusters := &unstructured.UnstructuredList{}
	clusters.SetGroupVersionKind(karmadaClusterListGVK)
	if err := r.HubCluster.GetAPIReader().List(ctx, clusters); err != nil {
		log.FromContext(ctx).Error(err, "failed counting the clusters a location targets")
		return
	}

	var matched float64
	for i := range clusters.Items {
		item := &clusters.Items[i]
		if karmadaClusterReady(item) &&
			item.GetLabels()[networkingv1alpha.ServingLocationTopologyLabel] == name {
			matched++
		}
	}
	locationMatchedClusters.WithLabelValues(name).Set(matched)
}

func (r *LocationPublisherReconciler) refreshFleetMetrics(ctx context.Context) {
	logger := log.FromContext(ctx)

	var locations networkingv1alpha.LocationList
	if err := r.SourceCluster.GetClient().List(ctx, &locations); err != nil {
		logger.Error(err, "failed counting source locations")
		return
	}

	publishable := 0
	for i := range locations.Items {
		if strings.TrimSpace(locations.Items[i].Spec.Topology[networkingv1alpha.TopologyCityCodeKey]) != "" {
			publishable++
		}
	}

	published, err := r.listPublished(ctx)
	if err != nil {
		logger.Error(err, "failed counting published copies")
		return
	}

	live, retained := 0, 0
	for i := range published {
		if _, blocked := published[i].Annotations[LocationRemovalBlockedAnnotation]; blocked {
			retained++
			continue
		}
		live++
	}

	locationSourceTotal.Set(float64(publishable))
	locationPublishedTotal.Set(float64(live))
	locationRetainedTotal.Set(float64(retained))
}

func (r *LocationPublisherReconciler) event(obj client.Object, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(obj, nil, eventType, reason, "PublishLocation", "%s", message)
}

func publishedContentEqual(
	published *networkingv1alpha.ServingLocation,
	location *networkingv1alpha.Location,
	cityCode string,
) bool {
	if published.Spec.Source.Generation != location.Generation {
		return false
	}
	if published.CityCode() != cityCode {
		return false
	}
	if len(published.Spec.Topology) != len(location.Spec.Topology) {
		return false
	}
	for key, value := range location.Spec.Topology {
		if published.Spec.Topology[key] != value {
			return false
		}
	}
	return true
}

func (r *LocationPublisherReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.SourceCluster == nil || r.HubCluster == nil {
		return errors.New("a source cluster and a hub cluster are required to publish locations")
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("location-publisher")
	}

	publishedPolicy := &unstructured.Unstructured{}
	publishedPolicy.SetGroupVersionKind(clusterPropagationPolicyGVK)

	return ctrl.NewControllerManagedBy(mgr).
		Named("location_publisher").
		WatchesRawSource(source.TypedKind(
			r.SourceCluster.GetCache(),
			&networkingv1alpha.Location{},
			handler.TypedEnqueueRequestsFromMapFunc(enqueueByName[*networkingv1alpha.Location]()),
		)).
		WatchesRawSource(source.TypedKind(
			r.HubCluster.GetCache(),
			&networkingv1alpha.ServingLocation{},
			handler.TypedEnqueueRequestsFromMapFunc(enqueueByName[*networkingv1alpha.ServingLocation]()),
		)).
		WatchesRawSource(source.TypedKind(
			r.HubCluster.GetCache(),
			publishedPolicy,
			handler.TypedEnqueueRequestsFromMapFunc(enqueuePolicyOwner()),
		)).
		Complete(r)
}

func enqueueByName[T client.Object]() handler.TypedMapFunc[T, reconcile.Request] {
	return func(_ context.Context, obj T) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: obj.GetName()}}}
	}
}

func enqueuePolicyOwner() handler.TypedMapFunc[*unstructured.Unstructured, reconcile.Request] {
	return func(_ context.Context, obj *unstructured.Unstructured) []reconcile.Request {
		name := strings.TrimPrefix(obj.GetName(), "location-")
		if name == obj.GetName() {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name}}}
	}
}
