# Physical attachment admission

Limits who may write a physical `NetworkInterfaceClaim` or
`NetworkInterface` in a cell, meaning one whose attachment mode is
`Physical`. The schemas already refuse an attachment point, purpose or
isolation on any other mode, and keep the mode from changing.

- Only a listed provider may create or change a physical claim.
- A listed realizer or break-glass operator may update a physical claim,
  such as to remove its finalizers, but not create one. The claim's schema
  already keeps its spec from changing.
- Only the realizer, or a break-glass operator, may remove the
  `networking.datumapis.com/physical-detach` finalizer.
- Only the operator may add that finalizer, or write a physical interface.
- A claim with `purpose: Boot` is created only in a namespace whose project,
  decoded from its `meta.datumapis.com/upstream-cluster-name` label, is a
  listed provider project. Later updates are not checked again, so changing
  the list never strands a boot claim that already exists.

The lists are the first five variables of the policy, each a CEL list of
strings, in this order:

| Index | Variable | Holds |
|---|---|---|
| 0 | `operators` | Usernames the operator authenticates as. Exempt from every rule. |
| 1 | `providers` | Usernames allowed to write physical claims. |
| 2 | `realizers` | Usernames of the physical realizer. |
| 3 | `breakGlassGroups` | Groups allowed to remove a stuck detach finalizer. |
| 4 | `providerProjects` | Project names whose namespaces may hold boot claims. |

The defaults name the cell's own service account as the operator and leave
every other list empty, so every physical claim is refused until a
deployment fills them in. A deployment replaces a list with a JSON patch:

```yaml
- op: replace
  path: /spec/variables/1/expression
  value: "['system:serviceaccount:example-provider:provider']"
```

The policy only matches physical claims and interfaces, and writes that add
or remove the detach finalizer, so a wrong list can only affect physical
attachment. Every other claim and interface is admitted as before. Within
physical attachment, a wrong `realizers` or `breakGlassGroups` list leaves
deleted claims waiting on their detach finalizer, with their addresses held.

Patches name the lists by index, so the order of the first five variables is
part of this component's contract. The operator's default entry is the cell's
own service account, and a test renders `config/cell` to keep the two in
step. A deployment that renames that service account must replace variable 0
too.

An operator in a break-glass group removes a detach finalizer only after
confirming on the switch that the port no longer carries the claim's
network. Anyone allowed to impersonate a listed username or group holds
the same power.
