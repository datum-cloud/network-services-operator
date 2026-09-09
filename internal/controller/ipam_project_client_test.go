// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"errors"
	"reflect"
	"testing"

	"k8s.io/client-go/rest"
)

func newTestIPAMClientFactory(t *testing.T, host string) *projectPathIPAMClientFactory {
	t.Helper()

	scheme, err := IPAMScheme()
	if err != nil {
		t.Fatalf("building the IPAM scheme: %v", err)
	}

	f, err := NewIPAMClientFactory(&rest.Config{Host: host}, scheme)
	if err != nil {
		t.Fatalf("building the factory: %v", err)
	}
	return f.(*projectPathIPAMClientFactory)
}

func hostForProject(t *testing.T, f *projectPathIPAMClientFactory, project string) string {
	t.Helper()

	cfg, err := f.configForProject(project)
	if err != nil {
		t.Fatalf("building a config for project %q: %v", project, err)
	}
	return cfg.Host
}

func TestIPAMClientFactory_AddressesEachProjectSeparately(t *testing.T) {
	f := newTestIPAMClientFactory(t, "https://ipam.example.com")

	const want = "https://ipam.example.com/apis/resourcemanager.miloapis.com/v1alpha1/projects/project-a/control-plane"
	if got := hostForProject(t, f, "project-a"); got != want {
		t.Errorf("expected the host to name the project, got %q", got)
	}
	if hostForProject(t, f, "project-a") == hostForProject(t, f, "project-b") {
		t.Error("expected two projects to reach distinct hosts")
	}
}

func TestIPAMClientFactory_LeavesImpersonationUnset(t *testing.T) {
	f := newTestIPAMClientFactory(t, "https://ipam.example.com")

	cfg, err := f.configForProject("project-a")
	if err != nil {
		t.Fatalf("building the config: %v", err)
	}

	if !reflect.DeepEqual(cfg.Impersonate, rest.ImpersonationConfig{}) {
		t.Errorf("expected the caller's own identity to be used, got impersonation %+v", cfg.Impersonate)
	}
}

// A base host can already carry a path, from a kubeconfig pointing at an
// aggregated endpoint. The project path replaces it rather than nesting under
// it, which would address a route that does not exist.
func TestIPAMClientFactory_ReplacesAnExistingBasePath(t *testing.T) {
	f := newTestIPAMClientFactory(t, "https://ipam.example.com/some/prefix")

	const want = "https://ipam.example.com/apis/resourcemanager.miloapis.com/v1alpha1/projects/project-a/control-plane"
	if got := hostForProject(t, f, "project-a"); got != want {
		t.Errorf("expected the base path to be replaced, got %q", got)
	}
}

func TestIPAMClientFactory_RejectsAnEmptyProject(t *testing.T) {
	f := newTestIPAMClientFactory(t, "https://ipam.example.com")

	if _, err := f.ClientForProject(""); !errors.Is(err, errNoProject) {
		t.Errorf("expected errNoProject, got %v", err)
	}
}

func TestIPAMClientFactory_CachesPerProject(t *testing.T) {
	f := newTestIPAMClientFactory(t, "https://ipam.example.com")

	first, err := f.ClientForProject("project-a")
	if err != nil {
		t.Fatalf("building the first client: %v", err)
	}
	second, err := f.ClientForProject("project-a")
	if err != nil {
		t.Fatalf("building the second client: %v", err)
	}

	if first != second {
		t.Error("expected a repeat call to return the cached client")
	}
}
