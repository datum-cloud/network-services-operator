// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
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
	"go.datum.net/network-services-operator/internal/config"
	conditionutil "go.datum.net/network-services-operator/internal/util/condition"
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	timeNow   func() time.Time
	httpGet   func(ctx context.Context, url string) ([]byte, *http.Response, error)
	lookupTXT func(ctx context.Context, name string) ([]string, error)
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=domains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=domains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=domains/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DomainReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

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

	verifiedCondition := conditionutil.FindStatusConditionOrDefault(domainStatus.Conditions, &metav1.Condition{
		Type:               networkingv1alpha.DomainConditionVerified,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.DomainReasonPendingVerification,
		Message:            "The Domain has not been verified",
		LastTransitionTime: metav1.Now(),
	})
	verifiedCondition.ObservedGeneration = domain.Generation

	verifiedDNSCondition := conditionutil.FindStatusConditionOrDefault(domainStatus.Conditions, &metav1.Condition{
		Type:               networkingv1alpha.DomainConditionVerifiedDNS,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.DomainReasonPendingVerification,
		Message:            "The Domain has not been verified via DNS",
		LastTransitionTime: metav1.Now(),
	})
	verifiedDNSCondition.ObservedGeneration = domain.Generation

	verifiedHTTPCondition := conditionutil.FindStatusConditionOrDefault(domainStatus.Conditions, &metav1.Condition{
		Type:               networkingv1alpha.DomainConditionVerifiedHTTP,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.DomainReasonPendingVerification,
		Message:            "The Domain has not been verified via HTTP",
		LastTransitionTime: metav1.Now(),
	})
	verifiedHTTPCondition.ObservedGeneration = domain.Generation

	logger.Info("reconciling domain")
	defer logger.Info("reconcile complete")

	if verifiedCondition.Status != metav1.ConditionTrue {
		logger.Info("domain ownership has not been verified.")

		if domainStatus.Verification == nil {
			// Update the domain with content the user can leverage to update DNS or
			// HTTP endpoints for verification.
			logger.Info("updating domain with verification requirements")
			verificationContent := uuid.New().String()
			domainStatus.Verification = &networkingv1alpha.DomainVerificationStatus{
				DNSRecord: networkingv1alpha.DNSVerificationRecord{
					Name:    fmt.Sprintf("%s.%s", r.Config.DomainVerification.DNSVerificationRecordPrefix, domain.Spec.DomainName),
					Type:    "TXT",
					Content: verificationContent,
				},
				HTTPToken: networkingv1alpha.HTTPVerificationToken{
					URL:  fmt.Sprintf("http://%s/%s/%s", domain.Spec.DomainName, r.Config.DomainVerification.HTTPVerificationTokenPath, domain.UID),
					Body: verificationContent,
				},
			}

			verifiedCondition.Message = "Update your DNS provider with record defined in `status.verification.dnsRecord`, " +
				"or HTTP server with token defined in `status.verification.httpToken`."
			verifiedDNSCondition.Message = "Update your DNS provider with record defined in `status.verification.dnsRecord`."
			verifiedHTTPCondition.Message = "Update your HTTP server with token defined in `status.verification.httpToken`."
		} else {
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

			initialAttempt := verifiedCondition.LastTransitionTime.Time
			elapsed := r.timeNow().Sub(initialAttempt)
			logger.Info("time elapsed since last transition time", "duration", elapsed)

			requeueAfter := wait.Jitter(
				r.Config.DomainVerification.GetRetryInterval(elapsed),
				r.Config.DomainVerification.RetryJitterMaxFactor,
			)

			// Note that this will result in a reconcile to be enqueued immediately
			// upon updating the status. The delayed requeue for attempting lookups
			// is enforced above.
			domainStatus.Verification.NextVerificationAttempt = metav1.NewTime(r.timeNow().Add(requeueAfter))

			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			r.attemptDNSVerification(ctx, domainStatus, verifiedDNSCondition)

			if verifiedDNSCondition.Status != metav1.ConditionTrue {
				r.attemptHTTPVerification(ctx, domainStatus, verifiedHTTPCondition)
			}

			if verifiedDNSCondition.Status == metav1.ConditionTrue || verifiedHTTPCondition.Status == metav1.ConditionTrue {
				verifiedCondition.Status = metav1.ConditionTrue
				verifiedCondition.Reason = networkingv1alpha.DomainReasonVerified
				verifiedCondition.Message = "Domain verification successful"

				// Clear out verification info and unnecessary conditions
				domainStatus.Verification = nil
				apimeta.RemoveStatusCondition(&domainStatus.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				apimeta.RemoveStatusCondition(&domainStatus.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP)
			}
		}
	}

	apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedCondition)

	if verifiedCondition.Status == metav1.ConditionFalse {
		apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedDNSCondition)
		apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedHTTPCondition)
	}

	if !equality.Semantic.DeepEqual(domain.Status, domainStatus) {
		domain.Status = *domainStatus
		if statusErr := cl.GetClient().Status().Update(ctx, domain); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating domain status: %w", statusErr)
		}
	}

	return ctrl.Result{}, nil
}

