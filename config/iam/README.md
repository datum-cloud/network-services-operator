# Customer status access

With subresource authorization enabled, anyone who can read a resource can read
its `/status` endpoint. Customers can update and patch the `/status` endpoint
only for Connectors:

| Role | Status access |
| --- | --- |
| Any role that can read a resource | Read that resource's status |
| Connector Admin, Network Admin | Also update and patch Connector status |

Network roles inherit these permissions from the corresponding Connector,
Gateway, and Domain roles. Status read grants nothing new: the same data is
already returned by a resource get, list, or watch. Status writes for other
resources are reserved for controllers and separately authorized staff;
declaring a subresource does not grant access to it. Existing Kubernetes
controller RBAC is unchanged.

The ProtectedResources declare `get`, `update`, and `patch` for the status
subresources served by our CRDs. HTTPRouteFilters, EndpointSlices, and Leases do
not serve a status subresource and have no status declaration.

## Rollout

This configuration depends on [Milo #824](https://github.com/milo-os/milo/pull/824)
and [openfga-provider #139](https://github.com/milo-os/openfga-provider/pull/139).
Install the updated ProtectedResource schema, apply the subresource declarations,
then enable subresource authorization on the provider manager and wait for its
model to converge. Apply the Role changes and wait for the Roles and
PolicyBindings to become ready before enabling enforcement on the webhook.

The provider feature remains off by default; these manifests do not enable it.
Do not apply the status permissions while the manager feature is off: the
manager does not recognize subresource permissions in that mode.
