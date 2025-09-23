// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const gatewayResourceReplicatorFinalizer = "gateway.networking.datumapis.com/gateway-resource-replicator"

type statusTransformFunc func(ctx context.Context, upstreamNamespace string, controllerName string, status map[string]any) (map[string]any, error)

type reasonHandler struct {
	Message     string
	MessageFunc func(string) string
}

type conditionReasonHandlers map[string]map[string]reasonHandler

type replicationResourceConfig struct {
	statusTransform   statusTransformFunc
	conditionHandlers conditionReasonHandlers
}

type replicationResource struct {
	gvk               schema.GroupVersionKind
	statusTransform   statusTransformFunc
	conditionHandlers conditionReasonHandlers
	controllerName    string
}

var defaultReplicationResourceConfigs = initReplicationResourceConfigs()

func initReplicationResourceConfigs() map[string]replicationResourceConfig {
	configs := make(map[string]replicationResourceConfig)

	gatewayEnvoyGVKs := []schema.GroupVersionKind{
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "Backend"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "BackendTrafficPolicy"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "SecurityPolicy"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "HTTPRouteFilter"},
	}

	for _, gvk := range gatewayEnvoyGVKs {
		configs[gvkKey(gvk)] = replicationResourceConfig{
			statusTransform:   transformGatewayEnvoyPolicyStatus,
			conditionHandlers: defaultGatewayEnvoyReasonHandlers(),
		}
	}

	return configs
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

	resourceCfg, ok := r.resources[gvkKey(req.GVK)]
	if !ok {
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

	if err := r.ensureDownstreamResource(ctx, resourceCfg, upstreamClient, upstreamObj, downstreamStrategy); err != nil {
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
	resource replicationResource,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	logger := log.FromContext(ctx)
	downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamObj)
	if err != nil {
		return fmt.Errorf("failed to derive downstream metadata: %w", err)
	}

	downstreamObj := &unstructured.Unstructured{}
	downstreamObj.SetGroupVersionKind(upstreamObj.GroupVersionKind())
	downstreamObj.SetName(downstreamObjectMeta.Name)
	downstreamObj.SetNamespace(downstreamObjectMeta.Namespace)

	operation, err := controllerutil.CreateOrUpdate(ctx, downstreamStrategy.GetClient(), downstreamObj, func() error {
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

	if operation != controllerutil.OperationResultNone {
		logger.Info(
			"downstream resource synced",
			"operation", operation,
			"downstreamNamespace", downstreamObj.GetNamespace(),
			"downstreamName", downstreamObj.GetName(),
			"gvk", resource.gvk.String(),
		)
	}

	if err := r.syncUpstreamStatus(ctx, resource, upstreamClient, upstreamObj, downstreamObjectMeta, downstreamStrategy); err != nil {
		return err
	}

	return nil
}

func (r *GatewayResourceReplicatorReconciler) syncUpstreamStatus(
	ctx context.Context,
	resource replicationResource,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
	downstreamMeta metav1.ObjectMeta,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	logger := log.FromContext(ctx)
	downstreamObj := &unstructured.Unstructured{}
	downstreamObj.SetGroupVersionKind(upstreamObj.GroupVersionKind())

	if err := downstreamStrategy.GetClient().Get(ctx, client.ObjectKey{Name: downstreamMeta.Name, Namespace: downstreamMeta.Namespace}, downstreamObj); err != nil {
		if apierrors.IsNotFound(err) {
			return r.clearUpstreamStatusIfNeeded(ctx, upstreamClient, upstreamObj)
		}
		return fmt.Errorf("failed to fetch downstream resource %s/%s for status sync: %w", downstreamMeta.Namespace, downstreamMeta.Name, err)
	}

	downstreamStatus, ok := downstreamObj.Object["status"]
	if !ok {
		return r.clearUpstreamStatusIfNeeded(ctx, upstreamClient, upstreamObj)
	}

	statusMap, ok := runtime.DeepCopyJSONValue(downstreamStatus).(map[string]any)
	if !ok {
		return fmt.Errorf("downstream status for %s/%s is not an object", downstreamMeta.Namespace, downstreamMeta.Name)
	}

	if resource.statusTransform != nil {
		transformed, err := resource.statusTransform(ctx, upstreamObj.GetNamespace(), resource.controllerName, statusMap)
		if err != nil {
			return fmt.Errorf("failed to transform status for %s %s/%s: %w", upstreamObj.GroupVersionKind(), upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
		}
		statusMap = transformed
	}

	filterConditionMessagesInStatus(statusMap, resource.conditionHandlers)

	existingStatus, existingHas := upstreamObj.Object["status"]
	if existingHas {
		if existingMap, ok := runtime.DeepCopyJSONValue(existingStatus).(map[string]any); ok {
			if apiequality.Semantic.DeepEqual(existingMap, statusMap) {
				return nil
			}
		}
	}

	if len(statusMap) == 0 && !existingHas {
		return nil
	}

	upstreamCopy := upstreamObj.DeepCopy()

	if len(statusMap) == 0 {
		delete(upstreamCopy.Object, "status")
	} else {
		upstreamCopy.Object["status"] = statusMap
	}

	if err := upstreamClient.Status().Update(ctx, upstreamCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to update upstream status for %s %s/%s: %w", upstreamObj.GroupVersionKind(), upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}

	logger.Info(
		"upstream status synced",
		"gvk", upstreamObj.GroupVersionKind().String(),
		"namespace", upstreamObj.GetNamespace(),
		"name", upstreamObj.GetName(),
	)

	return nil
}

func (r *GatewayResourceReplicatorReconciler) clearUpstreamStatusIfNeeded(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx)
	existingStatus, ok := upstreamObj.Object["status"]
	if !ok {
		return nil
	}

	if existingMap, ok := runtime.DeepCopyJSONValue(existingStatus).(map[string]any); ok && len(existingMap) == 0 {
		return nil
	}

	upstreamCopy := upstreamObj.DeepCopy()
	delete(upstreamCopy.Object, "status")

	if err := upstreamClient.Status().Update(ctx, upstreamCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to clear upstream status for %s %s/%s: %w", upstreamObj.GroupVersionKind(), upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}

	logger.Info(
		"upstream status cleared",
		"gvk", upstreamObj.GroupVersionKind().String(),
		"namespace", upstreamObj.GetNamespace(),
		"name", upstreamObj.GetName(),
	)

	return nil
}

func filterConditionMessagesInStatus(status map[string]any, handlers conditionReasonHandlers) {
	if status == nil {
		return
	}

	filterConditionsInMap(status, handlers)
}

func filterConditionsInMap(obj map[string]any, handlers conditionReasonHandlers) {
	if obj == nil {
		return
	}

	if rawConditions, ok := obj["conditions"]; ok {
		if conditions, ok := rawConditions.([]any); ok {
			filterConditionList(conditions, handlers)
		}
	}

	for _, value := range obj {
		switch typed := value.(type) {
		case map[string]any:
			filterConditionsInMap(typed, handlers)
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					filterConditionsInMap(nested, handlers)
				}
			}
		}
	}
}

func filterConditionList(conditions []any, handlers conditionReasonHandlers) {
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}

		statusValue, _ := condition["status"].(string)
		if statusValue != string(metav1.ConditionFalse) {
			continue
		}

		typeValue, _ := condition["type"].(string)
		reasonValue, _ := condition["reason"].(string)
		messageValue, _ := condition["message"].(string)

		condition["message"] = resolveConditionMessage(typeValue, reasonValue, messageValue, handlers)
	}
}

