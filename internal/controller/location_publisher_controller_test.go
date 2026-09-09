// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

type stubCache struct {
	cache.Cache
	synced bool
}

func (c *stubCache) WaitForCacheSync(context.Context) bool { return c.synced }

type stubCluster struct {
	cluster.Cluster
	client client.Client
	cache  cache.Cache
}

func (c *stubCluster) GetClient() client.Client    { return c.client }
func (c *stubCluster) GetAPIReader() client.Reader { return c.client }
func (c *stubCluster) GetCache() cache.Cache       { return c.cache }

func publisherScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatalf("failed building the scheme: %v", err)
	}
	scheme.AddKnownTypeWithName(karmadaClusterGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(karmadaClusterListGVK, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(clusterPropagationPolicyGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(clusterPropagationPolicyListGVK, &unstructured.UnstructuredList{})
	return scheme
}

func newPublisher(
	t *testing.T,
	sourceObjects []client.Object,
	hubObjects []client.Object,
	synced bool,
) (*LocationPublisherReconciler, client.Client, client.Client) {
	t.Helper()
	scheme := publisherScheme(t)

	sourceClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sourceObjects...).Build()
	hubClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hubObjects...).Build()

	reconciler := &LocationPublisherReconciler{
		Config:        config.NetworkServicesOperator{},
		SourceCluster: &stubCluster{client: sourceClient, cache: &stubCache{synced: synced}},
		HubCluster:    &stubCluster{client: hubClient, cache: &stubCache{synced: synced}},
	}
	return reconciler, sourceClient, hubClient
}

func sourceLocation(name, cityCode string) *networkingv1alpha.Location {
	location := &networkingv1alpha.Location{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: 4},
		Spec: networkingv1alpha.LocationSpec{
			LocationClassName: "datum-managed",
			Topology:          map[string]string{"topology.datum.net/region": "us-west-1"},
		},
	}
	if cityCode != "" {
		location.Spec.Topology[networkingv1alpha.TopologyCityCodeKey] = cityCode
	}
	return location
}

func publishedCopy() *networkingv1alpha.ServingLocation {
	published := &networkingv1alpha.ServingLocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "sjc-1",
			Labels: map[string]string{LocationPublisherManagedByLabel: LocationPublisherManagedByValue},
		},
		Spec: networkingv1alpha.ServingLocationSpec{
			Topology: map[string]string{networkingv1alpha.TopologyCityCodeKey: "SJC"},
		},
	}
	return published
}

func karmadaCluster(name string, ready bool, locationLabel string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetGroupVersionKind(karmadaClusterGVK)
	obj.SetName(name)
	if locationLabel != "" {
		obj.SetLabels(map[string]string{networkingv1alpha.ServingLocationTopologyLabel: locationLabel})
	}
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	_ = unstructured.SetNestedSlice(obj.Object, []any{
		map[string]any{"type": "Ready", "status": string(status)},
	}, "status", "conditions")
	return obj
}

func TestPublishRefusesALocationWithNoCityCode(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("sjc-1", "")}, nil, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no published copy, got err=%v", err)
	}
}

func TestPublishIsGatedOnExistenceNotReadiness(t *testing.T) {
	location := sourceLocation("sjc-1", "SJC")
	location.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "Degraded",
		LastTransitionTime: metav1.Now(),
	}}

	reconciler, _, hubClient := newPublisher(t, []client.Object{location}, nil, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("an unhealthy location must still be published: %v", err)
	}
	if published.CityCode() != "SJC" {
		t.Fatalf("expected city code SJC, got %q", published.CityCode())
	}
	if published.Spec.Source.Generation != 4 {
		t.Fatalf("expected source generation 4, got %d", published.Spec.Source.Generation)
	}
}

