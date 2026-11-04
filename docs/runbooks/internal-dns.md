# Internal DNS configuration

`internalDNS.enabled` is a global rollout switch. It defaults to `false`.
When enabled, every Network with an empty DNS mode uses managed DNS under
`datum.internal`. Set a Network's `spec.dns.mode` to `Disabled` to exclude it.
Disabling the process flag stops DNS reconciliation; it does not withdraw applied
DNS records or access. Use the Network setting for an orderly withdrawal.

Before enabling the feature:

- Install the DNS resolver context and access binding CRDs in project control
  planes, and the Galactic service endpoint and route policy CRDs in cells.
- Grant the project integration identity permission to manage resolver contexts
  and access bindings. Grant the cell identity permission to manage service
  endpoints and route policies. These permissions do not belong to consumers or
  product record publishers.
- Allow only trusted network controllers to write NetworkContext specs in
  federation and cell APIs. Their resolver settings configure guest interfaces.
  Consumer roles should manage Networks in the project API instead.
- Apply the `config/components/networkcontext-dns-admission` policy in every
  API where the manager writes project NetworkContexts. It refuses any create
  or update that changes `spec.dns` unless the request comes from a trusted
  identity. The trusted list names the manager's service account as
  `config/default` renders it. Replace it with the identity the manager
  authenticates as to that API, or the manager's own DNS writes are refused.
  The policy trusts that username, so anyone allowed to impersonate it can
  also write `spec.dns`.
- Configure federation access, DNS service destinations, and the Galactic cell
  controllers before enabling the project and cell DNS integrations.

The generated manager role includes integration permissions. A runtime feature
flag does not revoke RBAC grants. Deployments that require separate identities
must bind the project and cell permissions at their respective API boundaries.

With DNS disabled, no internal DNS controller or additional federation manager
starts. Existing project NetworkContexts remain unchanged by NetworkBinding
reconciliation. Certificate challenge and solver controllers use the deployment
API independently of the DNS setting.

Monitor resolver access readiness, route programming conditions, and DNS queries
from a newly created instance. Existing answers alone do not verify that new
consumer attachments can reach the resolver.

## Resolvers

Guests receive one nameserver per configured resolver. With a single
`frontend` and `producerAttachmentSelector`, that is one nameserver, and a
rollout of the resolver behind it interrupts guest DNS. Configure `resolvers`
instead to give guests independent resolvers, each served by its own producer
attachments:

```yaml
internalDNS:
  enabled: true
  resolvers:
  - frontend: fd53::53
    producerAttachmentSelector:
      service.datum.net/internal-dns: "true"
      internal-dns.datum.net/tier: node
  - frontend: fd53::54
    producerAttachmentSelector:
      service.datum.net/internal-dns: "true"
      internal-dns.datum.net/tier: node-b
```

- `resolvers` replaces `frontend` and `producerAttachmentSelector`; setting
  both is refused at startup. It holds at most three entries, with distinct
  frontends.
- In the namespaces listed in the cell's `galactic.attachmentLabelNamespaces`,
  an interface's `compute.datumapis.com/` labels are copied onto its
  attachment and kept in sync with the interface. A resolver whose claims
  compute creates can then be selected by labels on its Pod, such as
  `compute.datumapis.com/internal-dns-tier: node-b`. Compute copies Pod labels
  onto a claim when it creates or adopts the claim, so relabeling a running
  Pod does not reach its attachment until the claim is recreated or
  relabeled, and a key dropped from the Pod stays on a claim compute adopts.
  An interface that is no longer bound, such as one kept by a `Retain`
  policy, has these labels removed from its attachment so it stops matching.
- List only platform namespaces: tenants can set these labels on their own
  claims, and a selector matches attachments in every namespace. Anyone who
  can edit Pods in a listed namespace controls which resolver its attachments
  serve. Listing a namespace removes any hand-set `compute.datumapis.com/`
  labels from its attachments; unlisting one leaves the last copied labels in
  place until removed by hand.
- Give each attachment labels that only one resolver's selector matches.
  Startup refuses a selector that is equal to or contained in another, but
  two different selectors that both match one attachment pass that check.
  Galactic then rejects the service path of a `Single` resolver whose
  selector matches two producers on one node.
- All resolvers share the network's service destination. Each gets its own
  service endpoint and route policy per protocol.
- Set the same list on the manager and on every cell. The manager publishes
  the nameservers; the cells program the paths and report which frontends
  they verified. The manager publishes a list only once the cell reports
  that exact set, so upgrade or reconfigure cells before the manager. A
  context whose cell reports a different set keeps its current nameservers
  with `DNSReady` `Unknown`.
