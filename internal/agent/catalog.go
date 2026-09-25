// SPDX-License-Identifier: AGPL-3.0-only

// Package agent turns an Application Load Balancer's status into answers an
// assistant can give a customer: a lookup table over the condition reasons
// api/v1alpha declares (catalog.go), and a walk over the objects an ALB is made
// of (diagnose.go).
package agent

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// Actionability says who has to do something about a condition.
type Actionability string

const (
	// ActionabilityUser means the load balancer's configuration, a domain's DNS,
	// or a service behind it is wrong, and the customer can fix it.
	ActionabilityUser Actionability = "user"

	// ActionabilityPlatform means the cause is Datum's. No change the customer
	// makes will help, and suggesting one wastes their time.
	ActionabilityPlatform Actionability = "platform"

	// ActionabilityTransient means this is a normal in-flight state and the
	// right advice is to wait.
	ActionabilityTransient Actionability = "transient"

	// ActionabilityStalled means a transient reason has held longer than its
	// expected window. Treat it as suspect and escalate.
	//
	// Never written in the catalog: derived at read time from
	// LastTransitionTime, so it cannot go stale in storage.
	ActionabilityStalled Actionability = "stalled"

	// ActionabilityInformational means nothing is wrong and nothing is pending.
	// It exists because two of this API's reasons are set True to report that a
	// step does not apply, and calling those "transient" would tell a customer
	// to wait for something that is never going to happen.
	ActionabilityInformational Actionability = "informational"
)

// Scope says how much stops working. Root-cause selection sorts on this first:
// ranking by depth alone would surface a traffic-protection fault above a dead
// origin on a load balancer that is serving no traffic at all.
type Scope string

const (
	// ScopeAllTraffic means nothing this load balancer serves is reachable.
	ScopeAllTraffic Scope = "all-traffic"

	// ScopeOneHostname means one name does not reach it while the others do.
	ScopeOneHostname Scope = "one-hostname"

	// ScopeDegraded means it serves, but not from everything behind it.
	ScopeDegraded Scope = "degraded"

	// ScopeProtectionOnly means traffic flows but is not being inspected.
	ScopeProtectionOnly Scope = "protection-only"
)

// Skill names, published alongside this package and advertised by the
// capability document. The assistant loads one on demand.
const (
	SkillNotServing           = "alb-not-serving"
	SkillHostnameNotWorking   = "hostname-not-working"
	SkillDomainVerification   = "domain-verification"
	SkillDNSDelegation        = "dns-delegation"
	SkillCertificateNotIssued = "certificate-not-issued"
	SkillBackendNotReachable  = "backend-not-reachable"
	SkillEdgePropagation      = "edge-propagation"
	SkillProtectionTriage     = "traffic-protection-triage"
	SkillAccessLogTriage      = "access-log-triage"
	SkillCreate               = "alb-create"
)

// Expected windows for the transient reasons. A transient classification is a
// claim about duration, so every reason that says "wait" also says how long is
// reasonable, and the claim becomes falsifiable.
//
// Domain verification deliberately has no window. It is gated on a person
// creating a DNS record, so a window would be a claim about the customer's
// speed dressed up as a claim about Datum's.
const (
	// windowControlPlaneRead: a controller reads or evaluates. No DNS, no
	// certificate authority, no edge.
	windowControlPlaneRead = 5 * time.Minute

	// windowDNSRecord: Datum writes a record into a zone it runs. The write is
	// fast; observing it land is not.
	windowDNSRecord = 15 * time.Minute

	// windowCertificate: a certificate authority answers a challenge, which
	// needs the name to resolve publicly first.
	windowCertificate = 30 * time.Minute
)

// Remediation strings shared by several reasons.
const (
	remediationWait     = "Nothing to do here — this is normal and clears on its own."
	remediationEscalate = "Raise this with Datum. There is no change to your load balancer that will clear it."
)

