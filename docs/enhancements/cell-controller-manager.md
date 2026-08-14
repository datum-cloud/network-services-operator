# Cell controller manager

Design for [#368](https://github.com/datum-cloud/network-services-operator/issues/368).

## Problem

`network-services manager` registers every controller and webhook wherever it starts. That is safe with one deployment. Fulfilling `NetworkInterfaceClaim`s at an edge needs a second, cell-resident deployment, and that deployment would also start the gateway, domain, and connector controllers and reconcile the same objects as the control plane.

## Design

A second command, `network-services cell-manager`, with its own config Kind.

```yaml
apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: CellControllerManager
ipam:
  kubeconfigPath: /etc/ipam-cluster/kubeconfig
location:
  name: us-central-1
  namespace: default
```

**A command per role, not a switch inside one command.** A selector inside a shared config makes every invariant conditional: the manager would validate gateway settings a cell never uses, and cell settings would be optional for the control plane. Splitting the command lets each config Kind require exactly what its role needs. Strict decoding then rejects a control-plane field in a cell config outright rather than ignoring it.

**The control-plane config is unchanged.** `NetworkServicesOperator` keeps `ipam` and `networkInterface` as deprecated no-op fields so a config written before the split still decodes. Nothing reads them.

**Leases are distinct by construction.** The manager keeps the historical `6a7d51cc.datumapis.com`; the cell takes `6a7d51cc.datumapis.com-cell`. Co-located deployments cannot park each other, and upgrading the control plane brings no lease rename.

### What a cell runs

`networkinterfaceclaim` and `networkinterface`. A cell needs no webhook serving cert, no downstream cluster, and no singleton manager, so it constructs none of them.

The boundary is what a controller *writes*, not what it reads. Gateway, domain, and connector controllers write resource templates that Karmada propagates to every edge, so a single writer is required. Network interface writes stay in the cell.

## Layout

- `internal/config/cell.go` — the `CellControllerManager` Kind and its validation.
- `internal/cmd/cell/` — the command, its controller registry, and its IPAM client factory.
- `internal/cmd/clusterdiscovery/` — cluster discovery, shared by both commands.
- `config/components/cell-controllers/` — the cell Deployment, its ConfigMap, and its metrics Service.
- `config/cell/` — the overlay a cell control plane applies.

`config/cell` sets no `namePrefix`. The cell's resource names are short enough to stay inside the 63-character limit on Service names, which the prefixed form exceeded.

The component gives the cell Deployment its own image name so the component's image transformer, which applies to the parent's whole accumulation, cannot rewrite the control-plane Deployment.

`location` carries placeholder values. Two cells sharing a location both fulfil and both release the same claims' addresses, and nothing detects it, so every consuming overlay must replace the whole ConfigMap.

## Testing

`TestControllerRegistrations_EveryControllerClassifiedExactlyOnce` scans `internal/controller` for reconcilers and asserts each is registered by exactly one command. A new controller that neither command runs fails the suite.

`test/e2e/cell-controller-manager` asserts the deployed cell serves no webhooks, mounts no downstream kubeconfig, and holds its own lease with a different holder than the control plane's.

## Open

`cell` holds only the two network interface controllers. The claim controller creates a `NetworkBinding` that only the control plane reconciles, so a standalone cell resolves no `NetworkContext`. `docs/enhancements/network-interfaces.md` places `NetworkContext` in the cell, so the network presence controllers likely belong here too.

The cell shares the control plane's `ClusterRole`, and its metrics Service has no `ServiceMonitor`.
