// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/util/status"
)

// ConnectorAdvertisementReconciler reconciles a ConnectorAdvertisement object
type ConnectorAdvertisementReconciler struct {
	mgr mcmanager.Manager
}

const (
	connectorAdvertisementControllerName    = "connectoradvertisement"
	connectorAdvertisementConnectorRefIndex = "spec.connectorRef.name"
)

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectoradvertisements/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch

func (r *ConnectorAdvertisementReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var advertisement networkingv1alpha1.ConnectorAdvertisement
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &advertisement); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !advertisement.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("reconciling connectoradvertisement")
	defer logger.V(1).Info("reconcile complete")

	stored := advertisement.DeepCopy()

	acceptedCondition := apimeta.FindStatusCondition(advertisement.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
	if acceptedCondition == nil {
		acceptedCondition = &metav1.Condition{
			Type: networkingv1alpha1.ConnectorAdvertisementConditionAccepted,
		}
	}
	acceptedCondition.ObservedGeneration = advertisement.Generation

	if advertisement.Spec.ConnectorRef.Name == "" {
		acceptedCondition.Status = metav1.ConditionFalse
		acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonPending
		acceptedCondition.Message = "connectorRef.name is required"
		apimeta.SetStatusCondition(&advertisement.Status.Conditions, *acceptedCondition)
	} else {
		connector := &networkingv1alpha1.Connector{}
		connectorKey := client.ObjectKey{Namespace: advertisement.Namespace, Name: advertisement.Spec.ConnectorRef.Name}
		err := cl.GetClient().Get(ctx, connectorKey, connector)
		if err == nil && !metav1.IsControlledBy(&advertisement, connector) {
			err = handOverAdvertisement(ctx, cl, &advertisement, connector)
		}
		switch {
		case apierrors.IsNotFound(err):
			acceptedCondition.Status = metav1.ConditionFalse
			acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound
			acceptedCondition.Message = fmt.Sprintf("Connector %q not found", connectorKey.Name)
		case errors.Is(err, errConnectorDeleting):
			acceptedCondition.Status = metav1.ConditionFalse
			acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound
			acceptedCondition.Message = fmt.Sprintf("Connector %q is being deleted", connectorKey.Name)
		case err != nil:
			return ctrl.Result{}, err
		default:
			acceptedCondition.Status = metav1.ConditionTrue
			acceptedCondition.Reason = networkingv1alpha1.ConnectorAdvertisementReasonAccepted
			acceptedCondition.Message = fmt.Sprintf("Connector %q resolved", connector.Name)
		}
		apimeta.SetStatusCondition(&advertisement.Status.Conditions, *acceptedCondition)
	}

	if !equality.Semantic.DeepEqual(stored.Status, advertisement.Status) {
		if statusErr := status.Patch(ctx, cl.GetClient(), &advertisement, stored, fieldOwner(connectorAdvertisementControllerName)); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating connectoradvertisement status: %w", statusErr)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorAdvertisementReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&networkingv1alpha1.ConnectorAdvertisement{},
		connectorAdvertisementConnectorRefIndex,
		func(obj client.Object) []string {
			advertisement, ok := obj.(*networkingv1alpha1.ConnectorAdvertisement)
			if !ok || advertisement.Spec.ConnectorRef.Name == "" {
				return nil
			}
			return []string{advertisement.Spec.ConnectorRef.Name}
		},
	); err != nil {
		return err
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.ConnectorAdvertisement{}).
		Watches(
			&networkingv1alpha1.Connector{},
			func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					logger := log.FromContext(ctx)
					connector, ok := obj.(*networkingv1alpha1.Connector)
					if !ok {
						return nil
					}

					var ads networkingv1alpha1.ConnectorAdvertisementList
					if err := cl.GetClient().List(
						ctx,
						&ads,
						client.InNamespace(connector.Namespace),
						client.MatchingFields{connectorAdvertisementConnectorRefIndex: connector.Name},
					); err != nil {
						logger.Error(err, "failed to list connectoradvertisements", "connector", connector.Name)
						return nil
					}

					requests := make([]mcreconcile.Request, 0, len(ads.Items))
					for i := range ads.Items {
						requests = append(requests, mcreconcile.Request{
							ClusterName: clusterName,
							Request: ctrl.Request{
								NamespacedName: client.ObjectKeyFromObject(&ads.Items[i]),
							},
						})
					}

					return requests
				})
			},
		).
		Named(connectorAdvertisementControllerName).
		Complete(r)
}

