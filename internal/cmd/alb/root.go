// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

const short = "Manage Application Load Balancers on Datum Cloud"

const pluginAPIVersion = 1

const (
	cmdVersion    = "version"
	cmdCompletion = "completion"
	cmdHelp       = "help"
)

var entitlementSkip = map[string]bool{
	cmdVersion:                      true,
	cmdCompletion:                   true,
	cmdHelp:                         true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

var newClient = util.NewClient

var ensureEntitlement = util.EnsureNetworkingEntitlement

func Command() *cobra.Command {
	root := plugin.NewRootCmd("alb", short)

	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SuggestionsMinimumDistance = 2

	if out := root.PersistentFlags().Lookup("output"); out != nil {
		out.Usage = "Output format. One of: table|wide|json|yaml|name"
	}

	root.PersistentFlags().BoolP("verbose", "v", false, "Show the underlying cause of errors")
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress progress and footer output")
	root.PersistentFlags().String("color", "auto", "Colorize output. One of: auto|always|never")
	root.PersistentFlags().BoolP("yes", "y", false, "Skip confirmation prompts")

	_ = root.RegisterFlagCompletionFunc("output",
		util.CompleteEnum("table", "wide", "json", "yaml", "name"))
	_ = root.RegisterFlagCompletionFunc("color",
		util.CompleteEnum("auto", "always", "never"))

	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return util.UsageErrorf("%s", err.Error()).
			WithFix(fmt.Sprintf("run `%s --help` to see the available flags.", cmd.CommandPath()))
	})

	root.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return unknownSubcommandError(cmd, args[0])
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if skipsEntitlement(cmd) {
			return nil
		}
		return ensureEntitlement(cmd.Context(), util.ProjectFromCmd(cmd), cmd.InOrStdin(), cmd.ErrOrStderr())
	}

	root.AddCommand(
		createCommand(),
		listCommand(),
		describeCommand(),
		updateCommand(),
		deleteCommand(),
		hostnameCommand(),
		routeCommand(),
		wafCommand(),
		headerCommand(),
		authCommand(),
		versionCommand(),
	)

	enforceUsageExit(root)
	return root
}

func skipsEntitlement(cmd *cobra.Command) bool {
	if help, err := cmd.Flags().GetBool("help"); err == nil && help {
		return true
	}
	if cmd.Parent() == nil {
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if entitlementSkip[c.Name()] {
			return true
		}
	}
	return false
}

func unknownSubcommandError(cmd *cobra.Command, name string) *util.CLIError {
	msg := fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(suggestions, "\n\t")
	}
	return util.NewCLIError(util.ExitUsage, msg)
}

func enforceUsageExit(cmd *cobra.Command) {
	cmd.SuggestionsMinimumDistance = 2

	switch {
	case cmd.Args != nil:
		inner := cmd.Args
		cmd.Args = func(c *cobra.Command, args []string) error {
			return asUsageError(c, inner(c, args))
		}
	case cmd.HasSubCommands():
		cmd.Args = func(c *cobra.Command, args []string) error {
			if len(args) == 0 || c.Runnable() {
				return nil
			}
			return unknownSubcommandError(c, args[0])
		}
	}

	for _, sub := range cmd.Commands() {
		enforceUsageExit(sub)
	}
}

func asUsageError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var already *util.CLIError
	if errors.As(err, &already) {
		return err
	}
	ce := util.UsageErrorf("%s", argCountMessage(cmd))
	if fix := argCountFix(cmd); fix != "" {
		ce = ce.WithFix(fix)
	}
	return ce.WithCause(err)
}

func argCountMessage(cmd *cobra.Command) string {
	spec := argSpec(cmd)
	if spec == "" {
		return fmt.Sprintf("%s takes no arguments", commandLine(cmd))
	}
	return fmt.Sprintf("%s takes %s", commandLine(cmd), spec)
}

func argCountFix(cmd *cobra.Command) string {
	if ex := firstExample(cmd); ex != "" {
		return ex
	}
	if spec := argSpec(cmd); spec != "" {
		return commandLine(cmd) + " " + spec
	}
	return ""
}

func argSpec(cmd *cobra.Command) string {
	spec := strings.TrimSpace(strings.TrimPrefix(cmd.Use, cmd.Name()))
	return strings.TrimSpace(strings.TrimSuffix(spec, "[flags]"))
}

func commandLine(cmd *cobra.Command) string {
	return "datumctl " + cmd.CommandPath()
}

func firstExample(cmd *cobra.Command) string {
	for _, line := range strings.Split(cmd.Example, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}
