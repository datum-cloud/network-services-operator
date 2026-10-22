// SPDX-License-Identifier: AGPL-3.0-only

package cell

import (
	"slices"
	"testing"

	"go.datum.net/network-services-operator/internal/config"
)

func TestGalacticControllersRequireEnablement(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled"},
		{name: "enabled", enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.CellControllerManager{
				Galactic: config.GalacticCellConfig{Enabled: tt.enabled, AttachmentMode: "Hypervisor"},
			}
			registrations := controllerRegistrations(nil, cfg, nil, nil)
			names := make([]string, 0, len(registrations))
			for _, registration := range registrations {
				names = append(names, registration.name)
			}
			for _, name := range []string{
				"galactic-networkcontext", "galactic-subnet", "galactic-networkinterface",
			} {
				if got := slices.Contains(names, name); got != tt.enabled {
					t.Errorf("controller %q registered = %t, want %t", name, got, tt.enabled)
				}
			}
		})
	}
}

func TestInternalDNSDisabledNeedsNoHubOrDNSAPIs(t *testing.T) {
	for _, registration := range controllerRegistrations(nil, config.CellControllerManager{}, nil, nil) {
		if registration.name != "internaldns-cell" {
			continue
		}
		enabled, err := registration.setup()
		if err != nil || enabled {
			t.Fatalf("disabled DNS setup = %t, %v", enabled, err)
		}
		return
	}
	t.Fatal("DNS registration not found")
}
