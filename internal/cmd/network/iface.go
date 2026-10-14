// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

const listInterfaces = "datumctl network interface list"

func interfaceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "interface",
		Aliases: []string{"interfaces", "iface"},
		Short:   "Inspect instances' network interfaces and release kept ones",
		Long: `Inspect the network interfaces instances hold on your networks.

Datum creates an interface for each instance. A workload can ask to keep an
instance's addresses when the instance goes away; the kept interface holds them
until an instance of the same name comes back, or until you release it.`,
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
		Use:   "release <name>",
		Short: "Give up a kept interface's addresses for good",
		Long: `Delete a kept network interface, giving up its addresses for good.

Only an interface no instance holds can be released. Its addresses do not come
back: anything that refers to them, such as a DNS record or a firewall rule,
has to change.`,
		Example:           `  datumctl network interface release api-0-eth0`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeKeptInterfaceNames,
		RunE:              runInterfaceRelease,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without releasing")
	return cmd
}

func runInterfaceRelease(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, _, err := clients(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	var iface networkingv1alpha.NetworkInterface
	if err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, &iface); err != nil {
		return notFound("network interface", name, listInterfaces, err)
	}
	if iface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseAvailable {
		return util.NewCLIError(util.ExitConflict, fmt.Sprintf("network interface %q is held by a running instance", name)).
			WithFix("an instance's interface goes when the instance does; only kept interfaces can be released:\n       " +
				listInterfaces + " --kept")
	}

	if !dryRun {
		var addresses []string
		for _, a := range iface.Spec.Addresses {
			addresses = append(addresses, a.Address)
		}
		for _, a := range iface.Spec.ExternalAddresses {
			addresses = append(addresses, a.Address)
		}
		prompt := fmt.Sprintf("This releases network interface %q and gives up its addresses for good: %s",
			name, util.OrDash(strings.Join(addresses, ", ")))
		if err := confirmed(cmd, prompt, name); err != nil {
			return err
		}
	}

	if err := c.Delete(ctx, &iface, util.DryRunDeleteOpts(dryRun)...); err != nil {
		return notFound("network interface", name, listInterfaces, err)
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network interface %q validated for release.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network interface %q released.\n", name)
	return nil
}
