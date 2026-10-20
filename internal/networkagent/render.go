// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	validationpkg "go.datum.net/network-services-operator/internal/validation"
)

const (
	renderNamespace = "default"
	defaultMTU      = 1440
	minMTU          = 1300
	maxMTU          = 8856
	maxPorts        = 16

	baseToolResourcesList = "resources_list"
	baseToolResourcesPlan = "resources_plan"
)

var portNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type RenderPort struct {
	Name        string `json:"name" jsonschema:"Port name, a lowercase DNS label such as \"http\". A load balancer names this, not the number, so pick something that says what it carries."`
	Port        int32  `json:"port" jsonschema:"Port number the workload's instances listen on, 1 to 65535. This is the port on the instance itself, not one Datum publishes."`
	AppProtocol string `json:"appProtocol,omitempty" jsonschema:"Leave unset for HTTP/1.1, which suits most servers. Set h2c for servers that speak HTTP/2, such as gRPC servers. Every request on the port then reaches them as HTTP/2, so WebSockets belong on a different port."`
}

// AppProtocol returns the API value for a port's application protocol, given
// either the short name h2c or the value the API stores. Empty means HTTP/1.1.
func AppProtocol(value string) (string, error) {
	switch value {
	case "":
		return "", nil
	case "h2c", string(networkingv1alpha.NetworkServiceAppProtocolH2C):
		return string(networkingv1alpha.NetworkServiceAppProtocolH2C), nil
	}
	return "", fmt.Errorf("app protocol %q is not supported: use h2c for HTTP/2, or leave it unset for HTTP/1.1", value)
}

// AppProtocolLabel is how a port's application protocol is shown to a person.
func AppProtocolLabel(value string) string {
	return strings.TrimPrefix(value, "kubernetes.io/")
}

type RenderInput struct {
	Kind        string            `json:"kind" jsonschema:"Network or NetworkService."`
	Name        string            `json:"name" jsonschema:"Name, a lowercase DNS label such as \"backend\". Cannot be changed later."`
	MTU         int32             `json:"mtu,omitempty" jsonschema:"Network only. Largest packet the network carries, 1300 to 8856. Leave unset for 1440, which is right unless the person has a specific reason: larger values can hang connections between locations."`
	Workload    string            `json:"workload,omitempty" jsonschema:"NetworkService only. The workload whose instances answer for this service. Set this or matchLabels, not both."`
	MatchLabels map[string]string `json:"matchLabels,omitempty" jsonschema:"NetworkService only. Labels an instance's network interface must carry to belong to the service, for selecting across workloads. Set this or workload, not both."`
	Ports       []RenderPort      `json:"ports,omitempty" jsonschema:"NetworkService only. The ports the instances answer on, 1 to 16, names and numbers each unique."`
}

type RenderOutput struct {
	Manifest string   `json:"manifest"`
	Notes    []string `json:"notes,omitempty"`
}

func networkRender(deps DepsFor) mcp.ToolHandlerFor[RenderInput, RenderOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenderInput) (*mcp.CallToolResult, RenderOutput, error) {
		if _, err := deps(ctx); err != nil {
			return nil, RenderOutput{}, err
		}
		doc, notes, err := Render(in)
		if err != nil {
			return nil, RenderOutput{}, err
		}
		b, err := yaml.Marshal(doc)
		if err != nil {
			return nil, RenderOutput{}, fmt.Errorf("rendering manifest: %w", err)
		}
		return nil, RenderOutput{Manifest: string(b), Notes: notes}, nil
	}
}

func checkName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("name %q is not usable: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func manifest(kind, name string, spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": networkingv1alpha.GroupVersion.String(),
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": renderNamespace},
		"spec":       spec,
	}
}

func renderNetwork(in RenderInput) (map[string]any, []string, error) {
	if err := checkName(in.Name); err != nil {
		return nil, nil, err
	}
	if in.Workload != "" || len(in.MatchLabels) > 0 || len(in.Ports) > 0 {
		return nil, nil, errors.New("workload, matchLabels and ports belong to a NetworkService, not a Network")
	}
	if in.MTU != 0 && (in.MTU < minMTU || in.MTU > maxMTU) {
		return nil, nil, fmt.Errorf("mtu must be between %d and %d, not %d", minMTU, maxMTU, in.MTU)
	}

	n := networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Name: in.Name, Namespace: renderNamespace},
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			MTU:        in.MTU,
		},
	}
	if errs := validationpkg.ValidateNetwork(&n); len(errs) > 0 {
		return nil, nil, errs.ToAggregate()
	}

	spec := map[string]any{
		"ipam":       map[string]any{"mode": string(networkingv1alpha.NetworkIPAMModeAuto)},
		"ipFamilies": []string{string(networkingv1alpha.IPv6Protocol)},
	}
	notes := []string{
		"The network carries IPv6 only. Datum does not accept IPv4 on a new network, so do not offer it.",
		"An IPv6 /48 is given to the network when it is created, and each location it reaches gets its own " +
			"range from it. There is nothing to choose.",
		"Creating the network places it nowhere. It reaches a location when a workload placed there attaches " +
			"to it, so an unused network shows no locations and that is normal.",
		fmt.Sprintf("The name is final. Check it is not already taken with %s, then pass this manifest to %s.",
			baseToolResourcesList, baseToolResourcesPlan),
	}
	if in.MTU == 0 {
		notes = append(notes, fmt.Sprintf("No MTU was given, so the network uses %d.", defaultMTU))
	} else {
		spec["mtu"] = in.MTU
		if in.MTU > defaultMTU {
			notes = append(notes, fmt.Sprintf("An MTU of %d is above the default of %d. Traffic between "+
				"locations is wrapped in an extra header, and some paths drop larger packets silently, so "+
				"connections can hang rather than fail. Confirm the person wants this.", in.MTU, defaultMTU))
		}
	}
	return manifest(KindNetwork, in.Name, spec), notes, nil
}

