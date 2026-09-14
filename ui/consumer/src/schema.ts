import { z } from 'zod';

// ── Network ──────────────────────────────────────────────────────────────

export type NetworkReadyStatus = 'True' | 'False' | 'Unknown';

const networkConditionSchema = z.object({
  type: z.string(),
  status: z.enum(['True', 'False', 'Unknown']),
  reason: z.string().optional(),
  message: z.string().optional(),
  lastTransitionTime: z.string().optional(),
  observedGeneration: z.number().optional(),
});

export type NetworkCondition = z.infer<typeof networkConditionSchema>;

export const ipFamilySchema = z.enum(['IPv4', 'IPv6']);
export type IPFamily = z.infer<typeof ipFamilySchema>;

export const ipamModeSchema = z.enum(['Auto', 'Policy']);
export type IPAMMode = z.infer<typeof ipamModeSchema>;

export const networkResourceSchema = z.object({
  uid: z.string(),
  name: z.string(),
  namespace: z.string().optional(),
  resourceVersion: z.string().optional(),
  createdAt: z.coerce.date(),
  /** IPv6 /48 assigned to the network from the platform's tenant pool, when IPAM has allocated one. */
  ipv6Prefix: z.string().optional(),
  /** Ready condition status, verbatim from status.conditions — 'Unknown' when the condition is absent. */
  readyStatus: z.enum(['True', 'False', 'Unknown']),
  /** Ready condition reason, e.g. "Ready", "IPv6Required", "RangeOccupied" — raw controller reason string. */
  readyReason: z.string().optional(),
  /** Ready condition message — human-authored by the controller, may still be technical. */
  readyMessage: z.string().optional(),
  ipFamilies: z.array(ipFamilySchema).default([]),
  ipamMode: ipamModeSchema.optional(),
  mtu: z.number().optional(),
  conditions: z.array(networkConditionSchema).default([]),
});

export type Network = z.infer<typeof networkResourceSchema>;

export const networkListSchema = z.object({
  items: z.array(networkResourceSchema),
});

export type NetworkList = z.infer<typeof networkListSchema>;

export type BadgeType = 'success' | 'warning' | 'danger' | 'muted';

/**
 * Plain-language copy for known Ready condition reasons — the portal never
 * shows a raw controller reason/condition string to a customer. Sourced from
 * `internal/controller/network_controller.go`'s condition-setting code in
 * this repo; falls back to the raw reason for anything unmapped.
 */
const READY_REASON_COPY: Record<string, string> = {
  IPv6Required: 'IPv6 required',
  ProjectNamespaceNotFound: 'Project namespace not found',
  ProjectUnresolved: 'Project could not be resolved',
  RangeOccupied: 'Address range still in use',
  RangeUnsupported: 'Address range not supported',
};

/** Maps a Network's Ready reason to short, human copy for the table's Ready badge. */
export function readyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return READY_REASON_COPY[reason] ?? reason;
}

/**
 * Maps a Network's Ready condition status to a `Badge` `type` prop understood
 * by `@datum-cloud/datum-ui/badge`.
 */
export function readyStatusToBadgeType(status: NetworkReadyStatus): BadgeType {
  switch (status) {
    case 'True':
      return 'success';
    case 'False':
      return 'danger';
    default:
      return 'muted';
  }
}

/** Label for the Ready badge: "Ready" when true, plain-language reason when false, "Unknown" otherwise. */
export function readyStatusToLabel(status: NetworkReadyStatus, reason: string | undefined): string {
  if (status === 'True') return 'Ready';
  if (status === 'False') return readyReasonToLabel(reason);
  return 'Unknown';
}

// ── Subnet ───────────────────────────────────────────────────────────────

export const subnetResourceSchema = z.object({
  uid: z.string(),
  name: z.string(),
  createdAt: z.coerce.date(),
  location: z.string().optional(),
  subnetClass: z.string().optional(),
  ipFamily: z.string().optional(),
  startAddress: z.string().optional(),
  prefixLength: z.number().optional(),
  readyStatus: z.enum(['True', 'False', 'Unknown']),
  readyReason: z.string().optional(),
  readyMessage: z.string().optional(),
});

