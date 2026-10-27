// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func listenerCommand() *cobra.Command {
	root := &cobra.Command{Use: "listener", Short: "Manage public ports and UDP ranges"}
	for _, verb := range []string{"list", "add", "update", "remove"} {
		v := verb
		use := v + " <nlb> <listener>"
		args := cobra.ExactArgs(2)
		if v == "list" {
			use = v + " <nlb>"
			args = cobra.ExactArgs(1)
		}
		cmd := &cobra.Command{Use: use, Short: strings.ToUpper(v[:1]) + v[1:] + " listeners", Args: args, RunE: func(cmd *cobra.Command, args []string) error { return runListener(cmd, args, v) }}
		if v != "list" {
			mutationFlags(cmd)
		}
		if v == "add" || v == "update" {
			waitFlags(cmd)
			listenerFlags(cmd)
		}
		if v == "add" {
			backendFlags(cmd)
		}
		cmd.ValidArgsFunction = completeManaged
		root.AddCommand(cmd)
	}
	return root
}
func runListener(cmd *cobra.Command, args []string, verb string) error {
	if verb == "add" || verb == "update" {
		if err := checkTimeout(cmd); err != nil {
			return err
		}
	}
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	n, err := getNLB(cmd, c, args[0])
	if err != nil {
		return err
	}
	if verb == "list" {
		return printValue(cmd, n.Spec.Listeners, func() error { return printListeners(cmd, n.Spec.Listeners) })
	}
	name := args[1]
	before := n.DeepCopy()
	routes, err := getRoutes(cmd, c, n, name)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(n.Spec.Listeners, func(l api.NetworkLoadBalancerListener) bool { return l.Name == name })
	switch verb {
	case "add":
		l, err := listenerFromFlags(cmd, name)
		if err != nil {
			return util.UsageErrorf("%s", err)
		}
		b, err := backendFromFlags(cmd)
		if err != nil {
			return util.UsageErrorf("%s", err)
		}
		if index >= 0 && !reflect.DeepEqual(n.Spec.Listeners[index], l) {
			return util.UsageErrorf("listener %q already exists with different settings", name)
		}
		if index < 0 {
			n.Spec.Listeners = append(n.Spec.Listeners, l)
		}
		if err := spec.ValidateNLB(n); err != nil {
			return util.UsageErrorf("%s", err)
		}
		if err := preflight(cmd, c, n, l, b); err != nil {
			return err
		}
		r := spec.NewRoute(n, l, b)
		var prior api.NetworkLoadBalancerRoute
		routeErr := c.Get(cmd.Context(), key(r.Name), &prior)
		if routeErr == nil {
			if !spec.Owned(&prior, n) || !reflect.DeepEqual(prior.Spec, r.Spec) {
				return util.UsageErrorf("route %q already exists with different settings", r.Name)
			}
		} else if !apierrors.IsNotFound(routeErr) {
			return routeErr
		}
		for _, attached := range routes {
			if attached.Name != r.Name {
				return util.UsageErrorf("listener already has route %q; inspect it before adding another", attached.Name)
			}
		}
		if err := patch(cmd, c, before, n); err != nil {
			return err
		}
		if routeErr != nil {
			if err := createObject(cmd, c, r); err != nil {
				return partial(n.Name, "listener saved; route "+r.Name+" could not be created", err)
			}
		} else {
			r = &prior
		}
		if err := waitReady(cmd, c, n, name, r); err != nil {
			return err
		}
		return saved(cmd, detail{n, []api.NetworkLoadBalancerRoute{*r}}, name)
	case "update":
		if index < 0 {
			return util.UsageErrorf("listener %q not found", name)
		}
		if len(routes) != 1 {
			return util.UsageErrorf("listener %q must have exactly one route for this update; inspect its routes first", name)
		}
		l := n.Spec.Listeners[index]
		if l.TLS != nil || l.Hostname != "" {
			return util.UsageErrorf("this listener has advanced settings; edit it directly")
		}
		changed := false
		if cmd.Flags().Changed("port") {
			l.Port, _ = cmd.Flags().GetInt32("port")
			l.PortRange = nil
			changed = true
		}
		if cmd.Flags().Changed("port-range") {
			l.PortRange, err = spec.ParseRange(str(cmd, "port-range"))
			if err != nil {
				return err
			}
			l.Port = 0
			changed = true
		}
		if cmd.Flags().Changed("protocol") {
			l.Protocol = api.NetworkLoadBalancerProtocol(strings.ToUpper(str(cmd, "protocol")))
			changed = true
		}
		if cmd.Flags().Changed("address") {
			l.AddressRefs, _ = cmd.Flags().GetStringSlice("address")
			changed = true
		}
		if !changed {
			return util.UsageErrorf("no listener settings were supplied")
		}
		if _, err := spec.Listener(l.Name, string(l.Protocol), l.Port, l.PortRange, l.AddressRefs); err != nil {
			return util.UsageErrorf("%s", err)
		}
		for _, r := range routes {
			if !spec.Owned(&r, n) || len(r.Spec.Backends) != 1 {
				return util.UsageErrorf("route %q is managed outside this listener workflow; edit it directly", r.Name)
			}
			if err := preflight(cmd, c, n, l, r.Spec.Backends[0]); err != nil {
				return err
			}
		}
		n.Spec.Listeners[index] = l
		if err := spec.ValidateNLB(n); err != nil {
			return util.UsageErrorf("%s", err)
		}
		if err := patch(cmd, c, before, n); err != nil {
			return err
		}
		if err := waitReady(cmd, c, n, name, &routes[0]); err != nil {
			return err
		}
		return saved(cmd, n, name)
	case "remove":
		if index < 0 {
			return util.UsageErrorf("listener %q not found", name)
		}
		if len(n.Spec.Listeners) == 1 {
			return util.UsageErrorf("cannot remove the last listener; delete the load balancer instead")
		}
		for _, r := range routes {
			if !spec.Owned(&r, n) {
				return util.UsageErrorf("listener has externally managed route %q; detach it first", r.Name)
			}
		}
		message := fmt.Sprintf("Remove listener %q and its %d owned routes? Other listeners and backend services are kept.", name, len(routes))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
		if err := confirm(cmd, message, name); err != nil {
			return err
		}
		// Validate all deletes before changing anything. Then remove routes first so
		// failures leave a retryable listener rather than orphaning active attachments.
		for i := range routes {
			if err := c.Delete(cmd.Context(), &routes[i], client.DryRunAll); err != nil {
				return err
			}
		}
		for i := range routes {
			if err := deleteObject(cmd, c, &routes[i]); err != nil {
				return partial(n.Name, "some route removals may have completed", err)
			}
		}
		n.Spec.Listeners = slices.Delete(n.Spec.Listeners, index, index+1)
		if err := patch(cmd, c, before, n); err != nil {
			return partial(n.Name, "routes removed; listener removal did not complete", err)
		}
		return saved(cmd, n, name)
	}
	return nil
}
func addressCommand() *cobra.Command {
	root := &cobra.Command{Use: "address", Short: "Manage public address requests and locations"}
	for _, verb := range []string{"list", "add", "update", "remove"} {
		v := verb
		use := v + " <nlb> <request>"
		args := cobra.ExactArgs(2)
		if v == "list" {
			use = v + " <nlb>"
			args = cobra.ExactArgs(1)
		}
		cmd := &cobra.Command{Use: use, Short: strings.ToUpper(v[:1]) + v[1:] + " address requests", Args: args, RunE: func(cmd *cobra.Command, args []string) error { return runAddress(cmd, args, v) }}
		if v != "list" {
			mutationFlags(cmd)
		}
		if v == "add" || v == "update" {
			addressFlags(cmd)
		}
		cmd.ValidArgsFunction = completeManaged
		root.AddCommand(cmd)
	}
	return root
}
func runAddress(cmd *cobra.Command, args []string, verb string) error {
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	n, err := getNLB(cmd, c, args[0])
	if err != nil {
		return err
	}
	if verb == "list" {
		return printValue(cmd, n.Spec.Exposure.Addresses, func() error {
			if plugincli.OutputFromCmd(cmd) == "name" {
				for _, a := range n.Spec.Exposure.Addresses {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), a.Name); err != nil {
						return err
					}
				}
				return nil
			}
			tw := util.NewTabWriter(cmd.OutOrStdout())
			_, _ = fmt.Fprintln(tw, "NAME\tFAMILIES\tLOCATIONS")
			for _, a := range n.Spec.Exposure.Addresses {
				loc := "Backends"
				if a.Locations != nil {
					loc = string(a.Locations.From)
					if len(a.Locations.Names) > 0 {
						loc = strings.Join(a.Locations.Names, ",")
					}
				}
				_, _ = fmt.Fprintf(tw, "%s\t%v\t%s\n", a.Name, a.Families, loc)
			}
			return tw.Flush()
		})
	}
	before := n.DeepCopy()
	name := args[1]
	index := slices.IndexFunc(n.Spec.Exposure.Addresses, func(a api.NetworkLoadBalancerAddressRequest) bool { return a.Name == name })
	switch verb {
	case "add":
		if index >= 0 {
			return util.UsageErrorf("address request %q already exists", name)
		}
		a, err := addressFromFlags(cmd, name)
		if err != nil {
			return util.UsageErrorf("%s", err)
		}
		n.Spec.Exposure.Addresses = append(n.Spec.Exposure.Addresses, a)
	case "update":
		if index < 0 {
			return util.UsageErrorf("address request %q not found", name)
		}
		a := n.Spec.Exposure.Addresses[index]
		if !cmd.Flags().Changed("families") && !cmd.Flags().Changed("location") && !cmd.Flags().Changed("location-source") {
			return util.UsageErrorf("no address settings were supplied")
		}
		parsed, err := addressFromFlags(cmd, name)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("families") {
			a.Families = parsed.Families
		}
		if cmd.Flags().Changed("location") || cmd.Flags().Changed("location-source") {
			a.Locations = parsed.Locations
		}
		var affected []string
		for _, l := range n.Spec.Listeners {
			if slices.Contains(l.AddressRefs, name) {
				affected = append(affected, l.Name)
			}
		}
		message := fmt.Sprintf("Change address %q from %v to %v? This may release IPs and affect listeners %v.", name, n.Spec.Exposure.Addresses[index], a, affected)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
		if err := confirm(cmd, message, name); err != nil {
			return err
		}
		n.Spec.Exposure.Addresses[index] = a
	case "remove":
		if index < 0 {
			return util.UsageErrorf("address request %q not found", name)
		}
		for _, l := range n.Spec.Listeners {
			if slices.Contains(l.AddressRefs, name) {
				return util.UsageErrorf("address %q is still used by listener %q", name, l.Name)
			}
		}
		if err := confirm(cmd, "Remove address request "+name+" and release its public IPs?", name); err != nil {
			return err
		}
		n.Spec.Exposure.Addresses = slices.Delete(n.Spec.Exposure.Addresses, index, index+1)
	}
	if err := spec.ValidateNLB(n); err != nil {
		return util.UsageErrorf("%s", err)
	}
	if err := patch(cmd, c, before, n); err != nil {
		return err
	}
	return saved(cmd, n, name)
}
func routeCommand() *cobra.Command {
	root := &cobra.Command{Use: "route", Short: "Inspect or change where listener traffic goes"}
	for _, verb := range []string{"list", "describe", "update"} {
		v := verb
		use := v + " <route-name>"
		if v == "list" {
			use = v + " <nlb>"
		}
		cmd := &cobra.Command{Use: use, Short: strings.ToUpper(v[:1]) + v[1:] + " routes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runRoute(cmd, args, v) }}
		if v == "update" {
			backendFlags(cmd)
			mutationFlags(cmd)
			waitFlags(cmd)
		}
		cmd.ValidArgsFunction = completeManaged
		root.AddCommand(cmd)
	}
	return root
}
func runRoute(cmd *cobra.Command, args []string, verb string) error {
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	if verb == "list" {
		n, err := getNLB(cmd, c, args[0])
		if err != nil {
			return err
		}
		routes, err := getRoutes(cmd, c, n, "")
		if err != nil {
			return err
		}
		return printValue(cmd, routes, func() error { return printRoutes(cmd, routes) })
	}
	r := &api.NetworkLoadBalancerRoute{}
	if err := c.Get(cmd.Context(), key(args[0]), r); err != nil {
		return err
	}
	if verb == "describe" {
		return printValue(cmd, r, func() error { return printRoutes(cmd, []api.NetworkLoadBalancerRoute{*r}) })
	}
	if err := checkTimeout(cmd); err != nil {
		return err
	}
	if len(r.Spec.ParentRefs) != 1 || len(r.Spec.Backends) != 1 || len(r.Spec.SNIHostnames) > 0 || r.Spec.Backends[0].Transport != nil {
		return util.UsageErrorf("this route has advanced settings; edit it directly")
	}
	parent := r.Spec.ParentRefs[0]
	if parent.Listener == "" || parent.Namespace != "" && parent.Namespace != spec.Namespace {
		return util.UsageErrorf("this route uses an advanced attachment; edit it directly")
	}
	n, err := getNLB(cmd, c, parent.Name)
	if err != nil {
		return err
	}
	if !spec.Owned(r, n) {
		return util.UsageErrorf("route %q is managed outside this plugin; edit it directly", r.Name)
	}
	l, err := findListener(n, parent.Listener)
	if err != nil {
		return err
	}
	before := r.DeepCopy()
	b := r.Spec.Backends[0]
	changed := false
	if cmd.Flags().Changed("network-service") {
		b.NetworkService.Name = str(cmd, "network-service")
		changed = true
	}
	if cmd.Flags().Changed("backend-port") {
		b.NetworkService.Port = str(cmd, "backend-port")
		changed = true
	}
	hp, hport, path := "", "", ""
	if b.HealthCheck != nil {
		hp = b.HealthCheck.Protocol
		hport = b.HealthCheck.Port
		path = b.HealthCheck.Path
	}
	healthChanged := false
	for _, f := range []string{"health-protocol", "health-port", "health-path"} {
		if cmd.Flags().Changed(f) {
			changed = true
			healthChanged = true
			switch f {
			case "health-protocol":
				hp = str(cmd, f)
			case "health-port":
				hport = str(cmd, f)
			case "health-path":
				path = str(cmd, f)
			}
		}
	}
	if !changed {
		return util.UsageErrorf("no route settings were supplied")
	}
	validated, err := spec.Backend(b.NetworkService.Name, b.NetworkService.Port, hp, hport, path)
	if err != nil {
		return util.UsageErrorf("%s", err)
	}
	if healthChanged {
		b.HealthCheck = validated.HealthCheck
	}
	if err := preflight(cmd, c, n, l, b); err != nil {
		return err
	}
	r.Spec.Backends[0] = b
	if err := patch(cmd, c, before, r); err != nil {
		return err
	}
	if err := waitReady(cmd, c, n, l.Name, r); err != nil {
		return err
	}
	return saved(cmd, r, r.Name)
}
