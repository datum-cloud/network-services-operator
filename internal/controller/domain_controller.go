// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/likexian/whois"
	whoisparser "github.com/likexian/whois-parser"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	datumapisv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// DomainReconciler reconciles a Domain object
type DomainReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=datumapis.com,resources=domains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=datumapis.com,resources=domains/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DomainReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	// Get the cluster client
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the Domain instance
	domain := &datumapisv1alpha.Domain{}
	if err := cl.GetClient().Get(ctx, req.NamespacedName, domain); err != nil {
		// Handle the case where the Domain resource is not found
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling Domain", "domain", domain.Name, "domainName", domain.Spec.DomainName)

	// Check if domain ownership is verified
	if !r.isDomainVerified(domain) {
		// If not verified, generate and set verification TXT record
		if err := r.generateVerificationRecord(domain); err != nil {
			logger.Error(err, "Failed to generate verification record")
			return ctrl.Result{}, err
		}

		// Check DNS for verification record
		if r.checkDNSVerification(domain) {
			// Update domain status with verification results
			r.updateDomainVerificationStatus(domain, true)
		} else {
			// Verification failed, requeue after a delay
			logger.Info("Domain verification pending", "domain", domain.Spec.DomainName)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// If verified, fetch WHOIS data and update status
	if r.isDomainVerified(domain) && domain.Status.Registrar == nil {
		if err := r.fetchWHOISData(domain); err != nil {
			logger.Error(err, "Failed to fetch WHOIS data")
			return ctrl.Result{}, err
		}
	}

	// Update the domain status
	if err := cl.GetClient().Status().Update(ctx, domain); err != nil {
		logger.Error(err, "Failed to update domain status")
		return ctrl.Result{}, err
	}

	// Update Ready condition
	r.updateReadyCondition(domain)

	return ctrl.Result{}, nil
}

// isDomainVerified checks if the domain has been verified
func (r *DomainReconciler) isDomainVerified(domain *datumapisv1alpha.Domain) bool {
	// If we have registrar data, which indicates successful verification and WHOIS lookup
	if domain.Status.Registrar != nil {
		return true
	}
	// Check if verification records exist and have been verified
	if domain.Status.Verification == nil || len(domain.Status.Verification.RequiredDNSRecords) == 0 {
		return false
	}
	// If we have verification records but no registrar data, we need to verify DNS
	return r.checkDNSVerification(domain)
}

// generateVerificationRecord generates a verification TXT record for domain ownership
func (r *DomainReconciler) generateVerificationRecord(domain *datumapisv1alpha.Domain) error {
	// Generate a random verification token
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("failed to generate verification token: %w", err)
	}

	verificationToken := hex.EncodeToString(token)
	verificationRecord := fmt.Sprintf("datum-verification=%s", verificationToken)

	// Create verification status if it doesn't exist
	if domain.Status.Verification == nil {
		domain.Status.Verification = &datumapisv1alpha.DomainVerificationStatus{}
	}

	// Set the required DNS record
	domain.Status.Verification.RequiredDNSRecords = []datumapisv1alpha.DNSVerificationExpectedRecord{
		{
			Name:    fmt.Sprintf("_datum-verification.%s", domain.Spec.DomainName),
			Type:    "TXT",
			Content: verificationRecord,
		},
	}

	return nil
}

// checkDNSVerification checks if the verification TXT record exists in DNS
func (r *DomainReconciler) checkDNSVerification(domain *datumapisv1alpha.Domain) bool {
	if domain.Status.Verification == nil || len(domain.Status.Verification.RequiredDNSRecords) == 0 {
		return false
	}

	record := domain.Status.Verification.RequiredDNSRecords[0]

	// Perform DNS lookup for the TXT record with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use a custom resolver to avoid blocking
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 5 * time.Second,
			}
			return d.DialContext(ctx, network, address)
		},
	}

	txtRecords, err := resolver.LookupTXT(ctx, record.Name)
	if err != nil {
		return false
	}

	// Check if our verification record exists
	for _, txt := range txtRecords {
		if strings.Contains(txt, record.Content) {
			return true
		}
	}

	return false
}

