// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func hostnameCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "hostname",
		Aliases: []string{"hostnames"},
		Short:   "Manage custom hostnames on an Application Load Balancer",
	}
	cmd.AddCommand(hostnameAddCommand(), hostnameRemoveCommand(), hostnameListCommand())
	return cmd
}

func hostnameAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "add <name> <hostname>",
		Short:             "Attach a custom hostname",
		Example:           `  datumctl alb hostname add my-app app.example.com`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHostnameAdd,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func hostnameRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "remove <name> <hostname>",
		Aliases:           []string{"rm"},
		Short:             "Detach a custom hostname",
		Example:           `  datumctl alb hostname remove my-app app.example.com`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHostnameRemove,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func hostnameListCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "list <name>",
		Aliases:           []string{"ls"},
		Short:             "List hostnames on an Application Load Balancer",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHostnameList,
	}
}

func runHostnameAdd(cmd *cobra.Command, args []string) error {
	return mutateHostname(cmd, args[0], args[1], spec.AddHostname, "attached")
}

func runHostnameRemove(cmd *cobra.Command, args []string) error {
	return mutateHostname(cmd, args[0], args[1], spec.RemoveHostname, "removed")
}

func mutateHostname(
	cmd *cobra.Command,
	name, hostname string,
	mutate func(*networkingv1alpha.HTTPProxy, string) (*networkingv1alpha.HTTPProxy, error),
	verb string,
) error {
	return mutateProxy(cmd, name, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return mutate(current, hostname)
	}, fmt.Sprintf("Hostname %q %s on %q.\n", hostname, verb, name))
}

func runHostnameList(cmd *cobra.Command, args []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	proxy, err := util.GetHTTPProxy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd))
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, proxy.Spec.Hostnames)
	case util.OutputYAML:
		return util.PrintYAML(out, proxy.Spec.Hostnames)
	case util.OutputName:
		for _, h := range spec.Hostnames(proxy) {
			_, _ = fmt.Fprintln(out, h)
		}
		if proxy.Status.CanonicalHostname != "" {
			_, _ = fmt.Fprintln(out, proxy.Status.CanonicalHostname)
		}
		return nil
	}

	_, _ = fmt.Fprintf(out, "Default hostname: %s\n", util.OrDash(proxy.Status.CanonicalHostname))
	custom := spec.Hostnames(proxy)
	if len(custom) == 0 {
		_, _ = fmt.Fprintln(out, "Custom hostnames: none")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Custom hostnames: %s\n", strings.Join(custom, ", "))
	return nil
}
