# Skill: traffic protection

Use when the cause is `protection-only` scope, when protection is not attached,
or when legitimate traffic is being blocked.

## The three states

- **Not attached at all.** No policy protects this load balancer. Nothing
  reports this as a fault — no condition anywhere says so — and it is common,
  because creating a load balancer in the portal attaches protection on a
  best-effort basis and a failed attach is silent. `alb_get` and `alb_diagnose`
  both report it under `protection`.
- **`Observe`.** Requests are inspected and logged, and nothing is blocked. Safe
  to turn on at any time.
- **`Enforce`.** Matching requests are blocked.

Paranoia runs 1 to 4. Higher catches more and produces more false positives.

## Mention it, but keep it in proportion

A load balancer with no protection is worth raising. It is not an outage, and it
must never be reported above one. If a site is returning nothing at all, the
missing firewall is not the answer to "why is my site down" — say the actual
cause first, and mention protection after.

## Turning it on without breaking the site

The safe order, and the one to recommend unless they ask otherwise:

1. Attach in `Observe` at paranoia 1.
2. Leave it long enough to see real traffic, including whatever runs weekly.
3. Read the logs for what *would* have been blocked. Load `access-log-triage`.
4. Only then move to `Enforce`.
5. Raise paranoia one level at a time, repeating the same check.

Going straight to `Enforce` at a high paranoia level on a live site is how
customers block their own users, and it is the most common self-inflicted
outage here.

## Legitimate traffic being blocked

The signature is requests reaching the load balancer and coming back as `403`
without ever hitting the origin. The access logs show it; the origin's own logs
show nothing at all, which is what makes it confusing — from the origin's side
the traffic simply vanished.

What tends to trip rules at higher paranoia levels: file uploads, rich-text or
Markdown in a request body, SQL-like strings in a search field, long
base64 values, and API clients sending unusual headers.

The answer is almost never "turn it off". In order of preference: drop to
`Observe` while investigating, lower the paranoia level, or narrow what runs.
Turning protection off entirely is a decision for the customer to make
knowingly, not a fix to suggest casually.

## What not to say

Do not name the rule engine or the object protection is stored in. The customer
turned on protection for their load balancer; that is the whole model they need.

Do say the mode, the paranoia level, and — when something was blocked — what
about the request triggered it, because that is what they can change.
