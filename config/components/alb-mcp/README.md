# ALB MCP server

Deployable bundle for `cmd/alb-mcp`, the read-only MCP server that lets the
Patch assistant work with a customer's Application Load Balancers. See
[`docs/agent/README.md`](../../../docs/agent/README.md) for what it publishes
and why.

## Contents

| File | What it is |
| --- | --- |
| `service_account.yaml` | An intentionally unbound `ServiceAccount`. No `Role` or binding is ever attached to it — see the comments in that file for why. |
| `deployment.yaml` | Runs the `alb-mcp` binary from the operator's own image (`command: [/alb-mcp]`), exposing `/mcp`, `/llms-full.txt`, `/runbooks/*` and `/healthz` on port 8080. |
| `service.yaml` | A `ClusterIP` Service in front of the Deployment. Cluster-internal only; no Gateway or HTTPRoute fronts it. |

## Deployment

This bundle carries no control-plane configuration. The server refuses to start
against the cluster's own API server (see `checkControlPlaneEndpoint` in
`cmd/alb-mcp/main.go`), so the deployer must patch in a kubeconfig volume naming
the Datum control-plane address and CA, and set `KUBECONFIG` to it.

It is **not** applied by the operator's own deployment overlays. It is deployed
from the infra repo, alongside the assistant platform's capability document,
which names this Service's `/mcp`, `/llms-full.txt` and `/runbooks/` URLs.

Two things there have to move with it, and both fail silently if they do not:

- The assistant forwards the calling user's bearer token only to hosts on an
  allow-list. This Service's host has to be on it, without a port, or every tool
  call arrives unauthenticated.
- Network policy has to permit the assistant's namespace to reach this pod and
  nothing else to. A policy drop hangs rather than refuses, so the symptom is a
  timeout, not an error.

## Why the server holds no credential of its own

Every read runs as the caller, not as this Deployment's identity. The server
strips every credential off its base kubeconfig and rebuilds a client per
request from the bearer token on that request, pointed at the project named in
the `X-Datum-Project` header — never a tool argument, since a tool argument is
chosen by the model. This keeps the platform's own access control as the single
enforcement point and leaves no standing authority for a compromised or
prompt-injected tool call to reach beyond what the asker could already see.