// ReasonInfo explains one condition reason as it appears on one condition type.
//
// Keyed on the pair, not on the reason: this API reuses reason strings across
// conditions that mean different things. "Pending" alone appears on a load
// balancer's Accepted, on a hostname's CertificateReady and on a hostname's
// DNSRecordProgrammed, and the advice for the three is different. A lookup on
// the reason alone would answer all three with whichever was registered last —
// the wrong answer that reads like a right one, arriving through the very table
// built to prevent it.
type ReasonInfo struct {
	// Reason is the condition reason as the controllers write it.
	Reason string `json:"reason"`
	// ConditionType is the condition this entry explains the reason on.
	ConditionType string `json:"conditionType"`
	// Actionability says who must act.
	Actionability Actionability `json:"actionability"`
	// Scope says how much stops working. Empty when nothing is wrong.
	Scope Scope `json:"scope,omitempty"`
	// Explanation states, in the customer's language, what happened. They know
	// hostnames, domains, DNS records, certificates and origins; they have
	// never heard of a Gateway, a reconciler or an ancestor.
	Explanation string `json:"explanation"`
	// Remediation says what to do next. Empty for healthy reasons.
	Remediation string `json:"remediation,omitempty"`
	// Skill names the procedure covering this class of failure, if any.
	Skill string `json:"skill,omitempty"`
	// ExpectedDuration bounds how long a transient reason should plausibly
	// last; past it, callers report ActionabilityStalled. Zero means no window.
	ExpectedDuration time.Duration `json:"-"`
	// ExpectedWithin renders ExpectedDuration for a reader ("30m").
	ExpectedWithin string `json:"expectedWithin,omitempty"`
}

// ReasonCannotProgram is a reason this package derives rather than one the API
// declares. The controller reports a configuration it cannot assemble with the
// same Pending reason it uses for work it has not started, and only the message
// tells them apart. Giving the second case its own name is what lets it carry
// its own explanation, its own actionability and its own test.
const ReasonCannotProgram = "CannotProgram"

// cannotProgramPrefix is what the controller puts in front of the real cause.
const cannotProgramPrefix = "The HTTPProxy cannot be programmed:"

// Key identifies a catalog entry.
type Key struct {
	ConditionType string
	Reason        string
}

