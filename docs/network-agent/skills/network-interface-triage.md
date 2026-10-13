# Skill: an instance's network interface

Use when a cause has scope `one-instance`, or an instance is stuck because its
network interface is not addressed.

## What an interface is

Each instance gets one network interface on its workload's network, with an
IPv6 address from its location's range. The interface goes through three steps:
its addresses are held (`Allocated`), the network is ready for it in its
location (`Prepared`), and its addresses are carried on the network
(`Programmed`). `Programmed` only happens as the instance starts, so it
staying pending while the instance is starting is normal.

`HolderAvailable` is different: it is the instance saying whether it can serve.
That is the workload's health. Hand off to compute if its tools are available.

## Reasons the customer acts on

| Reason | What it means | What to do |
|---|---|---|
| `AddressFamilyNotCarried` | The workload asks for a kind of address the network does not carry — IPv4 on an IPv6-only network, which includes a workload asking for a public IPv4 address | Recreate the workload asking for IPv6 only. If it genuinely needs IPv4, say new networks cannot carry it and raise it with Datum; do not suggest a new network |
| `NetworkMismatch`, `InterfaceNameMismatch`, `AddressFamilyMissing`, `AddressClassMissing`, `ReclaimPolicyMismatch` | The instance came back to a kept interface, and the workload now asks for something it does not have | Bring the workload back with its old network settings, or delete the kept interface to give its addresses up for good |

## Kept interfaces

A workload can ask to keep an instance's addresses when the instance goes away,
so it comes back on the same addresses. The kept interface waits for an
instance of the same name. `network_get` lists these as `keptInterfaces`.

Deleting a kept interface gives its addresses up. They do not come back. Say
that plainly and get agreement before planning the delete — if the address is
in someone's DNS or firewall, they will need to change it.

## Reasons that are Datum's

`AddressPoolExhausted`, `RetainedAddressConflict`, `AddressAllocationRejected`,
`AllocationFailed`, `AddressAllocationMissing`, `InterfaceHeldByAnotherClaim`,
`SubnetGatewayUnusable`, `AddressFamiliesUnknown`, `ProjectNamespaceNotFound`.
Quote the reason and the message, and say it is Datum's.

## Reasons that are waiting

`NetworkNotAvailableInLocation` (the network has not reached the location yet —
see `network-location-triage`), `NetworkContextTerminating`,
`SubnetTerminating`, and `Pending` on any step. Normal unless stalled.
