package downstreamclient

import (
	"context"
	"fmt"

	mchandler "github.com/multicluster-runtime/multicluster-runtime/pkg/handler"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ mchandler.EventHandler = &enqueueRequestForOwner[client.Object]{}

type empty struct{}

// TypedEnqueueRequestForUpstreamOwner enqueues Requests for the upstream Owners of an object.
//
// This handler depends on the `compute.datumapis.com/upstream-namespace` label
// to exist on the resource for the event.
func TypedEnqueueRequestForUpstreamOwner[object client.Object](ownerType client.Object, mgr mcmanager.Manager) mchandler.TypedEventHandlerFunc[object, mcreconcile.Request] {

	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[object, mcreconcile.Request] {
		e := &enqueueRequestForOwner[object]{
			ownerType: ownerType,
			mgr:       mgr,
		}
		if err := e.parseOwnerTypeGroupKind(cl.GetScheme()); err != nil {
			panic(err)
		}

		return e
	}
}

type enqueueRequestForOwner[object client.Object] struct {
	// ownerType is the type of the Owner object to look for in OwnerReferences.  Only Group and Kind are compared.
	ownerType runtime.Object

	// groupKind is the cached Group and Kind from OwnerType
	groupKind schema.GroupKind

	mgr mcmanager.Manager
}

// Create implements EventHandler.
func (e *enqueueRequestForOwner[object]) Create(ctx context.Context, evt event.TypedCreateEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]empty{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Update implements EventHandler.
func (e *enqueueRequestForOwner[object]) Update(ctx context.Context, evt event.TypedUpdateEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]empty{}
	e.getOwnerReconcileRequest(evt.ObjectOld, reqs)
	e.getOwnerReconcileRequest(evt.ObjectNew, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Delete implements EventHandler.
func (e *enqueueRequestForOwner[object]) Delete(ctx context.Context, evt event.TypedDeleteEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]empty{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Generic implements EventHandler.
func (e *enqueueRequestForOwner[object]) Generic(ctx context.Context, evt event.TypedGenericEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]empty{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// parseOwnerTypeGroupKind parses the OwnerType into a Group and Kind and caches the result.  Returns false
// if the OwnerType could not be parsed using the scheme.
func (e *enqueueRequestForOwner[object]) parseOwnerTypeGroupKind(scheme *runtime.Scheme) error {
	// Get the kinds of the type
	kinds, _, err := scheme.ObjectKinds(e.ownerType)
	if err != nil {
		return err
	}
	// Expect only 1 kind.  If there is more than one kind this is probably an edge case such as ListOptions.
	if len(kinds) != 1 {
		return fmt.Errorf("expected exactly 1 kind for OwnerType %T, but found %s kinds", e.ownerType, kinds)
	}
	// Cache the Group and Kind for the OwnerType
	e.groupKind = schema.GroupKind{Group: kinds[0].Group, Kind: kinds[0].Kind}
	return nil
}

// getOwnerReconcileRequest looks at object and builds a map of reconcile.Request to reconcile
// owners of object that match e.OwnerType.
func (e *enqueueRequestForOwner[object]) getOwnerReconcileRequest(obj metav1.Object, result map[mcreconcile.Request]empty) {
	labels := obj.GetLabels()
	if labels[UpstreamOwnerKindLabel] == e.groupKind.Kind && labels[UpstreamOwnerGroupLabel] == e.groupKind.Group {
		annotations := obj.GetAnnotations()

		fmt.Printf("object cluster: %q namespace: %q name: %q\n", annotations[UpstreamOwnerClusterNameLabel], obj.GetNamespace(), obj.GetName())
		request := mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      labels[UpstreamOwnerNameLabel],
					Namespace: labels[UpstreamOwnerNamespaceLabel],
				},
			},
			ClusterName: annotations[UpstreamOwnerClusterNameLabel],
		}
		result[request] = empty{}
	}
}
