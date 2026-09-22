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

A route takes up to 16 origins and splits traffic across them. One rule decides
whether that works: **every origin in a route must agree on the Host header sent
upstream.**

- Origins that are network services need no Host rewrite, so a pool of those is
  fine.
- A URL origin takes its Host from its own hostname, so two URL origins on
  different hostnames conflict.

When they conflict, the load balancer does not reject the change. It goes on
serving what it published last and says why in the status message, with the
reason still reading as though it were merely waiting. That is the case
`alb_diagnose` reports separately — if the message begins with the load balancer
not being able to be published, nothing is in flight and waiting will not help.

There are two ways round it and one of them is a trap:

- **Give each origin its own route.** Safe.
- **Set a Host override on the route.** This makes the origins agree, and it
  publishes — but every origin then receives the same Host. Any origin that
  serves by hostname (Vercel, Netlify, Fly.io, Cloudflare Pages) will answer the
  wrong site or a 404. It looks like it worked, which is what makes it worse
  than the error.

A connector origin has to be the only origin in its route.

## Before you suggest editing in the console

The console edits one route with one origin. Once a load balancer has more than
that, changing its origin, TLS or redirect settings there rebuilds the route list
from the few fields the console models and drops the rest — extra routes, extra
origins, weights, path matches — and reports success.

So after adding a route or a second origin, say plainly: hostnames, protection
and auth stay safe to edit in the console; origin, TLS and redirect do not.

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
