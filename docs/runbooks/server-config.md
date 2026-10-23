# Runbook: Server config alerts

The manager and the cell controller manager read their server config file at
start, then check it every 10 seconds. When the file holds a config that loads
and differs from the running one, the process stops cleanly and the container
restarts on the new config.

A change that cannot be loaded is not applied. The process logs the error,
keeps running on the config it started with, and sets
`nso_server_config_rejected` to 1 until the file is fixed or reverted.

## ServerConfigRejected

**Meaning (warning).** For 10 minutes, the pod could not read its config file,
or the file held a config that fails to decode or validate, or names a
kubeconfig or trusted roots file that cannot be loaded.

**Impact.** The config change has not taken effect. The pod is healthy on its
old config, but the next restart (a node drain, an eviction, a rollout) loads
the broken config and crash-loops.

**Diagnose.** Find the reason in the pod's logs:

```sh
kubectl -n <namespace> logs <pod> | grep 'keeping the running config'
```

**Remediate.** Fix or revert the ConfigMap, or the Secret it names. The pod
picks up a fixed config within about a minute and restarts once. The alert
clears when the file loads again.
