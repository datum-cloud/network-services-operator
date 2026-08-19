# Fixture range allocation

`IPPool` and `IPClass` are cluster-scoped and every suite in a project shares
them, so root pool CIDRs must be disjoint — a root pool that overlaps another
in the same project is refused with 409, and chainsaw runs suites concurrently
within one project. The enforcing script upstream (`hack/verify-fixture-ranges.sh`)
does not exist at the pinned ref, so the discipline is this table. Add a range
here before you add a pool.

| project | pool | CIDR | class it backs |
|---|---|---|---|
| project-alpha | `datum-network-v6-root` | `2001:db8:a000::/36` | `datum-network-v6` |
| project-alpha | `datum-endpoint-v4-root` | `10.128.0.0/16` | `datum-endpoint-v4` |
| project-alpha | `datum-public-v4-root` | `198.51.100.0/24` | `datum-public-v4` |
| project-alpha | `datum-vpc-identity-root` | `fd00:a::/32` | `datum-vpc-identity` |
| project-beta | `datum-network-v6-root` | `2001:db8:b000::/36` | `datum-network-v6` |
| project-beta | `datum-endpoint-v4-root` | `10.129.0.0/16` | `datum-endpoint-v4` |
| project-beta | `datum-public-v4-root` | `203.0.113.0/24` | `datum-public-v4` |
| project-beta | `datum-vpc-identity-root` | `fd00:b::/32` | `datum-vpc-identity` |

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

## The routing identity class

`datum-vpc-identity` (leaf, no parent, `/64`) is not part of the IPv6 chain and
does not hand out addresses. A `/64` of it is a **network's identity on the SRv6
fabric** — the low bits are the number the fabric keys a VPC on. **Its pool is
never routed.** Nothing answers at one of these prefixes, nothing may advertise
or aggregate the pool, and no tenant address may be drawn from it. It is
modelled as a prefix only because IPAM already solves uniqueness, exhaustion,
retention and audit for prefixes.

It sets **no `poolPer` and no `uniqueWithin`**, which is the whole point: one
address space, one pool, one identity per network, identical in every location
that network reaches. A claim of this class therefore carries **no scope roles**
at all — the `network` and `location` roles the IPv6 chain requires would make
the identity per-scope, which is the bug the class exists to fix.

`reclaimPolicy` is `Retain` with a 720h lease. An identifier still installed in
a remote location's forwarding tables must not be handed to another network, so
releasing the claim does not free the identifier immediately. The lease duration
is a fixture choice, not a settled platform policy.

The pool is `visibility: platform`: a consumer neither requests an identity nor
sees one. Production allocates from a dedicated `/16`; the fixture narrows it to
a `/32` per project so the two projects can hold disjoint space in one cluster.

**Uniqueness here is per-project, not platform-wide.** Identities are claimed
through each project's own control-plane path, so `project-alpha` and
`project-beta` draw from separate pools and can hold the same low bits. Real
platform uniqueness needs the platform-owned IPAM tenancy the enhancement lists
as a dependency; until it exists, these fixtures prove the allocation path and
not the guarantee.

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
