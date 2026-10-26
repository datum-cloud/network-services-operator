// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

const listServices = "datumctl network service list"

func serviceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Aliases: []string{"services", "svc"},
		Short:   "Manage network services",
		Long: `Manage network services.

A network service gives a workload's instances on one network a stable name and
named ports. Datum keeps its membership current as instances come and go, and
sends each request to the nearest location with a ready instance. A load
balancer reaches it by the service's name and a port name.`,
	}
	cmd.AddCommand(
		serviceListCommand(),
		serviceDescribeCommand(),
		serviceCreateCommand(),
		serviceUpdateCommand(),
		serviceDeleteCommand(),
	)
	return cmd
}

func serviceListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List network services",
		Example: `  datumctl network service list`,
		Args:    cobra.NoArgs,
		RunE:    runServiceList,
	}
	cmd.Flags().Bool("no-headers", false, "Omit column headers")
	return cmd
}

func runServiceList(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, r, err := clients(cmd)
	if err != nil {
		return err
	}
	services, err := networkagent.Services(cmd.Context(), r, util.ResourceNamespace)
	if err != nil {
		return util.ClassifyError(fmt.Errorf("listing network services: %w", err))
	}

	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, services); done {
		return err
	}
	if format == util.OutputName {
		for _, s := range services {
			_, _ = fmt.Fprintln(out, s.Name)
		}
		return nil
	}
	if len(services) == 0 {
		_, _ = fmt.Fprintln(out, "No network services found.")
		nextSteps(cmd, "datumctl network service create <name> --workload <workload> --port http:8080")
		return nil
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	wide := format == util.OutputWide
	tw := util.NewTabWriter(out)
	if !noHeaders {
		header := "NAME\tREADY\tNETWORK\tPORTS\tSERVING\tPROBLEM"
		if wide {
			header = "NAME\tREADY\tNETWORK\tPORTS\tSELECTS\tSERVING\tPROBLEM"
		}
		_, _ = fmt.Fprintln(tw, header)
	}
	for _, s := range services {
		ready := "NotReady"
		if s.Ready {
			ready = "Ready"
		}
		network := util.OrDash(strings.Join(s.Networks, ", "))
		problem := problemCell(s.Reason, s.Actionability)
		if wide {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, ready, network, s.Ports,
				util.OrDash(s.Selects), s.Members, problem)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, ready, network, s.Ports, s.Members, problem)
	}
	return tw.Flush()
}

func serviceDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "describe <name>",
		Aliases:           []string{"get", "show"},
		Short:             "Show a network service: what it selects, its ports, where it is serving, and why not",
		Example:           `  datumctl network service describe api`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: plugincli.CompleteNetworkServiceNames,
		RunE:              runServiceDescribe,
	}
}

func serviceDiagnosis(cmd *cobra.Command, name string) (*networkagent.ServiceDiagnosis, error) {
	_, r, err := clients(cmd)
	if err != nil {
		return nil, err
	}
	d, err := networkagent.DiagnoseService(cmd.Context(), r, util.ResourceNamespace, name)
	if err != nil {
		return nil, notFound("network service", name, listServices, err)
	}
	return d, nil
}

