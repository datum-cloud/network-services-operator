// Package parity answers one question: is the edge proxy actually running the
// configuration the platform tried to give it? A successful-looking request can
// hide configuration that never applied, applied only partly, or applied in the
// wrong place — so we compare what was intended against what the proxy is live
// serving and name the gap, turning a surprising result into a cause instead of
// a guess.
package parity

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Family groups one kind of edge change so intended and live can be compared
// kind by kind. The values match the names the extension server reports, so its
// output decodes straight into these.
type Family string

const (
	FamilyWAFRoute         Family = "waf_route"
	FamilyWAFHCM           Family = "waf_hcm"
	FamilyLocalReply       Family = "local_reply"
	FamilyConnectorCluster Family = "connector_cluster"
	FamilyConnectorRoute   Family = "connector_route"
	FamilyConnectorOffline Family = "connector_offline"
	FamilyTLSPrune         Family = "tls_prune"
)

// AllFamilies is the canonical iteration order for reports.
var AllFamilies = []Family{
	FamilyWAFRoute, FamilyWAFHCM, FamilyLocalReply,
	FamilyConnectorCluster, FamilyConnectorRoute, FamilyConnectorOffline,
	FamilyTLSPrune,
}

// FailureClass names how a comparison differed. The values are stable because
// reports and logs are read by people and tooling outside this package.
type FailureClass string

const (
	ClassOK                FailureClass = "OK"
	ClassMissing           FailureClass = "MISSING"
	ClassCountMismatch     FailureClass = "COUNT_MISMATCH"
	ClassWrongKeyed        FailureClass = "WRONG_KEYED"
	ClassNACK              FailureClass = "NACK"
	ClassCeilingTruncation FailureClass = "CEILING_TRUNCATION"
)

// Expected is the set of changes the extension server reports it last applied.
type Expected struct {
	BuildID                uint64              `json:"buildID"`
	Keys                   map[Family][]string `json:"keys"`
	Counts                 map[Family]int      `json:"counts"`
	TLSPrunedChains        int                 `json:"tlsPrunedChains"`
	TLSPrunedSecrets       int                 `json:"tlsPrunedSecrets"`
	TLSListenersLeftIntact int                 `json:"tlsListenersLeftIntact"`
}

// Actual is what the edge proxy is really running, read back from its live
// configuration and its record of rejected or undelivered updates.
type Actual struct {
	// Keys must be formatted exactly as the extension server formats them, so the
	// two sides can be compared as sets.
	Keys map[Family][]string
	// Certificates the extension server says it removed that are still present on
	// the proxy — a removal that didn't take effect.
	TLSDroppedSecretsStillPresent []string
	// Updates the proxy rejected, by kind. Any rejection means the proxy kept
	// older configuration instead.
	XDSRejected map[string]int
	// Count of updates too large to deliver, which the proxy silently never sees.
	ResourceExhausted int
}

// FamilyResult is one family's comparison outcome.
type FamilyResult struct {
	Family        Family       `json:"family"`
	Class         FailureClass `json:"class"`
	ExpectedCount int          `json:"expectedCount"`
	ActualCount   int          `json:"actualCount"`
	// Expected but absent from the proxy.
	MissingKeys []string `json:"missingKeys,omitempty"`
	// Present on the proxy but not expected — the other side of a wrong-place match.
	UnexpectedKeys []string `json:"unexpectedKeys,omitempty"`
	// Plain-language explanation for differences that aren't about keys.
	Detail string `json:"detail,omitempty"`
}

// ParityReport is the full result of a comparison — never just pass/fail, so a
// reader can see which change differed, how, and the specific keys involved.
type ParityReport struct {
	ExpectedBuildID uint64         `json:"expectedBuildID"`
	Families        []FamilyResult `json:"families"`
	// Update kinds the proxy rejected.
	XDSRejections map[string]int `json:"xdsRejections,omitempty"`
	// Non-zero when updates were too large to deliver.
	ResourceExhausted int `json:"resourceExhausted,omitempty"`
}

// OK reports whether everything matched and the proxy accepted every update.
func (r ParityReport) OK() bool {
	if len(r.XDSRejections) > 0 || r.ResourceExhausted > 0 {
		return false
	}
	for _, f := range r.Families {
		if f.Class != ClassOK {
			return false
		}
	}
	return true
}

// Failures lists every problem — mismatched changes plus any rejected or
// undelivered updates — so a caller can show them all at once.
func (r ParityReport) Failures() []FamilyResult {
	var out []FamilyResult
	for _, f := range r.Families {
		if f.Class != ClassOK {
			out = append(out, f)
		}
	}
	for xds, n := range r.XDSRejections {
		out = append(out, FamilyResult{
			Class:  ClassNACK,
			Detail: fmt.Sprintf("xDS %s update_rejected=%d (Envoy rejected the snapshot)", xds, n),
		})
	}
	if r.ResourceExhausted > 0 {
		out = append(out, FamilyResult{
			Class:  ClassCeilingTruncation,
			Detail: fmt.Sprintf("ext-server gRPC ResourceExhausted=%d (message-ceiling truncation)", r.ResourceExhausted),
		})
	}
	return out
}

// AssertOK fails the test with the full report when the comparison did not pass.
func (r ParityReport) AssertOK(t testing.TB) {
	t.Helper()
	if !r.OK() {
		t.Errorf("config-dump parity gate FAILED:\n%s", r.String())
	}
}

