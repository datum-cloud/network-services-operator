// SPDX-License-Identifier: AGPL-3.0-only

// Package network is the datumctl network plugin: networks, network services
// and the network interfaces instances hold on them.
package network

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

const (
	short            = "Manage networks and network services on Datum Cloud"
	pluginAPIVersion = 1
	fieldManager     = "datumctl-network"
)

var Brand = util.Brand{Command: "network", ExitPrefix: "NETWORK", Product: "networks"}

var newClient = plugincli.NewClient

var ensureEntitlement = plugincli.EnsureNetworkingEntitlement

func Command() *cobra.Command {
	util.SetBrand(Brand)

	root := plugincli.NewRoot("network", short, func(cmd *cobra.Command) error {
		return ensureEntitlement(cmd.Context(), plugincli.ProjectFromCmd(cmd), cmd.InOrStdin(), cmd.ErrOrStderr())
	})
	root.Long = `Manage networks and network services on Datum Cloud.

A network is a private, IPv6-only address space your workloads attach to. It
reaches a location when a workload placed there attaches to it. A network
service gives a workload's instances on a network a stable name and named
ports, so a load balancer can send traffic to them.`

	root.AddCommand(
		listCommand(),
		describeCommand(),
		createCommand(),
		updateCommand(),
		deleteCommand(),
		diagnoseCommand(),
		serviceCommand(),
		interfaceCommand(),
		versionCommand(),
	)

	plugincli.EnforceUsageExit(root)
	return root
}

func clients(cmd *cobra.Command) (client.Client, networkagent.Reader, error) {
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return nil, nil, err
	}
	return c, networkagent.NewClientReader(c), nil
}

func notFound(kind, name, listCmd string, err error) error {
	if !apierrors.IsNotFound(err) {
		return util.ClassifyError(err)
	}
	return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("%s %q not found", kind, name)).
		WithFix("list them with:\n       " + listCmd).
		WithCause(err)
}

func outputFormat(cmd *cobra.Command) (util.OutputFormat, error) {
	return util.ParseOutputFormat(plugincli.OutputFromCmd(cmd))
}

func printStructured(w io.Writer, format util.OutputFormat, v any) (bool, error) {
	switch format {
	case util.OutputJSON:
		return true, util.PrintJSON(w, v)
	case util.OutputYAML:
		return true, util.PrintYAML(w, v)
	}
	return false, nil
}

func confirmed(cmd *cobra.Command, prompt, typed string) error {
	if plugincli.AssumeYes(cmd) {
		return nil
	}
	ok, err := util.ConfirmTyped(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt, typed)
	if err != nil {
		return err
	}
	if !ok {
		return util.NewCLIError(util.ExitAborted, "aborted")
	}
	return nil
}

func nextSteps(cmd *cobra.Command, lines ...string) {
	if plugincli.QuietFromCmd(cmd) || len(lines) == 0 {
		return
	}
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(w, "\nNext steps:")
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, "  "+l)
	}
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, util.RequestTimeout)
}
