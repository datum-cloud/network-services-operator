// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const gatewayResourceReplicatorFinalizer = "gateway.networking.datumapis.com/gateway-resource-replicator"

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=trafficprotectionpolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=trafficprotectionpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/status,verbs=get;update;patch
// The replicator's default resource set also mirrors label-selected ConfigMaps/Secrets and the
// Envoy Gateway policy types. These watches require list/watch on the upstream cluster; without
// them the corresponding informers fail to sync and the replicator silently never reconciles ANY
// resource (including TrafficProtectionPolicy/HTTPProxy/Connector). See config.go
// SetDefaults_GatewayResourceReplicatorConfig.
// +kubebuilder:rbac:groups="",resources=configmaps;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.envoyproxy.io,resources=backends;backendtrafficpolicies;securitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.envoyproxy.io,resources=backends/finalizers;backendtrafficpolicies/finalizers;securitypolicies/finalizers,verbs=update

type statusTransformFunc func(ctx context.Context, upstreamNamespace string, controllerName string, status map[string]any) (map[string]any, error)

type reasonHandler struct {
	Message     string
	MessageFunc func(string) string
}

type conditionReasonHandlers map[string]map[string]reasonHandler

type replicationResourceConfig struct {
	statusGVK         *schema.GroupVersionKind
	statusTransform   statusTransformFunc
	conditionHandlers conditionReasonHandlers

	// mirrorStatusDownstream copies upstream status → downstream status after
	// each spec sync. Used for resource types (e.g. Connector) whose status is
	// authoritative in the upstream cluster and must be readable by consumers
	// in the downstream cluster (e.g. the extension server). When true,
	// skipUpstreamStatusSync is implicitly honoured as well.
	mirrorStatusDownstream bool

	// skipUpstreamStatusSync suppresses the normal downstream→upstream status
	// propagation. Set this for resource types where the upstream status is
	// managed by NSO's own controllers (not by a downstream controller), so
	// the replicator must NOT overwrite or clear it.
	skipUpstreamStatusSync bool
}

type replicationResource struct {
	gvk                       schema.GroupVersionKind
	replicationResourceConfig replicationResourceConfig
	controllerName            string
}

var defaultReplicationResourceConfigs = initReplicationResourceConfigs()

