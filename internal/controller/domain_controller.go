// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	whois "github.com/domainr/whois"
	"github.com/google/uuid"
	"github.com/openrdap/rdap"
	"github.com/openrdap/rdap/bootstrap"
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
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	timeNow     func() time.Time
	httpGet     func(ctx context.Context, url string) ([]byte, *http.Response, error)
	lookupTXT   func(ctx context.Context, name string) ([]string, error)
	lookupNS    func(ctx context.Context, name string) ([]*net.NS, error)
	lookupIP    func(ctx context.Context, name string) ([]net.IPAddr, error)
	rdapDo      func(ctx context.Context, req *rdap.Request) (*rdap.Response, error)
	rdapQueryIP func(ctx context.Context, ip string) (*rdap.IPNetwork, error)
	whoisFetch  func(ctx context.Context, query, host string) (string, error)

	// external clients
	rdapClient *rdap.Client
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

	if !st.Registration.NextRefreshAttempt.IsZero() &&
		st.Registration.NextRefreshAttempt.After(now) {
		return st.Registration.NextRefreshAttempt.Time
	}

	// apex is guaranteed valid by the ValidDomain gate
	st.Apex = strings.EqualFold(strings.TrimSuffix(d.Spec.DomainName, "."), apex)

	ctxLookup, cancel := context.WithTimeout(ctx, r.Config.DomainRegistration.LookupTimeout.Duration)
	defer cancel()

	logger.Info("selecting registration protocol", "apex", apex, "domain", d.Spec.DomainName)
	// Try RDAP; if bootstrap says no match, prefer WHOIS; otherwise RDAP only
	useWHOIS := false
	req := &rdap.Request{Type: rdap.DomainRequest, Query: apex, Timeout: r.Config.DomainRegistration.LookupTimeout.Duration}
	resp, err := r.rdapDo(ctxLookup, req)
	if err != nil {
		if ce, ok := err.(*rdap.ClientError); ok && ce.Type == rdap.BootstrapNoMatch {
			logger.Info("rdap bootstrap reported no match; falling back to whois", "apex", apex)
			useWHOIS = true
		} else {
			logger.Error(err, "rdap lookup failed")
			st.Registration.NextRefreshAttempt = metav1.NewTime(now.Add(r.Config.DomainRegistration.RetryBackoff.Duration))
			return st.Registration.NextRefreshAttempt.Time
		}
	} else {
		if resp == nil {
			logger.Info("rdap returned nil response; falling back to whois", "apex", apex)
			useWHOIS = true
		} else if resp.Object == nil {
			logger.Info("rdap response has nil object; falling back to whois", "apex", apex)
			useWHOIS = true
		}
	}

	var reg networkingv1alpha.Registration
	var rdapDomain *rdap.Domain
	if !useWHOIS {
		rdapDomain, _ = resp.Object.(*rdap.Domain)
		if rdapDomain == nil {
			logger.Info("rdap response object is not a domain or is nil; retrying later", "apex", apex)
			st.Registration.NextRefreshAttempt = metav1.NewTime(now.Add(r.Config.DomainRegistration.RetryBackoff.Duration))
			return st.Registration.NextRefreshAttempt.Time
		}
		reg = r.mapRDAPDomainToRegistration(*rdapDomain)
		reg.Source = "rdap"
	} else {
		// WHOIS path
		wreg, werr := r.fetchRegistrationWhois(ctxLookup, apex)
		if werr != nil {
			logger.Error(werr, "whois lookup failed")
			st.Registration.NextRefreshAttempt = metav1.NewTime(now.Add(r.Config.DomainRegistration.RetryBackoff.Duration))
			return st.Registration.NextRefreshAttempt.Time
		}
		reg = *wreg
		reg.Source = "whois"
	}

	// Registry from bootstrap (authoritative)
	if !useWHOIS && resp.BootstrapAnswer != nil && len(resp.BootstrapAnswer.URLs) > 0 {
		var base *url.URL
		for _, u := range resp.BootstrapAnswer.URLs {
			if u != nil && strings.EqualFold(u.Scheme, "https") {
				base = u
				break
			}
		}
		if base == nil {
			base = resp.BootstrapAnswer.URLs[0]
		}
		if base != nil && base.Host != "" {
			hostBase := base.Scheme + "://" + base.Host
			reg.Registry = &networkingv1alpha.RegistryInfo{Name: base.Host, URL: hostBase}
		}
	}

	// populate top-level Nameservers based on apex/non-apex
	st.Nameservers = st.Nameservers[:0]

	if st.Apex {
		if !useWHOIS && rdapDomain != nil {
			// Apex: use RDAP-reported NS from the apex object
			for _, ns := range rdapDomain.Nameservers {
				st.Nameservers = append(st.Nameservers, networkingv1alpha.Nameserver{Hostname: ns.LDHName})
			}
		} else {
			// WHOIS mode (or no RDAP object): resolve NS via DNS
			if nsHosts, _ := r.delegatedZoneNS(ctxLookup, apex, apex); len(nsHosts) > 0 {
				for _, h := range nsHosts {
					st.Nameservers = append(st.Nameservers, networkingv1alpha.Nameserver{Hostname: h})
				}
			}
		}
	} else {
		// Non-apex: find delegated NS for the exact name; if none, fall back to apex NS
		if nsHosts, delegated := r.delegatedZoneNS(ctxLookup, d.Spec.DomainName, apex); delegated && len(nsHosts) > 0 {
			logger.Info("subdomain delegation detected; using delegated nameservers",
				"zoneApex", d.Spec.DomainName, "nsCount", len(nsHosts))
			for _, h := range nsHosts {
				st.Nameservers = append(st.Nameservers, networkingv1alpha.Nameserver{Hostname: h})
			}
		} else {
			if !useWHOIS && rdapDomain != nil {
				// fallback to apex NS from RDAP
				for _, ns := range rdapDomain.Nameservers {
					st.Nameservers = append(st.Nameservers, networkingv1alpha.Nameserver{Hostname: ns.LDHName})
				}
			} else {
				// WHOIS mode: fallback to apex NS via DNS
				if nsHosts, _ := r.delegatedZoneNS(ctxLookup, apex, apex); len(nsHosts) > 0 {
					for _, h := range nsHosts {
						st.Nameservers = append(st.Nameservers, networkingv1alpha.Nameserver{Hostname: h})
					}
				}
			}
		}
	}

	// Enrich top-level Nameservers with per-IP registrant
	for i := range st.Nameservers {
		host := st.Nameservers[i].Hostname
		ipAddrs, err := r.lookupIP(ctxLookup, host)
		if err != nil {
			logger.Error(err, "error looking up IP addresses for nameserver", "host", host)
			continue
		}
		st.Nameservers[i].IPs = nil
		for _, ipAddr := range ipAddrs {
			ip := ipAddr.IP.String()
			nsIP := networkingv1alpha.NameserverIP{Address: ip}
			if who := r.lookupRegistrantNameForIP(ctxLookup, ip); who != "" {
				nsIP.RegistrantName = who
			}
			st.Nameservers[i].IPs = append(st.Nameservers[i].IPs, nsIP)
		}
	}

	st.Registration = &reg

	// Schedule next refresh (with jitter)
	interval := r.Config.DomainRegistration.RefreshInterval.Duration
	next := now.Add(wait.Jitter(interval, r.Config.DomainRegistration.JitterMaxFactor))
	st.Registration.NextRefreshAttempt = metav1.NewTime(next)

	return next
}