func runServiceDescribe(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	d, err := serviceDiagnosis(cmd, args[0])
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if done, err := printStructured(out, format, d); done {
		return err
	}
	if format == util.OutputName {
		_, _ = fmt.Fprintln(out, d.Service)
		return nil
	}

	ports := make([]string, 0, len(d.Ports))
	for _, p := range d.Ports {
		port := fmt.Sprintf("%s → %d/%s", p.Name, p.Port, util.OrDash(p.Protocol))
		if p.PortRange != nil {
			port = fmt.Sprintf("%s → %d-%d/%s", p.Name, p.PortRange.Start, p.PortRange.End, util.OrDash(p.Protocol))
		}
		if p.AppProtocol != "" {
			port += " (" + networkagent.AppProtocolLabel(p.AppProtocol) + ")"
		}
		ports = append(ports, port)
	}
	tw := util.NewTabWriter(out)
	_, _ = fmt.Fprintf(tw, "Name:\t%s\n", d.Service)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", map[bool]string{true: "Ready", false: "NotReady"}[d.Ready])
	_, _ = fmt.Fprintf(tw, "Network:\t%s\n", util.OrDash(strings.Join(d.Networks, ", ")))
	_, _ = fmt.Fprintf(tw, "Selects:\t%s\n", util.OrDash(d.Selects))
	_, _ = fmt.Fprintf(tw, "Ports:\t%s\n", util.OrDash(strings.Join(ports, ", ")))
	_, _ = fmt.Fprintf(tw, "Instances:\t%d serving of %d, in %d location(s)\n",
		d.Members.Healthy, d.Members.Members, d.Members.Locations)
	_ = tw.Flush()

	if len(d.Locations) > 0 {
		_, _ = fmt.Fprintln(out, "\nLocations:")
		tw = util.NewTabWriter(out)
		_, _ = fmt.Fprintln(tw, "  LOCATION\tINSTANCES\tSERVING\tTAKING TRAFFIC")
		for _, l := range d.Locations {
			_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\n", l.Name, l.Members, l.Healthy, yesNo(l.Serving))
		}
		_ = tw.Flush()
	}
	_, _ = fmt.Fprintf(out, "\nDiagnosis:\n  %s\n", d.Summary)
	if len(d.NotServing) > 0 {
		_, _ = fmt.Fprintln(out, "\nInstances not serving:")
		tw := util.NewTabWriter(out)
		_, _ = fmt.Fprintln(tw, "  INTERFACE\tWORKLOAD\tLOCATION\tREPORTS")
		for _, m := range d.NotServing {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", m.Interface, util.OrDash(m.Workload),
				util.OrDash(m.Location), util.OrDash(m.Reason))
		}
		_ = tw.Flush()
	}
	if len(d.WorkloadsInProject) > 0 {
		_, _ = fmt.Fprintf(out, "\nWorkloads in this project: %s\n", strings.Join(d.WorkloadsInProject, ", "))
	}
	return nil
}

func addSelectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("workload", "", "Workload whose instances answer for the service")
	cmd.Flags().StringToString("selector", nil, "Labels an instance's network interface must carry, e.g. app=api,tier=web; instead of --workload")
	cmd.Flags().StringArray("port", nil, "Port as name:number, e.g. http:8080. For a port that speaks HTTP/2, "+
		"which gRPC needs, use the long form: name=grpc,port=9090,app-protocol=h2c. Repeat for more")
	cmd.MarkFlagsMutuallyExclusive("workload", "selector")
}

func parsePorts(raw []string) ([]networkagent.RenderPort, error) {
	ports := make([]networkagent.RenderPort, 0, len(raw))
	for _, p := range raw {
		port, err := parsePort(p)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	return ports, nil
}

// parsePort reads the short form name:number or the long form
// name=…,port=…,app-protocol=…, which carries what the short form cannot.
func parsePort(raw string) (networkagent.RenderPort, error) {
	usage := util.UsageErrorf("--port %q must be name:number, e.g. http:8080, "+
		"or name=<name>,port=<number>,app-protocol=h2c", raw)

	if !strings.Contains(raw, "=") {
		name, number, ok := strings.Cut(raw, ":")
		n, err := strconv.ParseInt(number, 10, 32)
		if !ok || name == "" || err != nil {
			return networkagent.RenderPort{}, usage
		}
		return networkagent.RenderPort{Name: name, Port: int32(n)}, nil
	}

	var port networkagent.RenderPort
	seen := map[string]bool{}
	for _, field := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return networkagent.RenderPort{}, usage
		}
		if seen[key] {
			return networkagent.RenderPort{}, util.UsageErrorf("--port %q sets %s twice", raw, key)
		}
		seen[key] = true
		switch key {
		case "name":
			port.Name = value
		case "port":
			n, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return networkagent.RenderPort{}, usage
			}
			port.Port = int32(n)
		case "app-protocol":
			port.AppProtocol = value
		default:
			return networkagent.RenderPort{}, util.UsageErrorf("--port %q has an unknown key %q: use name, port and app-protocol", raw, key)
		}
	}
	if port.Name == "" || port.Port == 0 {
		return networkagent.RenderPort{}, usage
	}
	return port, nil
}

func serviceCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a network service for a workload",
		Long: `Create a network service for a workload.

The service covers every instance of the workload, in every location it runs,
including instances added later. It can be created before the workload exists
and starts serving once instances report themselves ready. The name cannot be
changed later.`,
		Example: `  datumctl network service create api --workload api-backend --port http:8080
  datumctl network service create web --selector app=web --port http:80 --port name=grpc,port=9090,app-protocol=h2c`,
		Args: cobra.ExactArgs(1),
		RunE: runServiceCreate,
	}
	addSelectorFlags(cmd)
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without creating")
	return cmd
}

func runServiceCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	in, err := serviceInput(cmd, name)
	if err != nil {
		return err
	}
	if in.Workload == "" && len(in.MatchLabels) == 0 {
		return util.UsageErrorf("say which instances the service covers").
			WithFix("datumctl network service create " + name + " --workload <workload> --port http:8080")
	}
	doc, _, err := networkagent.Render(in)
	if err != nil {
		return util.UsageErrorf("%s", err.Error())
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, _, err := clients(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	opts := append([]client.CreateOption{client.FieldOwner(fieldManager)}, util.DryRunOpts(dryRun)...)
	if err := c.Create(ctx, &unstructured.Unstructured{Object: doc}, opts...); err != nil {
		return util.ClassifyError(fmt.Errorf("creating network service %q: %w", name, err))
	}

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "Network service %q validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Network service %q created.\n", name)
	nextSteps(cmd,
		"datumctl network service describe "+name,
		fmt.Sprintf("datumctl alb create <name> --network-service %s --port %s", name, in.Ports[0].Name))
	return nil
}

func serviceInput(cmd *cobra.Command, name string) (networkagent.RenderInput, error) {
	workload, _ := cmd.Flags().GetString("workload")
	selector, _ := cmd.Flags().GetStringToString("selector")
	rawPorts, _ := cmd.Flags().GetStringArray("port")
	ports, err := parsePorts(rawPorts)
	if err != nil {
		return networkagent.RenderInput{}, err
	}
	return networkagent.RenderInput{
		Kind:        networkagent.KindNetworkService,
		Name:        name,
		Workload:    workload,
		MatchLabels: selector,
		Ports:       ports,
	}, nil
}

func serviceUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Change what a network service selects, or its ports",
		Long: `Change what a network service selects, or its ports.

--workload or --selector replaces what the service selects. --port replaces the
whole port list, so name every port the service should keep, with its
app-protocol if it has one. A load balancer pointing at a port name you remove
stops reaching the service.`,
		Example: `  datumctl network service update api --workload api-v2
  datumctl network service update api --port http:8080 --port metrics:9100`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: plugincli.CompleteNetworkServiceNames,
		RunE:              runServiceUpdate,
	}
	addSelectorFlags(cmd)
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without changing anything")
	return cmd
}

func runServiceUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	if !cmd.Flags().Changed("workload") && !cmd.Flags().Changed("selector") && !cmd.Flags().Changed("port") {
		return util.UsageErrorf("nothing to change").
			WithFix("datumctl network service update " + name + " --workload <workload>")
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, _, err := clients(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	var svc networkingv1alpha.NetworkService
	if err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, &svc); err != nil {
		return notFound("network service", name, listServices, err)
	}

	for _, port := range svc.Spec.Ports {
		if port.PortRange != nil {
			return util.UsageErrorf("network service %q contains portRange; use a range-aware manifest update", name)
		}
	}
	in, err := serviceInput(cmd, name)
	if err != nil {
		return err
	}
	if !cmd.Flags().Changed("port") {
		for _, p := range svc.Spec.Ports {
			in.Ports = append(in.Ports, networkagent.RenderPort{Name: p.Name, Port: p.Port})
		}
	}
	if in.Workload == "" && len(in.MatchLabels) == 0 {
		in.MatchLabels = svc.Spec.NetworkInterfaces.Selector.MatchLabels
		if len(in.MatchLabels) == 0 {
			return util.UsageErrorf("network service %q selects by expression, which this command cannot keep while "+
				"changing ports", name).WithFix("pass --workload or --selector as well")
		}
	}
	if _, _, err := networkagent.Render(in); err != nil {
		return util.UsageErrorf("%s", err.Error())
	}

	if cmd.Flags().Changed("workload") || cmd.Flags().Changed("selector") {
		matchLabels := in.MatchLabels
		if in.Workload != "" {
			matchLabels = map[string]string{networkagent.WorkloadNameLabel: in.Workload}
		}
		svc.Spec.NetworkInterfaces.Selector = metav1.LabelSelector{MatchLabels: matchLabels}
	}
	if cmd.Flags().Changed("port") {
		svc.Spec.Ports = nil
		for _, p := range in.Ports {
			port := networkingv1alpha.NetworkServicePort{Name: p.Name, Port: p.Port}
			// Render has already validated the value.
			if appProtocol, _ := networkagent.AppProtocol(p.AppProtocol); appProtocol != "" {
				port.AppProtocol = ptr.To(networkingv1alpha.NetworkServiceAppProtocol(appProtocol))
			}
			svc.Spec.Ports = append(svc.Spec.Ports, port)
		}
	}

	opts := []client.UpdateOption{client.FieldOwner(fieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	if err := c.Update(ctx, &svc, opts...); err != nil {
		return util.ClassifyError(fmt.Errorf("updating network service %q: %w", name, err))
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network service %q update validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network service %q updated.\n", name)
	return nil
}

func serviceDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a network service",
		Long: `Delete a network service. Load balancers sending traffic to it stop reaching
anything on that route; they are listed before you confirm.`,
		Example: `  datumctl network service delete api
  datumctl network service delete api --yes`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: plugincli.CompleteNetworkServiceNames,
		RunE:              runServiceDelete,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without deleting")
	return cmd
}

func runServiceDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, _, err := clients(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(cmd.Context())
	defer cancel()
	var svc networkingv1alpha.NetworkService
	if err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, &svc); err != nil {
		return notFound("network service", name, listServices, err)
	}

	if !dryRun {
		prompt := fmt.Sprintf("This will delete network service %q.", name)
		if users := loadBalancersUsing(ctx, c, name); len(users) > 0 {
			prompt += "\n  Load balancers that stop reaching it: " + strings.Join(users, ", ")
		}
		if err := confirmed(cmd, prompt, name); err != nil {
			return err
		}
	}

	if err := c.Delete(ctx, &svc, util.DryRunDeleteOpts(dryRun)...); err != nil {
		return notFound("network service", name, listServices, err)
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network service %q validated for deletion.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Network service %q deleted.\n", name)
	return nil
}

func loadBalancersUsing(ctx context.Context, c client.Client, service string) []string {
	var proxies networkingv1alpha.HTTPProxyList
	if err := c.List(ctx, &proxies, client.InNamespace(util.ResourceNamespace)); err != nil {
		return nil
	}
	var out []string
	for _, p := range proxies.Items {
	rules:
		for _, rule := range p.Spec.Rules {
			for _, b := range rule.Backends {
				if b.NetworkService != nil && b.NetworkService.Name == service {
					out = append(out, p.Name)
					break rules
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
