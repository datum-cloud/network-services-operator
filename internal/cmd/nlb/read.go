// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type detail struct {
	LoadBalancer *api.NetworkLoadBalancer       `json:"loadBalancer"`
	Routes       []api.NetworkLoadBalancerRoute `json:"routes"`
}

func printValue(cmd *cobra.Command, value any, table func() error) error {
	format, err := util.ParseOutputFormat(plugincli.OutputFromCmd(cmd))
	if err != nil {
		return err
	}
	switch format {
	case util.OutputName:
		name := ""
		switch v := value.(type) {
		case client.Object:
			name = v.GetName()
		case detail:
			name = v.LoadBalancer.Name
		case map[string]any:
			name, _ = v["name"].(string)
		}
		if name != "" {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		}
	case util.OutputJSON:
		return util.PrintJSON(cmd.OutOrStdout(), value)
	case util.OutputYAML:
		return util.PrintYAML(cmd.OutOrStdout(), value)
	}
	return table()
}
func readCommand(kind string) *cobra.Command {
	use := kind
	if kind != "list" {
		use += " <name>"
	}
	cmd := &cobra.Command{Use: use, Short: map[string]string{"list": "List Network Load Balancers", "describe": "Show settings, regional endpoints, and problems", "endpoints": "Show public hostnames, addresses, and listener status"}[kind], Args: cobra.ExactArgs(1), ValidArgsFunction: completeNLB}
	if kind == "list" {
		cmd.Args = cobra.NoArgs
		cmd.Aliases = []string{"ls"}
	}
	if kind == "describe" {
		cmd.Aliases = []string{"get", "show"}
	}
	if kind == "endpoints" {
		cmd.Flags().String("location", "", "Filter by location")
		cmd.Flags().String("listener", "", "Filter by listener")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newClient(plugincli.ProjectFromCmd(cmd))
		if err != nil {
			return err
		}
		if kind == "list" {
			var list api.NetworkLoadBalancerList
			if err := c.List(cmd.Context(), &list, client.InNamespace(spec.Namespace)); err != nil {
				return err
			}
			sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
			return printValue(cmd, &list, func() error {
				tw := util.NewTabWriter(cmd.OutOrStdout())
				if plugincli.OutputFromCmd(cmd) != "name" {
					_, _ = fmt.Fprintln(tw, "NAME\tNETWORK\tLISTENERS\tENDPOINTS\tSTATUS")
				}
				for _, n := range list.Items {
					if plugincli.OutputFromCmd(cmd) == "name" {
						_, _ = fmt.Fprintln(tw, n.Name)
					} else {
						_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", n.Name, n.Spec.NetworkRef.Name, len(n.Spec.Listeners), len(n.Status.Endpoints), spec.ConditionText(n.Status.Conditions, n.Generation))
					}
				}
				return tw.Flush()
			})
		}
		n, err := getNLB(cmd, c, args[0])
		if err != nil {
			return err
		}
		if kind == "endpoints" {
			listener := str(cmd, "listener")
			if listener != "" {
				if _, err := findListener(n, listener); err != nil {
					return err
				}
			}
			eps := []api.NetworkLoadBalancerEndpointStatus{}
			for _, e := range n.Status.Endpoints {
				if loc := str(cmd, "location"); loc != "" && loc != e.Location {
					continue
				}
				if listener != "" {
					l, _ := findListener(n, listener)
					if !slices.Contains(l.AddressRefs, e.RequestName) {
						continue
					}
					e.Listeners = slices.DeleteFunc(slices.Clone(e.Listeners), func(s api.NetworkLoadBalancerListenerStatus) bool { return s.Name != listener })
				}
				eps = append(eps, e)
			}
			return printValue(cmd, eps, func() error { return printEndpoints(cmd, n, eps) })
		}
		routes, err := getRoutes(cmd, c, n, "")
		if err != nil {
			return err
		}
		return printValue(cmd, detail{n, routes}, func() error {
			if plugincli.OutputFromCmd(cmd) == "name" {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), n.Name)
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network Load Balancer: %s\nNetwork: %s\nStatus: %s\n", n.Name, n.Spec.NetworkRef.Name, spec.ConditionText(n.Status.Conditions, n.Generation))
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nListeners:")
			if err := printListeners(cmd, n.Spec.Listeners); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nEndpoints:")
			if err := printEndpoints(cmd, n, n.Status.Endpoints); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nRoutes:")
			if err := printRoutes(cmd, routes); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nIndividual NLB probe results: unknown (not exposed by this API).\nUse datumctl network service describe <service> to check membership and ports.")
			return nil
		})
	}
	return cmd
}
func ports(l api.NetworkLoadBalancerListener) string {
	if l.PortRange != nil {
		return fmt.Sprintf("%d-%d", l.PortRange.Start, l.PortRange.End)
	}
	return fmt.Sprint(l.Port)
}
func printListeners(cmd *cobra.Command, list []api.NetworkLoadBalancerListener) error {
	if plugincli.OutputFromCmd(cmd) == "name" {
		for _, l := range list {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), l.Name); err != nil {
				return err
			}
		}
		return nil
	}
	tw := util.NewTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(tw, "NAME\tPROTOCOL\tPORTS\tADDRESSES")
	for _, l := range list {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", l.Name, l.Protocol, ports(l), strings.Join(l.AddressRefs, ","))
	}
	return tw.Flush()
}
func printEndpoints(cmd *cobra.Command, n *api.NetworkLoadBalancer, eps []api.NetworkLoadBalancerEndpointStatus) error {
	if plugincli.OutputFromCmd(cmd) == "name" {
		for _, e := range eps {
			if e.CanonicalHostname != "" {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), e.CanonicalHostname); err != nil {
					return err
				}
			}
		}
		return nil
	}
	tw := util.NewTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(tw, "ADDRESS\tLOCATION\tHOSTNAME\tFAMILY\tIP\tANNOUNCED\tDNS\tREMOVING\tPROBLEM")
	for _, e := range eps {
		for _, a := range e.Addresses {
			problem := a.Reason
			if a.Message != "" {
				problem += ": " + a.Message
			}
			if !spec.Current(a.Conditions, "Announced", n.Generation) {
				problem = "Awaiting current announcement; " + problem
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\t%t\t%t\t%s\n", e.RequestName, e.Location, util.OrDash(e.CanonicalHostname), a.Family, util.OrDash(a.Value), a.Announced, a.DNSPublished, a.Retiring || n.DeletionTimestamp != nil, problem)
		}
		for _, l := range e.Listeners {
			_, _ = fmt.Fprintf(tw, "  Listener %s\t%s\t%s\n", l.Name, e.Location, spec.ConditionText(l.Conditions, n.Generation))
		}
	}
	return tw.Flush()
}
func printRoutes(cmd *cobra.Command, routes []api.NetworkLoadBalancerRoute) error {
	if plugincli.OutputFromCmd(cmd) == "name" {
		for _, r := range routes {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), r.Name); err != nil {
				return err
			}
		}
		return nil
	}
	tw := util.NewTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(tw, "NAME\tPARENT\tLISTENER\tBACKEND\tHEALTH\tEFFECTIVE\tSTATUS")
	for _, r := range routes {
		for _, p := range r.Spec.ParentRefs {
			backend, health := "", "default TCP"
			for _, b := range r.Spec.Backends {
				if backend != "" {
					backend += ", "
				}
				backend += b.NetworkService.Name + ":" + b.NetworkService.Port
				if b.HealthCheck != nil {
					health = b.HealthCheck.Protocol + ":" + b.HealthCheck.Port + b.HealthCheck.Path
				}
			}
			effective, status := "unknown", "Waiting for status"
			listener := p.Listener
			if p.Port != nil {
				listener = fmt.Sprint(*p.Port)
			}
			for _, ps := range r.Status.Parents {
				if ps.Name == p.Name && (ps.Listener == p.Listener || p.Port != nil) {
					effective = fmt.Sprint(ps.Effective)
					status = spec.ConditionText(ps.Conditions, r.Generation)
				}
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, p.Name, listener, backend, health, effective, status)
		}
	}
	return tw.Flush()
}
func getNLB(cmd *cobra.Command, c client.Client, name string) (*api.NetworkLoadBalancer, error) {
	n := &api.NetworkLoadBalancer{}
	if err := c.Get(cmd.Context(), key(name), n); err != nil {
		return nil, util.ClassifyError(fmt.Errorf("reading Network Load Balancer %q: %w", name, err))
	}
	return n, nil
}
func getRoutes(cmd *cobra.Command, c client.Client, n *api.NetworkLoadBalancer, listener string) ([]api.NetworkLoadBalancerRoute, error) {
	var list api.NetworkLoadBalancerRouteList
	if err := c.List(cmd.Context(), &list, client.InNamespace(spec.Namespace)); err != nil {
		return nil, err
	}
	result := []api.NetworkLoadBalancerRoute{}
	for _, r := range list.Items {
		if spec.Attached(&r, n, listener) {
			result = append(result, r)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func findListener(n *api.NetworkLoadBalancer, name string) (api.NetworkLoadBalancerListener, error) {
	for _, l := range n.Spec.Listeners {
		if l.Name == name {
			return l, nil
		}
	}
	return api.NetworkLoadBalancerListener{}, util.UsageErrorf("listener %q not found", name)
}
