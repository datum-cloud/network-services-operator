// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootListRoutes(t *testing.T) {
	root := Command()
	root.SetArgs([]string{"list", "--help"})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "List VPCs") {
		t.Errorf("expected list help, got %q", out.String())
	}
}

func TestRootVPCAlone(t *testing.T) {
	root := Command()
	root.SetArgs([]string{"myvpc"})
	var errOut bytes.Buffer
	root.SetErr(&errOut)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for vpc alone")
	}
	if !strings.Contains(err.Error(), "specify a verb") {
		t.Errorf("expected verb hint, got %q", err.Error())
	}
}

func TestRootVPCBogusVerb(t *testing.T) {
	root := Command()
	root.SetArgs([]string{"myvpc", "bogus"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unknown verb")
	}
	if !strings.Contains(err.Error(), "unknown verb") {
		t.Errorf("expected unknown verb error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "list") {
		t.Errorf("expected valid verbs listed, got %q", err.Error())
	}
}

func TestRootListExtraArgs(t *testing.T) {
	root := Command()
	root.SetArgs([]string{"list", "extra"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for extra args to list")
	}
	if !strings.Contains(err.Error(), "no positional arguments") {
		t.Errorf("expected no-args error, got %q", err.Error())
	}
}

func TestRootNoArgs(t *testing.T) {
	root := Command()
	root.SetArgs([]string{})
	var out bytes.Buffer
	root.SetOut(&out)
	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Manage VPCs") {
		t.Errorf("expected help output, got %q", out.String())
	}
}
