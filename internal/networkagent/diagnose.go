// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/agenttime"
)

var nowFunc = time.Now

type Confidence string

const (
	ConfidenceReported Confidence = "reported"
	ConfidenceClean    Confidence = "clean"
	ConfidencePartial  Confidence = "partial"
)

type Cause struct {
	Object         string        `json:"object"`
	Kind           string        `json:"kind"`
	Name           string        `json:"name"`
	Location       string        `json:"location,omitempty"`
	Workload       string        `json:"workload,omitempty"`
	ConditionType  string        `json:"conditionType"`
	Reason         string        `json:"reason"`
	Status         string        `json:"status"`
	Message        string        `json:"message,omitempty"`
	Actionability  Actionability `json:"actionability"`
	Scope          Scope         `json:"scope,omitempty"`
	Explanation    string        `json:"explanation"`
	Remediation    string        `json:"remediation,omitempty"`
	Skill          string        `json:"skill,omitempty"`
	ExpectedWithin string        `json:"expectedWithin,omitempty"`
	InStateSince   string        `json:"inStateSince,omitempty"`
	InStateFor     string        `json:"inStateFor,omitempty"`
	TimeDiscarded  string        `json:"timeDiscarded,omitempty"`
	Catalogued     bool          `json:"catalogued"`
}

const uncataloguedExplanation = "Datum reported something this assistant has no explanation for. The reason and " +
	"message are what to quote when raising it with Datum."

type subject struct {
	object   string
	kind     string
	name     string
	location string
	workload string
	created  metav1.Time
	fallback Scope
}

func causeFrom(s subject, c metav1.Condition, now time.Time) Cause {
	cause := Cause{
		Object:        s.object,
		Kind:          s.kind,
		Name:          s.name,
		Location:      s.location,
		Workload:      s.workload,
		ConditionType: c.Type,
		Reason:        c.Reason,
		Status:        string(c.Status),
		Message:       c.Message,
	}
	cause.InStateSince = agenttime.TransitionTime(c, s.created)
	cause.TimeDiscarded = agenttime.DiscardedTime(c, cause.InStateSince)
	if d, ok := agenttime.Age(cause.InStateSince, now); ok {
		cause.InStateFor = agenttime.HumanDuration(d)
	}

	info, ok := ExplainReason(s.kind, c.Type, c.Reason)
	if !ok {
		cause.Actionability = ActionabilityPlatform
		cause.Scope = s.fallback
		cause.Explanation = uncataloguedExplanation
		cause.Remediation = remediationEscalate
		return cause
	}
	cause.Catalogued = true
	cause.Actionability = ActionabilityAt(info, cause.InStateSince, now)
	cause.Scope = info.Scope
	if cause.Scope == "" {
		cause.Scope = s.fallback
	}
	cause.Explanation = info.Explanation
	cause.Remediation = info.Remediation
	cause.Skill = info.Skill
	cause.ExpectedWithin = info.ExpectedWithin
	return cause
}

func failingCauses(s subject, conditions []metav1.Condition, types []string, now time.Time) []Cause {
	var out []Cause
	seen := map[string]bool{}
	for _, t := range types {
		c := apimeta.FindStatusCondition(conditions, t)
		if c == nil || !Failing(*c) || seen[c.Reason] {
			continue
		}
		seen[c.Reason] = true
		if info, ok := ExplainReason(s.kind, c.Type, c.Reason); ok && info.Actionability == ActionabilityInformational {
			continue
		}
		out = append(out, causeFrom(s, *c, now))
	}
	return out
}

func actionabilityRank(a Actionability) int {
	switch a {
	case ActionabilityStalled:
		return 0
	case ActionabilityPlatform, ActionabilityUser:
		return 1
	case ActionabilityTransient:
		return 2
	default:
		return 3
	}
}

func rank(causes []Cause) {
	sort.SliceStable(causes, func(i, j int) bool {
		a, b := causes[i], causes[j]
		if scopeRank(a.Scope) != scopeRank(b.Scope) {
			return scopeRank(a.Scope) < scopeRank(b.Scope)
		}
		if actionabilityRank(a.Actionability) != actionabilityRank(b.Actionability) {
			return actionabilityRank(a.Actionability) < actionabilityRank(b.Actionability)
		}
		return a.Object < b.Object
	})
}

type LocationView struct {
	Location         string   `json:"location"`
	Ready            bool     `json:"ready"`
	AddressRanges    []string `json:"addressRanges,omitempty"`
	Instances        int      `json:"instances"`
	InstancesServing int      `json:"instancesServing"`
}

