package server

import (
	"context"
	"sync"

	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
)

// programmedReporter writes the edge's Programmed report outside the hook
// call, so a slow API server never delays a build. It reports after each build
// the hook returns, and after a change to a fact the report's removals depend
// on. Builds that arrive while a report is being written collapse into the
// newest; that is safe because a report only advances or keeps claims for
// what a build lacks. A fact change never replaces a waiting build.
type programmedReporter struct {
	report func(context.Context, mutate.BuiltTPPs)

	mu      sync.Mutex
	pending mutate.BuiltTPPs // the newest build not yet reported, or nil
	recheck bool             // a fact changed since the last report
	wake    chan struct{}
}

func newProgrammedReporter(report func(context.Context, mutate.BuiltTPPs)) *programmedReporter {
	return &programmedReporter{report: report, wake: make(chan struct{}, 1)}
}

// submit hands over the record of a build that the hook has returned.
func (r *programmedReporter) submit(built mutate.BuiltTPPs) {
	r.mu.Lock()
	r.pending = built
	r.mu.Unlock()
	r.signal()
}

// recheckFacts asks for a report with no build: removals only.
func (r *programmedReporter) recheckFacts() {
	r.mu.Lock()
	r.recheck = true
	r.mu.Unlock()
	r.signal()
}

func (r *programmedReporter) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// take returns what waits: the newest build, or an empty one for a fact change.
func (r *programmedReporter) take() (mutate.BuiltTPPs, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	built, ok := r.pending, r.pending != nil || r.recheck
	if built == nil && ok {
		built = mutate.BuiltTPPs{}
	}
	r.pending, r.recheck = nil, false
	return built, ok
}

// run writes reports until ctx ends.
func (r *programmedReporter) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
		}
		if built, ok := r.take(); ok {
			r.report(ctx, built)
		}
	}
}
