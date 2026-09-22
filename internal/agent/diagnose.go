// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

// Confidence says how much the answer is worth, separately from who has to act.
//
// It is its own axis deliberately. Actionability answers "whose problem is
// this"; confidence answers "how much do we actually know". Folding the second
// into the first is how a load balancer that reports everything true, and
// serves nothing, comes back as healthy.
type Confidence string

const (
	// ConfidenceReported means something reported a fault and this is it.
	ConfidenceReported Confidence = "reported"

	// ConfidenceUnverified means nothing reports a fault and nothing reports
	// success either. Datum publishes no signal for whether its edge can
	// actually serve a load balancer, so a green status on a load balancer that
	// has never taken a request is this, not health.
	ConfidenceUnverified Confidence = "unverified"

	// ConfidencePartial means a fault was found but some evidence behind it
	// could not be read.
	ConfidencePartial Confidence = "partial"
)

// StepState is how one step of a hostname's progress stands.
type StepState string

const (
	// StepOK means the step is done.
	StepOK StepState = "ok"
	// StepFailed means the step reported a fault.
	StepFailed StepState = "failed"
	// StepPending means the step is in flight.
	StepPending StepState = "pending"
	// StepNotReported means nothing published anything about this step. It is
	// neither pass nor fail, and it must never be read as either.
	//
	// Two live causes: the API declares a per-hostname Verified condition that
	// no controller writes, so ownership is always read from the Domain; and a
	// generated hostname publishes no DNS-record condition at all.
	StepNotReported StepState = "notReported"
	// StepNotApplicable means the step does not apply to this hostname, which
	// the API reports by setting the condition True.
	StepNotApplicable StepState = "notApplicable"
)

// Step names, in the order a hostname passes through them.
const (
	StepClaimed     = "claimed"
	StepOwnership   = "ownership-verified"
	StepDNSRecord   = "dns-record"
	StepCertificate = "certificate"
)

// Cause is one condition that reported something, with everything needed to act
// on it or escalate it.
type Cause struct {
	// Object names what reported this, in the customer's words.
	Object string `json:"object"`
	// Hostname is the hostname this concerns, when it concerns one. The
	// aggregate conditions never carry it, which is the whole reason the walk
	// fans out rather than reporting them.
	Hostname string `json:"hostname,omitempty"`
	// ConditionType and Reason are what to escalate with.
	ConditionType string `json:"conditionType"`
	Reason        string `json:"reason"`
	// Message is what the platform said, verbatim.
	Message string `json:"message,omitempty"`
	// Level is how deep in the walk this was found; deeper is more specific.
	Level int `json:"level"`

	Actionability Actionability `json:"actionability"`
	Scope         Scope         `json:"scope,omitempty"`
	Explanation   string        `json:"explanation,omitempty"`
	Remediation   string        `json:"remediation,omitempty"`
	Skill         string        `json:"skill,omitempty"`

	// InStateSince is when this condition last changed, RFC 3339, or empty when
	// the recorded value could not be believed.
	InStateSince string `json:"inStateSince,omitempty"`
	// InStateFor renders that as a duration a reader can judge ("15m").
	InStateFor string `json:"inStateFor,omitempty"`
	// TimeDiscarded explains a timestamp that was thrown away, so a reader
	// knows whether the API set none or set one that was a placeholder.
	TimeDiscarded string `json:"timeDiscarded,omitempty"`
}

