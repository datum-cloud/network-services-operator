// SPDX-License-Identifier: AGPL-3.0-only

// Package albagent is the importable surface of networking's Application Load
// Balancer agent tools, for an MCP server outside this module that wants to
// offer them — datum-mcp, which runs on a customer's own machine and so cannot
// reach the in-cluster alb-mcp.
//
// Everything here is a thin re-export of internal/agent. The diagnosis walk,
// the reason catalog and the product decoding stay internal and singular, so a
// second server mounting these tools describes a load balancer exactly the way
// the assistant and the alb plugin do. Do not add logic here; add it to
// internal/agent and re-export it.
//
// The knowledge and skills that go with these tools are already importable from
// go.datum.net/network-services-operator/docs/agent.
package albagent

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/agent"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// The tool names RegisterTools adds. Each carries the alb_ prefix so they can
// sit beside another server's tools without colliding.
const (
	ToolList           = agent.ToolList
	ToolGet            = agent.ToolGet
	ToolDiagnose       = agent.ToolDiagnose
	ToolReasonExplain  = agent.ToolReasonExplain
	ToolTrafficSummary = agent.ToolTrafficSummary
)

// Namespace is the namespace every load balancer object lives in within a
// project control plane.
const Namespace = util.ResourceNamespace

type (
	// Reader fetches the objects a load balancer is assembled from. It has no
	// write method, by design; see internal/agent.Reader.
	Reader = agent.Reader
	// LogReader reads a load balancer's access logs.
	LogReader = agent.LogReader
	// ToolDeps is what one request's tool calls read through.
	ToolDeps = agent.ToolDeps
	// DepsFor resolves ToolDeps per call, so the caller decides how identity
	// and project are established.
	DepsFor = agent.DepsFor
)

// RegisterTools adds every read-only load balancer tool to s.
func RegisterTools(s *mcp.Server, deps DepsFor) {
	agent.RegisterTools(s, deps)
}

// NewClientReader returns a Reader backed by c, which must be built with a
// scheme from NewScheme. Every read runs with whatever credentials c carries.
func NewClientReader(c client.Client) Reader {
	return agent.NewClientReader(c)
}

// NewClientLogReader returns a LogReader that reads the project logs API as the
// bearer of cfg. cfg must address the same project control plane the Reader
// does.
func NewClientLogReader(cfg *rest.Config) LogReader {
	return agent.NewClientLogReader(cfg)
}

// NewScheme returns a scheme registering every type a Reader decodes.
func NewScheme() (*runtime.Scheme, error) {
	return util.NewScheme()
}
