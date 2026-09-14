import type {
  HTTPProxy,
  IPAMMode,
  IPFamily,
  LabelSelector,
  LabelSelectorOperator,
  Network,
  NetworkInterface,
  NetworkInterfacePhase,
  NetworkReadyStatus,
  NetworkService,
  Subnet,
} from './schema';

interface RawCondition {
  type?: string;
  status?: 'True' | 'False' | 'Unknown';
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
  observedGeneration?: number;
}

interface RawObjectMeta {
  uid?: string;
  name?: string;
  namespace?: string;
  resourceVersion?: string;
  creationTimestamp?: string;
  labels?: Record<string, string>;
}

interface RawNetworkIPAM {
  mode?: string;
  ipv4Range?: string;
  ipv6Range?: string;
}

interface RawNetworkSpec {
  ipam?: RawNetworkIPAM;
  ipFamilies?: string[];
  mtu?: number;
}

interface RawNetworkIPAMStatus {
  ipv6Prefix?: string;
}

interface RawNetworkStatus {
  conditions?: RawCondition[];
  ipam?: RawNetworkIPAMStatus;
}

export interface RawNetwork {
  metadata?: RawObjectMeta;
  spec?: RawNetworkSpec;
  status?: RawNetworkStatus;
}

export interface RawNetworkList {
  items?: RawNetwork[];
}

const IP_FAMILIES: readonly IPFamily[] = ['IPv4', 'IPv6'];
const IPAM_MODES: readonly IPAMMode[] = ['Auto', 'Policy'];

function toIPFamilies(values: string[] | undefined): IPFamily[] {
  if (!values) return [];
  return values.filter((v): v is IPFamily => (IP_FAMILIES as readonly string[]).includes(v));
}

function toIPAMMode(value: string | undefined): IPAMMode | undefined {
  return value && (IPAM_MODES as readonly string[]).includes(value) ? (value as IPAMMode) : undefined;
}

function findReadyCondition(conditions: RawCondition[]): RawCondition | undefined {
  return conditions.find((c) => c.type === 'Ready');
}

export function toNetwork(raw: RawNetwork): Network {
  const conditions = raw.status?.conditions ?? [];
  const ready = findReadyCondition(conditions);
  const readyStatus: NetworkReadyStatus = ready?.status ?? 'Unknown';

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    namespace: raw.metadata?.namespace,
    resourceVersion: raw.metadata?.resourceVersion,
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    ipv6Prefix: raw.status?.ipam?.ipv6Prefix,
    readyStatus,
    readyReason: ready?.reason,
    readyMessage: ready?.message,
    ipFamilies: toIPFamilies(raw.spec?.ipFamilies),
    ipamMode: toIPAMMode(raw.spec?.ipam?.mode),
    mtu: raw.spec?.mtu,
    conditions: conditions.map((c) => ({
      type: c.type ?? '',
      status: c.status ?? 'Unknown',
      reason: c.reason,
      message: c.message,
      lastTransitionTime: c.lastTransitionTime,
      observedGeneration: c.observedGeneration,
    })),
  };
}

export function toNetworkList(items: RawNetwork[]): Network[] {
  return items.map(toNetwork);
}

interface RawSubnetSpec {
  location?: { name?: string };
  subnetClass?: string;
  ipFamily?: string;
  startAddress?: string;
  prefixLength?: number;
}

interface RawSubnetStatus {
  conditions?: RawCondition[];
  startAddress?: string;
  prefixLength?: number;
}

export interface RawSubnet {
  metadata?: RawObjectMeta;
  spec?: RawSubnetSpec;
  status?: RawSubnetStatus;
}

export interface RawSubnetList {
  items?: RawSubnet[];
}

