# `datumctl alb` plugin

| | |
|---|---|
| **Status** | Proposed |
| **Author** | Engineering |
| **Created** | 2026-09-09 |

> [!NOTE]
> This is a product design, not a description of shipped code. It is the contract
> the plugin will be built (and later reshaped) against. Decisions are proposed
> unless marked **open**. Work that is deliberately later is marked **not in v1**.

## Summary

`datumctl-alb` is a first-party `datumctl` plugin. It lets developers manage
Application Load Balancers from the terminal without knowing that an ALB is an
`HTTPProxy`, a `TrafficProtectionPolicy`, an Envoy `SecurityPolicy`, and an
htpasswd `Secret` glued together by naming convention.

The plugin borrows its plumbing from `datumctl dns` and `datumctl compute`, and
its product model from the cloud portal. You create a load balancer, get a
hostname, then attach custom hostnames, traffic protection, request headers, and
basic auth using the same shapes the portal writes — so a CLI-created ALB stays
editable in the UI.

## Motivation

Outside the portal, the only way to manage an Application Load Balancer is
`datumctl apply -f` against raw `HTTPProxy` YAML. That surface leaks the
platform's internal shape four ways.

**The unit of storage is not the unit of thought.** Users think in load
balancers. The API stores an `HTTPProxy` for routing, a generated `Gateway` the
user never sees, a `TrafficProtectionPolicy` that attaches by `targetRefs` to
that Gateway, a `SecurityPolicy` for basic auth, and a Secret of `{SHA}`
htpasswd lines. None of those nouns appear in the product.

**The interesting outcome is a hostname, not an object.** Create is not done
when the API returns 201. It is done when `status.canonicalHostname` exists, so
the user can CNAME a domain at `<uid>.datumproxy.net` or attach a custom
hostname they already own. YAML apply cannot wait for that, and the raw object
does not explain it.

**The portal already defers "advanced" work to the CLI, then cannot edit what
the CLI wrote.** Arbitrary request headers, extra rules, and backend-level
filters push an ALB into the portal's `advanced` class. The form becomes
read-only so it cannot destroy filters it does not understand. A plugin that
writes those shapes without warning, or that clobbers them on the next
`--endpoint` update, is how the two clients fight.

**Failures hide one level down.** `Accepted` and `Programmed` are roll-ups.
Hostname conflicts, domain verification, DNS authority, certificate challenges,
and WAF `PartialFailure` live on per-hostname conditions and on the policy's
ancestors. A user who greps YAML for `status: "True"` will miss them.

A CLI is the right place to fix all four, because all four are presentation
problems over APIs that are otherwise sound.

## Goals

- Present **Application Load Balancers**, not HTTPProxies.
- Make the common path a single command with no YAML: create, print the
  generated hostname, attach a custom hostname.
- Match portal create defaults and payload encoding, so CLI-created ALBs stay in
  the portal's simple / host-only classes and remain form-editable.
- Merge-safe updates that preserve connector backends, extra rules, and filters
  the CLI did not create.
- Surface hostname, certificate, DNS, and protection state in words a user can
  act on.
- Stay a well-behaved sibling of `datumctl dns` and `datumctl compute`: same
  plugin contract, output conventions, and flag vocabulary.

## Non-goals

- **Replacing `datumctl apply -f`.** Power users keep the raw path; the plugin is
  the ergonomic one.
- **Domain and DNS zone CRUD.** `datumctl dns` already covers zones, records, and
  nameserver delegation. This plugin points at it.
- **Metrics, logs, activity, and live PoP maps.** Those are portal observation
  surfaces. Logs are not shipped in the portal yet. The CLI is a mutation and
  status tool.
- **Caching.** The portal UX target lists a Caching card; no ALB caching API
  exists today.
- **Customer-branded error pages.** Edge error HTML is a data-plane concern, not
  a per-ALB setting.

## Prior art

| Source | What the plugin takes from it |
|---|---|
| `datumctl dns` plugin | Plugin skeleton, offline `version`, entitlement pre-flight, exit-code contract, `Error:` / `Fix:` rendering, blast-radius confirmations, server-side `--dry-run`, wait-with-timeout, `Next steps:` on describe, raw objects for `-o json\|yaml` |
| `datumctl compute` plugin | Table / footer / empty-state conventions, `--org` / `--project` / `--output`, condition-to-human-word mapping, API-backed completion |
| `milo-ipam` plugin | Documented exit codes, non-interactive refusal on unrecoverable deletes, `--yes` |
| Cloud portal (shipped) | Product noun, create defaults, hostname / WAF / Host-header / basic-auth dialogs, complexity classes, display-name annotation, Force HTTPS encoding, TPP attachment by Gateway name |
| Portal UX target (`docs/ux/alb-ui-views`) | Overview vs Configuration split; Configuration jump-nav for General, Custom Hostnames, Backend pool, TLS, Security & WAF, Access Control, Caching, Danger Zone |
| Industry (`aws elbv2`, `gcloud compute backend-services`, Cloudflare, Caddy, nginx) | URL origin, wait-for-ready on create, nested hostname and WAF verbs, never printing passwords |

