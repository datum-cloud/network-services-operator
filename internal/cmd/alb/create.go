// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

func createCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an Application Load Balancer",
		Long: `Create an Application Load Balancer in front of an origin.

The command waits for Datum to assign a default hostname, because that is what
you CNAME custom domains at. Pass --no-wait to return immediately.`,
		Example: `  # Create a load balancer and print the generated hostname
  datumctl alb create my-app --endpoint https://origin.example.com

  # Attach a custom hostname at create time
  datumctl alb create my-app --endpoint https://origin.example.com --hostname app.example.com

  # Validate against the API server without creating anything
  datumctl alb create my-app --endpoint https://origin.example.com --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: runCreate,
	}

	cmd.Flags().String("endpoint", "", "Origin URL (http or https)")
	cmd.Flags().StringArray("hostname", nil, "Custom hostname to attach (repeatable)")
	cmd.Flags().String("host-header", "", "Host header to send to the origin")
	cmd.Flags().String("tls-hostname", "", "Hostname used to verify TLS when the origin is an IP")
	cmd.Flags().String("display-name", "", "Human-friendly name shown in the cloud portal")
	cmd.Flags().Bool("force-https", true, "Redirect HTTP requests to HTTPS")
	cmd.Flags().Bool("no-force-https", false, "Disable the HTTP to HTTPS redirect")
	cmd.Flags().String("waf-mode", "Enforce", "Traffic protection mode: Enforce, Observe, or Disabled")
	cmd.Flags().Int("paranoia", 1, "OWASP CRS paranoia level (1-4)")
	cmd.Flags().Bool("no-waf", false, "Do not attach a traffic protection policy")
	cmd.Flags().Bool("wait", true, "Wait for the generated hostname")
	cmd.Flags().Bool("no-wait", false, "Return as soon as the load balancer is created")
	cmd.Flags().Duration("timeout", defaultWaitTime, "How long to wait for the generated hostname")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without creating")

	_ = cmd.MarkFlagRequired("endpoint")
	_ = cmd.RegisterFlagCompletionFunc("waf-mode", util.CompleteEnum("Enforce", "Observe", "Disabled"))

	return cmd
}

func runCreate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	name := args[0]

	endpoint, _ := cmd.Flags().GetString("endpoint")
	hostnames, _ := cmd.Flags().GetStringArray("hostname")
	hostHeader, _ := cmd.Flags().GetString("host-header")
	tlsHostname, _ := cmd.Flags().GetString("tls-hostname")
	displayName, _ := cmd.Flags().GetString("display-name")
	forceHTTPS, _ := cmd.Flags().GetBool("force-https")
	noForceHTTPS, _ := cmd.Flags().GetBool("no-force-https")
	if noForceHTTPS {
		forceHTTPS = false
	}
	wafModeFlag, _ := cmd.Flags().GetString("waf-mode")
	paranoia, _ := cmd.Flags().GetInt("paranoia")
	noWAF, _ := cmd.Flags().GetBool("no-waf")
	waitFlag, _ := cmd.Flags().GetBool("wait")
	noWait, _ := cmd.Flags().GetBool("no-wait")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if noWait {
		waitFlag = false
	}

	proxy, err := spec.BuildHTTPProxy(spec.CreateInput{
		Name:        name,
		DisplayName: displayName,
		Endpoint:    endpoint,
		TLSHostname: tlsHostname,
		HostHeader:  hostHeader,
		Hostnames:   hostnames,
		ForceHTTPS:  forceHTTPS,
	})
	if err != nil {
		return err
	}

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}

	createOpts := []client.CreateOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		createOpts = append(createOpts, client.DryRunAll)
	}

	if err := c.Create(ctx, proxy, createOpts...); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return util.NewCLIError(util.ExitConflict, fmt.Sprintf("application load balancer %q already exists", name)).
				WithFix("choose a different name, or describe the existing one:\n       datumctl alb describe " + name).
				WithCause(err)
		}
		return util.ClassifyError(fmt.Errorf("creating application load balancer %q: %w", name, err))
	}

	if !noWAF {
		mode, err := spec.ParseWAFMode(wafModeFlag)
		if err != nil {
			return err
		}
		tpp, err := spec.BuildTPP(spec.WAFInput{
			ProxyName:   name,
			DisplayName: display.HTTPProxyDisplayName(proxy),
			Mode:        mode,
			Paranoia:    paranoia,
		})
		if err != nil {
			return err
		}
		if err := c.Create(ctx, tpp, createOpts...); err != nil && !apierrors.IsAlreadyExists(err) {
			return util.ClassifyError(fmt.Errorf("attaching traffic protection to %q: %w", name, err))
		}
	}

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "Application load balancer %q validated.\n", name)
		return nil
	}

	if !waitFlag {
		_, _ = fmt.Fprintf(out, "Application load balancer %q created.\n", name)
		if !util.QuietFromCmd(cmd) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nNext steps:\n  datumctl alb describe %s\n", name)
		}
		return nil
	}

	if timeout <= 0 {
		timeout = defaultWaitTime
	}
	hostname, err := waitForCanonicalHostname(ctx, c, name, timeout)
	if err != nil {
		_, _ = fmt.Fprintf(out, "Application load balancer %q created.\n", name)
		return err
	}

	_, _ = fmt.Fprintf(out, "Application load balancer %q created.\n", name)
	_, _ = fmt.Fprintf(out, "Hostname: %s\n", hostname)
	if !util.QuietFromCmd(cmd) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nNext steps:\n  datumctl alb hostname add %s <custom-hostname>\n  datumctl alb describe %s\n", name, name)
	}
	return nil
}