export type Subnet = z.infer<typeof subnetResourceSchema>;

export const subnetListSchema = z.object({
  items: z.array(subnetResourceSchema),
});

export type SubnetList = z.infer<typeof subnetListSchema>;

const SUBNET_READY_REASON_COPY: Record<string, string> = {
  NotProgrammed: 'Not yet programmed',
  ProgrammingInProgress: 'Programming in progress',
};

/** Maps a Subnet's Ready reason to short, human copy, matching readyReasonToLabel's convention for Network. */
export function subnetReadyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return SUBNET_READY_REASON_COPY[reason] ?? reason;
}

/** CIDR-style display for a subnet's address block, e.g. "fd20:0:2::/64". */
export function subnetCidr(subnet: Subnet): string | undefined {
  if (!subnet.startAddress || subnet.prefixLength === undefined) return undefined;
  return `${subnet.startAddress}/${subnet.prefixLength}`;
}

// ── NetworkInterface ─────────────────────────────────────────────────────

export const networkInterfacePhaseSchema = z.enum(['Available', 'Bound']);
export type NetworkInterfacePhase = z.infer<typeof networkInterfacePhaseSchema>;

const networkInterfaceAddressSchema = z.object({
  family: z.string().optional(),
  address: z.string().optional(),
  gateway: z.string().optional(),
  primary: z.boolean().optional(),
});

export type NetworkInterfaceAddress = z.infer<typeof networkInterfaceAddressSchema>;

export const networkInterfaceResourceSchema = z.object({
  uid: z.string(),
  name: z.string(),
  createdAt: z.coerce.date(),
  network: z.string().optional(),
  claimName: z.string().optional(),
  interfaceName: z.string().optional(),
  attachmentMode: z.string().optional(),
  /** From the compute.datumapis.com/workload-name label — the workload this interface belongs to, when set by the holder. */
  workloadName: z.string().optional(),
  /** From the networking.datumapis.com/location label. */
  location: z.string().optional(),
  /**
   * The interface's full label set, verbatim — needed to evaluate a
   * NetworkService's networkInterfaceSelector against it (see
   * matchesLabelSelector), since that selector can reach any label, not just
   * the ones broken out above.
   */
  labels: z.record(z.string(), z.string()).default({}),
  phase: networkInterfacePhaseSchema.optional(),
  addresses: z.array(networkInterfaceAddressSchema).default([]),
  /** HolderAvailable condition status — whatever holds the interface reporting itself able to serve. There is no Ready condition on NetworkInterface. */
  holderAvailableStatus: z.enum(['True', 'False', 'Unknown']),
  holderAvailableReason: z.string().optional(),
  holderAvailableMessage: z.string().optional(),
});

export type NetworkInterface = z.infer<typeof networkInterfaceResourceSchema>;

export const networkInterfaceListSchema = z.object({
  items: z.array(networkInterfaceResourceSchema),
});

export type NetworkInterfaceList = z.infer<typeof networkInterfaceListSchema>;

const HOLDER_AVAILABLE_REASON_COPY: Record<string, string> = {
  HolderUnavailable: 'Not serving',
  HolderReleased: 'Holder released',
};

/**
 * HolderAvailable's reason is written by whatever holds the interface (a
 * workload, today), not by this operator — so unlike other reason fields in
 * this file, it isn't a fixed enum we can fully map. PascalCase reasons like
 * "ImageUnavailable" are split into words and lowercased instead of shown
 * verbatim.
 */
function humanizeReason(reason: string): string {
  const spaced = reason.replace(/([a-z0-9])([A-Z])/g, '$1 $2');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1).toLowerCase();
}

/** Maps a NetworkInterface's HolderAvailable reason to short, human copy. */
export function holderAvailableReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not serving';
  return HOLDER_AVAILABLE_REASON_COPY[reason] ?? humanizeReason(reason);
}

/** Label for the Serving badge: "Serving" when true, plain-language reason when false, "Unknown" otherwise. */
export function holderAvailableStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Serving';
  if (status === 'False') return holderAvailableReasonToLabel(reason);
  return 'Unknown';
}

