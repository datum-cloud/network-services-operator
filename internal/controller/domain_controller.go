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
	"golang.org/x/net/publicsuffix"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	conditionutil "go.datum.net/network-services-operator/internal/util/condition"
	"go.datum.net/network-services-operator/pkg/registrydata"
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	timeNow   func() time.Time
	httpGet   func(ctx context.Context, url string) ([]byte, *http.Response, error)
	lookupTXT func(ctx context.Context, name string) ([]string, error)

	registryClient registrydata.Client
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

	logger.Info("reconciling domain")
	defer logger.Info("reconcile complete")

	origStatus := domain.Status.DeepCopy()

	// Validate domain is registrable (eTLD+1 present and not a public suffix only)
	validCond := conditionutil.FindStatusConditionOrDefault(domain.Status.Conditions, &metav1.Condition{
		Type:               networkingv1alpha.DomainConditionValidDomain,
		Status:             metav1.ConditionUnknown,
		Reason:             networkingv1alpha.DomainReasonValid,
		Message:            "",
		LastTransitionTime: metav1.Now(),
	})
	validCond.ObservedGeneration = domain.Generation

	apex, apexErr := registeredApex(domain.Spec.DomainName)
	registrable := (apexErr == nil && apex != "")
	if registrable {
		validCond.Status = metav1.ConditionTrue
		validCond.Reason = networkingv1alpha.DomainReasonValid
		validCond.Message = "Domain is registrable"
	} else {
		validCond.Status = metav1.ConditionFalse
		validCond.Reason = networkingv1alpha.DomainReasonInvalidDomain
		validCond.Message = "Domain is not registrable (public-suffix only or invalid)"
		// Clear timers to avoid auto-retries
		if domain.Status.Verification != nil {
			domain.Status.Verification.NextVerificationAttempt = metav1.Time{}
		}
		if domain.Status.Registration != nil {
			domain.Status.Registration.NextRefreshAttempt = metav1.Time{}
		}
	}
	apimeta.SetStatusCondition(&domain.Status.Conditions, *validCond)

	// Persist and short-circuit if invalid
	if validCond.Status == metav1.ConditionFalse {
		if !equality.Semantic.DeepEqual(*origStatus, domain.Status) {
			if err := cl.GetClient().Status().Update(ctx, domain); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed updating domain status: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Delegate all verification work (including timers/backoff)
	nextVerification := r.reconcileVerification(ctx, domain)

	// Delegate all registration work (including timers/backoff)
	nextRegistration := r.reconcileRegistration(ctx, domain, apex)

	// Persist status if changed
	if !equality.Semantic.DeepEqual(*origStatus, domain.Status) {
		if err := cl.GetClient().Status().Update(ctx, domain); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating domain status: %w", err)
		}
	}

	// Compute earliest independent timer
	now := r.timeNow()
	var wake *time.Time
	if !nextVerification.IsZero() {
		w := nextVerification
		wake = &w
	}
	if !nextRegistration.IsZero() {
		if wake == nil || nextRegistration.Before(*wake) {
			w := nextRegistration
			wake = &w
		}
	}

	if wake != nil {
		// If the wake time is in the future, schedule a requeue after the remaining duration.
		if wake.After(now) {
			remaining := wake.Sub(now)
			// Floor sub-second values to a minimum of 1s to avoid a zero requeue
			if remaining < time.Second {
				return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
			}
			return ctrl.Result{RequeueAfter: remaining.Truncate(time.Second)}, nil
		}
		// If the wake time is now or already in the past, use a minimal positive RequeueAfter
		// to avoid the deprecated Requeue flag.
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileVerification contains the verification logic.
// It mutates domain.Status and returns the next verification attempt time (if any).
func (r *DomainReconciler) reconcileVerification(ctx context.Context, domain *networkingv1alpha.Domain) time.Time {
	logger := log.FromContext(ctx)

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

	var nextAttempt time.Time

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

			// Schedule the first verification attempt immediately so the controller
			// will requeue and begin verification without waiting for an external trigger.
			domainStatus.Verification.NextVerificationAttempt = metav1.NewTime(r.timeNow())
			nextAttempt = domainStatus.Verification.NextVerificationAttempt.Time

			verifiedCondition.Message = "Update your DNS provider with record defined in `status.verification.dnsRecord`, " +
				"or HTTP server with token defined in `status.verification.httpToken`."
			verifiedDNSCondition.Message = "Update your DNS provider with record defined in `status.verification.dnsRecord`."
			verifiedHTTPCondition.Message = "Update your HTTP server with token defined in `status.verification.httpToken`."
		} else {
			// If we're not yet due, short-circuit
			if remaining := domainStatus.Verification.NextVerificationAttempt.Sub(r.timeNow()); remaining > 0 {
				logger.Info("not attempting another validation until remaining time elapsed", "remaining", remaining)
				nextAttempt = r.timeNow().Add(remaining)
			} else {
				// Compute next backoff based on elapsed since last transition time
				initialAttempt := verifiedCondition.LastTransitionTime.Time
				elapsed := r.timeNow().Sub(initialAttempt)
				logger.Info("time elapsed since last transition time", "duration", elapsed)

				requeueAfter := wait.Jitter(
					r.Config.DomainVerification.GetRetryInterval(elapsed),
					r.Config.DomainVerification.RetryJitterMaxFactor,
				)

				// Set the next attempt on status (this intentionally triggers an immediate reconcile via status update)
				domainStatus.Verification.NextVerificationAttempt = metav1.NewTime(r.timeNow().Add(requeueAfter))
				nextAttempt = domainStatus.Verification.NextVerificationAttempt.Time

				// Perform verification attempts now
				attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				r.attemptDNSVerification(attemptCtx, domainStatus, verifiedDNSCondition)

				if verifiedDNSCondition.Status != metav1.ConditionTrue {
					r.attemptHTTPVerification(attemptCtx, domainStatus, verifiedHTTPCondition)
				}

				if verifiedDNSCondition.Status == metav1.ConditionTrue || verifiedHTTPCondition.Status == metav1.ConditionTrue {
					verifiedCondition.Status = metav1.ConditionTrue
					verifiedCondition.Reason = networkingv1alpha.DomainReasonVerified
					verifiedCondition.Message = "Domain verification successful"

					// Clear verification scaffolding and sub-conditions
					domainStatus.Verification = nil
					apimeta.RemoveStatusCondition(&domainStatus.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
					apimeta.RemoveStatusCondition(&domainStatus.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP)
					// When verified, no future verification timer is needed
					nextAttempt = time.Time{}
				}
			}
		}
	}

	// Update conditions
	apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedCondition)
	if verifiedCondition.Status == metav1.ConditionFalse {
		apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedDNSCondition)
		apimeta.SetStatusCondition(&domainStatus.Conditions, *verifiedHTTPCondition)
	}

	// Commit the staged status back
	domain.Status = *domainStatus

	return nextAttempt
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

