# Fixture range allocation

`IPPool` and `IPClass` are cluster-scoped and every suite in a project shares
them, so root pool CIDRs must be disjoint — a root pool that overlaps another
in the same project is refused with 409, and chainsaw runs suites concurrently
within one project. The enforcing script upstream (`hack/verify-fixture-ranges.sh`)
does not exist at the pinned ref, so the discipline is this table. Add a range
here before you add a pool.

| project | pool | CIDR | class it backs |
|---|---|---|---|
| project-alpha | `datum-network-v6-root` | `fd20:1000::/36` | `datum-network-v6` |
| project-alpha | `datum-endpoint-v4-root` | `10.128.0.0/16` | `datum-endpoint-v4` |
| project-alpha | `datum-public-v4-root` | `198.51.100.0/24` | `datum-public-v4` |
| project-beta | `datum-network-v6-root` | `fd20:2000::/36` | `datum-network-v6` |
| project-beta | `datum-endpoint-v4-root` | `10.129.0.0/16` | `datum-endpoint-v4` |
| project-beta | `datum-public-v4-root` | `203.0.113.0/24` | `datum-public-v4` |

## Where the IPv6 roots come from

`fd20::/20` is the platform's tenant VPC ULA pool
([tenant addressing plan](https://github.com/datum-cloud/enhancements/blob/main/architecture/design/network/addressing/tenant.md#ipv6-tenant-addressing-ula)),
and every VPC `/48` in the platform is issued from it. **A per-project root pool
is a stand-in for that platform-wide pool**, not the production model — the
production guarantee is that IPAM is the sole issuer of `/48`s from one pool, so
no two VPCs anywhere can collide. Here each project roots its own `/36` inside
`fd20::/20` instead, because `IPPool` is cluster-scoped and two suites drawing
from one root could not be told apart by the address they got. The gap is the
same one the note above records: these are per-project stand-ins, and the
uniqueness property under test is per-project rather than platform-wide.

Each `/36` carries 4,096 `/48`s, so a project's suites can address 4,096
networks before the fixture needs widening. Carve any new project's root from
`fd20::/20` on a `/36` boundary (`fd20:X000::/36`) and record it above.

Class names are deliberately IDENTICAL across the two projects: a controller
routing by project must reach different address space through the same class
name, and identical names are what makes a routing bug visible.

## Default classes

A claim naming no class but setting `spec.ipFamily` resolves through
`ipam.miloapis.com/is-default-class`. Exactly one class per family per project
carries it:

| project | IPv4 default | IPv6 default |
|---|---|---|
| project-alpha | `datum-endpoint-v4` | `datum-endpoint-v6` |
| project-beta | `datum-endpoint-v4` | `datum-endpoint-v6` |

The annotation goes on the LEAF of the IPv6 chain, not its root. Resolution
returns a class and allocation proceeds from there, so annotating
`datum-network-v6` would hand a claim a `/48` where an endpoint block was
wanted — and report it as a successful allocation.

`datum-public-v4` is deliberately NOT annotated: it is the named-class case and
must be reachable only through an explicit class name. Two defaults for one
family is not an error — IPAM lists the annotated classes `ORDER BY key` within
the project and takes the first whose family matches, silently ignoring the
rest — so a stray second annotation is a fixture that looks like it works.

## The IPv6 chain

`datum-network-v6` (`poolPer: [network]`, `/48`)
  → `datum-subnet-v6` (`poolPer: [network, location]`, `/64`)
    → `datum-endpoint-v6` (leaf, `/96`)

Only the root class is backed by an operator-authored pool. The `/48` and `/64`
pools are provisioned by the allocator on first claim. A claim of
`datum-endpoint-v6` must therefore carry both scope roles, `network` and
`location`; one missing a role is rejected rather than widened.

`datum-subnet-v6` repeats `network` in its `poolPer` on purpose. Pool identity
is keyed on (class name, scope digest) alone, with no reference to the parent
chain, so a subnet class scoped only by location would hand two networks in one
location the same pool.

### The VPC `/48` is a pool, not a claim

A `/48` only ever exists as the `IPPool` the cascade provisions for
`datum-network-v6` at scope `{network}`. There is no way to ask IPAM for that
pool directly, and **a claim of `datum-network-v6` does not produce it** — it
draws a second, unrelated `/48` from `datum-network-v6-root`, so a consumer
reading it would be told an address space its own endpoints are not in.

What makes IPAM materialise a network's `/48` is a claim whose ancestry needs
it. `NetworkReconciler` therefore claims one `/64` of `datum-subnet-v6` scoped
`{network}` per network, reads `status.poolRef` off it, and publishes that
pool's range as the VPC prefix. The `/64` it holds is the cost of the
`/48` existing before any interface asks for an address.

### Gateway reservation

`datum-subnet-v6` carries `reservations: {leading: 1, unitPrefixLength: 96}` so
the first `/96` of every `/64` — the block holding the subnet gateway, `::1` —
is withheld from endpoints. It is declared on `datum-subnet-v6` because that is
the class that provisions these `/64` pools, and a cascade-provisioned pool has
no author to state a reservation on.

> [!WARNING]
> **IPAM ignores this at the pinned ref.** `reservations` is accepted and
> validated on both `IPClass` and `IPPool`, but the allocator's search path
> (`allocation.FindFirstAvailableBlock`) never receives it — the code that
> excludes reserved positions has no non-test caller. Verified live: with this
> reservation set, the first endpoint claim in a fresh `/64` was allocated
> `fd20:f000::/96`, the very block containing the gateway `fd20:f000::1`. The
> declaration is correct and takes effect the moment IPAM wires it up; until
> then the gateway is still allocatable to an endpoint.

## Host addresses vs blocks

A host-address class is one whose `allowedPrefixLengths` pins min == max at the
family's full width — 32 for IPv4, 128 for IPv6. The two IPv4 classes here are
host-address classes; every class in the IPv6 chain hands out blocks.

**At the pinned IPAM ref, nothing ever writes `status.address`.** The field
exists on IPClaim and IPAllocation and round-trips through conversion, but no
code path populates it, so it reads empty for host-address classes too. A
host address arrives as a `/32` in `status.allocatedCIDR` and a consumer must
take it from there — reading `status.address` gets `""` and looks like a claim
that did not bind.

## Scope roles

Free strings, compared and never interpreted. The conventional pairs:

| role | apiGroup | kind |
|---|---|---|
| `network` | `networking.datumapis.com` | `Network` |
| `location` | `networking.datumapis.com` | `Location` |

Scope values are object NAMES, not UIDs.
