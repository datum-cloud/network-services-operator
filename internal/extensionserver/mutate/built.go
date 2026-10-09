package mutate

import (
	"sigs.k8s.io/gateway-api/apis/v1alpha2"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// BuiltTPP is one TrafficProtectionPolicy as a build contains it: the
// generation the build read, and each of the policy's targets that the build
// contains.
type BuiltTPP struct {
	Generation int64
	Targets    []v1alpha2.LocalPolicyTargetReferenceWithSectionName
}

// BuiltTPPs records, for one build, every TrafficProtectionPolicy that has at
// least one target in it, keyed "namespace/name".
//
// A valid policy is in the build when its target is, whether or not it
// changes a route: one that a route-level policy overrides on every route is
// built correctly by having no effect. A policy without directives is never in
// the build: its paranoia levels are inverted, so the extension server refuses
// to build it (see computeCorazaDirectives) and the project's controller marks
// it Invalid. A target is in the build when EG translated it: a Gateway when
// one of its virtual hosts is present, an HTTPRoute when one of its routes is,
// and a rule of an HTTPRoute when a route resolves to that rule.
type BuiltTPPs map[string]*BuiltTPP

// recordGateway records every policy in tpps that targets the Gateway named
// gatewayName. A Gateway target's sectionName is not resolved, as the WAF does
// not resolve it either (see findGatewayTPP).
func (b BuiltTPPs) recordGateway(tpps []extcache.TPPInfo, gatewayName string) {
	b.record(tpps, func(ref v1alpha2.LocalPolicyTargetReferenceWithSectionName) bool {
		return string(ref.Kind) == kindGateway && string(ref.Name) == gatewayName
	})
}

// recordRoute records every policy in tpps that targets the HTTPRoute named
// routeName as a whole, or the rule of it named ruleName.
func (b BuiltTPPs) recordRoute(tpps []extcache.TPPInfo, routeName, ruleName string) {
	if routeName == "" {
		return
	}
	b.record(tpps, func(ref v1alpha2.LocalPolicyTargetReferenceWithSectionName) bool {
		if string(ref.Kind) != kindHTTPRoute || string(ref.Name) != routeName {
			return false
		}
		if ref.SectionName == nil || *ref.SectionName == "" {
			return true
		}
		return ruleName != "" && string(*ref.SectionName) == ruleName
	})
}

func (b BuiltTPPs) record(tpps []extcache.TPPInfo, inBuild func(v1alpha2.LocalPolicyTargetReferenceWithSectionName) bool) {
	if b == nil {
		return
	}
	for i := range tpps {
		if len(tpps[i].Directives) == 0 {
			continue
		}
		for _, ref := range tpps[i].TargetRefs {
			if inBuild(ref) {
				b.add(&tpps[i], ref)
			}
		}
	}
}

func (b BuiltTPPs) add(tpp *extcache.TPPInfo, ref v1alpha2.LocalPolicyTargetReferenceWithSectionName) {
	key := tpp.Namespace + "/" + tpp.Name
	entry := b[key]
	if entry == nil {
		entry = &BuiltTPP{Generation: tpp.Generation}
		b[key] = entry
	}
	for _, have := range entry.Targets {
		if sameTarget(have, ref) {
			return
		}
	}
	entry.Targets = append(entry.Targets, ref)
}

func sameTarget(a, b v1alpha2.LocalPolicyTargetReferenceWithSectionName) bool {
	if a.Group != b.Group || a.Kind != b.Kind || a.Name != b.Name {
		return false
	}
	return sectionName(a) == sectionName(b)
}

func sectionName(ref v1alpha2.LocalPolicyTargetReferenceWithSectionName) string {
	if ref.SectionName == nil {
		return ""
	}
	return string(*ref.SectionName)
}

// Has reports whether this build contains the target ref.
func (b *BuiltTPP) Has(ref v1alpha2.LocalPolicyTargetReferenceWithSectionName) bool {
	if b == nil {
		return false
	}
	for _, have := range b.Targets {
		if sameTarget(have, ref) {
			return true
		}
	}
	return false
}
