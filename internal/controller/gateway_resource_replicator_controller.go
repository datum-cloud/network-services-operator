// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const gatewayResourceReplicatorFinalizer = "gateway.networking.datumapis.com/gateway-resource-replicator"

type replicationResource struct {
	gvk schema.GroupVersionKind
}

// GatewayResourceReplicatorReconciler mirrors configured upstream resources into the downstream control plane.
type GatewayResourceReplicatorReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster

	resources map[string]replicationResource
}

// Reconcile ensures the downstream resource mirrors the upstream resource, handling lifecycle via finalizers.
func (r *GatewayResourceReplicatorReconciler) Reconcile(ctx context.Context, req GVKRequest) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"gvk", req.GVK.String(),
		"cluster", req.ClusterName,
		"namespace", req.Namespace,
		"name", req.Name,
	)
	ctx = log.IntoContext(ctx, logger)

	if _, ok := r.resources[gvkKey(req.GVK)]; !ok {
		logger.Info("gvk not configured, skipping")
		return ctrl.Result{}, nil
	}

	upstreamCluster, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	upstreamClient := upstreamCluster.GetClient()

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(req.GVK)
	if err := upstreamClient.Get(ctx, req.NamespacedName, upstreamObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(req.ClusterName, upstreamClient, r.DownstreamCluster.GetClient())

	if !upstreamObj.GetDeletionTimestamp().IsZero() {
		return r.finalizeResource(ctx, upstreamClient, upstreamObj, downstreamStrategy)
	}

	if !controllerutil.ContainsFinalizer(upstreamObj, gatewayResourceReplicatorFinalizer) {
		controllerutil.AddFinalizer(upstreamObj, gatewayResourceReplicatorFinalizer)
		if err := upstreamClient.Update(ctx, upstreamObj); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
		}
		return ctrl.Result{}, nil
	}

	if err := r.ensureDownstreamResource(ctx, upstreamObj, downstreamStrategy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *GatewayResourceReplicatorReconciler) finalizeResource(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(upstreamObj, gatewayResourceReplicatorFinalizer) {
		if err := r.finalize(ctx, upstreamObj, downstreamStrategy); err != nil {
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(upstreamObj, gatewayResourceReplicatorFinalizer)
		if err := upstreamClient.Update(ctx, upstreamObj); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *GatewayResourceReplicatorReconciler) ensureDownstreamResource(
	ctx context.Context,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamObj)
	if err != nil {
		return fmt.Errorf("failed to derive downstream metadata: %w", err)
	}

	downstreamObj := &unstructured.Unstructured{}
	downstreamObj.SetGroupVersionKind(upstreamObj.GroupVersionKind())
	downstreamObj.SetName(downstreamObjectMeta.Name)
	downstreamObj.SetNamespace(downstreamObjectMeta.Namespace)

	_, err = controllerutil.CreateOrUpdate(ctx, downstreamStrategy.GetClient(), downstreamObj, func() error {
		downstreamObj.SetLabels(maps.Clone(upstreamObj.GetLabels()))
		downstreamObj.SetAnnotations(maps.Clone(upstreamObj.GetAnnotations()))

		if spec, ok := upstreamObj.Object["spec"]; ok {
			downstreamObj.Object["spec"] = runtime.DeepCopyJSONValue(spec)
		} else {
			delete(downstreamObj.Object, "spec")
		}

		if data, ok := upstreamObj.Object["data"]; ok {
			downstreamObj.Object["data"] = runtime.DeepCopyJSONValue(data)
		}

		if err := downstreamStrategy.SetControllerReference(ctx, upstreamObj, downstreamObj); err != nil {
			return fmt.Errorf("failed to set downstream controller reference: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure downstream resource %s/%s: %w", downstreamObj.GetNamespace(), downstreamObj.GetName(), err)
	}

	// TODO(jreese) sync downstream resource status to upstream resource.

	return nil
}

func (r *GatewayResourceReplicatorReconciler) finalize(
	ctx context.Context,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamObj)
	if err != nil {
		return fmt.Errorf("failed to resolve downstream metadata for cleanup: %w", err)
	}

	downstreamObj := &unstructured.Unstructured{}
	downstreamObj.SetGroupVersionKind(upstreamObj.GroupVersionKind())
	downstreamObj.SetName(downstreamObjectMeta.Name)
	downstreamObj.SetNamespace(downstreamObjectMeta.Namespace)

	if err := downstreamStrategy.GetClient().Delete(ctx, downstreamObj); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete downstream resource %s/%s: %w", downstreamObj.GetNamespace(), downstreamObj.GetName(), err)
	}

	if err := downstreamStrategy.DeleteAnchorForObject(ctx, upstreamObj); err != nil {
		return fmt.Errorf("failed to delete downstream anchor for %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}

	return nil
}

// SetupWithManager wires the controller for the configured resource types.
func (r *GatewayResourceReplicatorReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	resources := make(map[string]replicationResource)

	builder := mcbuilder.TypedControllerManagedBy[GVKRequest](mgr)

	for _, resourceCfg := range r.Config.GatewayResourceReplicator.Resources {
		if resourceCfg.Version == "" || resourceCfg.Kind == "" {
			return fmt.Errorf("invalid gateway resource replicator config entry: %+v", resourceCfg)
		}

		gvk := schema.GroupVersionKind{
			Group:   resourceCfg.Group,
			Version: resourceCfg.Version,
			Kind:    resourceCfg.Kind,
		}

		selector := labels.Everything()
		if resourceCfg.LabelSelector != nil {
			parsedSelector, err := metav1.LabelSelectorAsSelector(resourceCfg.LabelSelector)
			if err != nil {
				return fmt.Errorf("failed to parse label selector for %s: %w", gvk.String(), err)
			}
			selector = parsedSelector
		}

		resources[gvkKey(gvk)] = replicationResource{gvk: gvk}

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)

		builder = builder.Watches(obj, typedEnqueueRequestForGVK(gvk, selector))
	}

	r.resources = resources

	return builder.Named("gateway_resource_replicator").Complete(r)
}

func typedEnqueueRequestForGVK(
	gvk schema.GroupVersionKind,
	selector labels.Selector,
) mchandler.TypedEventHandlerFunc[client.Object, GVKRequest] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, GVKRequest] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []GVKRequest {
			if selector != nil && !selector.Matches(labels.Set(obj.GetLabels())) {
				return nil
			}

			return []GVKRequest{
				{
					GVK: gvk,
					Request: mcreconcile.Request{
						ClusterName: clusterName,
						Request: reconcile.Request{
							NamespacedName: types.NamespacedName{
								Namespace: obj.GetNamespace(),
								Name:      obj.GetName(),
							},
						},
					},
				},
			}
		})
	}
}

func gvkKey(gvk schema.GroupVersionKind) string {
	return gvk.String()
}
