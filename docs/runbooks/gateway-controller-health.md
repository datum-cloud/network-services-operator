# Runbook: Gateway controller reconcile-error alerts

These alerts fire when the gateway controller's reconcile error rate is
persistently high. A reconcile error means the controller attempted to write or
update a downstream Gateway object and was rejected by the API server. While
errors persist, that gateway's configuration is frozen at its last successfully
written state — listener changes, certificate updates, and connector changes do
not reach the edge.

The most common trigger is PR #217's cert-withholding feature: when every
listener on a gateway has an unusable TLS certificate, the controller withholds
all of them, producing a downstream Gateway with zero listeners. The Gateway-API
CRD rejects that as a Required value validation error, and the controller enters
a hot-loop retrying the same rejected write.

Related: issue [#212](https://github.com/datum-cloud/network-services-operator/issues/212)
and PR [#217](https://github.com/datum-cloud/network-services-operator/pull/217).

## Shared diagnosis

The controller exposes reconcile outcomes via the standard controller-runtime
counter `controller_runtime_reconcile_total` with a `result` label
(`error` / `success`).

Identify which gateways are failing by inspecting controller logs:

```sh
kubectl -n <nso-ns> logs -l <controller-selector> | grep 'Reconciler error' | grep 'gateway'
```

Each error log line names the namespace and name of the object that failed. The
error message explains why the write was rejected.

Check the downstream Gateway object directly to confirm the current state:

```sh
kubectl --context <downstream> -n <downstream-ns> get gateway <name> -o yaml
```

## GatewayControllerReconcileErrorRatioHigh

**Meaning (warning).** More than 20% of gateway controller reconcile attempts
are returning errors, sustained for 15 minutes. The controller is struggling to
write downstream Gateway objects and some changes are not reaching the edge.

**Impact.** Affected gateways are not receiving configuration updates. New
listeners, TLS cert rotation, and connector-status changes all depend on
successful reconciles. The edge continues running whatever configuration was
last successfully programmed.

**Diagnose.** Find the failing gateways in controller logs (see Shared
diagnosis). The most common error messages are:

- `spec.listeners: Required value` — all listeners were withheld (cert
  withholding left none); the Gateway-API CRD requires at least one listener.
  If this is the cause, `GatewayListenerCertUnusable` should also be firing for
  the same gateway — check whether all listeners on that gateway have unusable
  certificates.
- `Forbidden` / `Unauthorized` — permissions regression; check the controller's
  RBAC.
- `conflict` / `ResourceVersion` — transient write conflicts; these resolve on
  their own and should not sustain a high error rate.

**Remediate.** Fix the root cause identified in the logs. For the
all-listeners-withheld case, restore at least one usable TLS certificate for the
gateway (or remove the broken Certificate references) — the listener returns
automatically once the certificate is valid.

## GatewayControllerReconcileErrorRatioCritical

**Meaning (critical).** More than 50% of gateway controller reconcile attempts
are failing, sustained for 10 minutes. The controller has effectively stopped
programming downstream gateways.

**Impact.** Treat as an active outage for gateway configuration changes. No
listener updates, TLS rotations, or connector state changes are being applied
to any affected gateways. Customers may see stale routing, expired certificates
left in place, or connectors showing incorrect availability — whatever was
programmed before the errors began.

**Diagnose.** Follow the same steps as
[GatewayControllerReconcileErrorRatioHigh](#gatewaycontrollerreconcileerrorratohigh).
At this error rate the problem is systematic — check whether the issue affects a
single gateway (one bad object) or many (a broader regression like an RBAC
change or API server outage).

Check the API server error rate to rule out an upstream problem:

```sh
kubectl get --raw /healthz
```

**Remediate.** Fix the root cause as for the warning tier. If the error is
a validation failure on one gateway, fixing that object's configuration will
unblock the rest. If an API server or permissions change caused a broad failure,
roll it back and verify the controller regains a healthy reconcile ratio before
resolving the alert.
