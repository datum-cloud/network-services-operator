// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

type jsonPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// saveGateway saves the HTTPProxy controller's part of its Gateway and returns
// the live Gateway. The default listeners' hostnames are the gateway
// controller's, so desired carries the default listeners without them.
func (r *HTTPProxyReconciler) saveGateway(
	ctx context.Context,
	cl cluster.Cluster,
	httpProxy *networkingv1alpha.HTTPProxy,
	desired *gatewayv1.Gateway,
) (*gatewayv1.Gateway, controllerutil.OperationResult, error) {
	c := cl.GetClient()
	live := &gatewayv1.Gateway{}
	err := c.Get(ctx, client.ObjectKeyFromObject(desired), live)
	if client.IgnoreNotFound(err) != nil {
		return nil, controllerutil.OperationResultNone, err
	}
	exists := err == nil
	if exists && hasControllerConflict(live, httpProxy) {
		return nil, controllerutil.OperationResultNone, apierrors.NewAlreadyExists(gatewayv1.Resource(KindGateway), desired.Name)
	}

	want := desired.DeepCopy()
	if err := controllerutil.SetControllerReference(httpProxy, want, c.Scheme()); err != nil {
		return nil, controllerutil.OperationResultNone, fmt.Errorf("failed to set controller on gateway: %w", err)
	}
	result := controllerutil.OperationResultCreated
	if exists {
		result = controllerutil.OperationResultUpdated
		if err := removeListeners(ctx, c, cl.GetAPIReader(), live, want); err != nil {
			return nil, controllerutil.OperationResultNone, err
		}
		if gatewayCarries(live, want) {
			return live, controllerutil.OperationResultNone, nil
		}
	}

	intent, err := gatewayIntent(want, live.UID)
	if err != nil {
		return nil, controllerutil.OperationResultNone, err
	}
	if err := c.Apply(ctx, intent, client.FieldOwner(fieldOwner(httpProxyControllerName)), client.ForceOwnership); err != nil {
		return nil, controllerutil.OperationResultNone, err
	}
	saved := &gatewayv1.Gateway{}
	if err := roundTrip(intent, saved); err != nil {
		return nil, controllerutil.OperationResultNone, err
	}
	return saved, result, nil
}

// removeListeners removes from live every listener want does not carry. An
// apply cannot remove an entry another field manager shares
// (kubernetes/kubernetes#128102), and the operator's earlier saves share every
// listener, so one JSON patch tests each name at its index and removes it.
// When another writer moved a listener first, the API server rejects the patch
// with 422 Invalid, as it rejects a spec that fails validation; a fresh read
// tells the two apart, and only a real rejection is returned as Invalid.
func removeListeners(ctx context.Context, c client.Client, reader client.Reader, live, want *gatewayv1.Gateway) error {
	var ops []jsonPatchOperation
	for i := len(live.Spec.Listeners) - 1; i >= 0; i-- {
		name := live.Spec.Listeners[i].Name
		if gatewayutil.GetListenerByName(want.Spec.Listeners, name) != nil {
			continue
		}
		path := fmt.Sprintf("/spec/listeners/%d", i)
		ops = append(ops, jsonPatchOperation{Op: "test", Path: path + "/name", Value: name}, jsonPatchOperation{Op: "remove", Path: path})
	}
	if len(ops) == 0 {
		return nil
	}
	patch, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	read := live.DeepCopy()
	err = c.Patch(ctx, live, client.RawPatch(types.JSONPatchType, patch), client.FieldOwner(fieldOwner(httpProxyControllerName)))
	if !apierrors.IsInvalid(err) {
		return err
	}
	fresh := &gatewayv1.Gateway{}
	if getErr := reader.Get(ctx, client.ObjectKeyFromObject(live), fresh); getErr != nil {
		return getErr
	}
	if !slices.EqualFunc(read.Spec.Listeners, fresh.Spec.Listeners, func(a, b gatewayv1.Listener) bool { return a.Name == b.Name }) {
		return fmt.Errorf("listeners of gateway %s moved during the save", client.ObjectKeyFromObject(live))
	}
	return err
}

// gatewayCarries reports whether live already carries everything want does,
// counting the default listeners' hostnames as the gateway controller's.
func gatewayCarries(live, want *gatewayv1.Gateway) bool {
	expected := want.DeepCopy()
	for i, listener := range expected.Spec.Listeners {
		if stored := gatewayutil.GetListenerByName(live.Spec.Listeners, listener.Name); stored != nil && gatewayutil.IsDefaultListener(listener) {
			expected.Spec.Listeners[i].Hostname = stored.Hostname
		}
	}
	return equality.Semantic.DeepEqual(expected.Spec, live.Spec) &&
		equality.Semantic.DeepEqual(metav1.GetControllerOf(expected), metav1.GetControllerOf(live))
}

// gatewayIntent is the apply body for want: its spec and controller reference,
// and the live Gateway's UID, so an apply never creates a Gateway that was
// deleted.
func gatewayIntent(want *gatewayv1.Gateway, uid types.UID) (*gatewayac.GatewayApplyConfiguration, error) {
	intent := gatewayac.Gateway(want.Name, want.Namespace)
	if err := roundTrip(want, intent); err != nil {
		return nil, err
	}
	intent.Status = nil
	if uid != "" {
		intent.WithUID(uid)
	}
	return intent, nil
}

func roundTrip(from, into any) error {
	body, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}
