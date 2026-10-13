# Skill: a network not reaching a location

Use when a workload's instances in one location are waiting on the network, or
`network_diagnose` reports a cause with scope `one-location`.

## How a network reaches a location

The customer does nothing per location. When a workload placed in a location
attaches to a network, Datum sets the network up there and gives that location
its own address range from the network's /48. Every instance in the location is
addressed from that range.

So "the network is not in Dallas" almost always means "no workload on this
network is placed in Dallas yet", which is fine — or the set-up there is
reporting a reason, which `network_diagnose` returns with the location named.

## Reasons on a workload's use of the network

| Reason | What it means | Who acts |
|---|---|---|
| `NetworkNotFound` | The workload names a network that does not exist | The customer: create it (`network-create`) or fix the name |
| `LocationNotAvailable` | The project has not been offered that location | The customer picks another, or asks Datum for access |
| `ProjectUnresolved` | Datum cannot tell which project this is | Datum |
| `NetworkContextTerminating` | The network is being removed from that location and will be set up again | Nobody, unless stalled |
| `Pending`, `Unknown` | Not looked at yet | Nobody, unless stalled |

`NetworkContextNotReady` only points at the location's own state. The tool
follows it; report what the location says instead.

## Reasons on the network in a location

The same address reasons as on a network (see `network-not-ready`), but for this
location's range only. `Terminating` means it is being removed; wait unless
stalled.

## The location's address range

`NotProgrammed` or `ProgrammingInProgress` on a location's address range means
it is set aside but not yet in place. Normal for a few minutes. Stalled means
Datum's.

## What to tell them

Name the location. Say whether other locations are fine — `network_get` lists
them with instance counts — because "Dallas is waiting, Frankfurt is serving" is
a very different message from "the network is down".
