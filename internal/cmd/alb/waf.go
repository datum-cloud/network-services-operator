// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func wafCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "waf",
		Aliases: []string{"tpp"},
		Short:   "Manage traffic protection on an Application Load Balancer",
	}
	cmd.AddCommand(wafSetCommand(), wafDisableCommand(), wafDescribeCommand())
	return cmd
}

func wafSetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Enable or update traffic protection",
		Example: `  datumctl alb waf set my-app --mode Enforce --paranoia 1
  datumctl alb waf set my-app --mode Observe --paranoia 2`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runWAFSet,
	}
	cmd.Flags().String("mode", "Enforce", "Traffic protection mode: Enforce, Observe, or Disabled")
	cmd.Flags().Int("paranoia", 1, "OWASP CRS paranoia level (1-4)")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.RegisterFlagCompletionFunc("mode", util.CompleteEnum("Enforce", "Observe", "Disabled"))
	return cmd
}

func wafDisableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "disable <name>",
		Short:             "Remove traffic protection",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runWAFDisable,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without deleting")
	return cmd
}

func wafDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "describe <name>",
		Aliases:           []string{"show", "get"},
		Short:             "Show traffic protection settings",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runWAFDescribe,
	}
}

func runWAFSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	modeFlag, _ := cmd.Flags().GetString("mode")
	paranoia, _ := cmd.Flags().GetInt("paranoia")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	mode, err := spec.ParseWAFMode(modeFlag)
	if err != nil {
		return err
	}

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	proxy, err := util.GetHTTPProxy(ctx, c, name)
	if err != nil {
		return err
	}

	existing, err := findTPPForProxy(ctx, c, name)
	if err != nil {
		return err
	}

	opts := []client.CreateOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}

	if existing == nil {
		tpp, err := spec.BuildTPP(spec.WAFInput{
			ProxyName:   name,
			DisplayName: spec.DisplayName(proxy),
			Mode:        mode,
			Paranoia:    paranoia,
		})
		if err != nil {
			return err
		}
		if err := c.Create(ctx, tpp, opts...); err != nil && !apierrors.IsAlreadyExists(err) {
			return util.ClassifyError(fmt.Errorf("attaching traffic protection to %q: %w", name, err))
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Traffic protection on %q set to %s (paranoia %d).\n", name, mode, paranoia)
		return nil
	}

	updated, err := spec.ApplyTPPUpdate(existing, mode, paranoia)
	if err != nil {
		return err
	}
	if err := patchTPP(ctx, c, existing, updated, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("updating traffic protection on %q: %w", name, err))
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Traffic protection on %q set to %s (paranoia %d).\n", name, spec.TPPMode(updated), spec.TPPParanoia(updated))
	return nil
}

func runWAFDisable(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	tpp, err := findTPPForProxy(cmd.Context(), c, name)
	if err != nil {
		return err
	}
	if tpp == nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Traffic protection is already off on %q.\n", name)
		return nil
	}
	if err := deleteIfExists(cmd.Context(), c, tpp, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("removing traffic protection from %q: %w", name, err))
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Traffic protection removed from %q.\n", name)
	return nil
}

func runWAFDescribe(cmd *cobra.Command, args []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	if _, err := util.GetHTTPProxy(cmd.Context(), c, args[0]); err != nil {
		return err
	}
	tpp, err := findTPPForProxy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}

	format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd),
		util.OutputTable, util.OutputWide, util.OutputJSON, util.OutputYAML)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if tpp == nil {
		if format == util.OutputJSON || format == util.OutputYAML {
			return util.PrintJSON(out, map[string]any{"enabled": false})
		}
		_, _ = fmt.Fprintln(out, "Traffic protection: off")
		return nil
	}
	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, tpp)
	case util.OutputYAML:
		return util.PrintYAML(out, tpp)
	default:
		_, _ = fmt.Fprintf(out, "Traffic protection: %s\n", spec.TPPMode(tpp))
		_, _ = fmt.Fprintf(out, "Paranoia:           %d\n", spec.TPPParanoia(tpp))
		_, _ = fmt.Fprintf(out, "Policy:             %s\n", tpp.Name)
		return nil
	}
}
