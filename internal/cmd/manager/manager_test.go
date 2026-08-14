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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
)

var reconcilerControllerNames = map[string]string{
	"ChallengeReconciler":                          "challenge",
	"ConnectorAdvertisementReconciler":             "connectoradvertisement",
	"ConnectorReconciler":                          "connector",
	"DomainReconciler":                             "domain",
	"GatewayClassReconciler":                       "gatewayclass",
	"GatewayDownstreamCertificateSolverReconciler": "downstream-certificate-solver",
	"GatewayDownstreamGCReconciler":                "gateway_downstream_resources",
	"GatewayReconciler":                            "gateway",
	"GatewayResourceReplicatorReconciler":          "gateway_resource_replicator",
	"HTTPProxyReconciler":                          "httpproxy",
	"IrohDNSReconciler":                            "iroh-dns",
	"NetworkBindingReconciler":                     "networkbinding",
	"NetworkContextReconciler":                     "networkcontext",
	"NetworkInterfaceClaimReconciler":              "networkinterfaceclaim",
	"NetworkInterfaceReconciler":                   "networkinterface",
	"NetworkPolicyReconciler":                      "networkpolicy",
	"NetworkReconciler":                            "network",
	"SubnetClaimReconciler":                        "subnetclaim",
	"SubnetReconciler":                             "subnet",
	"TrafficProtectionPolicyReconciler":            "trafficprotectionpolicy",
}

func configForSets(sets ...config.ControllerSet) config.NetworkServicesOperator {
	cfg := config.NetworkServicesOperator{
		Controllers: config.ControllersConfig{Sets: sets},
		Gateway: config.GatewayConfig{
			EnableDownstreamCertificateSolver: true,
		},
		Connector: config.ConnectorConfig{
			Iroh: config.IrohConnectorConfig{DNSEnabled: true},
		},
	}
	cfg.NetworkInterface.Location = config.LocationConfig{Name: "edge-1", Namespace: "datum-locations"}
	return cfg
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

func TestControllerRegistrations_Sets(t *testing.T) {
	controlPlaneControllers := []string{
		"network",
		"networkbinding",
		"networkcontext",
		"networkpolicy",
		"subnet",
		"subnetclaim",
		"httpproxy",
		"gateway",
		"gatewayclass",
		"gateway_downstream_resources",
		"gateway_resource_replicator",
		"trafficprotectionpolicy",
		"downstream-certificate-solver",
		"domain",
		"connector",
		"connectoradvertisement",
		"iroh-dns",
		"challenge",
	}
	locationControllers := []string{"networkinterfaceclaim", "networkinterface"}

	tests := []struct {
		name string
		sets []config.ControllerSet
		want []string
	}{
		{
			name: "control-plane only",
			sets: []config.ControllerSet{config.ControllerSetControlPlane},
			want: controlPlaneControllers,
		},
		{
			name: "cell only",
			sets: []config.ControllerSet{config.ControllerSetCell},
			want: locationControllers,
		},
		{
			name: "every set",
			sets: config.AllControllerSets(),
			want: append(append([]string{}, controlPlaneControllers...), locationControllers...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := registeredNames(controllerRegistrations(nil, configForSets(tt.sets...), controllerDeps{}))

			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)

			if !slices.Equal(got, want) {
				t.Fatalf("expected %v, got %v", want, got)
			}
		})
	}
}

func TestControllerRegistrations_CapabilityGates(t *testing.T) {
	cfg := configForSets(config.ControllerSetControlPlane)
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

	perSet := map[config.ControllerSet][]string{}
	for _, set := range config.AllControllerSets() {
		perSet[set] = registeredNames(controllerRegistrations(nil, configForSets(set), controllerDeps{}))
	}

	for reconciler, name := range reconcilerControllerNames {
		var owners []config.ControllerSet
		for set, names := range perSet {
			if slices.Contains(names, name) {
				owners = append(owners, set)
			}
		}
		if len(owners) != 1 {
			t.Errorf("%s (%q) belongs to %d controller sets, want exactly 1", reconciler, name, len(owners))
		}
	}

	registered := registeredNames(controllerRegistrations(nil, configForSets(config.AllControllerSets()...), controllerDeps{}))
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

func TestSetupWebhooks_LocationOnlyRegistersNothing(t *testing.T) {
	registered, err := setupWebhooks(nil, configForSets(config.ControllerSetCell))
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(registered) != 0 {
		t.Fatalf("expected no webhooks, got %v", registered)
	}
}

func TestWebhookRegistrations_ControlPlaneRegistersEveryWebhook(t *testing.T) {
	want := []string{
		"Backend",
		"BackendTLSPolicy",
		"BackendTrafficPolicy",
		"Domain",
		"Gateway",
		"HTTPProxy",
		"HTTPRoute",
		"HTTPRouteFilter",
		"SecurityPolicy",
	}

	got := registeredNames(webhookRegistrations(nil, configForSets(config.ControllerSetControlPlane)))

	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestLeaderElectionIDForSets(t *testing.T) {
	tests := []struct {
		name string
		sets []config.ControllerSet
		want string
	}{
		{
			name: "every set keeps the historical lease name",
			sets: config.AllControllerSets(),
			want: "6a7d51cc.datumapis.com",
		},
		{
			name: "control-plane only keeps the historical lease name",
			sets: []config.ControllerSet{config.ControllerSetControlPlane},
			want: "6a7d51cc.datumapis.com",
		},
		{
			name: "cell only",
			sets: []config.ControllerSet{config.ControllerSetCell},
			want: "6a7d51cc.datumapis.com-cell",
		},
		{
			name: "order does not change the lease name",
			sets: []config.ControllerSet{config.ControllerSetCell, config.ControllerSetControlPlane},
			want: "6a7d51cc.datumapis.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leaderElectionIDForSets(tt.sets); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}

	if leaderElectionIDForSets([]config.ControllerSet{config.ControllerSetControlPlane}) ==
		leaderElectionIDForSets([]config.ControllerSet{config.ControllerSetCell}) {
		t.Fatal("control-plane and location must not share a lease")
	}
}

var setupWithManagerPattern = regexp.MustCompile(`func \(\w+ \*(\w+)\) SetupWithManager\(`)

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

type stubIPAMClientFactory struct{}

func (stubIPAMClientFactory) ClientForProject(string) (client.Client, error) {
	return nil, nil
}

func TestSetupControllers_LocationOnlyRegistersAgainstAManager(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	log.SetLogger(zap.New(zap.UseDevMode(true)))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	restConfig, err := env.Start()
	if err != nil {
		t.Fatalf("unable to start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	mgr, err := mcmanager.New(restConfig, nil, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("unable to build manager: %v", err)
	}

	registered, err := setupControllers(mgr, configForSets(config.ControllerSetCell), controllerDeps{
		ipamClients: stubIPAMClientFactory{},
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	want := []string{"networkinterfaceclaim", "networkinterface"}
	slices.Sort(registered)
	slices.Sort(want)
	if !slices.Equal(registered, want) {
		t.Fatalf("expected %v, got %v", want, registered)
	}
}
