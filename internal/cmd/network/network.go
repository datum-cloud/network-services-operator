// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

const (
	listNetworks = "datumctl network list"
	minMTU       = 1300
	maxMTU       = 8856
)

func listCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List networks",
		Example: `  datumctl network list
  datumctl network list -o wide`,
		Args: cobra.NoArgs,
		RunE: runList,
	}
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func runList(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, r, err := clients(cmd)
	if err != nil {
		return err
	}
	networks, err := networkagent.Networks(cmd.Context(), r, util.ResourceNamespace)
	if err != nil {
		return util.ClassifyError(fmt.Errorf("listing networks: %w", err))
	}

	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, networks); done {
		return err
	}
	if format == util.OutputName {
		for _, n := range networks {
			_, _ = fmt.Fprintln(out, n.Name)
		}
		return nil
	}
	if len(networks) == 0 {
		_, _ = fmt.Fprintln(out, "No networks found.")
		nextSteps(cmd, "datumctl network create <name>")
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	wide := format == util.OutputWide
	tw := util.NewTabWriter(out)
	if !noHeaders {
		header := "NAME\tREADY\tLOCATIONS\tINSTANCES\tSERVICES\tPROBLEM"
		if wide {
			header = "NAME\tREADY\tIPV6 PREFIX\tLOCATIONS\tINSTANCES\tWORKLOADS\tSERVICES\tPROBLEM\tAGE"
		}
		_, _ = fmt.Fprintln(tw, header)
	}
	for _, n := range networks {
		problem := problemCell(n.Reason, n.Actionability)
		if wide {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n", n.Name, readyWord(n.Ready, n.Deleting),
				util.OrDash(n.IPv6Prefix), util.OrDash(n.Locations), n.Instances, n.Workloads, n.Services,
				problem, util.OrDash(n.Age))
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", n.Name, readyWord(n.Ready, n.Deleting),
			util.OrDash(n.Locations), n.Instances, n.Services, problem)
	}
	return tw.Flush()
}

func readyWord(ready, deleting bool) string {
	switch {
	case deleting:
		return "Deleting"
	case ready:
		return "Ready"
	default:
		return "NotReady"
	}
}

func problemCell(reason string, who networkagent.Actionability) string {
	if reason == "" {
		return util.OrDash("")
	}
	return fmt.Sprintf("%s (%s)", reason, whoShort(who))
}

func describeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "describe <name>",
		Aliases:           []string{"get", "show"},
		Short:             "Show a network: its locations, workloads, services and health",
		Example:           `  datumctl network describe default`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNetworkNames,
		RunE:              runDescribe,
	}
}

func runDescribe(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, r, err := clients(cmd)
	if err != nil {
		return err
	}
	v, err := networkagent.Network(cmd.Context(), r, util.ResourceNamespace, args[0])
	if err != nil {
		return notFound("network", args[0], listNetworks, err)
	}

	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, v); done {
		return err
	}
	if format == util.OutputName {
		_, _ = fmt.Fprintln(out, v.Name)
		return nil
	}
	printNetworkView(out, v)
	return nil
}

func printNetworkView(w io.Writer, v networkagent.NetworkView) {
	tw := util.NewTabWriter(w)
	_, _ = fmt.Fprintf(tw, "Name:\t%s\n", v.Name)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", readyWord(v.Ready, v.Deleting))
	_, _ = fmt.Fprintf(tw, "Address families:\t%s\n", util.OrDash(strings.Join(v.IPFamilies, ", ")))
	_, _ = fmt.Fprintf(tw, "IPv6 prefix:\t%s\n", util.OrDash(v.IPv6Prefix))
	_, _ = fmt.Fprintf(tw, "MTU:\t%d\n", v.MTU)
	_, _ = fmt.Fprintf(tw, "Age:\t%s\n", util.OrDash(v.Age))
	_ = tw.Flush()

	_, _ = fmt.Fprintln(w, "\nLocations:")
	if len(v.Locations) == 0 {
		_, _ = fmt.Fprintln(w, "  none yet — a network reaches a location when a workload placed there attaches to it")
	} else {
		tw = util.NewTabWriter(w)
		_, _ = fmt.Fprintln(tw, "  LOCATION\tREADY\tADDRESS RANGE\tINSTANCES\tSERVING")
		for _, l := range v.Locations {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%d\n", l.Location, yesNo(l.Ready),
				util.OrDash(strings.Join(l.AddressRanges, ", ")), l.Instances, l.InstancesServing)
		}
		_ = tw.Flush()
	}

	printList(w, "Workloads", v.Workloads)
	printList(w, "Network services", v.Services)
	if len(v.KeptInterfaces) > 0 {
		printList(w, "Kept interfaces (holding addresses with no instance)", v.KeptInterfaces)
	}
	_, _ = fmt.Fprintf(w, "\nDiagnosis:\n  %s\n", v.Diagnosis)
	for _, u := range v.Unreadable {
		_, _ = fmt.Fprintf(w, "  could not read %s\n", u)
	}
}

