// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"github.com/spf13/cobra"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
)

const short = "Manage Application Load Balancers on Datum Cloud"

const pluginAPIVersion = 1

var newClient = plugincli.NewClient

var ensureEntitlement = plugincli.EnsureNetworkingEntitlement

func Command() *cobra.Command {
	root := plugincli.NewRoot("alb", short, func(cmd *cobra.Command) error {
		return ensureEntitlement(cmd.Context(), plugincli.ProjectFromCmd(cmd), cmd.InOrStdin(), cmd.ErrOrStderr())
	})

	root.AddCommand(
		createCommand(),
		listCommand(),
		describeCommand(),
		updateCommand(),
		deleteCommand(),
		hostnameCommand(),
		routeCommand(),
		logsCommand(),
		wafCommand(),
		headerCommand(),
		authCommand(),
		versionCommand(),
	)

	plugincli.EnforceUsageExit(root)
	return root
}