func resolveConditionMessage(conditionType, conditionReason, existing string, handlers conditionReasonHandlers) string {
	if handlers != nil {
		if reasonHandlers, ok := handlers[conditionType]; ok {
			if handler, ok := reasonHandlers[conditionReason]; ok {
				if handler.MessageFunc != nil {
					return handler.MessageFunc(existing)
				}
				if handler.Message != "" {
					return handler.Message
				}
			}
		}
	}

	return defaultFalseConditionMessage(existing)
}

func defaultFalseConditionMessage(existing string) string {
	trimmed := strings.TrimSpace(existing)
	if trimmed == "" {
		return "downstream reported a failing condition"
	}
	return trimmed
}

func transformGatewayEnvoyPolicyStatus(_ context.Context, upstreamNamespace string, controllerName string, status map[string]any) (map[string]any, error) {
	if status == nil {
		return nil, nil
	}

	var policyStatus gwapiv1alpha2.PolicyStatus
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(status, &policyStatus); err != nil {
		return nil, fmt.Errorf("failed to decode policy status: %w", err)
	}

	if upstreamNamespace != "" || controllerName != "" {
		ns := gwapiv1.Namespace(upstreamNamespace)
		for i := range policyStatus.Ancestors {
			if upstreamNamespace != "" {
				policyStatus.Ancestors[i].AncestorRef.Namespace = ptr.To(ns)
			}
			if controllerName != "" {
				policyStatus.Ancestors[i].ControllerName = gwapiv1.GatewayController(controllerName)
			}
		}
	}

	unstructuredStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&policyStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to encode policy status: %w", err)
	}

	return unstructuredStatus, nil
}

