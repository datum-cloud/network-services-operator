// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"regexp"
	"strings"
	"testing"
)

// bannedTerm is a word that must not reach a customer, with the argument for
// banning it. The argument is recorded so the list can be disputed rather than
// guessed at: every entry here is a claim that the word exists only inside
// Datum's implementation.
type bannedTerm struct {
	pattern *regexp.Regexp
	why     string
}

func ban(expr, why string) bannedTerm {
	return bannedTerm{pattern: regexp.MustCompile(`(?i)` + expr), why: why}
}

// internalVocabulary is banned everywhere a customer reads: catalog copy,
// assembled diagnosis prose, the knowledge document and the skills.
//
// The test for a word is whether the customer WRITES it in something they
// author, or READS it in output they already see. "hostname", "certificate",
// "origin" and "paranoia" all pass that test. The words below do not: they
// appear only inside how Datum is built.
var internalVocabulary = []bannedTerm{
	ban(`\bgateways?\b`, "the object an HTTPProxy compiles into. The customer never names one: the WAF policy's target is derived from the load balancer's own name, and neither the CLI nor the portal ever shows it. Allowed only as a quoted API identifier inside a manifest fragment"),
	ban(`\bhttpproxy\b`, "the stored kind. The product is an Application Load Balancer"),
	ban(`\btrafficprotectionpolic`, "the stored kind behind traffic protection. Say traffic protection, or WAF"),
	ban(`\bsecuritypolic`, "the Envoy Gateway object behind basic auth. The customer turned on a username and password"),
	ban(`\bhttproutes?\b`, "machinery an HTTPProxy compiles into"),
	ban(`\bendpointslices?\b`, "machinery an HTTPProxy compiles into"),
	ban(`\bcert-?manager\b`, "how a certificate is obtained. They want to know theirs is not issued yet"),
	ban(`\bdownstream\b`, "this operator's word for the cell-side cluster. Note upstream_host is an access-log label a customer reads, so bare 'upstream' is NOT banned"),
	ban(`\bcells?\b`, "an internal unit of infrastructure, absent from every public doc"),
	ban(`\bkarmada\b`, "an internal component name"),
	ban(`\bgalactic\b`, "an internal component name"),
	ban(`\bmulticluster\b`, "how the operator is built"),
	ban(`\breconcil`, "controller-loop vocabulary"),
	ban(`\bthe operator\b`, "controller vocabulary. Bare 'operator' is not banned: 'this needs an operator' is the right sentence for a platform fault"),
	ban(`\bancestors?\b`, "status.ancestors[] is where WAF status lives. Gateway API policy-status shape, meaningless to a customer"),
	ban(`\bmaterialis|\bmaterializ`, "implementation vocabulary for a delivery step. Say it arrived, or has not"),
	ban(`\bwebhooks?\b`, "admission machinery that runs before a change is stored; invisible to whoever made the change"),
	ban(`\brbac\b`, "how Datum decides who may do what internally; a customer reads 'you do not have permission'"),
	ban(`\bcanonical hostname\b`, "the API field name. The product word is 'generated hostname'"),
	ban(`\benvoy\b`, "the proxy implementation. Response-flag values like UF are Envoy's and survive as identifiers; the word does not"),
	ban(`\bcoraza\b`, "the WAF engine. Named in public docs, but a customer reading 'Coraza rejected it' learns nothing actionable"),
	ban(`\bacme\b`, "the certificate-issuance protocol. The sentence a customer reads says the certificate has not been issued yet"),
}

// customerFacingOnly is banned in catalog and diagnosis copy, but allowed in
// the skills and the knowledge document, which address the assistant rather
// than the customer.
var customerFacingOnly = []bannedTerm{
	ban(`\bconditions?\b`, "the customer sees a site that does not load, not a condition. The condition type still travels alongside as evidence"),
	ban(`\bprogramm(ed|ing)\b`, "the platform's word for configuration reaching the edge. Reason strings survive as identifiers; the prose says what it means"),
}

// apiIdentifiers are the strings that must survive the plain English because
// they are what a customer escalates with, or types into a DNS provider. They
// are stripped before scanning so that a banned word inside an identifier does
// not read as prose.
func apiIdentifiers() []string {
	var out []string
	for _, info := range AllReasons() {
		out = append(out, info.Reason, info.ConditionType)
	}
	return append(out,
		"status.canonicalHostname", "status.hostnameStatuses", "spec.hostnames",
		"kind: Gateway", "gateway.networking.k8s.io",
		SkillNotServing, SkillHostnameNotWorking, SkillDomainVerification,
		SkillDNSDelegation, SkillCertificateNotIssued, SkillBackendNotReachable,
		SkillEdgePropagation, SkillProtectionTriage, SkillAccessLogTriage, SkillCreate,
	)
}

// prose strips the identifiers a reader needs verbatim, leaving the sentences
// that were written for them.
func prose(s string, extra ...string) string {
	for _, id := range append(apiIdentifiers(), extra...) {
		if id != "" {
			s = strings.ReplaceAll(s, id, " ")
		}
	}
	return s
}

func scan(t *testing.T, where, text string, lists ...[]bannedTerm) {
	t.Helper()
	p := prose(text)
	for _, list := range lists {
		for _, term := range list {
			if m := term.pattern.FindString(p); m != "" {
				t.Errorf("%s uses %q, which a customer never writes or reads: %s\n  in: %s",
					where, m, term.why, text)
			}
		}
	}
}

// TestCatalogCopyUsesNoInternalVocabulary is the gate. This text reaches a
// paying customer close to verbatim, and that customer runs a website — they do
// not operate Datum.
func TestCatalogCopyUsesNoInternalVocabulary(t *testing.T) {
	for _, info := range AllReasons() {
		where := info.Reason + " on " + info.ConditionType
		scan(t, where+" (explanation)", info.Explanation, internalVocabulary, customerFacingOnly)
		scan(t, where+" (remediation)", info.Remediation, internalVocabulary, customerFacingOnly)
	}
}

// TestPlainLanguageKeepsTheEvidence is the counterweight. Plain English that
// drops the identifiers leaves a customer unable to escalate, and for an ALB
// the hostname matters most: every aggregate condition here reports "one or
// more hostnames" and names none, so an answer that also omits it sends them
// back to the console to find out which.
func TestPlainLanguageKeepsTheEvidence(t *testing.T) {
	hostnameCases := []struct{ conditionType, reason string }{
		{"Available", "InUse"},
		{"DNSRecordProgrammed", "DomainNotVerified"},
		{"CertificateReady", "ProvisioningFailed"},
	}
	for _, tc := range hostnameCases {
		info, ok := ExplainReason(tc.conditionType, tc.reason)
		if !ok {
			t.Fatalf("%s on %s is not catalogued", tc.reason, tc.conditionType)
		}
		text := strings.ToLower(info.Explanation + " " + info.Remediation)
		if !strings.Contains(text, "hostname") {
			t.Errorf("%s on %s never says which hostname is affected: %q",
				tc.reason, tc.conditionType, info.Explanation)
		}
	}
}

// TestBanArgumentsAreRecorded keeps the denylist disputable. A banned word with
// no argument behind it is a rule nobody can push back on.
func TestBanArgumentsAreRecorded(t *testing.T) {
	for _, list := range [][]bannedTerm{internalVocabulary, customerFacingOnly} {
		for _, term := range list {
			if len(term.why) < 20 {
				t.Errorf("%s is banned without an argument for banning it", term.pattern)
			}
		}
	}
}
