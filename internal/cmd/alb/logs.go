// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
)

func logsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show access logs for an Application Load Balancer",
		Long: `Query the same access logs the cloud portal shows for an Application Load
Balancer. Rows come from the project logs API, pinned to this load balancer.`,
		Example: `  datumctl alb logs my-app
  datumctl alb logs my-app --since 1h --limit 50
  datumctl alb logs my-app --method GET --code 500 --code 502
  datumctl alb logs my-app --follow`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: plugincli.CompleteALBNames,
		RunE:              runLogs,
	}

	cmd.Flags().Duration("since", 30*time.Minute, "How far back to look")
	cmd.Flags().Int("limit", util.DefaultLogsLimit, "Maximum number of log lines to return")
	cmd.Flags().StringArray("method", nil, "Only show these HTTP methods (repeatable)")
	cmd.Flags().StringArray("code", nil, "Only show these response status codes (repeatable)")
	cmd.Flags().StringArray("host", nil, "Only show these request hosts (repeatable, filtered after fetch)")
	cmd.Flags().BoolP("follow", "f", false, "Keep querying for new lines")
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	name := args[0]
	since, _ := cmd.Flags().GetDuration("since")
	limit, _ := cmd.Flags().GetInt("limit")
	methods, _ := cmd.Flags().GetStringArray("method")
	codes, _ := cmd.Flags().GetStringArray("code")
	hosts, _ := cmd.Flags().GetStringArray("host")
	follow, _ := cmd.Flags().GetBool("follow")
	noHeaders, _ := cmd.Flags().GetBool("no-headers")

	if since <= 0 {
		return util.UsageErrorf("--since must be greater than zero")
	}
	if limit <= 0 {
		return util.UsageErrorf("--limit must be greater than zero")
	}
	if limit > util.MaxLogsLimit {
		return util.UsageErrorf("--limit must be %d or fewer", util.MaxLogsLimit)
	}

	project := plugincli.ProjectFromCmd(cmd)
	c, err := newClient(project)
	if err != nil {
		return err
	}
	if _, err := util.GetHTTPProxy(cmd.Context(), c, name); err != nil {
		return err
	}

	cfg, err := plugincli.RestConfig(project)
	if err != nil {
		return err
	}

	format, err := util.ParseOutputFormat(plugincli.OutputFromCmd(cmd),
		util.OutputTable, util.OutputWide, util.OutputJSON, util.OutputYAML)
	if err != nil {
		return err
	}

	query := spec.BuildAlbLogQL(name, methods, codes)
	out := cmd.OutOrStdout()

	if !follow {
		entries, err := queryLogs(cmd.Context(), cfg, query, since, limit)
		if err != nil {
			return err
		}
		return printLogs(out, filterLogsByHost(entries, hosts), format, noHeaders)
	}

	if format != util.OutputTable && format != util.OutputWide {
		return util.UsageErrorf("--follow only supports table output").
			WithFix("omit -o, or use -o table")
	}

	var last time.Time
	headerPrinted := noHeaders
	ticker := time.NewTicker(util.LogsFollowInterval)
	defer ticker.Stop()

	printBatch := func() error {
		entries, err := queryLogs(cmd.Context(), cfg, query, since, limit)
		if err != nil {
			return err
		}
		entries = filterLogsByHost(entries, hosts)
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Time.Before(entries[j].Time)
		})
		var fresh []util.LogEntry
		for _, e := range entries {
			if !last.IsZero() && !e.Time.After(last) {
				continue
			}
			fresh = append(fresh, e)
		}
		if len(fresh) == 0 {
			return nil
		}
		if err := printLogs(out, fresh, format, headerPrinted); err != nil {
			return err
		}
		headerPrinted = true
		last = fresh[len(fresh)-1].Time
		return nil
	}

	if err := printBatch(); err != nil {
		return err
	}
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
			if err := printBatch(); err != nil {
				return err
			}
		}
	}
}

func queryLogs(ctx context.Context, cfg *rest.Config, query string, since time.Duration, limit int) ([]util.LogEntry, error) {
	end := time.Now().UTC()
	return util.QueryLogs(ctx, cfg, util.LogsQuery{
		Query: query,
		Start: end.Add(-since),
		End:   end,
		Limit: limit,
	})
}

func filterLogsByHost(entries []util.LogEntry, hosts []string) []util.LogEntry {
	wanted := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(strings.ToLower(h))
		if h != "" {
			wanted[h] = true
		}
	}
	if len(wanted) == 0 {
		return entries
	}
	kept := make([]util.LogEntry, 0, len(entries))
	for _, e := range entries {
		host := strings.ToLower(strings.TrimSpace(e.Labels["host"]))
		if wanted[host] {
			kept = append(kept, e)
		}
	}
	return kept
}

func printLogs(w io.Writer, entries []util.LogEntry, format util.OutputFormat, noHeaders bool) error {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Time.After(entries[j].Time)
	})

	switch format {
	case util.OutputJSON:
		return util.PrintJSON(w, entries)
	case util.OutputYAML:
		return util.PrintYAML(w, entries)
	}

	if len(entries) == 0 {
		_, _ = fmt.Fprintln(w, "No access logs found.")
		return nil
	}

	tw := util.NewTabWriter(w)
	if !noHeaders {
		if format == util.OutputWide {
			_, _ = fmt.Fprintln(tw, "TIME\tMETHOD\tSTATUS\tPATH\tHOST\tDURATION\tREQUEST ID\tUPSTREAM")
		} else {
			_, _ = fmt.Fprintln(tw, "TIME\tMETHOD\tSTATUS\tPATH\tHOST\tDURATION")
		}
	}
	for _, e := range entries {
		path, _ := util.TruncateCell(e.Labels["path"], 48)
		host, _ := util.TruncateCell(e.Labels["host"], 32)
		if format == util.OutputWide {
			upstream, _ := util.TruncateCell(e.Labels["upstream_host"], 32)
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				e.Time.Local().Format("15:04:05"),
				util.OrDash(e.Labels["method"]),
				util.OrDash(e.Labels["response_code"]),
				util.OrDash(path),
				util.OrDash(host),
				util.OrDash(e.Labels["duration"]),
				util.OrDash(e.Labels["request_id"]),
				util.OrDash(upstream),
			)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Time.Local().Format("15:04:05"),
			util.OrDash(e.Labels["method"]),
			util.OrDash(e.Labels["response_code"]),
			util.OrDash(path),
			util.OrDash(host),
			util.OrDash(e.Labels["duration"]),
		)
	}
	return tw.Flush()
}
