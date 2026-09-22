# Cell controller bundle

The published `cell` path contains the cell controller Deployment, configuration,
service account, RBAC and metrics Service. It uses an existing namespace and
CRDs installed by their own owners. It renders neither a Namespace nor a CRD.

## Installation order

Before reconciling `cell`, the deployment must provide:

1. The deployment namespace, owned by the platform. The default is
   `network-services-operator-system`; an overlay or Flux `targetNamespace` can
   select another namespace. Keep service ownership labels on that Namespace.
2. NSO's `crd/downstream` bundle on the cell. It supplies the NetworkInterfaceClaim,
   NetworkInterface, NetworkContext and Subnet APIs the cell controllers use.
3. The locations service's Location and ServingLocation CRDs. NSO does not
   install another service's APIs.
4. The IPAM and federation credentials and configuration described in the
   [cell controller design](../../docs/enhancements/cell-controller-manager.md).

Give separately reconciled prerequisites explicit ordering. In Datum infra,
`nso-cell-controllers` depends on `datum-downstream-gateway/nso-downstream-crds`
and `locations-system/locations-cell-crds`. Its parent owns the namespace and
credentials. The same ownership model applies to staging and production.

`config/crd` is the complete NSO API set for project control planes. Do not apply
it alongside `crd/downstream` through a second reconciler on the same cell.
The composed development and e2e overlays install their prerequisites in one
render and use the cell controller component directly.

## Upgrading existing installations

Older `cell` bundles also rendered the namespace and the complete NSO CRD set.
Before upgrading one of those installations, ensure its reconciler will not
prune resources removed from the bundle. Deleting a CRD deletes its instances;
deleting the namespace deletes the workloads in it. Datum infra already uses
`prune: false` for `nso-cell-controllers`.

Verify the prerequisite owners and dependency ordering first, then deploy the
new bundle. Once reconciled, its inventory should contain only controller
resources; the namespace and downstream CRDs should each have one owner. Keep
pruning disabled until that inventory transition is verified, including on any
version that could be rolled back to. This change does not delete old,
unused control-plane CRDs from cells.
