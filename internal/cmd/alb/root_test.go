// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandFlags(t *testing.T) {
	root := Command()

	tests := []struct {
		flag      string
		shorthand string
		want      string
	}{
		{flag: "org"},
		{flag: "project"},
		{flag: "output", shorthand: "o", want: "table"},
		{flag: "verbose", shorthand: "v", want: "false"},
		{flag: "quiet", shorthand: "q", want: "false"},
		{flag: "color", want: "auto"},
		{flag: "yes", shorthand: "y", want: "false"},
	}

	for _, tc := range tests {
		t.Run(tc.flag, func(t *testing.T) {
			f := root.PersistentFlags().Lookup(tc.flag)
			require.NotNil(t, f)
			assert.Equal(t, tc.shorthand, f.Shorthand)
			if tc.want != "" {
				assert.Equal(t, tc.want, f.DefValue)
			}
		})
	}
}

func TestCommandSilencesCobraOutput(t *testing.T) {
	root := Command()
	assert.True(t, root.SilenceUsage)
	assert.True(t, root.SilenceErrors)
	assert.Equal(t, 2, root.SuggestionsMinimumDistance)
}

func TestCommandTree(t *testing.T) {
	root := Command()
	want := []string{"create", "list", "describe", "update", "delete", "hostname", "route", "waf", "header", "auth", "version"}
	for _, name := range want {
		assert.NotNil(t, root.Commands(), name)
		found := false
		for _, cmd := range root.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		assert.True(t, found, "missing command %s", name)
	}
}

func TestSkipsEntitlement(t *testing.T) {
	root := Command()
	assert.True(t, skipsEntitlement(root))

	for _, cmd := range root.Commands() {
		if cmd.Name() == "version" {
			assert.True(t, skipsEntitlement(cmd))
			return
		}
	}
	t.Fatal("version command missing")
}
