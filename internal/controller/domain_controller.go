// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr mcmanager.Manager

	timeNow func() time.Time

	lookupTXT func(ctx context.Context, name string) ([]string, error)
}

// +kubebuilder:rbac:groups=datumapis.com,resources=domains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DomainReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	// Get the cluster client
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the Domain instance
	domain := &networkingv1alpha.Domain{}
	if err := cl.GetClient().Get(ctx, req.NamespacedName, domain); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	domainStatus := domain.Status.DeepCopy()

	verifiedCondition := apimeta.FindStatusCondition(domainStatus.Conditions, networkingv1alpha.DomainConditionVerified)

	if verifiedCondition == nil || verifiedCondition.Status == metav1.ConditionUnknown {
		verifiedCondition = &metav1.Condition{
			Type:               networkingv1alpha.DomainConditionVerified,
			Status:             metav1.ConditionFalse,
			Reason:             networkingv1alpha.DomainReasonPendingVerification,
			Message:            "The Domain has not been verified",
			LastTransitionTime: metav1.Now(),
		}
	}

	defer func() {
		verifiedCondition.ObservedGeneration = domain.Generation
		apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedCondition)

		if !equality.Semantic.DeepEqual(domain.Status, domainStatus) {
			domain.Status = *domainStatus
			if statusErr := cl.GetClient().Status().Update(ctx, domain); statusErr != nil {
				err = errors.Join(err, fmt.Errorf("failed updating domain status: %w", statusErr))
			}
		}
	}()

	logger.Info("reconciling domain")
	defer logger.Info("reconcile complete")

	if !apimeta.IsStatusConditionTrue(domainStatus.Conditions, networkingv1alpha.DomainConditionVerified) {
		logger.Info("domain ownership has not been verified.")

		if domainStatus.Verification == nil {
			// Update the domain with content the user can leverage to update DNS or
			// HTTP endpoints for verification.

			logger.Info("updating domain with verification requirements")
			verificationContent := uuid.New().String()
			domainStatus.Verification = &networkingv1alpha.DomainVerificationStatus{
				DNSRecord: networkingv1alpha.DNSVerificationRecord{
					Name:    fmt.Sprintf("_datum-custom-hostname.%s", domain.Spec.DomainName),
					Type:    "TXT",
					Content: verificationContent,
				},
				HTTPToken: networkingv1alpha.HTTPVerificationToken{
					URL:  fmt.Sprintf("http://%s/.well-known/datum-custom-hostname-challenge/%s", domain.Spec.DomainName, domain.UID),
					Body: verificationContent,
				},
			}

			verifiedCondition.Message = "Update your DNS provider with record defined in `status.verification.dnsRecord`, " +
				"or HTTP server with token defined in `status.verification.httpToken`"

			// Early exit as there's no point trying to do a lookup
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		if remaining := domainStatus.Verification.NextVerificationAttempt.Sub(r.timeNow()).Truncate(time.Second); remaining > 0 {
			logger.Info("not attempting another validation until remaining time elapsed", "remaining", remaining)
			// This exists because we want to communicate timing expectations to
			// users, but by doing so we update the status information which triggers
			// a reconcile, and without this check it could lead to many sequential
			// updates to the resource.
			//
			// We can't have this logic in a predicate, because we couldn't schedule a
			// future reconcile.
			return ctrl.Result{RequeueAfter: remaining}, nil
		}

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Retry intervals:
		// - Every 5s for the first minute
		// - Every 60s after the first minute
		// - Every 5m after first five minutes

		var requeueAfter time.Duration
		initialAttempt := verifiedCondition.LastTransitionTime.Time
		elapsed := r.timeNow().Sub(initialAttempt)
		if elapsed < 60*time.Second {
			requeueAfter = 5 * time.Second
		} else if elapsed < 5*time.Minute {
			requeueAfter = time.Minute
		} else {
			requeueAfter = 5 * time.Minute
		}
		requeueAfter = wait.Jitter(requeueAfter, 0.25)

		logger.Info("looking for TXT record")

		domainStatus.Verification.NextVerificationAttempt = metav1.NewTime(r.timeNow().Add(requeueAfter))

		txtContent, err := r.lookupTXT(ctx, domainStatus.Verification.DNSRecord.Name+".")
		if err != nil {
			if dnsErr, ok := err.(*net.DNSError); ok {
				switch {
				case dnsErr.IsNotFound:
					verifiedCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordNotFound
					verifiedCondition.Message = "TXT record not found"
				case dnsErr.IsTemporary, dnsErr.IsTimeout:
					verifiedCondition.Reason = networkingv1alpha.DomainReasonPendingVerification
					verifiedCondition.Message = "Temporary error or timeout encountered"
				default:
					verifiedCondition.Reason = networkingv1alpha.DomainReasonVerificationInternalError
					verifiedCondition.Message = "Internal error encountered during DNS lookup"
				}
				// TODO(jreese) remove when we add http checks and split conditions
				return ctrl.Result{}, nil
			} else {
				verifiedCondition.Reason = networkingv1alpha.DomainReasonVerificationInternalError
				verifiedCondition.Message = "Internal error encountered during DNS lookup"
				// TODO(jreese) remove when we add http checks and split conditions
				return ctrl.Result{}, fmt.Errorf("encountered unknown error during TXT lookup: %s", err)
			}
		}

		logger.Info("received DNS response", "txt", strings.Join(txtContent, ","))

		expectedContent := domainStatus.Verification.DNSRecord.Content
		if slices.Contains(txtContent, expectedContent) {
			verifiedCondition.Status = metav1.ConditionTrue
			verifiedCondition.Reason = networkingv1alpha.DomainReasonVerified
			verifiedCondition.Message = "TXT record verification successful"
			logger.Info(verifiedCondition.Message)

			// Clear out verification info
			domainStatus.Verification = nil
			return ctrl.Result{}, nil
		} else {
			verifiedCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordContentMismatch
			verifiedCondition.Message = fmt.Sprintf("TXT record content mismatch. Expected %q, got %q", expectedContent, strings.Join(txtContent, ", "))
			logger.Info(verifiedCondition.Message)

			// TODO(jreese) remove when we add http checks and split conditions
			return ctrl.Result{}, nil
		}

		// TODO(jreese) implement this once DNS is solid
		// if _, loaded := r.httpInFlight.LoadOrStore(key, struct{}{}); loaded {
		// 	// May need to implement backoffs in the future
		// 	requeueAfter = 1 * time.Second
		// } else {
		// 	enqueued := !r.httpVerificationPool.TryGo(func() error {
		// 		defer r.httpInFlight.Delete(key)

		// 		return nil
		// 	})

		// 	if !enqueued {
		// 		// Pool was full, try again later
		// 		r.httpInFlight.Delete(key)
		// 		requeueAfter = 1 * time.Second
		// 	}
		// }
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	r.timeNow = time.Now
	r.lookupTXT = net.DefaultResolver.LookupTXT

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.Domain{}, mcbuilder.WithPredicates(
			predicate.NewTypedPredicateFuncs(func(domain client.Object) bool {
				return !apimeta.IsStatusConditionTrue(
					domain.(*networkingv1alpha.Domain).Status.Conditions,
					networkingv1alpha.DomainConditionVerified,
				)
			}),
		)).
		WithOptions(controller.TypedOptions[mcreconcile.Request]{
			// TODO(jreese) get from config
			MaxConcurrentReconciles: 5,
		}).
		Named("domain").
		Complete(r)
}
