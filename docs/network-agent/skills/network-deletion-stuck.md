# Skill: deleting a network that does not finish

Use when a network has been deleted but is still there, or reports
`RangeOccupied`.

## Before deleting

Always read `network_get` first. Name the workloads and network services on it,
and any kept interfaces, and say what happens to each:

- Workloads on it lose their network. Their instances cannot be addressed.
- Network services on it stop serving.
- Kept interfaces hold addresses in the network; their addresses go with it.

Then plan, show, agree, apply — like any other change.

## `RangeOccupied`

The network's address space cannot be given back while an instance still holds
an address in it. Removal waits, deliberately: giving it back early would hand
those addresses to someone else while they are still in use.

`network_get` shows what is still on it:

1. **Workloads** — delete them, or move them to another network. Each instance
   gives its address back as it goes.
2. **Kept interfaces** — these hold addresses with no instance behind them.
   Deleting one gives its addresses up for good; say so and get agreement.

Removal finishes on its own once the last address is released. Nothing needs to
be re-applied.

On the network in one location, `RangeOccupied` is usually instances in that
location still shutting down. Wait unless it is reported stalled.

## When nothing is on it

If `network_get` shows no workloads, no kept interfaces and no locations, and it
still has not gone after `expectedWithin`, it is Datum's. Quote the reason and
the message.
