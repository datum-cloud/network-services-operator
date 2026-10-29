// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	PublicIPv4AddressesResourceType = "networking.datumapis.com/public-ipv4-addresses"

	networkLoadBalancerQuotaRetry = 10 * time.Second

	reasonQuotaPending     = "QuotaPending"
	reasonQuotaExceeded    = "QuotaExceeded"
	reasonQuotaRejected    = "QuotaRejected"
	reasonQuotaUnavailable = "QuotaUnavailable"
)

type quotaWithheld struct {
	reason  string
	message string
}

func (e *quotaWithheld) Error() string { return e.message }

func (r *NetworkLoadBalancerReconciler) enforcesAddressQuota(family networkingv1alpha.IPFamily) bool {
	return r.Quota != nil && family == networkingv1alpha.IPv4Protocol
}

func (r *NetworkLoadBalancerReconciler) holdAddressQuota(
	ctx context.Context, routing projectRouting, lb *networkingv1alpha.NetworkLoadBalancer,
	request loadBalancerAddressRequest, claimName string,
) (*quotaWithheld, error) {
	if !r.enforcesAddressQuota(request.family) {
		return nil, nil
	}
	cl, err := r.Quota.ClientForProject(routing.project)
	if err != nil {
		return nil, fmt.Errorf("build project quota client: %w", err)
	}
	desired := &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: routing.projectNamespace,
			Name:      claimName,
			Labels:    map[string]string{networkLoadBalancerOwnerLabel: string(lb.UID)},
		},
		Spec: quotav1alpha1.ResourceClaimSpec{
			ConsumerRef: quotav1alpha1.ConsumerRef{
				APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
				Kind:     "Project",
				Name:     routing.project,
			},
			ResourceRef: &quotav1alpha1.UnversionedObjectReference{
				APIGroup:  datumNetworkingAPIGroup,
				Kind:      "NetworkLoadBalancer",
				Name:      lb.Name,
				Namespace: routing.projectNamespace,
			},
			Requests: []quotav1alpha1.ResourceRequest{{ResourceType: PublicIPv4AddressesResourceType, Amount: 1}},
		},
	}

	existing := &quotav1alpha1.ResourceClaim{}
	err = cl.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, desired); err != nil {
			switch {
			case apierrors.IsAlreadyExists(err):
			case apierrors.IsForbidden(err) || apierrors.IsInvalid(err) || apierrors.IsNotFound(err):
				return &quotaWithheld{reasonQuotaUnavailable,
					fmt.Sprintf("Public IPv4 quota claim %q was rejected: %v", claimName, err)}, nil
			default:
				return nil, fmt.Errorf("file public IPv4 quota claim %q: %w", claimName, err)
			}
		}
		return &quotaWithheld{reasonQuotaPending, "Waiting for the project's public IPv4 address quota decision"}, nil
	} else if err != nil {
		return nil, fmt.Errorf("read public IPv4 quota claim %q: %w", claimName, err)
	}

	if existing.Labels[networkLoadBalancerOwnerLabel] != string(lb.UID) {
		return &quotaWithheld{reasonQuotaUnavailable,
			fmt.Sprintf("ResourceClaim %q does not belong to this load balancer", claimName)}, nil
	}
	if !existing.DeletionTimestamp.IsZero() {
		return &quotaWithheld{reasonQuotaPending, "Waiting for the project's public IPv4 address quota decision"}, nil
	}
	granted := apimeta.FindStatusCondition(existing.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
	switch {
	case granted == nil:
		return &quotaWithheld{reasonQuotaPending, "Waiting for the project's public IPv4 address quota decision"}, nil
	case granted.Status == metav1.ConditionTrue:
		return nil, nil
	case granted.Reason != quotav1alpha1.ResourceClaimDeniedReason &&
		granted.Reason != quotav1alpha1.ResourceClaimValidationFailedReason:
		return &quotaWithheld{reasonQuotaPending, "Waiting for the project's public IPv4 address quota decision"}, nil
	}

	if time.Since(granted.LastTransitionTime.Time) >= networkLoadBalancerRetry {
		if err := cl.Delete(ctx, existing, client.Preconditions{UID: &existing.UID}); err != nil &&
			!apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return nil, fmt.Errorf("refile denied public IPv4 quota claim %q: %w", claimName, err)
		}
	}
	if granted.Reason == quotav1alpha1.ResourceClaimValidationFailedReason {
		return &quotaWithheld{reasonQuotaRejected,
			fmt.Sprintf("The quota system rejected the public IPv4 address claim: %s", granted.Message)}, nil
	}
	return &quotaWithheld{reasonQuotaExceeded,
		"The project's public IPv4 address quota is exhausted; release an IPv4 address or request more quota"}, nil
}

func addressQuotaCondition(
	withheld *quotaWithheld, prior networkingv1alpha.NetworkLoadBalancerAddressStatus,
) (string, string) {
	if withheld.reason == reasonQuotaPending {
		if c := apimeta.FindStatusCondition(prior.Conditions, networkingv1alpha.NetworkLoadBalancerAllocated); c != nil &&
			c.Reason == reasonQuotaExceeded {
			return c.Reason, c.Message
		}
	}
	return withheld.reason, withheld.message
}

func (r *NetworkLoadBalancerReconciler) releaseAddressQuota(
	ctx context.Context, ipamClient client.Client, routing projectRouting,
	lb *networkingv1alpha.NetworkLoadBalancer, wanted map[string]bool,
) (bool, error) {
	if r.Quota == nil {
		return false, nil
	}
	cl, err := r.Quota.ClientForProject(routing.project)
	if err != nil {
		return false, fmt.Errorf("build project quota client: %w", err)
	}
	var claims quotav1alpha1.ResourceClaimList
	if err := cl.List(ctx, &claims, client.InNamespace(routing.projectNamespace),
		client.MatchingLabels{networkLoadBalancerOwnerLabel: string(lb.UID)}); err != nil {
		return false, fmt.Errorf("list public IPv4 quota claims: %w", err)
	}
	held := false
	for i := range claims.Items {
		claim := &claims.Items[i]
		if wanted[claim.Name] {
			continue
		}
		if old := priorClaimAddressStatus(lb.Status.Endpoints, claim.Name); old != nil && addressPendingWithdrawal(*old) {
			held = true
			continue
		}
		err := ipamClient.Get(ctx, client.ObjectKey{Namespace: routing.projectNamespace, Name: claim.Name}, &ipamv1alpha1.IPClaim{})
		if err == nil {
			held = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("read IPClaim %q before releasing its quota: %w", claim.Name, err)
		}
		if err := cl.Delete(ctx, claim, client.Preconditions{UID: &claim.UID}); err != nil &&
			!apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return false, fmt.Errorf("release public IPv4 quota claim %q: %w", claim.Name, err)
		}
	}
	return held, nil
}
