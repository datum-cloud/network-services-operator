# Testing the Datum edge

This is the map for how we test the network-services-operator (NSO) — what we
prove, and why it's built the way it is. It's written for anyone who wants to
understand the safety net, not just the people who maintain it.

## Why this exists

NSO programs the edge: when a customer creates a Gateway, a route, a web-app
firewall policy, or a connector, NSO turns that intent into live configuration
on the Envoy proxies that actually serve customer traffic. The hard part isn't
producing the configuration — it's making sure the configuration that lands on
the running proxy *does what the customer asked*.

Almost every production incident in this system has shared one shape:

> **The configuration was logically correct, the platform reported success, but
> the running proxy behaved differently than intended** — a firewall rule that
> protected nothing, an offline backend that still returned a blank error, a
> branded error page that never appeared, a single bad certificate that froze a
> whole shared listener.

These failures are invisible to ordinary tests. The Kubernetes resources report
"Programmed = True," unit tests pass, and the gap only shows up when real
traffic — often a real *attack* — arrives at the edge. So our testing is built
around one principle:

**Prove behavior at the edge with real traffic, against an environment that
looks like production — not against the platform's own report of success.**

## The two ideas everything rests on

**1. Production fidelity.** The recent incidents lived precisely on the axes
where our old test environment differed from production: the proxy version, the
"fail closed" availability coupling, the firewall data plane, and multi-cluster
replication. So the test environment stands up the *real* shape of the edge —
the same proxy version, the same extension server that rewrites configuration,
the same firewall image, and the same federation mechanism that fans
configuration out to edge clusters. If a test passes here, it passes against
something production actually runs. This environment is brought online with a
single command set (`task test-infra:up`); see
[`Taskfile.test-infra.yml`](../../Taskfile.test-infra.yml).

**2. Traffic-first, with a tie-breaker.** Every test's verdict is the
*observed behavior* of real traffic through the real proxy — a blocked attack,
a 503 for an offline backend, the branded page in the response body. We never
let "the platform says it worked" stand in for "the edge did the right thing."

Real traffic alone has one blind spot, though: a firewall that protects nothing
and a firewall that's simply not being attacked produce the *same* successful
response. So traffic is backed by a **parity check** — a comparison of what the
edge was told to do against what the running proxy is actually doing — which
turns a surprising result into a diagnosis instead of a guess. Traffic is always
the verdict; parity is the tie-breaker that catches the silently-inert case.
See [`test/parity/README.md`](../../test/parity/README.md).

## What we guarantee

The end-to-end suite ([`test/e2e-edge/README.md`](../../test/e2e-edge/README.md)) turns
each past incident into a standing guarantee, checked against real traffic:

- **A web-app firewall actually blocks attacks** — a malicious request is
  refused while a legitimate one still succeeds.
- **An offline backend fails cleanly** — the customer path returns a real 503,
  not a hang or a blank.
- **Branded error pages reach the customer** — the styled page appears in the
  actual response, not just in configuration.
- **One bad certificate can't take down its neighbors** — an invalid listener
  is isolated while the healthy listeners on the same proxy keep serving.

And the federation layer ([`config/federation/README.md`](../../config/federation/README.md))
proves that configuration created in the control plane genuinely arrives at the
edge clusters that serve traffic.

## Where things live

| Area | What it covers |
|---|---|
| [`Taskfile.test-infra.yml`](../../Taskfile.test-infra.yml) | Brings the production-fidelity edge online and runs the suites |
| [`test/e2e-edge/README.md`](../../test/e2e-edge/README.md) | The real-traffic guarantees, scenario by scenario |
| [`test/parity/README.md`](../../test/parity/README.md) | The parity check that catches silently-inert configuration |
| [`config/federation/README.md`](../../config/federation/README.md) | Fanning configuration out to edge clusters |
| [`e2e-version-pins.md`](e2e-version-pins.md) | Version pins in the prod-fidelity env — workarounds and deliberate prod-fidelity pins, with unpin criteria |

> The design rationale that led here — the original audit of test-vs-production
> gaps and the proposals for closing them — lives in the pull requests that
> introduced this work, not in the repository, because it describes a plan
> rather than the system as it stands today.