## Product model

An Application Load Balancer is one product object assembled from several API
objects.

| Product concept | Stored as | User-facing name |
|---|---|---|
| Load balancer | `HTTPProxy` in namespace `default` | Application Load Balancer |
| Display name | annotation `app.kubernetes.io/name` | Name in the portal |
| Default hostname | `status.canonicalHostname` | Generated hostname, e.g. `<uid>.datumproxy.net` |
| Custom hostname | `spec.hostnames[]` plus `status.hostnameStatuses[]` | Custom hostname |
| Origin | `spec.rules[].backends[].endpoint` (and optional `tls.hostname`) | Origin |
| Private origin | `backends[].connector` | Connector (shown, not assigned in v1) |
| Force HTTPS | extra rule: match `x-forwarded-proto: http`, `RequestRedirect` https/301 | Force HTTPS |
| Host override | rule-level `RequestHeaderModifier` set `Host` | Host header |
| Other request headers | same filter, additional `set` entries | Request headers (advanced) |
| Traffic protection | `TrafficProtectionPolicy` targeting Gateway `{proxy name}` | Protection / WAF |
| Basic auth | Envoy `SecurityPolicy` + Secret `{name}-basic-auth` | Access control |
| TLS for visitors | cert-manager Certificate on the data plane | Certificate status on each hostname |

The user never names the Gateway. The operator synthesizes it. Traffic
protection attaches to that Gateway by name, which is why the portal (and this
plugin) name the policy after the proxy.

### Why not expose HTTPProxy

The same argument DNS made for hiding `DNSRecordSet`. Teaching `HTTPProxy` would
teach Gateway API filters, `targetRefs`, Envoy CRDs, and htpasswd hashing. The
portal already refused that vocabulary. The CLI should too.

`kubectl` and `datumctl apply -f` remain for people who want the raw objects.
`-o json` and `-o yaml` on this plugin emit those objects for scripts, which is
how dns handles the same tension.

## Command surface

Nouns are singular with plural aliases. Verbs are explicit. The bare `alb`
command prints help, not a list: there is only one noun at the root, and `list`
is cheap enough to type. (DNS nests `zone` / `record` under `dns`, so the bare
noun can alias `list` there. Here it would hide `--help`.)

```
datumctl alb version [-o table|wide|json|yaml]

datumctl alb list     [--status active|pending|error] [--no-headers]
                      [-o table|wide|json|yaml|name]
datumctl alb create   <name> --endpoint URL [--hostname FQDN]...
                      [--display-name TEXT] [--host-header HOST]
                      [--tls-hostname HOST]
                      [--force-https|--no-force-https]
                      [--waf-mode Enforce|Observe|Disabled]
                      [--paranoia N] [--no-waf]
                      [--wait|--no-wait] [--timeout D] [--dry-run]
datumctl alb describe <name> [-o table|wide|json|yaml]
datumctl alb update   <name> [--endpoint URL] [--tls-hostname HOST]
                      [--display-name TEXT]
                      [--force-https|--no-force-https] [--dry-run]
datumctl alb delete   <name> [--yes] [--dry-run]

datumctl alb hostname add|remove|list <name> [<fqdn>] [--dry-run]
datumctl alb waf      set|disable|describe <name>
                      [--mode Enforce|Observe] [--paranoia N] [--dry-run]
datumctl alb header   set|unset|list <name> [Name=value|Name] [--dry-run]
datumctl alb auth     set|unset|list <name>
                      [--user NAME] [--password-stdin] [--dry-run]
```

Aliases: `ls` for `list`, `show` and `get` for `describe`, `rm` for `delete`.
`protection` is an alias of `waf`.

### Why `alb`, not `load-balancer` or `httpproxy`

Three options were on the table.

| Noun | Case for | Case against |
|---|---|---|
| `httpproxy` / `proxy` | Matches the API kind | Product never says this. Portal routes are `/alb`. |
| `load-balancer` | Full product phrase | Long, collides mentally with L4, and nobody types it twice. |
| `alb` | Portal URL, common speech, short | Acronym. Needs a good `--help` sentence. |

**Proposed: `alb`.** Help text always says "Application Load Balancer". The
binary is `datumctl-alb`, catalog name `alb`, matching `datumctl-dns`.

### Why nested `hostname`, `waf`, `header`, `auth`

The portal does not put these on the create form as equal fields. Create sets
origin, optional hostnames, Force HTTPS, and WAF defaults. Everything else is a
later dialog: custom hostnames, protection, Host header, basic auth.

Folding them into `update --waf-mode ... --hostname ... --user ...` would make
one command that can change four independent objects with one blast radius, and
would hide the fact that disabling WAF deletes a policy rather than flipping a
field on the proxy.