/** The address to show as an interface's primary address: the one marked primary, or the first one. */
export function networkInterfacePrimaryAddress(iface: NetworkInterface): string | undefined {
  const primary = iface.addresses.find((a) => a.primary) ?? iface.addresses[0];
  return primary?.address;
}

// ── NetworkService ───────────────────────────────────────────────────────
//
// From an unmerged prototype (datum-cloud/network-services-operator#411,
// branch proto/network-service) that staging currently runs, so it's live
// even though it isn't in this repo's main branch. A service's membership is
// resolved from a label selector against NetworkInterfaces; the resolved
// network is never written back onto the service, so there is no way to
// scope a list of services to one network — see useNetworkServices. The
// selector itself is exposed as networkInterfaceSelector below so a caller
// that already has a network's NetworkInterfaces (with their labels) can
// evaluate the match itself — see matchesLabelSelector and its use in
// network-topology.tsx.

export const labelSelectorOperatorSchema = z.enum(['In', 'NotIn', 'Exists', 'DoesNotExist']);
export type LabelSelectorOperator = z.infer<typeof labelSelectorOperatorSchema>;

export const labelSelectorRequirementSchema = z.object({
  key: z.string(),
  operator: labelSelectorOperatorSchema,
  values: z.array(z.string()).default([]),
});

export type LabelSelectorRequirement = z.infer<typeof labelSelectorRequirementSchema>;

/** A Kubernetes-style label selector, matched against a NetworkInterface's labels by matchesLabelSelector. */
export const labelSelectorSchema = z.object({
  matchLabels: z.record(z.string(), z.string()).default({}),
  matchExpressions: z.array(labelSelectorRequirementSchema).default([]),
});

export type LabelSelector = z.infer<typeof labelSelectorSchema>;

/**
 * Evaluates a Kubernetes-style label selector against a label set, the same
 * semantics the API server uses for matchLabels/matchExpressions. A selector
 * with no clauses matches everything — the CRD requires at least one clause,
 * so this only comes up for malformed/missing data, and matching everything
 * is the same permissive default zod applies elsewhere in this file.
 */
export function matchesLabelSelector(labels: Record<string, string>, selector: LabelSelector): boolean {
  for (const [key, value] of Object.entries(selector.matchLabels)) {
    if (labels[key] !== value) return false;
  }
  for (const requirement of selector.matchExpressions) {
    const value = labels[requirement.key];
    switch (requirement.operator) {
      case 'In':
        if (value === undefined || !requirement.values.includes(value)) return false;
        break;
      case 'NotIn':
        if (value !== undefined && requirement.values.includes(value)) return false;
        break;
      case 'Exists':
        if (value === undefined) return false;
        break;
      case 'DoesNotExist':
        if (value !== undefined) return false;
        break;
    }
  }
  return true;
}

export const networkServicePortSchema = z.object({
  name: z.string(),
  port: z.number(),
  protocol: z.string().optional(),
});

export type NetworkServicePort = z.infer<typeof networkServicePortSchema>;

export const networkServiceLocationStatusSchema = z.object({
  name: z.string(),
  members: z.number(),
  healthy: z.number(),
  serving: z.boolean(),
});

export type NetworkServiceLocationStatus = z.infer<typeof networkServiceLocationStatusSchema>;

export const networkServiceSummarySchema = z.object({
  locations: z.number().default(0),
  members: z.number().default(0),
  healthy: z.number().default(0),
});

export type NetworkServiceSummary = z.infer<typeof networkServiceSummarySchema>;

export const networkServiceResourceSchema = z.object({
  uid: z.string(),
  name: z.string(),
  createdAt: z.coerce.date(),
  /** spec.networkInterfaces.selector — see matchesLabelSelector for how this decides which network a service belongs to. */
  networkInterfaceSelector: labelSelectorSchema,
  ports: z.array(networkServicePortSchema).default([]),
  summary: networkServiceSummarySchema,
  locations: z.array(networkServiceLocationStatusSchema).default([]),
  membersResolvedStatus: z.enum(['True', 'False', 'Unknown']),
  membersResolvedReason: z.string().optional(),
  membersResolvedMessage: z.string().optional(),
  readyStatus: z.enum(['True', 'False', 'Unknown']),
  readyReason: z.string().optional(),
  readyMessage: z.string().optional(),
});

