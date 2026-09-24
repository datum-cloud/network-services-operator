// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"

	"go.datum.net/network-services-operator/internal/cmd/network/util"
)

type vpcVerb struct {
	run func(cmd *cobra.Command, vpc string) error
}

var vpcVerbs = map[string]vpcVerb{
	"list": {run: runVPCList},
}

func Command() *cobra.Command {
	root := plugin.NewRootCmd("network", "Manage VPCs and network primitives on Datum Cloud")
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.PersistentFlags().Bool("no-headers", false, "Skip printing table headers")
	root.PersistentFlags().Bool("verbose", false, "Show extended error details")

	_ = root.RegisterFlagCompletionFunc("output",
		util.CompleteOutputFormats("json", "yaml", "wide"))

	root.AddCommand(listCmd())

	root.Args = cobra.ArbitraryArgs
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}

		vpc := args[0]
		if len(args) == 1 {
			return fmt.Errorf("specify a verb: datumctl network %s list", vpc)
		}

		verb := args[1]
		v, ok := vpcVerbs[verb]
		if !ok {
			names := vpcVerbNames()
			return fmt.Errorf("unknown verb %q for VPC %q; valid verbs: %s", verb, vpc, strings.Join(names, ", "))
		}

		if len(args) > 2 {
			return fmt.Errorf("%q accepts no extra arguments after the verb, got %v", cmd.CommandPath(), args[2:])
		}

		return v.run(cmd, vpc)
	}

	root.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return util.CompleteNetworkNames(cmd, args, toComplete)
		case 1:
			return vpcVerbNames(), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}

	root.Long = `Manage VPCs and network primitives on Datum Cloud.

  datumctl network list                List VPCs in the project
  datumctl network <vpc> list          List what is attached to a VPC

Known limitation: a VPC named "list", "help", or "completion" is shadowed by
the subcommand of the same name.`

	return root
}

func vpcVerbNames() []string {
	names := make([]string, 0, len(vpcVerbs))
	for k := range vpcVerbs {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