export function toSubnet(raw: RawSubnet): Subnet {
  const conditions = raw.status?.conditions ?? [];
  const ready = findReadyCondition(conditions);
  const readyStatus: NetworkReadyStatus = ready?.status ?? 'Unknown';

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    location: raw.spec?.location?.name,
    subnetClass: raw.spec?.subnetClass,
    ipFamily: raw.spec?.ipFamily,
    startAddress: raw.status?.startAddress ?? raw.spec?.startAddress,
    prefixLength: raw.status?.prefixLength ?? raw.spec?.prefixLength,
    readyStatus,
    readyReason: ready?.reason,
    readyMessage: ready?.message,
  };
}

export function toSubnetList(items: RawSubnet[]): Subnet[] {
  return items.map(toSubnet);
}

interface RawNetworkInterfaceAddress {
  family?: string;
  address?: string;
  gateway?: string;
  primary?: boolean;
}

interface RawNetworkInterfaceSpec {
  network?: { name?: string };
  claimRef?: { name?: string };
  interfaceName?: string;
  attachmentMode?: string;
  addresses?: RawNetworkInterfaceAddress[];
}

interface RawNetworkInterfaceStatus {
  phase?: string;
  conditions?: RawCondition[];
}

export interface RawNetworkInterface {
  metadata?: RawObjectMeta;
  spec?: RawNetworkInterfaceSpec;
  status?: RawNetworkInterfaceStatus;
}

export interface RawNetworkInterfaceList {
  items?: RawNetworkInterface[];
}

const NETWORK_INTERFACE_PHASES: readonly NetworkInterfacePhase[] = ['Available', 'Bound'];

function toNetworkInterfacePhase(value: string | undefined): NetworkInterfacePhase | undefined {
  return value && (NETWORK_INTERFACE_PHASES as readonly string[]).includes(value)
    ? (value as NetworkInterfacePhase)
    : undefined;
}

function findCondition(conditions: RawCondition[], type: string): RawCondition | undefined {
  return conditions.find((c) => c.type === type);
}

export function toNetworkInterface(raw: RawNetworkInterface): NetworkInterface {
  const conditions = raw.status?.conditions ?? [];
  const holderAvailable = findCondition(conditions, 'HolderAvailable');
  const holderAvailableStatus: NetworkReadyStatus = holderAvailable?.status ?? 'Unknown';
  const labels = raw.metadata?.labels ?? {};

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    network: raw.spec?.network?.name,
    claimName: raw.spec?.claimRef?.name,
    interfaceName: raw.spec?.interfaceName,
    attachmentMode: raw.spec?.attachmentMode,
    workloadName: labels['compute.datumapis.com/workload-name'],
    location: labels['networking.datumapis.com/location'],
    labels,
    phase: toNetworkInterfacePhase(raw.status?.phase),
    addresses: (raw.spec?.addresses ?? []).map((a) => ({
      family: a.family,
      address: a.address,
      gateway: a.gateway,
      primary: a.primary,
    })),
    holderAvailableStatus,
    holderAvailableReason: holderAvailable?.reason,
    holderAvailableMessage: holderAvailable?.message,
  };
}

export function toNetworkInterfaceList(items: RawNetworkInterface[]): NetworkInterface[] {
  return items.map(toNetworkInterface);
}

interface RawNetworkServicePort {
  name?: string;
  port?: number;
  protocol?: string;
}

interface RawNetworkServiceLocationStatus {
  name?: string;
  members?: number;
  healthy?: number;
  serving?: boolean;
}

interface RawNetworkServiceSummary {
  locations?: number;
  members?: number;
  healthy?: number;
}

interface RawLabelSelectorRequirement {
  key?: string;
  operator?: string;
  values?: string[];
}

interface RawLabelSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: RawLabelSelectorRequirement[];
}

interface RawNetworkServiceSpec {
  networkInterfaces?: { selector?: RawLabelSelector };
  ports?: RawNetworkServicePort[];
}

const LABEL_SELECTOR_OPERATORS: readonly LabelSelectorOperator[] = ['In', 'NotIn', 'Exists', 'DoesNotExist'];