type NetworkDiagnosis struct {
	Network     string         `json:"network"`
	Ready       bool           `json:"ready"`
	Deleting    bool           `json:"deleting,omitempty"`
	Confidence  Confidence     `json:"confidence"`
	Summary     string         `json:"summary"`
	RootCause   *Cause         `json:"rootCause,omitempty"`
	OtherCauses []Cause        `json:"otherCauses,omitempty"`
	Locations   []LocationView `json:"locations,omitempty"`
	Unreadable  []string       `json:"unreadable,omitempty"`
}

func diagnoseNetwork(ne *networkEvidence, now time.Time) *NetworkDiagnosis {
	n := ne.network
	d := &NetworkDiagnosis{
		Network:    n.Name,
		Ready:      apimeta.IsStatusConditionTrue(n.Status.Conditions, networkingv1alpha.NetworkReady),
		Deleting:   !n.DeletionTimestamp.IsZero(),
		Locations:  locationViews(ne),
		Unreadable: ne.unreadable,
	}

	causes := networkCauses(ne, now)
	rank(causes)
	if len(causes) > 0 {
		d.RootCause = &causes[0]
		d.OtherCauses = causes[1:]
	}

	switch {
	case d.RootCause != nil:
		d.Confidence = ConfidenceReported
	case len(d.Unreadable) > 0:
		d.Confidence = ConfidencePartial
	default:
		d.Confidence = ConfidenceClean
	}
	d.Summary = networkSummary(d)
	return d
}

func networkCauses(ne *networkEvidence, now time.Time) []Cause {
	n := ne.network
	var causes []Cause

	causes = append(causes, failingCauses(subject{
		object: fmt.Sprintf("network %s", n.Name), kind: KindNetwork, name: n.Name,
		created: n.CreationTimestamp, fallback: ScopeNetwork,
	}, n.Status.Conditions, []string{networkingv1alpha.NetworkIPAMAllocated, networkingv1alpha.NetworkReady}, now)...)

	readyContexts := map[string]bool{}
	for i := range ne.contexts {
		c := &ne.contexts[i]
		loc := c.Spec.Location.Name
		if apimeta.IsStatusConditionTrue(c.Status.Conditions, networkingv1alpha.NetworkContextReady) {
			readyContexts[loc] = true
		}
		causes = append(causes, failingCauses(subject{
			object: fmt.Sprintf("network %s in %s", n.Name, loc), kind: KindNetworkContext, name: c.Name,
			location: loc, created: c.CreationTimestamp, fallback: ScopeLocation,
		}, c.Status.Conditions, []string{networkingv1alpha.NetworkContextIPAMAllocated, networkingv1alpha.NetworkContextReady}, now)...)
	}

	for i := range ne.bindings {
		b := &ne.bindings[i]
		ready := apimeta.FindStatusCondition(b.Status.Conditions, networkingv1alpha.NetworkBindingReady)
		if ready == nil || !Failing(*ready) {
			continue
		}
		loc := b.Spec.Location.Name
		if isPointer(KindNetworkBinding, ready.Type, ready.Reason) && locationKnown(ne, loc) {
			continue
		}
		workload := ""
		if b.Spec.Consumer != nil {
			workload = b.Spec.Consumer.Name
		}
		causes = append(causes, causeFrom(subject{
			object: fmt.Sprintf("network %s in %s", n.Name, loc), kind: KindNetworkBinding, name: b.Name,
			location: loc, workload: workload, created: b.CreationTimestamp, fallback: ScopeLocation,
		}, *ready, now))
	}

	for i := range ne.subnets {
		s := &ne.subnets[i]
		loc := s.Spec.Location.Name
		causes = append(causes, failingCauses(subject{
			object: fmt.Sprintf("address range for network %s in %s", n.Name, loc), kind: KindSubnet, name: s.Name,
			location: loc, created: s.CreationTimestamp, fallback: ScopeLocation,
		}, s.Status.Conditions, []string{networkingv1alpha.SubnetAllocated, networkingv1alpha.SubnetProgrammed, networkingv1alpha.SubnetReady}, now)...)
	}

	for i := range ne.claims {
		causes = append(causes, claimCauses(&ne.claims[i], now)...)
	}

	for i := range ne.interfaces {
		iface := &ne.interfaces[i]
		if iface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound {
			continue
		}
		causes = append(causes, failingCauses(interfaceSubject(iface), iface.Status.Conditions, []string{
			networkingv1alpha.NetworkInterfaceAllocated,
			networkingv1alpha.NetworkInterfacePrepared,
			networkingv1alpha.NetworkInterfaceProgrammed,
		}, now)...)
	}
	return causes
}

func locationKnown(ne *networkEvidence, loc string) bool {
	for _, c := range ne.contexts {
		if c.Spec.Location.Name == loc {
			return true
		}
	}
	return false
}

