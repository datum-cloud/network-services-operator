# The parity check

The parity check answers one question the customer ultimately cares about:

> **Is the edge actually doing what it was told to do?**

It compares two things — what the platform *intended* to program onto the edge,
and what the running proxy is *actually* serving — and reports any difference.
It's the tie-breaker described in [`docs/testing/README.md`](../../docs/testing/README.md),
backing up the real-traffic guarantees in
[`test/e2e/README.md`](../e2e/README.md).

## Why real traffic isn't enough on its own

A successful response is reassuring but ambiguous. If a firewall is supposed to
block an attack and the attack instead succeeds, the response alone can't tell
you *why*: is the firewall switched off, did the rule not match, did the edge
never pick up the new configuration, or was the configuration applied to the
wrong place? Worse, the most dangerous version of this failure is silent — a
firewall that protects nothing still lets every ordinary request through, so
everything looks healthy right up until a real attack arrives in production.

The parity check removes the ambiguity. It looks past the response at the edge's
actual live configuration, so a surprising result becomes a specific, named
diagnosis instead of a guess.

## What it catches

It sorts any gap between intended and actual into the categories that map to
real incidents:

| Diagnosis | What it means for the customer |
|---|---|
| **Missing** | The edge was told to add protection but carries none — the rule is inert. |
| **Incomplete** | Only part of what was intended made it — partial protection. |
| **Misplaced** | The right amount of configuration, applied to the wrong place. |
| **Rejected** | The edge refused the update outright and is serving stale config. |
| **Overflowed** | The configuration was too large to deliver and was silently dropped. |

## How it fits the suite

- **Real traffic is the verdict.** Did the edge behave correctly? That's the
  pass/fail.
- **Parity is the diagnosis.** When the verdict surprises, parity says which of
  the failures above you're in — and it catches the silent, protects-nothing
  case that ordinary traffic can't reveal.

The rule the suite holds to: parity supports a real-traffic test, it never
replaces one. A scenario that could send the attack and watch it be blocked does
exactly that; parity is added on top, never in place of the behavior itself.

## Using it

The check ships as a small command, `parity-check` (see
[`cmd/parity-check`](../../cmd/parity-check)), which the end-to-end suite runs
automatically as a shared step. It can also be pointed at a live edge on its own
to ask, "does this proxy match what it was told?" — useful when diagnosing an
edge that's misbehaving.
