// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	cmacmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/config"
)

// ChallengeReconciler watches cert-manager Challenge resources and automatically
// deletes challenges that enter an "errored" state for Gateway-related certificates.
// This triggers cert-manager to create a new challenge and retry the ACME verification.
type ChallengeReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator
}

// +kubebuilder:rbac:groups=acme.cert-manager.io,resources=challenges,verbs=get;list;watch;delete

// Reconcile handles the reconciliation of Challenge resources.
// If a challenge is in an "errored" state and is related to a Gateway-managed issuer,
// it will be deleted to trigger cert-manager to retry.
func (r *ChallengeReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	// Get the cluster client
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the Challenge resource
	challenge := &cmacmev1.Challenge{}
	if err := cl.GetClient().Get(ctx, req.NamespacedName, challenge); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only process challenges that are in the "errored" state
	if challenge.Status.State != cmacmev1.Errored {
		return ctrl.Result{}, nil
	}

	// Check if this challenge is for a Gateway-related issuer
	if !r.isGatewayRelatedIssuer(challenge.Spec.IssuerRef) {
		logger.V(1).Info("ignoring errored challenge for non-Gateway issuer",
			"challenge", challenge.Name,
			"issuerKind", challenge.Spec.IssuerRef.Kind,
			"issuerName", challenge.Spec.IssuerRef.Name,
		)
		return ctrl.Result{}, nil
	}

	// Delete the errored challenge to trigger cert-manager to retry
	logger.Info("deleting errored challenge to trigger retry",
		"challenge", challenge.Name,
		"namespace", challenge.Namespace,
		"dnsName", challenge.Spec.DNSName,
		"issuerKind", challenge.Spec.IssuerRef.Kind,
		"issuerName", challenge.Spec.IssuerRef.Name,
		"reason", challenge.Status.Reason,
	)

	if err := cl.GetClient().Delete(ctx, challenge); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// isGatewayRelatedIssuer checks if the given issuer reference is for a Gateway-managed
// certificate issuer. This includes:
// - ClusterIssuers that are mapped in the ClusterIssuerMap configuration
// - Issuers (namespace-scoped) when PerGatewayCertificateIssuer mode is enabled
func (r *ChallengeReconciler) isGatewayRelatedIssuer(ref cmmeta.ObjectReference) bool {
	// Check if ClusterIssuer is in the configured map
	if ref.Kind == "ClusterIssuer" || ref.Kind == "" {
		// Default kind is ClusterIssuer for cert-manager
		for _, mappedIssuer := range r.Config.Gateway.ClusterIssuerMap {
			if mappedIssuer == ref.Name {
				return true
			}
		}
	}

	// In per-gateway mode, any namespace-scoped Issuer is gateway-related
	if r.Config.Gateway.PerGatewayCertificateIssuer && ref.Kind == "Issuer" {
		return true
	}

	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChallengeReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		For(&cmacmev1.Challenge{}).
		Named("challenge").
		Complete(r)
}
