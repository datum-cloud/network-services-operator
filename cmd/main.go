package main

import (
	"os"

	"github.com/spf13/cobra"

	managercmd "go.datum.net/network-services-operator/internal/cmd/manager"
	extservercmd "go.datum.net/network-services-operator/internal/extensionserver/cmd"
)

// version variables are injected at link time via ldflags:
//
//	-X main.version=...
//	-X main.gitCommit=...
//	-X main.gitTreeState=...
//	-X main.buildDate=...
var (
	version      = "dev"
	gitCommit    = "unknown"
	gitTreeState = "unknown"
	buildDate    = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:   "network-services",
		Short: "Datum network services operator",
	}
	root.AddCommand(managercmd.NewCommand(managercmd.BuildInfo{
		Version:      version,
		GitCommit:    gitCommit,
		GitTreeState: gitTreeState,
		BuildDate:    buildDate,
	}))
	root.AddCommand(extservercmd.NewCommand())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