// HostnameStep is one step of one hostname's progress.
type HostnameStep struct {
	Step    string    `json:"step"`
	State   StepState `json:"state"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
	// Note explains a state that would otherwise be misread, above all
	// notReported.
	Note string `json:"note,omitempty"`
}

// DomainView is what the Domain behind a custom hostname says.
type DomainView struct {
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
	Apex     bool   `json:"apex,omitempty"`
	// VerificationRecord is the record the customer has to create, verbatim.
	VerificationRecord *DNSRecordView `json:"verificationRecord,omitempty"`
	// Nameservers are the nameservers this domain currently publishes.
	Nameservers []string `json:"nameservers,omitempty"`
}

// DNSRecordView is a DNS record a customer has to create, in the three fields
// every DNS provider's form asks for.
type DNSRecordView struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// HostnameProgress is one hostname and how far it has got.
type HostnameProgress struct {
	Hostname string `json:"hostname"`
	// Generated is true for the hostname Datum assigned, which the customer
	// points a CNAME at rather than proving ownership of.
	Generated bool           `json:"generated,omitempty"`
	Working   bool           `json:"working"`
	Steps     []HostnameStep `json:"steps"`
	Domain    *DomainView    `json:"domain,omitempty"`
}

// Diagnosis is the answer to "why is this load balancer not working".
type Diagnosis struct {
	LoadBalancer      string     `json:"loadBalancer"`
	DisplayName       string     `json:"displayName,omitempty"`
	Serving           bool       `json:"serving"`
	Confidence        Confidence `json:"confidence"`
	GeneratedHostname string     `json:"generatedHostname,omitempty"`
	ObjectAge         string     `json:"objectAge,omitempty"`

	RootCause   *Cause             `json:"rootCause,omitempty"`
	OtherCauses []Cause            `json:"otherCauses,omitempty"`
	Hostnames   []HostnameProgress `json:"hostnames,omitempty"`

	// Protection reports traffic protection, including the case nothing
	// reports: no policy guards this load balancer at all.
	Protection ProtectionView `json:"protection"`

	Summary   string   `json:"summary"`
	NextSteps []string `json:"nextSteps,omitempty"`
	// Unread names evidence the walk could not read, so a thin answer is
	// visibly thin rather than quietly confident.
	Unread []string `json:"unread,omitempty"`
}

// ProtectionView reports whether traffic is being inspected.
type ProtectionView struct {
	Attached bool   `json:"attached"`
	Policy   string `json:"policy,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Paranoia int    `json:"paranoia,omitempty"`
}

// Diagnose walks a load balancer and everything it depends on, and returns the
// deepest condition that names a real cause.
//
// The walk fans out rather than following pointers. This API's top-level
// conditions aggregate over a set of hostnames and name none of them —
// "PartialFailure" means one or more, and which one lives in
// status.hostnameStatuses[]. So an answer is only an answer once it names the
// hostname, or the route, or the service it is about.
func Diagnose(ctx context.Context, r Reader, namespace, name string) (*Diagnosis, error) {
	return DiagnoseAt(ctx, r, namespace, name, time.Now())
}

