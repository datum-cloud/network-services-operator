// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func CompleteNetworkNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	project := ProjectFromCmd(cmd)
	c, err := NewClient(project)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var list networkingv1alpha.NetworkList
	if err := c.List(context.Background(), &list, client.InNamespace(ResourceNamespace)); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, len(list.Items))
	for i, n := range list.Items {
		names[i] = n.Name
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func CompleteOutputFormats(allowed ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return allowed, cobra.ShellCompDirectiveNoFileComp
	}
}
