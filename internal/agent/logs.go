// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// LogReader fetches a load balancer's access logs.
//
// It is separate from Reader on purpose. Logs come from a different API to the
// rest of this package, and a project entitled to load balancers is not
// necessarily entitled to observability — so a caller who can read every load
// balancer here may still be unable to read a single log line. Keeping the two
// apart lets that be reported as a capability the project lacks rather than as
// a failure of the load balancer.
type LogReader interface {
	// QueryALBLogs returns access-log entries for one load balancer, newest
	// first. It returns errLogsUnavailable when the project does not serve the
	// logs API or the caller may not read it.
	QueryALBLogs(ctx context.Context, q ALBLogQuery) ([]util.LogEntry, error)
}

// ALBLogQuery is one access-log request.
type ALBLogQuery struct {
	ProxyName string
	Since     time.Duration
	Limit     int
	Methods   []string
	Codes     []string
}

// errLogsUnavailable marks the difference between "this project cannot show you
// logs" and "looking failed". The first is an answer; the second is a fault.
var errLogsUnavailable = errors.New("access logs are not available")

// ClientLogReader implements LogReader against the project's logs API, using
// whatever credentials cfg carries — which, in the server, is the caller's own.
type ClientLogReader struct {
	Config *rest.Config
}

var _ LogReader = (*ClientLogReader)(nil)

// NewClientLogReader returns a LogReader that reads as the bearer of cfg.
func NewClientLogReader(cfg *rest.Config) *ClientLogReader {
	return &ClientLogReader{Config: cfg}
}

func (r *ClientLogReader) QueryALBLogs(ctx context.Context, q ALBLogQuery) ([]util.LogEntry, error) {
	end := time.Now()
	entries, err := util.QueryLogs(ctx, r.Config, util.LogsQuery{
		Query: spec.BuildAlbLogQL(q.ProxyName, q.Methods, q.Codes),
		Start: end.Add(-q.Since),
		End:   end,
		Limit: q.Limit,
	})
	if err != nil {
		// The logs API answers 403 when the caller may not read it and 404 when
		// the project does not serve it at all. Both mean "no logs for you
		// here", which the tool reports rather than raising.
		var cliErr *util.CLIError
		if errors.As(err, &cliErr) &&
			(cliErr.Code() == util.ExitForbidden || cliErr.Code() == util.ExitUnavailable) {
			return nil, fmt.Errorf("%w: %s", errLogsUnavailable, plainMessage(err))
		}
		return nil, err
	}
	return entries, nil
}

// plainMessage strips the CLI's exit decoration from an error so the text can
// be handed to a model without telling it to run a command it does not have.
func plainMessage(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i >= 0 {
		msg = msg[:i]
	}
	return strings.TrimSpace(msg)
}

// TrafficSummary is what a load balancer actually served, as opposed to what it
// is configured to serve.
type TrafficSummary struct {
	// Available is false when this project cannot show access logs at all. That
	// is a property of the project, not a fault of the load balancer.
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`

	LoadBalancer string `json:"loadBalancer"`
	WindowStart  string `json:"windowStart,omitempty"`
	WindowEnd    string `json:"windowEnd,omitempty"`

	Total int `json:"total"`
	// ByCode counts responses per status code, so a wall of 502s reads at a
	// glance.
	ByCode map[string]int `json:"byCode,omitempty"`
	// ResponseFlags counts the edge's own verdict per request. These are the
	// most useful field when a status code alone does not explain the failure.
	ResponseFlags map[string]int `json:"responseFlags,omitempty"`
	Hosts         []string       `json:"hosts,omitempty"`
	FirstSeen     string         `json:"firstSeen,omitempty"`
	LastSeen      string         `json:"lastSeen,omitempty"`

	Sample []TrafficLine `json:"sample,omitempty"`

	// Note carries the one reading that is always wrong to make unaided.
	Note string `json:"note,omitempty"`
}

// TrafficLine is one request, reduced to the fields worth spending tokens on.
type TrafficLine struct {
	Time          string `json:"time"`
	Method        string `json:"method,omitempty"`
	Path          string `json:"path,omitempty"`
	Code          string `json:"code,omitempty"`
	Host          string `json:"host,omitempty"`
	Duration      string `json:"duration,omitempty"`
	ResponseFlags string `json:"responseFlags,omitempty"`
	UpstreamHost  string `json:"upstreamHost,omitempty"`
}

// summariseTraffic aggregates entries into counts first and examples second.
// The consumer is a language model: counts orient it, and a wall of log lines
// buries the finding it was asked for.
func summariseTraffic(name string, entries []util.LogEntry, hosts []string, sampleLimit int, window time.Duration) TrafficSummary {
	s := TrafficSummary{Available: true, LoadBalancer: name}

	if len(hosts) > 0 {
		entries = filterByHost(entries, hosts)
	}

	s.Total = len(entries)
	if s.Total == 0 {
		s.Note = "Nothing reached this load balancer in the window looked at. That is not a " +
			"fault: a load balancer nobody has called looks exactly like one that is broken. " +
			"It means no request arrived, not that a request would fail."
		return s
	}

	byCode := map[string]int{}
	flags := map[string]int{}
	seenHosts := map[string]bool{}
	var first, last time.Time

	for _, e := range entries {
		if code := e.Labels["response_code"]; code != "" {
			byCode[code]++
		}
		if f := e.Labels["response_flags"]; f != "" && f != "-" {
			flags[f]++
		}
		if h := e.Labels["host"]; h != "" {
			seenHosts[h] = true
		}
		if first.IsZero() || e.Time.Before(first) {
			first = e.Time
		}
		if last.IsZero() || e.Time.After(last) {
			last = e.Time
		}
	}

	s.ByCode = byCode
	if len(flags) > 0 {
		s.ResponseFlags = flags
	}
	for h := range seenHosts {
		s.Hosts = append(s.Hosts, h)
	}
	sort.Strings(s.Hosts)

	if !first.IsZero() {
		s.FirstSeen = first.UTC().Format(time.RFC3339)
		s.WindowStart = last.Add(-window).UTC().Format(time.RFC3339)
	}
	if !last.IsZero() {
		s.LastSeen = last.UTC().Format(time.RFC3339)
		s.WindowEnd = last.UTC().Format(time.RFC3339)
	}

	if sampleLimit > 0 {
		for i, e := range entries {
			if i >= sampleLimit {
				break
			}
			s.Sample = append(s.Sample, TrafficLine{
				Time:          e.Time.UTC().Format(time.RFC3339),
				Method:        e.Labels["method"],
				Path:          e.Labels["path"],
				Code:          e.Labels["response_code"],
				Host:          e.Labels["host"],
				Duration:      e.Labels["duration"],
				ResponseFlags: e.Labels["response_flags"],
				UpstreamHost:  e.Labels["upstream_host"],
			})
		}
	}
	return s
}

// filterByHost narrows to the hostnames asked for. The logs API cannot filter
// on this, because the label is synthesised from whichever of several headers
// carried the name, so it happens here.
func filterByHost(entries []util.LogEntry, hosts []string) []util.LogEntry {
	want := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		want[strings.ToLower(strings.TrimSpace(h))] = true
	}
	out := entries[:0:0]
	for _, e := range entries {
		if want[strings.ToLower(e.Labels["host"])] {
			out = append(out, e)
		}
	}
	return out
}
