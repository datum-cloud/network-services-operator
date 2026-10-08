// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// fakeLogs serves access-log entries from memory, and counts calls so a test
// can assert that something did NOT read logs.
type fakeLogs struct {
	entries []util.LogEntry
	err     error
	calls   int
	lastQ   ALBLogQuery
}

func (f *fakeLogs) QueryALBLogs(_ context.Context, q ALBLogQuery) ([]util.LogEntry, error) {
	f.calls++
	f.lastQ = q
	return f.entries, f.err
}

var _ LogReader = (*fakeLogs)(nil)

func line(ago time.Duration, code, method, host, flags string) util.LogEntry {
	return util.LogEntry{
		Time: testNow.Add(-ago),
		Labels: map[string]string{
			"response_code": code, "method": method, "host": host,
			"response_flags": flags, "path": "/", "upstream_host": "10.0.0.1:8080",
		},
	}
}

func depsWithLogs(r Reader, l LogReader) DepsFor {
	return func(context.Context) (ToolDeps, error) {
		return ToolDeps{Reader: r, Namespace: "default", Logs: l}, nil
	}
}

// TestTrafficSummaryCountsBeforeExamples pins the shape of the answer. A wall
// of log lines buries the finding; counts orient the reader first.
func TestTrafficSummaryCountsBeforeExamples(t *testing.T) {
	pinClock(t)
	logs := &fakeLogs{entries: []util.LogEntry{
		line(1*time.Minute, "200", "GET", "app.example.com", ""),
		line(2*time.Minute, "200", "GET", "app.example.com", ""),
		line(3*time.Minute, "502", "GET", "app.example.com", "UF"),
		line(4*time.Minute, "403", "POST", "other.example.com", ""),
	}}

	_, out, err := albTrafficSummary(depsWithLogs(&fakeReader{}, logs))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app"})
	require.NoError(t, err)

	s := out.Summary
	assert.True(t, s.Available)
	assert.Equal(t, 4, s.Total)
	assert.Equal(t, map[string]int{"200": 2, "502": 1, "403": 1}, s.ByCode)
	assert.Equal(t, map[string]int{"UF": 1}, s.ResponseFlags,
		"the edge's own verdict is the most useful field when a code alone does not explain it")
	assert.Equal(t, []string{"app.example.com", "other.example.com"}, s.Hosts)
	assert.NotEmpty(t, s.Sample)
	assert.Empty(t, s.Note, "there was traffic, so the no-traffic caveat does not apply")
}

// TestNoTrafficIsNotAFault is the reading that is always wrong to make unaided:
// a load balancer nobody has called is indistinguishable from a broken one.
func TestNoTrafficIsNotAFault(t *testing.T) {
	pinClock(t)
	_, out, err := albTrafficSummary(depsWithLogs(&fakeReader{}, &fakeLogs{}))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app"})
	require.NoError(t, err)

	assert.True(t, out.Summary.Available, "looking succeeded; it just found nothing")
	assert.Zero(t, out.Summary.Total)
	assert.Contains(t, out.Summary.Note, "not a fault")
	assert.Contains(t, out.Summary.Note, "no request arrived")
}

// TestLogsUnavailableIsAnAnswerNotAnError pins the difference between a project
// that cannot show logs and a failure to look. The first must not surface as a
// tool error, or the assistant reports a broken load balancer.
func TestLogsUnavailableIsAnAnswerNotAnError(t *testing.T) {
	pinClock(t)
	logs := &fakeLogs{err: fmt.Errorf("%w: access logs are not available in this project yet", errLogsUnavailable)}

	_, out, err := albTrafficSummary(depsWithLogs(&fakeReader{}, logs))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app"})
	require.NoError(t, err, "not being entitled to logs is not a fault of the load balancer")
	assert.False(t, out.Summary.Available)
	assert.Contains(t, out.Summary.UnavailableReason, "not available")

	// And with no log reader wired at all.
	_, out2, err := albTrafficSummary(depsFor(&fakeReader{}))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app"})
	require.NoError(t, err)
	assert.False(t, out2.Summary.Available)
}

// TestDiagnoseNeverReadsLogs is the isolation guarantee. If diagnosis reached
// for logs, a logs outage or a project without the entitlement would make every
// load balancer undiagnosable — turning a missing extra into a total failure.
func TestDiagnoseNeverReadsLogs(t *testing.T) {
	pinClock(t)
	logs := &fakeLogs{entries: []util.LogEntry{line(time.Minute, "200", "GET", "app.example.com", "")}}

	reader := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{healthyProxy()}}
	d, err := DiagnoseAt(context.Background(), reader, "default", "my-app", testNow)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Zero(t, logs.calls, "diagnosis must not depend on a second API")
}

// TestTrafficWindowAndSampleAreBounded keeps one tool call from becoming an
// unbounded read.
func TestTrafficWindowAndSampleAreBounded(t *testing.T) {
	pinClock(t)
	logs := &fakeLogs{}

	_, _, err := albTrafficSummary(depsWithLogs(&fakeReader{}, logs))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app", Since: "720h", SampleLimit: 5000})
	require.NoError(t, err)
	assert.Equal(t, maxTrafficWindow, logs.lastQ.Since, "an unbounded window is clamped")
	assert.Equal(t, util.MaxLogsLimit, logs.lastQ.Limit)

	_, _, err = albTrafficSummary(depsWithLogs(&fakeReader{}, logs))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app", Since: "nonsense"})
	assert.Error(t, err, "an unparseable window is a usage error, not a silent default")
}

// TestTrafficFiltersByHostClientSide pins why host filtering happens here: the
// label is synthesised from whichever header carried the name, so the logs API
// cannot filter on it.
func TestTrafficFiltersByHostClientSide(t *testing.T) {
	pinClock(t)
	logs := &fakeLogs{entries: []util.LogEntry{
		line(1*time.Minute, "200", "GET", "app.example.com", ""),
		line(2*time.Minute, "200", "GET", "other.example.com", ""),
	}}

	_, out, err := albTrafficSummary(depsWithLogs(&fakeReader{}, logs))(
		context.Background(), nil, TrafficSummaryInput{Name: "my-app", Host: []string{"app.example.com"}})
	require.NoError(t, err)
	assert.Equal(t, 1, out.Summary.Total)
	assert.Equal(t, []string{"app.example.com"}, out.Summary.Hosts)
	assert.Empty(t, logs.lastQ.Methods, "host is not sent to the logs API")
}
