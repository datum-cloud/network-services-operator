# Skill: creating a network service

Use when someone wants a stable name for a workload's instances — usually so a
load balancer can send traffic to them — or asks for a "service" on a network.

## What a network service is

A name, a set of instances chosen by label, and the ports they answer on. Datum
keeps the membership current as instances come and go and move between
locations, and sends each request to the nearest location that has a ready
instance.

It is not a load balancer and has no public address. A load balancer reaches it
by the service's name and **a port name**.

## The one thing to know

**You never write a service directly.** `network_render`, `resources_plan`,
show the plan, get agreement, `resources_apply` that plan. Agreement is a
person saying yes.

## What to gather

| Input | Where it comes from |
|---|---|
| Name | Ask, or name it after what it serves. Lowercase, a DNS label. **Final** |
| Which instances | The workload's name. `network_get` on the workload's network lists the workloads on it |
| Ports | The port each instance listens on, and a name for it — `http`, `grpc`. Ask; never guess the number |

Use `workload` in `network_render`, not `matchLabels`, unless they need one
service across several workloads. Hand-written labels are the commonest reason
a service matches nothing: the labels have to be on the instances' network
interfaces, not just on the workload.

## Rules worth saying out loud

- A service covers **one network**. If the workloads it selects are on different
  networks, it serves neither.
- Port names and numbers are each unique within the service, and at most 16.
- A service can be created before its workload exists. It reports
  `NoMatchingInterfaces` until something matches; that is normal.
- It serves an instance only once the instance reports itself ready.

## Show the plan properly

> This creates a network service called `api` covering every instance of the
> workload `api-backend`, on port `http` (8080). A load balancer can then send
> traffic to `api` on port `http`.

## After it applies

Run `network_service_diagnose`. With running instances, expect it to serve
within a minute or two. If it does not, load `network-service-not-serving`.

If the person wants it on the internet, that is a load balancer whose origin is
this service and port name. If load balancer tools are available, hand off to
their create skill.
