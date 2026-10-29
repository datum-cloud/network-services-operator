# datum-networking

A Claude Code plugin for Datum Cloud networking: Application Load Balancers,
the hostnames and domains in front of them, and the networks and network
services behind them.

It teaches Claude to create a load balancer and to find out why one is not
working, using the same diagnosis and procedures Datum's own assistant uses.

## Install

1. Install datum-mcp, which the plugin starts for you:

   ```bash
   brew install datum-cloud/tap/datum-mcp
   ```

   Other platforms: see the
   [datum-mcp install steps](https://github.com/datum-cloud/datum-mcp#install-the-binary).

2. Add the Datum marketplace and install the plugin:

   ```bash
   claude plugin marketplace add datum-cloud/claude-code-plugins
   claude plugin install datum-networking@datum-claude-code-plugins
   ```

3. Start Claude Code. The first datum-mcp call opens a browser to sign you in to
   Datum Cloud. Then ask Claude which project to work in, or say "use project
   `<name>`".

If you already registered datum-mcp yourself with `claude mcp add`, you can
remove that registration. The plugin starts its own copy, and both read the same
sign-in and active project.

## What's in it

**Agent**

- `site-triage` finds out why a site or API on Datum is not up, and reports the
  cause, who has to act, and the fix. It reads only.

**Load balancer procedures**, loaded from datum-mcp's `alb_guide`:

| Skill | Covers |
|---|---|
| `alb-create` | Putting a site or API behind Datum, and any change to a load balancer |
| `alb-not-serving` | A site that is down or erroring, cause unknown |
| `hostname-not-working` | One hostname that does not work |
| `domain-verification` | Proving you own a domain |
| `dns-delegation` | Whether Datum or you answer DNS for a domain |
| `certificate-not-issued` | HTTPS failing on one hostname |
| `backend-not-reachable` | Errors from the origin rather than from Datum |
| `edge-propagation` | Reports healthy, does not answer |
| `traffic-protection-triage` | The WAF: off, observing, or blocking real users |
| `access-log-triage` | What actually arrived |

**Network procedures**, loaded from datum-mcp's `network_guide`:

| Skill | Covers |
|---|---|
| `network-create` | Creating a network |
| `network-service-create` | Giving a workload a stable name a load balancer can reach |
| `network-not-ready` | A network that is not ready |
| `network-location-triage` | A network not reaching one location |
| `network-interface-triage` | One instance whose interface is not addressed |
| `network-service-not-serving` | A network service with nothing behind it |
| `network-deletion-stuck` | A network delete that does not finish |

**datumctl how-tos**, for driving Datum from the command line yourself:

| Skill | Covers |
|---|---|
| `alb` | `datumctl alb`: load balancers end to end, and advanced Envoy Gateway policies |
| `httproute` | HTTPRoute matching, splitting, redirects, rewrites and mirroring |
| `client-traffic` | ClientTrafficPolicy: TLS, mTLS, HTTP/2 and HTTP/3, timeouts, client IP |
| `domains` | Domain resources and their verification |
| `networks` | `datumctl network`: networks and network services |

## How the procedures stay current

The procedures are written once, in [docs/agent/skills](../docs/agent/skills)
and [docs/network-agent/skills](../docs/network-agent/skills), and built into
datum-mcp. Each skill here only tells Claude which one to load, so the
procedure always matches the tools of the datum-mcp you have installed. A test
fails when a procedure is published without a skill here, or the reverse.

The datumctl how-tos came from
[datum-cloud/skills](https://github.com/datum-cloud/skills).
