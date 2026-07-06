# Runbook: Gateway TLS certificate alerts

These alerts cover the health of the TLS certificates that gateway listeners use
to serve HTTPS. Every HTTPS hostname on a gateway shares a single edge listener,
so an unusable certificate is handled in two layers:

1. The **controller** leaves a listener with an unusable certificate out of the
   downstream gateway, so one bad certificate only affects its own hostname and
   every other hostname keeps serving. The affected listener reports the problem
   to the customer through its status conditions.
2. The **extension server** is a backstop: if a bad certificate reaches the edge
   anyway, it drops only the affected part of the listener rather than letting
   the whole listener fail.

A certificate is "unusable" when it has expired, is not valid yet, is missing,
its certificate and key do not match, or it has not been issued yet.

Related: issue [#212](https://github.com/datum-cloud/network-services-operator/issues/212).
The infra-side `EnvoyListenerUpdateRejected` alert fires when the edge actually
rejects a listener update — the alerts here are designed to fire *before* that
happens, or to explain it when it does.

## Shared diagnosis

Each alert carries labels identifying the affected object: `namespace`, `name`
(the gateway), `listener`, and usually `hostname`.

Find the gateway and the failing listener's status:

```sh
kubectl -n <namespace> get gateway <name> -o yaml | yq '.status.listeners'
```

A gated listener reports `Programmed: False` (reason `Invalid`) and
`ResolvedRefs: False` (reason `InvalidCertificateRef`) with a plain-language
message naming the hostname.

Inspect the backing certificate on the downstream (edge) cluster. The Certificate
and its Secret are named `<gateway>-<listener>`:

```sh
kubectl --context <downstream> -n <downstream-ns> get certificate <gateway>-<listener> -o yaml
kubectl --context <downstream> -n <downstream-ns> get secret <gateway>-<listener> -o yaml
```

The most common root cause is a customer pointing their domain away from Datum:
ACME renewal then fails, the certificate goes `Ready: False`, and it eventually
expires. That is a customer action, not a platform fault — the listener is
correctly withheld and recovers on its own once the certificate can be issued.

## GatewayListenerCertUnusable

**Meaning.** The controller is withholding a listener because its certificate is
unusable. The customer's HTTPS hostname is unavailable until the certificate
recovers.

**Impact.** Limited to the one hostname. Other hostnames on the gateway are
unaffected — this is the isolation working as intended.

**Diagnose.** Read the `reason` label and the listener status message (see Shared
diagnosis). Check the downstream Certificate's `Ready` condition and its
`status.notAfter`.

**Remediate.** Usually no platform action is needed — confirm whether the
customer's domain still points to Datum. If it does and issuance is genuinely
stuck, investigate cert-manager (the issuer, ACME order, and challenge for that
hostname). The listener returns automatically once the certificate is issued.

## GatewayListenerCertExpiringSoon

**Meaning.** A currently-healthy certificate expires within seven days. This is a
warning to act before it starts gating the listener.

**Impact.** None yet. It becomes `GatewayListenerCertUnusable` if the certificate
expires before it is renewed.

**Diagnose.** Check the downstream Certificate's `status.renewalTime` and whether
recent renewal attempts are failing (cert-manager events / logs for that
Certificate). Confirm the hostname's DNS still resolves to Datum, since ACME
renewal depends on it.

**Remediate.** If renewal is failing because DNS moved away, this will become a
customer-driven gating event — no platform fix. If renewal is failing for a
platform reason, fix the issuer / ACME path so cert-manager can renew.

## TLSBackstopPruningChains

**Meaning.** The extension server is actively dropping broken certificates from
the configuration it sends to the edge. This is expected for a short window
between a certificate failing and the controller withholding the listener.

**Impact.** None on its own — the backstop is protecting the listener. The
affected hostname is the one whose certificate is broken.

**Diagnose.** Check extension server logs for `pruned invalid TLS chains` to find
the affected hostnames:

```sh
kubectl -n <ext-server-ns> logs -l <ext-server-selector> | grep 'pruned invalid TLS chains'
```

If `GatewayListenerCertUnusable` is also firing for the same hostname, both
layers are working as expected and no action is needed. If **only** this alert
fires, the controller did not withhold the listener — see the next alert and
check why (start with the listener's status conditions and the controller logs).

**Remediate.** Generally none. If it persists without a matching
`GatewayListenerCertUnusable`, treat it as a controller gap and investigate the
gateway reconcile for that listener.

## TLSBackstopListenerAllCertsBroken

**Meaning (critical).** Every certificate on an edge listener is broken. The
backstop never removes a listener entirely, so the edge will reject the
configuration update for that listener and its config will freeze on its last
good state.

**Impact.** The listener stops accepting configuration changes. Because the edge
listener is shared, this can affect every hostname on it — this is the
fleet-impacting failure the two-layer design exists to prevent, so reaching it
means the controller-side protection did not catch the listener.

**Diagnose.**

```sh
kubectl -n <ext-server-ns> logs -l <ext-server-selector> | grep 'listeners_left_intact'
```

Cross-check the infra `EnvoyListenerUpdateRejected` alert, which confirms the
edge is rejecting the update. Identify every certificate on the affected listener
and why each is broken (expired, not yet valid, or mismatched), then determine
why the controller did not withhold the listener before it reached the edge.

**Remediate.** Restore or remove the broken certificates so the listener has at
least one usable certificate, which lets the edge accept the update again. Then
follow up on the controller gap that allowed an all-broken listener to be
programmed.
