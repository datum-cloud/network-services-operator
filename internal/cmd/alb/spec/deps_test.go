// SPDX-License-Identifier: AGPL-3.0-only

package spec_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The alb MCP server reads an HTTPProxy through spec, and spec reads it through
// util. Neither may reach go.datum.net/datumctl, which is the plugin host's
// runtime: it resolves credentials from a datumctl process that does not exist
// in a server. Go imports are package-wide, so one file importing it puts the
// whole datumctl plugin runtime in a binary that has no CLI at all. That is why
// the pieces which genuinely need the host live in internal/cmd/alb/plugincli.
func TestSpecAndUtilDoNotDependOnTheDatumctlPluginSDK(t *testing.T) {
	for _, pkg := range []string{
		"go.datum.net/network-services-operator/internal/cmd/alb/spec",
		"go.datum.net/network-services-operator/internal/cmd/alb/util",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasPrefix(dep, "go.datum.net/datumctl") {
				t.Errorf("%s depends on %s; move whatever needs the plugin host into internal/cmd/alb/plugincli", pkg, dep)
			}
		}
	}
}
