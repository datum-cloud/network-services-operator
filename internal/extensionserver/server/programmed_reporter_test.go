package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
)

func TestProgrammedReporter_ReportsOnlyTheNewestBuild(t *testing.T) {
	reports := make(chan mutate.BuiltTPPs, 4)
	r := newProgrammedReporter(func(_ context.Context, built mutate.BuiltTPPs) { reports <- built })

	// Two builds return before the reporter runs: only the newer is reported.
	r.submit(mutate.BuiltTPPs{"ns/old": {Generation: 1}})
	r.submit(mutate.BuiltTPPs{"ns/new": {Generation: 2}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.run(ctx) }()

	select {
	case got := <-reports:
		assert.Contains(t, got, "ns/new")
		assert.NotContains(t, got, "ns/old")
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
	}
	select {
	case extra := <-reports:
		t.Fatalf("a second report for one wake-up: %v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	require.NoError(t, <-done)
}

func TestProgrammedReporter_ReportsAnEmptyBuild(t *testing.T) {
	// A build with no policy in it still rewrites every policy's report.
	reports := make(chan mutate.BuiltTPPs, 1)
	r := newProgrammedReporter(func(_ context.Context, built mutate.BuiltTPPs) { reports <- built })
	r.submit(mutate.BuiltTPPs{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.run(ctx) }()

	select {
	case got := <-reports:
		assert.Empty(t, got)
	case <-time.After(5 * time.Second):
		t.Fatal("an empty build was not reported")
	}
}

func TestProgrammedReporter_AFactChangeNeverReplacesAWaitingBuild(t *testing.T) {
	r := newProgrammedReporter(func(context.Context, mutate.BuiltTPPs) {})
	r.submit(mutate.BuiltTPPs{"ns/p": {Generation: 3}})
	r.recheckFacts()

	got, ok := r.take()
	require.True(t, ok)
	assert.Contains(t, got, "ns/p", "the waiting build is reported, and its report rechecks the facts too")

	r.recheckFacts()
	got, ok = r.take()
	require.True(t, ok)
	assert.Empty(t, got, "a fact change alone reports with no build: removals only")

	_, ok = r.take()
	assert.False(t, ok, "nothing waits")
}
