# Network MCP server

Deployable bundle for `cmd/network-mcp`, the read-only MCP server that lets the
Patch assistant work with a customer's networks and network services. See
[`docs/network-agent/README.md`](../../../docs/network-agent/README.md) for what
it publishes and why.

## Contents

| File | What it is |
| --- | --- |
| `service_account.yaml` | An intentionally unbound `ServiceAccount`. No `Role` or binding is ever attached to it. |
| `deployment.yaml` | Runs the `network-mcp` binary from the operator's own image (`command: [/network-mcp]`), exposing `/mcp`, `/llms-full.txt`, `/runbooks/*` and `/healthz` over HTTPS on port 8080. |
| `service.yaml` | A `ClusterIP` Service in front of the Deployment. Cluster-internal only. |

## Deployment

Deployed the same way as `../alb-mcp`, and with the same three requirements:

- Patch in a kubeconfig volume naming the Datum control-plane address and CA,
  and set `KUBECONFIG` to it. The server refuses to start against the cluster's
  own API server (`CheckControlPlaneEndpoint` in `internal/mcpserver`).
- Add this Service's host, without a port, to the assistant's bearer-token
  forwarding allow-list, or every tool call arrives unauthenticated.
- Permit the assistant's namespace, and nothing else, to reach this pod.

It serves HTTPS only, for the reason and in the way `../alb-mcp` does: a
certificate from the `datum-control-plane` ClusterIssuer, mounted through the
cert-manager CSI driver and re-read on renewal.

It is not applied by the operator's own overlays. It is deployed from the infra
repo, alongside the capability registration in
`../assistant-capability/network-service-agent-configuration.yaml`, whose URLs
assume the Service is named `network-services-operator-network-mcp` in
`datum-system`. The certificate in `deployment.yaml` assumes the same name, so
a different `namePrefix` or namespace has to patch both.