function toLabelSelector(raw: RawLabelSelector | undefined): LabelSelector {
  return {
    matchLabels: raw?.matchLabels ?? {},
    matchExpressions: (raw?.matchExpressions ?? []).flatMap((r) =>
      r.key && r.operator && (LABEL_SELECTOR_OPERATORS as readonly string[]).includes(r.operator)
        ? [{ key: r.key, operator: r.operator as LabelSelectorOperator, values: r.values ?? [] }]
        : []
    ),
  };
}

interface RawNetworkServiceStatus {
  summary?: RawNetworkServiceSummary;
  locations?: RawNetworkServiceLocationStatus[];
  conditions?: RawCondition[];
}

export interface RawNetworkService {
  metadata?: RawObjectMeta;
  spec?: RawNetworkServiceSpec;
  status?: RawNetworkServiceStatus;
}

export interface RawNetworkServiceList {
  items?: RawNetworkService[];
}

export function toNetworkService(raw: RawNetworkService): NetworkService {
  const conditions = raw.status?.conditions ?? [];
  const membersResolved = findCondition(conditions, 'MembersResolved');
  const ready = findCondition(conditions, 'Ready');
  const membersResolvedStatus: NetworkReadyStatus = membersResolved?.status ?? 'Unknown';
  const readyStatus: NetworkReadyStatus = ready?.status ?? 'Unknown';

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    networkInterfaceSelector: toLabelSelector(raw.spec?.networkInterfaces?.selector),
    ports: (raw.spec?.ports ?? []).map((p) => ({
      name: p.name ?? '',
      port: p.port ?? 0,
      protocol: p.protocol,
    })),
    summary: {
      locations: raw.status?.summary?.locations ?? 0,
      members: raw.status?.summary?.members ?? 0,
      healthy: raw.status?.summary?.healthy ?? 0,
    },
    locations: (raw.status?.locations ?? []).map((l) => ({
      name: l.name ?? '',
      members: l.members ?? 0,
      healthy: l.healthy ?? 0,
      serving: l.serving ?? false,
    })),
    membersResolvedStatus,
    membersResolvedReason: membersResolved?.reason,
    membersResolvedMessage: membersResolved?.message,
    readyStatus,
    readyReason: ready?.reason,
    readyMessage: ready?.message,
  };
}

export function toNetworkServiceList(items: RawNetworkService[]): NetworkService[] {
  return items.map(toNetworkService);
}

interface RawHTTPProxyBackend {
  networkService?: { name?: string; port?: string };
}

interface RawHTTPProxyRule {
  backends?: RawHTTPProxyBackend[];
}

interface RawHTTPProxySpec {
  rules?: RawHTTPProxyRule[];
}

interface RawHTTPProxyStatus {
  hostnames?: string[];
  canonicalHostname?: string;
  conditions?: RawCondition[];
}

export interface RawHTTPProxy {
  metadata?: RawObjectMeta;
  spec?: RawHTTPProxySpec;
  status?: RawHTTPProxyStatus;
}

export interface RawHTTPProxyList {
  items?: RawHTTPProxy[];
}

function toNetworkServiceNames(spec: RawHTTPProxySpec | undefined): string[] {
  const names = (spec?.rules ?? []).flatMap((rule) =>
    (rule.backends ?? []).flatMap((backend) => (backend.networkService?.name ? [backend.networkService.name] : []))
  );
  return [...new Set(names)];
}

export function toHTTPProxy(raw: RawHTTPProxy): HTTPProxy {
  const conditions = raw.status?.conditions ?? [];
  const programmed = findCondition(conditions, 'Programmed');
  const programmedStatus: NetworkReadyStatus = programmed?.status ?? 'Unknown';

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    hostnames: raw.status?.hostnames ?? [],
    canonicalHostname: raw.status?.canonicalHostname,
    networkServiceNames: toNetworkServiceNames(raw.spec),
    programmedStatus,
    programmedReason: programmed?.reason,
    programmedMessage: programmed?.message,
  };
}

export function toHTTPProxyList(items: RawHTTPProxy[]): HTTPProxy[] {
  return items.map(toHTTPProxy);
}
