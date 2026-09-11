// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func routeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "route",
		Aliases: []string{"routes"},
		Short:   "Manage routes and their origins on an Application Load Balancer",
		Long: `A route is a path prefix plus the pool of origins that serve it. Every load
balancer has a default "/" route; add more to send other paths elsewhere.`,
	}
	cmd.AddCommand(
		routeAddCommand(),
		routeRemoveCommand(),
		routeListCommand(),
		routeUpdateCommand(),
		routeBackendCommand(),
	)
	return cmd
}

func routeAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a route",
		Example: `  datumctl alb route add my-app --path /api --endpoint https://api.example.com
  datumctl alb route add my-app --path /checkout \
    --endpoint https://a.example.com --endpoint https://b.example.com
  datumctl alb route add my-app --path / --network-service storefront --port http`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteAdd,
	}
	addBackendFlags(cmd)
	cmd.Flags().String("path", "", "Path prefix the route matches (defaults to / when the load balancer has no default route)")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func routeRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "remove <name> --path PREFIX",
		Aliases:           []string{"rm"},
		Short:             "Remove a route",
		Example:           `  datumctl alb route remove my-app --path /api`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteRemove,
	}
	cmd.Flags().String("path", "", "Path prefix of the route to remove")
	cmd.Flags().Bool("force", false, "Remove the default route even when other routes remain")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func routeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "list <name>",
		Aliases:           []string{"ls"},
		Short:             "List routes and their origins",
		Example:           `  datumctl alb route list my-app`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteList,
	}
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func routeUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name> --path PREFIX",
		Short: "Replace the origins on a route",
		Long: `Replace every origin on one route with the origins given. Other routes are
left alone. To change a single origin, use "route backend add" or "remove".`,
		Example: `  datumctl alb route update my-app --path / --network-service storefront --port http
  datumctl alb route update my-app --path /api --endpoint https://api-new.example.com`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteUpdate,
	}
	addBackendFlags(cmd)
	cmd.Flags().String("path", "", "Path prefix of the route to update")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func routeBackendCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backend",
		Aliases: []string{"backends", "origin", "origins"},
		Short:   "Manage the origins on one route",
	}
	cmd.AddCommand(routeBackendAddCommand(), routeBackendRemoveCommand(), routeBackendListCommand())
	return cmd
}

func routeBackendAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> --path PREFIX",
		Short: "Add one origin to a route",
		Example: `  datumctl alb route backend add my-app --path /api --endpoint https://api-2.example.com
  datumctl alb route backend add my-app --path / --network-service storefront --port http`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteBackendAdd,
	}
	addBackendFlags(cmd)
	cmd.Flags().String("path", "", "Path prefix of the route")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func routeBackendRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "remove <name> --path PREFIX",
		Aliases:           []string{"rm"},
		Short:             "Remove one origin from a route",
		Example:           `  datumctl alb route backend remove my-app --path /api --endpoint https://api.example.com`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteBackendRemove,
	}
	addBackendFlags(cmd)
	cmd.Flags().String("path", "", "Path prefix of the route")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func routeBackendListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <name> [--path PREFIX]",
		Short: "List the origins on a route, or on every route",
		Example: `  datumctl alb route backend list my-app --path /api
  datumctl alb route backend list my-app`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runRouteBackendList,
	}
	cmd.Flags().String("path", "", "Only show origins on this route")
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func runRouteAdd(cmd *cobra.Command, args []string) error {
	backends, err := backendsFromFlags(cmd)
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("path")
	return mutateRoute(cmd, args[0], backends, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.AddRoute(current, path, backends)
	}, fmt.Sprintf("Route %s added to %q.\n", displayPath(path), args[0]))
}

func runRouteRemove(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("path")
	force, _ := cmd.Flags().GetBool("force")

	if !util.AssumeYes(cmd) {
		ok, err := util.ConfirmYesNo(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Remove route %s from %q?", path, args[0]), false)
		if err != nil {
			return err
		}
		if !ok {
			return util.NewCLIError(util.ExitAborted, "aborted")
		}
	}

	return mutateRoute(cmd, args[0], nil, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.RemoveRoute(current, path, force)
	}, fmt.Sprintf("Route %s removed from %q.\n", path, args[0]))
}

func runRouteUpdate(cmd *cobra.Command, args []string) error {
	backends, err := backendsFromFlags(cmd)
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("path")
	return mutateRoute(cmd, args[0], backends, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.ReplaceRouteBackends(current, path, backends)
	}, fmt.Sprintf("Route %s on %q updated.\n", path, args[0]))
}

func runRouteBackendAdd(cmd *cobra.Command, args []string) error {
	backend, err := singleBackendFromFlags(cmd)
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("path")
	return mutateRoute(cmd, args[0], []spec.BackendInput{backend}, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.AddRouteBackend(current, path, backend)
	}, fmt.Sprintf("Origin added to route %s on %q.\n", path, args[0]))
}

