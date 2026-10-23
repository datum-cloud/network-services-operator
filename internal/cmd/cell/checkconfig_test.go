// SPDX-License-Identifier: AGPL-3.0-only

package cell

import (
	"os"
	"path/filepath"
	"testing"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	"go.datum.net/network-services-operator/internal/config"
)

const testKubeconfig = "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: https://c.example\ncontexts:\n- name: c\n  context:\n    cluster: c\ncurrent-context: c\n"

func TestCheckConfigFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(good, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	loadable := func() config.CellControllerManager {
		var c config.CellControllerManager
		c.IPAM.KubeconfigPath = good
		c.Federation.KubeconfigPath = good
		c.Discovery.Mode = multiclusterproviders.ProviderMilo
		c.Discovery.DiscoveryKubeconfigPath = good
		c.Discovery.ProjectKubeconfigPath = good
		return c
	}

	if err := checkConfigFiles(loadable()); err != nil {
		t.Fatalf("loadable config rejected: %v", err)
	}

	for name, breakIt := range map[string]func(*config.CellControllerManager){
		"ipam":       func(c *config.CellControllerManager) { c.IPAM.KubeconfigPath = missing },
		"federation": func(c *config.CellControllerManager) { c.Federation.KubeconfigPath = missing },
		"discovery":  func(c *config.CellControllerManager) { c.Discovery.DiscoveryKubeconfigPath = missing },
		"project":    func(c *config.CellControllerManager) { c.Discovery.ProjectKubeconfigPath = missing },
	} {
		t.Run(name, func(t *testing.T) {
			c := loadable()
			breakIt(&c)
			if err := checkConfigFiles(c); err == nil {
				t.Fatal("config naming a missing kubeconfig was accepted")
			}
		})
	}
}
