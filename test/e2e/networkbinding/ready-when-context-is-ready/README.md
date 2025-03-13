# Test: `ready-when-context-is-ready`

*No description*

## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [Create Network](#step-Create Network) | 0 | 1 | 0 | 0 | 0 |
| 2 | [Create NetworkBinding](#step-Create NetworkBinding) | 0 | 3 | 0 | 0 | 0 |
| 3 | [Set NetworkContext Ready Condition to True](#step-Set NetworkContext Ready Condition to True) | 0 | 1 | 0 | 0 | 0 |
| 4 | [Assert NetworkBinding is Ready](#step-Assert NetworkBinding is Ready) | 0 | 1 | 0 | 0 | 0 |

### Step: `Create Network`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `Create NetworkBinding`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `Set NetworkContext Ready Condition to True`

Under normal operation, a plugin is expected to move the network context
to be ready once it has been programmed at the plugin's backend. We may
introduce a plugin that can be used during tests so that this won't be
necessary.

A direct kubectl command is used as Chainsaw purposefully does not
support updating subresources. See https://github.com/kyverno/chainsaw/issues/300
for more details.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `Assert NetworkBinding is Ready`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |

---

