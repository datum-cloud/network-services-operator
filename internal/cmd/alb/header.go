// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func headerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "header",
		Aliases: []string{"headers"},
		Short:   "Manage request headers sent to the origin",
	}
	cmd.AddCommand(headerSetCommand(), headerUnsetCommand(), headerListCommand())
	return cmd
}

func headerSetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name> <Name=value>",
		Short: "Set a request header on the origin",
		Example: `  datumctl alb header set my-app Host=origin.example.com
  datumctl alb header set my-app X-Debug=1`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHeaderSet,
	}
	cmd.Flags().Bool("add", false, "Add the header instead of replacing an existing value")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func headerUnsetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "unset <name> <Name>",
		Aliases:           []string{"rm", "remove"},
		Short:             "Remove a request header override",
		Example:           `  datumctl alb header unset my-app Host`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHeaderUnset,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func headerListCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "list <name>",
		Aliases:           []string{"ls"},
		Short:             "List request header overrides",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runHeaderList,
	}
}

func runHeaderSet(cmd *cobra.Command, args []string) error {
	name, value, err := spec.ParseHeaderArg(args[1])
	if err != nil {
		return err
	}
	add, _ := cmd.Flags().GetBool("add")
	mutate := spec.SetRequestHeader
	if add {
		mutate = spec.AddRequestHeader
	}
	return mutateProxy(cmd, args[0], func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return mutate(current, name, value)
	}, fmt.Sprintf("Header %s=%s set on %q.\n", name, value, args[0]))
}

func runHeaderUnset(cmd *cobra.Command, args []string) error {
	return mutateProxy(cmd, args[0], func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.UnsetRequestHeader(current, args[1])
	}, fmt.Sprintf("Header %q removed from %q.\n", args[1], args[0]))
}

func runHeaderList(cmd *cobra.Command, args []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	proxy, err := util.GetHTTPProxy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}
	set, add, remove := spec.ListRequestHeaders(proxy)
	if len(set)+len(add)+len(remove) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No request header overrides.")
		return nil
	}
	for _, h := range set {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "set\t%s=%s\n", h.Name, h.Value)
	}
	for _, h := range add {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "add\t%s=%s\n", h.Name, h.Value)
	}
	for _, h := range remove {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remove\t%s\n", h)
	}
	return nil
}
