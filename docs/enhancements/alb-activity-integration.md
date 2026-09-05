# Enhancement: Application Load Balancer Activity

**Status**: Implementing
**Author**: Engineering
**Created**: 2026-09-05

## Summary

Integrate the Activity Service with HTTPProxy and TrafficProtectionPolicy so
Application Load Balancer (ALB) timelines use product language, hostname-level
diffs, and async lifecycle outcomes — the same bar DNS already meets.

## Motivation

Users and support currently see Kubernetes-flavored CRUD lines
(`created HTTP proxy`, `updated traffic protection policy <name>`) with no
backend, no what-changed, and no programming / certificate / DNS outcomes.
HTTPProxy and TPP controllers already compute rich conditions but emit no
events, so those transitions never reach the timeline.

## Goals

- Product nouns: **load balancer** and **traffic protection**
- Display text is the portal chosen name, then the resource name
- Update summaries name the logical change (hostname added, backend changed,
  mode changed)
- Async failures and first-ready are visible
- Annotation and event scheme is extensible for later ALB features

## Non-Goals

- Portal activity-tab copy or grouping
- Rewriting Gateway / HTTPRoute / Domain / BackendTLSPolicy policies
- Replacing Kubernetes Events
- Chatty per-reconcile “still programmed” / “still pending” lines

## Proposed Activity Timeline

| Timestamp | Activity |
|-----------|----------|
| 10:00:00 | user@example.com created load balancer test-alb pointing to https://origin.example.com |
| 10:00:01 | test-alb is waiting for domain verification |
| 10:00:30 | TLS certificate issued for test-alb |
| 10:00:31 | Load balancer test-alb is live |
| 10:02:00 | user@example.com added a custom hostname api.example.com to test-alb |
| 10:03:00 | user@example.com updated load balancer test-alb to point to https://new-origin.example.com |
| 10:04:00 | user@example.com enabled traffic protection on test-alb in Observe mode |
| 10:05:00 | user@example.com changed traffic protection on test-alb from Observe to Enforce |
| 10:05:02 | Traffic protection is live on test-alb |

### Error Scenario

| Timestamp | Activity |
|-----------|----------|
| 10:00:00 | user@example.com created load balancer test-alb pointing to https://origin.example.com |
| 10:00:01 | A hostname on test-alb is already in use |
| 10:00:05 | Failed to issue TLS certificate for test-alb |
| 10:00:06 | Failed to program DNS for test-alb |

## Design Details

Activities come from two sources:

| Source | Use case |
|--------|----------|
| Audit logs | Human create / update / delete |
| Kubernetes events | Async controller outcomes that audits cannot represent |

### Display annotations

Stamped at admission (`failurePolicy: Fail`) by mutating webhooks. Helpers live
in `internal/display`.

| Annotation | Example | Set by |
|------------|---------|--------|
| `networking.datumapis.com/display-name` | `Test ALB` | Mutating webhook |
| `networking.datumapis.com/display-value` | `https://origin.example.com` | Mutating webhook |
| `networking.datumapis.com/activity-change` | `added` / `removed` / `updated` | Mutating webhook on update |
| `networking.datumapis.com/activity-field` | `hostname` / `backend` / `rule` / `mode` / `exclusions` / `sampling` / `paranoia` | Same |
| `networking.datumapis.com/activity-name` | `api.example.com` | The thing that changed |
| `networking.datumapis.com/activity-value` | `https://new-origin.example.com` | New value |

HTTPProxy diffs (old spec vs new):

- Only hostnames added → `added` + `hostname`
- Only hostnames removed → `removed` + `hostname`
- Only backend changed → `updated` + `backend`
- Only rules/matches/filters changed → `updated` + `rule`
- Mixed → `updated` with affected names

TPP diffs:

- Create display-name from the attached HTTPProxy name when resolvable,
  else the target Gateway/HTTPRoute name; display-value is mode
- Mode / sampling / exclusions / paranoia → `activity-field` + old/new value
- Missing owner is non-fatal; policy fallbacks still fire

### Controller events

CRUD is left to audit logs. Controllers emit `events.k8s.io/v1` Events only on
condition transitions. First-ready plus failures; no per-reconcile chatter.

HTTPProxy reasons: `Programmed`, `ProgrammingFailed`, `HostnameInUse`,
`HostnamesUnverified`, `CertificateIssued`, `CertificateFailed`,
`DNSRecordFailed`.

TPP reasons: `Programmed`, `ProgrammingFailed`, `WaitingForCertificates`.

TPP events set `related` to the owning HTTPProxy when found so timeline links
open the load balancer.

Event emission is best-effort — create failures are logged and swallowed.

### Directory structure

```
docs/enhancements/alb-activity-integration.md
config/milo/activity/policies/httpproxy-policy.yaml
config/milo/activity/policies/trafficprotectionpolicy-policy.yaml
internal/display/
internal/activitypolicy/
internal/webhook/v1alpha/httpproxy_webhook.go
internal/webhook/v1alpha/trafficprotectionpolicy_webhook.go
internal/controller/alb_activity_events.go
```

## Alternatives Considered

Custom Activity objects from controllers, or admission webhooks that create
activities. Both duplicate the Activity Service. We use ActivityPolicy + Events,
with a mutating webhook only to stamp display annotations so create audits see
the load balancer name and backends.

## Open Questions

1. Display text prefers the portal chosen name
   (`app.kubernetes.io/name`), then `metadata.name`. Hostnames stay on
   `activity-name` for add/remove.
2. Later ALB features add a new `activity-field` and policy rules — do not
   invent a second scheme.
