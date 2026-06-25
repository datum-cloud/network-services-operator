package parity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Fetcher retrieves a response body from an endpoint. It is an interface because
// different environments reach the proxy differently: some can make a plain HTTP
// request, others must tunnel into the proxy pod first.
type Fetcher interface {
	Fetch(ctx context.Context, path string) ([]byte, error)
}

// HTTPFetcher fetches over plain HTTP from a base URL, for endpoints reachable
// directly without tunneling into a pod.
type HTTPFetcher struct {
	BaseURL string // e.g. "http://127.0.0.1:19000"
	Client  *http.Client
}

func (h HTTPFetcher) Fetch(ctx context.Context, path string) ([]byte, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(h.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: status %d: %s", url, resp.StatusCode, snippet(body))
	}
	return body, nil
}

// FetchExpected reads the configuration the extension server says it intended to
// program, which is the reference we compare the live proxy against.
func FetchExpected(ctx context.Context, extFetcher Fetcher) (Expected, error) {
	body, err := extFetcher.Fetch(ctx, ProgrammedSetPath)
	if err != nil {
		return Expected{}, fmt.Errorf("fetch programmed-set: %w", err)
	}
	var exp Expected
	if err := json.Unmarshal(body, &exp); err != nil {
		return Expected{}, fmt.Errorf("decode programmed-set JSON: %w", err)
	}
	if exp.Keys == nil {
		exp.Keys = map[Family][]string{}
	}
	if exp.Counts == nil {
		exp.Counts = map[Family]int{}
	}
	return exp, nil
}

// ProgrammedSetPath is duplicated from the extension server rather than imported
// so this package stays free of any internal/ dependency; the two MUST stay in sync.
const ProgrammedSetPath = "/debug/programmed-set"

// ExpectedSource pairs a fetched Expected set with the replica it came from, so
// the caller can report which replica was authoritative.
type ExpectedSource struct {
	Replica  string
	Expected Expected
}

// FetchExpectedFromMany queries every extension server replica and returns the
// authoritative one. Only one replica actually builds configuration at a time,
// so querying a fixed replica can hit an idle one and falsely report missing or
// stale configuration.
//
// The replica with the highest build count wins, since that count only ever
// increases on the replica doing the work and idle replicas sit at zero. A tie
// (no replica has built yet) resolves to the first replica so the caller still
// gets a valid empty result instead of an error.
//
// An unreachable replica is skipped rather than fatal, as long as at least one
// replica answered; its error is returned for diagnosis.
func FetchExpectedFromMany(ctx context.Context, fetchers map[string]Fetcher) (ExpectedSource, map[string]error, error) {
	if len(fetchers) == 0 {
		return ExpectedSource{}, nil, fmt.Errorf("no ext-server replicas to query")
	}

	errs := map[string]error{}
	var best *ExpectedSource
	// Stable order so ties resolve deterministically.
	for _, replica := range sortedKeys(fetchers) {
		exp, err := FetchExpected(ctx, fetchers[replica])
		if err != nil {
			errs[replica] = err
			continue
		}
		src := ExpectedSource{Replica: replica, Expected: exp}
		if best == nil || exp.BuildID > best.Expected.BuildID {
			s := src
			best = &s
		}
	}

	if best == nil {
		return ExpectedSource{}, errs, fmt.Errorf("all %d ext-server replicas failed to answer", len(fetchers))
	}
	return *best, errs, nil
}

// sortedKeys returns the map keys sorted, for deterministic iteration.
func sortedKeys(m map[string]Fetcher) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// FetchActual reads the proxy's live configuration and scans it for the
// artifacts of each change family. corazaFilterName identifies the firewall
// filter; droppedSecrets names the TLS certificates that should have been
// removed, so we can confirm none are still present.
//
// adminFetcher reaches the proxy; extFetcher reaches the extension server to
// check whether any update was too large to deliver. Pass a nil extFetcher to
// skip that check.
func FetchActual(
	ctx context.Context,
	adminFetcher Fetcher,
	extFetcher Fetcher,
	corazaFilterName string,
	droppedSecrets []string,
) (Actual, *ConfigDump, error) {
	dumpRaw, err := adminFetcher.Fetch(ctx, "/config_dump")
	if err != nil {
		return Actual{}, nil, fmt.Errorf("fetch config_dump: %w", err)
	}
	dump, err := ParseConfigDump(dumpRaw)
	if err != nil {
		return Actual{}, nil, err
	}

	act := ScanActual(dump, corazaFilterName, droppedSecrets)

	// Counts of updates the proxy rejected.
	statsRaw, err := adminFetcher.Fetch(ctx, "/stats?format=prometheus")
	if err != nil {
		// Best-effort: the comparison still works without these counts, so a
		// failure here yields an empty map rather than aborting.
		statsRaw = nil
	}
	act.XDSRejected = scrapeXDSRejected(statsRaw)

	// Whether any update was too large to deliver.
	if extFetcher != nil {
		if metricsRaw, mErr := extFetcher.Fetch(ctx, "/metrics"); mErr == nil {
			act.ResourceExhausted = scrapeResourceExhausted(metricsRaw)
		}
	}
	return act, dump, nil
}

// snippet returns the first line of b for error messages.
func snippet(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// scrapeXDSRejected sums the proxy's rejected-update counters, grouped by the
// kind of configuration. Route counters are per-route so they are matched by
// substring rather than exact name.
func scrapeXDSRejected(stats []byte) map[string]int {
	out := map[string]int{}
	if len(stats) == 0 {
		return out
	}
	for line := range strings.SplitSeq(string(stats), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "update_rejected") {
			continue
		}
		name, val := splitMetricLine(line)
		if val == 0 {
			continue
		}
		fam := classifyXDS(name)
		out[fam] += val
	}
	return out
}

// classifyXDS groups a metric by the kind of configuration it counts. The
// proxy rewrites dots to underscores in metric names, so we match on substrings.
func classifyXDS(name string) string {
	switch {
	case strings.Contains(name, "lds"):
		return "lds"
	case strings.Contains(name, "cds"):
		return "cds"
	case strings.Contains(name, "rds"):
		return "rds"
	case strings.Contains(name, "sds"):
		return "sds"
	default:
		return "other"
	}
}

// scrapeResourceExhausted counts how often the extension server failed to
// deliver an update because it was too large. A non-zero result means
// configuration was truncated.
func scrapeResourceExhausted(metrics []byte) int {
	if len(metrics) == 0 {
		return 0
	}
	total := 0
	for line := range strings.SplitSeq(string(metrics), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "grpc_server_handled_total") {
			continue
		}
		if !strings.Contains(line, `grpc_code="ResourceExhausted"`) {
			continue
		}
		_, val := splitMetricLine(line)
		total += val
	}
	return total
}

// splitMetricLine parses one metrics line "name{labels} value" into the name
// (without labels) and the integer value (floats are truncated; non-numeric
// yields 0).
func splitMetricLine(line string) (name string, value int) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0
	}
	valStr := fields[len(fields)-1]
	nameWithLabels := strings.Join(fields[:len(fields)-1], " ")
	name, _, _ = strings.Cut(nameWithLabels, "{")
	if f, err := strconv.ParseFloat(valStr, 64); err == nil {
		value = int(f)
	}
	return name, value
}