export type NetworkService = z.infer<typeof networkServiceResourceSchema>;

export const networkServiceListSchema = z.object({
  items: z.array(networkServiceResourceSchema),
});

export type NetworkServiceList = z.infer<typeof networkServiceListSchema>;

const NETWORK_SERVICE_MEMBERS_RESOLVED_REASON_COPY: Record<string, string> = {
  NoMatchingInterfaces: 'No matching interfaces yet',
  MultipleNetworks: 'Selector matches interfaces on more than one network',
};

/** Maps a NetworkService's MembersResolved reason to short, human copy. */
export function networkServiceMembersResolvedReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not resolved';
  return NETWORK_SERVICE_MEMBERS_RESOLVED_REASON_COPY[reason] ?? reason;
}

/** Label for the MembersResolved badge: "Resolved" when true, plain-language reason when false, "Unknown" otherwise. */
export function networkServiceMembersResolvedStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Resolved';
  if (status === 'False') return networkServiceMembersResolvedReasonToLabel(reason);
  return 'Unknown';
}

const NETWORK_SERVICE_READY_REASON_COPY: Record<string, string> = {
  NoServingLocations: 'No location is taking traffic',
};

/** Maps a NetworkService's Ready reason to short, human copy. */
export function networkServiceReadyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return NETWORK_SERVICE_READY_REASON_COPY[reason] ?? reason;
}

/** Label for the Ready badge: "Ready" when true, plain-language reason when false, "Unknown" otherwise. */
export function networkServiceReadyStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Ready';
  if (status === 'False') return networkServiceReadyReasonToLabel(reason);
  return 'Unknown';
}

// ── HTTPProxy ────────────────────────────────────────────────────────────
//
// The customer-facing edge for a network: an HTTPProxy has no field
// referencing the Network(s) its backends live on (see useHTTPProxies), so
// the topology diagram instead looks at each proxy's `networkServiceNames` —
// the NetworkServices named by its rules' `networkService` backends (see
// api/v1alpha's NetworkServiceBackendRef, from the same unmerged
// proto/network-service prototype NetworkService itself comes from). A proxy
// only counts as fronting a given network once one of those NetworkServices
// is confirmed to resolve to that network's own NetworkInterfaces — see
// matchesLabelSelector and its use in network-topology.tsx.

export const httpProxyResourceSchema = z.object({
  uid: z.string(),
  name: z.string(),
  createdAt: z.coerce.date(),
  hostnames: z.array(z.string()).default([]),
  canonicalHostname: z.string().optional(),
  networkServiceNames: z.array(z.string()).default([]),
  programmedStatus: z.enum(['True', 'False', 'Unknown']),
  programmedReason: z.string().optional(),
  programmedMessage: z.string().optional(),
});

export type HTTPProxy = z.infer<typeof httpProxyResourceSchema>;

export const httpProxyListSchema = z.object({
  items: z.array(httpProxyResourceSchema),
});

export type HTTPProxyList = z.infer<typeof httpProxyListSchema>;

const HTTPPROXY_PROGRAMMED_REASON_COPY: Record<string, string> = {
  Pending: 'Waiting for controller',
  Invalid: 'Configuration invalid',
  Conflict: 'Conflict while programming',
  InstanceBackendNotFound: 'Backend not found',
};

/** Maps an HTTPProxy's Programmed reason to short, human copy. */
export function httpProxyProgrammedReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not programmed';
  return HTTPPROXY_PROGRAMMED_REASON_COPY[reason] ?? reason;
}

/** Label for the Programmed badge: "Programmed" when true, plain-language reason when false, "Unknown" otherwise. */
export function httpProxyProgrammedStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Programmed';
  if (status === 'False') return httpProxyProgrammedReasonToLabel(reason);
  return 'Unknown';
}