// mapRDAPDomainToRegistration maps a raw RDAP domain into our Registration model.
func (r *DomainReconciler) mapRDAPDomainToRegistration(d rdap.Domain) networkingv1alpha.Registration {
	reg := networkingv1alpha.Registration{}

	reg.Domain = d.LDHName
	reg.Handle = d.Handle

	// IDs & statuses
	reg.RegistryDomainID = pickRegistryDomainID(d.PublicIDs)
	copyStatuses(&reg, d.Status)

	// Lifecycle timestamps
	applyLifecycleFromEvents(&reg, d.Events)

	// DNSSEC
	if d.SecureDNS != nil {
		reg.DNSSEC = buildDNSSEC(d.SecureDNS)
	}

	// Contacts & abuse
	if contacts, abuse := buildContacts(d.Entities); contacts != nil || abuse != nil {
		reg.Contacts = contacts
		reg.Abuse = abuse
	}

	// Registrar
	if ri := buildRegistrarInfo(d.Entities); ri != nil {
		reg.Registrar = ri
	}

	return reg
}

func pickRegistryDomainID(publicIDs []rdap.PublicID) string {
	for _, pid := range publicIDs {
		if pid.Identifier == "" {
			continue
		}
		pt := strings.ToLower(pid.Type)
		// Skip registrar/iana IDs
		if strings.Contains(pt, "iana") || strings.Contains(pt, "registrar") {
			continue
		}
		// Prefer domain/roid-ish types
		if strings.Contains(pt, "roid") || strings.Contains(pt, "domain") {
			return pid.Identifier
		}
	}
	return ""
}