func (r *DomainReconciler) attemptDNSVerification(
	ctx context.Context,
	domainStatus *networkingv1alpha.DomainStatus,
	verifiedDNSCondition *metav1.Condition,
) {
	logger := log.FromContext(ctx)

	logger.Info("looking for TXT record", "record", domainStatus.Verification.DNSRecord.Name+".")
	txtContent, err := r.lookupTXT(ctx, domainStatus.Verification.DNSRecord.Name+".")
	if err != nil {
		if dnsErr, ok := err.(*net.DNSError); ok {
			switch {
			case dnsErr.IsNotFound:
				verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordNotFound
				verifiedDNSCondition.Message = "TXT record not found"
			case dnsErr.IsTemporary, dnsErr.IsTimeout:
				verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonPendingVerification
				verifiedDNSCondition.Message = "Temporary error or timeout encountered"
			default:
				verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonVerificationInternalError
				verifiedDNSCondition.Message = "Internal error encountered during DNS lookup"
			}
		} else {
			verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonVerificationInternalError
			verifiedDNSCondition.Message = "Internal error encountered during DNS lookup"
		}
	} else {

		logger.Info("received DNS response")

		expectedContent := domainStatus.Verification.DNSRecord.Content
		if slices.Contains(txtContent, expectedContent) {
			verifiedDNSCondition.Status = metav1.ConditionTrue
			verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonVerified
			verifiedDNSCondition.Message = "TXT record verification successful"
		} else {
			verifiedDNSCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordContentMismatch
			verifiedDNSCondition.Message = fmt.Sprintf("TXT record content mismatch. Expected %q, got %q", expectedContent, strings.Join(txtContent, ", "))

		}
		logger.Info(verifiedDNSCondition.Message)
	}
}

func (r *DomainReconciler) attemptHTTPVerification(
	ctx context.Context,
	domainStatus *networkingv1alpha.DomainStatus,
	verifiedHTTPCondition *metav1.Condition,
) {
	logger := log.FromContext(ctx)

	logger.Info("looking for HTTP token", "url", domainStatus.Verification.HTTPToken.URL)
	responseBody, httpResponse, err := r.httpGet(ctx, domainStatus.Verification.HTTPToken.URL)
	if err != nil {
		if strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "connection refused") {
			verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonPendingVerification
			verifiedHTTPCondition.Message = "unable to establish connection with HTTP token endpoint"
		} else if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
			verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonPendingVerification
			verifiedHTTPCondition.Message = "Timeout encountered while fetching HTTP token"
		} else {
			logger.Error(err, "unhandled error during http token verification")
			verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonVerificationInternalError
			verifiedHTTPCondition.Message = "Internal error encountered during HTTP verification"
		}
	} else if httpResponse.StatusCode == http.StatusNotFound {
		verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordNotFound
		verifiedHTTPCondition.Message = "HTTP token endpoint not found"
	} else if httpResponse.StatusCode == http.StatusOK {
		logger.Info("received HTTP response")

		expectedContent := domainStatus.Verification.HTTPToken.Body
		actualContent := strings.TrimSpace(string(responseBody))
		if actualContent == expectedContent {
			verifiedHTTPCondition.Status = metav1.ConditionTrue
			verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonVerified
			verifiedHTTPCondition.Message = "HTTP token verification successful"
		} else {
			verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonVerificationRecordContentMismatch
			verifiedHTTPCondition.Message = fmt.Sprintf("HTTP token content mismatch. Expected %q, got %q", expectedContent, actualContent)
		}
		logger.Info(verifiedHTTPCondition.Message)
	} else {
		verifiedHTTPCondition.Reason = networkingv1alpha.DomainReasonVerificationUnexpectedResponse
		verifiedHTTPCondition.Message = fmt.Sprintf("unexpected status code from HTTP token endpoint. HTTP %d", httpResponse.StatusCode)
	}

}

// defaultHTTPGet is the default HTTP GET implementation for verification
func defaultHTTPGet(ctx context.Context, url string) ([]byte, *http.Response, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		// Avoid linter violation and exception
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, resp, nil
	}

	// Limit response body to 1KB to prevent malicious responses
	limitedReader := io.LimitReader(resp.Body, 1024)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, resp, fmt.Errorf("reading response body: %w", err)
	}

	return body, resp, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	r.timeNow = time.Now
	r.httpGet = defaultHTTPGet
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
			MaxConcurrentReconciles: r.Config.DomainVerification.MaxConcurrentVerifications,
		}).
		Named("domain").
		Complete(r)
}