// catalog is the full reason vocabulary for an Application Load Balancer.
// Entries key off the api/v1alpha constants rather than string literals so the
// two cannot drift silently; TestCatalogCoversEveryInScopeAPIReason enforces
// completeness.
var catalog = []ReasonInfo{
	// ------------------------------------------- the load balancer itself
	{
		Reason:        networkingv1alpha.HTTPProxyReasonAccepted,
		ConditionType: networkingv1alpha.HTTPProxyConditionAccepted,
		Actionability: ActionabilityInformational,
		Explanation:   "This load balancer's settings are valid and Datum has taken them.",
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonProgrammed,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityInformational,
		Explanation: "This load balancer's settings have been published. That means the settings " +
			"reached Datum's edge, not that a request has been served through them — nothing " +
			"reports that, so treat a load balancer that has only just been published as " +
			"unconfirmed until a request succeeds.",
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonPending,
		ConditionType: networkingv1alpha.HTTPProxyConditionAccepted,
		Actionability: ActionabilityTransient,
		Scope:         ScopeAllTraffic,
		Explanation:   "Datum has not looked at this load balancer yet.",
		Remediation:   remediationWait,
		// No skill: nothing has run, so there is nothing to triage yet.
		ExpectedDuration: windowControlPlaneRead,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonPending,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityTransient,
		Scope:         ScopeAllTraffic,
		Explanation: "Datum has taken this load balancer's settings but has not published them yet. " +
			"Read the status message before telling anyone to wait: this same reason is also used " +
			"when Datum has decided it cannot publish the settings at all, and in that case waiting " +
			"never clears it.",
		Remediation:      remediationWait,
		ExpectedDuration: windowControlPlaneRead,
	},
	// The same reason with a different meaning, told apart by the message. The
	// controller keeps the reason at Pending on purpose — a failure to assemble
	// the configuration can as easily be a read that succeeds on retry as a
	// permanent mistake, and the reason should not claim to know which. The
	// message does know, so the walk reads it.
	//
	// This one matters more than most: the load balancer goes on serving the
	// configuration it published last, so the change looks accepted, is not
	// applied, and says so only in a message nobody reads.
	{
		Reason:        ReasonCannotProgram,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "Datum could not assemble this load balancer's settings into something it can " +
			"serve, so it is still serving whatever it published last. The change was accepted and " +
			"has not taken effect.",
		Remediation: "The status message names the conflict. Waiting will not clear it — the " +
			"settings have to change.",
		Skill: SkillNotServing,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonInvalid,
		ConditionType: networkingv1alpha.HTTPProxyConditionAccepted,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "Something in this load balancer's settings is not valid, so none of it has " +
			"been published. Whatever it was serving before is still being served; the change is " +
			"what has not taken.",
		Remediation: "The status message names the setting that was rejected. Correct it and save again.",
		Skill:       SkillNotServing,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonConflict,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityPlatform,
		Scope:         ScopeAllTraffic,
		Explanation:   "Datum hit a conflict publishing this load balancer and stopped rather than overwrite something.",
		Remediation:   remediationEscalate,
		Skill:         SkillNotServing,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonConnectorMetadataApplied,
		ConditionType: networkingv1alpha.HTTPProxyConditionConnectorMetadataProgrammed,
		Actionability: ActionabilityInformational,
		Explanation:   "The private connection this load balancer sends traffic over is set up.",
	},

	// ------------------------------------------------- routes and origins
	{
		Reason:        networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "A route on this load balancer sends traffic to a network service that does " +
			"not exist, or names a port that service does not declare. Datum will not guess at " +
			"either, so the load balancer has not been published.",
		Remediation: "Check the service name and the port name — the port is a name the service " +
			"declares, like \"http\", not a number. Create the service first if it is missing.",
		Skill: SkillBackendNotReachable,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonInstanceBackendNotFound,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "A route on this load balancer points straight at a set of machine addresses " +
			"that no longer exists.",
		Remediation: "Point the route at a network service instead, which tracks its machines as " +
			"they come and go.",
		Skill: SkillBackendNotReachable,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonNetworkServiceMembersUnreferenced,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityPlatform,
		Scope:         ScopeDegraded,
		Explanation: "A network service behind this load balancer has more machines than Datum is " +
			"currently sending traffic to. It is serving, but not from all of them.",
		Remediation: remediationEscalate,
		Skill:       SkillBackendNotReachable,
	},
	{
		Reason:        networkingv1alpha.HTTPProxyReasonNetworkServiceMembersUnaddressable,
		ConditionType: networkingv1alpha.HTTPProxyConditionProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeDegraded,
		Explanation: "Some machines behind a network service have no address of the kind that " +
			"service hands out, so they are not being sent any traffic. The rest are serving.",
		Remediation: "Check that those machines have an address of the family the service publishes.",
		Skill:       SkillBackendNotReachable,
	},
}

