// SPDX-License-Identifier: AGPL-3.0-only

// Command alb-mcp serves networking's Application Load Balancer tools
// over MCP, alongside the knowledge and skills an assistant reads before calling
// them:
//
//	POST /mcp                   Streamable HTTP MCP, stateless
//	GET  /llms-full.txt         Knowledge: what a load balancer is made of
//	GET  /runbooks/<name>.md    Skills: the procedures
//	GET  /healthz               liveness
//
// One process, because networking's capability document names all of those
// URLs. Only /mcp takes a credential; see internal/mcpserver for why the
// documents do not.
//
// The server holds no credential of its own for a customer's project: it reads
// and writes through a client built from the caller's own bearer token. So a
// tool call can never see or change more than the person who asked, the
// platform's own access control stays the single enforcement point, and there
// is no standing authority here to escalate with.
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

	agentdocs "go.datum.net/network-services-operator/docs/agent"
	"go.datum.net/network-services-operator/internal/agent"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/mcpserver"
)

const (
	binaryName    = "alb-mcp"
	serverName    = "datum-alb-mcp"
	serverVersion = "0.1.0"
)

var version string

var setupLog = ctrl.Log.WithName("setup")

func main() {
	buildinfo.SetVersion(version)

	var addr string

	flag.StringVar(&addr, "addr", mcpserver.EnvOr("ALB_MCP_ADDR", ":8080"),
		"address to serve MCP on")

	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(logging.New(&opts))

	// GetConfig resolves the --kubeconfig flag controller-runtime registers,
	// then KUBECONFIG, then in-cluster config, then ~/.kube/config. Only the
	// endpoint and CA are used; see mcpserver.ClientConfig.
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
		func(s *mcp.Server, r *http.Request) { agent.RegisterTools(s, depsFromRequest(r, baseConfig)) })
	if err != nil {
		setupLog.Error(err, "server failed")
		os.Exit(1)
	}
}

func newKnowledgeHandler() (*mcpserver.KnowledgeHandler, error) {
	return mcpserver.NewKnowledgeHandler(agentdocs.FS, agentdocs.KnowledgeFile, agentdocs.SkillsDir)
}

// depsFromRequest returns a DepsFor that resolves the caller's identity and
// project from r.
func depsFromRequest(r *http.Request, baseConfig *rest.Config) agent.DepsFor {
	callerConfig := mcpserver.CallerConfig(r, baseConfig, serverName)

	return func(context.Context) (agent.ToolDeps, error) {
		cfg, err := callerConfig()
		if err != nil {
			return agent.ToolDeps{}, err
		}
		c, err := mcpserver.NewClient(cfg)
		if err != nil {
			return agent.ToolDeps{}, err
		}
		// Access logs read through the same config, so they carry the same
		// caller and the same project, and no second credential exists here.
		return agent.ToolDeps{
			Reader:    agent.NewClientReader(c),
			Writer:    agent.NewClientWriter(c, serverName),
			Namespace: util.ResourceNamespace,
			Logs:      agent.NewClientLogReader(cfg),
		}, nil
	}
}
