// SPDX-License-Identifier: AGPL-3.0-only

package source

import (
	"context"
	"errors"
	"net/http"
	"testing"

	coordinationv1 "k8s.io/api/coordination/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
)

// noopHandler is the simplest valid mchandler.EventHandlerFunc — it never
// actually wires events, which is fine because none of these tests reach the
// Start path.
func noopHandler() mchandler.EventHandlerFunc {
	return mchandler.EnqueueRequestForOwner(&coordinationv1.Lease{})
}

// fakeCluster is a hand-written stub satisfying cluster.Cluster. It returns
// the configured scheme + REST mapper and panics on anything OptionalKind
// shouldn't touch — guarding against accidental scope creep in the wrapper.
type fakeCluster struct {
	scheme *runtime.Scheme
	mapper apimeta.RESTMapper
}

func (f *fakeCluster) GetHTTPClient() *http.Client                          { panic("not used") }
func (f *fakeCluster) GetConfig() *rest.Config                              { panic("not used") }
func (f *fakeCluster) GetCache() crcache.Cache                              { panic("not used") }
func (f *fakeCluster) GetScheme() *runtime.Scheme                           { return f.scheme }
func (f *fakeCluster) GetClient() client.Client                             { panic("not used") }
func (f *fakeCluster) GetFieldIndexer() client.FieldIndexer                 { panic("not used") }
func (f *fakeCluster) GetEventRecorderFor(name string) record.EventRecorder { panic("not used") }
func (f *fakeCluster) GetRESTMapper() apimeta.RESTMapper                    { return f.mapper }
func (f *fakeCluster) GetAPIReader() client.Reader                          { panic("not used") }
func (f *fakeCluster) Start(_ context.Context) error                        { panic("not used") }

var _ cluster.Cluster = (*fakeCluster)(nil)

// noMatchMapper wraps a real mapper and rewrites RESTMapping on coordinationv1.Lease
// to return apimeta.NoKindMatchError — simulating a PCP that doesn't advertise
// coordination.k8s.io in discovery.
type noMatchMapper struct {
	apimeta.RESTMapper
	hideGroup string
}

func (m *noMatchMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*apimeta.RESTMapping, error) {
	if gk.Group == m.hideGroup {
		return nil, &apimeta.NoKindMatchError{GroupKind: gk, SearchedVersions: versions}
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	return s
}

func TestOptionalKind_NoMatchErrorDegradesGracefully(t *testing.T) {
	s := newScheme(t)
	mapper := &noMatchMapper{
		RESTMapper: testrestmapper.TestOnlyStaticRESTMapper(s),
		hideGroup:  coordinationv1.SchemeGroupVersion.Group,
	}
	cl := &fakeCluster{scheme: s, mapper: mapper}

	src := OptionalKind(&coordinationv1.Lease{}, noopHandler())

	got, shouldEngage, err := src.ForCluster("cluster-a", cl)
	if err != nil {
		t.Fatalf("ForCluster returned error: %v", err)
	}
	if shouldEngage {
		t.Errorf("shouldEngage=true, want false when REST mapper has no match")
	}
	if got != nil {
		t.Errorf("source = %v, want nil when REST mapper has no match", got)
	}
}

func TestOptionalKind_HealthyMappingDelegates(t *testing.T) {
	s := newScheme(t)
	cl := &fakeCluster{
		scheme: s,
		mapper: testrestmapper.TestOnlyStaticRESTMapper(s),
	}

	src := OptionalKind(&coordinationv1.Lease{}, noopHandler())

	got, shouldEngage, err := src.ForCluster("cluster-a", cl)
	if err != nil {
		t.Fatalf("ForCluster returned error: %v", err)
	}
	if !shouldEngage {
		t.Errorf("shouldEngage=false, want true when REST mapper is healthy")
	}
	if got == nil {
		t.Errorf("source = nil, want non-nil delegate when REST mapper is healthy")
	}
}

// otherMapperError is a generic mapper error that is NOT NoKindMatchError, used
// to verify OptionalKind propagates it instead of silently no-op'ing.
type otherErrorMapper struct {
	apimeta.RESTMapper
	err error
}

func (m *otherErrorMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*apimeta.RESTMapping, error) {
	return nil, m.err
}

func TestOptionalKind_NonMatchErrorsPropagate(t *testing.T) {
	s := newScheme(t)
	boom := errors.New("transient discovery failure")
	cl := &fakeCluster{
		scheme: s,
		mapper: &otherErrorMapper{
			RESTMapper: testrestmapper.TestOnlyStaticRESTMapper(s),
			err:        boom,
		},
	}

	src := OptionalKind(&coordinationv1.Lease{}, noopHandler())

	_, _, err := src.ForCluster("cluster-a", cl)
	if err == nil {
		t.Fatalf("expected error to propagate, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap %v", err, boom)
	}
}