// hostnameCatalog covers the per-hostname conditions and the aggregates over
// them. Kept as its own slice only for reading; appended into catalog by init.
var hostnameCatalog = []ReasonInfo{
	// ------------------------------------------ aggregates over hostnames
	//
	// These say "one or more hostnames" and name none. They are listed in
	// aggregateReasons and are never reportable as a root cause; the walk fans
	// out into status.hostnameStatuses[] to find which hostname and why.
	{
		Reason:        networkingv1alpha.HTTPProxyReasonHostnamesVerified,
		ConditionType: networkingv1alpha.HTTPProxyConditionHostnamesVerified,
		Actionability: ActionabilityInformational,
		Explanation:   "Every custom hostname on this load balancer has had its domain ownership proven.",
	},
	{
		Reason:        networkingv1alpha.UnverifiedHostnamesPresent,
		ConditionType: networkingv1alpha.HTTPProxyConditionHostnamesVerified,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation: "At least one custom hostname on this load balancer is still waiting for you " +
			"to prove you own its domain. Until that is done, that hostname will not serve.",
		Remediation: "Look at each hostname to see which one, then create the DNS record that " +
			"proves ownership of its domain.",
		Skill: SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.HostnameInUseReason,
		ConditionType: networkingv1alpha.HTTPProxyConditionHostnamesInUse,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		// Negative polarity: this condition is True when something is wrong.
		Explanation: "A hostname on this load balancer is already claimed somewhere else on Datum, " +
			"so this load balancer cannot have it.",
		Remediation: "Use a different hostname, or release the one holding it. Who holds it is not " +
			"visible from here — it may be outside your project — so if you believe it should be " +
			"yours, raise it with Datum rather than searching for it.",
		Skill: SkillHostnameNotWorking,
	},
	{
		Reason:        networkingv1alpha.CertificatesReadyReasonAllCertificatesReady,
		ConditionType: networkingv1alpha.HTTPProxyConditionCertificatesReady,
		Actionability: ActionabilityInformational,
		Explanation:   "Every hostname on this load balancer has a certificate, so HTTPS works on all of them.",
	},
	{
		Reason:           networkingv1alpha.CertificatesReadyReasonCertificatesPending,
		ConditionType:    networkingv1alpha.HTTPProxyConditionCertificatesReady,
		Actionability:    ActionabilityTransient,
		Scope:            ScopeOneHostname,
		Explanation:      "At least one hostname on this load balancer does not have its certificate yet, so HTTPS will fail on it.",
		Remediation:      "Look at each hostname to see which one. A certificate cannot be issued until that hostname resolves publicly, so check its DNS first.",
		Skill:            SkillCertificateNotIssued,
		ExpectedDuration: windowCertificate,
	},
	{
		Reason:        networkingv1alpha.CertificatesReadyReasonCertificatesFailed,
		ConditionType: networkingv1alpha.HTTPProxyConditionCertificatesReady,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Getting a certificate failed for at least one hostname on this load balancer, so HTTPS will fail on it.",
		Remediation:   "Look at each hostname to see which one and why. The usual cause is that the hostname does not resolve to this load balancer yet.",
		Skill:         SkillCertificateNotIssued,
	},
	{
		Reason:        networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
		ConditionType: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Actionability: ActionabilityInformational,
		Explanation:   "Datum has created the DNS records for every hostname on this load balancer.",
	},
	{
		Reason:        networkingv1alpha.DNSRecordsProgrammedReasonAllApplicableCreated,
		ConditionType: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Actionability: ActionabilityInformational,
		Explanation: "Datum has created the DNS records for every hostname whose DNS it runs. The " +
			"rest are yours to point at this load balancer.",
	},
	{
		Reason:        networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		ConditionType: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum could not create the DNS record for at least one hostname on this load balancer.",
		Remediation:   "Look at each hostname to see which one and why.",
		Skill:         SkillHostnameNotWorking,
	},

	// --------------------------------------------- one hostname: claiming
	{
		Reason:        networkingv1alpha.HostnameAvailableReasonClaimed,
		ConditionType: networkingv1alpha.HostnameConditionAvailable,
		Actionability: ActionabilityInformational,
		Explanation:   "This load balancer holds this hostname.",
	},
	{
		Reason:        networkingv1alpha.HostnameAvailableReasonInUse,
		ConditionType: networkingv1alpha.HostnameConditionAvailable,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "This hostname is already claimed somewhere else on Datum, so this load balancer cannot serve it.",
		Remediation: "Use a different hostname, or release the one holding it. Who holds it is not " +
			"visible from here — it may be outside your project.",
		Skill: SkillHostnameNotWorking,
	},
}

