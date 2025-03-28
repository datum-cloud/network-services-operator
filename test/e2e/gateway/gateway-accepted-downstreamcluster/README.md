# Test: `gateway-accepted`

*No description*

## Bindings

| # | Name | Value |
|:-:|---|---|
| 1 | `clusterIssuerName` | "(join('-', ['e2e', $namespace]))" |
| 2 | `gatewayClassName` | "(join('-', ['e2e', $namespace]))" |
| 3 | `downstreamNamespaceName` | "tbd" |
| 4 | `KUBECONFIG` | "asdf" |

## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [Create CA](#step-Create CA) | 0 | 1 | 0 | 0 | 0 |
| 2 | [Create GatewayClass for the upstream gateways](#step-Create GatewayClass for the upstream gateways) | 0 | 1 | 0 | 0 | 0 |
| 3 | [Create GatewayClass for the downstream gateways](#step-Create GatewayClass for the downstream gateways) | 0 | 2 | 0 | 0 | 0 |
| 4 | [Provision Gateway](#step-Provision Gateway) | 0 | 6 | 2 | 0 | 0 |
| 5 | [Provision HTTPRoute](#step-Provision HTTPRoute) | 0 | 7 | 2 | 0 | 0 |
| 6 | [Provision Pod to test connectivity](#step-Provision Pod to test connectivity) | 0 | 5 | 1 | 0 | 0 |

### Step: `Create CA`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `Create GatewayClass for the upstream gateways`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `Create GatewayClass for the downstream gateways`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `create` | 0 | 0 | *No description* |

### Step: `Provision Gateway`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 1 | *No description* |
| 2 | `create` | 0 | 1 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `script` | 0 | 3 | *No description* |
| 5 | `assert` | 3 | 0 | *No description* |
| 6 | `assert` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `Provision HTTPRoute`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 1 | *No description* |
| 2 | `create` | 0 | 0 | *No description* |
| 3 | `create` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |
| 5 | `assert` | 0 | 0 | *No description* |
| 6 | `script` | 0 | 0 | *No description* |
| 7 | `assert` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `Provision Pod to test connectivity`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 1 | *No description* |
| 2 | `script` | 0 | 1 | *No description* |
| 3 | `create` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |
| 5 | `script` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

---

