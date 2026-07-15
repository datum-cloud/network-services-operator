package parity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func exp(keys map[Family][]string) Expected {
	e := Expected{BuildID: 1, Keys: keys, Counts: map[Family]int{}}
	for f, k := range keys {
		e.Counts[f] = len(k)
	}
	return e
}

func actual(keys map[Family][]string) Actual {
	return Actual{Keys: keys}
}

func TestCompare_OK(t *testing.T) {
	keys := map[Family][]string{
		FamilyWAFRoute:         {"rc##vh##fwd##ns/tpp/Observe"},
		FamilyConnectorCluster: {"httproute/ns/proxy/rule/0"},
	}
	rep := Compare(exp(keys), actual(keys))
	assert.True(t, rep.OK(), rep.String())
}

// TestCompare_WAFDisabledIsNotMissing covers the case where the firewall is
// turned off: the extension server intended no firewall changes and the proxy
// has none. That must pass — the comparison asks whether what was intended is
// present, and nothing was intended.
func TestCompare_WAFDisabledIsNotMissing(t *testing.T) {
	want := map[Family][]string{
		FamilyConnectorCluster: {"httproute/ns/proxy/rule/0"},
		FamilyLocalReply:       {"l##fc"},
	}
	got := map[Family][]string{
		FamilyConnectorCluster: {"httproute/ns/proxy/rule/0"},
		FamilyLocalReply:       {"l##fc"},
	}
	rep := Compare(exp(want), actual(got))
	assert.True(t, rep.OK(), rep.String())
	assert.Equal(t, ClassOK, familyResult(t, rep, FamilyWAFRoute).Class, "zero-expected WAF route must be OK, not MISSING")
	assert.Equal(t, ClassOK, familyResult(t, rep, FamilyWAFHCM).Class, "zero-expected WAF HCM must be OK, not MISSING")
	assert.Equal(t, ClassOK, familyResult(t, rep, FamilyConnectorCluster).Class)
	assert.Equal(t, ClassOK, familyResult(t, rep, FamilyLocalReply).Class)
}

func TestCompare_Missing(t *testing.T) {
	want := map[Family][]string{FamilyWAFRoute: {"rc##vh##fwd##ns/tpp/Observe"}}
	rep := Compare(exp(want), actual(nil))
	r := familyResult(t, rep, FamilyWAFRoute)
	assert.Equal(t, ClassMissing, r.Class)
	assert.Equal(t, want[FamilyWAFRoute], r.MissingKeys)
	assert.False(t, rep.OK())
}

func TestCompare_CountMismatch(t *testing.T) {
	want := map[Family][]string{FamilyWAFRoute: {"a", "b", "c"}}
	got := map[Family][]string{FamilyWAFRoute: {"a", "b"}}
	rep := Compare(exp(want), actual(got))
	r := familyResult(t, rep, FamilyWAFRoute)
	assert.Equal(t, ClassCountMismatch, r.Class)
	assert.Equal(t, []string{"c"}, r.MissingKeys)
}

// TestCompare_WrongKeyed covers the case only this comparison can catch: the
// right number of changes, but applied to the wrong place.
func TestCompare_WrongKeyed(t *testing.T) {
	want := map[Family][]string{FamilyWAFRoute: {"rc##vh##fwd##nsA/tpp/Observe"}}
	got := map[Family][]string{FamilyWAFRoute: {"rc##vh##fwd##nsB/tpp/Observe"}}
	rep := Compare(exp(want), actual(got))
	r := familyResult(t, rep, FamilyWAFRoute)
	assert.Equal(t, ClassWrongKeyed, r.Class)
	assert.Equal(t, r.ExpectedCount, r.ActualCount, "counts equal — count-only gate would pass")
	assert.Equal(t, []string{"rc##vh##fwd##nsA/tpp/Observe"}, r.MissingKeys)
	assert.Equal(t, []string{"rc##vh##fwd##nsB/tpp/Observe"}, r.UnexpectedKeys)
}

func TestCompare_NACK(t *testing.T) {
	a := actual(nil)
	a.XDSRejected = map[string]int{"lds": 1, "rds": 0}
	rep := Compare(exp(nil), a)
	assert.False(t, rep.OK())
	require.Contains(t, rep.XDSRejections, "lds")
	assert.NotContains(t, rep.XDSRejections, "rds", "zero-delta types must not be reported")
}

func TestCompare_CeilingTruncation(t *testing.T) {
	a := actual(nil)
	a.ResourceExhausted = 3
	rep := Compare(exp(nil), a)
	assert.False(t, rep.OK())
	assert.Equal(t, 3, rep.ResourceExhausted)
	classes := map[FailureClass]bool{}
	for _, f := range rep.Failures() {
		classes[f.Class] = true
	}
	assert.True(t, classes[ClassCeilingTruncation])
}

func TestCompareTLSPrune_NegativeAssertion(t *testing.T) {
	e := Expected{BuildID: 1, Keys: map[Family][]string{}, Counts: map[Family]int{}, TLSPrunedSecrets: 2}

	// Pass: no pruned secret remains in the dump.
	rep := Compare(e, Actual{Keys: map[Family][]string{}})
	assert.Equal(t, ClassOK, familyResult(t, rep, FamilyTLSPrune).Class)

	// Fail: a pruned secret is still present.
	a := Actual{Keys: map[Family][]string{}, TLSDroppedSecretsStillPresent: []string{"bad-cert"}}
	rep2 := Compare(e, a)
	r := familyResult(t, rep2, FamilyTLSPrune)
	assert.Equal(t, ClassMissing, r.Class)
	assert.Equal(t, []string{"bad-cert"}, r.UnexpectedKeys)
}

func familyResult(t *testing.T, rep ParityReport, fam Family) FamilyResult {
	t.Helper()
	for _, f := range rep.Families {
		if f.Family == fam {
			return f
		}
	}
	t.Fatalf("family %s not in report", fam)
	return FamilyResult{}
}

// --- stats scrape ---

func TestScrapeXDSRejected(t *testing.T) {
	stats := []byte(`
# HELP envoy_listener_manager_lds_update_rejected
envoy_listener_manager_lds_update_rejected 2
envoy_cluster_manager_cds_update_rejected 0
envoy_http_foo_rds_bar_update_rejected 1
envoy_listener_manager_lds_update_success 40
`)
	got := scrapeXDSRejected(stats)
	assert.Equal(t, 2, got["lds"])
	assert.Equal(t, 1, got["rds"])
	_, hasCDS := got["cds"]
	assert.False(t, hasCDS, "zero-value counters must be skipped")
}

func TestScrapeResourceExhausted(t *testing.T) {
	metrics := []byte(`
grpc_server_handled_total{grpc_code="OK",grpc_method="PostTranslateModify"} 100
grpc_server_handled_total{grpc_code="ResourceExhausted",grpc_method="PostTranslateModify"} 4
`)
	assert.Equal(t, 4, scrapeResourceExhausted(metrics))
}
