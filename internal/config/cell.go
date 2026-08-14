package config

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

// CellControllerManager configures the controller manager that runs in a
// cell's control plane.
type CellControllerManager struct {
	metav1.TypeMeta

	MetricsServer MetricsServerConfig `json:"metricsServer"`

	Discovery DiscoveryConfig `json:"discovery"`

	// LeaderElection configures controller-runtime leader election timings.
	LeaderElection LeaderElectionConfig `json:"leaderElection,omitempty"`

	// ControlPlaneClient configures the Kubernetes client connection to the
	// cell control plane the manager runs in.
	ControlPlaneClient ClientConnectionConfig `json:"controlPlaneClient,omitempty"`

	// ProjectClient configures the Kubernetes client connection used for both
	// project discovery and per-project cluster connections.
	ProjectClient ClientConnectionConfig `json:"projectClient,omitempty"`

	// IPAM configures the connection to the IPAM aggregated API server that
	// network interface addresses are claimed from.
	IPAM IPAMConfig `json:"ipam"`

	// Location names the location this cell serves. Leave it unset unless you
	// have to pin one: a ServingLocation delivered to the cell wins over it,
	// and is the normal way a cell learns where it is.
	//
	// Set it only for a cell that no ServingLocation reaches yet. Never give
	// two cells the same location. They would both hand out and both release
	// the same addresses, and nothing detects it.
	//
	// With no location from either source, the cell still starts. It reports a
	// waiting state on the claims it cannot fulfil and recovers on its own when
	// a ServingLocation arrives.
	//
	// This field is temporary. It goes away once no cell reports Configured for
	// nso_cell_location_identity_source.
	Location LocationConfig `json:"location,omitempty"`
}

// Validate reports whether the cell configuration can serve a cell.
func (c *CellControllerManager) Validate() error {
	if err := c.IPAM.validate(); err != nil {
		return fmt.Errorf("ipam: %w", err)
	}
	return nil
}
