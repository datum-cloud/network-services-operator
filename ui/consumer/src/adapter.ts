import type {
  HTTPProxy,
  IPAMMode,
  IPFamily,
  LabelSelector,
  LabelSelectorOperator,
  Network,
  NetworkInterface,
  NetworkInterfacePhase,
  NetworkLoadBalancer,
  NetworkLoadBalancerListener,
  NetworkLoadBalancerRoute,
  NetworkReadyStatus,
  NetworkService,
  NlbProtocol,
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
  /** Set once deletion is requested; the object stays until its finalizers clear. */
  deletionTimestamp?: string;
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

/**
 * Networks still in the project. One being deleted is left out: it lingers
 * while its finalizer runs, and listing it would bring a just-deleted network
 * back until the next refresh after it is gone.
 */
export function toNetworkList(items: RawNetwork[]): Network[] {
  return items.filter((item) => !item.metadata?.deletionTimestamp).map(toNetwork);
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
    allocated: findCondition(conditions, 'Allocated')?.status === 'True',
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
    instanceIndex: labels['compute.datumapis.com/instance-index'],
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

interface RawNetworkLoadBalancerListener {
  name?: string;
  protocol?: string;
  port?: number;
  addressRefs?: string[];
}

interface RawNetworkLoadBalancerSpec {
  networkRef?: { name?: string };
  exposure?: { addresses?: { name?: string }[] };
  listeners?: RawNetworkLoadBalancerListener[];
}

interface RawNetworkLoadBalancerEndpointStatus {
  requestName?: string;
  location?: string;
  canonicalHostname?: string;
  addresses?: { family?: string; value?: string }[];
}

interface RawNetworkLoadBalancerStatus {
  conditions?: RawCondition[];
  endpoints?: RawNetworkLoadBalancerEndpointStatus[];
}

export interface RawNetworkLoadBalancer {
  metadata?: RawObjectMeta;
  spec?: RawNetworkLoadBalancerSpec;
  status?: RawNetworkLoadBalancerStatus;
}

export interface RawNetworkLoadBalancerList {
  items?: RawNetworkLoadBalancer[];
}

const NLB_PROTOCOLS: readonly NlbProtocol[] = ['TCP', 'UDP', 'TLS'];

function toNetworkLoadBalancerListeners(
  listeners: RawNetworkLoadBalancerListener[] | undefined
): NetworkLoadBalancerListener[] {
  return (listeners ?? []).flatMap((l, index) =>
    l.protocol && (NLB_PROTOCOLS as readonly string[]).includes(l.protocol)
      ? [
          {
            name: l.name ?? '',
            protocol: l.protocol as NlbProtocol,
            port: l.port ?? 0,
            addressRef: l.addressRefs?.[0],
            index,
          },
        ]
      : []
  );
}

export function toNetworkLoadBalancer(raw: RawNetworkLoadBalancer): NetworkLoadBalancer {
  const ready = findReadyCondition(raw.status?.conditions ?? []);
  const readyStatus: NetworkReadyStatus = ready?.status ?? 'Unknown';

  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    resourceVersion: raw.metadata?.resourceVersion,
    createdAt: raw.metadata?.creationTimestamp ? new Date(raw.metadata.creationTimestamp) : new Date(),
    networkName: raw.spec?.networkRef?.name,
    listeners: toNetworkLoadBalancerListeners(raw.spec?.listeners),
    addressNames: (raw.spec?.exposure?.addresses ?? []).map((a) => a.name ?? ''),
    endpoints: (raw.status?.endpoints ?? []).map((e) => ({
      requestName: e.requestName ?? '',
      location: e.location ?? '',
      hostname: e.canonicalHostname || undefined,
      addresses: (e.addresses ?? []).flatMap((a) =>
        a.value ? [{ family: a.family ?? '', value: a.value }] : []
      ),
    })),
    readyStatus,
    readyReason: ready?.reason,
    readyMessage: ready?.message,
  };
}

/** Same rule as toNetworkList: one being deleted is left out. */
export function toNetworkLoadBalancerList(items: RawNetworkLoadBalancer[]): NetworkLoadBalancer[] {
  return items.filter((item) => !item.metadata?.deletionTimestamp).map(toNetworkLoadBalancer);
}

interface RawNetworkLoadBalancerRouteParentRef {
  name?: string;
  listener?: string;
  port?: number;
}

interface RawNetworkLoadBalancerRouteSpec {
  parentRefs?: RawNetworkLoadBalancerRouteParentRef[];
  backends?: {
    networkService?: { name?: string; port?: string };
    healthCheck?: { protocol?: string; port?: string };
  }[];
}

interface RawNetworkLoadBalancerRouteParentStatus {
  name?: string;
  listener?: string;
  effective?: boolean;
  conditions?: RawCondition[];
}

export interface RawNetworkLoadBalancerRoute {
  metadata?: RawObjectMeta;
  spec?: RawNetworkLoadBalancerRouteSpec;
  status?: { parents?: RawNetworkLoadBalancerRouteParentStatus[] };
}

const ROUTE_PARENT_CONDITIONS = ['Accepted', 'ResolvedRefs', 'Programmed'];

function toRouteParentStatus(raw: RawNetworkLoadBalancerRouteParentStatus) {
  const conditions = raw.conditions ?? [];
  const failing = ROUTE_PARENT_CONDITIONS.map((type) => findCondition(conditions, type)).find(
    (c) => c?.status !== 'True'
  );
  return {
    name: raw.name ?? '',
    listener: raw.listener ?? '',
    effective: raw.effective ?? false,
    status: failing ? (failing.status ?? 'Unknown') : ('True' as const),
    reason: failing?.reason,
    message: failing?.message,
  };
}

export interface RawNetworkLoadBalancerRouteList {
  items?: RawNetworkLoadBalancerRoute[];
}

export function toNetworkLoadBalancerRoute(raw: RawNetworkLoadBalancerRoute): NetworkLoadBalancerRoute {
  const backendNames = (raw.spec?.backends ?? []).flatMap((b) =>
    b.networkService?.name ? [b.networkService.name] : []
  );
  return {
    uid: raw.metadata?.uid ?? '',
    name: raw.metadata?.name ?? '',
    resourceVersion: raw.metadata?.resourceVersion,
    parents: (raw.spec?.parentRefs ?? []).map((p) => ({ name: p.name ?? '', listener: p.listener, port: p.port })),
    backendNames: [...new Set(backendNames)],
    backends: (raw.spec?.backends ?? []).flatMap((b) =>
      b.networkService?.name
        ? [
            {
              name: b.networkService.name,
              port: b.networkService.port ?? '',
              healthCheck: b.healthCheck?.protocol
                ? { protocol: b.healthCheck.protocol, port: b.healthCheck.port }
                : undefined,
            },
          ]
        : []
    ),
    parentStatuses: (raw.status?.parents ?? []).map(toRouteParentStatus),
  };
}

/** Same rule as toNetworkList: one being deleted is left out. */
export function toNetworkLoadBalancerRouteList(items: RawNetworkLoadBalancerRoute[]): NetworkLoadBalancerRoute[] {
  return items.filter((item) => !item.metadata?.deletionTimestamp).map(toNetworkLoadBalancerRoute);
}