func initReplicationResourceConfigs() map[string]replicationResourceConfig {
	configs := make(map[string]replicationResourceConfig)

	gatewayEnvoyGVKs := []schema.GroupVersionKind{
		{Group: groupEnvoyGateway, Version: versionV1Alpha1, Kind: "BackendTrafficPolicy"},
		{Group: groupEnvoyGateway, Version: versionV1Alpha1, Kind: "SecurityPolicy"},
		{Group: groupEnvoyGateway, Version: versionV1Alpha1, Kind: "HTTPRouteFilter"},
		{Group: groupGatewayNetworking, Version: "v1alpha3", Kind: KindBackendTLSPolicy},
	}

	for _, gvk := range gatewayEnvoyGVKs {
		cfg := replicationResourceConfig{
			statusTransform:   transformGatewayEnvoyPolicyStatus,
			conditionHandlers: defaultGatewayEnvoyReasonHandlers(),
		}

		// Status updates have to go to the storage version of a resource
		if gvk.Group == groupGatewayNetworking && gvk.Kind == KindBackendTLSPolicy {
			cfg.statusGVK = &schema.GroupVersionKind{
				Group:   groupGatewayNetworking,
				Version: "v1",
				Kind:    KindBackendTLSPolicy,
			}
		}

		configs[gvkKey(gvk)] = cfg
	}

	backendGVK := schema.GroupVersionKind{Group: groupEnvoyGateway, Version: versionV1Alpha1, Kind: "Backend"}

	configs[gvkKey(backendGVK)] = replicationResourceConfig{
		statusTransform: func(ctx context.Context, upstreamNamespace, controllerName string, status map[string]any) (map[string]any, error) {
			return status, nil
		},
		conditionHandlers: defaultGatewayEnvoyReasonHandlers(),
	}

	// Policy types whose status is owned by NSO's upstream controllers —
	// the replicator mirrors spec downstream so the extension server can read
	// them from the local edge cluster. The replicator must NOT propagate
	// downstream status back upstream (there is no downstream controller
	// writing to these objects).
	policyGVKs := []schema.GroupVersionKind{
		{Group: groupNetworkingDatumAPIs, Version: versionV1Alpha, Kind: KindTrafficProtectionPolicy},
		{Group: groupNetworkingDatumAPIs, Version: versionV1Alpha, Kind: KindHTTPProxy},
	}
	for _, gvk := range policyGVKs {
		configs[gvkKey(gvk)] = replicationResourceConfig{
			skipUpstreamStatusSync: true,
		}
	}

	// Connector status (conditions + connectionDetails) is authoritative
	// upstream and must be readable by the extension server downstream so it
	// can determine whether a tunnel is online before injecting connector
	// cluster patches. Mirror status downstream; do not propagate back.
	connectorGVK := schema.GroupVersionKind{Group: groupNetworkingDatumAPIs, Version: versionV1Alpha1, Kind: KindConnector}
	configs[gvkKey(connectorGVK)] = replicationResourceConfig{
		mirrorStatusDownstream: true,
		skipUpstreamStatusSync: true,
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
		jsonKeyNamespace, req.Namespace,
		jsonKeyName, req.Name,
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

	// In single-cluster mode the downstream ns-<uid> namespaces live in the same
	// cluster as upstream namespaces. Skip objects whose namespace is already a
	// downstream-mapped namespace to prevent unbounded ns-<uid>→ns-ns-<uid>→…
	// replication recursion.
	if req.Namespace != "" {
		var ns corev1.Namespace
		if err := upstreamClient.Get(ctx, client.ObjectKey{Name: req.Namespace}, &ns); err == nil {
			if _, ok := ns.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]; ok {
				logger.V(5).Info("skipping downstream-mapped namespace as replication source")
				return ctrl.Result{}, nil
			}
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to check namespace labels: %w", err)
		}
	}

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(req.GVK)
	if err := upstreamClient.Get(ctx, req.NamespacedName, upstreamObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling resource")
	defer logger.Info("reconcile complete")

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(string(req.ClusterName), upstreamClient, r.DownstreamCluster.GetClient())

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

// nolint:unparam
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

	// Time the full downstream sync (CreateOrUpdate + status mirror) per resource
	// kind so replication latency regressions per family are attributable.
	syncStart := time.Now()
	syncOutcome := "success"
	defer func() {
		replicatorSyncDuration.WithLabelValues(resource.gvk.Kind, syncOutcome).Observe(
			time.Since(syncStart).Seconds(),
		)
	}()

	downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamObj)
	if err != nil {
		syncOutcome = syncOutcomeError
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
		// Conflict errors are retried automatically by controller-runtime but are
		// otherwise invisible. Count them separately so a rising rate signals
		// replication-path saturation under high churn.
		if apierrors.IsConflict(err) {
			replicatorConflictsTotal.WithLabelValues(resource.gvk.Kind).Inc()
		}
		syncOutcome = syncOutcomeError
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

	// Mirror upstream status → downstream when configured (e.g. Connector).
	// This is the reverse of syncUpstreamStatus: upstream is authoritative and
	// the downstream copy must reflect it so local consumers (extension server)
	// can read liveness without reaching into upstream project namespaces.
	if resource.replicationResourceConfig.mirrorStatusDownstream {
		if err := r.mirrorUpstreamStatusToDownstream(ctx, resource.gvk.Kind, upstreamObj, downstreamObj, downstreamStrategy); err != nil {
			syncOutcome = syncOutcomeError
			return err
		}
	}

	// Propagate downstream status → upstream for types where a downstream
	// controller (e.g. Envoy Gateway) writes acceptance conditions. Skip for
	// types whose status is owned by NSO's own upstream controllers.
	if !resource.replicationResourceConfig.skipUpstreamStatusSync {
		if err := r.syncUpstreamStatus(ctx, resource, upstreamClient, upstreamObj, downstreamObjectMeta, downstreamStrategy); err != nil {
			syncOutcome = syncOutcomeError
			return err
		}
	}

	return nil
}

// mirrorUpstreamStatusToDownstream copies the upstream object's status
// subresource to the corresponding downstream object. This is used for resource
// types (currently Connector) where the upstream cluster holds the authoritative
// status and downstream consumers need to read it locally.
func (r *GatewayResourceReplicatorReconciler) mirrorUpstreamStatusToDownstream(
	ctx context.Context,
	resourceKind string,
	upstreamObj *unstructured.Unstructured,
	downstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) error {
	logger := log.FromContext(ctx)

	// Re-fetch the downstream to get the current resourceVersion needed for
	// the status subresource update.
	currentDownstream := downstreamObj.DeepCopy()
	if err := downstreamStrategy.GetClient().Get(ctx, client.ObjectKeyFromObject(currentDownstream), currentDownstream); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to fetch downstream %s/%s for status mirror: %w",
			currentDownstream.GetNamespace(), currentDownstream.GetName(), err)
	}

	upstreamStatus, hasUpstreamStatus := upstreamObj.Object["status"]
	existingDownstreamStatus, hasExistingDownstreamStatus := currentDownstream.Object["status"]

	// Nothing to sync: upstream has no status and downstream already has none.
	if !hasUpstreamStatus && !hasExistingDownstreamStatus {
		return nil
	}

	// Already in sync: deep equality check avoids a spurious write.
	if hasUpstreamStatus && hasExistingDownstreamStatus &&
		apiequality.Semantic.DeepEqual(upstreamStatus, existingDownstreamStatus) {
		return nil
	}

	if hasUpstreamStatus {
		currentDownstream.Object["status"] = runtime.DeepCopyJSONValue(upstreamStatus)
	} else {
		delete(currentDownstream.Object, "status")
	}

	if err := downstreamStrategy.GetClient().Status().Update(ctx, currentDownstream); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		// Count status-mirror failures per resource kind so flaky downstream API
		// server connectivity surfaces as a metric rather than only as an
		// incremented generic reconcile error counter.
		replicatorStatusMirrorErrorsTotal.WithLabelValues(resourceKind).Inc()
		return fmt.Errorf("failed to mirror upstream status to downstream %s/%s: %w",
			currentDownstream.GetNamespace(), currentDownstream.GetName(), err)
	}

	logger.Info(
		"downstream status mirrored from upstream",
		"gvk", upstreamObj.GroupVersionKind().String(),
		jsonKeyNamespace, upstreamObj.GetNamespace(),
		"name", upstreamObj.GetName(),
	)

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
			return r.clearUpstreamStatusIfNeeded(ctx, resource, upstreamClient, upstreamObj)
		}
		return fmt.Errorf("failed to fetch downstream resource %s/%s for status sync: %w", downstreamMeta.Namespace, downstreamMeta.Name, err)
	}

	downstreamStatus, ok := downstreamObj.Object[jsonKeyStatus]
	if !ok {
		return r.clearUpstreamStatusIfNeeded(ctx, resource, upstreamClient, upstreamObj)
	}

	statusMap, ok := runtime.DeepCopyJSONValue(downstreamStatus).(map[string]any)
	if !ok {
		return fmt.Errorf("downstream status for %s/%s is not an object", downstreamMeta.Namespace, downstreamMeta.Name)
	}

	if resource.replicationResourceConfig.statusTransform != nil {
		transformed, err := resource.replicationResourceConfig.statusTransform(ctx, upstreamObj.GetNamespace(), resource.controllerName, statusMap)
		if err != nil {
			return fmt.Errorf("failed to transform status for %s %s/%s: %w", upstreamObj.GroupVersionKind(), upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
		}
		statusMap = transformed
	}

	filterConditionMessagesInStatus(statusMap, resource.replicationResourceConfig.conditionHandlers)

	existingStatus, existingHas := upstreamObj.Object[jsonKeyStatus]
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
		delete(upstreamCopy.Object, jsonKeyStatus)
	} else {
		upstreamCopy.Object[jsonKeyStatus] = statusMap
	}

	if resource.replicationResourceConfig.statusGVK != nil {
		upstreamCopy.SetGroupVersionKind(*resource.replicationResourceConfig.statusGVK)
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
		jsonKeyNamespace, upstreamObj.GetNamespace(),
		jsonKeyName, upstreamObj.GetName(),
	)

	return nil
}