// reconcileRegistration keeps Status.Registration fresh and schedules independent timers.
// Returns the next registration refresh time (zero if none).
func (r *DomainReconciler) reconcileRegistration(ctx context.Context, d *networkingv1alpha.Domain, apex string) time.Time {
	logger := log.FromContext(ctx)

	st := &d.Status
	now := r.timeNow()

	if st.Registration == nil {
		st.Registration = &networkingv1alpha.Registration{}
	}

	// Compute desired-based behavior and candidate wake
	var candidateWake time.Time
	expedite := false
	if d.Spec.DesiredRegistrationRefreshAttempt != nil {
		desired := d.Spec.DesiredRegistrationRefreshAttempt.Time
		// Pending desired if we haven't attempted since desired
		desiredPending := (st.Registration.LastRefreshAttempt.IsZero() || st.Registration.LastRefreshAttempt.Time.Before(desired))
		// Expedite when desired time is now or in the past AND it's still pending.
		if !desired.IsZero() && !desired.After(now) && desiredPending {
			expedite = true
			logger.Info("expediting registration refresh per spec.desiredRegistrationRefreshAttempt", "desired", desired)
		} else if desiredPending && desired.After(now) {
			// Use desired future time as a wake candidate.
			candidateWake = desired
		}
	}
	// Consider NextRefreshAttempt as another wake candidate when in the future
	if !st.Registration.NextRefreshAttempt.IsZero() && st.Registration.NextRefreshAttempt.After(now) && !expedite {
		nra := st.Registration.NextRefreshAttempt.Time
		if candidateWake.IsZero() || nra.Before(candidateWake) {
			candidateWake = nra
		}
	}
	// If we have a future candidate wake and we're not expediting, return the soonest wake without attempting now.
	if !candidateWake.IsZero() && candidateWake.After(now) && !expedite {
		return candidateWake
	}

	// Indicate the time of the last refresh attempt (which is now)
	st.Registration.LastRefreshAttempt = metav1.NewTime(now)

	// apex is guaranteed valid by the ValidDomain gate
	st.Apex = strings.EqualFold(strings.TrimSuffix(d.Spec.DomainName, "."), apex)

	ctxLookup, cancel := context.WithTimeout(ctx, r.Config.DomainRegistration.LookupTimeout.Duration)
	defer cancel()

	// Registry data lookup (RDAP/WHOIS/DNS + caching + rate limiting)
	opts := registrydata.LookupOptions{ForceRefresh: expedite}
	res, lookupErr := r.registryClient.LookupDomain(ctxLookup, d.Spec.DomainName, opts)
	if res != nil {
		if res.Registration != nil {
			st.Registration = res.Registration
		}
		st.Nameservers = res.Nameservers
	}
	if st.Registration == nil {
		st.Registration = &networkingv1alpha.Registration{}
	}
	// Stamp the time we attempted a refresh now that we've built/updated the snapshot.
	st.Registration.LastRefreshAttempt = metav1.NewTime(now)

	// Schedule next refresh (with jitter)
	if lookupErr != nil {
		if rl, ok := lookupErr.(*registrydata.RateLimitedError); ok {
			delay := rl.RetryAfter
			if res != nil && res.SuggestedDelay > delay {
				delay = res.SuggestedDelay
			}
			if delay <= 0 {
				delay = r.Config.DomainRegistration.RetryBackoff.Duration
			}
			next := now.Add(wait.Jitter(delay, r.Config.DomainRegistration.JitterMaxFactor))
			st.Registration.NextRefreshAttempt = metav1.NewTime(next)
			return next
		}
		next := now.Add(r.Config.DomainRegistration.RetryBackoff.Duration)
		st.Registration.NextRefreshAttempt = metav1.NewTime(next)
		return next
	}

	interval := r.Config.DomainRegistration.RefreshInterval.Duration
	if res != nil && res.SuggestedDelay > interval {
		interval = res.SuggestedDelay
	}
	next := now.Add(wait.Jitter(interval, r.Config.DomainRegistration.JitterMaxFactor))
	st.Registration.NextRefreshAttempt = metav1.NewTime(next)
	return next
}

