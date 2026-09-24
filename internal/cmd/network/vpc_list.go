// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/network/util"
)

const workloadNameLabel = "compute.datumapis.com/workload-name"

type vpcDetail struct {
	Network    *networkingv1alpha.Network           `json:"network"`
	Interfaces []networkingv1alpha.NetworkInterface `json:"interfaces"`
	Subnets    []networkingv1alpha.Subnet           `json:"subnets"`
	Services   []networkingv1alpha.NetworkService   `json:"services"`
}

func runVPCList(cmd *cobra.Command, vpc string) error {
	project := util.ProjectFromCmd(cmd)
	c, err := util.NewClient(project)
	if err != nil {
		return err
	}

	outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")
	noHeaders, _ := cmd.Root().PersistentFlags().GetBool("no-headers")

	return vpcList(cmd.Context(), cmd.OutOrStdout(), c, project, vpc, util.OutputFormat(outputFlag), noHeaders)
}

func vpcList(ctx context.Context, out io.Writer, c client.Client, project, vpc string, format util.OutputFormat, noHeaders bool) error {
	var net networkingv1alpha.Network
	if err := c.Get(ctx, client.ObjectKey{Namespace: util.ResourceNamespace, Name: vpc}, &net); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("VPC %q not found in project %s", vpc, project)
		}
		return err
	}

	var subnets networkingv1alpha.SubnetList
	if err := c.List(ctx, &subnets,
		client.InNamespace(util.ResourceNamespace),
		client.MatchingLabels{networkingv1alpha.NetworkLabel: vpc},
	); err != nil {
		return err
	}

	var allInterfaces networkingv1alpha.NetworkInterfaceList
	if err := c.List(ctx, &allInterfaces, client.InNamespace(util.ResourceNamespace)); err != nil {
		return err
	}

	var vpcInterfaces []networkingv1alpha.NetworkInterface
	for _, ni := range allInterfaces.Items {
		if ni.Spec.Network.Name == vpc {
			vpcInterfaces = append(vpcInterfaces, ni)
		}
	}

	var allServices networkingv1alpha.NetworkServiceList
	if err := c.List(ctx, &allServices, client.InNamespace(util.ResourceNamespace)); err != nil {
		return err
	}

	ifLabelSet := buildInterfaceLabelSets(vpcInterfaces)
	var vpcServices []networkingv1alpha.NetworkService
	for _, svc := range allServices.Items {
		sel, err := metav1.LabelSelectorAsSelector(&svc.Spec.NetworkInterfaces.Selector)
		if err != nil {
			continue
		}
		for _, ls := range ifLabelSet {
			if sel.Matches(ls) {
				vpcServices = append(vpcServices, svc)
				break
			}
		}
	}

	detail := vpcDetail{
		Network:    &net,
		Interfaces: vpcInterfaces,
		Subnets:    subnets.Items,
		Services:   vpcServices,
	}

	switch format {
	case util.OutputJSON:
		return util.PrintJSON(out, detail)
	case util.OutputYAML:
		return util.PrintYAML(out, detail)
	}

	prefix := ""
	if net.Status.IPAM != nil {
		prefix = net.Status.IPAM.IPv6Prefix
	}
	_, _ = fmt.Fprintf(out, "VPC %s  IPv6 %s  Ready %s\n\n",
		net.Name, prefix, util.ReadyStatus(net.Status.Conditions))

	printInterfaces(out, vpcInterfaces, format == util.OutputWide, noHeaders)
	printSubnets(out, subnets.Items, noHeaders)
	printServices(out, vpcServices, noHeaders)

	return nil
}

