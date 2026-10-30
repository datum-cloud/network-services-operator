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

	// Federation configures the connection to the federation hub a cell
	// publishes its network interfaces to, so a consumer can see the interface
	// behind their instance in their own control plane.
	//
	// Required. A cell that publishes nothing still allocates addresses and
	// still reports its interfaces Bound, so leaving it unset produces a cell
	// that looks healthy and whose interfaces never arrive. Refusing to start
	// is what makes that a deployment error instead of a silent one.
	Federation FederationConfig `json:"federation,omitempty"`

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

	// InternalDNS configures managed resolver access. It is disabled unless
	// explicitly enabled.
	InternalDNS InternalDNSConfig `json:"internalDNS,omitempty"`
	// Galactic enables this cell to reconcile downstream Galactic VPCs and
	// attachments once the Galactic CRDs are installed.
	Galactic GalacticCellConfig `json:"galactic,omitempty"`

	// NetworkLoadBalancer serves network load balancer cell intents from this
	// cell's Galactic gateways. It is disabled unless explicitly enabled, and
	// needs galactic.enabled.
	NetworkLoadBalancer NetworkLoadBalancerCellConfig `json:"networkLoadBalancer,omitempty"`
}

// +k8s:deepcopy-gen=true

type NetworkLoadBalancerCellConfig struct {
	MaxExposedPorts        int32 `json:"maxExposedPorts,omitempty"`
	SharedAddressListeners bool  `json:"sharedAddressListeners,omitempty"`
	UDPPortRanges          bool  `json:"udpPortRanges,omitempty"`
	Enabled                bool  `json:"enabled,omitempty"`

	// IPv4VIPs serves IPv4 load balancer addresses from this cell. Turn it on
	// only once every gateway in the cell has Galactic's IPv4 VIPs enabled and
	// the cell announces the range its IPv4 addresses come from.
	IPv4VIPs bool `json:"ipv4VIPs,omitempty"`
}

// +k8s:deepcopy-gen=true

type GalacticCellConfig struct {
	Enabled        bool   `json:"enabled,omitempty"`
	AttachmentMode string `json:"attachmentMode,omitempty"`

	// AttachmentLabelNamespaces lists the namespaces whose interfaces carry
	// their compute.datumapis.com/ labels onto their VPC attachments. Tenants
	// can write those labels on their own claims, and attachment labels select
	// platform producers such as internal DNS resolvers, so only platform
	// namespaces belong here.
	AttachmentLabelNamespaces []string `json:"attachmentLabelNamespaces,omitempty"`

	// TrafficPolicyEnforced states that every node in this cell runs Galactic
	// traffic policy in enforce mode, so the policy written for an interface
	// actually drops what it denies. Galactic reports a policy as applied in
	// every mode, so nothing in the binding says whether it is enforced.
	//
	// While false, an interface on a network with internet access turned off
	// reports that the location does not block it yet, rather than reporting
	// a network as private when nothing is blocking.
	TrafficPolicyEnforced bool `json:"trafficPolicyEnforced,omitempty"`

	// HypervisorHostOnlyPorts lists up to four TCP ports that guests attached
	// in Hypervisor mode serve only to their own node, such as a unikernel's
	// exec agent. Galactic drops traffic to them unless it is addressed to the
	// guest's link-local address.
	//
	// Set it only once every node in the cell runs a Galactic that installs
	// galactic-tap-host-only. The conflist names that plugin, so a node
	// without it fails the attachment rather than leaving the ports open, as
	// does a node whose datapath is not loaded.
	HypervisorHostOnlyPorts []uint16 `json:"hypervisorHostOnlyPorts,omitempty"`
}

const maxHostOnlyPorts = 4

// Validate reports whether the cell configuration can serve a cell.
func (c *CellControllerManager) Validate() error {
	if err := c.IPAM.validate(); err != nil {
		return fmt.Errorf("ipam: %w", err)
	}
	if err := c.Federation.validate(); err != nil {
		return fmt.Errorf("federation: %w", err)
	}
	if err := c.InternalDNS.validate(); err != nil {
		return fmt.Errorf("internalDNS: %w", err)
	}
	if c.NetworkLoadBalancer.Enabled && !c.Galactic.Enabled {
		return fmt.Errorf("networkLoadBalancer.enabled needs galactic.enabled")
	}
	if c.NetworkLoadBalancer.IPv4VIPs && !c.NetworkLoadBalancer.Enabled {
		return fmt.Errorf("networkLoadBalancer.ipv4VIPs needs networkLoadBalancer.enabled")
	}
	if c.Galactic.Enabled {
		switch c.Galactic.AttachmentMode {
		case "Netns", "Hypervisor", "HypervisorDeclared":
		default:
			return fmt.Errorf("galactic.attachmentMode must be Netns, Hypervisor, or HypervisorDeclared")
		}
	}
	if err := validateHostOnlyPorts(c.Galactic.HypervisorHostOnlyPorts); err != nil {
		return fmt.Errorf("galactic.hypervisorHostOnlyPorts: %w", err)
	}
	return nil
}

func validateHostOnlyPorts(ports []uint16) error {
	if len(ports) > maxHostOnlyPorts {
		return fmt.Errorf("at most %d ports, got %d", maxHostOnlyPorts, len(ports))
	}
	seen := map[uint16]bool{}
	for _, port := range ports {
		if port == 0 {
			return fmt.Errorf("port 0 is not a port")
		}
		if seen[port] {
			return fmt.Errorf("port %d is listed twice", port)
		}
		seen[port] = true
	}
	return nil
}
