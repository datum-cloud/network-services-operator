# IPv4 for Network Load Balancers in the portal

Plan for [#951](https://github.com/datum-cloud/network-services-operator/issues/951).
Scope is the `ui/consumer` portal plugin only. Backend NLB and IPAM behaviour
do not change.

## Status

| Phase | Owner | What |
|---|---|---|
| 1 | done on this branch | Research, this plan, shared helpers in `src/lib/ipv4-quota.ts`, SDK 1.1.0 |
| 2A | parallel | Create form and add-listener family choice, disabled state, request action |
| 2B | parallel | Overview IPv4 address display, quota refusal on NLB pages |

## Quota

| Thing | Value |
|---|---|
| Resource type (quota metric) | `ipam.miloapis.com/public-ipv4claims` |
| Limit | `public-ipv4claims-per-project`, consumer `Project` |
| Default | 0 in production, 2 in staging (`infra/apps/ipam-system/*`) |
| Charged on | `IPClaim` create where `spec.ipFamily == 'IPv4'` and `spec.addressScope == 'Public'` (milo-os/ipam `service-catalog-public-ipv4`). Also counts against `ipam.miloapis.com/ipclaims`. |
| Who creates the claim | The NSO NLB controller, one `IPClaim` per address request × location × family |
| Granted by | Staff, with a `ResourceGrant` |
| Bucket | `AllowanceBucket` in `milo-system` of the project control plane, `spec.resourceType: ipam.miloapis.com/public-ipv4claims`, `status.{limit,allocated,available}` |

Each location an NLB address resolves to takes one public IPv4 claim. A
project with 1 available can still be refused in a second location. The
plugin can only check "at least one left" before creating, so a partial
refusal is handled after the fact by the status path (Task B).

### How the plugin reads it

`@datum-cloud/portal-plugin-sdk` 1.1.0, shared as a Module Federation singleton
(the host provides it; `vite.config.ts` `shared`), manifest `sdk.range: ^1.1.0`.

- `useResourceQuota({ resourceType, scope: 'project' })` returns a live
  `ResourceQuotaVerdict`. The host derives it from the project's allowance
  buckets (`cloud-portal` `plugin-sdk-bindings.tsx`, `derive-quota-verdict.ts`).
- `useRequestQuotaIncrease()` opens the portal's prefilled HelpScout quota
  request.

The SDK fails **open**: missing bucket, list error/403, or a `Feature`-type
registration all return `isUnknown: true, denied: false`. While loading it
returns `isLoading: true` and also `isUnknown: true`.

The plugin wraps both in `src/lib/ipv4-quota.ts`:

| Export | Use |
|---|---|
| `useIpv4Availability()` | Fail-closed verdict: `{ state, canChoose, disabledReason, limit, allocated, available }` |
| `deriveIpv4Availability(verdict)` | Pure mapping, for tests |
| `useRequestIpv4()` | `(nlbName?) => void`, opens the prefilled public IPv4 request |
| `buildIpv4QuotaIncreaseRequest(nlbName?)` | The `QuotaIncreaseRequest` it sends |
| `isIpv4QuotaRefusal(addressStatus)` | An NLB status address entry refused on quota |
| `isQuotaRefusalError(error)` / `isQuotaRefusalMessage(msg)` | A 403 from a write that carries Milo's quota wording |
| `IPV4_COPY` | All user-facing strings below |
| `PUBLIC_IPV4_RESOURCE_TYPE` | The resource type |

Test helper `src/test/portal-host.tsx`: `fakePortalHost(verdict)` returns a
`Wrapper` with real `PortalPluginHostProvider` and fake bindings, plus
`requestQuotaIncrease` / `useResourceQuota` spies, and `IPV4_VERDICTS` for every
bucket state. `nlb-list.test.tsx` and `nlb-detail.test.tsx` already render in it.
Any component that calls the hooks must be rendered inside it in tests, because
the SDK throws without a host provider.

## Fail-closed rules

IPv4 is offered only when a bucket exists and shows at least one address
available.

| Bucket state | SDK verdict | `state` | `canChoose` | UI |
|---|---|---|---|---|
| Loading | `isLoading` | `loading` | no | IPv4 option disabled, no explanation or action yet |
| Missing (no grant ever) | `isUnknown` | `unknown` | no | Disabled + `noAllocation` copy + paid add-on + request action |
| Unreadable (list error/403) | `isUnknown` | `unknown` | no | Same as missing |
| Known, counts absent | — | `unknown` | no | Same as missing |
| `limit` 0 | `denied` | `no-allocation` | no | Same as missing |
| `limit` > 0, `available` ≤ 0 | `denied` | `used-up` | no | Disabled + `usedUp` copy + paid add-on + request action |
| `available` ≥ 1 | — | `available` | yes | IPv4 offered, unchecked by default |

The API stays the final check: a claim refused despite a stale verdict shows
up through the refusal path below.

## UI behaviour per goal

### Choose IPv4 when creating (Task A)

- The create dialog gets an **Address families** field: IPv6 (always on, not
  removable, since the platform requires it) and IPv4.
- IPv4 is a checkbox, default off. Checked sends
  `exposure.addresses[0].families: ['IPv6', 'IPv4']`; unchecked stays
  `['IPv6']`.
- Enabled only when `canChoose`. If the verdict flips to not choosable while
  checked, the box unchecks and becomes disabled.

### Same rules when adding a listener (Task A)

- Add mode only. The new listener gets its own address, so the patch's
  `value.families` follows the same field.
- Edit mode keeps the existing address and shows no family field.

### No allocation or used up (Task A)

Under the disabled IPv4 option, in order:

1. `disabledReason` (`noAllocation` or `usedUp`)
2. `paidAddOn`
3. A link-style button `requestAction` that calls `useRequestIpv4()(nlbName?)`.
   In the create dialog `nlbName` is the typed name if valid, else omitted.

### Fail closed (Task A, via the shared hook)

Nothing beyond using `useIpv4Availability().canChoose`. Never gate on
`denied` alone.

### Show the address (Task B)

How IPv4 appears in status (`NetworkLoadBalancerStatus`):

```yaml
status:
  endpoints:
    - requestName: tcp-80          # = spec.exposure.addresses[].name = listener addressRef
      location: us-central-1
      canonicalHostname: us-central-1.<words>.<managedDomain>
      addresses:
        - family: IPv6
          value: 2001:db8::1
          conditions: [{ type: Allocated, status: "True" }, ...]
        - family: IPv4
          value: 203.0.113.7       # absent until allocated
          claimRef: { name: nlb-<uid>-tcp-80-<hash>, ... }
          conditions: [{ type: Allocated, status: "True" }, Programmed, Announced]
```

Today the adapter keeps only allocated addresses (`value` set) and the
overview treats `addresses[0]` as "the" address and hard-codes
`Family: IPv6` and "IPv4 coming soon".

Changes:

- Adapter/schema: keep each endpoint address's `family`, `value`, and the
  `Allocated` condition (`status`, `reason`, `message`), and the requested
  families per `spec.exposure.addresses[]`.
- Overview model: `ListenerRow` gets `ipv6Address`, `ipv4Address`,
  `ipv4Requested`, `ipv4Refused` (via `isIpv4QuotaRefusal`). Pick the address
  by family, never by position.
- `AddressCell`: hostname, then IPv6, then IPv4 under it, each copyable. IPv4
  requested but not allocated shows the pending skeleton; refused shows a short
  "IPv4 refused" badge with the refusal tooltip.
- `AddressesCard`: `Family` reads `IPv6` or `IPv6 + IPv4` (any listener
  requests IPv4). Remove "IPv4 coming soon".

### Clear errors (Task B)

How a refusal surfaces: the NLB write itself never hits the IPv4 quota. The
controller creates the `IPClaim`; Milo quota admission answers it with:

- HTTP **403**, `reason: Forbidden`
- message: `ipclaims.ipam.miloapis.com "<claim>" is forbidden: You've reached your quota for this resource type (<detail>). Delete unused resources to free up capacity, or contact support to request a higher limit.`

The controller records it on the address:
`status.endpoints[].addresses[family=IPv4].conditions[type=Allocated]` with
`status: False`, `reason: AllocationFailed`, `message:` the 403 text above.
The NLB-level `Allocated` condition is `False`/`AllocationPending` with
`<claim>: <same text>`. The controller retries with backoff, so a later grant
heals it without user action.

Detection: `isIpv4QuotaRefusal(address)` (IPv4, no value, `Allocated` not
True, message matches `reached your quota | quota exceeded | insufficient
quota`). The Milo message does not name the metric; IPv4 family plus quota
wording is the signal. A refusal on the general `ipclaims` metric would match
too, which is acceptable: it is still a quota refusal of the IPv4 address.

UI:

- NLB detail page: when any address is an IPv4 quota refusal, show a warning
  callout above the overview: `quotaRefused` copy, `paidAddOn`, and the
  `requestAction` button calling `useRequestIpv4()(nlb.name)`.
- Listener row: the "IPv4 refused" badge described above.
- `isQuotaRefusalError(error)` covers any write that returns a quota 403
  directly (not expected for IPv4 today). Task A uses it in the two dialogs to
  show `quotaRefused`-style copy with the request action instead of the raw
  message; Task B uses it for the detail page's mutation toasts.

## Copy

All in `IPV4_COPY` (`src/lib/ipv4-quota.ts`).

| Key | Text |
|---|---|
| `optionLabel` | IPv4 |
| `noAllocation` | This project doesn’t have a public IPv4 allocation. |
| `usedUp` | This project has used all of its public IPv4 addresses. |
| `paidAddOn` | Public IPv4 is a paid add-on. |
| `requestAction` | Request IPv4 address |
| `quotaRefused` | This load balancer couldn’t get a public IPv4 address because the project’s IPv4 allocation is used up. Its IPv6 address isn’t affected. |

Prefilled request: resource type `ipam.miloapis.com/public-ipv4claims`, scope
`project`, context `Public IPv4 address for network load balancer <name>.` or
`Public IPv4 address for a network load balancer.`

Do not show the SDK's `deniedReason`: there is no `ResourceRegistration`
display name visible to projects, so it renders the raw resource type.

**Follow-up:** pricing is not settled. The copy says "paid add-on" with no
price. Add the price (or a pricing link) to `paidAddOn` once the IPv4 billing
epic linked from datum-cloud/enhancements#955 lands.

## Work breakdown

Both tasks branch from `feat/951-nlb-ipv4-portal`. All paths are under
`ui/consumer/src/`. Neither task edits `lib/ipv4-quota.ts`,
`test/portal-host.tsx`, `package.json`, `vite.config.ts` or the manifest; ask
the coordinator if a shared change is needed.

### Task A: family choice, disabled state, request action

Owns:

- `components/nlb-create-form.ts`, `components/nlb-create-form.test.ts`
  (`ipv4: boolean` in `listenerFields`; `families` in `toNlbCreateRequests`
  and `toAddListenerRequests`; quota 403 in `toNlbCreateErrors`)
- `components/create-nlb-dialog.tsx`, `components/create-nlb-dialog.test.tsx`
- `components/add-listener-dialog.tsx`, `components/add-listener-dialog.test.tsx`
- new `components/ip-family-field.tsx`, `components/ip-family-field.test.tsx`
  (the IPv6/IPv4 field with disabled state, reason, paid add-on note and
  request action; takes an `Ipv4Availability` and an `onRequest`)

Tests: every row of the fail-closed table renders the right option state;
checked IPv4 sends both families on create and add; unchecked sends IPv6 only;
request action calls `requestQuotaIncrease` with the prefilled request; edit
mode shows no family field; dialog tests render in `fakePortalHost`.

### Task B: overview address display, quota refusal on NLB pages

Owns:

- `schema.ts`, `schema.test.ts`, `adapter.ts`, `adapter.test.ts` (NLB parts)
- `components/nlb-overview-model.ts`, `components/nlb-overview-model.test.ts`
- `components/nlb-overview.tsx` (+ new `components/nlb-overview.test.tsx` if needed)
- `components/nlb-table.tsx`, `components/nlb-table.test.tsx` (if the list shows IPv4)
- `pages/nlb-detail.tsx`, `pages/nlb-detail.test.tsx`
- `pages/nlb-list.tsx`, `pages/nlb-list.test.tsx`
- new `components/ipv4-quota-callout.tsx` (+ test)

Tests: IPv4 address shown next to IPv6 per listener; pending IPv4 shows
skeleton; refused IPv4 shows badge and the page callout; request action
prefills with the NLB name; IPv6-only NLBs render as today; address picked by
family regardless of order.

### Seams

- Task A only reads `NetworkLoadBalancer.name` from the schema; Task B may add
  fields but must not rename or remove existing ones.
- `nlb-detail.tsx` renders `AddListenerDialog` with today's props; Task A must
  not change its props.
- `nlb-list.tsx` renders `CreateNlbDialog` with today's props; same rule.

## Test plan

- Unit (done): `lib/ipv4-quota.test.tsx` covers loading, missing, unreadable,
  counts absent, 0 allowed, used up, available, the prefilled request, and
  refusal detection.
- Unit (2A/2B): as listed per task.
- `npm run typecheck`, `npm test`, `npm run build` in `ui/consumer`.
- Manual in a portal running cloud-portal ≥ v0.26.0, against a project with a
  `public-ipv4claims` grant: create with IPv4, see both addresses on the
  overview; add a listener with IPv4; in a project without a grant, see the
  disabled option and open the prefilled request; exhaust the grant (backends
  in two locations with limit 1) and see the refusal callout.

## Real data

Read-only `datumctl` against production:

- `personal-project-44245c5a` was not visible to the authenticated session
  (`projects.resourcemanager.miloapis.com "personal-project-44245c5a" not found`).
- `personal-project-efb26a2b` (the user's personal project) has a granted
  bucket: `resourceType: ipam.miloapis.com/public-ipv4claims`, `limit: 1`,
  `allocated: 0`, `available: 1`, two contributing grants (1 and 0). It has no
  NLBs, so the IPv4 status shape above comes from the API types and
  controller, not a live object.
- No `ResourceRegistration` is listable from the project, which is why the
  SDK's `deniedReason` would show the raw resource type.

## Open questions

1. Should edit mode let a user add IPv4 to an existing listener's address
   (patch `families`)? This plan keeps it out.
2. Should the create form warn that each location takes one IPv4 address?
3. Pricing copy and link (follow-up above).
4. Which project did the brief mean by `personal-project-44245c5a`? It may be
   in another environment.