func interfaceSubject(iface *networkingv1alpha.NetworkInterface) subject {
	workload := workloadOf(iface)
	object := fmt.Sprintf("network interface %s", iface.Name)
	if workload != "" {
		object = fmt.Sprintf("network interface %s of workload %s", iface.Name, workload)
	}
	return subject{
		object: object, kind: KindNetworkInterface, name: iface.Name, location: interfaceLocation(iface),
		workload: workload, created: iface.CreationTimestamp, fallback: ScopeInstance,
	}
}

func claimCauses(claim *networkingv1alpha.NetworkInterfaceClaim, now time.Time) []Cause {
	ready := apimeta.FindStatusCondition(claim.Status.Conditions, networkingv1alpha.NetworkInterfaceClaimReady)
	if ready == nil || !Failing(*ready) {
		return nil
	}
	workload := workloadOf(claim)
	s := subject{
		object: fmt.Sprintf("network interface for instance %s", claim.Name), kind: KindNetworkInterfaceClaim,
		name: claim.Name, workload: workload, created: claim.CreationTimestamp, fallback: ScopeInstance,
	}
	reported := *ready
	if isPointer(KindNetworkInterfaceClaim, ready.Type, ready.Reason) {
		unmet := strings.TrimPrefix(ready.Reason, notPrefix)
		if c := apimeta.FindStatusCondition(claim.Status.Conditions, unmet); c != nil {
			reported = *c
		}
	}
	return []Cause{causeFrom(s, reported, now)}
}

func locationViews(ne *networkEvidence) []LocationView {
	byLoc := map[string]*LocationView{}
	view := func(loc string) *LocationView {
		if loc == "" {
			loc = "unknown"
		}
		if v, ok := byLoc[loc]; ok {
			return v
		}
		v := &LocationView{Location: loc}
		byLoc[loc] = v
		return v
	}
	for _, c := range ne.contexts {
		v := view(c.Spec.Location.Name)
		v.Ready = apimeta.IsStatusConditionTrue(c.Status.Conditions, networkingv1alpha.NetworkContextReady)
	}
	for _, s := range ne.subnets {
		if s.Status.StartAddress != nil && s.Status.PrefixLength != nil {
			v := view(s.Spec.Location.Name)
			v.AddressRanges = append(v.AddressRanges, fmt.Sprintf("%s/%d", *s.Status.StartAddress, *s.Status.PrefixLength))
		}
	}
	for i := range ne.interfaces {
		iface := &ne.interfaces[i]
		if iface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound {
			continue
		}
		v := view(interfaceLocation(iface))
		v.Instances++
		if apimeta.IsStatusConditionTrue(iface.Status.Conditions, networkingv1alpha.NetworkInterfaceHolderAvailable) {
			v.InstancesServing++
		}
	}
	out := make([]LocationView, 0, len(byLoc))
	for _, v := range byLoc {
		sort.Strings(v.AddressRanges)
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Location < out[j].Location })
	return out
}

func networkSummary(d *NetworkDiagnosis) string {
	if c := d.RootCause; c != nil {
		s := fmt.Sprintf("%s (%s on %s", c.Explanation, c.Reason, c.Object)
		if c.InStateFor != "" {
			s += ", for " + c.InStateFor
		}
		s += ")."
		if len(d.OtherCauses) > 0 {
			s += fmt.Sprintf(" %d more finding(s) below.", len(d.OtherCauses))
		}
		return s
	}
	if d.Confidence == ConfidencePartial {
		return fmt.Sprintf("Nothing reported a fault on network %s, but some of what it is made of could not be "+
			"read, so this is not a clean bill of health. See unreadable.", d.Network)
	}
	if len(d.Locations) == 0 {
		return fmt.Sprintf("Network %s is ready and not in use yet. A network reaches a location when a workload "+
			"placed there attaches to it, so having no locations is normal for a new network.", d.Network)
	}
	instances := 0
	for _, l := range d.Locations {
		instances += l.Instances
	}
	return fmt.Sprintf("Network %s is ready in %d location(s) with %d instance(s) attached, and nothing reports a fault.",
		d.Network, len(d.Locations), instances)
}