// updateDomainVerificationStatus updates the domain verification status
func (r *DomainReconciler) updateDomainVerificationStatus(domain *datumapisv1alpha.Domain, verified bool) {
	if verified && domain.Status.Verification != nil {
		// Mark verification as successful by ensuring we have the required records
		// The actual verification happens in checkDNSVerification
		return
	}

	// If verification failed, we might want to clear the verification records
	// or mark them as failed. For now, we keep them for retry.
}

// fetchWHOISData fetches WHOIS information for the domain
func (r *DomainReconciler) fetchWHOISData(domain *datumapisv1alpha.Domain) error {
	// Perform WHOIS lookup with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use a goroutine to perform the WHOIS lookup with timeout
	type result struct {
		data string
		err  error
	}

	resultChan := make(chan result, 1)

	go func() {
		data, err := whois.Whois(domain.Spec.DomainName)
		resultChan <- result{data: data, err: err}
	}()

	select {
	case res := <-resultChan:
		if res.err != nil {
			return fmt.Errorf("failed to perform WHOIS lookup: %w", res.err)
		}

		// Parse WHOIS data using the proper parser
		registrarInfo := r.parseWHOISData(res.data)
		domain.Status.Registrar = registrarInfo

	case <-ctx.Done():
		return fmt.Errorf("WHOIS lookup timed out after 30 seconds")
	}

	return nil
}

// parseWHOISData parses WHOIS response using the whois-parser library
func (r *DomainReconciler) parseWHOISData(whoisData string) *datumapisv1alpha.DomainRegistrarStatus {
	registrar := &datumapisv1alpha.DomainRegistrarStatus{}

	// Parse the WHOIS data using the proper parser
	result, err := whoisparser.Parse(whoisData)
	if err != nil {
		// If parsing fails, fall back to basic extraction
		return r.parseWHOISDataFallback(whoisData)
	}

	// Extract registrar information from the parsed result
	if result.Registrar != nil {
		registrar.IANAName = result.Registrar.Name
		if result.Registrar.ID != "" {
			registrar.IANAID = result.Registrar.ID
		}
	}

	// Extract domain information
	if result.Domain != nil {
		registrar.CreatedDate = result.Domain.CreatedDate
		registrar.ModifiedDate = result.Domain.UpdatedDate
		registrar.ExpirationDate = result.Domain.ExpirationDate
		registrar.Nameservers = result.Domain.NameServers

		// Check DNSSEC status
		registrar.DNSSEC.Signed = result.Domain.DNSSec

		// Extract status codes
		for _, status := range result.Domain.Status {
			if strings.Contains(strings.ToLower(status), "client") {
				registrar.ClientStatusCodes = append(registrar.ClientStatusCodes, status)
			} else if strings.Contains(strings.ToLower(status), "server") {
				registrar.ServerStatusCodes = append(registrar.ServerStatusCodes, status)
			}
		}
	}

	// Set defaults if no data found
	if registrar.IANAName == "" {
		registrar.IANAName = "Unknown Registrar"
	}
	if registrar.IANAID == "" {
		registrar.IANAID = "0"
	}

	return registrar
}