// DiagnoseAt is Diagnose with the clock injected, so ages are testable.
func DiagnoseAt(ctx context.Context, r Reader, namespace, name string, now time.Time) (*Diagnosis, error) {
	proxy, err := r.GetProxy(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	return diagnoseProxy(ctx, r, namespace, proxy, now), nil
}

func diagnoseProxy(
	ctx context.Context,
	r Reader,
	namespace string,
	proxy *networkingv1alpha.HTTPProxy,
	now time.Time,
) *Diagnosis {
	d := &Diagnosis{
		LoadBalancer:      proxy.Name,
		DisplayName:       spec.DisplayName(proxy),
		GeneratedHostname: proxy.Status.CanonicalHostname,
		ObjectAge:         humanDuration(sinceCreation(proxy.CreationTimestamp, now)),
	}

	var causes []Cause
	causes = append(causes, proxyCauses(proxy, now)...)

	domains, err := r.ListDomains(ctx, namespace)
	if err != nil {
		d.Unread = append(d.Unread, "the domains behind this load balancer's hostnames could not be read, so nothing here explains a hostname that is not verified")
	}

	d.Hostnames = hostnameProgress(proxy, domains, now)
	causes = append(causes, hostnameCauses(proxy, d.Hostnames, now)...)

	d.Protection = protectionFor(ctx, r, namespace, proxy, d)

	sortCauses(causes)
	if len(causes) > 0 {
		d.RootCause = &causes[0]
		d.OtherCauses = causes[1:]
	}

	d.Serving = d.RootCause == nil || d.RootCause.Scope == ScopeProtectionOnly
	d.Confidence = confidence(d)
	d.Summary, d.NextSteps = summarise(d)
	return d
}

// proxyCauses reads the load balancer's own conditions. Aggregates are skipped:
// the walk finds what they are aggregating over instead.
func proxyCauses(proxy *networkingv1alpha.HTTPProxy, now time.Time) []Cause {
	var out []Cause
	for i := range proxy.Status.Conditions {
		c := proxy.Status.Conditions[i]
		if !Failing(c.Type, c.Status) {
			continue
		}
		if IsAggregate(c.Type, c.Reason) {
			continue
		}
		out = append(out, newCause(
			"load balancer "+proxy.Name, "", c, proxy.CreationTimestamp, levelProxy, now))
	}
	return out
}

// Levels record how deep in the walk a cause was found. Deeper is more
// specific, and more specific wins within the same scope.
const (
	levelProxy = iota
	levelHostname
	levelDomain
)

func newCause(object, hostname string, c metav1.Condition, created metav1.Time, level int, now time.Time) Cause {
	cause := Cause{
		Object:        object,
		Hostname:      hostname,
		ConditionType: c.Type,
		Reason:        c.Reason,
		Message:       c.Message,
		Level:         level,
	}
	cause.InStateSince = transitionTime(c, created)
	cause.TimeDiscarded = discardedTime(c, cause.InStateSince)
	if d, ok := age(cause.InStateSince, now); ok {
		cause.InStateFor = humanDuration(d)
	}

	info, ok := ExplainReason(c.Type, c.Reason)
	if !ok {
		// An uncatalogued reason still travels: a bare code the customer can
		// escalate with beats silence. TestCatalogCoversEveryInScopeAPIReason
		// exists so this stays theoretical.
		cause.Actionability = ActionabilityPlatform
		cause.Explanation = "Datum reported something this assistant does not have an explanation for."
		cause.Remediation = remediationEscalate
		return cause
	}
	cause.Actionability = ActionabilityAt(info, cause.InStateSince, now)
	cause.Scope = info.Scope
	cause.Explanation = info.Explanation
	cause.Remediation = info.Remediation
	cause.Skill = info.Skill
	if cause.Actionability == ActionabilityStalled {
		cause.Remediation = fmt.Sprintf(
			"This normally clears within %s and has been like this for %s, so treat it as stuck rather than in progress. %s",
			info.ExpectedWithin, cause.InStateFor, remediationEscalate)
	}
	return cause
}

// sortCauses puts the most useful answer first: widest blast radius, then
// deepest, then stable.
//
// Scope leads deliberately. Ranking by depth alone would put a traffic
// protection fault above a dead origin, and answer "your WAF is not attached"
// to someone whose site is returning nothing at all.
func sortCauses(causes []Cause) {
	sort.SliceStable(causes, func(i, j int) bool {
		si, sj := scopeRank(causes[i].Scope), scopeRank(causes[j].Scope)
		if si != sj {
			return si < sj
		}
		// "Nothing has run yet" is true but says nothing about what is wrong,
		// so anything that names something outranks it.
		ni := IsNotStarted(causes[i].ConditionType, causes[i].Reason)
		nj := IsNotStarted(causes[j].ConditionType, causes[j].Reason)
		if ni != nj {
			return !ni
		}
		return causes[i].Level > causes[j].Level
	})
}

func scopeRank(s Scope) int {
	switch s {
	case ScopeAllTraffic:
		return 0
	case ScopeOneHostname:
		return 1
	case ScopeDegraded:
		return 2
	case ScopeProtectionOnly:
		return 3
	default:
		return 4
	}
}

func sinceCreation(created metav1.Time, now time.Time) time.Duration {
	if !plausible(created) {
		return 0
	}
	d := now.Sub(created.Time)
	if d < 0 {
		return 0
	}
	return d
}

func conditionOf(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if strings.EqualFold(conditions[i].Type, conditionType) {
			return &conditions[i]
		}
	}
	return nil
}