func (r *GatewayResourceReplicatorReconciler) clearUpstreamStatusIfNeeded(
	ctx context.Context,
	resource replicationResource,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx)
	existingStatus, ok := upstreamObj.Object[jsonKeyStatus]
	if !ok {
		return nil
	}

	if existingMap, ok := runtime.DeepCopyJSONValue(existingStatus).(map[string]any); ok && len(existingMap) == 0 {
		return nil
	}

	upstreamCopy := upstreamObj.DeepCopy()
	delete(upstreamCopy.Object, jsonKeyStatus)
	if resource.replicationResourceConfig.statusGVK != nil {
		upstreamCopy.SetGroupVersionKind(*resource.replicationResourceConfig.statusGVK)
	}

	if err := upstreamClient.Status().Update(ctx, upstreamCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to clear upstream status for %s %s/%s: %w", upstreamObj.GroupVersionKind(), upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}

	logger.Info(
		"upstream status cleared",
		"gvk", upstreamObj.GroupVersionKind().String(),
		jsonKeyNamespace, upstreamObj.GetNamespace(),
		jsonKeyName, upstreamObj.GetName(),
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

		statusValue, _ := condition[jsonKeyStatus].(string)
		if statusValue != string(metav1.ConditionFalse) {
			continue
		}

		typeValue, _ := condition[jsonKeyType].(string)
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
	// In gateway-api v1.5.1, policy condition constants moved from v1alpha2 to v1.
	return conditionReasonHandlers{
		string(gwapiv1.PolicyConditionAccepted): {
			string(gwapiv1.PolicyReasonConflicted): {
				Message: "conflicting policy attachments detected",
			},
			string(gwapiv1.PolicyReasonInvalid): {
				Message: "policy configuration is invalid",
			},
			string(gwapiv1.PolicyReasonTargetNotFound): {
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
	logger.Info("downstream resource deleted", jsonKeyNamespace, downstreamObj.GetNamespace(), jsonKeyName, downstreamObj.GetName(), "gvk", upstreamObj.GroupVersionKind().String())

	if err := downstreamStrategy.DeleteAnchorForObject(ctx, upstreamObj); err != nil {
		return fmt.Errorf("failed to delete downstream anchor for %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}
	logger.Info("downstream anchor deleted", "gvk", upstreamObj.GroupVersionKind().String(), jsonKeyNamespace, upstreamObj.GetNamespace(), jsonKeyName, upstreamObj.GetName())

	return nil
}

// SetupWithManager wires the controller for the configured resource types.
func (r *GatewayResourceReplicatorReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	resources := make(map[string]replicationResource)

	builder := mcbuilder.TypedControllerManagedBy[GVKRequest](mgr)

	for _, resourceCfg := range r.Config.Gateway.ResourceReplicator.Resources {
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

		resource := replicationResource{
			gvk:            gvk,
			controllerName: string(r.Config.Gateway.ControllerName),
		}
		if cfg, ok := defaultReplicationResourceConfigs[gvkKey(gvk)]; ok {
			resource.replicationResourceConfig = cfg
		}

		resources[gvkKey(gvk)] = resource

		upstreamWatchObj := newUnstructuredForGVK(gvk)

		builder = builder.Watches(upstreamWatchObj, typedEnqueueRequestForGVK(gvk, selector))

		downstreamWatchObj := newUnstructuredForGVK(gvk)

		src := mcsource.TypedKind(
			downstreamWatchObj,
			typedEnqueueDownstreamGVKRequest(gvk),
		)

		clusterSrc, _, err := src.ForCluster("", r.DownstreamCluster)
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
	return func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[*unstructured.Unstructured, GVKRequest] {
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

			clusterName := multicluster.ClusterName(strings.TrimPrefix(strings.ReplaceAll(clusterLabel, "_", "/"), "cluster-"))

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
	return func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, GVKRequest] {
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
