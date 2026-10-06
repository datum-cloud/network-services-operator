# Who writes a gateway's DNS names

Each name has one writer.

| Name | Example | Writer | Records |
|---|---|---|---|
| The gateway's own name | `<gateway>.datumproxy.net` | external-dns, from the gateway's `DNSEndpoint` | `A` and `AAAA` |
| Its `v4.` form | `v4.<gateway>.datumproxy.net` | external-dns, from the same `DNSEndpoint` | `A` only |
| Its `v6.` form | `v6.<gateway>.datumproxy.net` | external-dns, from the same `DNSEndpoint` | `AAAA` only |
| A hostname a user attaches | `app.example.com` | Gateway DNS, as a `DNSRecordSet` in the user's zone | `CNAME` or `ALIAS` to the gateway's own name |

## Rules

- Gateway DNS skips every name the gateway owns: the canonical name, its `v4.` and `v6.` forms, and the legacy UID forms. The check is `isDatumManagedGatewayHostname` in [`gateway_controller.go`](../../internal/controller/gateway_controller.go).
- external-dns publishes the gateway's own names for every gateway, from `ensureDownstreamGatewayDNSEndpoints` in the same file.
- Two `DNSRecordSet`s at one name are dns-operator's to arbitrate. See its [record ownership](https://github.com/datum-cloud/dns-operator/blob/main/docs/architecture/record-ownership.md).

## Why

- A `v4.` name answers IPv4 only, and a `v6.` name IPv6 only. A `CNAME` or `ALIAS` to the gateway's own name would answer both.
- PowerDNS refuses an `A` or `AAAA` beside a `CNAME`, so a second writer at a `v4.` or `v6.` name stops the address records from publishing.
