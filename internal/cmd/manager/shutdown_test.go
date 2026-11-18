// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"
)

// managerManifest is the Deployment that runs the manager command.
var managerManifest = filepath.Join("..", "..", "..", "config", "manager", "manager.yaml")

// TestShutdownFitsTheGracePeriod fails when the kubelet can kill the manager
// before it stops: the webhook shutdown delay and the manager's graceful
// shutdown run one after the other, and both must end inside the pod's
// terminationGracePeriodSeconds.
func TestShutdownFitsTheGracePeriod(t *testing.T) {
	grace := managerGracePeriod(t)

	require.Positive(t, webhookShutdownDelay, "without a delay, the webhook server closes open connections with no warning")
	budget := webhookShutdownDelay + managerGracefulShutdownTimeout
	require.Less(t, budget, grace,
		"the shutdown delay (%s) plus the graceful shutdown timeout (%s) must end before the grace period in %s (%s)",
		webhookShutdownDelay, managerGracefulShutdownTimeout, managerManifest, grace)
}

// managerGracePeriod reads terminationGracePeriodSeconds from the Deployment
// whose container runs the manager command.
func managerGracePeriod(t *testing.T) time.Duration {
	t.Helper()
	data, err := os.ReadFile(managerManifest)
	require.NoError(t, err)

	for _, doc := range strings.Split(string(data), "\n---\n") {
		var deployment appsv1.Deployment
		require.NoError(t, yaml.Unmarshal([]byte(doc), &deployment))
		if deployment.Kind != "Deployment" {
			continue
		}
		pod := deployment.Spec.Template.Spec
		for _, container := range pod.Containers {
			if slices.Equal(container.Command, []string{"/network-services", "manager"}) {
				require.NotNil(t, pod.TerminationGracePeriodSeconds,
					"%s sets no terminationGracePeriodSeconds, so the default of 30s applies", managerManifest)
				return time.Duration(*pod.TerminationGracePeriodSeconds) * time.Second
			}
		}
	}
	t.Fatalf("no Deployment in %s runs the manager command", managerManifest)
	return 0
}

// TestManagerStartsTheDelayedClose fails when the manager command no longer
// starts the delayed close: on the signal, the webhook server must mark each
// response Connection: close, wait webhookShutdownDelay, and only then cancel
// the manager's context. The wiring lives inline in the command's RunE, so the
// test reads it from manager.go.
func TestManagerStartsTheDelayedClose(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "manager.go", nil, 0)
	require.NoError(t, err)

	signal := assignedFrom(file, "ctrl.SetupSignalHandler")
	require.Len(t, signal, 1, "manager.go must take the signal from ctrl.SetupSignalHandler, once")
	manager := assignedFrom(file, "context.WithCancel")
	require.Len(t, manager, 2, "manager.go must make the manager's context with context.WithCancel, once")
	closes := goCallsTo(file, "CloseConnectionsThenStop")
	require.Len(t, closes, 1, "manager.go must start the delayed close once, in its own goroutine")
	args := make([]string, 0, len(closes[0].Args))
	for _, arg := range closes[0].Args {
		args = append(args, identName(arg))
	}
	require.Equal(t, []string{signal[0], "webhookShutdownDelay", manager[1]}, args,
		"the delayed close must wait for the signal, hold for webhookShutdownDelay, then stop the manager")
}

// assignedFrom returns the names that the one assignment from a call to
// "pkg.Func" defines, or nil when no assignment or more than one calls it.
func assignedFrom(file *ast.File, call string) []string {
	var found [][]string
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if ok && len(assign.Rhs) == 1 && callName(assign.Rhs[0]) == call {
			names := make([]string, 0, len(assign.Lhs))
			for _, lhs := range assign.Lhs {
				names = append(names, identName(lhs))
			}
			found = append(found, names)
		}
		return true
	})
	if len(found) != 1 {
		return nil
	}
	return found[0]
}

// goCallsTo returns each call to a method of this name that a go statement
// starts.
func goCallsTo(file *ast.File, method string) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		if sel, ok := stmt.Call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
			calls = append(calls, stmt.Call)
		}
		return true
	})
	return calls
}

// callName returns "pkg.Func" for a call to a selector, or "".
func callName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return identName(sel.X) + "." + sel.Sel.Name
}

func identName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}
