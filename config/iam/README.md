# Customer status access

With subresource authorization enabled, customers can access the `/status`
endpoint only for Connectors:

| Role | Connector status access |
| --- | --- |
| Connector Viewer, Network Viewer | Read |
| Connector Admin, Network Admin | Read, update, patch |
| Other networking roles | None |

Network roles inherit these permissions from the corresponding Connector role.
Customers can still read observed status through their existing resource get,
list, and watch permissions. Status endpoints for other resources are reserved
for controllers and separately authorized staff; declaring a subresource does
not grant access to it. Existing Kubernetes controller RBAC is unchanged.

The ProtectedResources declare `get`, `update`, and `patch` for the status
subresources served by our CRDs. HTTPRouteFilters, EndpointSlices, and Leases do
not serve a status subresource and have no status declaration.

## Rollout

This configuration depends on [Milo #824](https://github.com/milo-os/milo/pull/824)
and [openfga-provider #139](https://github.com/milo-os/openfga-provider/pull/139).
Install the updated ProtectedResource schema, apply the subresource declarations,
then enable subresource authorization on the provider manager and wait for its
model to converge. Apply the Connector Role changes and wait for the Roles and
PolicyBindings to become ready before enabling enforcement on the webhook.

The provider feature remains off by default; these manifests do not enable it.
Do not apply the new Connector Role permissions while the manager feature is
off: the manager does not recognize subresource permissions in that mode.