func TestPublishedAtTracksContentNotReconciles(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("sjc-1", "SJC")}, nil, true)

	request := ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	var first networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &first); err != nil {
		t.Fatalf("failed reading the published copy: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	var second networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &second); err != nil {
		t.Fatalf("failed re-reading the published copy: %v", err)
	}

	if !first.Spec.Source.PublishedAt.Equal(&second.Spec.Source.PublishedAt) {
		t.Fatalf("a no-op reconcile restamped publishedAt: %v then %v",
			first.Spec.Source.PublishedAt, second.Spec.Source.PublishedAt)
	}
	if !equality.Semantic.DeepEqual(first.Spec, second.Spec) {
		t.Fatalf("a no-op reconcile changed the published content: %+v then %+v",
			first.Spec, second.Spec)
	}
}

func TestPublishedAtMovesWhenContentChanges(t *testing.T) {
	reconciler, sourceClient, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("sjc-1", "SJC")}, nil, true)

	request := ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	var location networkingv1alpha.Location
	if err := sourceClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &location); err != nil {
		t.Fatalf("failed reading the source location: %v", err)
	}
	location.Spec.Topology["topology.datum.net/region"] = "us-west-2"
	if err := sourceClient.Update(context.Background(), &location); err != nil {
		t.Fatalf("failed updating the source location: %v", err)
	}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("failed reading the published copy: %v", err)
	}
	if published.Spec.Topology["topology.datum.net/region"] != "us-west-2" {
		t.Fatalf("the content change did not reach the hub: %v", published.Spec.Topology)
	}
}

func TestPublishWritesOnePolicyPerLocation(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("sjc-1", "SJC")}, nil, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(clusterPropagationPolicyGVK)
	if err := hubClient.Get(context.Background(),
		client.ObjectKey{Name: "location-sjc-1"}, policy); err != nil {
		t.Fatalf("failed reading the generated policy: %v", err)
	}

	matchLabels, _, err := unstructured.NestedStringMap(policy.Object,
		"spec", "placement", "clusterAffinity", "labelSelector", "matchLabels")
	if err != nil {
		t.Fatalf("failed reading the policy placement: %v", err)
	}
	if matchLabels[networkingv1alpha.ServingLocationTopologyLabel] != "sjc-1" {
		t.Fatalf("the policy does not target this location's cluster label: %v", matchLabels)
	}

	selectors, _, err := unstructured.NestedSlice(policy.Object, "spec", "resourceSelectors")
	if err != nil {
		t.Fatalf("failed reading the resource selectors: %v", err)
	}
	if len(selectors) != 3 {
		t.Fatalf("expected the location plus its presence objects, got %v", selectors)
	}

	selector := selectors[0].(map[string]any)
	if selector["name"] != "sjc-1" || selector["kind"] != "ServingLocation" {
		t.Fatalf("the policy selects the wrong resource: %v", selector)
	}

	// A cell serving nowhere must not be handed another location's addressing,
	// so everything derived from a location is selected by the location it names.
	for _, raw := range selectors[1:] {
		selector := raw.(map[string]any)
		matched, _, err := unstructured.NestedStringMap(selector, "labelSelector", "matchLabels")
		if err != nil {
			t.Fatalf("failed reading the selector for %v: %v", selector["kind"], err)
		}
		if matched[networkingv1alpha.LocationLabel] != "sjc-1" {
			t.Fatalf("%v is not scoped to this location: %v", selector["kind"], matched)
		}
	}

	kinds := []string{
		selectors[1].(map[string]any)["kind"].(string),
		selectors[2].(map[string]any)["kind"].(string),
	}
	if kinds[0] != "NetworkContext" || kinds[1] != "Subnet" {
		t.Fatalf("the policy carries the wrong presence kinds: %v", kinds)
	}
}

func TestAnEmptySourceListDoesNotPrune(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t, nil,
		[]client.Object{publishedCopy()}, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("an empty source list pruned a published copy: %v", err)
	}
}

func TestAnUnsyncedCacheDoesNotPrune(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("lhr-1", "LHR")},
		[]client.Object{publishedCopy()}, false)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("an unsynced cache pruned a published copy: %v", err)
	}
}

func TestAnUnlabelledHubObjectIsNeverDeleted(t *testing.T) {
	stranger := publishedCopy()
	stranger.Labels = nil

	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("lhr-1", "LHR")},
		[]client.Object{stranger}, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("an unlabelled hub object was deleted: %v", err)
	}
}

