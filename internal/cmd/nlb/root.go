// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Version = "dev"
var newClient = plugincli.NewClient
var ensureEntitlement = plugincli.EnsureNetworkingEntitlement

func Command() *cobra.Command {
	util.SetBrand(util.Brand{Command: "nlb", ExitPrefix: "NLB", Product: "Network Load Balancers"})
	root := plugin.NewRootCmd("nlb", "Manage Network Load Balancers on Datum Cloud")
	root.SilenceUsage = true
	root.SilenceErrors = true
	if flag := root.PersistentFlags().Lookup("output"); flag != nil {
		flag.Usage = "Output format. One of: table|wide|json|yaml|name"
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "Show underlying errors")
	root.PersistentFlags().BoolP("quiet", "q", false, "Hide progress messages")
	root.PersistentFlags().BoolP("yes", "y", false, "Skip confirmation prompts")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Name() == "version" || strings.HasPrefix(cmd.Name(), "__complete") {
			return nil
		}
		for p := cmd; p != nil; p = p.Parent() {
			if p.Name() == "completion" {
				return nil
			}
		}
		_, err := util.ParseOutputFormat(plugincli.OutputFromCmd(cmd))
		if err != nil {
			return err
		}
		return ensureEntitlement(cmd.Context(), plugincli.ProjectFromCmd(cmd), cmd.InOrStdin(), cmd.ErrOrStderr())
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return util.UsageErrorf("%s", err) })
	root.AddCommand(readCommand("list"), readCommand("describe"), readCommand("endpoints"), createCommand(), updateCommand(), deleteCommand(), listenerCommand(), addressCommand(), routeCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show plugin version without connecting", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return printValue(cmd, map[string]string{"version": Version, "apiVersion": api.GroupVersion.String()}, func() error { _, err := fmt.Fprintf(cmd.OutOrStdout(), "datumctl-nlb %s\n", Version); return err })
	}})
	_ = root.RegisterFlagCompletionFunc("output", plugincli.CompleteEnum("table", "wide", "json", "yaml", "name"))
	wrapArgs(root)
	return root
}
func wrapArgs(cmd *cobra.Command) {
	if cmd.Args != nil {
		original := cmd.Args
		cmd.Args = func(c *cobra.Command, a []string) error {
			if err := original(c, a); err != nil {
				return util.UsageErrorf("%s", err)
			}
			return nil
		}
	}
	for _, c := range cmd.Commands() {
		wrapArgs(c)
	}
}
func str(cmd *cobra.Command, name string) string   { v, _ := cmd.Flags().GetString(name); return v }
func boolean(cmd *cobra.Command, name string) bool { v, _ := cmd.Flags().GetBool(name); return v }
func key(name string) client.ObjectKey {
	return client.ObjectKey{Namespace: spec.Namespace, Name: name}
}
func mutationFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "Validate and preview without saving")
}
func waitFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("no-wait", false, "Return after saving instead of waiting for readiness")
	cmd.Flags().Duration("timeout", spec.DefaultTimeout, "Maximum time to wait")
}
func backendFlags(cmd *cobra.Command) {
	cmd.Flags().String("network-service", "", "Existing backend service")
	cmd.Flags().String("backend-port", "", "Named port on the backend service")
	cmd.Flags().String("health-protocol", "", "Health check: tcp, http, or https")
	cmd.Flags().String("health-port", "", "Named TCP health port on the service")
	cmd.Flags().String("health-path", "", "HTTP health-check path")
	_ = cmd.RegisterFlagCompletionFunc("network-service", plugincli.CompleteNetworkServiceNames)
	_ = cmd.RegisterFlagCompletionFunc("health-protocol", plugincli.CompleteEnum("tcp", "http", "https"))
	_ = cmd.RegisterFlagCompletionFunc("backend-port", completeServicePorts)
	_ = cmd.RegisterFlagCompletionFunc("health-port", completeServicePorts)
}
func listenerFlags(cmd *cobra.Command) {
	cmd.Flags().String("protocol", "tcp", "Public transport: tcp or udp")
	cmd.Flags().Int32("port", 0, "Public port")
	cmd.Flags().String("port-range", "", "Inclusive public UDP range, e.g. 10000-10127")
	cmd.Flags().StringSlice("address", []string{spec.DefaultAddress}, "Address requests used by the listener")
	cmd.MarkFlagsMutuallyExclusive("port", "port-range")
	_ = cmd.RegisterFlagCompletionFunc("protocol", plugincli.CompleteEnum("tcp", "udp"))
}
func addressFlags(cmd *cobra.Command) {
	cmd.Flags().String("families", spec.DefaultFamilies, "IP versions: ipv6, ipv4, or ipv4,ipv6")
	cmd.Flags().String("location-source", "", "Follow backends (default) or all locations")
	cmd.Flags().StringSlice("location", nil, "Choose locations by name")
	cmd.MarkFlagsMutuallyExclusive("location-source", "location")
}
func completeNLB(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), plugincli.CompletionTimeout)
	defer cancel()
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var list api.NetworkLoadBalancerList
	if c.List(ctx, &list, client.InNamespace(spec.Namespace)) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, n := range list.Items {
		if strings.HasPrefix(n.Name, prefix) {
			names = append(names, n.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
func completeServicePorts(cmd *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), plugincli.CompletionTimeout)
	defer cancel()
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var s api.NetworkService
	if c.Get(ctx, key(str(cmd, "network-service")), &s) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, p := range s.Spec.Ports {
		if strings.HasPrefix(p.Name, prefix) {
			names = append(names, p.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeManaged(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 && (cmd.Parent().Name() != "route" || cmd.Name() == "list") {
		return completeNLB(cmd, args, prefix)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), plugincli.CompletionTimeout)
	defer cancel()
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := []string{}
	if cmd.Parent().Name() == "route" && len(args) == 0 {
		var routes api.NetworkLoadBalancerRouteList
		if c.List(ctx, &routes, client.InNamespace(spec.Namespace)) == nil {
			for _, r := range routes.Items {
				names = append(names, r.Name)
			}
		}
	} else if len(args) == 1 && cmd.Name() != "add" {
		var n api.NetworkLoadBalancer
		if c.Get(ctx, key(args[0]), &n) == nil {
			if cmd.Parent().Name() == "listener" {
				for _, l := range n.Spec.Listeners {
					names = append(names, l.Name)
				}
			} else {
				for _, a := range n.Spec.Exposure.Addresses {
					names = append(names, a.Name)
				}
			}
		}
	}
	filtered := names[:0]
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			filtered = append(filtered, name)
		}
	}
	return filtered, cobra.ShellCompDirectiveNoFileComp
}
