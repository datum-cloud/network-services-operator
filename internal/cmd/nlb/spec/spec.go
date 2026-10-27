// SPDX-License-Identifier: AGPL-3.0-only

// Package spec builds and checks NLB resources without loading CLI credentials.
package spec

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Defaults remain provisional until the product decisions are settled.
const DefaultFamilies = "ipv6"
const DefaultAddress = "public"
const DefaultTimeout = 2 * time.Minute
const Namespace = "default"
const ManagedBy = "datumctl-nlb"
const DisplayName = "kubernetes.io/display-name"

func Name(name string) error {
	if errors := validation.IsDNS1123Label(name); len(errors) > 0 {
		return fmt.Errorf("name %q: %s", name, strings.Join(errors, "; "))
	}
	return nil
}
func ParseRange(value string) (*api.PortRange, error) {
	start, end, ok := strings.Cut(value, "-")
	if !ok {
		return nil, fmt.Errorf("port range must be START-END")
	}
	a, e1 := strconv.ParseInt(start, 10, 32)
	b, e2 := strconv.ParseInt(end, 10, 32)
	if e1 != nil || e2 != nil || a < 1 || b > 65535 || a > b {
		return nil, fmt.Errorf("port range must be ordered and between 1 and 65535")
	}
	return &api.PortRange{Start: int32(a), End: int32(b)}, nil
}
func Address(name, families, source string, locations []string) (api.NetworkLoadBalancerAddressRequest, error) {
	a := api.NetworkLoadBalancerAddressRequest{Name: name}
	if err := Name(name); err != nil {
		return a, err
	}
	for _, f := range strings.Split(families, ",") {
		var family api.IPFamily
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "ipv4":
			family = api.IPv4Protocol
		case "ipv6":
			family = api.IPv6Protocol
		default:
			return a, fmt.Errorf("families must be ipv4, ipv6, or ipv4,ipv6")
		}
		if slices.Contains(a.Families, family) {
			return a, fmt.Errorf("IP versions must not repeat")
		}
		a.Families = append(a.Families, family)
	}
	if len(locations) > 0 {
		if source != "" && source != "names" {
			return a, fmt.Errorf("choose --location or --location-source, not both")
		}
		a.Locations = &api.NetworkLoadBalancerLocationSelection{From: api.NetworkLoadBalancerLocationSourceNames, Names: locations}
		seen := map[string]bool{}
		for _, loc := range locations {
			if seen[loc] {
				return a, fmt.Errorf("location %q is repeated", loc)
			}
			seen[loc] = true
			if err := Name(loc); err != nil {
				return a, err
			}
		}
	} else {
		switch strings.ToLower(source) {
		case "", "backends":
		case "all":
			a.Locations = &api.NetworkLoadBalancerLocationSelection{From: api.NetworkLoadBalancerLocationSourceAll}
		default:
			return a, fmt.Errorf("location source must be backends or all")
		}
	}
	return a, nil
}
func Listener(name, protocol string, port int32, portRange *api.PortRange, addresses []string) (api.NetworkLoadBalancerListener, error) {
	l := api.NetworkLoadBalancerListener{Name: name, Protocol: api.NetworkLoadBalancerProtocol(strings.ToUpper(protocol)), Port: port, PortRange: portRange, AddressRefs: addresses}
	if err := Name(name); err != nil {
		return l, err
	}
	if l.Protocol != "TCP" && l.Protocol != "UDP" {
		return l, fmt.Errorf("protocol must be tcp or udp")
	}
	if (port != 0) == (portRange != nil) {
		return l, fmt.Errorf("set exactly one of --port or --port-range")
	}
	interval := api.PortInterval(port, portRange)
	if !interval.Valid() {
		return l, fmt.Errorf("ports must be between 1 and 65535")
	}
	if !interval.Singleton() && l.Protocol != "UDP" {
		return l, fmt.Errorf("port ranges require UDP")
	}
	if len(addresses) == 0 {
		return l, fmt.Errorf("at least one address request is required")
	}
	for _, a := range addresses {
		if err := Name(a); err != nil {
			return l, err
		}
	}
	return l, nil
}
func ValidateNLB(n *api.NetworkLoadBalancer) error {
	if err := Name(n.Name); err != nil {
		return err
	}
	if err := Name(n.Spec.NetworkRef.Name); err != nil {
		return err
	}
	if len(n.Spec.Listeners) < 1 || len(n.Spec.Listeners) > 16 {
		return fmt.Errorf("a load balancer needs 1 to 16 listeners")
	}
	if len(n.Spec.Exposure.Addresses) < 1 || len(n.Spec.Exposure.Addresses) > 16 {
		return fmt.Errorf("a load balancer needs 1 to 16 address requests")
	}
	addresses := map[string]bool{}
	for _, a := range n.Spec.Exposure.Addresses {
		if addresses[a.Name] {
			return fmt.Errorf("duplicate address request %q", a.Name)
		}
		addresses[a.Name] = true
	}
	names := map[string]bool{}
	for i, l := range n.Spec.Listeners {
		if names[l.Name] {
			return fmt.Errorf("duplicate listener %q", l.Name)
		}
		names[l.Name] = true
		for _, a := range l.AddressRefs {
			if !addresses[a] {
				return fmt.Errorf("listener %q uses missing address request %q", l.Name, a)
			}
		}
		for _, other := range n.Spec.Listeners[:i] {
			if (l.Protocol == "UDP") != (other.Protocol == "UDP") {
				continue
			}
			a, b := api.PortInterval(l.Port, l.PortRange), api.PortInterval(other.Port, other.PortRange)
			if a.Start > b.End || b.Start > a.End {
				continue
			}
			for _, ref := range l.AddressRefs {
				if slices.Contains(other.AddressRefs, ref) {
					return fmt.Errorf("listeners %q and %q overlap on address %q", l.Name, other.Name, ref)
				}
			}
		}
	}
	return nil
}
func Backend(service, port, healthProtocol, healthPort, path string) (api.NetworkLoadBalancerRouteBackend, error) {
	b := api.NetworkLoadBalancerRouteBackend{NetworkService: api.NetworkLoadBalancerNetworkServiceRef{Name: service, Port: port}}
	if err := Name(service); err != nil {
		return b, err
	}
	if err := Name(port); err != nil {
		return b, err
	}
	if healthProtocol == "" {
		if healthPort != "" || path != "" {
			return b, fmt.Errorf("--health-protocol is required with health settings")
		}
		return b, nil
	}
	p := strings.ToUpper(healthProtocol)
	if p != "TCP" && p != "HTTP" && p != "HTTPS" {
		return b, fmt.Errorf("health protocol must be tcp, http, or https")
	}
	if p != "TCP" && healthPort == "" {
		return b, fmt.Errorf("HTTP health checks require --health-port")
	}
	if path != "" && (p == "TCP" || !strings.HasPrefix(path, "/")) {
		return b, fmt.Errorf("health path must start with / and requires HTTP or HTTPS")
	}
	b.HealthCheck = &api.NetworkLoadBalancerHealthCheck{Protocol: p, Port: healthPort, Path: path}
	return b, nil
}
func ValidateBackend(l api.NetworkLoadBalancerListener, b api.NetworkLoadBalancerRouteBackend, s *api.NetworkService) error {
	var port *api.NetworkServicePort
	for i := range s.Spec.Ports {
		if s.Spec.Ports[i].Name == b.NetworkService.Port {
			port = &s.Spec.Ports[i]
			break
		}
	}
	if port == nil {
		return fmt.Errorf("service %q has no port named %q", s.Name, b.NetworkService.Port)
	}
	protocol := port.Protocol
	if protocol == "" {
		protocol = "TCP"
	}
	if string(protocol) != string(l.Protocol) {
		return fmt.Errorf("listener protocol %s does not match service port protocol %s", l.Protocol, protocol)
	}
	a, c := api.PortInterval(l.Port, l.PortRange), api.PortInterval(port.Port, port.PortRange)
	if !a.Valid() || !c.Valid() {
		return fmt.Errorf("listener and backend ports must be valid")
	}
	if !a.Singleton() {
		if c.Singleton() || c.Start > a.Start || c.End < a.End {
			return fmt.Errorf("backend UDP range must cover the entire public range without port translation")
		}
	} else if !c.Singleton() {
		return fmt.Errorf("single public ports require single backend ports")
	}
	h := b.HealthCheck
	if h == nil {
		if protocol == "UDP" {
			return fmt.Errorf("UDP requires a health check on a named TCP port")
		}
		return nil
	}
	if h.Port == "" {
		if protocol != "TCP" || h.Protocol != "TCP" {
			return fmt.Errorf("this health check requires --health-port")
		}
		return nil
	}
	for _, p := range s.Spec.Ports {
		if p.Name == h.Port {
			if p.Protocol != "" && p.Protocol != "TCP" || !api.PortInterval(p.Port, p.PortRange).Singleton() {
				return fmt.Errorf("health port must be a single TCP port")
			}
			return nil
		}
	}
	return fmt.Errorf("service %q has no health port named %q", s.Name, h.Port)
}
func RouteName(nlb, listener string) string {
	raw := nlb + "-" + listener
	sum := sha256.Sum256([]byte(nlb + "/" + listener))
	if len(raw) > 45 {
		raw = raw[:45]
	}
	return fmt.Sprintf("%s-%x", strings.TrimRight(raw, "-"), sum[:6])
}
func NewRoute(n *api.NetworkLoadBalancer, l api.NetworkLoadBalancerListener, b api.NetworkLoadBalancerRouteBackend) *api.NetworkLoadBalancerRoute {
	r := &api.NetworkLoadBalancerRoute{TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "NetworkLoadBalancerRoute"}, ObjectMeta: metav1.ObjectMeta{Name: RouteName(n.Name, l.Name), Namespace: n.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": ManagedBy}}, Spec: api.NetworkLoadBalancerRouteSpec{ParentRefs: []api.NetworkLoadBalancerRouteParentRef{{Name: n.Name, Listener: l.Name}}, Backends: []api.NetworkLoadBalancerRouteBackend{b}}}
	if n.UID != "" {
		r.OwnerReferences = []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "NetworkLoadBalancer", Name: n.Name, UID: n.UID}}
	}
	return r
}
func Owned(r *api.NetworkLoadBalancerRoute, n *api.NetworkLoadBalancer) bool {
	if r.Labels["app.kubernetes.io/managed-by"] != ManagedBy || len(r.Spec.ParentRefs) != 1 {
		return false
	}
	p := r.Spec.ParentRefs[0]
	if p.Name != n.Name || p.Namespace != "" && p.Namespace != n.Namespace {
		return false
	}
	return HasOwnerReference(r, n)
}

// HasOwnerReference checks the route's garbage-collection dependency, regardless
// of its current parent attachments or whether the plugin may still edit it.
func HasOwnerReference(r *api.NetworkLoadBalancerRoute, n *api.NetworkLoadBalancer) bool {
	if n.UID == "" || r.Namespace != n.Namespace {
		return false
	}
	for _, o := range r.OwnerReferences {
		gv, err := schema.ParseGroupVersion(o.APIVersion)
		if err == nil && gv.Group == api.GroupVersion.Group && o.Kind == "NetworkLoadBalancer" && o.Name == n.Name && o.UID == n.UID {
			return true
		}
	}
	return false
}
