# Runbook: Gateway readiness alerts

These alerts are about one Gateway each, so they name it in the `subject` label as `<project>/<namespace>/<name>`.
`GatewayPlatformPartNotReady` pages on-call when the platform's part of a Gateway fails. The two readiness warnings
go to the owning team's channel when a Gateway is not `Accepted` and `Programmed` for any reason.

A Gateway has two parts, and the first diagnosis step is telling them apart:

- **The platform's part**: the Gateway is `Accepted`, and its default listeners (`default-http`, `default-https`), which
  serve the Gateway's own hostname with the platform's certificate, are `Programmed`.
- **The customer's part**: listeners for the customer's own hostnames. They need the customer's domain verified and
  pointed at Datum before a certificate can be issued.

When only a customer listener fails, the Gateway's own hostname keeps serving and other customer hostnames are
unaffected.

A Gateway in a project without a per-project metrics exporter has no series, so none of these alerts see it
([datum-cloud/infra#4338](https://github.com/datum-cloud/infra/issues/4338)).

Related: issue [#345](https://github.com/datum-cloud/network-services-operator/issues/345). For certificates, see
[Gateway TLS certificate alerts](gateway-tls-certificates.md).

## Shared diagnosis

Read the Gateway's conditions and every listener's conditions:

```sh
kubectl -n <namespace> get gateway <name> -o yaml | yq '.status.conditions, .status.listeners'
```

| what the status shows | whose part | what it means |
|---|---|---|
| `Accepted: False` | platform | no controller took the Gateway; check `spec.gatewayClassName` |
| a default listener `Programmed: False` | platform | the edges have not built the Gateway's own hostname |
| a customer listener `Programmed: False`, reason `Invalid`, "We couldn't issue a TLS certificate for …" | customer | the domain does not point at Datum, so the certificate cannot be issued |
| a customer listener with reason `UnverifiedHostnamesPresent` | customer | the hostname's Domain is not verified |

The Gateway's own `Programmed` is `False` with reason `Pending` while a listener waits on a certificate, and
`ListenersNotValid` while a hostname is unverified, so read the listeners, not the Gateway condition alone.

## GatewayPlatformPartNotReady

**Meaning.** For 5 minutes the Gateway has not been `Accepted`, or a default listener has not been `Programmed`.

**Impact.** The Gateway's own hostname does not serve. Customer hostnames on it do not serve either if it is not
`Accepted`.

**Diagnose.** Shared diagnosis. `Accepted: False` with no controller usually means a `spec.gatewayClassName` that no
controller serves. A default listener not `Programmed` means the edges have not built the Gateway.

**Remediate.** Find why the controller or the edges have not built it, and fix that. It is the platform's part: the
customer cannot fix it.

## GatewayNotReadySLOViolation

**Meaning.** The Gateway is older than 60 seconds and not both `Accepted` and `Programmed`.

**Impact.** Depends on the part: a platform part not ready means the Gateway's own hostname does not serve; a customer
listener not ready means only that hostname does not serve HTTPS.

**Diagnose.** Shared diagnosis. A Gateway created in the last minutes may still be getting its certificates.

**Remediate.** A customer part: tell the customer which hostname is waiting and why, from the listener's message; it
recovers on its own once the domain is verified and points at Datum. A platform part: find why the controller or the
edges have not built it, and fix that.

## GatewayDegradedSLOViolation

**Meaning.** The Gateway was ready and one of `Accepted` or `Programmed` has been `False` for over 60 seconds.

**Impact.** As above.

**Diagnose.** Shared diagnosis. A customer listener that served before and now fails usually means the domain was
pointed away from Datum and the certificate could not renew.

**Remediate.** As above.

Both alerts stop after 7 days on a Gateway that was not ready at any point in them. From there
`GatewayNeverProgrammed`, at severity `info`, records it and notifies no one.