// dnsAndCertCatalog covers one hostname's DNS record and certificate steps.
var dnsAndCertCatalog = []ReasonInfo{
	// ----------------------------------- one hostname: the DNS record
	{
		Reason:        networkingv1alpha.DNSRecordReasonCreated,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityInformational,
		Explanation:   "Datum has created the DNS record pointing this hostname at this load balancer.",
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonUpdated,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityInformational,
		Explanation:   "Datum has updated the DNS record pointing this hostname at this load balancer.",
	},
	{
		Reason:           networkingv1alpha.DNSRecordReasonPending,
		ConditionType:    networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability:    ActionabilityTransient,
		Scope:            ScopeOneHostname,
		Explanation:      "Datum has not written the DNS record for this hostname yet.",
		Remediation:      remediationWait,
		Skill:            SkillHostnameNotWorking,
		ExpectedDuration: windowDNSRecord,
	},
	{
		Reason:           networkingv1alpha.DNSRecordReasonRetryPending,
		ConditionType:    networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability:    ActionabilityTransient,
		Scope:            ScopeOneHostname,
		Explanation:      "Writing the DNS record for this hostname did not work the first time and Datum is trying again.",
		Remediation:      remediationWait,
		Skill:            SkillHostnameNotWorking,
		ExpectedDuration: windowDNSRecord,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonZoneNotFound,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityInformational,
		// Set True on purpose. Reporting this as a failure would send a customer
		// looking for a fault in the one arrangement that needs no fix.
		Explanation: "Datum does not run DNS for this hostname's domain, so there is no record for " +
			"it to create. That is a normal arrangement, not a fault.",
		Remediation: "Create a CNAME record with your own DNS provider pointing this hostname at " +
			"this load balancer's generated hostname.",
		Skill: SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonNotApplicable,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityInformational,
		Explanation: "Datum does not manage this hostname's domain, so there is no record for it to " +
			"create. That is a normal arrangement, not a fault.",
		Remediation: "Create a CNAME record with your own DNS provider pointing this hostname at " +
			"this load balancer's generated hostname.",
		Skill: SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonDomainNotVerified,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "You have not proven you own this hostname's domain yet, so Datum will not point it anywhere.",
		Remediation:   "Create the DNS record that proves ownership of the domain. The domain itself says which record and what value.",
		Skill:         SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonZoneNotReady,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "The DNS zone for this hostname's domain is not ready, so no record can be written into it.",
		Remediation:   "This is a DNS problem rather than a load balancer one. Check the zone for that domain.",
		Skill:         SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonDNSAuthorityMissing,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation: "You have proven you own this domain, but Datum is not answering DNS for it, " +
			"so it cannot create the record. Either the domain's nameservers still point at your " +
			"old provider, or its zone here is not finished.",
		Remediation: "At your registrar, point the domain's nameservers at the ones Datum's zone " +
			"gives you. If you would rather keep your own DNS, create a CNAME to this load " +
			"balancer's generated hostname instead.",
		Skill: SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonConflict,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation: "A DNS record for this hostname already exists and Datum did not write it, so " +
			"it will not overwrite it.",
		Remediation: "Remove the existing record for that hostname, or point this load balancer at a different one.",
		Skill:       SkillHostnameNotWorking,
	},
	{
		Reason:        networkingv1alpha.DNSRecordReasonFailed,
		ConditionType: networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Actionability: ActionabilityPlatform,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum could not write the DNS record for this hostname.",
		Remediation:   remediationEscalate,
		Skill:         SkillHostnameNotWorking,
	},

	// ----------------------------------- one hostname: the certificate
	{
		Reason:        networkingv1alpha.CertificateReadyReasonCertificateIssued,
		ConditionType: networkingv1alpha.HostnameConditionCertificateReady,
		Actionability: ActionabilityInformational,
		Explanation:   "This hostname has a certificate, so HTTPS works on it.",
	},
	{
		Reason:        networkingv1alpha.CertificateReadyReasonPending,
		ConditionType: networkingv1alpha.HostnameConditionCertificateReady,
		Actionability: ActionabilityTransient,
		Scope:         ScopeOneHostname,
		Explanation:   "This hostname does not have its certificate yet, so HTTPS will fail on it.",
		Remediation: "A certificate cannot be issued until the hostname resolves publicly to this " +
			"load balancer. Check that its DNS is in place first — if it is not, that is the cause " +
			"and this is only the symptom.",
		Skill:            SkillCertificateNotIssued,
		ExpectedDuration: windowCertificate,
	},
	{
		Reason:        networkingv1alpha.CertificateReadyReasonChallengeInProgress,
		ConditionType: networkingv1alpha.HostnameConditionCertificateReady,
		Actionability: ActionabilityTransient,
		Scope:         ScopeOneHostname,
		Explanation:   "This hostname's certificate is being issued right now: the authority is checking that the name really points here.",
		Remediation:   remediationWait,
		Skill:         SkillCertificateNotIssued,
		// Longer than the plain pending case: the check is already running and
		// is gated on public DNS caches expiring.
		ExpectedDuration: windowCertificate,
	},
	{
		Reason:        networkingv1alpha.CertificateReadyReasonProvisioningFailed,
		ConditionType: networkingv1alpha.HostnameConditionCertificateReady,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Getting a certificate for this hostname failed, so HTTPS will fail on it.",
		Remediation: "Almost always this is DNS: the hostname has to resolve publicly to this load " +
			"balancer before a certificate can be issued. Check that first, then look at the " +
			"status message.",
		Skill: SkillCertificateNotIssued,
	},
}

