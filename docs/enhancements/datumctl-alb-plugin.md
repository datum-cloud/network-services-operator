# `datumctl alb` plugin

| | |
|---|---|
| **Status** | Implemented |
| **Author** | Engineering |
| **Created** | 2026-09-08 |

## Summary

`datumctl-alb` is a first-party `datumctl` plugin. It lets developers manage Application Load Balancers from the terminal using the same resource shapes the cloud portal writes: `HTTPProxy`, `TrafficProtectionPolicy`, Envoy `SecurityPolicy`, and an htpasswd `Secret`.

## Motivation

Outside the portal, the only way to manage ALBs is `datumctl apply -f` against raw `HTTPProxy` YAML. That leaks Gateway API filter encoding, WAF attachment via `targetRefs` on a generated Gateway, and basic auth as a SecurityPolicy plus SHA htpasswd secret.

The portal already points users at datumctl for "advanced" header configuration. This plugin is that surface, plus parity with the portal's create / hostname / WAF / basic-auth flows.

## Goals

- Present **Application Load Balancers**, not HTTPProxies.
- Make the common path a single command: create, print the generated hostname, attach a custom hostname.
- Write portal-compatible payloads so CLI-created ALBs stay editable in the UI when they stay in the simple / host-only classes.
- Merge-safe updates that preserve connector backends and extra rules the CLI did not create.

## Non-goals

- Connector or instance backend assignment.
- Multiple origins, arbitrary path matching, response headers, URL rewrite.
- TPP sampling, score thresholds, and rule exclusions.
- Metrics, logs, and activity feeds.
- Domain CRUD (`datumctl dns` already covers DNS).

## Command surface

```
datumctl alb create   <name> --endpoint URL [--hostname FQDN]... [--host-header HOST]
                      [--tls-hostname HOST] [--display-name TEXT]
                      [--force-https|--no-force-https]
                      [--waf-mode Enforce|Observe|Disabled] [--paranoia N] [--no-waf]
                      [--wait|--no-wait] [--timeout D] [--dry-run]

datumctl alb list     [--no-headers] [-o table|wide|json|yaml|name]
datumctl alb describe <name> [-o table|wide|json|yaml]
datumctl alb update   <name> [--endpoint URL] [--tls-hostname HOST]
                      [--display-name TEXT] [--force-https|--no-force-https] [--dry-run]
datumctl alb delete   <name> [--yes] [--dry-run]

datumctl alb hostname add|remove|list <name> [<fqdn>]
datumctl alb waf      set|disable|describe <name> [--mode] [--paranoia]
datumctl alb header   set|unset|list <name> [Name=value|Name]
datumctl alb auth     set|unset|list <name> [--user] [--password-stdin]
datumctl alb version
```

Aliases: `ls` for `list`, `show`/`get` for `describe`, `rm` for `delete`.

Create waits by default until `status.canonicalHostname` is set.

## Payload contract

Matches `cloud-portal` `http-proxy.adapter.ts`:

- HTTPProxy `networking.datumapis.com/v1alpha` in namespace `default`
- Display name annotation `app.kubernetes.io/name`
- Force HTTPS = extra rule, no backends, `x-forwarded-proto: http` + `RequestRedirect` https/301
- Host override = rule-level `RequestHeaderModifier.set[{name: Host}]`
- TPP `targetRefs` a Gateway with the same name as the HTTPProxy
- Basic auth = Envoy `SecurityPolicy` + Secret `{name}-basic-auth` with `{SHA}` htpasswd

## Layout

```
cmd/datumctl-alb/main.go
internal/cmd/alb/
.goreleaser-plugin.yaml
docs/cli/datumctl-alb.md
```

## Phasing

v1 is UI parity plus request-header mutations the portal already defers to datumctl. Catalog publication (`datumctl plugin install alb`) is a follow-up PR to `datum-cloud/datumctl-plugins` after the first tagged plugin release.
