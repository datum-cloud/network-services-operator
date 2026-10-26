// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"

	"github.com/spf13/cobra"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

func complete(cmd *cobra.Command, args []string, names func(context.Context, networkagent.Reader) []string) (
	[]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), plugincli.CompletionTimeout)
	defer cancel()
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return names(ctx, networkagent.NewClientReader(c)), cobra.ShellCompDirectiveNoFileComp
}

func completeNetworkNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	return complete(cmd, args, func(ctx context.Context, r networkagent.Reader) []string {
		networks, err := r.ListNetworks(ctx, util.ResourceNamespace)
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(networks))
		for _, n := range networks {
			out = append(out, n.Name)
		}
		return out
	})
}
