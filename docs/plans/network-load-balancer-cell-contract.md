# Network load balancer cell contract

## Identity available today

A project's `NetworkInterface` is a copy of a cell interface. The copy has the
cell location, source namespace, and source name. This change also publishes the
source interface UID. These four values identify one source object, including
across deletion and recreation under the same name. The copy deliberately omits
`status.attachmentRef` and `status.vpc`; those values name cell resources and
are not authoritative in a project control plane.

The route resolver counts only projected endpoints carrying this source UID.
Older copies without the label remain unprogrammable until the cell publishes
them again. The UID on a copy is a hint for the cell to check against its live
object, never authorization by itself.

Inside a cell, Cloud creates a `VPCAttachment` with
`spec.interfaceRef.name` pointing to the source interface in the same
namespace. The source interface reports the attachment in
`status.attachmentRef` and the fabric VPC identifier in `status.vpc`. Galactic
reads attachment addresses, `status.vpc`, and `status.node`. Matching an address
alone is unsafe because different VPCs can use the same address.

## Published intent

The project reconciler writes a `NetworkLoadBalancerCellIntent` into the mapped
hub namespace. The federation policy carries its spec to gateway-enabled
cells. Only the cell whose configured location equals `spec.location` may
process it. The route's UID and generation and the load balancer's UID and
generation identify the source objects; cross-cluster owner references cannot
provide that guarantee.

An intent is published only for the oldest route on an existing listener with
resolved backend references, a ready network, and an allocated IPv6 address
whose `Allocated=True` condition observed the current load balancer
generation. Every published address names one location; an empty location or
an address shared by two allocation entries is excluded.
The source interface namespace and UID come from the projected interface;
the cell must verify them against live state. The published shape is:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancerCellIntent
spec:
  source:
    projectCluster: <project cluster identity>
    namespace: <project namespace>
    routeName: <route name>
    routeUID: <route UID>
    routeGeneration: <route generation>
    parentName: <load balancer name>
    parentUID: <load balancer UID>
    parentGeneration: <load balancer generation>
    listenerName: <listener name>
    addressRequest: <address request name>
  location: <cell location>
  networkName: <project network name>
  vip: <allocated IPv6 address>
  protocol: TCP # or UDP
  listenerPort: 443
  state: Validate
  backends:
    - sourceNamespace: <cell namespace>
      sourceName: <cell NetworkInterface name>
      sourceUID: <cell NetworkInterface UID>
      address: <interface IPv6 address>
      port: 8443
      probe:
        protocol: TCP
        port: 8443
```

`Validate` requests identity and health validation only. It never authorizes
`NetworkRule` creation, `Programmed=True`, or VIP announcement. Loss of
eligibility or removal of a route changes the old intent to `Withdrawn` with
an empty backend list. The route holds a finalizer until that withdrawal is
written. Withdrawn objects remain on the hub so a later cell acknowledgment
can prove the old VIP is no longer announced before they are deleted. A new
VIP, listener port, parent UID, or route UID gets a distinct intent name, so
the previous identity is explicitly withdrawn.

Only a platform controller may write this intent. The cell must treat the
projected source tuple as a lookup hint, not as proof. For every backend it
must read the cell `NetworkInterface` and require the UID, network, bound phase,
holder availability, location, and address to match. It must then read the
same-namespace `VPCAttachment` named by the interface's `status.attachmentRef`
and require its `spec.interfaceRef.name`, controller owner UID, `status.vpc`,
IPv6 address, and programmed generation to match the source. The attachment
must have a node and the source must identify the expected VPC. A missing or
changed field removes the backend from the effective set. The cell must also
verify that the source namespace belongs to the project recorded in the
intent, and that the attachment's `spec.vpc` resolves to a live VPC whose
fabric ID matches both statuses. A source UID change invalidates old intent
even when the name and IP are reused.

The cell should report resolved attachment identities and probe results per
backend, with the intent generation it observed. Cell status is not propagated
back by Karmada, so a dedicated cell-to-hub acknowledgment is still needed.
A project controller may set
route `Programmed=True` only after the cell has acknowledged that generation
and every announcing gateway node confirms the same healthy backend set. VIP
announcement follows that acknowledgment; an empty healthy set withdraws the
VIP but retains the allocated address.

## Galactic API dependencies

The deployed `NetworkRule` requires a nonempty `backendSelector`. Cloud's
`VPCAttachment` writer does not provide a trusted label that selects all and
only this route's attachments. `backendAddresses` narrows a selector, so it
cannot be used alone in the deployed API. The Network API follow-up permits an omitted selector
**only when `backendAddresses` is explicitly set**. Galactic should then
select attachments by authoritative `status.vpc == spec.vpcRef` and intersect
their IPv6 addresses with `backendAddresses`. An explicit empty list means no
backends and withdrawal. The writer must verify authorization for `vpcRef`;
the existing Galactic admission contract does not itself prove it.

Current DSR also cannot safely serve two VIPs sharing a backend
address/port/protocol, and does not implement weighted selection or active
application probes. Those capabilities and an all-gateway programming
acknowledgment are required before this intent can drive the full native API.
Until then, route and load balancer `Programmed` and `Announced` remain false.

## Cell programming (opt-in)

A cell with `networkLoadBalancer.enabled` in its cell controller config serves
`Validate` intents for its own location before the acknowledgment above exists.
For each backend it applies the interface and attachment checks in
[Published intent](#published-intent). It labels each attachment that passes
with `nlb.networking.datumapis.com/<intent name>: backend`, and writes one
Galactic `NetworkService` with the intent's name in the intent's namespace,
owned by the intent so it is deleted with it. The gateways must watch
services in every namespace to serve it. That service selects those labels, uses the
attachments' fabric VPC as `vpcRef`, and serves the VIP on the listener port. It
forwards to the backend port. An intent is not served if its backends span
VPCs or ports, if no backend passes, or if it is withdrawn or deleted. In those
cases the service is deleted and the labels are removed.

Nothing is reported back to the project yet, so route and load balancer
`Programmed` stay unknown while the VIP serves. Enable this only where that is
acceptable, such as staging.
