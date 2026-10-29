---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# Internet access for a network

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-goals](#non-goals)
- [Proposal](#proposal)
  - [Turning internet access on or off](#turning-internet-access-on-or-off)
  - [The platform default](#the-platform-default)
  - [Networks that existed before this feature](#networks-that-existed-before-this-feature)
  - [What a customer reads back](#what-a-customer-reads-back)
- [Design details](#design-details)
  - [How the setting reaches an instance](#how-the-setting-reaches-an-instance)
  - [The policies written for each interface](#the-policies-written-for-each-interface)
  - [Writing the policy before the instance starts](#writing-the-policy-before-the-instance-starts)
  - [Changing the setting on a running network](#changing-the-setting-on-a-running-network)
  - [Reporting honestly when nothing is blocking](#reporting-honestly-when-nothing-is-blocking)
  - [The per-interface placeholder](#the-per-interface-placeholder)
  - [Outbound source address](#outbound-source-address)
- [Dependencies](#dependencies)
- [Rollout plan](#rollout-plan)
  - [Upgrade notes](#upgrade-notes)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [References](#references)

## Summary

Networks on Datum Cloud should be private by default. This design gives each network one
setting that turns internet access on or off for every instance on it, in every location.

When internet access is off, instances on the network reach each other and the platform's
private services, such as DNS, and nothing else. The one exception is traffic through an
interface's external address, which stays open; no deployment hands one out today, and that
path must be closed before a public IPv4 class ships (#710). When it is on, they reach the whole
internet: IPv6 sites directly and IPv4 sites through DNS64. Customer networks are IPv6 only,
so one switch covers both.

The network services operator (NSO) carries the setting to each location. In each location
it writes a Galactic traffic policy for every instance interface, and Galactic enforces that
policy on the node running the instance. Nothing new is needed in Galactic or in the edge
translators, which are already deployed.

**Audience:** engineers working on NSO, compute, Galactic and infra. This document assumes you
know networks, network contexts and network interfaces.

## Motivation

Today every instance on every network can reach the internet, and a customer cannot turn
that off. A customer who wants a private network, for a database tier or for workloads that
must never call out, has no way to ask for one, and no way to check that they got one.

The platform also has no single place that decides internet access. Galactic can already
filter each interface's traffic by forwarding path, but nothing writes those filters for
customer networks.

### Goals

- One setting on a network turns internet access on or off for every instance on it.
  Traffic through an external address is not covered yet (#710).
- The setting covers IPv6 and IPv4 destinations alike.
- Turning access off applies to running instances, not only to new ones.
- Operators choose the default for new networks, and can change it later without changing
  any existing network.
- Networks created before this feature keep the internet access they have today.
- A customer can read whether their setting is in effect, and is never told their network is
  private when nothing is blocking it.

### Non-goals

- **Per-address-family control.** One switch covers both families. If finer control is ever
  needed it will come from destination rules in Galactic traffic policy, not from choosing
  an address family.
- **Per-interface control.** The interface carries a placeholder that only accepts `Inherit`.
- **Destination filtering.** This design decides whether a network reaches the internet, not
  which sites it reaches.
- **Inbound traffic.** Traffic arriving through an external address stays with the existing
  external-address feature. This design leaves that path open, unchanged.
- **Egress rate limiting.** A separate feature.
- **A platform override.** `spec.egress.internet.mode` belongs to the customer: anyone who
  can update the network can turn access on or off. The platform also needs its own way to
  cut a network off the internet, for example in response to abuse, that a customer cannot
  undo. That control will be a separate design, held outside the customer's spec (a
  platform-owned field or policy) so it never competes with the customer's setting.
- **Outbound address announcement.** Infra already announces the translators' public
  addresses to the internet.
- **A target for how fast a change applies.** Turning access off must reach running
  instances, but this design promises no time limit.

## Proposal

### Turning internet access on or off

A customer sets one field on the network:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: Network
metadata:
  name: default
spec:
  ipFamilies: [IPv6]
  egress:
    internet:
      mode: Disabled   # or Enabled
```

`Enabled` lets every instance on the network reach the internet. `Disabled` limits every
instance to the network itself and the platform's private services, except for traffic
through an external address (see
[The policies written for each interface](#the-policies-written-for-each-interface)). The
setting applies to
every instance in every location the network is used, including instances that are already
running.

### The platform default

The schema has no default for `mode`. A static schema default could not follow an operator
setting, and the platform default is meant to change over time.

Instead, NSO's operator config holds the default:

```yaml
networkEgress:
  defaultInternetMode: Enabled   # or Disabled; Enabled when unset
```

When a network is created without a mode, NSO's admission webhook writes the current
default into its spec. Every stored network therefore records an explicit mode, and nothing
downstream ever has to guess.

Changing the default affects only networks created afterwards. A network keeps the mode it
was stamped with. The webhook also protects that on update: an update that drops the field,
such as reapplying an old manifest, keeps the mode the network already had rather than
picking up a newer default.

We launch with the default set to `Enabled`, so nothing changes for customers. Later we flip
it to `Disabled`, and new networks become private by default.

### Networks that existed before this feature

Networks created before this feature have no mode. NSO's network controller stamps each one
with the configured default the next time it reconciles it, which at launch is `Enabled`, so
those networks keep internet access.

The stamp is safe to run any number of times. It only writes a network that has no mode, and
it writes with the version it read, so a mode a customer sets at the same moment wins.

Until a network is stamped, NSO treats a missing mode as the configured default, never as
`Disabled`, so no running workload loses internet access mid-rollout. In a cell, a network
context that carries no mode at all was written before the control plane projected one, and
the cell treats it as `Enabled` for the same reason.

The default must not be flipped to `Disabled` until every existing network has been stamped:

```sh
kubectl get networks -A -o json \
  | jq -r '.items[] | select(.spec.egress.internet.mode == null) | "\(.metadata.namespace)/\(.metadata.name)"'
```

### What a customer reads back

Each network interface, and the claim compute reads, carries an `InternetEgressReady`
condition. It says whether the network's setting is in effect for that instance:

| Status    | Reason        | Meaning                                                                    |
| --------- | ------------- | -------------------------------------------------------------------------- |
| `True`    | `Enabled`     | The instance can reach the internet.                                       |
| `True`    | `Disabled`    | Internet traffic is blocked; the instance reaches the network and the      |
|           |               | platform's private services only.                                          |
| `True`    | `Disabled`    | As above, but the interface holds an external address that still reaches  |
|           |               | the internet; the message says so.                                         |
| `False`   | `NotEnforced` | Access is off, but this location does not block it yet.                    |
| `Unknown` | `Pending`     | The node running the instance has not applied the setting yet.             |
| `False`   | `Failed`      | The node running the instance could not apply the setting.                 |

`Pending` and `Failed` appear only in a cell with `trafficPolicyEnforced: true`. Before
then the condition reads `Enabled` or `NotEnforced` whatever the node reports, so a binding
that failed to apply during rollout shows only on the Galactic binding itself.

The messages are written for customers. They never name nodes, policies or translators.
Operators find those details on the Galactic binding.

## Design details

### How the setting reaches an instance

1. **Customer** sets `Network.spec.egress.internet.mode`, or the webhook stamps the default.
2. **NSO control plane** copies the mode onto each location's `NetworkContext` as
   `spec.egress.internet.mode`. It always writes a mode: the network's own, or the configured
   default for a network not yet stamped.
3. **NSO cell controller** already creates a Galactic `VPCAttachment` for each interface. For
   each one it also writes an egress `TrafficPolicy`, an ingress `TrafficPolicy`, and a
   `TrafficPolicyBinding` with the attachment's name that points at the two.
4. **Galactic** installs the bound policies on the node running the instance and reports, per
   node, whether each direction is `Applied`, `Pending` or `Failed`.
5. **NSO cell controller** reads that report and sets `InternetEgressReady` on the interface
   and its claim.

| Object                                  | Written by          | Read by                  |
| --------------------------------------- | ------------------- | ------------------------ |
| `Network.spec.egress.internet.mode`     | customer or webhook | NSO control plane        |
| `NetworkContext.spec.egress.internet`   | NSO control plane   | NSO cell controller      |
| `TrafficPolicy` (egress and ingress)    | NSO cell controller | Galactic                 |
| `TrafficPolicyBinding`                  | NSO cell controller | Galactic                 |
| `TrafficPolicyBinding.status.nodes`     | Galactic            | NSO cell controller      |
| `NetworkInterface` `InternetEgressReady`| NSO cell controller | customer, compute        |

### The policies written for each interface

Each interface gets its own pair of policies, named `<attachment>-egress` and
`<attachment>-ingress`, and its own binding. They are owned by the interface, so they go
away with it. Policies are not shared between interfaces, and this design does not wait for
a network-wide policy (Galactic #914).

Galactic policies are ordered Allow and Deny rules, first match wins, and anything no rule
matches is denied. Rules here match only on the forwarding path:

| Rule                   | Path            | Internet on | Internet off |
| ---------------------- | --------------- | ----------- | ------------ |
| `allow-private`        | `Private`       | Allow       | Allow        |
| `allow-internet`       | `PublicNAT`     | Allow       | -            |
| `deny-internet`        | `PublicNAT`     | -           | Deny         |
| `allow-public-service` | `PublicService` | Allow       | Allow        |

Both directions carry the same rules:

- **`Private`** is traffic inside the network, same-node delivery and the platform's private
  services, which is how instances reach DNS. It is always allowed.
- **`PublicNAT`** is traffic to and from the edge translators (NAT66, and NAT64 with DNS64).
  Galactic does not track connections, so the translators' replies need their own ingress
  rule. Both are allowed only when internet access is on. When it is off the explicit deny
  rule makes blocked attempts show up in Galactic's per-rule counters.
- **`PublicService`** is traffic through an external address. Inbound is out of scope, so it
  stays allowed in both directions, exactly as it works today. No deployment hands an
  instance an external address yet, because no public IPv4 class ships, but compute can
  already request one. Once one can be allocated, this path lets such an instance reach the
  internet with access off, so an interface holding an external address says so in its
  `Disabled` status. Closing the path needs Galactic to tell an instance's own connections
  from replies: denying it on egress would also drop the replies to inbound traffic,
  because Galactic does not track connections. It must be closed before a public IPv4 class
  ships (#710).

### Writing the policy before the instance starts

When Galactic enforces policy, an interface whose binding has not reached its node denies
everything, private traffic included. So the cell controller writes, in this order:

1. the `VPCAttachment`;
2. both `TrafficPolicy` objects and the `TrafficPolicyBinding`;
3. the `NetworkAttachmentDefinition` the instance attaches through;
4. the interface's `Prepared` condition, which compute waits on before creating the instance.

An instance therefore never starts before its binding exists. Galactic still needs a moment
to install the binding once the instance lands on a node; during that moment the interface
reports `Pending`. A unit test checks the order.

### Changing the setting on a running network

When a network's mode changes, the control plane rewrites each location's network context,
and the cell controller rewrites the rules in each interface's two policies. The binding
keeps pointing at the same policies, so Galactic swaps the rules in place on the running
instance, with no restart and no reattach. Galactic applies a policy change to every packet
evaluated after it, including packets of connections that were already open.

The cell controller only reports `Disabled` once the node reports the new generation of both
policies as installed. A node still running the previous rules keeps the interface `Pending`.

### Reporting honestly when nothing is blocking

Galactic reports a binding as `Applied` once the node has installed it, whether the node
enforces policy or not. Enforcement is off by default and off in production today, so
`Applied` alone never proves traffic is blocked (Galactic #919).

Until Galactic reports enforcement itself, NSO's cell config states it:

```yaml
galactic:
  trafficPolicyEnforced: false   # true only when every node in the cell enforces
```

While it is `false`, an interface on a network with internet access off reports
`NotEnforced`, with a message saying the instance can still reach the internet. Operators
set it to `true` only after the cell's nodes run Galactic in enforce mode. When Galactic
#919 lands, NSO should read enforcement from the binding and drop this setting.

### The per-interface placeholder

`NetworkInterfaceClaim.spec.egress.internet.mode` and the matching field on
`NetworkInterface` accept only `Inherit`, which follows the network. They settle the default
now, so per-interface control can later accept `Enabled` and `Disabled` without changing any
existing interface. Nothing reads them yet.

### Outbound source address

The earlier draft reported the address an interface's traffic leaves from. Galactic does not
expose it per interface: a location's translators share their addresses across tenants, and
which translator carries an address is decided inside the data plane and is not published.
This design drops the field rather than ship one nothing can fill. It can return if Galactic
publishes the translator serving each attachment.

## Dependencies

1. **Galactic traffic policy in each cell.** The Galactic cell controller, its webhook and the
   `TrafficPolicy` CRDs must run in every cell before NSO ships there. Writing a policy is
   part of preparing an interface, so a cell that cannot accept one cannot start instances.
2. **Enforcement.** Nothing is blocked until a cell's nodes enforce traffic policy. That is a
   per-cell infra rollout (infra #7511 for the staging lab).
3. **Galactic #919** would let NSO report enforcement from Galactic itself rather than from
   operator config.
4. **Per-network attribution** blocks launch (see [Open questions](#open-questions)).

## Rollout plan

1. **Ship the control plane with the default `Enabled`.** Deploy NSO with
   `networkEgress.defaultInternetMode: Enabled`, in the same release as the infra entry that
   wires the new Network webhook (infra #7535). New networks are stamped `Enabled`, and the
   controller stamps every existing network `Enabled` through the same webhook. Confirm no
   network is left without a mode with the check under
   [Networks that existed before this feature](#networks-that-existed-before-this-feature).
   Nothing changes for customers.
2. **Then, one cell at a time, in this order:**
   1. Install Galactic traffic policy: its CRDs, cell controller and webhook. Nodes stay in
      audit mode, so nothing is dropped.
   2. Ship NSO's cell controller with `galactic.trafficPolicyEnforced: false`. On start it
      reconciles every interface, so running instances get their policies and binding then,
      not only new ones. While the cell's network contexts lack a mode, interfaces are
      treated as `Enabled`.
   3. In the staging lab, change the Kyverno allow-all rule so it stops generating
      `TrafficPolicyBinding`s. It writes bindings under the attachment's name, the same name
      NSO uses, and keeps them in sync, so the two would fight. Nodes are still auditing, so
      the swap drops nothing, and NSO rewrites any binding the rule leaves behind. Between
      steps 2 and 3 both write the same binding; that is harmless while nodes audit. NSO does
      not work around the rule.
   4. Wait until every binding reports `Applied` on its node and the audit counters show
      nothing enforcement would drop.
   5. Move the cell's nodes to enforce mode (infra #7511 for the staging lab).
   6. Set `galactic.trafficPolicyEnforced: true` last. Only then can an interface report
      `Disabled`, `Pending` or `Failed`.

   In the staging lab, verify that a `Disabled` network cannot reach the internet, still
   reaches its own instances and DNS, and that turning it back on restores access on a
   running instance. Production cells follow the same order.
3. **Later, flip the default to `Disabled`,** only once the check in step 1 returns no
   network. Any update to an unstamped network stamps the current default, not only the
   controller's backfill, so flipping earlier can cut off an existing network. New networks
   are then private by default, and existing networks keep their mode.

### Upgrade notes

- The cell controller reads each interface's `NetworkContext` before it writes the policies.
  An interface whose context the cell cannot read stays unprepared, with `Prepared` reason
  `AwaitingNetworkContext`, and is retried. The context is the same one the cell already
  derives the VPC from, so this only shows when the projection is missing.
- A cell with Galactic enabled but without the traffic policy CRDs stops starting instances:
  the interface controller cannot watch or write the policies. Nothing checks for the CRDs at
  startup, so step 2.1 must come first.

## Drawbacks

- **Every interface carries three more objects.** Two policies and a binding per interface is
  more objects than a shared policy would need. It keeps each interface independent and
  matches Galactic's per-interface model today.
- **Enforcement is reported from config.** `trafficPolicyEnforced` can drift from what the
  nodes actually do. It errs on the safe side when left `false`, and goes away with Galactic
  #919.
- **Network writes depend on the webhook.** The mutating Network webhook uses
  `failurePolicy: Fail`, so while it is down no one can create or edit a Network, and the
  controller's backfill fails and retries. The webhook must be serving before the
  controller stamps existing networks.
- **Private by default arrives later than the API.** The API ships with the default `Enabled`,
  so customers see the switch before new networks are private.

## Alternatives

- **A static schema default.** Simpler, but it cannot follow an operator setting, and it would
  silently change existing networks' meaning whenever it changed.
- **Shared policies per network.** Fewer objects, but Galactic binds per interface today, and
  a network-wide policy (Galactic #914) does not exist yet.
- **Per-network egress routing in Galactic.** Galactic closed those attempts (#580, #613).
  Traffic policy already decides the path per interface, on the node, for running instances.
- **An attach-time setting in the network config, with shard claims.** The earlier draft
  carried the mode in the attachment's network config and recorded an `EgressShardClaim` and
  a `NetworkEgressPolicy` per attachment through the cloud and network repos. Those repos are
  merged into Galactic and no longer developed, and an attach-time setting cannot change a
  running instance.
- **Per-address-family reach.** Dropped. Customer networks reach IPv4 through DNS64, and
  finer control belongs in destination rules.

## Open questions

1. **Per-network attribution (launch blocker).** We need to know which network sent which
   internet traffic, so an abuse report can be traced to a customer. Today's translator
   metrics only break down by machine and site (Galactic #722 is related). Galactic's
   per-interface rule counters show allowed and denied packets per attachment, which may be
   a starting point, but they do not tie a translated public address and port back to a
   network. This must be answered before internet access ships as a customer feature.
2. **What a customer sees when the translators run out of capacity.** Proposed answer: treat
   it as an operator problem, alerted on from the translators' own metrics, and surface it to
   customers through `InternetEgressReady` only if internet access actually degrades for
   their instances.

## References

- [A network in every location it is used](network-in-every-location.md): the per-location
  network context that carries the mode
- [A network interface a workload can be handed](network-interfaces.md): the interface that
  reports `InternetEgressReady`
- [Cell controller manager](cell-controller-manager.md): the controller that writes the
  policies
- Galactic: [interface traffic policy design](https://github.com/datum-cloud/galactic/blob/main/docs/enhancements/networking/interface-traffic-policy/README.md)
  and [rollout guide](https://github.com/datum-cloud/galactic/blob/main/docs/traffic-policy/rollout.md)
- [Galactic #919](https://github.com/datum-cloud/galactic/issues/919): report the effective
  enforcement mode
- [Galactic #914](https://github.com/datum-cloud/galactic/issues/914): network-wide policy
- [Galactic #722](https://github.com/datum-cloud/galactic/issues/722): NAT egress observability
- [infra #7511](https://github.com/datum-cloud/infra/issues/7511): enforce traffic policy on
  the staging lab
