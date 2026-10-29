---
name: networks
description: 'Drive Datum Cloud networks and network services yourself with the datumctl network plugin: create, change and delete them, release kept interfaces, and read why one is not working. Use when the person wants datumctl commands rather than the datum-mcp tools.'
---

# Skill: Networks and Network Services

> **Datum's own assistant** can answer questions about a network directly, using read-only tools of its own. This skill is a different surface: it teaches you to drive `datumctl` yourself.

## Description

Create and manage private networks on Datum Cloud, give a workload's instances a stable name with a network service, and find out why a network or service is not working.

A network is a private, IPv6-only address space that a project's workloads attach to. One network spans every location its workloads run in. A network service gives a workload's instances on one network a stable name and named ports, which is what a load balancer sends traffic to.

Prefer `datumctl network`, which presents networks and services as the product. Drop to raw manifests only for workloads, which choose their network in their own spec.

## Capabilities

- Create, list, describe, change and delete networks
- Create network services for a workload and point a load balancer at them
- Change what a service selects, or its ports
- See every instance's network interface, and release the ones kept after their instance went away
- Read why a network or a service is not working, and who has to act
- Preview every change before applying it

## Before you run anything: install the plugin

`datumctl network` is a plugin, not built in. Without it every command below
fails with `unknown command "network"`.

```bash
datumctl plugin install network
datumctl network version
```

That installs it from the official Datum catalog as a managed plugin, so
datumctl tracks it, verifies the download, and upgrades it with
`datumctl plugin upgrade network`. `network version` needs no login, no project
and no network, so run it first whenever anything else fails.

## What a network is made of

| What you think about | What is stored |
|---|---|
| A network | A `Network` you create |
| Its address space | An IPv6 `/48` Datum assigns, in `status.ipam.ipv6Prefix` |
| The network in one location | Made by Datum when a workload placed there attaches; each location gets a `/64` from the `/48` |
| An instance's address | A `NetworkInterface` Datum makes, labelled with the workload's name |
| A stable name for a workload's instances | A `NetworkService` you create |
| Which instances a service covers | `spec.networkInterfaces.selector`, normally the workload name label |
| A service's ports | `spec.ports[]`, each a name and a number |

You never create the per-location objects, the address ranges or the interfaces.

## The everyday commands

### Networks

```bash
datumctl network create backend
datumctl network create backend --mtu 1400 --wait
datumctl network list
datumctl network describe backend
datumctl network update backend --mtu 1400
datumctl network delete backend
```

The name is the only choice that matters, and it is final. Networks are IPv6-only and Datum assigns the address space; there is no address range to pick.

A new network is in no location. It reaches a location when a workload placed there attaches to it, so `describe` showing no locations is normal for a new network:

```bash
datumctl compute deploy api --image ghcr.io/acme/api:1.4.2 --location us-east-1 --network backend
```

A workload chooses its network when it is created and cannot move. `compute deploy` without `--network` uses `default`, and offers to create it if it is missing.

The MTU is the only setting that changes after create, and the change applies to every instance on the network. Leave it at 1440 unless the user has a reason: a larger MTU can hang connections between locations.

### Network services

```bash
datumctl network service create api --workload api-backend --port http:8080
datumctl network service list
datumctl network service describe api
datumctl network service update api --port http:8080 --port metrics:9100
datumctl network service update api --workload api-v2
datumctl network service delete api
```

`--port` is `name:number`. The number is the port the instances listen on; the name is what a load balancer refers to:

```bash
datumctl alb create shop --network-service api --port http
```

- Use `--workload` unless one service must cover several workloads. `--selector app=web` selects by label instead, and the labels have to be on the instances' network interfaces, not only on the workload. That mismatch is the usual reason a service matches nothing.
- A service covers **one network**. If what it selects spans two, it serves neither.
- A service can be created before its workload exists. It serves once matching instances report themselves ready.
- `update --port` replaces the whole port list. Name every port to keep. A load balancer pointing at a removed port name stops reaching the service.
- `compute deploy --http-port` creates a network service named after the workload, behind the load balancer it creates. Those show up in `service list`; change them through `compute deploy`, not here.