// String renders a compact, readable summary.
func (r ParityReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "parity report (expected build %d): %s\n", r.ExpectedBuildID, statusWord(r.OK()))
	for _, f := range r.Families {
		fmt.Fprintf(&b, "  %-18s %-15s expected=%d actual=%d", f.Family, f.Class, f.ExpectedCount, f.ActualCount)
		if len(f.MissingKeys) > 0 {
			fmt.Fprintf(&b, " missing=%v", truncateKeys(f.MissingKeys))
		}
		if len(f.UnexpectedKeys) > 0 {
			fmt.Fprintf(&b, " unexpected=%v", truncateKeys(f.UnexpectedKeys))
		}
		if f.Detail != "" {
			fmt.Fprintf(&b, " (%s)", f.Detail)
		}
		b.WriteByte('\n')
	}
	for xds, n := range r.XDSRejections {
		fmt.Fprintf(&b, "  NACK: xDS %s update_rejected=%d\n", xds, n)
	}
	if r.ResourceExhausted > 0 {
		fmt.Fprintf(&b, "  CEILING_TRUNCATION: gRPC ResourceExhausted=%d\n", r.ResourceExhausted)
	}
	return b.String()
}

func statusWord(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// truncateKeys caps a key list for log readability.
func truncateKeys(keys []string) []string {
	const max = 5
	if len(keys) <= max {
		return keys
	}
	out := append([]string{}, keys[:max]...)
	return append(out, fmt.Sprintf("...(+%d more)", len(keys)-max))
}

// Compare diffs intended against live and classifies each kind of change. The
// key-by-key match is the important one: it is the only way to catch a change
// that quietly applied to the wrong place.
func Compare(exp Expected, act Actual) ParityReport {
	report := ParityReport{ExpectedBuildID: exp.BuildID}

	for _, fam := range AllFamilies {
		report.Families = append(report.Families, compareFamily(fam, exp, act))
	}

	// Record any updates the proxy rejected.
	for xds, n := range act.XDSRejected {
		if n > 0 {
			if report.XDSRejections == nil {
				report.XDSRejections = map[string]int{}
			}
			report.XDSRejections[xds] = n
		}
	}
	report.ResourceExhausted = act.ResourceExhausted
	return report
}

// compareFamily classifies one kind of change. Removed certificates are the
// exception: success there means the certificate is gone, so its "actual" is how
// many that should be absent are still present.
func compareFamily(fam Family, exp Expected, act Actual) FamilyResult {
	if fam == FamilyTLSPrune {
		return compareTLSPrune(exp, act)
	}

	expKeys := normalize(exp.Keys[fam])
	actKeys := normalize(act.Keys[fam])
	res := FamilyResult{
		Family:        fam,
		ExpectedCount: len(expKeys),
		ActualCount:   len(actKeys),
	}

	switch {
	case len(expKeys) == 0:
		// Nothing expected — any actual is not this gate's concern (the gate
		// asserts what the ext-server INTENDED is present, not the inverse).
		res.Class = ClassOK
	case len(actKeys) == 0:
		res.Class = ClassMissing
		res.MissingKeys = expKeys
		res.Detail = "expected mutations but the config_dump carries none (inert / never-applied)"
	case len(actKeys) < len(expKeys):
		res.Class = ClassCountMismatch
		res.MissingKeys = setDiff(expKeys, actKeys)
		res.Detail = "config_dump carries fewer than expected (truncated / partial publish)"
	default:
		missing := setDiff(expKeys, actKeys)
		if len(missing) == 0 {
			res.Class = ClassOK
		} else {
			// Right number of changes, but not the ones we expected — something
			// applied in the wrong place.
			res.Class = ClassWrongKeyed
			res.MissingKeys = missing
			res.UnexpectedKeys = setDiff(actKeys, expKeys)
			res.Detail = "expected key set not present in the config_dump (applied to wrong route/gateway)"
		}
	}
	return res
}

// compareTLSPrune checks the opposite of the others: a certificate the extension
// server says it removed must be gone from the proxy. One still present means the
// removal never took effect.
func compareTLSPrune(exp Expected, act Actual) FamilyResult {
	res := FamilyResult{
		Family:        FamilyTLSPrune,
		ExpectedCount: exp.TLSPrunedSecrets,
		ActualCount:   len(act.TLSDroppedSecretsStillPresent),
	}
	if exp.TLSPrunedSecrets == 0 {
		res.Class = ClassOK
		return res
	}
	if len(act.TLSDroppedSecretsStillPresent) > 0 {
		res.Class = ClassMissing // a removal that didn't take effect
		res.UnexpectedKeys = normalize(act.TLSDroppedSecretsStillPresent)
		res.Detail = "pruned TLS secret(s) still present in the config_dump (prune did not take effect)"
		return res
	}
	res.Class = ClassOK
	return res
}

// normalize returns a sorted, de-duplicated copy of keys (nil-safe).
func normalize(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setDiff returns the elements of a not present in b. Both inputs must be
// normalized (sorted, deduped).
func setDiff(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, x := range b {
		bset[x] = struct{}{}
	}
	var out []string
	for _, x := range a {
		if _, ok := bset[x]; !ok {
			out = append(out, x)
		}
	}
	return out
}
