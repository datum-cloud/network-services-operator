// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.datum.net/datumctl/plugin"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/network"
)

var version = "dev"

func main() {
	plugin.ServeManifest(plugin.Manifest{
		Name:          "network",
		Version:       version,
		Description:   "Manage networks and network services on Datum Cloud",
		APIVersion:    1,
		MinAPIVersion: 1,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	network.Version = version

	root := network.Command()
	if err := root.ExecuteContext(ctx); err != nil {
		verbose, _ := root.PersistentFlags().GetBool("verbose")
		os.Exit(util.RenderExit(root.ErrOrStderr(), err, verbose))
	}
}