### Interfaces

```bash
datumctl network interface list --network backend
datumctl network interface list --workload api
datumctl network interface list --kept
datumctl network interface release api-0-eth0
```

A workload with `reclaimPolicy: Retain` on its network interface keeps an instance's addresses after the instance goes away. The kept interface holds them until an instance of the same name returns. `release` gives them up for good, and only works on a kept interface. Anything that refers to the address, such as a DNS record or a firewall rule, has to change.

## Reading status without getting it wrong

`describe` ends with a diagnosis line that names the cause, what it is on and who acts. Read it before guessing from conditions.

- **No locations is not a fault.** A network reaches a location only when a workload there attaches to it.
- **A service with no serving instances is a workload problem.** The service serves only instances that report themselves ready. `service describe` lists the instances that are not serving and what they report; look at the workload next, not the network.
- **`NoMatchingInterfaces`** on a service means nothing matches what it selects. `service describe` lists the workloads that do exist in the project, so a typo is easy to spot.
- **`MultipleNetworks`** means the selector matches instances on two networks. Narrow it.
- **`RangeOccupied`** on a network being deleted means instances or kept interfaces still hold addresses in it. Removal finishes on its own once they let go.
- **`AddressFamilyNotCarried`** on an instance's interface means the workload asked for IPv4, which networks do not carry — including a workload asking for a public IPv4 address. There is no network setting that changes this.
- Address allocation reasons (`AddressPoolExhausted`, `AllocationFailed` and similar) are Datum's to fix. Do not suggest recreating the network.

## What you cannot get here

- IPv4 on a network, or a network that leaves out IPv6
- Choosing a network's address ranges or subnets
- Moving a workload to another network
- Traffic between networks, firewall rules, private DNS names, or outbound internet access from instances — see the Galactic VPC limitations page

## Driving this from a script or an agent

- **Destructive commands refuse without a terminal.** `delete`, `update --mtu`, `service delete` and `interface release` ask for the name to be typed back. With no terminal, or in CI, they exit `NETWORK_ABORTED` unless you pass `--yes`.
- **`create --wait` can exit non-zero having created the network.** A timeout waiting for it to become ready is an error, but the network exists. Do not retry `create`; run `describe`.
- **`delete` returns before the network is gone** when instances are still on it. Check with `describe`.

Every writing command takes `--dry-run`, which validates against the API and discards the change. Every read takes `-o json|yaml|wide|name`.

Failures print a named exit code — `exit status 4 # NETWORK_NOT_FOUND`. The names are `NETWORK_USAGE` (2), `NETWORK_FORBIDDEN` (3), `NETWORK_NOT_FOUND` (4), `NETWORK_CONFLICT` (5), `NETWORK_INVALID` (6), `NETWORK_UNAVAILABLE` (8), `NETWORK_ABORTED` (9).

## Constraints & Guardrails

- Always use `datumctl` — never `kubectl`
- `--project` is required, or select one with `datumctl ctx use`
- `networking.datumapis.com/v1alpha` is unstable; field names may change between releases
- Prefer `datumctl network` over raw manifests
- Never offer IPv4. A network that lists it is refused, even if a form somewhere offers it
- Before deleting a network, run `describe` and tell the user which workloads lose their network and which services stop serving
- Before releasing a kept interface, tell the user its addresses are gone for good
- Creating, changing or deleting networks needs the Network Admin role in the project

## See Also

- [Galactic VPC documentation](https://www.datum.net/docs/galactic-vpc/overview.md)
- [Network services](https://www.datum.net/docs/galactic-vpc/network-services.md)
- [`datumctl` overview](https://www.datum.net/docs/datumctl/overview.md)
- [Application Load Balancer skill](../alb/SKILL.md)
