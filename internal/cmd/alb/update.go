// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func updateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an Application Load Balancer",
		Example: `  datumctl alb update my-app --endpoint https://new-origin.example.com
  datumctl alb update my-app --no-force-https
  datumctl alb update my-app --display-name "Production API"`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runUpdate,
	}

	cmd.Flags().String("endpoint", "", "Origin URL (http or https)")
	cmd.Flags().String("tls-hostname", "", "Hostname used to verify TLS when the origin is an IP")
	cmd.Flags().String("display-name", "", "Human-friendly name shown in the cloud portal")
	cmd.Flags().Bool("force-https", false, "Redirect HTTP requests to HTTPS")
	cmd.Flags().Bool("no-force-https", false, "Disable the HTTP to HTTPS redirect")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	in := spec.UpdateInput{}
	changed := false

	if cmd.Flags().Changed("endpoint") {
		v, _ := cmd.Flags().GetString("endpoint")
		in.Endpoint = &v
		changed = true
	}
	if cmd.Flags().Changed("tls-hostname") {
		v, _ := cmd.Flags().GetString("tls-hostname")
		in.TLSHostname = &v
		changed = true
	}
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
		if noForceHTTPS {
			v = false
		}
		in.ForceHTTPS = &v
		changed = true
	}

	if !changed {
		return util.UsageErrorf("nothing to update").
			WithFix("pass --endpoint, --tls-hostname, --display-name, or --force-https/--no-force-https")
	}

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}

	current, err := util.GetHTTPProxy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}

	updated, err := spec.ApplyHTTPProxyUpdate(current, in)
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if err := patchProxy(cmd.Context(), c, current, updated, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("updating application load balancer %q: %w", args[0], err))
	}

	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q validated.\n", args[0])
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q updated.\n", args[0])
	return nil
}