`update` is for the HTTPProxy document itself: origin, TLS hostname, display
name, Force HTTPS. Nested commands own the satellite objects and the hostname
list.

### Why `waf` rather than `tpp` or `protection`

The API kind is `TrafficProtectionPolicy`. The portal label is **Protection**.
Users and docs say **WAF**. `tpp` is an operator shortName and should never
appear in a product command.

**Proposed: `waf` as the typed command, `protection` as an alias, help text
"traffic protection (WAF)".** Status words follow the portal: Disabled,
Pending, Monitoring (Observe + programmed), Protected (Enforce + programmed),
Error (`PartialFailure`).

Detection paranoia, sampling, score thresholds, and rule exclusions are **not
in v1**. The shipped portal dialog only exposes enabled, Observe/Enforce, and a
single blocking paranoia (Relaxed / Balanced / Strict / Maximum → 1–4). Matching
that is more valuable than exposing every CRD field.

### `version` runs offline

`datumctl alb version` prints the plugin version and the networking API
group-version. It uses no credentials, makes no API call, and needs no project
or entitlement pre-flight.

You reach for a version check while debugging a broken login or an unreachable
control plane. One that needs either is useless exactly when you want it.

## Identity: name vs display name

The portal asks for a **display name** (max 50 characters) and generates a
Kubernetes object name by slug plus a random suffix. The CLI has the opposite
problem: scripts need a stable identifier they chose.

Three options:

| Option | Behaviour | Risk |
|---|---|---|
| A. Portal clone | Positional is display name; CLI generates `name` | Scripts cannot predict the object. Delete and describe become guesswork. |
| B. kubectl clone | Positional is object name; display name optional | Portal list shows the annotation, so a CLI-created ALB named `api` appears as `api` unless `--display-name` is set. |
| C. Dual lookup | Commands accept either; display names must be unique | Display names are not unique today. Ambiguous lookup would be a surprise delete. |

**Proposed: B.** `<name>` is `metadata.name`. `--display-name` writes
`app.kubernetes.io/name`, the same annotation the portal and activity policies
use. `list` and `describe` show the display name when present, then the object
name. Lookup by display name is **open** and **not in v1**.

Object names follow DNS-1123. The CLI rejects a display name over 50 characters
so a CLI-created ALB cannot break the portal form.

## Create and defaults

Create is the only command that must feel like the portal's "New Application
Load Balancer" dialog.

```sh
datumctl alb create my-app --endpoint https://origin.example.com
datumctl alb create my-app --endpoint https://origin.example.com --hostname app.example.com
datumctl alb create my-app --endpoint https://203.0.113.10 --tls-hostname origin.example.com
datumctl alb create my-app --endpoint http://origin.example.com --no-force-https --no-waf
```

Portal create defaults, which this command copies:

| Setting | Default | Why |
|---|---|---|
| Origin protocol | `https` when the URL includes a scheme; required in `--endpoint` | Portal splits protocol and host. CLI users paste URLs. |
| Force HTTPS | on | Portal create hard-codes `enableHttpRedirect: true`. |
| WAF | Enforce, paranoia 1 (Relaxed) | Portal create hard-codes this even if the form is changed. |
| Custom hostnames | none | The generated hostname is enough to start. |
| Host header | unset | Incoming Host is forwarded. |
| Display name | unset (object name is shown) | Unlike the portal, the CLI does not invent a pretty name. |

`--no-waf` skips creating a `TrafficProtectionPolicy`. `--waf-mode Disabled` is
the same outcome, kept so mode flags are a closed enum. `--no-force-https`
omits the redirect rule.

`--endpoint` is a URL, not a bare host. A missing scheme is a usage error with a
fix that shows `https://...`. An `https://` IP origin without `--tls-hostname`
is a usage error: the API requires a hostname for certificate validation, and
the portal already refuses that form.

`--host-header` on create is the portal's Host override. It is validated as a
single literal hostname (optional port). Wildcards and IP literals are rejected
for the same reason the portal rejects them: no upstream certificate can match
them.

### Why `--endpoint` is a URL, not protocol + host

The portal splits protocol and host because a form dropdown is clearer than
asking people to type `https://`. A CLI that copies that split would be
`--protocol https --host origin.example.com --port 8080`, which is three flags
for a value people already have as a URL. Paste wins.

## Waiting

`--wait` is default-on for `create`. `--no-wait` is the escape. `--timeout`
bounds the wait and defaults to 2 minutes, same as `datumctl dns zone create`.

Create waits until `status.canonicalHostname` is set. That is the string the
user CNAMEs at, and the string Overview copies. Returning before it exists
hands them an object they cannot use.

Three other wait targets were considered and rejected for v1:

| Wait until | Why not the default |
|---|---|
| API 201 only | Object exists; hostname does not. |
| `Programmed=True` | Can be true before the generated hostname is published, and stays false while a custom hostname is unverified. |
| `CertificatesReady=True` | Requires HTTPS listeners and can stay pending on a custom hostname the user has not delegated yet. Wrong for the "I just wanted the default hostname" path. |
| First request | Portal Overview empty state. No API signal. **Not in v1.** |

