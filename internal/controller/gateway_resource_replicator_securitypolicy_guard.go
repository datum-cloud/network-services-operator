// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const securityPolicyPendingSecretReason = "PendingSecret"

var secretGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}

type securityPolicySecretRef struct {
	namespace string
	name      string
}

func isSecurityPolicyGVK(gvk schema.GroupVersionKind) bool {
	return gvk.Group == groupEnvoyGateway && gvk.Kind == "SecurityPolicy"
}

// securityPolicySecretRefs enumerates every Secret a SecurityPolicy spec can
// reference: the OIDC client secret and optional client-ID secret, the
// BasicAuth users secret, and the API-key credential secrets.
func securityPolicySecretRefs(obj *unstructured.Unstructured) []securityPolicySecretRef {
	spec, ok := obj.Object["spec"].(map[string]any)
	if !ok {
		return nil
	}

	policyNamespace := obj.GetNamespace()
	var refs []securityPolicySecretRef

	add := func(ref map[string]any) {
		if ref == nil {
			return
		}
		name, _ := ref[jsonKeyName].(string)
		if name == "" {
			return
		}
		namespace := policyNamespace
		if ns, ok := ref[jsonKeyNamespace].(string); ok && ns != "" {
			namespace = ns
		}
		refs = append(refs, securityPolicySecretRef{namespace: namespace, name: name})
	}

	if oidc, ok := spec["oidc"].(map[string]any); ok {
		add(mapField(oidc, "clientSecret"))
		add(mapField(oidc, "clientIDRef"))
	}
	if basicAuth, ok := spec["basicAuth"].(map[string]any); ok {
		add(mapField(basicAuth, "users"))
	}
	if apiKeyAuth, ok := spec["apiKeyAuth"].(map[string]any); ok {
		if credentialRefs, ok := apiKeyAuth["credentialRefs"].([]any); ok {
			for _, item := range credentialRefs {
				if ref, ok := item.(map[string]any); ok {
					add(ref)
				}
			}
		}
	}

	return refs
}

func mapField(obj map[string]any, key string) map[string]any {
	field, _ := obj[key].(map[string]any)
	return field
}

func securityPolicyReferencesSecret(obj *unstructured.Unstructured, namespace, name string) bool {
	for _, ref := range securityPolicySecretRefs(obj) {
		if ref.namespace == namespace && ref.name == name {
			return true
		}
	}
	return false
}

// missingDownstreamSecrets returns the SecurityPolicy secret references that are
// not yet present in the downstream control plane. A non-empty result means the
// policy must be held back from the shared gateway.
func (r *GatewayResourceReplicatorReconciler) missingDownstreamSecrets(
	ctx context.Context,
	upstreamObj *unstructured.Unstructured,
	downstreamStrategy downstreamclient.ResourceStrategy,
) ([]securityPolicySecretRef, error) {
	refs := securityPolicySecretRefs(upstreamObj)
	if len(refs) == 0 {
		return nil, nil
	}

	downstreamClient := downstreamStrategy.GetClient()
	var missing []securityPolicySecretRef

	for _, ref := range refs {
		downstreamNamespace, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, ref.namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve downstream namespace for secret %s/%s: %w", ref.namespace, ref.name, err)
		}

		var secret corev1.Secret
		err = downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: ref.name}, &secret)
		if apierrors.IsNotFound(err) {
			missing = append(missing, ref)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to check downstream secret %s/%s: %w", downstreamNamespace, ref.name, err)
		}
	}

	return missing, nil
}

// holdSecurityPolicy skips projecting a SecurityPolicy whose referenced Secrets
// are absent downstream and records a held condition on the source object.
func (r *GatewayResourceReplicatorReconciler) holdSecurityPolicy(
	ctx context.Context,
	resource replicationResource,
	upstreamClient client.Client,
	upstreamObj *unstructured.Unstructured,
	missing []securityPolicySecretRef,
) error {
	logger := log.FromContext(ctx)
	logger.Info(
		"holding SecurityPolicy: referenced secret not present downstream",
		"missingSecrets", secretRefNames(missing),
	)

	desired := securityPolicyHeldStatus(resource, upstreamObj, missing)

	statusMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&desired)
	if err != nil {
		return fmt.Errorf("failed to encode held SecurityPolicy status: %w", err)
	}

	if existing, ok := upstreamObj.Object[jsonKeyStatus].(map[string]any); ok {
		if apiequality.Semantic.DeepEqual(existing, statusMap) {
			return nil
		}
	}

	upstreamCopy := upstreamObj.DeepCopy()
	upstreamCopy.Object[jsonKeyStatus] = statusMap

	if err := upstreamClient.Status().Update(ctx, upstreamCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to record held status on SecurityPolicy %s/%s: %w", upstreamObj.GetNamespace(), upstreamObj.GetName(), err)
	}

	return nil
}

