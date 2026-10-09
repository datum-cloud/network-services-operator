# Skill: origins that cannot be reached

Use when the cause names a backend, a network service, or a port — or when a
load balancer is published but returns errors from the origin rather than from
Datum.

## Two kinds of origin

A route sends traffic to either:

- **A URL** — `https://origin.example.com`. Datum connects to it as any client
  would. If it is an IP address over HTTPS, a TLS hostname must be given too,
  because there is no name to check the certificate against.
- **A network service and a port name** — `storefront` and `http`. The port is a
  **name the service declares**, never a number. `8080` is not a port name.

Datum never creates, edits or deletes a network service. If one is named and
does not exist, the load balancer is not published at all.

## `NetworkServiceBackendNotFound`

Two different problems share this reason, and they need different answers.
Distinguish them by reading the service:

- **The service does not exist.** The name is wrong, or it was never created, or
  it is in another project. Say which name was asked for.
- **The service exists but does not declare that port name.** List the port names
  it does declare and give them the right one. This is the more common case and
  the more frustrating one, because the customer can see the service and assumes
  the reference is fine.

Either way the whole load balancer is unpublished, not just that route — so the
scope is `all-traffic` and the customer's site is down. Lead with that.

## A network service with nothing behind it

`NoMatchingInterfaces` means the service selects nothing. The API treats this as
an ordinary state rather than an error, because a service is commonly written
before the thing it points at exists — and it clears on its own once something
matches.

That framing is right, but do not let it read as "fine". Until something
matches, there is nowhere for traffic to go. Say both: this is a normal state,
and nothing is being served.

The fix is theirs: start the workload behind it, or correct what the service
selects. Retired capacity reads exactly like capacity that never existed, so a
workload that was deleted looks the same as one never created.

## Several origins on one route

A route takes up to 16 origins and splits requests across them by weight.
Weights are relative: origins at 1 and 3 get 25% and 75%, and an origin with no
weight counts as 1. `alb_get` gives each origin's `weight` and `share`.

Each URL origin is sent its own hostname as the Host header, so origins on
different hostnames can share a route. A Host override on the route is the
trap: it sends the same Host to every origin, so in a pool spanning several
hostnames any origin that serves by hostname (Vercel, Netlify, Fly.io,
Cloudflare Pages) answers the wrong site or a 404. It looks like it worked.

A connector origin has to be the only origin in its route.

### Drained origins

Weight 0 drains an origin: it stays in the pool and gets no requests. That is
how someone takes an origin out without deleting it, so a drained origin is not
a fault. But if `alb_get` marks a route `drained`, every origin on it is at 0.
The route is published and serves nothing. Requests to it fail at the edge
without reaching any origin, so their log lines have no upstream host. Nothing in
the status says so. The fix is
theirs: give an origin a weight above 0.

### Load balancing and health checks

Both are set once and apply to every route. `alb_get` reports them as
`loadBalancing` and `healthChecks`.

- **Algorithm.** Least request is the default when nothing is set. Round robin,
  random and consistent hash are the alternatives. Consistent hash keeps a client
  on one origin by its IP or by a header. If someone says one origin gets all of
  a test's requests, check for consistent hash before anything else: a test run
  from one machine has one IP.
- **Passive health checks.** Off unless turned on. When on, an endpoint that
  returns a run of 5xx responses (5 by default) stops getting requests for a
  while (30s at first), then gets them again. It is ejected again if it is still
  failing, for longer each time. At most half of an origin's endpoints are
  ejected at once by default. There are no active probes. Datum does not request
  a health path, so an origin that never gets requests is never judged.

Checks act inside one origin. They move requests off a failing endpoint onto
the same origin's other endpoints. A URL origin whose hostname resolves to
several addresses has several endpoints, and so does a network service with
several members. **They never move an origin's share to another origin.** The
weights decide that, whatever the origin's health, so a broken origin keeps
failing its share of requests with or without checks.

What failures look like with checks on:

- **Errors come in bursts.** A failing endpoint answers 5xx until it is ejected,
  then those errors stop for the ejection time and come back when it returns.
  That is the checks working, not an outage that comes and goes. The fix is the
  endpoint.
- **An origin with one endpoint, or with most of its endpoints failing.** Once
  fewer than half of an origin's endpoints are left, what happens depends on the
  route. If every origin on it is a URL, requests are spread over all the
  origin's endpoints again, failing ones included, so the checks stop helping.
  If any origin on the route is a network service, nothing is sent to the
  failing endpoints and those requests fail at once with `UH`.

To take a broken origin out of the split, drain it (weight 0) or remove it.

## Before you suggest editing in the console

The console manages the pool of origins on `/`, with weights, the algorithm and
health checks, so pointing someone there for those is fine. Once a load balancer
has a second route, a path match or a per-origin filter, the console locks all of
that, along with TLS, redirect and the Host override, and says to use `datumctl`.
That lock is expected. It protects what the console cannot show. Say so when you
help someone add a route, and give them the `datumctl alb` commands for the
algorithm and health checks instead.

## The rest

| Reason | What it means | Whose |
|---|---|---|
| `MultipleNetworks` | The service selects things on more than one network, and a service covers one | Theirs — narrow the selector |
| `NoServingLocations` | Every location it has something in is out of rotation | Datum's |
| `NetworkServiceMembersUnreferenced` | More members than Datum is currently sending traffic to; it serves, but not from all | Datum's |
| `NetworkServiceMembersUnaddressable` | Some members have no address of the kind the service hands out | Theirs |

The last two are `degraded`, not down. Say it is serving, and say what is not
being used.

## When the origin is a URL and the load balancer looks fine

Nothing in the load balancer's status watches a URL origin. If everything reads
`ok` and requests still fail, the answer is in the access logs, not in status:
response flags and upstream codes say whether Datum could reach the origin at
all. Load `access-log-triage`.

Do not diagnose the customer's own server from here. Say what Datum saw when it
tried.
