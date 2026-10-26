// SPDX-License-Identifier: AGPL-3.0-only

// Command network-mcp serves networking's read-only network and network service
// tools over MCP, alongside the knowledge and skills an assistant reads before
// calling them:
//
//	POST /mcp                   Streamable HTTP MCP, stateless
//	GET  /llms-full.txt         Knowledge: what a network is made of
//	GET  /runbooks/<name>.md    Skills: the procedures
//	GET  /healthz               liveness
//
// Every read runs as the caller; see internal/mcpserver.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.miloapis.com/instrumentation/buildinfo"
	"go.miloapis.com/instrumentation/logging"

	networkagentdocs "go.datum.net/network-services-operator/docs/network-agent"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/mcpserver"
	"go.datum.net/network-services-operator/internal/networkagent"
)

const (
	binaryName    = "network-mcp"
	serverName    = "datum-network-mcp"
	serverVersion = "0.1.0"
)

var version string

var setupLog = ctrl.Log.WithName("setup")

func main() {
	buildinfo.SetVersion(version)

	var addr string

	flag.StringVar(&addr, "addr", mcpserver.EnvOr("NETWORK_MCP_ADDR", ":8080"),
		"address to serve MCP on")

	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(logging.New(&opts))

	baseConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "unable to load control plane configuration")
		os.Exit(1)
	}

	if err := mcpserver.CheckControlPlaneEndpoint(baseConfig, binaryName); err != nil {
		setupLog.Error(err, "refusing to start")
		os.Exit(1)
	}

	docs, err := newKnowledgeHandler()
	if err != nil {
		setupLog.Error(err, "unable to load embedded documents")
		os.Exit(1)
	}

	setupLog.Info("listening", "addr", addr, "mcp", "/mcp", "docs", docs.Paths())
	err = mcpserver.Serve(addr,
		&mcp.Implementation{Name: serverName, Version: serverVersion}, docs,
		func(s *mcp.Server, r *http.Request) { networkagent.RegisterTools(s, depsFromRequest(r, baseConfig)) })
	if err != nil {
		setupLog.Error(err, "server failed")
		os.Exit(1)
	}
}

func newKnowledgeHandler() (*mcpserver.KnowledgeHandler, error) {
	return mcpserver.NewKnowledgeHandler(networkagentdocs.FS, networkagentdocs.KnowledgeFile, networkagentdocs.SkillsDir)
}

func depsFromRequest(r *http.Request, baseConfig *rest.Config) networkagent.DepsFor {
	callerConfig := mcpserver.CallerConfig(r, baseConfig, serverName)

	return func(context.Context) (networkagent.ToolDeps, error) {
		cfg, err := callerConfig()
		if err != nil {
			return networkagent.ToolDeps{}, err
		}
		c, err := mcpserver.NewClient(cfg)
		if err != nil {
			return networkagent.ToolDeps{}, err
		}
		return networkagent.ToolDeps{
			Reader:    networkagent.NewClientReader(c),
			Namespace: util.ResourceNamespace,
		}, nil
	}
}
