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