func TestARecreatedLocationStopsBeingRetained(t *testing.T) {
	retained := publishedCopy()
	retained.Annotations = map[string]string{
		LocationRemovalBlockedAnnotation: "ClustersStillServing: cell-a",
	}

	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("sjc-1", "SJC")},
		[]client.Object{retained}, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("failed reading the published copy: %v", err)
	}
	if _, blocked := published.Annotations[LocationRemovalBlockedAnnotation]; blocked {
		t.Fatal("a republished location is still marked as retained, so it stays out of the gap metric forever")
	}
}

func TestRemovalGuard(t *testing.T) {
	tests := []struct {
		name          string
		clusters      []client.Object
		expectAllowed bool
		expectReason  string
	}{
		{
			name: "a ready cluster still claims the location",
			clusters: []client.Object{
				karmadaCluster("cell-a", true, "sjc-1"),
				karmadaCluster("cell-b", true, "lhr-1"),
			},
			expectReason: "ClustersStillServing",
		},
		{
			name: "the fleet is fully labelled and nothing claims the location",
			clusters: []client.Object{
				karmadaCluster("cell-a", true, "lhr-1"),
				karmadaCluster("cell-b", true, "fra-1"),
			},
			expectAllowed: true,
		},
		{
			name: "the fleet is only partly labelled",
			clusters: []client.Object{
				karmadaCluster("cell-a", true, "lhr-1"),
				karmadaCluster("cell-b", true, ""),
			},
			expectReason: "FleetNotFullyLabelled",
		},
		{
			name: "an unlabelled cluster that is not ready does not block",
			clusters: []client.Object{
				karmadaCluster("cell-a", true, "lhr-1"),
				karmadaCluster("cell-b", false, ""),
			},
			expectAllowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconciler, _, _ := newPublisher(t, nil, test.clusters, true)

			decision, err := reconciler.evaluateRemovalGuard(context.Background(), "sjc-1")
			if err != nil {
				t.Fatalf("guard evaluation failed: %v", err)
			}
			if decision.allowed != test.expectAllowed {
				t.Fatalf("expected allowed=%v, got %v (%s: %s)",
					test.expectAllowed, decision.allowed, decision.reason, decision.message)
			}
			if decision.reason != test.expectReason {
				t.Fatalf("expected reason %q, got %q", test.expectReason, decision.reason)
			}
		})
	}
}

func TestABlockedRemovalRetainsTheCopy(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("lhr-1", "LHR")},
		[]client.Object{
			publishedCopy(),
			karmadaCluster("cell-a", true, "sjc-1"),
		}, true)

	result, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("a blocked removal must keep re-checking")
	}

	var published networkingv1alpha.ServingLocation
	if err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published); err != nil {
		t.Fatalf("the blocked copy was deleted: %v", err)
	}
	blocked := published.Annotations[LocationRemovalBlockedAnnotation]
	if blocked == "" {
		t.Fatal("the blocked reason must be readable on the retained copy")
	}
}

func TestRemovalProceedsOnceNothingClaimsTheLocation(t *testing.T) {
	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("lhr-1", "LHR")},
		[]client.Object{
			publishedCopy(),
			karmadaCluster("cell-a", true, "lhr-1"),
		}, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected the copy to be pruned, got err=%v", err)
	}
}

func TestTheOverrideAnnotationReleasesTheGuard(t *testing.T) {
	overridden := publishedCopy()
	overridden.Annotations = map[string]string{LocationRemovalOverrideAnnotation: "true"}

	reconciler, _, hubClient := newPublisher(t,
		[]client.Object{sourceLocation("lhr-1", "LHR")},
		[]client.Object{
			overridden,
			karmadaCluster("cell-a", true, "sjc-1"),
		}, true)

	if _, err := reconciler.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKey{Name: "sjc-1"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var published networkingv1alpha.ServingLocation
	err := hubClient.Get(context.Background(), client.ObjectKey{Name: "sjc-1"}, &published)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("the override did not release the guard, err=%v", err)
	}
}