func copyStatuses(reg *networkingv1alpha.Registration, st []string) {
	if len(st) > 0 {
		reg.Statuses = append(reg.Statuses, st...)
	}
}

func applyLifecycleFromEvents(reg *networkingv1alpha.Registration, events []rdap.Event) {
	for _, ev := range events {
		date := parseRFC3339Ptr(ev.Date)
		if date == nil {
			continue
		}
		switch strings.ToLower(ev.Action) {
		case "registration":
			reg.CreatedAt = date
		case "last changed":
			reg.UpdatedAt = date
		case "expiration":
			reg.ExpiresAt = date
		}
	}
}

func parseRFC3339Ptr(s string) *metav1.Time {
	if s == "" {
		return nil
	}
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	t := metav1.NewTime(tt)
	return &t
}

// whoisFetchAtHost performs a WHOIS query at a specific host for a query label and returns the body as string
func whoisFetchAtHost(ctx context.Context, query, host string) (string, error) {
	req, err := whois.NewRequest(query)
	if err != nil {
		return "", err
	}
	req.Host = host
	resp, err := whois.DefaultClient.FetchContext(ctx, req)
	if err != nil {
		return "", err
	}
	return string(resp.Body), nil
}

// findWhoisValue scans WHOIS body for a key (case-insensitive) of the form
// "Key: value" and returns the trimmed value. It tolerates variable spacing/tabs
// around the colon and ignores inline content after the first colon.
// Note: keys are checked in order; the first key that yields a value is returned.
func findWhoisValue(body string, keys []string) string {
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
	}
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// Split on first ':' only
		idx := strings.IndexByte(l, ':')
		if idx <= 0 {
			continue
		}
		left := strings.ToLower(strings.TrimSpace(l[:idx]))
		right := strings.TrimSpace(l[idx+1:])
		if _, ok := keySet[left]; ok {
			return right
		}
	}
	return ""
}