// errConnectorDeleting says the connector an advertisement names is being
// deleted, so it may not take the advertisement over.
var errConnectorDeleting = errors.New("connector is being deleted")

// handOverAdvertisement makes connector the controller of advertisement, and
// updates advertisement from the response. It drops every controller
// reference to a Connector, so a re-pointed advertisement no longer goes with
// its previous connector when the garbage collector deletes that one.
//
// It takes the cluster, not a client, so the connector is rechecked past the
// cache first. The write is one JSON patch without a resourceVersion, so an
// edit to any other field does not fail it. It tests that the advertisement
// still names connector and still has the owner references it was read with;
// if another writer changed either, the API server refuses the patch and the
// next reconcile reads again.
func handOverAdvertisement(
	ctx context.Context,
	cl cluster.Cluster,
	advertisement *networkingv1alpha1.ConnectorAdvertisement,
	connector *networkingv1alpha1.Connector,
) error {
	c := cl.GetClient()
	connectorGVK, err := c.GroupVersionKindFor(connector)
	if err != nil {
		return err
	}
	if err := recheckConnector(ctx, cl.GetAPIReader(), connector, connectorGVK); err != nil {
		return err
	}
	read := advertisement.GetOwnerReferences()
	desired := advertisement.DeepCopy()
	desired.SetOwnerReferences(withoutControllersOfKind(read, connectorGVK.GroupKind()))
	// Refuses a controller of any other kind, as it always has.
	if err := controllerutil.SetControllerReference(connector, desired, c.Scheme()); err != nil {
		return err
	}
	patch, err := advertisementHandOverPatch(connector.Name, read, desired.GetOwnerReferences())
	if err != nil {
		return err
	}
	return c.Patch(ctx, advertisement, client.RawPatch(types.JSONPatchType, patch),
		client.FieldOwner(fieldOwner(connectorAdvertisementControllerName)))
}

// recheckConnector reads connector's metadata past the cache, as Kubernetes'
// controllers recheck an owner before it adopts (RecheckDeletionTimestamp in
// k8s.io/kubernetes/pkg/controller, kubernetes/kubernetes#42639). It refuses a
// connector that is being deleted, and one the cache holds under another UID:
// one deleted and created again since. Either would leave the advertisement
// for the garbage collector.
func recheckConnector(ctx context.Context, reader client.Reader, connector *networkingv1alpha1.Connector, gvk schema.GroupVersionKind) error {
	live := &metav1.PartialObjectMetadata{}
	live.SetGroupVersionKind(gvk)
	if err := reader.Get(ctx, client.ObjectKeyFromObject(connector), live); err != nil {
		return err
	}
	if live.GetDeletionTimestamp() != nil {
		return errConnectorDeleting
	}
	if live.GetUID() != connector.GetUID() {
		return fmt.Errorf("connector %s has UID %s on the API server and %s in the cache",
			client.ObjectKeyFromObject(connector), live.GetUID(), connector.GetUID())
	}
	return nil
}

// withoutControllersOfKind returns refs without the controller references to
// an object of kind gk.
func withoutControllersOfKind(refs []metav1.OwnerReference, gk schema.GroupKind) []metav1.OwnerReference {
	kept := make([]metav1.OwnerReference, 0, len(refs))
	for _, ref := range refs {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err == nil && ptr.Deref(ref.Controller, false) && (schema.GroupKind{Group: gv.Group, Kind: ref.Kind}) == gk {
			continue
		}
		kept = append(kept, ref)
	}
	return kept
}

// advertisementHandOverPatch returns a JSON patch (RFC 6902) that sets the
// owner references to desired, if the spec still names connectorName and the
// owner references are still read. An "add" replaces a member that exists.
func advertisementHandOverPatch(connectorName string, read, desired []metav1.OwnerReference) ([]byte, error) {
	const ownersPath = "/metadata/ownerReferences"
	// An absent list is tested as null, which the API server's JSON patch
	// (json-patch v4) accepts for a missing member. The null is spelt out
	// because omitempty drops a nil.
	var tested any = json.RawMessage("null")
	if len(read) > 0 {
		tested = read
	}
	return json.Marshal([]jsonPatchOperation{
		{Op: "test", Path: "/spec/connectorRef/name", Value: connectorName},
		{Op: "test", Path: ownersPath, Value: tested},
		{Op: "add", Path: ownersPath, Value: desired},
	})
}
