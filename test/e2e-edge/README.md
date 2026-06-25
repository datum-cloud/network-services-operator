# End-to-end edge guarantees

These tests prove that the Datum edge *behaves* the way customers expect, by
sending real traffic through the real proxy and checking what actually comes
back. They are the standing guarantees described in
[`docs/testing/README.md`](../../docs/testing/README.md).

## How a guarantee is checked

Every scenario follows the same three-part check, in priority order:

1. **The traffic verdict (always decisive).** The test makes a real request and
   asserts on the real response — a blocked attack, a 503, a branded page, a
   200 from a healthy listener. If the edge behaves wrongly, this fails. A test
   is never satisfied by the platform merely *reporting* success.
2. **The configuration is genuinely present.** A
   [parity check](../parity/README.md) confirms the running proxy actually
   carries the configuration it was told to — closing the blind spot where a
   successful-looking response hides a rule that protects nothing.
3. **It's the right configuration serving the request.** A build marker
   confirms the response came from the configuration under test, not from stale
   config left over from a previous state — so a pass can't be a timing fluke.

The first is the point; the second and third exist so a surprising result
becomes a diagnosis rather than a mystery.

## The scenarios

Each one corresponds to a past production incident, now held in place.

### Web-app firewall enforcement
A malicious request (matching the firewall's attack rules) must be refused,
while a legitimate request to the same endpoint still succeeds. The test also
flips the policy into observe-only mode and confirms the same attack is then
*allowed* — proving the block is genuinely driven by the customer's firewall
policy and not by some unrelated default. This guards the customer's actual
protection, and the risk that a single bad firewall rule wedges the whole
listener.

### Offline backend returns a clean 503
When a backend connector is offline, the customer-facing path must return a
real 503 — not hang, and not serve a blank. This is checked as an observed
response on the user path, because that's what a customer experiences.

### Branded error page reaches the customer
When the edge serves an error, the customer must receive Datum's styled page,
confirmed by finding the page's content in the actual response body — not by
trusting that the configuration was applied. Production once needed manual
restarts to make this take effect, exactly the kind of inert-configuration gap
this scenario now catches.

### One bad certificate can't break its neighbors
A single invalid certificate must not take down the other, healthy listeners
sharing the same proxy. The test introduces a genuinely bad certificate and
confirms its listener is isolated while sibling listeners keep serving real
traffic — the "one bad resource freezes everything" failure mode, contained.

## Running them

The scenarios run against the production-fidelity environment:

```
task test-infra:up        # bring the edge online (proxy + extension server + firewall)
task test-infra:smoke     # quick confidence check: real traffic serves
task test-infra:e2e       # the full guarantee suite above
```

See [`Taskfile.test-infra.yml`](../../Taskfile.test-infra.yml) for the
environment these assume.

## Layout

- Scenario folders (`waf-enforcement/`, `connector-offline-503/`,
  `branded-error-page/`, `atomic-reject-isolation/`) — one guarantee each.
- `_steps/` — shared, reusable checks (send-a-request, confirm-configuration,
  capture-the-build-marker) so every scenario asserts behavior the same way.
- `_fixtures/` — the supporting pieces a scenario needs (a sample backend, an
  attack corpus, a pre-made bad certificate, the offline-backend stand-in).

## A note on honesty

Where the edge genuinely cannot yet do something, the suite says so rather than
papering over it. The "offline backend recovers" path is deliberately held back
because the edge today does not reliably re-apply configuration when a backend
comes *back* online — and the test proves that gap exists instead of pretending
it's closed. A guarantee we can't keep is documented as a gap, not asserted as
a pass.
