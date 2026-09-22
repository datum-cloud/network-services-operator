import { z } from 'zod';

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
  ipv6Prefix: z.string().optional(),
  readyStatus: z.enum(['True', 'False', 'Unknown']),
  readyReason: z.string().optional(),
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

const READY_REASON_COPY: Record<string, string> = {
  IPv6Required: 'IPv6 required',
  ProjectNamespaceNotFound: 'Project namespace not found',
  ProjectUnresolved: 'Project could not be resolved',
  RangeOccupied: 'Address range still in use',
  RangeUnsupported: 'Address range not supported',
};

export function readyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return READY_REASON_COPY[reason] ?? reason;
}

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

export function readyStatusToLabel(status: NetworkReadyStatus, reason: string | undefined): string {
  if (status === 'True') return 'Ready';
  if (status === 'False') return readyReasonToLabel(reason);
  return 'Unknown';
}

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

export function subnetReadyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return SUBNET_READY_REASON_COPY[reason] ?? reason;
}

export function subnetCidr(subnet: Subnet): string | undefined {
  if (!subnet.startAddress || subnet.prefixLength === undefined) return undefined;
  return `${subnet.startAddress}/${subnet.prefixLength}`;
}

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
  workloadName: z.string().optional(),
  location: z.string().optional(),
  labels: z.record(z.string(), z.string()).default({}),
  phase: networkInterfacePhaseSchema.optional(),
  addresses: z.array(networkInterfaceAddressSchema).default([]),
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

// HolderAvailable's reason is written by whatever holds the interface, not
// this operator, so it isn't a fixed enum to map. Split PascalCase into words
// instead of showing it verbatim.
function humanizeReason(reason: string): string {
  const spaced = reason.replace(/([a-z0-9])([A-Z])/g, '$1 $2');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1).toLowerCase();
}

export function holderAvailableReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not serving';
  return HOLDER_AVAILABLE_REASON_COPY[reason] ?? humanizeReason(reason);
}

export function holderAvailableStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Serving';
  if (status === 'False') return holderAvailableReasonToLabel(reason);
  return 'Unknown';
}

export function networkInterfacePrimaryAddress(iface: NetworkInterface): string | undefined {
  const primary = iface.addresses.find((a) => a.primary) ?? iface.addresses[0];
  return primary?.address;
}

export const labelSelectorOperatorSchema = z.enum(['In', 'NotIn', 'Exists', 'DoesNotExist']);
export type LabelSelectorOperator = z.infer<typeof labelSelectorOperatorSchema>;

export const labelSelectorRequirementSchema = z.object({
  key: z.string(),
  operator: labelSelectorOperatorSchema,
  values: z.array(z.string()).default([]),
});

export type LabelSelectorRequirement = z.infer<typeof labelSelectorRequirementSchema>;

export const labelSelectorSchema = z.object({
  matchLabels: z.record(z.string(), z.string()).default({}),
  matchExpressions: z.array(labelSelectorRequirementSchema).default([]),
});

export type LabelSelector = z.infer<typeof labelSelectorSchema>;

// A selector with no clauses matches everything: the CRD requires at least
// one clause, so this only comes up for malformed or missing data.
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

export function networkServiceMembersResolvedReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not resolved';
  return NETWORK_SERVICE_MEMBERS_RESOLVED_REASON_COPY[reason] ?? reason;
}

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

export function networkServiceReadyReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not ready';
  return NETWORK_SERVICE_READY_REASON_COPY[reason] ?? reason;
}

export function networkServiceReadyStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Ready';
  if (status === 'False') return networkServiceReadyReasonToLabel(reason);
  return 'Unknown';
}

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

export function httpProxyProgrammedReasonToLabel(reason: string | undefined): string {
  if (!reason) return 'Not programmed';
  return HTTPPROXY_PROGRAMMED_REASON_COPY[reason] ?? reason;
}

export function httpProxyProgrammedStatusToLabel(
  status: NetworkReadyStatus,
  reason: string | undefined
): string {
  if (status === 'True') return 'Programmed';
  if (status === 'False') return httpProxyProgrammedReasonToLabel(reason);
  return 'Unknown';
}
