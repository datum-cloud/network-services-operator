# Skill: reading what actually arrived

Use when status reports nothing wrong and requests still fail, when you need to
know whether a load balancer is serving at all, or when traffic protection may
be blocking real users.

## Reading them

`alb_traffic_summary` returns counts first and examples second: how many
requests arrived, the response-code breakdown, the edge's own response flags,
which hostnames were asked for, and a sample of recent lines.

Narrow it when you are hunting something specific:

- `code` when chasing a particular failure
- `host` when a customer has several hostnames and only one misbehaves
- `since` no wider than you need — a wide window on a busy load balancer buries
  the thing you are looking for

If the project cannot show logs at all, the result says so rather than failing.
That is a property of the project, not a fault of the load balancer, and the
person can still look with `datumctl alb logs <name>` or the Logs tab in the
console.

## Why this matters more here than elsewhere

A load balancer's status describes its configuration. It does not describe a
single request. The access logs are the only evidence anywhere that this load
balancer is actually serving traffic — which makes them the answer to
`confidence: unverified` and to every "it says it's fine but it isn't" question.

## No requests is not a fault

This is the trap. A load balancer nobody has called produces exactly the same
empty result as one that is completely broken.

Never report an empty result as a diagnosis. Say that nothing has reached it in
the window you looked at, and say what that does and does not mean:

> Nothing has reached this load balancer in the last hour. That is expected if
> nothing points at it yet — it does not tell us whether it would serve a
> request.

Then widen the window, or send a request yourself and look again.

## Reading the result

**Response codes** say who answered:

- `200`, `301`, `404` and other application responses: traffic is reaching the
  origin and the origin is answering. The load balancer is doing its job;
  anything wrong is in their application.
- `403` with no corresponding request at the origin: blocked before it got
  there. Traffic protection. Load `traffic-protection-triage`.
- `502` / `503`: Datum could not get a usable answer from the origin. The origin
  is down, refusing connections, or too slow.
- `429`: rate limited.

**Response flags** say what went wrong at the connection level. They are short
codes, and they are the most useful field in the log when nothing else explains
the failure — `UF` for an upstream connection failure, `UH` for no healthy
upstream, `UT` for an upstream timeout. Quote the flag and say what it means;
do not explain the proxy that produced it.

**Hosts** tell you which hostname requests actually used. A customer who thinks
they are testing a custom hostname and whose requests all show the generated one
has a DNS problem, not a load balancer problem.

## Narrowing

Filter by response code first when hunting a failure, and by hostname when a
customer has several and only one misbehaves. Keep the window as small as will
still show the problem; a wide window on a busy load balancer buries the thing
you are looking for.

## What to hand back

Give them counts before examples. "Of 412 requests in the last hour, 9 came back
403 and 2 came back 503" orients them; a wall of log lines does not. Then show a
few lines that illustrate the finding, and say which field in them matters.
