// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
)

func updateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update load balancer-wide settings",
		Long: `Update settings that apply to the whole Application Load Balancer.

The load balancing algorithm and passive health checks apply to every
route's origins. Passive health checks stop sending requests to an endpoint
after a run of 5xx responses, then let it back in after the ejection time.
Any tuning flag turns them on, keeping values already set; the rest take the
API defaults (5 consecutive 5xx, 30s, at most 50% of a backend ejected).

Origins belong to routes. Change them with "datumctl alb route update" or
"datumctl alb route backend add|update|remove".`,
		Example: `  datumctl alb update my-app --display-name "Production API"
  datumctl alb update my-app --no-force-https
  datumctl alb update my-app --algorithm round-robin
  datumctl alb update my-app --hash-header X-User-ID
  datumctl alb update my-app --algorithm default
  datumctl alb update my-app --health-checks
  datumctl alb update my-app --consecutive-5xx 3 --base-ejection-time 1m
  datumctl alb update my-app --no-health-checks`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: plugincli.CompleteALBNames,
		RunE:              runUpdate,
	}

	cmd.Flags().String("display-name", "", "Human-friendly name shown in the cloud portal")
	cmd.Flags().Bool("force-https", false, "Redirect HTTP requests to HTTPS")
	cmd.Flags().Bool("no-force-https", false, "Disable the HTTP to HTTPS redirect")
	cmd.Flags().String("algorithm", "", "Load balancing algorithm: round-robin, random, least-request, consistent-hash, or default to unset (least request)")
	cmd.Flags().String("hash-header", "", "Request header to hash on; implies --algorithm consistent-hash (source IP is hashed otherwise)")
	cmd.Flags().Bool("health-checks", false, "Turn on passive health checks")
	cmd.Flags().Bool("no-health-checks", false, "Turn off passive health checks")
	cmd.Flags().Int32("consecutive-5xx", 0, "Consecutive 5xx responses that eject an endpoint (default 5)")
	cmd.Flags().String("base-ejection-time", "", "How long an endpoint stays ejected the first time, for example 30s or 2m (default 30s)")
	cmd.Flags().Int32("max-ejection-percent", 0, "Most of a backend's endpoints that may be ejected at once, 1-100 (default 50)")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")

	_ = cmd.RegisterFlagCompletionFunc("algorithm", cobra.FixedCompletions(
		[]string{"round-robin", "random", "least-request", "consistent-hash", spec.AlgorithmDefault}, cobra.ShellCompDirectiveNoFileComp))

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

	in.LoadBalancer.Algorithm, _ = cmd.Flags().GetString("algorithm")
	in.LoadBalancer.HashHeader, _ = cmd.Flags().GetString("hash-header")
	if cmd.Flags().Changed("algorithm") && strings.TrimSpace(in.LoadBalancer.Algorithm) == "" {
		return util.UsageErrorf("--algorithm must not be empty").
			WithFix("use round-robin, random, least-request, consistent-hash, or default")
	}
	if cmd.Flags().Changed("hash-header") && strings.TrimSpace(in.LoadBalancer.HashHeader) == "" {
		return util.UsageErrorf("--hash-header must not be empty")
	}
	changed = changed || in.LoadBalancer.IsSet()

	healthChecks, _ := cmd.Flags().GetBool("health-checks")
	noHealthChecks, _ := cmd.Flags().GetBool("no-health-checks")
	if healthChecks && noHealthChecks {
		return util.UsageErrorf("cannot combine --health-checks and --no-health-checks")
	}
	if cmd.Flags().Changed("health-checks") || cmd.Flags().Changed("no-health-checks") {
		v := healthChecks && !noHealthChecks
		in.HealthCheck.Enabled = &v
	}
	if cmd.Flags().Changed("consecutive-5xx") {
		v, _ := cmd.Flags().GetInt32("consecutive-5xx")
		in.HealthCheck.Consecutive5xxErrors = &v
	}
	if cmd.Flags().Changed("base-ejection-time") {
		v, _ := cmd.Flags().GetString("base-ejection-time")
		in.HealthCheck.BaseEjectionTime = &v
	}
	if cmd.Flags().Changed("max-ejection-percent") {
		v, _ := cmd.Flags().GetInt32("max-ejection-percent")
		in.HealthCheck.MaxEjectionPercent = &v
	}
	changed = changed || in.HealthCheck.IsSet()

	if !changed {
		return util.UsageErrorf("nothing to update").
			WithFix("pass --display-name, --force-https/--no-force-https, --algorithm, or --health-checks/--no-health-checks")
	}

	return mutateProxy(cmd, args[0], func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
		return spec.ApplyHTTPProxyUpdate(current, in)
	}, fmt.Sprintf("Application load balancer %q updated.\n", args[0]))
}
