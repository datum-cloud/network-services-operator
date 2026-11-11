# ALB MCP server

Deployable bundle for `cmd/alb-mcp`, the MCP server that lets the
Patch assistant work with a customer's Application Load Balancers. See
[`docs/agent/README.md`](../../../docs/agent/README.md) for what it publishes
and why.

## Contents

| File | What it is |
| --- | --- |
| `service_account.yaml` | An intentionally unbound `ServiceAccount`. No `Role` or binding is ever attached to it — see the comments in that file for why. |
| `deployment.yaml` | Runs the `alb-mcp` binary from the operator's own image (`command: [/alb-mcp]`), exposing `/mcp`, `/llms-full.txt`, `/runbooks/*` and `/healthz` over HTTPS on port 8080. |
| `service.yaml` | A `ClusterIP` Service in front of the Deployment. Cluster-internal only; no Gateway or HTTPRoute fronts it. |

## Deployment

This bundle carries no control-plane configuration. The server refuses to start
against the cluster's own API server (see `CheckControlPlaneEndpoint` in
`internal/mcpserver`), so the deployer must patch in a kubeconfig volume naming
the Datum control-plane address and CA, and set `KUBECONFIG` to it.

It is **not** applied by the operator's own deployment overlays. It is deployed
from the infra repo, alongside the assistant platform's capability document,
which names this Service's `/mcp`, `/llms-full.txt` and `/runbooks/` URLs.

## Transport

The server answers HTTPS only. The assistant refuses to send a caller's bearer
token to a plain `http://` endpoint, so the Deployment mounts a certificate from
the `datum-control-plane` ClusterIssuer through the cert-manager CSI driver and
passes it as `--tls-cert-file` and `--tls-key-file`. The binary re-reads the
files when the driver renews them, so a rotation needs no restart. Run without
both flags, as in local development, it serves plain HTTP.

The certificate names the Service as Datum deploys it:
`network-services-operator-alb-mcp` in `datum-system`. Kustomize rewrites the
Service's own name and namespace but not the volume attributes that name it, so
a deployment using a different `namePrefix` or namespace has to patch
`csi.cert-manager.io/common-name` and `csi.cert-manager.io/dns-names` to match,
along with the URLs in `../assistant-capability/service-agent-configuration.yaml`.

Two more things there have to move with it, and both fail silently if they do
not:

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