func securityPolicyHeldStatus(
	resource replicationResource,
	upstreamObj *unstructured.Unstructured,
	missing []securityPolicySecretRef,
) gwapiv1alpha2.PolicyStatus {
	var status gwapiv1alpha2.PolicyStatus
	if existing, ok := upstreamObj.Object[jsonKeyStatus].(map[string]any); ok {
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(existing, &status)
	}

	controllerName := gwapiv1.GatewayController(resource.controllerName)
	condition := metav1.Condition{
		Type:               string(gwapiv1.PolicyConditionAccepted),
		Status:             metav1.ConditionFalse,
		Reason:             securityPolicyPendingSecretReason,
		Message:            heldSecurityPolicyMessage(missing),
		ObservedGeneration: upstreamObj.GetGeneration(),
	}

	refs := securityPolicyAncestorRefs(upstreamObj)
	ancestors := make([]gwapiv1.PolicyAncestorStatus, 0, len(refs))
	for _, ref := range refs {
		conditions := existingAncestorConditions(status.Ancestors, ref, controllerName)
		apimeta.SetStatusCondition(&conditions, condition)
		ancestors = append(ancestors, gwapiv1.PolicyAncestorStatus{
			AncestorRef:    ref,
			ControllerName: controllerName,
			Conditions:     conditions,
		})
	}

	status.Ancestors = ancestors
	return status
}

func existingAncestorConditions(
	ancestors []gwapiv1.PolicyAncestorStatus,
	ref gwapiv1.ParentReference,
	controllerName gwapiv1.GatewayController,
) []metav1.Condition {
	for _, ancestor := range ancestors {
		if ancestor.ControllerName == controllerName && apiequality.Semantic.DeepEqual(ancestor.AncestorRef, ref) {
			return ancestor.Conditions
		}
	}
	return nil
}

func securityPolicyAncestorRefs(upstreamObj *unstructured.Unstructured) []gwapiv1.ParentReference {
	spec, _ := upstreamObj.Object["spec"].(map[string]any)
	namespace := gwapiv1.Namespace(upstreamObj.GetNamespace())

	var refs []gwapiv1.ParentReference
	build := func(target map[string]any) {
		name, _ := target[jsonKeyName].(string)
		if name == "" {
			return
		}
		ref := gwapiv1.ParentReference{
			Name:      gwapiv1.ObjectName(name),
			Namespace: ptr.To(namespace),
		}
		if group, ok := target["group"].(string); ok {
			ref.Group = ptr.To(gwapiv1.Group(group))
		}
		if kind, ok := target[jsonKeyKind].(string); ok {
			ref.Kind = ptr.To(gwapiv1.Kind(kind))
		}
		if sectionName, ok := target["sectionName"].(string); ok && sectionName != "" {
			ref.SectionName = ptr.To(gwapiv1.SectionName(sectionName))
		}
		refs = append(refs, ref)
	}

	if spec != nil {
		if target, ok := spec["targetRef"].(map[string]any); ok {
			build(target)
		}
		if targets, ok := spec["targetRefs"].([]any); ok {
			for _, item := range targets {
				if target, ok := item.(map[string]any); ok {
					build(target)
				}
			}
		}
	}

	if len(refs) == 0 {
		refs = append(refs, gwapiv1.ParentReference{
			Name:      gwapiv1.ObjectName(upstreamObj.GetName()),
			Namespace: ptr.To(namespace),
		})
	}

	return refs
}

func heldSecurityPolicyMessage(missing []securityPolicySecretRef) string {
	names := secretRefNames(missing)
	return fmt.Sprintf(
		"held from the shared gateway until referenced secret(s) are present downstream: %s",
		strings.Join(names, ", "),
	)
}

func secretRefNames(refs []securityPolicySecretRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, fmt.Sprintf("%s/%s", ref.namespace, ref.name))
	}
	sort.Strings(names)
	return names
}

// enqueueSecurityPoliciesForDownstreamSecret re-enqueues the SecurityPolicies
// that reference a Secret once that Secret becomes available in the downstream
// control plane, so a held policy is projected as soon as it is safe.
func (r *GatewayResourceReplicatorReconciler) enqueueSecurityPoliciesForDownstreamSecret(
	securityPolicyGVK schema.GroupVersionKind,
) mchandler.TypedEventHandlerFunc[*unstructured.Unstructured, GVKRequest] {
	return func(_ multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[*unstructured.Unstructured, GVKRequest] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, downstreamSecret *unstructured.Unstructured) []GVKRequest {
			labels := downstreamSecret.GetLabels()
			if labels == nil {
				return nil
			}

			upstreamNamespace := labels[downstreamclient.UpstreamOwnerNamespaceLabel]
			secretName := labels[downstreamclient.UpstreamOwnerNameLabel]
			if upstreamNamespace == "" || secretName == "" {
				return nil
			}

			clusterName := multicluster.ClusterName(
				downstreamclient.UpstreamClusterNameFromLabel(labels[downstreamclient.UpstreamOwnerClusterNameLabel]),
			)

			upstreamCluster, err := r.mgr.GetCluster(ctx, clusterName)
			if err != nil {
				return nil
			}

			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   securityPolicyGVK.Group,
				Version: securityPolicyGVK.Version,
				Kind:    securityPolicyGVK.Kind + "List",
			})
			if err := upstreamCluster.GetClient().List(ctx, list, client.InNamespace(upstreamNamespace)); err != nil {
				return nil
			}

			var requests []GVKRequest
			for i := range list.Items {
				policy := &list.Items[i]
				if !securityPolicyReferencesSecret(policy, upstreamNamespace, secretName) {
					continue
				}
				requests = append(requests, GVKRequest{
					GVK: securityPolicyGVK,
					Request: mcreconcile.Request{
						ClusterName: clusterName,
						Request: reconcile.Request{
							NamespacedName: types.NamespacedName{
								Namespace: policy.GetNamespace(),
								Name:      policy.GetName(),
							},
						},
					},
				})
			}

			return requests
		})
	}
}
