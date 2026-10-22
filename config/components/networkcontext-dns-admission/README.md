# NetworkContext DNS admission

Refuses any create or update that changes `NetworkContext.spec.dns` unless the
request comes from a trusted username.

The trusted list names the manager's service account as `config/default`
renders it. A deployment must replace it with the identity the manager
authenticates as to each API where it writes NetworkContexts, such as a
project API reached through a kubeconfig. Otherwise every DNS write the
manager makes is refused and internal DNS never becomes ready.

Anyone allowed to impersonate a trusted username can also write `spec.dns`.
