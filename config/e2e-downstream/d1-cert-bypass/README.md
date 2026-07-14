# Expired-certificate isolation fixture (test-env-only)

In production, an expired or otherwise unusable TLS certificate is caught early:
the platform withholds that listener from the edge before it is ever delivered.
The extension server *also* removes unusable certificates at the edge, as a
second line of defense — but because the earlier check normally catches the
problem first, that edge-side removal rarely gets exercised on the normal path.

This fixture lets a test exercise it directly, by handing the edge a genuinely
expired certificate and bypassing the earlier check.

1. `mint-expired-secret.sh <namespace> <secret-name> <hostname>` writes a
   self-signed, already-expired certificate as a TLS Secret to stdout. Apply it
   into the `e2e-direct` namespace on the edge cluster.
2. The test then applies a gateway directly to the edge whose HTTPS listener
   uses that certificate. The extension server removes the bad listener while a
   healthy sibling keeps serving — which is what the test asserts.

> **Use the `e2e-direct` namespace.** The gateway controller only watches
> namespaces that already carry the `meta.datumapis.com/upstream-cluster-name`
> label when they are created; a label added afterward is not reliably picked
> up, and a gateway there can stay unprogrammed. The `e2e-direct` namespace is
> created with the label up front for exactly this reason. If you must create a
> namespace inline, set the label at creation time.

`task -t Taskfile.test-infra.yml d1-mint-expired-secret` is a thin wrapper around
the script (defaults: `NAMESPACE=e2e-direct`, `SECRET=d1-expired-tls`,
`HOSTNAME=d1-bad.e2e.env.datum.net`).
