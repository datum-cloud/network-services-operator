// SPDX-License-Identifier: AGPL-3.0-only
package spec

import (
	"fmt"
	"slices"

	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Current(conditions []metav1.Condition, kind string, generation int64) bool {
	for _, c := range conditions {
		if c.Type == kind {
			return c.Status == metav1.ConditionTrue && c.ObservedGeneration == generation
		}
	}
	return false
}
func ConditionText(conditions []metav1.Condition, generation int64) string {
	for _, c := range conditions {
		if c.ObservedGeneration != generation {
			return "Waiting for latest settings"
		}
		if c.Type == "Degraded" {
			if c.Status == metav1.ConditionTrue {
				return c.Reason + ": " + c.Message
			}
			continue
		}
		if c.Status != metav1.ConditionTrue {
			return c.Reason + ": " + c.Message
		}
	}
	if len(conditions) == 0 {
		return "Waiting for status"
	}
	return "Ready"
}
func Attached(r *api.NetworkLoadBalancerRoute, n *api.NetworkLoadBalancer, listener string) bool {
	for _, p := range r.Spec.ParentRefs {
		if p.Name != n.Name || (p.Namespace != "" && p.Namespace != n.Namespace) {
			continue
		}
		if listener == "" {
			return true
		}
		for _, l := range n.Spec.Listeners {
			if l.Name == listener && (p.Listener == listener || p.Port != nil && l.PortRange == nil && *p.Port == l.Port) {
				return true
			}
		}
	}
	return false
}
func RouteReady(r *api.NetworkLoadBalancerRoute, n, listener string) bool {
	if r.DeletionTimestamp != nil {
		return false
	}
	for _, p := range r.Status.Parents {
		if p.Name == n && p.Listener == listener && p.Effective && Current(p.Conditions, "Accepted", r.Generation) && Current(p.Conditions, "ResolvedRefs", r.Generation) && Current(p.Conditions, "Programmed", r.Generation) {
			return true
		}
	}
	return false
}

// Ready requires all currently selected locations and families, not merely an
// NLB-wide Ready condition. Shared addresses never imply a listener is serving.
func Ready(n *api.NetworkLoadBalancer, routes []api.NetworkLoadBalancerRoute, listener string) (bool, string) {
	return ready(n, routes, listener, nil)
}

// ReadyForRoute checks the route saved by the command, not any other route that
// happens to serve the same listener. Its identity and generation must still
// match the write response, and status must describe that generation.
func ReadyForRoute(n *api.NetworkLoadBalancer, routes []api.NetworkLoadBalancerRoute, listener string, expected *api.NetworkLoadBalancerRoute) (bool, string) {
	return ready(n, routes, listener, expected)
}

func ready(n *api.NetworkLoadBalancer, routes []api.NetworkLoadBalancerRoute, listener string, expected *api.NetworkLoadBalancerRoute) (bool, string) {
	if n.DeletionTimestamp != nil {
		return false, "Removing public access"
	}
	if expected != nil {
		var target *api.NetworkLoadBalancerRoute
		for i := range routes {
			r := &routes[i]
			if r.Name == expected.Name && r.Namespace == expected.Namespace {
				target = r
				break
			}
		}
		if target == nil {
			return false, fmt.Sprintf("Waiting for route %s", expected.Name)
		}
		if target.UID != expected.UID || target.Generation != expected.Generation {
			return false, fmt.Sprintf("Route %s changed while waiting; inspect its current settings", expected.Name)
		}
		if target.DeletionTimestamp != nil {
			return false, fmt.Sprintf("Route %s is being removed", expected.Name)
		}
		if !Attached(target, n, listener) {
			return false, fmt.Sprintf("Route %s is no longer attached to listener %s", expected.Name, listener)
		}
		for _, p := range target.Status.Parents {
			if p.Name == n.Name && p.Listener == listener && !p.Effective && Current(p.Conditions, "Accepted", target.Generation) {
				return false, fmt.Sprintf("Route %s is not effective on listener %s; another route may take precedence", expected.Name, listener)
			}
		}
		routes = []api.NetworkLoadBalancerRoute{*target}
	}
	for _, l := range n.Spec.Listeners {
		if listener != "" && l.Name != listener {
			continue
		}
		routeReady := false
		for i := range routes {
			if Attached(&routes[i], n, l.Name) && RouteReady(&routes[i], n.Name, l.Name) {
				routeReady = true
				break
			}
		}
		if !routeReady {
			return false, "Waiting for route on listener " + l.Name
		}
		for _, ref := range l.AddressRefs {
			var locations []string
			for _, a := range n.Status.AddressRequests {
				if a.Name == ref {
					locations = a.ResolvedLocations
				}
			}
			if len(locations) == 0 {
				return false, "Waiting for locations on address " + ref
			}
			var families []api.IPFamily
			for _, a := range n.Spec.Exposure.Addresses {
				if a.Name == ref {
					families = a.Families
					if a.Locations != nil && a.Locations.From == api.NetworkLoadBalancerLocationSourceNames {
						for _, loc := range a.Locations.Names {
							if !slices.Contains(locations, loc) {
								return false, "Waiting for location " + loc
							}
						}
					}
				}
			}
			if len(families) == 0 {
				return false, "Waiting for address " + ref
			}
			for _, loc := range locations {
				var ep *api.NetworkLoadBalancerEndpointStatus
				for i := range n.Status.Endpoints {
					e := &n.Status.Endpoints[i]
					if e.RequestName == ref && e.Location == loc {
						ep = e
						break
					}
				}
				if ep == nil {
					return false, "Waiting for endpoint in " + loc
				}
				listenerReady := false
				for _, ls := range ep.Listeners {
					if ls.Name == l.Name {
						listenerReady = Current(ls.Conditions, "Programmed", n.Generation) && Current(ls.Conditions, "Announced", n.Generation)
					}
				}
				if !listenerReady {
					return false, fmt.Sprintf("Waiting for listener %s in %s", l.Name, loc)
				}
				for _, family := range families {
					ready := false
					for _, a := range ep.Addresses {
						if a.Family == family {
							// WithdrawalPending is a publication hold, armed even while
							// an address serves normally. Retiring indicates removal.
							ready = a.Value != "" && a.Announced && a.DNSPublished && !a.Retiring && ep.CanonicalHostname != "" && Current(a.Conditions, "Announced", n.Generation)
						}
					}
					if !ready {
						return false, fmt.Sprintf("Waiting for %s address and DNS in %s", family, loc)
					}
				}
			}
		}
	}
	if listener != "" {
		found := false
		for _, l := range n.Spec.Listeners {
			found = found || l.Name == listener
		}
		if !found {
			return false, "Listener not found"
		}
	}
	if len(n.Spec.Listeners) == 0 {
		return false, "Waiting for listeners"
	}
	return true, "Ready"
}
