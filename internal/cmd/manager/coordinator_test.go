// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
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
	var uncached map[string]bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		calls := callsTo(fn.Body, "sharded.New")
		if len(calls) > 0 {
			coordinators = append(coordinators, calls...)
			uncached = namesAssignedFrom(fn.Body, "client.New")
		}
	}

	require.Len(t, coordinators, 1, "manager.go must build the cluster sharding coordinator once")
	require.NotEmpty(t, coordinators[0].Args, "sharded.New takes the coordinator's client first")
	arg := identName(coordinators[0].Args[0])
	require.True(t, uncached[arg],
		"the coordinator's client must come from client.New in the same function, not from a cluster's cached client")
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
