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
- Each selector must select only its own resolver's attachments. A selector
  that is equal to or contained in another is refused, and Galactic rejects a
  service path whose selector matches two producers on one node.
- All resolvers share the network's service destination. Each gets its own
  service endpoint and route policy per protocol.
- Set the same list on the manager and on every cell. The manager publishes
  the nameservers; the cells program the paths.

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
Guests that already applied the old list keep it until their provider reapplies
interface settings.

Interfaces carry every nameserver in `status.dns`. A compute provider that
configures guests from its own static resolver list, rather than from
`status.dns`, must list the new frontend as well, or guests never use it.
