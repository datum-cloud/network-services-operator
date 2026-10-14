# Skill: a network that is not ready

Use when `network_diagnose` or `network_list` reports the network itself not
ready, or a workload says its network is not ready.

## Start here

Run `network_diagnose`. The root cause names the kind, the reason and who acts.
Take the reason from there, not from the network's `Ready` alone: `Ready`
repeats the address step's reason, and the tool reports it once.

## `IPv6Required` — the customer's

The network does not carry IPv6. Nothing on it can be given an address, and
waiting changes nothing. This is a network created before networks became
IPv6-only, with IPv4 alone.

The fix is to add `IPv6` to `spec.ipFamilies`. Render nothing for this: read
the network with `resources_list`, change only `ipFamilies` to include `IPv6`,
then plan, show, agree, apply. Keeping `IPv4` alongside is allowed on a network
that already had it.

## `Pending` — not looked at yet

Normal for a few minutes after create. Past `expectedWithin` it is reported as
stalled; then it is Datum's to look at.

## Address allocation reasons — Datum's

`ProjectNamespaceNotFound`, `ProjectUnresolved`, `RangeUnsupported`,
`AddressPoolExhausted`, `RetainedAddressConflict`, `AddressAllocationRejected`
and `AllocationFailed` all mean Datum could not set aside the network's address
space. None is fixable by the customer. Say it is Datum's, quote the reason and
the message, and stop. Do not suggest deleting and recreating the network: it
will hit the same thing and may lose the name.

## `RangeOccupied`

Only seen while a network is being deleted. Load `network-deletion-stuck`.

## Ready, but workloads still cannot use it

The network itself is fine; the problem is in a location or an instance. Look at
`otherCauses` and load `network-location-triage` or `network-interface-triage`.