type MemberView struct {
	Interface string `json:"interface"`
	Workload  string `json:"workload,omitempty"`
	Location  string `json:"location,omitempty"`
	Serving   bool   `json:"serving"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

type PortView struct {
	Name        string                       `json:"name"`
	Port        int32                        `json:"port,omitempty"`
	PortRange   *networkingv1alpha.PortRange `json:"portRange,omitempty"`
	Protocol    string                       `json:"protocol,omitempty"`
	AppProtocol string                       `json:"appProtocol,omitempty"`
}

type ServiceDiagnosis struct {
	Service            string                                           `json:"service"`
	Ready              bool                                             `json:"ready"`
	Confidence         Confidence                                       `json:"confidence"`
	Summary            string                                           `json:"summary"`
	Selects            string                                           `json:"selects"`
	Networks           []string                                         `json:"networks,omitempty"`
	Ports              []PortView                                       `json:"ports"`
	Members            networkingv1alpha.NetworkServiceSummary          `json:"members"`
	Locations          []networkingv1alpha.NetworkServiceLocationStatus `json:"locations,omitempty"`
	NotServing         []MemberView                                     `json:"notServing,omitempty"`
	WorkloadsInProject []string                                         `json:"workloadsInProject,omitempty"`
	RootCause          *Cause                                           `json:"rootCause,omitempty"`
	OtherCauses        []Cause                                          `json:"otherCauses,omitempty"`
	Unreadable         []string                                         `json:"unreadable,omitempty"`
}

const maxNotServing = 20

func diagnoseService(s *networkingv1alpha.NetworkService, e *evidence, now time.Time) *ServiceDiagnosis {
	d := &ServiceDiagnosis{
		Service:    s.Name,
		Ready:      apimeta.IsStatusConditionTrue(s.Status.Conditions, networkingv1alpha.NetworkServiceReady),
		Networks:   serviceNetworks(s, e.interfaces),
		Members:    s.Status.Summary,
		Locations:  s.Status.Locations,
		Unreadable: e.unreadable,
	}
	if sel, err := serviceSelector(s); err == nil {
		d.Selects = sel.String()
	}
	for _, p := range s.Spec.Ports {
		d.Ports = append(d.Ports, PortView{Name: p.Name, Port: p.Port, PortRange: p.PortRange, Protocol: string(p.Protocol),
			AppProtocol: string(ptr.Deref(p.AppProtocol, ""))})
	}

	causes := failingCauses(subject{
		object: fmt.Sprintf("network service %s", s.Name), kind: KindNetworkService, name: s.Name,
		created: s.CreationTimestamp, fallback: ScopeService,
	}, s.Status.Conditions, []string{networkingv1alpha.NetworkServiceMembersResolved, networkingv1alpha.NetworkServiceReady}, now)

	for _, m := range serviceMembers(s, e.interfaces) {
		c := apimeta.FindStatusCondition(m.Status.Conditions, networkingv1alpha.NetworkInterfaceHolderAvailable)
		if c != nil && c.Status == metav1.ConditionTrue {
			continue
		}
		mv := MemberView{Interface: m.Name, Workload: workloadOf(&m), Location: interfaceLocation(&m)}
		if c != nil {
			mv.Reason, mv.Message = c.Reason, c.Message
			if len(d.NotServing) == 0 && d.Ready {
				causes = append(causes, causeFrom(interfaceSubject(&m), *c, now))
			}
		}
		if len(d.NotServing) < maxNotServing {
			d.NotServing = append(d.NotServing, mv)
		}
	}

	if r := apimeta.FindStatusCondition(s.Status.Conditions, networkingv1alpha.NetworkServiceMembersResolved); r != nil &&
		r.Reason == networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces {
		d.WorkloadsInProject = workloadsOn(e.interfaces)
	}

	rank(causes)
	if len(causes) > 0 {
		d.RootCause = &causes[0]
		d.OtherCauses = causes[1:]
	}
	switch {
	case d.RootCause != nil:
		d.Confidence = ConfidenceReported
	case len(d.Unreadable) > 0:
		d.Confidence = ConfidencePartial
	default:
		d.Confidence = ConfidenceClean
	}
	d.Summary = serviceSummary(d)
	return d
}

func serviceSummary(d *ServiceDiagnosis) string {
	if c := d.RootCause; c != nil {
		s := fmt.Sprintf("%s (%s on %s", c.Explanation, c.Reason, c.Object)
		if c.InStateFor != "" {
			s += ", for " + c.InStateFor
		}
		s += ")."
		if len(d.WorkloadsInProject) > 0 {
			s += fmt.Sprintf(" It selects %s; workloads in this project: %s.", d.Selects, strings.Join(d.WorkloadsInProject, ", "))
		}
		return s
	}
	if d.Confidence == ConfidencePartial {
		return fmt.Sprintf("Nothing reported a fault on network service %s, but some evidence could not be read. "+
			"See unreadable.", d.Service)
	}
	return fmt.Sprintf("Network service %s is serving: %d of %d instance(s) ready across %d location(s).",
		d.Service, d.Members.Healthy, d.Members.Members, d.Members.Locations)
}