// parseWHOISDataFallback provides a fallback parsing method if the main parser fails
func (r *DomainReconciler) parseWHOISDataFallback(whoisData string) *datumapisv1alpha.DomainRegistrarStatus {
	registrar := &datumapisv1alpha.DomainRegistrarStatus{}

	// Common WHOIS field patterns for fallback
	patterns := map[string]*regexp.Regexp{
		"registrar":       regexp.MustCompile(`(?i)registrar:\s*(.+)`),
		"iana_id":         regexp.MustCompile(`(?i)iana id:\s*(\d+)`),
		"created_date":    regexp.MustCompile(`(?i)(?:created|creation) date:\s*(.+)`),
		"updated_date":    regexp.MustCompile(`(?i)(?:updated|modified) date:\s*(.+)`),
		"expiration_date": regexp.MustCompile(`(?i)(?:expiration|expires|expiry) date:\s*(.+)`),
		"nameservers":     regexp.MustCompile(`(?i)name server:\s*(.+)`),
		"dnssec":          regexp.MustCompile(`(?i)dnssec:\s*(.+)`),
		"status":          regexp.MustCompile(`(?i)status:\s*(.+)`),
	}

	// Extract registrar name
	if match := patterns["registrar"].FindStringSubmatch(whoisData); len(match) > 1 {
		registrar.IANAName = strings.TrimSpace(match[1])
	}

	// Extract IANA ID
	if match := patterns["iana_id"].FindStringSubmatch(whoisData); len(match) > 1 {
		registrar.IANAID = strings.TrimSpace(match[1])
	}

	// Extract dates
	if match := patterns["created_date"].FindStringSubmatch(whoisData); len(match) > 1 {
		registrar.CreatedDate = strings.TrimSpace(match[1])
	}

	if match := patterns["updated_date"].FindStringSubmatch(whoisData); len(match) > 1 {
		registrar.ModifiedDate = strings.TrimSpace(match[1])
	}

	if match := patterns["expiration_date"].FindStringSubmatch(whoisData); len(match) > 1 {
		registrar.ExpirationDate = strings.TrimSpace(match[1])
	}

	// Extract nameservers
	nameserverMatches := patterns["nameservers"].FindAllStringSubmatch(whoisData, -1)
	for _, match := range nameserverMatches {
		if len(match) > 1 {
			ns := strings.TrimSpace(match[1])
			if ns != "" {
				registrar.Nameservers = append(registrar.Nameservers, ns)
			}
		}
	}

	// Extract DNSSEC status
	if match := patterns["dnssec"].FindStringSubmatch(whoisData); len(match) > 1 {
		dnssecStatus := strings.ToLower(strings.TrimSpace(match[1]))
		registrar.DNSSEC.Signed = strings.Contains(dnssecStatus, "signed") ||
			strings.Contains(dnssecStatus, "yes") ||
			strings.Contains(dnssecStatus, "enabled")
	}

	// Extract status codes
	statusMatches := patterns["status"].FindAllStringSubmatch(whoisData, -1)
	for _, match := range statusMatches {
		if len(match) > 1 {
			status := strings.TrimSpace(match[1])
			if status != "" {
				// Categorize status codes
				if strings.Contains(strings.ToLower(status), "client") {
					registrar.ClientStatusCodes = append(registrar.ClientStatusCodes, status)
				} else if strings.Contains(strings.ToLower(status), "server") {
					registrar.ServerStatusCodes = append(registrar.ServerStatusCodes, status)
				}
			}
		}
	}

	// Set defaults if no data found
	if registrar.IANAName == "" {
		registrar.IANAName = "Unknown Registrar"
	}
	if registrar.IANAID == "" {
		registrar.IANAID = "0"
	}

	return registrar
}

// updateReadyCondition updates the Ready condition for the domain
func (r *DomainReconciler) updateReadyCondition(domain *datumapisv1alpha.Domain) {
	// Check if domain is ready
	ready := r.isDomainVerified(domain) && domain.Status.Registrar != nil

	// Find existing Ready condition
	var readyCondition *metav1.Condition
	for i := range domain.Status.Conditions {
		if domain.Status.Conditions[i].Type == "Ready" {
			readyCondition = &domain.Status.Conditions[i]
			break
		}
	}

	// Create new condition if it doesn't exist
	if readyCondition == nil {
		readyCondition = &metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
		}
		domain.Status.Conditions = append(domain.Status.Conditions, *readyCondition)
	}

	// Update condition status
	status := metav1.ConditionFalse
	reason := "DomainNotReady"
	message := "Domain verification pending"

	if ready {
		status = metav1.ConditionTrue
		reason = "DomainReady"
		message = "Domain verified and WHOIS data fetched"
	}

	// Only update if status changed
	if readyCondition.Status != status {
		readyCondition.Status = status
		readyCondition.Reason = reason
		readyCondition.Message = message
		readyCondition.LastTransitionTime = metav1.Now()
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&datumapisv1alpha.Domain{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("domain").
		Complete(r)
}