`hostname add` does **not** wait by default. Verification and certificate
issuance depend on the user proving the domain, which can take hours. The
command prints the hostname's current conditions and a next step: verify the
domain, or `datumctl dns zone nameservers` if they want Datum to serve the zone.

`--wait` on `hostname add` is **open**. If added, it should wait for
`Available=True` and `CertificateReady=True` on that hostname, not for
`Programmed` on the whole proxy.

## Hostnames

A load balancer has two kinds of hostname, and mixing them in one list is how
people delete the generated one.

| Kind | Source | User action |
|---|---|---|
| Generated | `status.canonicalHostname` | Copy it. CNAME or ALIAS a domain at it. Never put it in `spec.hostnames`. |
| Custom | `spec.hostnames[]` | Must be unique on the platform. Verified via a `Domain` in the same namespace (created automatically if missing). |

```sh
datumctl alb hostname add    my-app app.example.com
datumctl alb hostname list   my-app
datumctl alb hostname remove my-app app.example.com
```

`hostname list` shows generated and custom rows, marked, with per-hostname
status: availability, domain verification, DNS record, certificate.

Adding a hostname that is already programmed on another resource fails with
the server's conflict message and a fix that names the competing load balancer
when the condition includes it.

Removing a custom hostname warns that Datum-managed DNS records for that name
will go with it — the same warning the portal delete path shows. Removing the
generated hostname is not a thing: it is not in `spec.hostnames`.

Wildcard hostnames are rejected. The API does not support them.

### Custom hostname vs CNAME-only

The generated hostname is enough for many apps. Custom hostnames are for when
the visitor should see `app.example.com` on the certificate, not
`*.datumproxy.net`.

Two valid onboarding paths, both first-class:

1. Create, print canonical hostname, user CNAMEs `app.example.com` at it,
   never calls `hostname add`. TLS is the platform cert on `datumproxy.net`.
2. Create, `hostname add app.example.com`, prove the Domain (or delegate the
   zone to Datum DNS), wait for the certificate.

The plugin should not push path 2 as mandatory. The create success message
offers both.

## Origins and backends

