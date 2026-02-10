// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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
)

// nolint:unused

// SetupDomainWebhookWithManager registers the webhook for Domain in the manager.
func SetupDomainWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&networkingv1alpha.Domain{}).
		WithValidator(&DomainCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-domain,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=domains,verbs=create;delete,versions=v1alpha,name=vdomain-v1alpha.kb.io,admissionReviewVersions=v1

type DomainCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &DomainCustomValidator{}

var dnsZoneListGVK = schema.GroupVersionKind{
	Group:   "dns.networking.miloapis.com",
	Version: "v1alpha1",
	Kind:    "DNSZoneList",
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Domain.
func (v *DomainCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	domain, ok := obj.(*networkingv1alpha.Domain)
	if !ok {
		return nil, fmt.Errorf("expected a Domain object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	upstreamCluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	upstreamClient := upstreamCluster.GetClient()

	var domains networkingv1alpha.DomainList
	if err := upstreamClient.List(ctx, &domains, client.InNamespace(domain.GetNamespace())); err != nil {
		return nil, fmt.Errorf("failed to list Domains in namespace %q: %w", domain.GetNamespace(), err)
	}

	target := normalizeHostname(domain.Spec.DomainName)
	for _, d := range domains.Items {
		if normalizeHostname(d.Spec.DomainName) == target {
			gr := schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "domains"}
			return nil, apierrors.NewConflict(
				gr,
				domain.GetName(),
				fmt.Errorf("domain resource with .spec.domainName %q already exists", domain.Spec.DomainName),
			)
		}
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Domain.
// Domain spec.domainName is immutable, so no additional update validation is required.
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

	logger := logf.FromContext(ctx).WithValues("cluster", clusterName)
	logger.Info("Validating Domain deletion", "name", domain.GetName(), "namespace", domain.GetNamespace())

	upstreamCluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	upstreamClient := upstreamCluster.GetClient()

	var httpProxies networkingv1alpha.HTTPProxyList
	if err := upstreamClient.List(ctx, &httpProxies, client.InNamespace(domain.GetNamespace())); err != nil {
		return nil, fmt.Errorf("failed to list HTTPProxies in namespace %q: %w", domain.GetNamespace(), err)
	}

	domainName := normalizeHostname(domain.Spec.DomainName)
	for _, p := range httpProxies.Items {
		// "In use" means any HTTPProxy references the hostname, regardless of whether it has
		// been Accepted/Programmed yet.
		for _, h := range p.Spec.Hostnames {
			if hostnameCoveredByDomain(domainName, string(h)) {
				gr := schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "domains"}
				return nil, apierrors.NewForbidden(
					gr,
					domain.GetName(),
					fmt.Errorf("cannot delete Domain while in use by an HTTPProxy"),
				)
			}
		}

		for _, h := range p.Status.Hostnames {
			if hostnameCoveredByDomain(domainName, string(h)) {
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
	// If the DNSZone CRD is not installed in the upstream cluster, skip this check.
	zoneList := &unstructured.UnstructuredList{}
	zoneList.SetGroupVersionKind(dnsZoneListGVK)
	if err := upstreamClient.List(
		ctx,
		zoneList,
		client.InNamespace(domain.GetNamespace()),
	); err != nil {
		// "No match" indicates the GVK isn't served by the API server (schema/CRD not installed).
		if apimeta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed listing DNSZones referencing Domain: %w", err)
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

	return nil, nil
}

func normalizeHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

// hostnameCoveredByDomain returns true if domainName matches hostname exactly, or
// if hostname is a subdomain of domainName (suffix match with dot boundary).
func hostnameCoveredByDomain(domainName, hostname string) bool {
	d := normalizeHostname(domainName)
	h := normalizeHostname(hostname)
	if d == "" || h == "" {
		return false
	}
	if h == d {
		return true
	}
	return strings.HasSuffix(h, "."+d)
}