func runRouteBackendRemove(cmd *cobra.Command, args []string) error {
	backend, err := singleBackendFromFlags(cmd)
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("path")

	if !util.AssumeYes(cmd) {
		ok, err := util.ConfirmYesNo(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Remove origin from route %s on %q?", path, args[0]), false)
		if err != nil {
			return err
		}
		if !ok {
			return util.NewCLIError(util.ExitAborted, "aborted")
		}
	}

	return mutateRoute(cmd, args[0], nil, func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.RemoveRouteBackend(current, path, backend)
	}, fmt.Sprintf("Origin removed from route %s on %q.\n", path, args[0]))
}

func mutateRoute(
	cmd *cobra.Command,
	name string,
	referenced []spec.BackendInput,
	mutate func(*networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error),
	success string,
) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	current, err := util.GetHTTPProxy(cmd.Context(), c, name)
	if err != nil {
		return err
	}
	updated, err := mutate(current)
	if err != nil {
		return err
	}
	if err := ensureNetworkServices(cmd.Context(), c, referenced); err != nil {
		return err
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if err := patchProxy(cmd.Context(), c, current, updated, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("updating routes on %q: %w", name, err))
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), success)
	return nil
}

func runRouteList(cmd *cobra.Command, args []string) error {
	proxy, err := loadProxy(cmd, args[0])
	if err != nil {
		return err
	}

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd))
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	routes := spec.Routes(proxy)
	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, proxy.Spec.Rules)
	case util.OutputYAML:
		return util.PrintYAML(out, proxy.Spec.Rules)
	case util.OutputName:
		for _, r := range routes {
			if !r.ForceHTTPS {
				_, _ = fmt.Fprintln(out, r.Path)
			}
		}
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	tw := util.NewTabWriter(out)
	if !noHeaders {
		_, _ = fmt.Fprintln(tw, "PATH\tORIGINS\tKIND")
	}
	for _, r := range routes {
		if r.ForceHTTPS {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Path, "redirect http → https", "system")
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Path, formatBackends(r.Backends), backendKinds(r.Backends))
	}
	return tw.Flush()
}

func runRouteBackendList(cmd *cobra.Command, args []string) error {
	proxy, err := loadProxy(cmd, args[0])
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("path")

	routes := spec.UserRoutes(proxy)
	if path != "" {
		path, err = spec.NormalizePath(path)
		if err != nil {
			return err
		}
		var match []spec.Route
		for _, r := range routes {
			if r.Path == path {
				match = append(match, r)
			}
		}
		if len(match) == 0 {
			return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("route %q not found", path)).
				WithFix(fmt.Sprintf("list routes with:\n       datumctl alb route list %s", args[0]))
		}
		routes = match
	}

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd))
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch format {
	case util.OutputJSON, util.OutputYAML:
		payload := make([]map[string]any, 0, len(routes))
		for _, r := range routes {
			payload = append(payload, map[string]any{"path": r.Path, "backends": r.Backends})
		}
		if format == util.OutputJSON {
			return util.PrintJSON(out, payload)
		}
		return util.PrintYAML(out, payload)
	case util.OutputName:
		for _, r := range routes {
			for _, b := range r.Backends {
				_, _ = fmt.Fprintln(out, spec.FormatBackend(b))
			}
		}
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	return printBackendTable(out, routes, noHeaders)
}

func printBackendTable(w io.Writer, routes []spec.Route, noHeaders bool) error {
	tw := util.NewTabWriter(w)
	if !noHeaders {
		_, _ = fmt.Fprintln(tw, "PATH\tORIGIN\tKIND\tTLS HOSTNAME")
	}
	for _, r := range routes {
		for _, b := range r.Backends {
			tls := ""
			if b.TLS != nil && b.TLS.Hostname != nil {
				tls = *b.TLS.Hostname
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Path, spec.FormatBackend(b), spec.BackendKind(b), util.OrDash(tls))
		}
	}
	return tw.Flush()
}

func loadProxy(cmd *cobra.Command, name string) (*networkingv1alpha.HTTPProxy, error) {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return nil, err
	}
	return util.GetHTTPProxy(cmd.Context(), c, name)
}

func formatBackends(backends []networkingv1alpha.HTTPProxyRuleBackend) string {
	parts := make([]string, 0, len(backends))
	for _, b := range backends {
		parts = append(parts, spec.FormatBackend(b))
	}
	return util.OrDash(strings.Join(parts, ", "))
}

func backendKinds(backends []networkingv1alpha.HTTPProxyRuleBackend) string {
	seen := map[string]bool{}
	kinds := make([]string, 0, len(backends))
	for _, b := range backends {
		kind := spec.BackendKind(b)
		if seen[kind] {
			continue
		}
		seen[kind] = true
		kinds = append(kinds, kind)
	}
	return util.OrDash(strings.Join(kinds, ", "))
}

func displayPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return spec.DefaultRoutePath
	}
	return path
}
