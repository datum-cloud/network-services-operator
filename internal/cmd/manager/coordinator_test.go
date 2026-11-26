// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/config"
)

// TestCoordinatorReadsLeasesFromTheAPIServer fails when the cluster sharding
// coordinator gets a client that reads from the informer cache. The
// coordinator renews each lease with a Get and then an Update. A Get from a
// lagging cache sends an old resourceVersion, the API server answers 409, and
// the coordinator stops the project. The wiring lives in manager.go, so the
// test reads it from there.
func TestCoordinatorReadsLeasesFromTheAPIServer(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "manager.go", nil, 0)
	require.NoError(t, err)

	var coordinators []*ast.CallExpr
	var coordinatorClients, uncached map[string]bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name == "newClusterShardingCoordinatorClient" {
			uncached = namesAssignedFrom(fn.Body, "client.New")
		}
		calls := callsTo(fn.Body, "sharded.New")
		if len(calls) > 0 {
			coordinators = append(coordinators, calls...)
			coordinatorClients = namesAssignedFrom(fn.Body, "newClusterShardingCoordinatorClient")
		}
	}

	require.Len(t, coordinators, 1, "manager.go must build the cluster sharding coordinator once")
	require.NotEmpty(t, coordinators[0].Args, "sharded.New takes the coordinator's client first")
	arg := identName(coordinators[0].Args[0])
	require.True(t, coordinatorClients[arg],
		"the coordinator's client must come from newClusterShardingCoordinatorClient in the same function, not from a cluster's cached client")
	require.NotEmpty(t, uncached, "newClusterShardingCoordinatorClient must build its client with client.New")
}

// TestCoordinatorClientHasNoClientSideLimit fails when the coordinator's
// client limits its own requests. Each 10 s the coordinator renews each shard
// lease with a Get and an Update, through one client that the peer registry
// shares. Under the control plane's limit of 50 requests a second, one round
// for 600 owned projects takes about 22 s. That is past the 20 s after which
// the other managers read a lease as expired. API Priority and Fairness on the
// API server limits this client instead.
func TestCoordinatorClientHasNoClientSideLimit(t *testing.T) {
	const ownedProjects = 600
	const roundDeadline = 2 * time.Second

	api := newLeaseAPI(t)
	coordinatorClient, err := newClusterShardingCoordinatorClient(controlPlaneConfig(api.URL))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), roundDeadline)
	defer cancel()
	for i := range ownedProjects {
		key := client.ObjectKey{Namespace: "shard-leases", Name: fmt.Sprintf("project-%d", i)}
		lease := &coordinationv1.Lease{}
		require.NoError(t, coordinatorClient.Get(ctx, key, lease), "the Get of lease %d of %d", i+1, ownedProjects)
		require.NoError(t, coordinatorClient.Update(ctx, lease), "the Update of lease %d of %d", i+1, ownedProjects)
	}
	require.EqualValues(t, ownedProjects, api.gets.Load(), "each Get must reach the API server")
	require.EqualValues(t, ownedProjects, api.puts.Load(), "each Update must reach the API server")
}

// TestCoordinatorClientKeepsTheControlPlaneLimit fails when building the
// coordinator's client changes the config that NSO's other control-plane
// clients are built from.
func TestCoordinatorClientKeepsTheControlPlaneLimit(t *testing.T) {
	cfg := controlPlaneConfig("https://control-plane.example")
	want := rest.CopyConfig(cfg)

	_, err := newClusterShardingCoordinatorClient(cfg)
	require.NoError(t, err)

	require.Equal(t, want.QPS, cfg.QPS)
	require.Equal(t, want.Burst, cfg.Burst)
}

// callsTo returns each call to "pkg.Func" under node.
func callsTo(node ast.Node, call string) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		if expr, ok := n.(*ast.CallExpr); ok && callName(expr) == call {
			calls = append(calls, expr)
		}
		return true
	})
	return calls
}

// namesAssignedFrom returns the first name of each assignment under node whose
// value is a call to "pkg.Func".
func namesAssignedFrom(node ast.Node, call string) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if ok && len(assign.Rhs) == 1 && callName(assign.Rhs[0]) == call {
			names[identName(assign.Lhs[0])] = true
		}
		return true
	})
	return names
}

// controlPlaneConfig returns the config of the control plane at host, with the
// client limits that NSO applies to it by default.
func controlPlaneConfig(host string) *rest.Config {
	cfg := &rest.Config{Host: host}
	limits := config.ClientConnectionConfig{}
	config.SetDefaults_ClientConnectionConfig(&limits)
	limits.ApplyTo(cfg)
	return cfg
}

// leaseAPI serves discovery for coordination.k8s.io/v1, and a Get and an
// Update of any Lease. It counts the Gets and the Updates.
type leaseAPI struct {
	*httptest.Server
	gets, puts atomic.Int64
}

func newLeaseAPI(t *testing.T) *leaseAPI {
	t.Helper()
	api := &leaseAPI{}
	leases := metav1.GroupVersionForDiscovery{GroupVersion: "coordination.k8s.io/v1", Version: "v1"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, &metav1.APIVersions{
			TypeMeta: metav1.TypeMeta{Kind: "APIVersions"},
			Versions: []string{"v1"},
		})
	})
	mux.HandleFunc("GET /apis", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, &metav1.APIGroupList{
			TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"},
			Groups: []metav1.APIGroup{{
				Name:             "coordination.k8s.io",
				Versions:         []metav1.GroupVersionForDiscovery{leases},
				PreferredVersion: leases,
			}},
		})
	})
	mux.HandleFunc("GET /apis/coordination.k8s.io/v1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, &metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
			GroupVersion: leases.GroupVersion,
			APIResources: []metav1.APIResource{{
				Name:       "leases",
				Namespaced: true,
				Kind:       "Lease",
				Verbs:      metav1.Verbs{"get", "update"},
			}},
		})
	})
	lease := "/apis/coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}"
	mux.HandleFunc("GET "+lease, func(w http.ResponseWriter, r *http.Request) {
		api.gets.Add(1)
		writeJSON(t, w, &coordinationv1.Lease{
			TypeMeta: metav1.TypeMeta{Kind: "Lease", APIVersion: leases.GroupVersion},
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       r.PathValue("namespace"),
				Name:            r.PathValue("name"),
				ResourceVersion: "1",
			},
		})
	})
	mux.HandleFunc("PUT "+lease, func(w http.ResponseWriter, r *http.Request) {
		api.puts.Add(1)
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Error(err)
		}
	})
	api.Server = httptest.NewServer(mux)
	t.Cleanup(api.Close)
	return api
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Error(err)
	}
}
