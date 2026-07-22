// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	gatewaySyncLabel             = "networking.datumapis.com/gateway-sync"
	gatewaySyncManagedAnnotation = "networking.datumapis.com/gateway-sync-managed"
)

// reconcileReferencedSecretLabels keeps the gateway-sync label on the upstream
// Secrets a SecurityPolicy references in step with the live policies in the
// namespace. Labeled Secrets are mirrored downstream by the replicator (and
// propagated to the edge by Karmada), which is what releases the guard's hold.
//
// Only same-namespace references are auto-managed so the refcount stays sound:
// every managed Secret lives in the namespace whose policies are listed here.
func (r *GatewayResourceReplicatorReconciler) reconcileReferencedSecretLabels(
	ctx context.Context,
	upstreamClient client.Client,
	securityPolicyGVK schema.GroupVersionKind,
	namespace string,
) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   securityPolicyGVK.Group,
		Version: securityPolicyGVK.Version,
		Kind:    securityPolicyGVK.Kind + "List",
	})
	if err := upstreamClient.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list SecurityPolicies in %s: %w", namespace, err)
	}

	referenced := make(map[string]struct{})
	for i := range list.Items {
		policy := &list.Items[i]
		if !policy.GetDeletionTimestamp().IsZero() {
			continue
		}
		for _, ref := range securityPolicySecretRefs(policy) {
			if ref.namespace != namespace {
				continue
			}
			referenced[ref.name] = struct{}{}
		}
	}

	for name := range referenced {
		if err := r.ensureSecretSyncLabel(ctx, upstreamClient, namespace, name); err != nil {
			return err
		}
	}

	return r.gcSecretSyncLabels(ctx, upstreamClient, namespace, referenced)
}

func (r *GatewayResourceReplicatorReconciler) ensureSecretSyncLabel(
	ctx context.Context,
	upstreamClient client.Client,
	namespace, name string,
) error {
	var secret corev1.Secret
	if err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get referenced secret %s/%s: %w", namespace, name, err)
	}

	if _, ok := secret.Labels[gatewaySyncLabel]; ok {
		return nil
	}

	patch := client.MergeFrom(secret.DeepCopy())
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[gatewaySyncLabel] = "true"
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[gatewaySyncManagedAnnotation] = "true"

	if err := upstreamClient.Patch(ctx, &secret, patch); err != nil {
		return fmt.Errorf("failed to label referenced secret %s/%s: %w", namespace, name, err)
	}

	log.FromContext(ctx).Info("labeled referenced secret for propagation", jsonKeyNamespace, namespace, jsonKeyName, name)
	return nil
}

func (r *GatewayResourceReplicatorReconciler) gcSecretSyncLabels(
	ctx context.Context,
	upstreamClient client.Client,
	namespace string,
	referenced map[string]struct{},
) error {
	var secrets corev1.SecretList
	if err := upstreamClient.List(ctx, &secrets, client.InNamespace(namespace), client.HasLabels{gatewaySyncLabel}); err != nil {
		return fmt.Errorf("failed to list gateway-sync secrets in %s: %w", namespace, err)
	}

	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if secret.Annotations[gatewaySyncManagedAnnotation] != "true" {
			continue
		}
		if _, ok := referenced[secret.Name]; ok {
			continue
		}

		patch := client.MergeFrom(secret.DeepCopy())
		delete(secret.Labels, gatewaySyncLabel)
		delete(secret.Annotations, gatewaySyncManagedAnnotation)
		if err := upstreamClient.Patch(ctx, secret, patch); err != nil {
			return fmt.Errorf("failed to unlabel unreferenced secret %s/%s: %w", namespace, secret.Name, err)
		}

		log.FromContext(ctx).Info("unlabeled unreferenced secret", jsonKeyNamespace, namespace, jsonKeyName, secret.Name)
	}

	return nil
}