v1 origin is a single public URL, matching the portal Origins card ("Edit
origin", singular) and the API's current `MaxItems=1` on backends.

```sh
datumctl alb update my-app --endpoint https://new-origin.example.com
datumctl alb update my-app --endpoint https://203.0.113.10 --tls-hostname origin.example.com
```

The update rebuilds the backend rule and preserves:

- a `connector` reference, if one is already on the backend
- extra rules the CLI did not create (path matches, redirects other than Force
  HTTPS, URL rewrite)
- request-header `set` entries other than ones `header` commands own

That preservation is load-bearing. A CLI that "sets the origin" by writing a
fresh one-rule spec will detach a Connector-backed ALB from its tunnel.

### Connector and instance backends — not in v1

The API already accepts `backends[].connector` (public URL through a tunnel)
and `backends[].instance` (VPC pod EndpointSlice). The portal shows a Connector
when one is attached, including Datum Desktop download, but create still asks
for a URL.

**Proposed v1: describe and list show a Connector when present; no
`--connector` flag.** Assigning a Connector from the CLI without the portal's
OS / agent context is how people attach the wrong tunnel. Instance backends
have an open control-plane gap (upstream EndpointSlice projection) and should
not be a CLI feature until that is closed.

Backend pools (multiple origins, weights) appear in the UX target and are
blocked by the API's `MaxItems=1`. **Not in v1.**

Path matching, URL rewrite, and response headers are likewise **not in v1**.
They force the portal `advanced` class. People who need them use `apply -f`.

## Force HTTPS

Force HTTPS is not "redirect all HTTP to HTTPS" in the naive sense. The edge
already terminates TLS. A redirect that matches every request loops. The portal
encodes this as a dedicated rule:

- match path `/` **and** header `x-forwarded-proto: Exact: http`
- filter `RequestRedirect` scheme `https`, status 301
- **no backends** on that rule

The plugin writes and recognizes that exact shape. A different redirect (302,
HTTPS backend-side, path rewrite) is left untouched.

`update --no-force-https` removes that rule only. `update --force-https` inserts
it if missing.

Basic auth without Force HTTPS prints a warning, matching the portal: credentials
would traverse the Internet in plaintext.

## Host header and request headers

The portal offers one header: **Host**, forwarded to the origin so virtual
hosts and origin certificates see a name they know. Anything else is "advanced"
and the portal form goes read-only.

The CLI is the surface the portal already points at for the rest.

```sh
datumctl alb create my-app --endpoint https://origin.example.com --host-header origin.example.com
datumctl alb header set   my-app Host=origin.example.com
datumctl alb header set   my-app X-Debug=1
datumctl alb header list  my-app
datumctl alb header unset my-app X-Debug
```

`--host-header` and `header set Host=...` are the same field. `header set` of a
non-Host name is allowed and **warns** that the portal will treat this load
balancer as advanced and show the form read-only.

Two other policies were considered:

| Policy | Why not |
|---|---|
| Refuse non-Host headers | Contradicts the portal's own "use datumctl" pointer. |
| Allow silently | Users will be surprised when the UI locks. A warning is the honest default. |

`header unset Host` clears the override (passthrough). Unsetting the last
non-Host header and leaving only Host should return the object to host-only
complexity so the portal form unlocks again. That round-trip is a correctness
requirement, not a nicety.

Header names are case-insensitive on match (RFC 7230) and written `Host` for
the override, matching the portal adapter.

## Traffic protection

```sh
datumctl alb waf set      my-app --mode Enforce --paranoia 1
datumctl alb waf set      my-app --mode Observe --paranoia 2
datumctl alb waf describe my-app
datumctl alb waf disable  my-app
```

`set` creates the policy if missing, named after the proxy, targeting
`Gateway/{name}` in group `gateway.networking.k8s.io`. That is the portal
create convention; attachment is by `targetRefs`, not by policy name, but
same-name is what `selectPolicyForProxy` prefers when several policies match.

`disable` deletes the policy after confirmation. Setting `--mode Disabled`
is accepted as a synonym for disable rather than writing `mode: Disabled` and
leaving a policy that still occupies the attachment. The portal's "Remove
protection" path deletes.

Paranoia is the blocking level 1–4. Help text uses the portal labels:

| Level | Label |
|---|---|
| 1 | Relaxed |
| 2 | Balanced |
| 3 | Strict |
| 4 | Maximum |

If the live policy has a higher detection level than blocking, `describe` shows
both. `set` does not change detection unless we later add `--paranoia-detection`
(**open**, **not in v1**).

Sampling percentage, score thresholds, and rule exclusions (tags, IDs, ranges)
exist on the CRD and have no portal dialog. Exposing them in v1 would make the
CLI a second, undocumented WAF console. **Not in v1**, listed under [Open
questions](#open-questions) because security-sensitive tenants will ask.

`waf describe` prints mode, paranoia, and the portal readiness state
(Disabled / Pending / Monitoring / Protected / Error), plus the programmed
message when not ready.

## Access control

```sh
echo 'secret' | datumctl alb auth set my-app --user admin --password-stdin
datumctl alb auth list my-app
datumctl alb auth unset my-app
```

Passwords are write-only. `list` prints usernames, never hashes. The Secret
uses `{SHA}` htpasswd because that is what Envoy Gateway's BasicAuth filter
accepts. bcrypt would look more responsible and would not work.

Portal rules the CLI copies:

- at least one user when enabled
- username max 64 characters, no spaces or colons (htpasswd field separator)
- password min 4 characters
- usernames unique
- `unset` deletes the `SecurityPolicy` and the `{name}-basic-auth` Secret

`--password-stdin` is required in v1 so passwords never land in argv or shell
history. A prompt when stdin is a TTY is **open**.

Multi-user: `auth set` with one `--user` replaces the whole user set in v1,
matching a dialog save, not a merge. Merging users (`auth add` / `auth remove`)
is **not in v1** — the portal saves the full list each time, and a merge CLI
would diverge from that mental model.

## Complexity and UI round-trip

The portal classifies every HTTPProxy:

| Class | Meaning | Portal |
|---|---|---|
| `simple` | No rule-level filters on the backend rule | Full form, Host header empty |
| `host-only` | Exactly one filter: set `Host` | Full form, Host header populated |
| `advanced` | Anything else: extra filters, extra backend rules, backend-level filters | Read-only banner |

**Proposed policy:**

- Create and portal-equivalent edits stay in `simple` or `host-only`.
- `header set` of a non-Host name moves the object to `advanced` and warns.
- `describe` prints the class so a user can see why the UI locked.
- `update --endpoint` and Force HTTPS toggles **must not** drop filters they
  do not own. If a merge cannot be done safely, the command errors and tells
  them to use `apply -f` or `--force`.
- `--force` on update is the "I accept data loss" hatch. It is required
  non-interactively for an unsafe merge. **Open** whether `--force` exists in
  v1 or we only refuse.

The failure mode to avoid is the one DNS hit with owner-name comparisons: a
guard that is present, runs, and does not recognise its own object. Here the
analogue is "rebuilt the backend rule from `--endpoint` and lost the Connector
/ extra headers / extra rules." Tests should include a Connector-backed proxy
and an advanced proxy as update fixtures, not only the happy simple case.

## Status vocabulary

List and describe render product words, not condition types.

### Load balancer

| Conditions | Rendered |
|---|---|
| no status yet | `Pending` |
| `Accepted=False` | `Rejected` |
| `HostnamesInUse=True` | `Hostname in use` |
| `Programmed=False` | `Pending` (message from the condition) |
| `Programmed=True` | `Active` |
| `CertificatesReady` failed | `Active` plus cert detail on describe, not a second roll-up that hides programmed |

`Active` matches the portal Overview badge. `--status` on list accepts
`active`, `pending`, `error`, `rejected`, and hyphen-folded reason tokens.
Unknown server reasons pass through raw, same rule as dns: a closed filter list
would deny a row the table can print.

### Per hostname

| Condition | Rendered |
|---|---|
| `Available=False/InUse` | `In use` |
| `Available=True/Claimed` | `Claimed` |
| `Verified=False` | `Unverified` |
| `DNSRecordProgrammed` with `DNSAuthorityMissing` | `DNS not delegated` |
| `DNSRecordProgrammed` with `NotApplicable` / `DNSZoneNotFound` | `External DNS` (user CNAMEs at the generated hostname) |
| `CertificateReady=True/CertificateIssued` | `Certificate ready` |
| `CertificateReady` challenge | `Certificate challenge` |
| `CertificateReady` failed | `Certificate failed` |

Describe prints the server's message verbatim under the word, the same way dns
prints `FriendlyMessage`.

### Protection

Portal readiness, not CRD mode alone:

| Mode + programmed | Rendered |
|---|---|
| no policy / Disabled | `Disabled` |
| Observe or Enforce, not programmed, reason `PartialFailure` | `Error` |
| Observe or Enforce, not programmed | `Pending` |
| Observe + programmed | `Monitoring` |
| Enforce + programmed | `Protected` |

## Output

### `alb list`

```
NAME     DISPLAY NAME   HOSTNAME                         ORIGIN                      PROTECTION        STATUS   AGE
my-app   Storefront     7f3a….datumproxy.net             https://origin.example.com  Enforce · Relaxed  Active   14d
api      —              api.example.com                  https://api.internal:8443   Observe · Balanced Pending  3m
```

`-o wide` adds object name (if the first column showed display name — it does
not; name stays the identity column), TLS hostname, Force HTTPS, class
(simple / host-only / advanced), and Connector if any.

Footer tally is after filtering: `--status pending` reports what it printed.

### `alb describe`

```
Load balancer    my-app                         project: acme-prod
Display name     Storefront
Created          14d ago
Class            simple

Status           Active — programmed, certificate ready
Hostname         7f3a9c2e.datumproxy.net
Origin           https://origin.example.com
Force HTTPS      on
Host header      —
Protection       Protected — Enforce · Relaxed
Basic auth       off

Custom hostnames
  app.example.com    Claimed, certificate ready

Next steps:
  Add a hostname:    datumctl alb hostname add my-app app.example.com
  Change origin:     datumctl alb update my-app --endpoint https://new.example.com
  Copy a curl:       curl -sI https://7f3a9c2e.datumproxy.net
```

The empty-overview idea from the UX target — a ready-to-copy `curl` against the
default hostname — belongs here as a next step, not as a live request stream.

When a custom hostname is unverified, the next-steps block becomes the
instruction, analogous to dns `zone describe` when delegation is incomplete.

### Machine output emits raw objects

`-o json|yaml` on `list` and `describe` emit the `HTTPProxy`. They do not
synthesize a fictional "ALB" CRD. WAF and auth are separate objects; `waf
describe -o json` and `auth list -o json` emit those.

Scripts that want the flattened table get `-o json` on a future `--flatten`, or
use `-o name`. Silently emitting a mutilated HTTPProxy with WAF fields jammed
into annotations would not round-trip through `apply`.

## Errors and exit codes

Same ladder as `datumctl dns`, with `ALB_` symbols.

| Code | Symbol | Trigger |
|---|---|---|
| 0 | — | success |
| 1 | `ALB_ERROR` | generic or unexpected |
| 2 | `ALB_USAGE` | bad flags or arguments, including client-side URL / hostname / header validation |
| 3 | `ALB_FORBIDDEN` | HTTP 403, HTTP 401, or networking not entitled for the project |
| 4 | `ALB_NOT_FOUND` | load balancer, hostname, or attached policy not found |
| 5 | `ALB_CONFLICT` | HTTP 409, hostname in use |
| 6 | `ALB_INVALID` | HTTP 400 or 422, admission rejection |
| 8 | `ALB_UNAVAILABLE` | transport failure, HTTP 429, or any HTTP 5xx |
| 9 | `ALB_ABORTED` | user declined a confirmation, or the command was interrupted |

Exit 8 is the retryable one. Automation retries 8 and nothing else.

401 is exit 3 with a fix that names `datumctl login`. Ctrl-C is exit 9, same as
declining a prompt. Transport failures are detected by error type, never by
matching message text.

Errors render as `Error:`, optional `Fix:`, then `exit status N   # ALB_CONFLICT`.
The underlying cause appears only under `--verbose`. Messages are lowercase with
no trailing period, identifiers quoted with `%q`.

Quota denials (HTTP 403 with a quota message) should use the same user-facing
hint the portal toast uses, not a raw forbidden. Exact mapping is **open**.

## Mutation safety

### Confirmation tiers

| Action | Gate |
|---|---|
| create, update origin, hostname add, header set, waf set, auth set | none |
| hostname remove | `y/N`; proceeds when non-interactive |
| waf disable, auth unset | `y/N`; proceeds when non-interactive |
| delete | type the load balancer **name** (not display name); **refuses** non-interactively without `--yes` |

Delete states the cascade explicitly: traffic protection policy, basic-auth
policy and secret, and Datum-managed DNS records for custom hostnames. The
generated hostname in `datumproxy.net` is platform-managed and goes away with
the object.

```
Deleting Application Load Balancer my-app will also delete its traffic
protection policy and basic auth configuration.
DNS records Datum created for custom hostnames will be removed.

Type the load balancer name to confirm: _
```

Prompts go to stderr. `--yes` skips them.

### `--dry-run` on every mutation

Server-side dry-run, so admission actually runs. The preview is the same code
path as the write. Output is the diff that would be applied.

### Read-modify-write with a precondition

Hostname, header, WAF, and auth mutations fetch, edit, and patch with
`resourceVersion`. Retry once on conflict, then:

```
Error: the load balancer "my-app" changed while this command was running
Fix:   re-run the command — someone else modified the same object.
```

The portal omits the precondition. The CLI sends it, same choice dns made.

## Interaction with DNS

This plugin does not create zones or records by hand. The HTTPProxy controller
creates `DNSRecordSet` objects labelled as Gateway-managed when Datum DNS has
authority for the hostname.

| User intent | Tool |
|---|---|
| Serve `app.example.com` on this ALB with Datum DNS | `datumctl dns zone create example.com`, delegate NS, then `datumctl alb hostname add my-app app.example.com` |
| Keep DNS elsewhere | CNAME `app.example.com` at the generated hostname; do not `hostname add` unless they also want a Datum certificate on that name |
| See why a hostname is `DNS not delegated` | `datumctl dns zone nameservers example.com --check` |

`hostname add` next-steps should name the dns plugin when the condition is
`DNSAuthorityMissing` or `DomainNotVerified`, rather than inventing a second
delegation UI.

Editing Gateway-managed DNS records is `datumctl dns record … --force` and is
warned there. This plugin should not offer a flag that writes those records.

## What the portal has that the CLI should not

The shipped Overview and the UX target include surfaces that are wrong in a
terminal, or not APIs yet.

| Portal / UX surface | CLI v1 |
|---|---|
| Overview traffic banner, charts, PoP map | **Not in v1** |
| Live request stream / logs | **Not in v1** (portal: "Logs coming soon") |
| Metrics (edge requests, WAF events) | **Not in v1** |
| Activity tab | **Not in v1** — activity is a portal timeline |
| Active PoPs | **Not in v1** |
| Caching card | **Not in v1** — no API |
| Configuration jump-nav as one `edit` TUI | Nested commands instead |
| Quota empty-state on create | Pre-flight **open**; admission error must still be readable if we skip it |

The CLI's "overview" is `describe`. That is enough to answer "is it live, what
is the hostname, what should I do next."

## Plumbing

Binary name `datumctl-alb`. `plugin.ServeManifest` before Cobra.
`plugin.NewRootCmd("alb", …)` injects `--org`, `--project`, `--output`.
`plugin.Token()` is fetched immediately before each call.

`SilenceUsage` and `SilenceErrors` are both set. Argument validation is always
exit 2; stock Cobra validators are re-labelled after registration, same as dns.

Persistent flags beyond the SDK three: `--verbose`, `--quiet`, `--color`,
`--yes`.

### Entitlement pre-flight

`PersistentPreRunE` checks that the project is entitled for networking. Skip
for `version`, `completion`, `help`, `__complete*`, `--help`, and the bare
root.

The service identifier a user types is `networking.datumapis.com`. The
entitlement object's name is `networking-datumapis-com`. User-facing hints use
the identifier and name `datumctl services enable`. Recognition also accepts a
legacy bare `networking`, and takes the best phase across matches so a stale
rejected grant cannot mask a live one.

`datumctl` does not currently export a shared `serviceactivation` helper the
dns plugin can import as a library from this module; both plugins implement
the same pre-flight. If a shared package appears, this plugin should switch to
it rather than keep a fork.

### Shell completion

API-backed for load balancer names. Static for enums (`--waf-mode`,
`--paranoia`, `--output`, `--color`). Hostname completion inside an ALB is
**not in v1**.

Every completion path returns `NoFileComp`, including errors, and is bounded by
a short deadline. Failures are silent.

### Layout (when built)

```
cmd/datumctl-alb/main.go
internal/cmd/alb/          commands
internal/cmd/alb/spec/     HTTPProxy / TPP / SecurityPolicy builders
internal/cmd/alb/util/     client, errors, entitlement, printer
docs/cli/datumctl-alb.md   user guide (not this document)
```

Install for development: `make build-plugin` / `make install-plugin` into
`~/.datumctl/plugins/alb`. Catalog publication (`datumctl plugin install alb`)
is a follow-up to the plugin catalog after the first tagged plugin release.

Until the catalog lists it:

```sh
datumctl plugin install datum-cloud/network-services-operator@<tag>
```

## Payload contract

Writes must match the portal adapter so UI round-trip holds.

- `HTTPProxy` `networking.datumapis.com/v1alpha`, namespace `default`
- Display name annotation `app.kubernetes.io/name`
- Force HTTPS = redirect rule with `x-forwarded-proto: http`, no backends
- Host override = rule-level `RequestHeaderModifier.set[{name: Host}]`
- TPP `targetRefs` a Gateway with the same name as the HTTPProxy, group
  `gateway.networking.k8s.io`
- Basic auth = Envoy `SecurityPolicy` + Secret `{name}-basic-auth` with
  `{SHA}` htpasswd
- WAF default on create: `Enforce`, blocking paranoia 1
- Do not send `mode: Disabled`; delete the policy instead
- Merge updates preserve `connector` and filters the command does not own

Client-side validation the API is weak on, so the CLI refuses before submit:

- origin URL scheme `http` or `https`
- origin host is a FQDN (two-plus labels) or IP
- `--tls-hostname` required for `https://<ip>`
- custom hostnames: RFC 1123, no wildcards, no trailing dot, max 16
- Host header: no wildcard, no IP literal, no whitespace, max 253
- paranoia integer 1–4
- basic-auth username / password rules above

These are CLI policy where the CRD already admits the value, and mirrors where
the CRD would reject it anyway (better messages). The distinction belongs in
this document the same way dns split "platform rejects" from "CLI declines."

## Phasing

**Phase 1 — the everyday loop.** `create` (wait for generated hostname),
`list`, `describe`, `update` origin / display name / Force HTTPS, `delete`
with cascade; `hostname add|remove|list`; `waf set|disable|describe`;
`header set|unset|list` with advanced warning; `auth set|unset|list`;
`version`; exit codes; entitlement; completion of names. User guide under
`docs/cli/`.

**Phase 2 — catalog and install.** Tagged goreleaser plugin archives, catalog
entry so `datumctl plugin install alb` works, release-bot PR to the catalog
repo.

**Phase 3 — parity with Configuration UX, as APIs exist.** Connector attach,
backend pools if `MaxItems` increases, detection paranoia, WAF exclusions,
multi-user `auth add`/`remove`, wait-on-hostname, lookup by display name.
Each item needs the API and a portal dialog before the CLI copies it.

**Not planned until the portal ships them:** logs, metrics, activity, caching,
live request stream.

## Alternatives considered

### One `alb set` instead of nested verbs

Rejected. Four satellite objects and a hostname list do not belong on one
flag set. Blast radius and help text both get worse.

### Generate object names from display names, like the portal

Rejected for v1. Scripts need a name they chose. Display name stays an
annotation.

### Hide WAF and auth until `describe` / portal

Rejected. Create defaults Enable WAF because the portal does. Omitting it
would make CLI-created ALBs less safe than UI-created ones, which is the
wrong surprise.

### Flatten WAF onto HTTPProxy in json output

Rejected. The object would not apply back. Raw kinds only.

### Wait for `Programmed` instead of canonical hostname

Rejected. Programmed is not the hostname, and the hostname is the product.

## Open questions

### Should `hostname add --wait` exist in phase 1?

Verification is often blocked on a human at a registrar. A wait that times out
at 2 minutes will look broken. A long wait needs a distinct timeout default.
Leaning **no for v1**: print conditions and next steps instead.

### Should the CLI refuse to mutate `advanced` proxies without `--force`?

Leaning **yes**, for merge-unsafe fields only (rebuilding `spec.rules`). Read
paths and additive hostname edits can proceed. Needs a precise definition of
unsafe so we do not lock people out of `hostname add`.

### Detection paranoia, sampling, thresholds, exclusions

No portal dialog. Security tenants will want exclusions. Defer until the
Configuration "Security & WAF" card exists, then copy it rather than inventing
a CLI-only WAF dialect.

### Lookup by display name

Useful, unsafe until uniqueness is enforced. Leave as object name only.

### Quota pre-flight

The portal disables Create when the project is out of `httpproxies` quota. The
CLI can wait for admission. A pre-flight would need the quota API from the
plugin process. Worth it if the admission message is opaque; otherwise skip.

### Prompt for password when stdin is a TTY

`--password-stdin` is enough for scripts. A prompt is nicer for humans and
easy to get wrong with confirmation readers sharing stdin. Decide at
implementation time with a test that a piped password never hits argv.

### Shared entitlement helper

Both dns and alb will copy the same pre-flight. A small module in `datumctl`
would pay for itself after the second plugin. Out of scope here; do not block
v1 on it.
)
