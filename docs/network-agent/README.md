# Network agent capabilities

What networking publishes to the Patch assistant about **networks and network
services**: the knowledge it reads, the skills it may follow, and (via
`internal/networkagent`) the diagnosis it can run. Load balancers have their own
agent in `../agent`. Connectors are not covered yet.

## Contents

| Path | Role |
|---|---|
| `llms-full.txt` | Knowledge. The network model, the rules that are always true, and how to read status. |
| `skills/*.md` | Skills. Procedures, fetched on demand. |
| `embed.go` | Embeds both into `cmd/network-mcp`. |
| `../../internal/networkagent` | The reason catalog, the diagnosis walks and the tools. |
| `../../internal/mcpserver` | Caller identity, project routing and document serving, shared with `alb-mcp`. |
| `../../config/components/network-mcp` | The deployment. |
| `../../config/components/assistant-capability/network-service-agent*.yaml` | Registration with the service catalog. |

## Tools

All read-only, all prefixed `network_`.

| Tool | Answers |
|---|---|
| `network_list` | Every network, worst first |
| `network_get` | One network: families, MTU, prefix, locations, workloads, services, kept interfaces |
| `network_diagnose` | Why a network, or a workload's use of it, is not working |
| `network_service_list` | Every network service, worst first |
| `network_service_diagnose` | Why a service sends traffic nowhere |
| `network_reason_explain` | What a reason means on a given kind, and who acts |
| `network_render` | A `Network` or `NetworkService` manifest admission will accept. Writes nothing |

Creating or changing anything goes through the assistant's own
`resources_plan` and `resources_apply`, which hold the plan token and the one
confirmation step every service shares. That is what "tell Patch to create a
network" runs on: the `network-create` skill, `network_render`, then plan,
confirm, apply.

## Why the catalog is keyed on kind

The same reason string means different things on different kinds.
`RangeOccupied` on a network means deletion is blocked by workloads the
customer has to remove; on the network in one location it is instances still
shutting down. `ProjectNamespaceNotFound` is reported by a network, a location
and an instance. A key of `(conditionType, reason)` — the ALB catalog's — is not
enough here, because every kind calls its summary condition `Ready`.

## Keeping this honest

Tests fail when:

- a file appears in `api/v1alpha` that is in neither scope list;
- a reason constant in an in-scope API file, or a reason literal any network
  controller writes, has no catalog entry;
- a non-informational entry has no scope or remediation, or a transient one no
  window;
- customer-facing copy or the published documents use a word that only exists
  inside the implementation;
- a skill is named in code but not published, or the reverse;
- the documents name a `network_` tool that is not registered;
- the registration's tool list, skills or URLs drift from what the server
  serves.

When a network controller gains a reason, add its entry in the same change and
pair it with the kind and condition the controller sets it on.