// domainCatalog covers the Domain behind a custom hostname. This is where
// ownership actually lives: the hostname's own Verified condition is declared
// in the API but no controller writes it, so the ownership step of a hostname's
// progress is read from here and never from the hostname's status.
var domainCatalog = []ReasonInfo{
	{
		Reason:        networkingv1alpha.DomainReasonVerified,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityInformational,
		Explanation:   "You have proven you own this domain.",
	},
	{
		Reason:        networkingv1alpha.DomainReasonPendingVerification,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		// Deliberately user, not transient, and with no window: this waits on a
		// person creating a DNS record. A window here would be a claim about how
		// fast the customer is, dressed up as a claim about Datum.
		Explanation: "Datum is waiting for proof that you own this domain. Nothing on it will serve until that is done.",
		Remediation: "Create the DNS record the domain asks for, exactly as given. Once it is " +
			"visible publicly, Datum picks it up on its own.",
		Skill: SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonVerificationRecordNotFound,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum looked for the record that proves you own this domain and did not find it.",
		Remediation: "Check the record exists at the exact name asked for, and that it is visible " +
			"publicly — a record inside a private or split-horizon zone will not be found. New " +
			"records can take a while to spread.",
		Skill: SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonVerificationRecordContentMismatch,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "The record proving you own this domain is there, but its value is not the one Datum asked for.",
		Remediation: "Replace the value with the one the domain gives, exactly — no added quotes " +
			"and no trailing spaces. If you have an older record from a previous attempt, remove it.",
		Skill: SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonVerificationUnexpectedResponse,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum got an answer it did not expect while checking that you own this domain.",
		Remediation:   "Check the domain resolves at all, and that nothing in front of it is answering on its behalf.",
		Skill:         SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonVerificationInternalError,
		ConditionType: networkingv1alpha.DomainConditionVerified,
		Actionability: ActionabilityPlatform,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum hit an error of its own while checking that you own this domain.",
		Remediation:   remediationEscalate,
		Skill:         SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonValid,
		ConditionType: networkingv1alpha.DomainConditionValidDomain,
		Actionability: ActionabilityInformational,
		Explanation:   "This is a domain you can register, so it can be verified and used.",
	},
	{
		Reason:        networkingv1alpha.DomainReasonInvalidDomain,
		ConditionType: networkingv1alpha.DomainConditionValidDomain,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation: "This is not a domain anyone can register — it is a public suffix like \"com\" " +
			"or \"co.uk\" rather than a name of your own. Nothing can be verified against it.",
		Remediation: "Use the domain you actually registered.",
		Skill:       SkillDomainVerification,
	},
	{
		Reason:        networkingv1alpha.DomainReasonDNSZoneNotFound,
		ConditionType: networkingv1alpha.DomainConditionVerifiedDNSZone,
		Actionability: ActionabilityInformational,
		Explanation:   "Datum does not run DNS for this domain. That is a normal arrangement, not a fault.",
		Remediation:   "Keep proving ownership with a DNS record at your own provider, and point your hostnames here with a CNAME.",
		Skill:         SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DomainReasonDNSZoneNotReady,
		ConditionType: networkingv1alpha.DomainConditionVerifiedDNSZone,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation:   "Datum has a DNS zone for this domain but it is not finished, so it cannot answer for it yet.",
		Remediation:   "This is a DNS problem rather than a load balancer one. Check that zone.",
		Skill:         SkillDNSDelegation,
	},
	{
		Reason:        networkingv1alpha.DomainReasonDNSZoneNameserverMismatch,
		ConditionType: networkingv1alpha.DomainConditionVerifiedDNSZone,
		Actionability: ActionabilityUser,
		Scope:         ScopeOneHostname,
		Explanation: "This domain's nameservers do not include the ones Datum's zone for it uses, " +
			"so the wider internet still asks somebody else about this domain.",
		Remediation: "At your registrar, set the domain's nameservers to the ones Datum's zone gives you.",
		Skill:       SkillDNSDelegation,
	},
}