- Never replace or reorder the first entry. Every resolver's route objects
  are named from the first entry, so changing it recreates the routes for
  all resolvers at once. Startup cannot check this.

To add a second resolver to a running deployment:

1. Deploy the second resolver's producer attachments on every node first, with
   labels that only its selector matches.
2. Change the configuration to `resolvers`, keeping the existing frontend and
   selector as the first entry. The first entry's route objects keep their
   names, so its live routes are updated in place rather than recreated.
3. Guests keep the existing nameserver until the cell reports every path
   programmed, then receive both. Check that `DNSReady` returns to `True` on
   network contexts and that `status.dns.nameservers` on new interfaces lists
   both frontends.

Removing a resolver from the list deletes its endpoints and route policies on
the next reconcile and drops its frontend from the published nameservers.
The remaining nameservers stay published while the cell catches up.
Guests that already applied the old list keep it until their provider reapplies
interface settings.

Interfaces carry every nameserver in `status.dns`. A compute provider that
configures guests from its own static resolver list, rather than from
`status.dns`, must list the new frontend as well, or guests never use it.

## Several resolvers behind one address

By default a resolver is `Single`: each node may hold one of its producer
attachments, so serving one address from two resolver instances on a node
means two addresses, and guests decide how to use them. Most guests send
almost every query to the first address and try the second only after a
timeout, so a rollout of the first resolver still interrupts lookups.

Set `distribution: Spread` on a resolver to let several producer attachments
on one node serve its address. Galactic sends each new query to any of them
that its health check has not found unhealthy, and stops sending new queries
to an instance within roughly `intervalSeconds × unhealthyThreshold +
timeoutSeconds` of it failing. When every instance on a node is unhealthy,
all of them stay eligible.

```yaml
internalDNS:
  enabled: true
  resolvers:
  - frontend: fd53::53
    producerSelector:
      matchLabels:
        service.datum.net/internal-dns: "true"
      matchExpressions:
      - key: internal-dns.datum.net/tier
        operator: In
        values: [node, node-b]
    distribution: Spread
    healthCheck:
      port: 8053
      # Optional; the defaults are shown.
      intervalSeconds: 5
      timeoutSeconds: 2
      unhealthyThreshold: 3
      healthyThreshold: 3
```

- `producerSelector` is a full Kubernetes label selector, with `matchLabels`
  and `matchExpressions`. It replaces `producerAttachmentSelector` on that
  entry; set one or the other. Use it to select attachments that differ in a
  label value, such as two tiers, without relabeling them.
  `producerAttachmentSelector` keeps working unchanged.
- `distribution` is `Single` (the default) or `Spread`. `healthCheck` is
  required with `Spread` and refused with `Single`.
- `healthCheck.port` is required. Each resolver serves both UDP and TCP, and
  the UDP path has no TCP port of its own to probe. Every selected attachment
  must accept TCP connections on that port while it can answer queries.
- The node that hosts an attachment probes it from its host-side interface's
  link-local address. Where interface traffic policy is enforced, the
  attachment must allow TCP from `fe80::/10` to the health check port.
- A node may hold at most 16 attachments of one `Spread` resolver.
- The cells need a Galactic release that supports `Spread` and publishes
  producer health reports, and the cell identity needs read access to those
  reports. The generated role grants it.
- The overlap rule between resolvers still applies: a selector must not
  match every attachment another resolver's selector matches.

### Moving from separate addresses to one shared address

Starting from two `Single` resolvers, `fd53::53` on tier `node` and
`fd53::54` on tier `node-b`:

1. Make every resolver instance accept TCP on the health check port, and
   allow it in interface traffic policy where that is enforced.
2. Upgrade Galactic in every cell to a release that supports `Spread`.
3. Optional: switch the first resolver alone to `Spread`, keeping its
   selector. This checks the health check with one instance per node before
   widening it. Switching a resolver between `Single` and `Spread` replaces
   its routes on each node once; do it one cell at a time and check that
   `DNSReady` returns to `True`.
4. Replace the list with the single `Spread` resolver above, which selects
   both tiers, on the cells and then on the manager. Both tiers' instances
   start serving `fd53::53`, and `fd53::54`'s route objects are deleted.
5. Guests that already applied both nameservers keep `fd53::54` until their
   provider reapplies interface settings. It no longer answers, so those
   guests time out on it if they fall back to it, which they rarely need
   to now that `fd53::53` survives the loss of either tier.

Keep `fd53::53` as the first entry throughout: changing the first entry
recreates every resolver's routes.

To go back, restore the previous list. Switching back to `Single` replaces
the routes once more, and a `Single` resolver must again select at most one
attachment per node.
