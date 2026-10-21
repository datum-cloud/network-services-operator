// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"go.datum.net/network-services-operator/internal/cmd/cell"
	"go.datum.net/network-services-operator/internal/config"
)

var setupWithManagerPattern = regexp.MustCompile(`func \(\w+ \*(\w+)\) SetupWithManager\(`)

func TestFederationRESTConfig(t *testing.T) {
	deployment := &rest.Config{Host: "https://deployment.example"}
	got, err := federationRESTConfig(config.FederationConfig{}, deployment)
	if err != nil || got.Host != deployment.Host || got == deployment {
		t.Fatalf("deployment hub configuration: got %v, error %v", got, err)
	}
	kubeconfig := filepath.Join(t.TempDir(), "hub.yaml")
	data := []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: hub\n  cluster:\n    server: https://hub.example\ncontexts:\n- name: hub\n  context:\n    cluster: hub\ncurrent-context: hub\n")
	if err := os.WriteFile(kubeconfig, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = federationRESTConfig(config.FederationConfig{KubeconfigPath: kubeconfig}, deployment)
	if err != nil || got.Host != "https://hub.example" {
		t.Fatalf("explicit hub configuration: got %v, error %v", got, err)
	}
	if _, err := federationRESTConfig(config.FederationConfig{KubeconfigPath: kubeconfig + ".missing"}, deployment); err == nil {
		t.Fatal("missing federation kubeconfig was accepted")
	}
}

var reconcilerControllerNames = map[string]string{
	"ChallengeReconciler":                          "challenge",
	"ConnectorAdvertisementReconciler":             "connectoradvertisement",
	"ConnectorReconciler":                          "connector",
	"DomainReconciler":                             "domain",
	"EdgeReachabilityReconciler":                   "edgereachability",
	"GatewayClassReconciler":                       "gatewayclass",
	"GatewayDownstreamCertificateSolverReconciler": "downstream-certificate-solver",
	"GatewayDownstreamGCReconciler":                "gateway_downstream_resources",
	"GatewayReconciler":                            "gateway",
	"GatewayResourceReplicatorReconciler":          "gateway_resource_replicator",
	"HTTPProxyReconciler":                          "httpproxy",
	"InternalDNSCellReconciler":                    "internaldns-cell",
	"InternalDNSProjectReconciler":                 "internaldns-project",
	"InternalDNSReadinessReconciler":               "internaldns-readiness",
	"IrohDNSReconciler":                            "iroh-dns",
	"NetworkBindingReconciler":                     "networkbinding",
	"NetworkContextReconciler":                     "networkcontext",
	"NetworkContextHoldReconciler":                 "networkcontexthold",
	"NetworkInterfaceClaimReconciler":              "networkinterfaceclaim",
	"NetworkInterfaceProjectionGCReconciler":       "networkinterfaceprojectiongc",
	"NetworkInterfaceProjector":                    "networkinterfaceprojector",
	"NetworkInterfaceReconciler":                   "networkinterface",
	"NetworkInterfaceWriteBackReconciler":          "networkinterfacewriteback",
	"NetworkPolicyReconciler":                      "networkpolicy",
	"NetworkPresenceReconciler":                    "networkpresence",
	"NetworkPresenceSyncReconciler":                "networkpresencesync",
	"NetworkReconciler":                            "network",
	"NetworkServiceReconciler":                     "networkservice",
	"SubnetClaimReconciler":                        "subnetclaim",
	"SubnetReconciler":                             "subnet",
	"TrafficProtectionPolicyReconciler":            "trafficprotectionpolicy",
	"VPCEndpointSliceWriteBackReconciler":          "vpcendpointslicewriteback",
}

func registeredNames(registrations []namedSetup) []string {
	names := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		if registration.enabled {
			names = append(names, registration.name)
		}
	}
	return names
}

func TestControllerRegistrations_CapabilityGates(t *testing.T) {
	var cfg config.NetworkServicesOperator
	cfg.Gateway.EnableDownstreamCertificateSolver = false
	cfg.Gateway.Coraza.Disabled = true
	cfg.Gateway.DeleteErroredChallenges = new(bool)
	cfg.Connector.Iroh.DNSEnabled = false

	got := registeredNames(controllerRegistrations(nil, cfg, controllerDeps{}))

	for _, name := range []string{
		"downstream-certificate-solver",
		"trafficprotectionpolicy",
		"challenge",
		"iroh-dns",
	} {
		if slices.Contains(got, name) {
			t.Errorf("expected %q to stay unregistered, got %v", name, got)
		}
	}
	if !slices.Contains(got, "gateway") {
		t.Errorf("expected ungated control-plane controllers to stay registered, got %v", got)
	}
}

func TestControllerRegistrations_EveryControllerClassifiedExactlyOnce(t *testing.T) {
	reconcilers := reconcilersInSource(t)

	for _, reconciler := range reconcilers {
		if _, ok := reconcilerControllerNames[reconciler]; !ok {
			t.Errorf("%s is not classified into a controller set; add it to reconcilerControllerNames and to controllerRegistrations", reconciler)
		}
	}
	for reconciler := range reconcilerControllerNames {
		if !slices.Contains(reconcilers, reconciler) {
			t.Errorf("%s is classified but no longer exists in internal/controller", reconciler)
		}
	}

	perCommand := map[string][]string{
		"manager":      ControllerNames(),
		"cell-manager": cell.ControllerNames(),
	}

	for reconciler, name := range reconcilerControllerNames {
		var owners []string
		for command, names := range perCommand {
			if slices.Contains(names, name) {
				owners = append(owners, command)
			}
		}
		if len(owners) != 1 {
			t.Errorf("%s (%q) is registered by %d commands, want exactly 1", reconciler, name, len(owners))
		}
	}

	var registered []string
	for _, names := range perCommand {
		registered = append(registered, names...)
	}
	classified := slices.Collect(maps.Values(reconcilerControllerNames))
	for _, name := range registered {
		if !slices.Contains(classified, name) {
			t.Errorf("controller %q is registered but maps to no reconciler", name)
		}
	}
	if len(registered) != len(reconcilerControllerNames) {
		t.Errorf("expected %d registered controllers, got %d: %v", len(reconcilerControllerNames), len(registered), registered)
	}
}

func TestWebhookRegistrations_RegistersEveryWebhook(t *testing.T) {
	want := []string{
		"Backend",
		"BackendTLSPolicy",
		"BackendTrafficPolicy",
		"Domain",
		"Gateway",
		"HTTPProxy",
		"HTTPRoute",
		"HTTPRouteFilter",
		"Network",
		"SecurityPolicy",
		"TrafficProtectionPolicy",
	}

	got := registeredNames(webhookRegistrations(nil, config.NetworkServicesOperator{}))

	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func reconcilersInSource(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join("..", "..", "controller")

	var reconcilers []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range setupWithManagerPattern.FindAllStringSubmatch(string(source), -1) {
			if !slices.Contains(reconcilers, match[1]) {
				reconcilers = append(reconcilers, match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unable to scan %s: %v", dir, err)
	}

	if len(reconcilers) == 0 {
		t.Fatalf("found no reconcilers in %s", dir)
	}
	return reconcilers
}
