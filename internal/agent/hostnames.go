// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// hostnameProgress builds each hostname's four-step ladder.
//
// Only three of the four steps are ever reported on the hostname itself. The
// API declares a per-hostname Verified condition but no controller writes one,
// so ownership is read from the Domain covering that hostname and the step is
// marked notReported when there is no Domain to read.
func hostnameProgress(
	proxy *networkingv1alpha.HTTPProxy,
	domains []networkingv1alpha.Domain,
) []HostnameProgress {
	var out []HostnameProgress

	if h := proxy.Status.CanonicalHostname; h != "" {
		out = append(out, generatedHostnameProgress(h))
	}

	for i := range proxy.Status.HostnameStatuses {
		hs := proxy.Status.HostnameStatuses[i]
		out = append(out, customHostnameProgress(hs, domains))
	}
	return out
}

// generatedHostnameProgress describes the hostname Datum assigned. It is not
// claimed, not verified and needs no record of the customer's: they point a
// CNAME at it. Its certificate is Datum's to hold.
func generatedHostnameProgress(hostname string) HostnameProgress {
	return HostnameProgress{
		Hostname:  hostname,
		Generated: true,
		Working:   true,
		Steps: []HostnameStep{
			{Step: StepClaimed, State: StepOK, Note: "Datum assigned this hostname, so nothing had to claim it."},
			{Step: StepOwnership, State: StepNotApplicable, Note: "This is Datum's own hostname; there is nothing for you to prove."},
			{
				Step:  StepDNSRecord,
				State: StepNotReported,
				Note: "Datum publishes no status for this hostname's DNS record, so its absence here " +
					"means nothing either way. Make a request against the hostname to see whether it resolves.",
			},
			{Step: StepCertificate, State: StepNotReported, Note: "Datum holds the certificate for its own hostname and publishes no status for it."},
		},
	}
}

func customHostnameProgress(
	hs networkingv1alpha.HostnameStatus,
	domains []networkingv1alpha.Domain,
) HostnameProgress {
	p := HostnameProgress{Hostname: hs.Hostname}

	p.Steps = append(p.Steps, stepFrom(
		StepClaimed, hs.Conditions, networkingv1alpha.HostnameConditionAvailable,
		"Nothing reports whether this load balancer holds this hostname yet."))

	domain := domainFor(hs.Hostname, domains)
	p.Steps = append(p.Steps, ownershipStep(domain))
	if domain != nil {
		p.Domain = domainView(domain)
	}

	p.Steps = append(p.Steps, stepFrom(
		StepDNSRecord, hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		"Nothing reports whether a DNS record for this hostname has been written."))

	p.Steps = append(p.Steps, stepFrom(
		StepCertificate, hs.Conditions, networkingv1alpha.HostnameConditionCertificateReady,
		"Nothing reports whether this hostname has a certificate."))

	p.Working = true
	for _, s := range p.Steps {
		if s.State == StepFailed || s.State == StepPending {
			p.Working = false
			break
		}
	}
	return p
}

// stepFrom reads one condition into a step. An absent condition is
// notReported — neither pass nor fail — and the two reasons the API sets True
// to mean "does not apply" become notApplicable rather than success, so nobody
// is told to wait for something that is never coming.
func stepFrom(step string, conditions []metav1.Condition, conditionType, absentNote string) HostnameStep {
	c := conditionOf(conditions, conditionType)
	if c == nil {
		return HostnameStep{Step: step, State: StepNotReported, Note: absentNote}
	}

	out := HostnameStep{Step: step, Reason: c.Reason, Message: c.Message}

	info, known := ExplainReason(conditionType, c.Reason)
	switch {
	case known && info.Actionability == ActionabilityInformational && notApplicableReasons[c.Reason]:
		out.State = StepNotApplicable
		out.Note = info.Explanation
	case !Failing(conditionType, c.Status):
		out.State = StepOK
	case known && info.Actionability == ActionabilityTransient:
		out.State = StepPending
	default:
		out.State = StepFailed
	}
	return out
}

// notApplicableReasons are set True to report that a step does not apply. They
// are the customer's own DNS arrangement, not a fault and not something in
// flight.
var notApplicableReasons = map[string]bool{
	networkingv1alpha.DNSRecordReasonZoneNotFound:  true,
	networkingv1alpha.DNSRecordReasonNotApplicable: true,
}

