# Skill: a network service sending traffic nowhere

Use when a network service is not ready, a load balancer pointing at one returns
errors from the origin, or someone asks why a service "has no endpoints".

## Start here

Run `network_service_diagnose`. It returns what the service selects, the network
it is on, members and serving counts per location, the instances that are not
serving with what they report, and the root cause.

## `NoMatchingInterfaces` — nothing matches

Normal for a service written before its workload, and nothing is served until
something matches. Say both.

Then compare `selects` with `workloadsInProject`. The usual causes:

- The workload is not running yet, or was deleted.
- The selector names the workload wrongly — a typo, or the name of something
  else.
- The service was written with hand-made labels that are on the workload but
  not on its instances' network interfaces.

Fix the selector, or start the workload. Changing a service's selector is a
normal plan and apply.

## `MultipleNetworks`

The selector matches instances on more than one network. A service covers one,
so Datum serves none rather than pick. Narrow the selector to one workload, or
move the workloads onto one network.

## `InvalidSelector`

The selector cannot be read. The message says what is wrong. Re-render with
`network_render`.

## `NoServingLocations` — instances exist, none is ready

The network is fine. Every matched instance is reporting itself not ready to
serve: starting, crashing, or shutting down. `notServing` lists them with what
they report. Move to the workload; if compute's tools are available, diagnose
the workload named there.

Do not tell the customer to change the service. It is doing what it should.

## Serving, but not everywhere

`ready` is true and `notServing` is not empty: some locations or instances are
out. Traffic goes to the nearest location with a ready instance, so people near
the broken location get a farther one — slower, not down. Say which locations
are out.

## Behind a load balancer

If a load balancer uses this service, it names the service and one of its
**port names**. A service that serves while the load balancer still fails is a
port name mismatch or a load balancer problem — hand off to the load balancer
tools if available.