// parseTimeFlex tries several common WHOIS time formats
func parseTimeFlex(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05-0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func buildDNSSEC(sd *rdap.SecureDNS) *networkingv1alpha.DNSSECInfo {
	out := &networkingv1alpha.DNSSECInfo{}
	if sd.DelegationSigned != nil {
		enabled := *sd.DelegationSigned
		out.Enabled = &enabled
	}
	for _, ds := range sd.DS {
		var keyTag uint16
		if ds.KeyTag != nil {
			keyTag = uint16(*ds.KeyTag)
		}
		var alg, dt uint8
		if ds.Algorithm != nil {
			alg = *ds.Algorithm
		}
		if ds.DigestType != nil {
			dt = *ds.DigestType
		}
		out.DS = append(out.DS, networkingv1alpha.DSRecord{
			KeyTag:     keyTag,
			Algorithm:  alg,
			DigestType: dt,
			Digest:     ds.Digest,
		})
	}
	return out
}

func buildContacts(entities []rdap.Entity) (*networkingv1alpha.ContactSet, *networkingv1alpha.AbuseContact) {
	cs := &networkingv1alpha.ContactSet{}
	var abuse *networkingv1alpha.AbuseContact

	for _, e := range entities {
		name, email, phone := extractVCard(e.VCard)
		for _, role := range e.Roles {
			switch strings.ToLower(role) {
			case "registrant":
				cs.Registrant = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "administrative":
				cs.Admin = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "technical":
				cs.Tech = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "abuse":
				abuse = &networkingv1alpha.AbuseContact{Email: email, Phone: phone}
			}
		}
	}

	// normalize nil when empty
	if cs.Registrant == nil && cs.Admin == nil && cs.Tech == nil {
		cs = nil
	}
	return cs, abuse
}

func buildRegistrarInfo(entities []rdap.Entity) *networkingv1alpha.RegistrarInfo {
	for _, e := range entities {
		if !hasRole(e.Roles, "registrar") {
			continue
		}
		name, _, _ := extractVCard(e.VCard)
		ri := &networkingv1alpha.RegistrarInfo{Name: name}

		for _, pid := range e.PublicIDs {
			if strings.Contains(strings.ToLower(pid.Type), "iana") && pid.Identifier != "" {
				ri.IANAID = pid.Identifier
				break
			}
		}
		for _, l := range e.Links {
			if l.Href != "" {
				ri.URL = l.Href
				break
			}
		}
		return ri
	}
	return nil
}

func (r *DomainReconciler) lookupRegistrantNameForIP(ctx context.Context, ip string) string {
	ipNet, err := r.rdapQueryIP(ctx, ip)
	if err != nil || ipNet == nil {
		return ""
	}
	for _, e := range ipNet.Entities {
		if hasRole(e.Roles, "registrant") {
			name, _, _ := extractVCard(e.VCard)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

func extractVCard(vc *rdap.VCard) (name, email, phone string) {
	if vc == nil {
		return "", "", ""
	}
	if n := vc.Name(); n != "" {
		name = n
	}
	if e := vc.Email(); e != "" {
		email = e
	}
	if t := vc.Tel(); t != "" {
		phone = t
	}
	if p := vc.GetFirst("org"); p != nil {
		vals := p.Values()
		if len(vals) > 0 && vals[0] != "" {
			name = vals[0]
		}
	}
	return
}

func registeredApex(name string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(name), ".")
	return publicsuffix.EffectiveTLDPlusOne(n)
}

// delegatedZoneNS finds the deepest node at/under apex that has NS records.
// Returns (nsHostnames, delegated).
func (r *DomainReconciler) delegatedZoneNS(ctx context.Context, fqdn, apex string) ([]string, bool) {
	trimDot := func(s string) string { return strings.TrimSuffix(s, ".") }
	addDot := func(s string) string {
		if s == "" || strings.HasSuffix(s, ".") {
			return s
		}
		return s + "."
	}

	cur := strings.ToLower(trimDot(fqdn))
	apex = strings.ToLower(trimDot(apex))

	for {
		// Presence of NS at this node means a zone cut (authoritative for this subtree)
		recs, err := r.lookupNS(ctx, addDot(cur))
		if err == nil && len(recs) > 0 {
			hosts := make([]string, 0, len(recs))
			for _, rr := range recs {
				if rr != nil && rr.Host != "" {
					hosts = append(hosts, trimDot(rr.Host))
				}
			}
			return hosts, cur != apex
		}
		if cur == apex {
			break
		}
		if i := strings.IndexByte(cur, '.'); i > 0 {
			cur = cur[i+1:]
		} else {
			break
		}
	}
	return nil, false
}

// fetchRegistrationWhois attempts WHOIS-based mapping for a domain apex
func (r *DomainReconciler) fetchRegistrationWhois(ctx context.Context, apex string) (*networkingv1alpha.Registration, error) {
	// Bootstrap via IANA to find the authoritative WHOIS server
	tld, _ := publicsuffix.PublicSuffix(apex)
	// query IANA WHOIS for the TLD
	bodyIANA, _ := r.whoisFetch(ctx, tld, r.Config.DomainRegistration.WhoisBootstrapHost)
	referHost := strings.TrimSpace(findWhoisValue(bodyIANA, []string{"refer", "whois"}))

	// Try registry WHOIS host from IANA, with fallbacks
	var registryBody string
	var err error
	tryHosts := []string{}
	if referHost != "" {
		tryHosts = append(tryHosts, referHost)
	}
	tryHosts = append(tryHosts, "whois.registry."+strings.ToLower(tld))
	tryHosts = append(tryHosts, "whois.nic."+strings.ToLower(tld))
	for _, h := range tryHosts {
		if b, e := r.whoisFetch(ctx, apex, h); e == nil && strings.TrimSpace(b) != "" {
			registryBody = b
			break
		} else {
			err = e
		}
	}
	if registryBody == "" {
		if err == nil {
			err = fmt.Errorf("no WHOIS registry body for %s", apex)
		}
		return nil, err
	}

	// Follow registrar WHOIS referral once if present
	bodyToParse := registryBody
	if registrarHost := strings.TrimSpace(findWhoisValue(registryBody, []string{"Registrar WHOIS Server"})); registrarHost != "" {
		if b, e := r.whoisFetch(ctx, apex, registrarHost); e == nil && strings.TrimSpace(b) != "" {
			bodyToParse = b
		}
	}

	body := bodyToParse
	reg := &networkingv1alpha.Registration{Domain: apex}
	// Registry Domain ID
	if v := findWhoisValue(body, []string{"Registry Domain ID", "Domain ID", "roid"}); v != "" {
		reg.RegistryDomainID = strings.TrimSpace(v)
	}
	// Registrar name and IANA ID
	if v := findWhoisValue(body, []string{"Registrar", "Sponsoring Registrar"}); v != "" {
		name := strings.TrimSpace(v)
		if name != "" {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{Name: name}
		}
	}
	if v := findWhoisValue(body, []string{"Registrar IANA ID"}); v != "" {
		if reg.Registrar == nil {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{}
		}
		reg.Registrar.IANAID = strings.TrimSpace(v)
	}
	if v := findWhoisValue(body, []string{"Registrar URL"}); v != "" {
		if reg.Registrar == nil {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{}
		}
		reg.Registrar.URL = strings.TrimSpace(v)
	}
	// Dates
	if v := findWhoisValue(body, []string{"Creation Date", "Created On"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.CreatedAt = &mt
		}
	}
	if v := findWhoisValue(body, []string{"Updated Date", "Last Updated On"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.UpdatedAt = &mt
		}
	}
	if v := findWhoisValue(body, []string{"Registry Expiry Date", "Expiration Date", "Expiry Date"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.ExpiresAt = &mt
		}
	}
	// Abuse (registrar)
	if email := strings.TrimSpace(findWhoisValue(body, []string{"Registrar Abuse Contact Email"})); email != "" {
		if reg.Abuse == nil {
			reg.Abuse = &networkingv1alpha.AbuseContact{}
		}
		reg.Abuse.Email = email
	}
	if phone := strings.TrimSpace(findWhoisValue(body, []string{"Registrar Abuse Contact Phone"})); phone != "" {
		if reg.Abuse == nil {
			reg.Abuse = &networkingv1alpha.AbuseContact{}
		}
		reg.Abuse.Phone = phone
	}
	// Statuses
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		ll := strings.ToLower(l)
		if strings.HasPrefix(ll, strings.ToLower("Domain Status:")) {
			val := strings.TrimSpace(strings.TrimPrefix(l, "Domain Status:"))
			if val != "" {
				reg.Statuses = append(reg.Statuses, strings.Fields(val)[0])
			}
		}
	}
	// DNSSEC
	if v := findWhoisValue(body, []string{"DNSSEC"}); v != "" {
		vv := strings.ToLower(strings.TrimSpace(v))
		enabled := vv != "unsigned" && vv != "no"
		reg.DNSSEC = &networkingv1alpha.DNSSECInfo{Enabled: &enabled}
	}
	// Contacts (Registrant/Admin/Tech) — organization/email/phone when available and not redacted
	redact := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		up := strings.ToUpper(s)
		if up == "REDACTED" || strings.Contains(up, "REDACTED") {
			return ""
		}
		return s
	}
	setContact := func(orgKey, emailKey, phoneKey string) *networkingv1alpha.Contact {
		org := redact(findWhoisValue(body, []string{orgKey}))
		email := redact(findWhoisValue(body, []string{emailKey}))
		phone := redact(findWhoisValue(body, []string{phoneKey}))
		if org == "" && email == "" && phone == "" {
			return nil
		}
		return &networkingv1alpha.Contact{Organization: org, Email: email, Phone: phone}
	}
	registrant := setContact("Registrant Organization", "Registrant Email", "Registrant Phone")
	admin := setContact("Admin Organization", "Admin Email", "Admin Phone")
	tech := setContact("Tech Organization", "Tech Email", "Tech Phone")
	if registrant != nil || admin != nil || tech != nil {
		reg.Contacts = &networkingv1alpha.ContactSet{Registrant: registrant, Admin: admin, Tech: tech}
	}
	return reg, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	r.timeNow = time.Now
	r.httpGet = defaultHTTPGet
	r.lookupTXT = net.DefaultResolver.LookupTXT

	r.rdapClient = &rdap.Client{
		HTTP:      &http.Client{Timeout: r.Config.DomainRegistration.LookupTimeout.Duration},
		Bootstrap: &bootstrap.Client{}, // enable IANA dns.json auto-discovery
	}

	r.lookupNS = net.DefaultResolver.LookupNS
	r.lookupIP = net.DefaultResolver.LookupIPAddr
	r.rdapDo = func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) {
		return r.rdapClient.Do(req.WithContext(ctx))
	}
	r.rdapQueryIP = func(ctx context.Context, ip string) (*rdap.IPNetwork, error) {
		req := &rdap.Request{
			Type:    rdap.IPRequest,
			Query:   ip,
			Timeout: r.Config.DomainRegistration.LookupTimeout.Duration,
		}

		r.whoisFetch = whoisFetchAtHost
		resp, err := r.rdapClient.Do(req.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		ipNet, _ := resp.Object.(*rdap.IPNetwork)
		return ipNet, nil
	}

	return mcbuilder.ControllerManagedBy(mgr).
		// Watch all Domains so registration continues after verification
		For(&networkingv1alpha.Domain{}).
		WithOptions(controller.TypedOptions[mcreconcile.Request]{
			MaxConcurrentReconciles: r.Config.DomainVerification.MaxConcurrentVerifications,
		}).
		Named("domain").
		Complete(r)
}
