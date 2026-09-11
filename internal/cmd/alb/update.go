// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func updateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update load balancer-wide settings",
		Long: `Update settings that apply to the whole Application Load Balancer.

Origins belong to routes. Change them with "datumctl alb route update" or
"datumctl alb route backend add|remove".`,
		Example: `  datumctl alb update my-app --display-name "Production API"
  datumctl alb update my-app --no-force-https`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runUpdate,
	}

	cmd.Flags().String("display-name", "", "Human-friendly name shown in the cloud portal")
	cmd.Flags().Bool("force-https", false, "Redirect HTTP requests to HTTPS")
	cmd.Flags().Bool("no-force-https", false, "Disable the HTTP to HTTPS redirect")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")

	for _, retired := range []string{"endpoint", "network-service", "port", "tls-hostname"} {
		cmd.Flags().String(retired, "", "")
		_ = cmd.Flags().MarkHidden(retired)
	}
	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if backendFlagsChanged(cmd) {
		return util.UsageErrorf("origins are managed per route, not on the load balancer").
			WithFix(fmt.Sprintf("replace a route's origins with:\n       datumctl alb route update %s --path / --endpoint URL\n     or change one origin with:\n       datumctl alb route backend add|remove %s --path / ...", args[0], args[0]))
	}

	in := spec.UpdateInput{}
	changed := false

	if cmd.Flags().Changed("display-name") {
		v, _ := cmd.Flags().GetString("display-name")
		in.DisplayName = &v
		changed = true
	}

	forceHTTPS, _ := cmd.Flags().GetBool("force-https")
	noForceHTTPS, _ := cmd.Flags().GetBool("no-force-https")
	if forceHTTPS && noForceHTTPS {
		return util.UsageErrorf("cannot combine --force-https and --no-force-https")
	}
	if cmd.Flags().Changed("force-https") || cmd.Flags().Changed("no-force-https") {
		v := forceHTTPS && !noForceHTTPS
		in.ForceHTTPS = &v
		changed = true
	}

	if !changed {
		return util.UsageErrorf("nothing to update").
			WithFix("pass --display-name, or --force-https/--no-force-https")
	}

	return mutateProxy(cmd, args[0], func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.ApplyHTTPProxyUpdate(current, in)
	}, fmt.Sprintf("Application load balancer %q updated.\n", args[0]))
}
