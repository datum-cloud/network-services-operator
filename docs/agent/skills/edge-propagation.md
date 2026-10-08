# Skill: reports ready, but is not serving

Use when a load balancer reports no faults and still does not answer, especially
just after it was created or changed. `alb_diagnose` returning
`confidence: unverified` points here.

## What the status actually claims

`Programmed` means the settings reached Datum's edge. It does **not** mean a
request has been served. Nothing Datum publishes says that.

Conditions go true within seconds of a load balancer being created, while the
edge is still catching up. So for a short period after any create, a load
balancer looks completely healthy and is not yet reachable.

## Do not invent a number

There is no documented convergence window. Do not tell a customer "give it two
minutes" or "this takes about five minutes" — Datum has not promised that, and a
number you made up becomes the thing they hold you to. Worse, once the made-up
window elapses they will believe something is broken when it may not be.

Say what is true:

> The settings are published and nothing reports a problem. Datum does not
> publish a signal for whether its edge is serving this yet, so the only way to
> know is to make a request.

Then give them the request.

## Settle it with evidence

```
curl -sSI https://<generated hostname>
```

The generated hostname is the one to test first, because it bypasses every
question about the customer's own DNS. If that works and their custom hostname
does not, the problem is DNS, not the load balancer — go to
`hostname-not-working`.

`alb_traffic_summary` is the other half: it shows what actually arrived. Note
that **no traffic is not a fault**. A load balancer nobody has called looks
exactly like one that is broken, so never read an empty result as a diagnosis.
Say that nothing has reached it, and that this is expected if nothing has been
pointed at it yet.

## When it stops being propagation

Propagation is a plausible explanation for minutes, not for hours. If a load
balancer has been reporting clean for a long time and has never served a
request, stop attributing it to the edge and look for something that would
explain silence:

- Nothing points at it. No CNAME, no custom hostname attached. Very common, and
  invisible in status.
- The origin refuses connections. The access logs show this; status does not.
- Requests are arriving and being blocked. Check traffic protection — `Enforce`
  at a high paranoia level can block legitimate traffic. Load
  `traffic-protection-triage`.

The load balancer's age is in the diagnosis. Read it before you decide which of
these you are looking at.