func printList(w io.Writer, title string, items []string) {
	_, _ = fmt.Fprintf(w, "\n%s:", title)
	if len(items) == 0 {
		_, _ = fmt.Fprintln(w, " none")
		return
	}
	_, _ = fmt.Fprintln(w)
	for _, i := range items {
		_, _ = fmt.Fprintf(w, "  %s\n", i)
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func createCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a network",
		Long: `Create a network.

Networks are IPv6-only and Datum assigns each one an IPv6 /48, so the name is
the only required choice. The name cannot be changed later.

A new network is in no location until a workload placed there attaches to it.`,
		Example: `  datumctl network create backend
  datumctl network create backend --mtu 1400 --wait`,
		Args: cobra.ExactArgs(1),
		RunE: runCreate,
	}
	cmd.Flags().Int32("mtu", 0, fmt.Sprintf("Largest packet the network carries, %d to %d (default 1440)", minMTU, maxMTU))
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without creating")
	cmd.Flags().Bool("wait", false, "Wait until the network is ready")
	cmd.Flags().Duration("timeout", 2*time.Minute, "How long --wait waits")
	return cmd
}

func runCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	mtu, _ := cmd.Flags().GetInt32("mtu")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	waitReady, _ := cmd.Flags().GetBool("wait")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	doc, _, err := networkagent.Render(networkagent.RenderInput{Kind: networkagent.KindNetwork, Name: name, MTU: mtu})
	if err != nil {
		return util.UsageErrorf("%s", err.Error())
	}

	c, _, err := clients(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	obj := &unstructured.Unstructured{Object: doc}
	opts := append([]client.CreateOption{client.FieldOwner(fieldManager)}, util.DryRunOpts(dryRun)...)
	if err := c.Create(ctx, obj, opts...); err != nil {
		return util.ClassifyError(fmt.Errorf("creating network %q: %w", name, err))
	}

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "Network %q validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Network %q created.\n", name)

	if waitReady {
		if err := waitForReady(cmd, c, name, timeout); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Network %q is ready.\n", name)
	}
	nextSteps(cmd,
		"datumctl network describe "+name,
		"datumctl compute deploy <workload> --image <image> --location <location> --network "+name)
	return nil
}

func waitForReady(cmd *cobra.Command, c client.Client, name string, timeout time.Duration) error {
	var last *metav1.Condition
	err := wait.PollUntilContextTimeout(cmd.Context(), 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			var n networkingv1alpha.Network
			if err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, &n); err != nil {
				return false, nil
			}
			last = apimeta.FindStatusCondition(n.Status.Conditions, networkingv1alpha.NetworkReady)
			return last != nil && last.Status == metav1.ConditionTrue, nil
		})
	if err == nil {
		return nil
	}
	ce := util.NewCLIError(util.ExitUnavailable, fmt.Sprintf("network %q was not ready within %s", name, timeout)).
		WithFix("see why with:\n       datumctl network diagnose " + name)
	if last != nil {
		ce = util.NewCLIError(util.ExitUnavailable, fmt.Sprintf("network %q was not ready within %s: %s",
			name, timeout, last.Reason)).WithFix("see why with:\n       datumctl network diagnose " + name)
	}
	return ce
}

func updateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Change a network's MTU",
		Long: `Change a network's MTU, the only setting that can change after create.

The new MTU applies to every instance on the network.`,
		Example:           `  datumctl network update backend --mtu 1400`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNetworkNames,
		RunE:              runUpdate,
	}
	cmd.Flags().Int32("mtu", 0, fmt.Sprintf("Largest packet the network carries, %d to %d", minMTU, maxMTU))
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without changing anything")
	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	if !cmd.Flags().Changed("mtu") {
		return util.UsageErrorf("nothing to change").WithFix("datumctl network update " + name + " --mtu 1400")
	}
	mtu, _ := cmd.Flags().GetInt32("mtu")
	if mtu < minMTU || mtu > maxMTU {
		return util.UsageErrorf("--mtu must be between %d and %d, not %d", minMTU, maxMTU, mtu)
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, r, err := clients(cmd)
	if err != nil {
		return err
	}
	v, err := networkagent.Network(cmd.Context(), r, util.ResourceNamespace, name)
	if err != nil {
		return notFound("network", name, listNetworks, err)
	}
	if v.MTU == mtu {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network %q already has an MTU of %d.\n", name, mtu)
		return nil
	}

	if !dryRun {
		instances := 0
		for _, l := range v.Locations {
			instances += l.Instances
		}
		prompt := fmt.Sprintf("This changes the MTU of network %q from %d to %d for all %d instance(s) on it.",
			name, v.MTU, mtu, instances)
		if err := confirmed(cmd, prompt, name); err != nil {
			return err
		}
	}

	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	n := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace}}
	patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(`{"spec":{"mtu":%d}}`, mtu)))
	opts := []client.PatchOption{client.FieldOwner(fieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	if err := c.Patch(ctx, n, patch, opts...); err != nil {
		return util.ClassifyError(fmt.Errorf("updating network %q: %w", name, err))
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network %q update validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network %q now has an MTU of %d.\n", name, mtu)
	return nil
}

func deleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a network",
		Long: `Delete a network.

Workloads on it lose their network and network services on it stop serving.
Removal finishes only once every instance has let go of its address, so a
network with workloads on it stays until they are removed or moved.`,
		Example: `  datumctl network delete backend
  datumctl network delete backend --yes`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNetworkNames,
		RunE:              runDelete,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without deleting")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, r, err := clients(cmd)
	if err != nil {
		return err
	}
	v, err := networkagent.Network(cmd.Context(), r, util.ResourceNamespace, name)
	if err != nil {
		return notFound("network", name, listNetworks, err)
	}

	if !dryRun {
		if err := confirmed(cmd, deletePrompt(v), name); err != nil {
			return err
		}
	}

	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	n := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace}}
	if err := c.Delete(ctx, n, util.DryRunDeleteOpts(dryRun)...); err != nil {
		return notFound("network", name, listNetworks, err)
	}

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "Network %q validated for deletion.\n", name)
		return nil
	}
	if len(v.Workloads) > 0 || len(v.KeptInterfaces) > 0 {
		_, _ = fmt.Fprintf(out, "Network %q is being deleted. It is removed once the instances and kept interfaces "+
			"on it let go of their addresses.\n", name)
		nextSteps(cmd, "datumctl network diagnose "+name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Network %q deleted.\n", name)
	return nil
}

func deletePrompt(v networkagent.NetworkView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This will delete network %q.", v.Name)
	if len(v.Workloads) > 0 {
		fmt.Fprintf(&b, "\n  Workloads that lose their network: %s", strings.Join(v.Workloads, ", "))
	}
	if len(v.Services) > 0 {
		fmt.Fprintf(&b, "\n  Network services that stop serving: %s", strings.Join(v.Services, ", "))
	}
	if len(v.KeptInterfaces) > 0 {
		fmt.Fprintf(&b, "\n  Kept interfaces whose addresses are given up: %s", strings.Join(v.KeptInterfaces, ", "))
	}
	return b.String()
}

func diagnoseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnose <name>",
		Short: "Explain why a network, or a workload's use of it, is not working",
		Long: `Walk a network, every location it has reached, each location's address range
and every instance's network interface, and report the cause that names
something: what it is, who has to act, and what to do.`,
		Example:           `  datumctl network diagnose default`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNetworkNames,
		RunE:              runDiagnose,
	}
}

func runDiagnose(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, r, err := clients(cmd)
	if err != nil {
		return err
	}
	d, err := networkagent.DiagnoseNetwork(cmd.Context(), r, util.ResourceNamespace, args[0])
	if err != nil {
		return notFound("network", args[0], listNetworks, err)
	}
	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, d); done {
		return err
	}
	_, _ = fmt.Fprintf(out, "Network %s: %s\n\n%s\n", d.Network, readyWord(d.Ready, d.Deleting), d.Summary)
	printCauses(out, d.RootCause, d.OtherCauses, d.Unreadable)
	return nil
}