// ownershipStep reads domain ownership from the Domain, because the hostname's
// own Verified condition is declared but never written.
func ownershipStep(domain *networkingv1alpha.Domain) HostnameStep {
	if domain == nil {
		return HostnameStep{
			Step:  StepOwnership,
			State: StepNotReported,
			Note: "No domain covering this hostname was found, so there is nothing recording whether " +
				"you have proven you own it.",
		}
	}

	c := conditionOf(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified)
	if c == nil {
		return HostnameStep{
			Step:  StepOwnership,
			State: StepNotReported,
			Note:  "The domain covering this hostname reports nothing about ownership yet.",
		}
	}

	out := HostnameStep{Step: StepOwnership, Reason: c.Reason, Message: c.Message}
	if c.Status == metav1.ConditionTrue {
		out.State = StepOK
		return out
	}
	out.State = StepFailed
	if info, ok := ExplainReason(networkingv1alpha.DomainConditionVerified, c.Reason); ok {
		out.Note = info.Explanation
	}
	return out
}

// domainFor returns the Domain covering a hostname: an exact match, or the
// longest name the hostname is a subdomain of.
//
// The rule matches hostnameCoveredByDomain in internal/webhook/v1alpha, which
// is the authority on it. Longest-match is this package's own choice: with both
// example.com and app.example.com present, the more specific one is the one
// carrying the answer.
func domainFor(hostname string, domains []networkingv1alpha.Domain) *networkingv1alpha.Domain {
	h := normaliseHostname(hostname)
	var best *networkingv1alpha.Domain
	for i := range domains {
		d := normaliseHostname(domains[i].Spec.DomainName)
		if d == "" || h == "" {
			continue
		}
		if h != d && !strings.HasSuffix(h, "."+d) {
			continue
		}
		if best == nil || len(d) > len(normaliseHostname(best.Spec.DomainName)) {
			best = &domains[i]
		}
	}
	return best
}

func normaliseHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

func domainView(d *networkingv1alpha.Domain) *DomainView {
	v := &DomainView{Name: d.Spec.DomainName, Apex: d.Status.Apex}

	if c := conditionOf(d.Status.Conditions, networkingv1alpha.DomainConditionVerified); c != nil {
		v.Verified = c.Status == metav1.ConditionTrue
	}
	if ver := d.Status.Verification; ver != nil && ver.DNSRecord.Name != "" {
		v.VerificationRecord = &DNSRecordView{
			Name:    ver.DNSRecord.Name,
			Type:    ver.DNSRecord.Type,
			Content: ver.DNSRecord.Content,
		}
	}
	for _, ns := range d.Status.Nameservers {
		if ns.Hostname != "" {
			v.Nameservers = append(v.Nameservers, ns.Hostname)
		}
	}
	return v
}

// hostnameCauses turns the failing steps into causes. This is the fan-out the
// aggregate conditions make necessary: they say "one or more hostnames" and
// name none, so the answer has to be found here or not at all.
func hostnameCauses(proxy *networkingv1alpha.HTTPProxy, progress []HostnameProgress, now time.Time) []Cause {
	var out []Cause

	byHostname := map[string]*networkingv1alpha.HostnameStatus{}
	for i := range proxy.Status.HostnameStatuses {
		byHostname[proxy.Status.HostnameStatuses[i].Hostname] = &proxy.Status.HostnameStatuses[i]
	}

	for _, p := range progress {
		if p.Generated {
			continue
		}
		hs := byHostname[p.Hostname]
		if hs == nil {
			continue
		}
		for _, step := range p.Steps {
			if step.State != StepFailed && step.State != StepPending {
				continue
			}
			conditionType := conditionTypeForStep(step.Step)
			if conditionType == "" {
				continue
			}

			level, created, c := levelHostname, proxy.CreationTimestamp, conditionOf(hs.Conditions, conditionType)
			if step.Step == StepOwnership {
				// Ownership is the Domain's answer, so report it at the depth
				// it was actually found.
				level = levelDomain
				c = &metav1.Condition{Type: networkingv1alpha.DomainConditionVerified, Reason: step.Reason, Message: step.Message}
			}
			if c == nil {
				continue
			}
			out = append(out, newCause("hostname "+p.Hostname, p.Hostname, *c, created, level, now))
		}
	}
	return out
}

func conditionTypeForStep(step string) string {
	switch step {
	case StepClaimed:
		return networkingv1alpha.HostnameConditionAvailable
	case StepOwnership:
		return networkingv1alpha.DomainConditionVerified
	case StepDNSRecord:
		return networkingv1alpha.HostnameConditionDNSRecordProgrammed
	case StepCertificate:
		return networkingv1alpha.HostnameConditionCertificateReady
	default:
		return ""
	}
}
