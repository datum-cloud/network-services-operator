package config

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

// CellControllerManager configures the controller manager that runs in a
// cell's control plane. It carries only what a cell needs: the connection to
// the IPAM API addresses are claimed from, and the location the cell serves.
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

	// Location names the location this cell serves, and is only the fallback:
	// a ServingLocation delivered to the cell wins over it. No two cells may
	// share one. Absent both, the cell reports a waiting state on the claims it
	// cannot fulfil and heals when the delivered copy arrives, so its absence is
	// not a validation failure.
	Location LocationConfig `json:"location,omitempty"`
}

// Validate reports whether the cell configuration can serve a cell.
func (c *CellControllerManager) Validate() error {
	if err := c.IPAM.validate(); err != nil {
		return fmt.Errorf("ipam: %w", err)
	}
	return nil
}
