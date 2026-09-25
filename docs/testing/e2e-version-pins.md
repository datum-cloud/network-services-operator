# e2e version pins

The prod-fidelity e2e env (`task test-infra:up`) stands up two Kind clusters — an
upstream control plane running the operator and a downstream cluster running the
Envoy Gateway data plane — to mirror the production edge. Some tool versions
there are pinned as compat workarounds (drop once upstream catches up); others
are deliberate prod-fidelity pins (drop only when production moves). Both are
tracked here so no pin lingers unexamined. See #278.

## Workaround pins — remove when the cause is gone

| Pin | Version | Where | Why | Unpin when |
| --- | --- | --- | --- | --- |
| helm | `v3.12.3` | `.github/workflows/test-e2e.yml:38` (setup-helm) | kustomize v5.5.0 probes helm with `helm version -c`; helm 3.13 dropped the `-c` shorthand, so latest helm breaks every `kustomize build --enable-helm` in `test-infra:up`. | kustomize bumps to a release that no longer issues the `-c` probe (or otherwise works with helm ≥3.13). Then drop `version:` and take latest helm. |
| kustomize | `v5.5.0` | `Makefile:191` (`KUSTOMIZE_VERSION`) | Pre-existing; the root cause of the helm pin above. | Bump to a release that fixes the helm probe, then re-run `task validate-kustomizations` and the e2e env. Unblocks removing the helm pin. |
| kind | `v0.32.0` | `Makefile:204` (`KIND_VERSION`) | `Taskfile.test-infra.yml:25` hardcodes `bin/kind-v0.32.0` to ship the prod node image; the Makefile pin was bumped from v0.27.0 so `make kind` builds that exact binary. The hardcoded path and the Makefile var can silently drift. | The Taskfile references the Makefile `KIND` var instead of the hardcoded `bin/kind-v0.32.0` path, so one pin governs both. Then this is just the normal kind pin. |

## Deliberate prod-fidelity pins — remove only on prod upgrade

| Pin | Version | Where | Why | Unpin when |
| --- | --- | --- | --- | --- |
| Envoy Gateway + SDK | `v1.7.4` + SDK `v1.8.1` | `Taskfile.test-infra.yml:40-41`, `config/tools/envoy-gateway-downstream/kustomization.yaml:9` | EG 1.8 does not program merged gateways served through the extension manager (waf-gw stays with empty status); 1.7.4 matches production. | Production moves off EG 1.7.4. |
| kindest/node | `v1.35.5` | `Taskfile.test-infra.yml:37` | Exact production edge node image. | Production Kubernetes upgrade. |
| Envoy proxy | `contrib-v1.39.1` | `config/e2e-downstream/envoyproxy.yaml:119` | Exact production edge proxy image. | Production edge Envoy image changes. |
| coraza-waf | `v2.0.4` | `Taskfile.test-infra.yml:44`, `config/e2e-downstream/envoyproxy.yaml:134` | Exact production WAF filter. | Production WAF image changes. |
