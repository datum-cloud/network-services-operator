// SPDX-License-Identifier: AGPL-3.0-only

// Package networkagent is the importable surface of networking's network and
// network service agent tools, for an MCP server outside this module that wants
// to offer them — datum-mcp, which runs on a customer's own machine and so
// cannot reach the in-cluster network-mcp.
//
// Everything here is a thin re-export of internal/networkagent. The diagnosis
// walk, the reason catalog and the rendering stay internal and singular, so a
// second server mounting these tools describes a network exactly the way the
// assistant and the network plugin do. Do not add logic here; add it to
// internal/networkagent and re-export it.
//
// The knowledge and skills that go with these tools are already importable from
// go.datum.net/network-services-operator/docs/network-agent.
package networkagent

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

// The tool names RegisterTools adds. Each carries the network_ prefix so they
// can sit beside another server's tools without colliding.
const (
	ToolList            = networkagent.ToolList
	ToolGet             = networkagent.ToolGet
	ToolDiagnose        = networkagent.ToolDiagnose
	ToolServiceList     = networkagent.ToolServiceList
	ToolServiceDiagnose = networkagent.ToolServiceDiagnose
	ToolReasonExplain   = networkagent.ToolReasonExplain
	ToolRender          = networkagent.ToolRender
)

// Namespace is the namespace every network object lives in within a project
// control plane.
const Namespace = util.ResourceNamespace

type (
	// Reader fetches the objects a network is assembled from. It has no write
	// method, by design; see internal/networkagent.Reader.
	Reader = networkagent.Reader
	// ToolDeps is what one request's tool calls read through.
	ToolDeps = networkagent.ToolDeps
	// DepsFor resolves ToolDeps per call, so the caller decides how identity
	// and project are established.
	DepsFor = networkagent.DepsFor
)

// RegisterTools adds every read-only network tool to s.
func RegisterTools(s *mcp.Server, deps DepsFor) {
	networkagent.RegisterTools(s, deps)
}

// NewClientReader returns a Reader backed by c, which must be built with a
// scheme from NewScheme. Every read runs with whatever credentials c carries.
func NewClientReader(c client.Client) Reader {
	return networkagent.NewClientReader(c)
}

// NewScheme returns a scheme registering every type a Reader decodes.
func NewScheme() (*runtime.Scheme, error) {
	return util.NewScheme()
}
