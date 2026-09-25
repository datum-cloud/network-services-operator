// SPDX-License-Identifier: AGPL-3.0-only

package spec_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	specPkg = "go.datum.net/network-services-operator/internal/cmd/alb/spec"
	utilPkg = "go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// The alb MCP server decodes a load balancer through spec, and spec reads it
// through util. Neither may reach go.datum.net/datumctl, the plugin host's
// runtime: it resolves credentials by running a datumctl process, which does
// not exist beside a server. Go imports are package-wide, so one file importing
// it puts the whole plugin runtime in every binary that decodes a load
// balancer. What genuinely needs the host lives in internal/cmd/alb/plugincli.
func TestSpecAndUtilDoNotDependOnTheDatumctlPluginHost(t *testing.T) {
	for _, pkg := range []string{specPkg, utilPkg} {
		for _, dep := range deps(t, "-deps", pkg) {
			if strings.HasPrefix(dep, "go.datum.net/datumctl") {
				t.Errorf("%s depends on %s; move whatever needs the plugin host into internal/cmd/alb/plugincli", pkg, dep)
			}
		}
	}
}

// A function here taking a *cobra.Command is one that reads a CLI flag, and a
// server has no flags to read. Those belong in plugincli beside ProjectFromCmd.
//
// This checks direct imports only, deliberately: k8s.io/component-base/cli/flag
// imports cobra, so every package reaching client-go has it transitively and a
// transitive ban would be unsatisfiable.
func TestSpecAndUtilDoNotImportCobraDirectly(t *testing.T) {
	for _, pkg := range []string{specPkg, utilPkg} {
		for _, dep := range deps(t, "-f", "{{range .Imports}}{{.}}\n{{end}}", pkg) {
			if dep == "github.com/spf13/cobra" {
				t.Errorf("%s imports cobra directly; move whatever reads a flag into internal/cmd/alb/plugincli", pkg)
			}
		}
	}
}

func deps(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}