// networkServiceCatalog covers a network service named as an origin. Only its
// own reasons: whether a service is healthy is that service's answer, and this
// package reports it rather than re-deriving it.
var networkServiceCatalog = []ReasonInfo{
	{
		Reason:        networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces,
		ConditionType: networkingv1alpha.NetworkServiceMembersResolved,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "This network service has nothing behind it: nothing matches what it selects. " +
			"A service written before the thing it points at is an ordinary state, not an error — " +
			"but until something matches, there is nowhere for traffic to go.",
		Remediation: "Start the workload behind this service, or check that what it selects matches " +
			"the labels that workload actually carries.",
		Skill: SkillBackendNotReachable,
	},
	{
		Reason:        networkingv1alpha.NetworkServiceReasonMultipleNetworks,
		ConditionType: networkingv1alpha.NetworkServiceMembersResolved,
		Actionability: ActionabilityUser,
		Scope:         ScopeAllTraffic,
		Explanation: "This network service selects things on more than one network. A service " +
			"covers one network, so rather than quietly pick one, Datum serves none of them.",
		Remediation: "Narrow what the service selects so everything it matches is on one network.",
		Skill:       SkillBackendNotReachable,
	},
	{
		Reason:        networkingv1alpha.NetworkServiceReasonNoServingLocations,
		ConditionType: networkingv1alpha.NetworkServiceReady,
		Actionability: ActionabilityPlatform,
		Scope:         ScopeAllTraffic,
		Explanation:   "Every location this network service has something in is out of rotation, so none of them can take traffic.",
		Remediation:   remediationEscalate,
		Skill:         SkillBackendNotReachable,
	},
}

func init() {
	catalog = append(catalog, hostnameCatalog...)
	catalog = append(catalog, dnsAndCertCatalog...)
	catalog = append(catalog, domainCatalog...)
	catalog = append(catalog, networkServiceCatalog...)

	byKey = make(map[Key]ReasonInfo, len(catalog))
	byReason = make(map[string][]ReasonInfo, len(catalog))
	for i := range catalog {
		catalog[i].ExpectedWithin = humanDuration(catalog[i].ExpectedDuration)
		k := Key{ConditionType: catalog[i].ConditionType, Reason: catalog[i].Reason}
		byKey[k] = catalog[i]
		byReason[catalog[i].Reason] = append(byReason[catalog[i].Reason], catalog[i])
	}
}

var (
	byKey    map[Key]ReasonInfo
	byReason map[string][]ReasonInfo
)

// ExplainReason returns the catalog entry for a reason on a condition type.
//
// Callers that have the condition type must pass it. A reason alone is
// ambiguous in this API — "Pending" is three different answers — which is why
// ExplainAnyReason exists separately and says so out loud.
func ExplainReason(conditionType, reason string) (ReasonInfo, bool) {
	info, ok := byKey[Key{ConditionType: conditionType, Reason: reason}]
	return info, ok
}

// ExplainAnyReason returns every meaning a reason has, in declaration order.
// More than one result means the reason is ambiguous without its condition
// type, and the caller must say so rather than picking one.
func ExplainAnyReason(reason string) []ReasonInfo {
	out := make([]ReasonInfo, len(byReason[reason]))
	copy(out, byReason[reason])
	return out
}