func registeredApex(name string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(name), ".")
	return publicsuffix.EffectiveTLDPlusOne(n)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	r.timeNow = time.Now
	r.httpGet = defaultHTTPGet
	r.lookupTXT = net.DefaultResolver.LookupTXT

	regClient, err := registrydata.NewClient(registrydata.Config{
		Cache: registrydata.CacheConfig{
			Backend: registrydata.CacheBackendMemory,
		},
		// Keep these relatively short; status is the long-term store.
		CacheTTLs: registrydata.CacheTTLs{
			Domain:       15 * time.Minute,
			Nameserver:   5 * time.Minute,
			IPRegistrant: 6 * time.Hour,
		},
		RateLimits: registrydata.RateLimits{
			DefaultRatePerSec: 1.0,
			DefaultBurst:      5,
			DefaultBlock:      2 * time.Second,
		},
		HTTPClient:         &http.Client{Timeout: r.Config.DomainRegistration.LookupTimeout.Duration},
		WhoisBootstrapHost: r.Config.DomainRegistration.WhoisBootstrapHost,
	})
	if err != nil {
		return err
	}
	r.registryClient = regClient
	return mcbuilder.ControllerManagedBy(mgr).
		// Watch all Domains so registration continues after verification
		For(&networkingv1alpha.Domain{}).
		WithOptions(controller.TypedOptions[mcreconcile.Request]{
			MaxConcurrentReconciles: r.Config.DomainVerification.MaxConcurrentVerifications,
		}).
		Named("domain").
		Complete(r)
}