func defaultGatewayEnvoyReasonHandlers() conditionReasonHandlers {
	return conditionReasonHandlers{
		string(gwapiv1alpha2.PolicyConditionAccepted): {
			string(gwapiv1alpha2.PolicyReasonConflicted): {
				Message: "conflicting policy attachments detected",
			},
			string(gwapiv1alpha2.PolicyReasonInvalid): {
				Message: "policy configuration is invalid",
			},
			string(gwapiv1alpha2.PolicyReasonTargetNotFound): {
				Message: "referenced resource not found in upstream namespace",
			},
		},
	}
}

func (r *GatewayResourceReplicatorReconciler) finalize(
	ctx context.Context,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	logger := log.FromContext(ctx)
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
	logger.Info("downstream resource deleted", "namespace", downstreamObj.GetNamespace(), "name", downstreamObj.GetName(), "gvk", upstreamObj.GroupVersionKind().String())

	if err := downstreamStrategy.DeleteAnchorForObject(ctx, upstreamObj); err != nil {
		return fmt.Errorf("failed to delete downstream anchor for %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}
	logger.Info("downstream anchor deleted", "gvk", upstreamObj.GroupVersionKind().String(), "namespace", upstreamObj.GetNamespace(), "name", upstreamObj.GetName())

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

		resource := replicationResource{gvk: gvk, controllerName: string(r.Config.Gateway.ControllerName)}
		if cfg, ok := defaultReplicationResourceConfigs[gvkKey(gvk)]; ok {
			resource.statusTransform = cfg.statusTransform
			resource.conditionHandlers = cfg.conditionHandlers
		}

		resources[gvkKey(gvk)] = resource

		upstreamWatchObj := newUnstructuredForGVK(gvk)

		builder = builder.Watches(upstreamWatchObj, typedEnqueueRequestForGVK(gvk, selector))

		downstreamWatchObj := newUnstructuredForGVK(gvk)

		src := mcsource.TypedKind(
			downstreamWatchObj,
			typedEnqueueDownstreamGVKRequest(gvk),
		)

		clusterSrc, err := src.ForCluster("", r.DownstreamCluster)
		if err != nil {
			return fmt.Errorf("failed to build downstream watch for %s: %w", gvk.String(), err)
		}

		builder = builder.WatchesRawSource(clusterSrc)
	}

	r.resources = resources

	return builder.Named("gateway_resource_replicator").Complete(r)
}

func newUnstructuredForGVK(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	return obj
}

func typedEnqueueDownstreamGVKRequest(gvk schema.GroupVersionKind) mchandler.TypedEventHandlerFunc[*unstructured.Unstructured, GVKRequest] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[*unstructured.Unstructured, GVKRequest] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj *unstructured.Unstructured) []GVKRequest {
			labels := obj.GetLabels()
			if labels == nil {
				return nil
			}

			if labels[downstreamclient.UpstreamOwnerGroupLabel] != gvk.Group || labels[downstreamclient.UpstreamOwnerKindLabel] != gvk.Kind {
				return nil
			}

			clusterLabel := labels[downstreamclient.UpstreamOwnerClusterNameLabel]
			if clusterLabel == "" {
				return nil
			}

			clusterName := strings.TrimPrefix(strings.ReplaceAll(clusterLabel, "_", "/"), "cluster-")

			request := GVKRequest{
				GVK: gvk,
				Request: mcreconcile.Request{
					ClusterName: clusterName,
					Request: reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      labels[downstreamclient.UpstreamOwnerNameLabel],
							Namespace: labels[downstreamclient.UpstreamOwnerNamespaceLabel],
						},
					},
				},
			}

			return []GVKRequest{request}
		})
	}
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