// AllReasons returns the whole catalog, in declaration order.
func AllReasons() []ReasonInfo {
	out := make([]ReasonInfo, len(catalog))
	copy(out, catalog)
	return out
}

// aggregateKeys are the conditions that report over a set of hostnames and name
// none of them. Unlike a pointer, which redirects to one deeper condition that
// exists, these require fanning out across status.hostnameStatuses[] to find
// which hostname is affected and why.
//
// The walk never reports one as a root cause: "one or more hostnames have no
// certificate" is not an answer anybody can act on.
var aggregateKeys = map[Key]struct{}{
	{networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure}: {},
	{networkingv1alpha.HTTPProxyConditionCertificatesReady, networkingv1alpha.CertificatesReadyReasonCertificatesPending}:  {},
	{networkingv1alpha.HTTPProxyConditionCertificatesReady, networkingv1alpha.CertificatesReadyReasonCertificatesFailed}:   {},
	{networkingv1alpha.HTTPProxyConditionHostnamesVerified, networkingv1alpha.UnverifiedHostnamesPresent}:                  {},
	{networkingv1alpha.HTTPProxyConditionHostnamesInUse, networkingv1alpha.HostnameInUseReason}:                            {},
	// Cross-object: the cause is on the Domain, not here.
	{networkingv1alpha.HostnameConditionDNSRecordProgrammed, networkingv1alpha.DNSRecordReasonDomainNotVerified}: {},
}

// notStartedKeys are the conditions that say nothing has run yet.
//
// These are causes — "Datum has not looked at this yet" is the honest answer
// when it is true, and suppressing it reports a load balancer that has never
// been evaluated as having no faults. But they are the least specific answer
// there is, so they rank behind any cause that names something.
var notStartedKeys = map[Key]struct{}{
	{networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyReasonPending}:   {},
	{networkingv1alpha.HTTPProxyConditionProgrammed, networkingv1alpha.HTTPProxyReasonPending}: {},
}

// IsNotStarted reports whether a condition says nothing has run yet, rather
// than naming something that went wrong.
func IsNotStarted(conditionType, reason string) bool {
	_, ok := notStartedKeys[Key{ConditionType: conditionType, Reason: reason}]
	return ok
}

// IsAggregate reports whether a condition reports over a set without naming a
// member, so the walk must descend rather than report it.
func IsAggregate(conditionType, reason string) bool {
	_, ok := aggregateKeys[Key{ConditionType: conditionType, Reason: reason}]
	return ok
}

// negativePolarity are the condition types that are True when something is
// wrong. Every other condition here is True when it is healthy.
//
// HTTPProxyConditionHostnamesInUse is the only one today, and it is the easiest
// mistake in this package to make: a generic `status != True means failing`
// helper reads a hostname collision as healthy and drops it silently.
var negativePolarity = map[string]struct{}{
	networkingv1alpha.HTTPProxyConditionHostnamesInUse: {},
}

// Failing reports whether a condition is saying something is wrong, honouring
// the condition types that are True when they are unhappy.
func Failing(conditionType string, status metav1.ConditionStatus) bool {
	if _, inverted := negativePolarity[conditionType]; inverted {
		return status == metav1.ConditionTrue
	}
	return status != metav1.ConditionTrue
}

// ActionabilityAt reports how a reason should be treated given how long its
// condition has held, escalating a transient reason to ActionabilityStalled
// once it outlives the catalog's window. Nothing is persisted: lastTransition
// is compared against now on every read.
//
// A missing, unparseable or implausible timestamp returns the static
// classification — absence of evidence that a state is old is not evidence that
// it has stalled. Both HTTPProxy and Domain default lastTransitionTime to the
// Unix epoch in their CRDs, so without that guard every freshly created object
// reads as stuck for twenty thousand days.
func ActionabilityAt(info ReasonInfo, lastTransition string, now time.Time) Actionability {
	if info.Actionability != ActionabilityTransient || info.ExpectedDuration <= 0 {
		return info.Actionability
	}
	elapsed, ok := age(lastTransition, now)
	if !ok || elapsed <= info.ExpectedDuration {
		return info.Actionability
	}
	return ActionabilityStalled
}
