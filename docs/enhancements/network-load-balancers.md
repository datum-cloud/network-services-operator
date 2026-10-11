# Network Load Balancers

| | |
|---|---|
| **Status** | Proposed |
| **Author** | Engineering |
| **Created** | 2026-10-11 |

## Summary

A project can expose a `NetworkService` on public TCP ports through a
`NetworkLoadBalancer`. The load balancer owns listeners and public addresses; a
separate `NetworkLoadBalancerRoute` connects a listener to a service. Each address
request chooses whether the platform allocates one address per location or one
address shared across locations.

The API is specific to Datum. It borrows the useful separation of listeners and
routes from Gateway API without making consumers configure Gateway API objects.

## What a consumer writes

This example publishes the same TCP service on a distinct IPv4 address in every
eligible location and on one shared IPv4 address announced from all of them:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkService
metadata:
  name: game
spec:
  networkInterfaces:
    selector:
      matchLabels:
        compute.datumapis.com/workload-name: game
  ports:
    - name: game
      port: 27015
      protocol: TCP
---
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancer
metadata:
  name: game
spec:
  networkRef:
    name: default
  exposure:
    addresses:
      - name: regional
        mode: Unicast
        family: IPv4
        locations:
          from: All
      - name: global
        mode: Anycast
        family: IPv4
        locations:
          from: All
  listeners:
    - name: game
      protocol: TCP
      port: 27015
      addressRefs: [regional, global]
---
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancerRoute
metadata:
  name: game
spec:
  parentRef:
    name: game
    listener: game
  backend:
    networkService:
      name: game
      port: game
```

`Unicast` allocates a different address for each selected location. `Anycast`
allocates one address and announces it from each selected location. These modes
describe the address *across locations*: within a location, more than one edge
node may announce the same address. `locations.from: All` follows every current
and future location eligible to serve this load balancer. A `Names` selector
with explicit location names limits the request to those locations. Adding an
eligible location to `All` allocates another unicast address, while the anycast
address stays the same.

A listener can name several address requests when they serve the same route.
Consumers use separate listeners when addresses need different ports or routes.
The route is a separate object so a service owner can change backends without
changing address ownership or listener configuration. A route attaches to one
named listener and initially has one `NetworkService` backend. Only one route
may claim a listener in the first release; a conflicting route reports a
condition rather than taking traffic unpredictably.

## What a consumer reads

The load balancer reports each allocated IP as one status entry, regardless of
address mode. For two locations, the example above could report:

```yaml
status:
  addresses:
    - requestName: regional
      family: IPv4
      value: 192.0.2.10
      announcements:
        - location: us-central-1
          state: Announced
    - requestName: regional
      family: IPv4
      value: 192.0.2.11
      announcements:
        - location: eu-west-1
          state: Announced
    - requestName: global
      family: IPv4
      value: 198.51.100.20
      announcements:
        - location: us-central-1
          state: Announced
        - location: eu-west-1
          state: Announced
```

These are illustrative addresses. An allocation remains visible when its route
or backends are unavailable; its announcement state changes instead. Conditions
on the load balancer distinguish address allocation and programming. Route
conditions report attachment, backend resolution, and programming. `Ready`
means at least one selected location is serving; per-location status shows which
ones are not. A missing backend does not silently release a consumer's IP.

## Delivery scope

The first release forwards TCP connections to healthy members of a
`NetworkService` in the ingress location. It supports one catch-all route per
listener, per-location unicast, multi-location anycast, and their combination.
The address allocation and location selection shape is intended for later use
by Application Load Balancers too. `HTTPProxy` keeps its current API until that
capability is implemented.

NSO must resolve the service's members, allocate VIPs, select eligible
locations, and report status. Cell controllers translate the resolved intent
into Galactic `NetworkRule` and `ServiceVIPBinding` objects. Galactic already
supports VIP-to-backend TCP/UDP forwarding and announcements, but the controllers
do not yet create these objects from project intent. Announcing a location must
be gated on a programmed rule and serving backends; losing the last usable
backend withdraws that location without releasing its address. Anycast needs
one globally unique allocation propagated to every selected location.

UDP, explicit health probes, multiple weighted backends, source-based matches,
cross-location backend fallback, and advanced routing policies require separate
data plane and API work. They are not implied by the first route shape.
