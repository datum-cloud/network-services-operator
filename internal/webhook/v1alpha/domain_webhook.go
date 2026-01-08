// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// nolint:unused

// SetupDomainWebhookWithManager registers the webhook for Domain in the manager.
func SetupDomainWebhookWithManager(mgr mcmanager.Manager, downstreamClient client.Client) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&networkingv1alpha.Domain{}).
		WithValidator(&DomainCustomValidator{mgr: mgr, downstreamClient: downstreamClient}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-domain,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=domains,verbs=delete,versions=v1alpha,name=vdomain-v1alpha.kb.io,admissionReviewVersions=v1

type DomainCustomValidator struct {
	mgr              mcmanager.Manager
	downstreamClient client.Client
}

var _ webhook.CustomValidator = &DomainCustomValidator{}

var dnsZoneListGVK = schema.GroupVersionKind{
	Group:   "dns.networking.miloapis.com",
	Version: "v1alpha1",
	Kind:    "DNSZoneList",
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Domain.
func (v *DomainCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Domain.
func (v *DomainCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Domain.
func (v *DomainCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	domain, ok := obj.(*networkingv1alpha.Domain)
	if !ok {
		return nil, fmt.Errorf("expected a Domain object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	if v.downstreamClient == nil {
		return nil, fmt.Errorf("downstream client is nil")
	}

	logger := logf.FromContext(ctx).WithValues("cluster", clusterName)
	logger.Info("Validating Domain deletion", "name", domain.GetName(), "namespace", domain.GetNamespace())

	upstreamCluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	upstreamClient := upstreamCluster.GetClient()

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(clusterName, upstreamClient, v.downstreamClient)
	downstreamNamespace, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, domain.GetNamespace())
	if err != nil {
		return nil, fmt.Errorf("failed to derive downstream namespace for upstream namespace %q: %w", domain.GetNamespace(), err)
	}

	var downstreamHTTPProxies networkingv1alpha.HTTPProxyList
	if err := v.downstreamClient.List(ctx, &downstreamHTTPProxies, client.InNamespace(downstreamNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list downstream HTTPProxies in namespace %q: %w", downstreamNamespace, err)
	}

	domainName := normalizeHostname(domain.Spec.DomainName)
	for _, p := range downstreamHTTPProxies.Items {
		if !isConditionTrue(p.Status.Conditions, networkingv1alpha.HTTPProxyConditionAccepted) {
			continue
		}
		if !isConditionTrue(p.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed) {
			continue
		}

		for _, h := range p.Status.Hostnames {
			if normalizeHostname(string(h)) == domainName {
				gr := schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "domains"}
				return nil, apierrors.NewForbidden(
					gr,
					domain.GetName(),
					fmt.Errorf(
						"cannot delete Domain while in use by an HTTPProxy",
					),
				)
			}
		}
	}

	// Prevent deletion if a DNSZone references this Domain.
	// Check the downstream mapped namespace (project namespace), since DNSZones are translated.
	// If the DNSZone CRD is not installed in the downstream cluster, skip this check.
	zoneList := &unstructured.UnstructuredList{}
	zoneList.SetGroupVersionKind(dnsZoneListGVK)
	if err := v.downstreamClient.List(
		ctx,
		zoneList,
		client.InNamespace(downstreamNamespace),
	); err != nil {
		// "No match" indicates the GVK isn't served by the API server (schema/CRD not installed).
		if apimeta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed listing downstream DNSZones referencing Domain: %w", err)
	}

	for _, z := range zoneList.Items {
		refName, found, _ := unstructured.NestedString(z.Object, "status", "domainRef", "name")
		if !found {
			continue
		}
		if refName == domain.GetName() {
			gr := schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "domains"}
			return nil, apierrors.NewForbidden(
				gr,
				domain.GetName(),
				fmt.Errorf("cannot delete Domain while in use by a DNSZone"),
			)
		}
	}

	if len(zoneList.Items) > 0 {
		gr := schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "domains"}
		return nil, apierrors.NewForbidden(
			gr,
			domain.GetName(),
			fmt.Errorf("cannot delete Domain while in use by a DNSZone"),
		)
	}

	return nil, nil
}

func isConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	c := apimeta.FindStatusCondition(conditions, conditionType)
	return c != nil && c.Status == metav1.ConditionTrue
}

func normalizeHostname(h string) string {
	return strings.TrimSuffix(strings.TrimSpace(h), ".")
}
