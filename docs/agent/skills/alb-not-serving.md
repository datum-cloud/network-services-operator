# Skill: a load balancer that is not working

Use when someone says their site or API is down, returns an error, or "isn't
working", and you do not yet know why.

## Start with the diagnosis, not the status

Call `alb_diagnose` with the load balancer's name. It walks the load balancer,
every hostname, and the domain behind each, and returns a cause that names
something — a hostname, a service, a record.

Do not read `alb_get`'s conditions and reason from them yourself. The top-level
conditions aggregate over a set of hostnames and name none of them, so
`PartialFailure` and `CertificatesPending` are not answers; `alb_diagnose` fans
out to find which hostname and why.

## Read the cause in this order

1. **`scope`** — how much stops working.
   - `all-traffic`: nothing is being served. Answer this first, always.
   - `one-hostname`: other hostnames still work. Say which one is affected and
     which still work, or the customer will think everything is down.
   - `degraded`: it serves, but not from everything behind it.
   - `protection-only`: traffic flows, but nothing is inspecting it.
2. **`actionability`** — who acts. Never hand a customer a `platform` cause with
   advice about their own configuration; say it is Datum's and say to raise it.
3. **`inStateFor`** — how long. A `transient` cause that has outlived its window
   comes back as `stalled`; treat it as stuck, not as in progress.
4. **`skill`** — the cause names the procedure. Load it.

## If there is no cause at all

Read `confidence` before you say anything reassuring.

- `unverified` means the settings are published and nothing reports a fault, and
  **nothing reports whether the edge can actually serve it**. Do not say it is
  working. Say what is and is not known, then settle it: a request against the
  generated hostname, or `alb_traffic_summary` to see whether anything is
  arriving at all. Load `edge-propagation` if it was created recently.
- `partial` means some evidence could not be read. The `unread` list says what.
  Say so rather than presenting a thin answer as a complete one.

A load balancer that reports nothing wrong but is genuinely unreachable is
usually one of: nothing has been pointed at it yet (no CNAME, no custom
hostname), the origin behind it is refusing connections, or it was created
moments ago. The first two show up in the access logs; the third does not show
up anywhere.

## Two cases nothing reports as a fault

- **No traffic protection.** Creating a load balancer in the portal attaches it
  on a best-effort basis, so a load balancer with none is common and no
  condition anywhere says so. `alb_diagnose` reports it under `protection`.
  Mention it, but never above an actual outage.
- **Basic auth half-installed.** A security policy without its secret, or the
  reverse, makes requests fail in a way no condition explains. If auth was
  recently changed and nothing else fits, check both are there.

## What to say

Lead with what is broken and for whom, then what to do, then the evidence.

> `app.example.com` is not serving: you have not proven you own `example.com`
> yet, so Datum will not point it anywhere. Your other hostname,
> `www.example.com`, is unaffected.
>
> Create this DNS record at your provider: name `_datum-challenge.example.com`,
> type `TXT`, value `token-abc`.
>
> (`Verified` = `RecordNotFound` on the domain, for 2 hours.)

Keep the identifiers. They are what the customer types into their DNS provider
and what they escalate with.
