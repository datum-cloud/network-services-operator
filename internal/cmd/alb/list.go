// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func listCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Application Load Balancers",
		Example: `  datumctl alb list
  datumctl alb list -o json`,
		Args: cobra.NoArgs,
		RunE: runList,
	}
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func runList(cmd *cobra.Command, _ []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}

	var list networkingv1alpha.HTTPProxyList
	if err := c.List(cmd.Context(), &list, client.InNamespace(util.ResourceNamespace)); err != nil {
		return util.ClassifyError(fmt.Errorf("listing application load balancers: %w", err))
	}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd))
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, &list)
	case util.OutputYAML:
		return util.PrintYAML(out, &list)
	case util.OutputName:
		for i := range list.Items {
			_, _ = fmt.Fprintln(out, list.Items[i].Name)
		}
		return nil
	}

	if len(list.Items) == 0 {
		_, _ = fmt.Fprintln(out, "No application load balancers found.")
		if !util.QuietFromCmd(cmd) {
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), "\nNext steps:\n  datumctl alb create <name> --endpoint https://origin.example.com\n")
		}
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	wafModes := tppModeByProxy(cmd.Context(), c)
	return printProxyTable(out, list.Items, wafModes, format == util.OutputWide, noHeaders)
}

func printProxyTable(w io.Writer, items []networkingv1alpha.HTTPProxy, wafModes map[string]string, wide, noHeaders bool) error {
	tw := util.NewTabWriter(w)

	if !noHeaders {
		if wide {
			_, _ = fmt.Fprintln(tw, "NAME\tDISPLAY NAME\tENDPOINT\tHOSTNAME\tHOSTNAMES\tHOST HEADER\tFORCE HTTPS\tWAF\tSTATUS\tAGE")
		} else {
			_, _ = fmt.Fprintln(tw, "NAME\tENDPOINT\tHOSTNAME\tSTATUS\tWAF\tAGE")
		}
	}

	for i := range items {
		p := &items[i]
		status, _ := util.ProxyStatus(p)
		hostname, _ := util.TruncateCell(p.Status.CanonicalHostname, 40)
		endpoint, _ := util.TruncateCell(spec.Endpoint(p), 40)
		if wide {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				p.Name,
				util.OrDash(spec.DisplayName(p)),
				util.OrDash(endpoint),
				util.OrDash(hostname),
				util.OrDash(strings.Join(spec.Hostnames(p), ",")),
				util.OrDash(spec.HostHeader(p)),
				boolWord(spec.ForceHTTPS(p)),
				util.OrDash(wafModes[p.Name]),
				status,
				util.RelativeAge(p.CreationTimestamp),
			)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Name,
			util.OrDash(endpoint),
			util.OrDash(hostname),
			status,
			util.OrDash(wafModes[p.Name]),
			util.RelativeAge(p.CreationTimestamp),
		)
	}
	return tw.Flush()
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
