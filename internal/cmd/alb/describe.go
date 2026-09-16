// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func describeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "describe <name>",
		Aliases: []string{"show", "get"},
		Short:   "Show details of an Application Load Balancer",
		Example: `  datumctl alb describe my-app
  datumctl alb describe my-app -o yaml`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runDescribe,
	}
	return cmd
}

func runDescribe(cmd *cobra.Command, args []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}

	proxy, err := util.GetHTTPProxy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd),
		util.OutputTable, util.OutputWide, util.OutputJSON, util.OutputYAML)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, proxy)
	case util.OutputYAML:
		return util.PrintYAML(out, proxy)
	}

	status, detail := util.ProxyStatus(proxy)
	_, _ = fmt.Fprintf(out, "Name:               %s\n", proxy.Name)
	_, _ = fmt.Fprintf(out, "Display name:       %s\n", util.OrDash(spec.DisplayName(proxy)))
	_, _ = fmt.Fprintf(out, "Status:             %s\n", status)
	if detail != "" && detail != strings.ToLower(status) {
		_, _ = fmt.Fprintf(out, "Status detail:      %s\n", detail)
	}
	_, _ = fmt.Fprintf(out, "Age:                %s\n", util.RelativeAgeVerbose(proxy.CreationTimestamp))
	_, _ = fmt.Fprintf(out, "Default hostname:   %s\n", util.OrDash(proxy.Status.CanonicalHostname))
	if connector := spec.ConnectorName(proxy); connector != "" {
		_, _ = fmt.Fprintf(out, "Connector:          %s\n", connector)
	}
	_, _ = fmt.Fprintf(out, "Force HTTPS:        %s\n", boolWord(spec.ForceHTTPS(proxy)))

	routes := spec.UserRoutes(proxy)
	if len(routes) == 0 {
		_, _ = fmt.Fprintln(out, "Routes:             none")
	} else {
		_, _ = fmt.Fprintln(out, "Routes:")
		for _, r := range routes {
			_, _ = fmt.Fprintf(out, "  %s\n", r.Path)
			for _, b := range r.Backends {
				line := fmt.Sprintf("    %s (%s)", spec.FormatBackend(b), spec.BackendKind(b))
				if b.TLS != nil && b.TLS.Hostname != nil && *b.TLS.Hostname != "" {
					line += "  tls=" + *b.TLS.Hostname
				}
				_, _ = fmt.Fprintln(out, line)
			}
		}
	}
	_, _ = fmt.Fprintf(out, "Host header:        %s\n", util.OrDash(spec.HostHeader(proxy)))

	set, add, remove := spec.ListRequestHeaders(proxy)
	if len(set)+len(add)+len(remove) > 0 {
		_, _ = fmt.Fprintln(out, "Request headers:")
		for _, h := range set {
			_, _ = fmt.Fprintf(out, "  set %s=%s\n", h.Name, h.Value)
		}
		for _, h := range add {
			_, _ = fmt.Fprintf(out, "  add %s=%s\n", h.Name, h.Value)
		}
		for _, h := range remove {
			_, _ = fmt.Fprintf(out, "  remove %s\n", h)
		}
	}

	hostnames := spec.Hostnames(proxy)
	_, _ = fmt.Fprintf(out, "Custom hostnames:   %s\n", util.OrDash(strings.Join(hostnames, ", ")))
	if len(proxy.Status.HostnameStatuses) > 0 {
		_, _ = fmt.Fprintln(out, "Hostname status:")
		for _, hs := range proxy.Status.HostnameStatuses {
			_, _ = fmt.Fprintf(out, "  %s  available=%s  dns=%s  cert=%s\n",
				hs.Hostname,
				util.ConditionStatus(hs.Conditions, networkingv1alpha.HostnameConditionAvailable),
				util.ConditionStatus(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed),
				util.ConditionStatus(hs.Conditions, networkingv1alpha.HostnameConditionCertificateReady),
			)
		}
	}

	if tpp, err := findTPPForProxy(cmd.Context(), c, proxy.Name); err != nil {
		_, _ = fmt.Fprintf(out, "Traffic protection: %s\n", err.Error())
	} else if tpp == nil {
		_, _ = fmt.Fprintln(out, "Traffic protection: off")
	} else {
		_, _ = fmt.Fprintf(out, "Traffic protection: %s (paranoia %d)\n", spec.TPPMode(tpp), spec.TPPParanoia(tpp))
	}

	if policy, err := getSecurityPolicy(cmd.Context(), c, proxy.Name); err != nil {
		_, _ = fmt.Fprintf(out, "Basic auth:         %s\n", err.Error())
	} else if policy == nil {
		_, _ = fmt.Fprintln(out, "Basic auth:         off")
	} else {
		secret, secretErr := getBasicAuthSecret(cmd.Context(), c, proxy.Name)
		usernames := spec.ParseHtpasswdUsernames(secret)
		if secretErr != nil {
			_, _ = fmt.Fprintf(out, "Basic auth:         enabled (%s)\n", secretErr.Error())
		} else {
			_, _ = fmt.Fprintf(out, "Basic auth:         enabled (%s)\n", util.OrDash(strings.Join(usernames, ", ")))
		}
	}

	if cond := apimeta.FindStatusCondition(proxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionCertificatesReady); cond != nil {
		_, _ = fmt.Fprintf(out, "Certificates:       %s (%s)\n", cond.Status, util.OrDash(cond.Reason))
	}

	if proxy.Status.CanonicalHostname != "" && !util.QuietFromCmd(cmd) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nTry it:\n  curl -I https://%s/\n", proxy.Status.CanonicalHostname)
	}

	return nil
}
