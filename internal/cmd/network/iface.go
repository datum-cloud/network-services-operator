// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

func interfaceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "interface",
		Aliases: []string{"interfaces", "iface"},
		Short:   "Inspect instances' network interfaces",
		Long: `Inspect the network interfaces instances hold on your networks.

Datum creates an interface for each instance. A workload can ask to keep an
instance's addresses when the instance goes away; the kept interface holds them
until an instance of the same name comes back.`,
	}
	cmd.AddCommand(interfaceListCommand(), interfaceReleaseCommand())
	return cmd
}

func interfaceListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List network interfaces",
		Example: `  datumctl network interface list
  datumctl network interface list --network backend --workload api
  datumctl network interface list --kept`,
		Args: cobra.NoArgs,
		RunE: runInterfaceList,
	}
	cmd.Flags().String("network", "", "Only interfaces on this network")
	cmd.Flags().String("workload", "", "Only interfaces of this workload")
	cmd.Flags().Bool("kept", false, "Only kept interfaces, holding addresses with no instance")
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	_ = cmd.RegisterFlagCompletionFunc("network", completeNetworkNames)
	return cmd
}

func runInterfaceList(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, r, err := clients(cmd)
	if err != nil {
		return err
	}
	all, err := networkagent.Interfaces(cmd.Context(), r, util.ResourceNamespace)
	if err != nil {
		return util.ClassifyError(fmt.Errorf("listing network interfaces: %w", err))
	}

	network, _ := cmd.Flags().GetString("network")
	workload, _ := cmd.Flags().GetString("workload")
	kept, _ := cmd.Flags().GetBool("kept")
	interfaces := all[:0]
	for _, i := range all {
		if (network == "" || i.Network == network) && (workload == "" || i.Workload == workload) && (!kept || i.Kept) {
			interfaces = append(interfaces, i)
		}
	}

	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, interfaces); done {
		return err
	}
	if format == util.OutputName {
		for _, i := range interfaces {
			_, _ = fmt.Fprintln(out, i.Name)
		}
		return nil
	}
	if len(interfaces) == 0 {
		_, _ = fmt.Fprintln(out, "No network interfaces found.")
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	tw := util.NewTabWriter(out)
	if !noHeaders {
		_, _ = fmt.Fprintln(tw, "NAME\tNETWORK\tLOCATION\tWORKLOAD\tADDRESSES\tSTATE\tAGE")
	}
	for _, i := range interfaces {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i.Name, i.Network, util.OrDash(i.Location),
			util.OrDash(i.Workload), util.OrDash(strings.Join(i.Addresses, ", ")), interfaceState(i), util.OrDash(i.Age))
	}
	return tw.Flush()
}

func interfaceState(i networkagent.InterfaceSummary) string {
	switch {
	case i.Kept:
		return "Kept"
	case i.Serving:
		return "Serving"
	default:
		return "NotServing"
	}
}

func interfaceReleaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "release <name>",
		Short:  "Not available: a kept interface cannot be released",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runInterfaceRelease,
	}
	cmd.Flags().Bool("dry-run", false, "Has no effect: the command always refuses")
	return cmd
}

func runInterfaceRelease(_ *cobra.Command, args []string) error {
	return util.NewCLIError(util.ExitUnavailable, fmt.Sprintf("network interface %q cannot be released", args[0])).
		WithFix("a kept interface holds its addresses until an instance of the same name comes back")
}
