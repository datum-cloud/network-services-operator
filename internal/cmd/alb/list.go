// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
)

func listCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Application Load Balancers",
		Example: `  datumctl alb list
  datumctl alb list --status error
  datumctl alb list -o json`,
		Args: cobra.NoArgs,
		RunE: runList,
	}
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	cmd.Flags().String("status", "", "Only show load balancers in this state: active, pending, or error")
	_ = cmd.RegisterFlagCompletionFunc("status", plugincli.CompleteEnum("active", "pending", "error"))
	return cmd
}

func runList(cmd *cobra.Command, _ []string) error {
	statusFlag, _ := cmd.Flags().GetString("status")
	if _, err := util.ParseStatusFilter(statusFlag); err != nil {
		return err
	}

	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}

	var list networkingv1alpha.HTTPProxyList
	if err := c.List(cmd.Context(), &list, client.InNamespace(util.ResourceNamespace)); err != nil {
		return util.ClassifyError(fmt.Errorf("listing application load balancers: %w", err))
	}

	statusFilter, _ := util.ParseStatusFilter(statusFlag)
	if statusFilter != "" {
		kept := list.Items[:0]
		for i := range list.Items {
			status, _ := util.ProxyStatus(&list.Items[i])
			if util.StatusMatchesFilter(status, statusFilter) {
				kept = append(kept, list.Items[i])
			}
		}
		list.Items = kept
	}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})

	format, err := util.ParseOutputFormat(plugincli.OutputFromCmd(cmd))
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
		if !plugincli.QuietFromCmd(cmd) {
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
			_, _ = fmt.Fprintln(tw, "NAME\tDISPLAY NAME\tHOSTNAME\tCUSTOM\tORIGIN\tROUTES\tWAF\tSTATUS\tAGE")
		} else {
			_, _ = fmt.Fprintln(tw, "NAME\tDISPLAY NAME\tHOSTNAME\tCUSTOM\tORIGIN\tWAF\tSTATUS\tAGE")
		}
	}

	nameWidth, cellWidth := 28, 28
	if wide {
		nameWidth, cellWidth = 24, 24
	}

	for i := range items {
		p := &items[i]
		status, _ := util.ProxyStatus(p)
		name, _ := util.TruncateCell(p.Name, nameWidth)
		display, _ := util.TruncateCell(spec.DisplayName(p), cellWidth)
		hostname, _ := util.TruncateCell(p.Status.CanonicalHostname, cellWidth)
		custom, _ := util.TruncateCell(spec.HostnamesSummary(p), cellWidth)
		origin, _ := util.TruncateCell(spec.OriginSummary(p), cellWidth)
		if wide {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
				name,
				util.OrDash(display),
				util.OrDash(hostname),
				util.OrDash(custom),
				util.OrDash(origin),
				len(spec.UserRoutes(p)),
				util.OrDash(wafModes[p.Name]),
				status,
				util.RelativeAge(p.CreationTimestamp),
			)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			name,
			util.OrDash(display),
			util.OrDash(hostname),
			util.OrDash(custom),
			util.OrDash(origin),
			util.OrDash(wafModes[p.Name]),
			status,
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