func renderService(in RenderInput) (map[string]any, []string, error) {
	if err := checkName(in.Name); err != nil {
		return nil, nil, err
	}
	if in.MTU != 0 {
		return nil, nil, errors.New("mtu belongs to a Network, not a NetworkService")
	}
	if (in.Workload == "") == (len(in.MatchLabels) == 0) {
		return nil, nil, errors.New("set exactly one of workload or matchLabels: a service has to select something, and one way of saying it")
	}
	if len(in.Ports) == 0 || len(in.Ports) > maxPorts {
		return nil, nil, fmt.Errorf("a service needs between 1 and %d ports, not %d", maxPorts, len(in.Ports))
	}

	names, numbers := map[string]bool{}, map[int32]bool{}
	ports := make([]map[string]any, 0, len(in.Ports))
	for _, p := range in.Ports {
		if len(p.Name) > validation.DNS1123LabelMaxLength || !portNamePattern.MatchString(p.Name) {
			return nil, nil, fmt.Errorf("port name %q must be a lowercase DNS label", p.Name)
		}
		if p.Port < 1 || p.Port > 65535 {
			return nil, nil, fmt.Errorf("port %q must be between 1 and 65535, not %d", p.Name, p.Port)
		}
		if names[p.Name] {
			return nil, nil, fmt.Errorf("port name %q is used twice", p.Name)
		}
		if numbers[p.Port] {
			return nil, nil, fmt.Errorf("port number %d is used twice", p.Port)
		}
		appProtocol, err := AppProtocol(p.AppProtocol)
		if err != nil {
			return nil, nil, fmt.Errorf("port %q: %w", p.Name, err)
		}
		names[p.Name], numbers[p.Port] = true, true
		port := map[string]any{"name": p.Name, "port": p.Port}
		if appProtocol != "" {
			port["appProtocol"] = appProtocol
		}
		ports = append(ports, port)
	}

	matchLabels := in.MatchLabels
	notes := []string{
		"Traffic is served from the location nearest whoever is asking, falling back to the next nearest. " +
			"That is the only behaviour today and needs no setting.",
		"A service only reaches instances on one network. If what it selects spans two, it serves neither.",
		fmt.Sprintf("Pass this manifest to %s. The service can be created before the workload behind it exists; "+
			"it serves once matching instances report themselves ready.", baseToolResourcesPlan),
	}
	if in.Workload != "" {
		if errs := validation.IsValidLabelValue(in.Workload); len(errs) > 0 {
			return nil, nil, fmt.Errorf("workload %q is not usable: %s", in.Workload, strings.Join(errs, "; "))
		}
		matchLabels = map[string]string{WorkloadNameLabel: in.Workload}
		notes = append(notes, fmt.Sprintf("The service selects every instance of workload %q, in every "+
			"location it runs, including ones added later.", in.Workload))
	} else {
		for k, v := range matchLabels {
			if errs := validation.IsQualifiedName(k); len(errs) > 0 {
				return nil, nil, fmt.Errorf("label key %q is not usable: %s", k, strings.Join(errs, "; "))
			}
			if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
				return nil, nil, fmt.Errorf("label value %q is not usable: %s", v, strings.Join(errs, "; "))
			}
		}
		notes = append(notes, "The service selects by label rather than by workload. Check those labels are "+
			"on the instances' network interfaces, not only on the workload, or nothing will match.")
	}

	spec := map[string]any{
		"networkInterfaces": map[string]any{"selector": map[string]any{"matchLabels": matchLabels}},
		"ports":             ports,
	}
	notes = append(notes, "A load balancer reaches this service by its name and a port name, so tell the person "+
		"the port names.")
	return manifest(KindNetworkService, in.Name, spec), notes, nil
}
