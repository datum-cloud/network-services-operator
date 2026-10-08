// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.datum.net/datumctl/plugin"

	"go.datum.net/network-services-operator/internal/cmd/alb"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

var version = "dev"

func main() {
	plugin.ServeManifest(plugin.Manifest{
		Name:          "alb",
		Version:       version,
		Description:   "Manage Application Load Balancers on Datum Cloud",
		APIVersion:    1,
		MinAPIVersion: 1,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	alb.Version = version

	root := alb.Command()
	if err := root.ExecuteContext(ctx); err != nil {
		verbose, _ := root.PersistentFlags().GetBool("verbose")
		os.Exit(util.RenderExit(root.ErrOrStderr(), err, verbose))
	}
}