func printInterfaces(out io.Writer, items []networkingv1alpha.NetworkInterface, wide, noHeaders bool) {
	_, _ = fmt.Fprintln(out, "Interfaces")
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
		_, _ = fmt.Fprintln(out)
		return
	}

	tw := util.NewTabWriter(out)
	if !noHeaders {
		hdr := "  NAME\tWORKLOAD\tLOCATION\tINTERFACE\tIPV6\tPHASE\tREADY\tAGE"
		if wide {
			hdr += "\tCLAIM\tMODE\tEXTERNAL"
		}
		_, _ = fmt.Fprintln(tw, hdr)
	}
	for _, ni := range items {
		ipv6 := primaryAddress(ni.Spec.Addresses)
		ready := interfaceReady(ni.Status.Conditions)
		row := fmt.Sprintf("  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
			ni.Name,
			ni.Labels[workloadNameLabel],
			ni.Labels[networkingv1alpha.NetworkInterfaceLocationLabel],
			ni.Spec.InterfaceName,
			ipv6,
			string(ni.Status.Phase),
			ready,
			util.RelativeAge(metav1.Time{Time: ni.CreationTimestamp.Time}),
		)
		if wide {
			claim := ""
			if ni.Labels[networkingv1alpha.NetworkInterfaceHolderLabel] != "" {
				claim = ni.Labels[networkingv1alpha.NetworkInterfaceHolderLabel]
			}
			external := externalAddrs(ni.Spec.ExternalAddresses)
			row += fmt.Sprintf("\t%s\t%s\t%s", claim, string(ni.Spec.AttachmentMode), external)
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out)
}

func printSubnets(out io.Writer, items []networkingv1alpha.Subnet, noHeaders bool) {
	_, _ = fmt.Fprintln(out, "Subnets")
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
		_, _ = fmt.Fprintln(out)
		return
	}

	tw := util.NewTabWriter(out)
	if !noHeaders {
		_, _ = fmt.Fprintln(tw, "  NAME\tLOCATION\tCIDR\tREADY\tAGE")
	}
	for _, s := range items {
		cidr := ""
		if s.Status.StartAddress != nil && s.Status.PrefixLength != nil {
			cidr = fmt.Sprintf("%s/%d", *s.Status.StartAddress, *s.Status.PrefixLength)
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
			s.Name,
			s.Spec.Location.Name,
			cidr,
			util.ReadyStatus(s.Status.Conditions),
			util.RelativeAge(metav1.Time{Time: s.CreationTimestamp.Time}),
		)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out)
}

func printServices(out io.Writer, items []networkingv1alpha.NetworkService, noHeaders bool) {
	_, _ = fmt.Fprintln(out, "Services")
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
		_, _ = fmt.Fprintln(out)
		return
	}

	tw := util.NewTabWriter(out)
	if !noHeaders {
		_, _ = fmt.Fprintln(tw, "  NAME\tPORTS\tLOCATIONS\tMEMBERS\tHEALTHY\tREADY\tAGE")
	}
	for _, svc := range items {
		ports := formatPorts(svc.Spec.Ports)
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			svc.Name,
			ports,
			svc.Status.Summary.Locations,
			svc.Status.Summary.Members,
			svc.Status.Summary.Healthy,
			util.ReadyStatus(svc.Status.Conditions),
			util.RelativeAge(metav1.Time{Time: svc.CreationTimestamp.Time}),
		)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out)
}

func primaryAddress(addrs []networkingv1alpha.NetworkInterfaceAddress) string {
	for _, a := range addrs {
		if a.Primary {
			return a.Address
		}
	}
	if len(addrs) > 0 {
		return addrs[0].Address
	}
	return ""
}

func externalAddrs(addrs []networkingv1alpha.NetworkInterfaceExternalAddress) string {
	if len(addrs) == 0 {
		return ""
	}
	s := ""
	for i, a := range addrs {
		if i > 0 {
			s += ","
		}
		s += a.Address
	}
	return s
}

func interfaceReady(conditions []metav1.Condition) string {
	for _, condType := range []string{
		networkingv1alpha.NetworkInterfaceAllocated,
		networkingv1alpha.NetworkInterfacePrepared,
		networkingv1alpha.NetworkInterfaceProgrammed,
	} {
		c := util.FindCondition(conditions, condType)
		if c == nil || c.Status != metav1.ConditionTrue {
			return string(metav1.ConditionFalse)
		}
	}
	return string(metav1.ConditionTrue)
}

func formatPorts(ports []networkingv1alpha.NetworkServicePort) string {
	s := ""
	for i, p := range ports {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("%d/%s", p.Port, p.Protocol)
	}
	return s
}

func buildInterfaceLabelSets(interfaces []networkingv1alpha.NetworkInterface) []labels.Set {
	sets := make([]labels.Set, len(interfaces))
	for i, ni := range interfaces {
		sets[i] = labels.Set(ni.Labels)
	}
	return sets
}
