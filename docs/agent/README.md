# Agent capabilities

What this service publishes to an AI assistant: the knowledge it reads, the
skills it may follow, and (via `internal/agent`) the diagnosis it can run.

This is the provider side of the Datum AI Agent Framework. A service registers
agent capabilities alongside its catalog registration, entitlement decides which
projects receive them, and the assistant composes a conversation from exactly
the services a project is entitled to. Networking owns what appears here; the
assistant owns the document schema that carries it.

## Contents

| Path | Role |
|---|---|
| `llms-full.txt` | Knowledge. What an Application Load Balancer is made of and, critically, how to read what it reports. Fetched over HTTP and appended to the system prompt. |
| `skills/*.md` | Skills. Reviewed, step-by-step procedures, loaded on demand. |
| `embed.go` | Embeds both into the binary, so `cmd/alb-mcp` can serve them with no files to mount beside it. |
| `../../internal/agent` | The reason catalog and the diagnosis walk that back the tools. |

## Tools

`cmd/alb-mcp` publishes four read-only tools over Streamable HTTP:

| Tool | Answers |
|---|---|
| `alb_list` | Which load balancers exist and which need attention, worst first |
| `alb_get` | One load balancer assembled as the product rather than as the objects behind it |
| `alb_diagnose` | Why one is not working, walked to a cause that names something |
| `alb_reason_explain` | What a condition reason means, and who has to act |

Every tool is prefixed `alb_`, so an assistant can compose tools from several
services in one conversation without names colliding; the capability document
registers the prefixed names.

**Networking publishes no mutating tool.** Everything else a customer needs to
change a load balancer comes from the assistant's base tools, which it gives
every project turn and which act as the caller: `resources_list` and
`schema_get` to see what is there, and `resources_validate`, `resources_plan`
and `resources_apply` for the change itself. The plan token and the confirmation
step live there, once, for every service.

## HTTP surface

One process answers everything the capability document points at:

| Route | Serves |
|---|---|
| `POST /mcp` | Streamable HTTP MCP, stateless. Requires the caller's bearer token and `X-Datum-Project`. |
| `GET /llms-full.txt` | The knowledge document. Public. |
| `GET /runbooks/<name>.md` | One skill. Public. |
| `GET /healthz` | Liveness. |

The URL says `runbooks` while the directory says `skills`: the path belongs to
the agent framework and is already baked into shipped capability documents, so
it is not networking's to rename. Both document routes are unauthenticated on
purpose — the assistant fetches them to build a system prompt, before it holds
any project context, and they are static text with no tenant data in them.

## Why the knowledge leads with "how to read status"

Because a straight reading of this API gives confident wrong answers. Three
things make it unlike a resource model an assistant can reason about naively,
and all three are load-bearing enough that both the knowledge document and
`internal/agent` are built around them:

- **The top-level conditions aggregate over a set and name no member of it.**
  `DNSRecordsProgrammed=PartialFailure` means "one or more hostnames" and never
  says which. An assistant that reports the aggregate has told a customer
  something is wrong and left them to find out what. The walk fans out into
  `status.hostnameStatuses[]`; it is not following a pointer to a deeper
  condition, because there is no pointer.
- **`HostnamesInUse` is true when something is wrong.** Every other condition
  here is true when it is healthy, so the ordinary reading marks a hostname
  collision as fine and drops it silently.
- **Two reasons are set true to mean "does not apply".** `DNSZoneNotFound` and
  `NotApplicable` mean Datum does not run that domain's DNS — a normal
  arrangement. Reporting them as faults invents a problem; reporting them as
  pending tells the customer to wait for something that is never coming.

## Three things the platform does not report

These are limits, not bugs to route around, and the published documents say so
with the issue numbers:

- **Whether the edge can serve a load balancer.** Conditions go true seconds
  after create while the edge is still catching up, and nothing publishes when
  that finishes. There is no documented convergence window, so none is applied:
  a clean result is reported as `unverified` and the summary names the request
  that would settle it (#457).
- **A generated hostname's DNS record.** No condition is published at all, so
  its absence carries no information (#458).
- **Domain ownership, per hostname.** `HostnameConditionVerified` is declared in
  `api/v1alpha` and no controller writes it. Ownership is read from the `Domain`
  covering the hostname; anything waiting on the hostname's own condition waits
  forever.

Two tests are written to **fail** once the first two are fixed, so a fixed
platform cannot leave a silent workaround behind.

## Skills

Skills use progressive disclosure: only a name and a one-line description enter
the system prompt, and the body is fetched when a request matches. That lets
networking publish many procedures at near-zero prompt cost — but only if the
knowledge document does not already carry the procedure. `llms-full.txt` is
orientation and classification; the procedures live here and nowhere else, and
section 9 of it names the skill for each subsystem rather than answering from
the prompt.

| Skill | Covers |
|---|---|
| `alb-not-serving` | Top-level triage, symptom to root cause to owner |
| `hostname-not-working` | Claim, ownership, DNS record, certificate — in the order each gates the next |
| `domain-verification` | The record to create, and why "not found" is usually not a typo |
| `dns-delegation` | Whether Datum answers for a domain or the customer does |
| `certificate-not-issued` | When the certificate is the cause and when it is a symptom of DNS |
| `backend-not-reachable` | A missing service or port name, a selector matching nothing, locations out of rotation |
| `edge-propagation` | Reports ready but is not serving, and when that stops being propagation |
| `traffic-protection-triage` | Off, not attached, or blocking real traffic |
| `access-log-triage` | What actually arrived — and why no traffic is not a fault |
| `alb-create` | Prerequisites, what is settled at create, and render, plan, show, confirm, apply |

A skill never grants privileges. It can only direct the model toward tools that
are independently on the enforced allow-list, which is why these go through the
same review gate as any published configuration.

## Keeping this honest

Every reason in `internal/agent`'s catalog is classified user-actionable,
platform fault, transient or informational — the distinction that decides
whether a customer should change their configuration or escalate.

The catalog is keyed on `(conditionType, reason)`, not on reason. Reason strings
are not unique in this API: `Pending` appears on a load balancer's `Accepted`, a
hostname's `CertificateReady` and a hostname's `DNSRecordProgrammed`, with
different advice each time, so a map keyed on the reason alone would answer all
three with whichever was registered last.

Tests parse `api/v1alpha` and fail when a reason is added without being
classified; when a new file appears there without being argued in or out of the
catalog's scope; and when a reason is catalogued against a condition the
controllers do not set it on — a mispairing is otherwise invisible, because it
reads at runtime as an uncatalogued reason.

Every reason also has to be *readable*. This text reaches a paying customer
almost verbatim, and that customer runs a website — they do not operate Datum. A
term is theirs if they write it in something they author (`hostname`, `origin`,
`paranoia`, a DNS record's name and value) or read in output they already see
(`HostnamesInUse`, a response code); it is ours, and banned from the copy, if it
only ever appears inside the implementation. `TestCatalogCopyUsesNoInternalVocabulary`
and `TestPublishedDocsUseNoInternalVocabulary` enforce that, and each banned term
carries the argument for banning it so the list can be disputed rather than
guessed at. `TestPlainLanguageKeepsTheEvidence` is the counterweight: the
hostname, the condition type, the reason code and the duration must survive the
plain English, because those are what a customer escalates with — and because
this API's aggregates never name a hostname, an answer that drops it sends them
back to the console.

`Gateway` is the interesting case and the rule generalises: an API name may
appear as a quoted identifier, because the reader needs it verbatim to match
what they see in a manifest, but never as a bare word in a sentence. The
customer never names one — the protection policy's target is derived from the
load balancer's own name, and neither the CLI nor the portal ever shows it — so
"traffic protection has nothing to attach to yet" is both the permitted sentence
and the more actionable one.

When you add a condition reason, add its catalog entry in the same change, pair
it with the condition the controller actually sets it on, give it a window if it
is transient, write the explanation for the customer rather than for yourself,
and update the relevant skill if the procedure changes.
