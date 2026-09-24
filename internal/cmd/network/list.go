// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/network/util"
)

type networkView struct {
	Name       string                     `json:"name"`
	IPv6Prefix string                     `json:"ipv6Prefix,omitempty"`
	Locations  int                        `json:"locations"`
	Interfaces int                        `json:"interfaces"`
	Ready      string                     `json:"ready"`
	MTU        int32                      `json:"mtu,omitempty"`
	IPAM       string                     `json:"ipam,omitempty"`
	Reason     string                     `json:"reason,omitempty"`
	Age        string                     `json:"age"`
	Network    *networkingv1alpha.Network `json:"network,omitempty"`
}

type listOpts struct {
	format    util.OutputFormat
	noHeaders bool
}

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VPCs in the project",
		Args:  util.NoPositionalArgs,
		RunE:  runList,
	}
	return cmd
}

func runList(cmd *cobra.Command, _ []string) error {
	project := util.ProjectFromCmd(cmd)
	c, err := util.NewClient(project)
	if err != nil {
		return err
	}

	outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")
	noHeaders, _ := cmd.Root().PersistentFlags().GetBool("no-headers")
	opts := listOpts{
		format:    util.OutputFormat(outputFlag),
		noHeaders: noHeaders,
	}

	return listNetworks(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), c, project, opts)
}

func listNetworks(ctx context.Context, out, errOut io.Writer, c client.Client, project string, opts listOpts) error {
	var networks networkingv1alpha.NetworkList
	var contexts networkingv1alpha.NetworkContextList
	var interfaces networkingv1alpha.NetworkInterfaceList

	if err := c.List(ctx, &networks, client.InNamespace(util.ResourceNamespace)); err != nil {
		return err
	}
	if err := c.List(ctx, &contexts, client.InNamespace(util.ResourceNamespace)); err != nil {
		return err
	}
	if err := c.List(ctx, &interfaces, client.InNamespace(util.ResourceNamespace)); err != nil {
		return err
	}

	if len(networks.Items) == 0 {
		_, _ = fmt.Fprintf(errOut, "No VPCs found in project %s.\n", project)
		return nil
	}

	ctxCount := make(map[string]int)
	for _, nc := range contexts.Items {
		name := nc.Labels[networkingv1alpha.NetworkLabel]
		if name != "" {
			ctxCount[name]++
		}
	}

	ifCount := make(map[string]int)
	for _, ni := range interfaces.Items {
		ifCount[ni.Spec.Network.Name]++
	}

	views := make([]networkView, len(networks.Items))
	for i, n := range networks.Items {
		v := networkView{
			Name:       n.Name,
			Locations:  ctxCount[n.Name],
			Interfaces: ifCount[n.Name],
			Ready:      util.ReadyStatus(n.Status.Conditions),
			MTU:        n.Spec.MTU,
			IPAM:       string(n.Spec.IPAM.Mode),
			Reason:     util.ReadyReason(n.Status.Conditions),
			Age:        util.RelativeAge(metav1.Time{Time: n.CreationTimestamp.Time}),
			Network:    &networks.Items[i],
		}
		if n.Status.IPAM != nil {
			v.IPv6Prefix = n.Status.IPAM.IPv6Prefix
		}
		views[i] = v
	}

	switch opts.format {
	case util.OutputJSON:
		return util.PrintJSON(out, views)
	case util.OutputYAML:
		return util.PrintYAML(out, views)
	}

	tw := util.NewTabWriter(out)
	defer func() { _ = tw.Flush() }()

	if !opts.noHeaders {
		if opts.format == util.OutputWide {
			_, _ = fmt.Fprintln(tw, "NAME\tIPV6 PREFIX\tLOCATIONS\tINTERFACES\tREADY\tAGE\tMTU\tIPAM\tREASON")
		} else {
			_, _ = fmt.Fprintln(tw, "NAME\tIPV6 PREFIX\tLOCATIONS\tINTERFACES\tREADY\tAGE")
		}
	}

	for _, v := range views {
		if opts.format == util.OutputWide {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%d\t%s\t%s\n",
				v.Name, v.IPv6Prefix, v.Locations, v.Interfaces,
				v.Ready, v.Age, v.MTU, v.IPAM, v.Reason)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n",
				v.Name, v.IPv6Prefix, v.Locations, v.Interfaces,
				v.Ready, v.Age)
		}
	}

	return nil
}
