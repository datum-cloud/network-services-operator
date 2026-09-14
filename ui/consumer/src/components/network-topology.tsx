import { useHTTPProxies, useNetworkInterfaces, useNetworkServices, useSubnets } from '../lib/api';
import {
  holderAvailableStatusToLabel,
  httpProxyProgrammedStatusToLabel,
  matchesLabelSelector,
  networkInterfacePrimaryAddress,
  readyStatusToBadgeType,
} from '../schema';
import type { HTTPProxy, NetworkInterface, NetworkReadyStatus, NetworkService, Subnet } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Button } from '@datum-cloud/datum-ui/button';
import { useCopyToClipboard } from '@datum-cloud/datum-ui/hooks';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Popover, PopoverContent, PopoverTrigger } from '@datum-cloud/datum-ui/popover';
import {
  BoxIcon,
  CheckIcon,
  CopyIcon,
  GlobeIcon,
  MapPinIcon,
  NetworkIcon,
  RotateCcwIcon,
  RouterIcon,
  ZoomInIcon,
  ZoomOutIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

const WIDTH = 960;
const HEIGHT = 380;
const CENTER = { x: 380, y: HEIGHT / 2 };
// An ellipse, not a circle: a wide fan reads better here, but applying a
// large radius to sin() at a wide angle would push nodes past the top/bottom
// edge, so the vertical radius is bounded separately.
const LOCATION_RADIUS = { x: 220, y: 95 };
const WORKLOAD_RADIUS = { x: 160, y: 80 };
const MAX_LOCATION_ARC_DEGREES = 140;
const LOCATION_ARC_STEP_DEGREES = 40;
const MAX_WORKLOAD_ARC_DEGREES = 150;
const WORKLOAD_ARC_STEP_DEGREES = 45;

const GATEWAY_RADIUS = { x: 180, y: 100 };
const GATEWAY_BASE_ANGLE = 180;
const MAX_GATEWAY_ARC_DEGREES = 100;
const GATEWAY_ARC_STEP_DEGREES = 45;
const INTERNET_POSITION = { x: 60, y: HEIGHT / 2 };

const MIN_ZOOM = 0.5;
const MAX_ZOOM = 2.5;
const ZOOM_STEP = 0.2;

function clampZoom(zoom: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
}

const STATUS_COLOR: Record<NetworkReadyStatus, string> = {
  True: 'var(--color-badge-success)',
  False: 'var(--color-badge-danger)',
  Unknown: 'var(--color-badge-muted)',
};

function toRadians(degrees: number): number {
  return (degrees * Math.PI) / 180;
}

function pointOnEllipse(
  center: { x: number; y: number },
  radius: { x: number; y: number },
  angleDegrees: number
) {
  const angle = toRadians(angleDegrees);
  return { x: center.x + radius.x * Math.cos(angle), y: center.y + radius.y * Math.sin(angle) };
}

interface LocationNode {
  subnet: Subnet;
  position: { x: number; y: number };
  workloads: { iface: NetworkInterface; position: { x: number; y: number } }[];
}

export function layout(subnets: Subnet[], interfaces: NetworkInterface[]): LocationNode[] {
  const arcSpan = subnets.length <= 1 ? 0 : Math.min(MAX_LOCATION_ARC_DEGREES, LOCATION_ARC_STEP_DEGREES * (subnets.length - 1));
  const angleStep = subnets.length <= 1 ? 0 : arcSpan / (subnets.length - 1);
  const startAngle = -arcSpan / 2;

  return subnets.map((subnet, i) => {
    const angle = startAngle + i * angleStep;
    const position = pointOnEllipse(CENTER, LOCATION_RADIUS, angle);
    const atLocation = interfaces.filter((iface) => iface.location === subnet.location);

    const workloadSpread =
      atLocation.length <= 1
        ? 0
        : Math.min(MAX_WORKLOAD_ARC_DEGREES, WORKLOAD_ARC_STEP_DEGREES * (atLocation.length - 1));
    const spreadStart = angle - workloadSpread / 2;
    const spreadStep = atLocation.length > 1 ? workloadSpread / (atLocation.length - 1) : 0;

    return {
      subnet,
      position,
      workloads: atLocation.map((iface, j) => ({
        iface,
        position: pointOnEllipse(
          position,
          WORKLOAD_RADIUS,
          atLocation.length === 1 ? angle : spreadStart + j * spreadStep
        ),
      })),
    };
  });
}

interface GatewayNode {
  proxy: HTTPProxy;
  position: { x: number; y: number };
}

export function networkServiceNamesOnNetwork(
  networkServices: NetworkService[],
  interfaces: NetworkInterface[]
): Set<string> {
  return new Set(
    networkServices
      .filter((service) => interfaces.some((iface) => matchesLabelSelector(iface.labels, service.networkInterfaceSelector)))
      .map((service) => service.name)
  );
}

export function gatewayLayout(httpProxies: HTTPProxy[]): GatewayNode[] {
  const arcSpan =
    httpProxies.length <= 1
      ? 0
      : Math.min(MAX_GATEWAY_ARC_DEGREES, GATEWAY_ARC_STEP_DEGREES * (httpProxies.length - 1));
  const angleStep = httpProxies.length <= 1 ? 0 : arcSpan / (httpProxies.length - 1);
  const startAngle = GATEWAY_BASE_ANGLE - arcSpan / 2;

  return httpProxies.map((proxy, i) => ({
    proxy,
    position: pointOnEllipse(CENTER, GATEWAY_RADIUS, startAngle + i * angleStep),
  }));
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, copy] = useCopyToClipboard();

  return (
    <button
      type="button"
      onClick={() => void copy(value, { withToast: true, toastMessage: `${label} copied to clipboard` })}
      className="text-muted-foreground hover:text-foreground shrink-0 transition-colors"
      aria-label={`Copy ${label.toLowerCase()}`}>
      <Icon icon={copied ? CheckIcon : CopyIcon} size={14} />
    </button>
  );
}

function GatewayInfo({ proxy }: { proxy: HTTPProxy }) {
  const hostname = proxy.hostnames[0] ?? proxy.canonicalHostname;

  return (
    <div className="space-y-2 text-sm" data-testid="gateway-info-popover">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium">{proxy.name}</span>
        <Badge type={readyStatusToBadgeType(proxy.programmedStatus)} theme="light">
          {httpProxyProgrammedStatusToLabel(proxy.programmedStatus, proxy.programmedReason)}
        </Badge>
      </div>
      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] items-center gap-x-2 gap-y-1">
        <dt>Hostname</dt>
        <dd className="flex min-w-0 items-center gap-1">
          <span className="truncate font-mono">{hostname ?? '—'}</span>
          {hostname && <CopyButton value={hostname} label="Hostname" />}
        </dd>
        <dt>Services</dt>
        <dd className="truncate">{proxy.networkServiceNames.join(', ') || '—'}</dd>
      </dl>
    </div>
  );
}

function WorkloadInfo({ iface }: { iface: NetworkInterface }) {
  return (
    <div className="space-y-2 text-sm" data-testid="workload-info-popover">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium">{iface.workloadName ?? iface.name}</span>
        <Badge type={readyStatusToBadgeType(iface.holderAvailableStatus)} theme="light">
          {holderAvailableStatusToLabel(iface.holderAvailableStatus, iface.holderAvailableReason)}
        </Badge>
      </div>
      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-2 gap-y-1">
        <dt>Location</dt>
        <dd>{iface.location ?? '—'}</dd>
        <dt>Address</dt>
        <dd className="truncate font-mono">{networkInterfacePrimaryAddress(iface) ?? '—'}</dd>
      </dl>
    </div>
  );
}

function NodeLabel({
  position,
  size,
  borderColor,
  icon,
  label,
  labelWidth = 90,
  bold,
  popover,
}: {
  position: { x: number; y: number };
  size: number;
  borderColor: string;
  icon: typeof NetworkIcon;
  label: string;
  labelWidth?: number;
  bold?: boolean;
  popover?: React.ReactNode;
}) {
  const circle = (
    <span
      className="bg-background flex items-center justify-center rounded-full border-2"
      style={{ width: size, height: size, borderColor }}>
      <Icon icon={icon} size={Math.round(size * 0.45)} />
    </span>
  );

  return (
    <div
      className="absolute flex flex-col items-center gap-1"
      style={{ left: position.x, top: position.y, transform: 'translate(-50%, -50%)' }}>
      {popover ? (
        <Popover>
          <PopoverTrigger asChild>
            <button
              type="button"
              className="focus-visible:ring-ring cursor-pointer rounded-full focus-visible:ring-2 focus-visible:outline-none"
              aria-label={`${label} details`}
              // Stops a click here from also starting a canvas drag via the
              // pan handler in NetworkTopology.
              onPointerDown={(e) => e.stopPropagation()}>
              {circle}
            </button>
          </PopoverTrigger>
          <PopoverContent className="w-64">{popover}</PopoverContent>
        </Popover>
      ) : (
        circle
      )}
      <span
        className={bold ? 'text-xs font-medium' : 'text-muted-foreground text-xs'}
        style={{
          maxWidth: labelWidth,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          textAlign: 'center',
        }}>
        {label}
      </span>
    </div>
  );
}

export function NetworkTopology({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const { data: subnets } = useSubnets(projectId, networkName);
  const { data: interfaces } = useNetworkInterfaces(projectId, networkName);
  const { data: httpProxies } = useHTTPProxies(projectId);
  const { data: networkServices } = useNetworkServices(projectId);

  // State-backed, not useRef: this component returns null until subnets
  // load, so a plain ref's effect would run once against a still-null ref
  // and never retry. This re-runs the effect once the div actually mounts.
  const [viewportEl, setViewportEl] = useState<HTMLDivElement | null>(null);
  const dragRef = useRef<{ startX: number; startY: number; originX: number; originY: number } | null>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });

  // Native listener, not onWheel: React's synthetic handler doesn't always
  // call preventDefault in time to stop the page from scrolling while zooming.
  useEffect(() => {
    if (!viewportEl) return;
    const handleWheel = (e: WheelEvent) => {
      e.preventDefault();
      setZoom((z) => clampZoom(z + (e.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP)));
    };
    viewportEl.addEventListener('wheel', handleWheel, { passive: false });
    return () => viewportEl.removeEventListener('wheel', handleWheel);
  }, [viewportEl]);

  function handlePanStart(e: React.PointerEvent<HTMLDivElement>) {
    dragRef.current = { startX: e.clientX, startY: e.clientY, originX: pan.x, originY: pan.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function handlePanMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!dragRef.current) return;
    setPan({
      x: dragRef.current.originX + (e.clientX - dragRef.current.startX),
      y: dragRef.current.originY + (e.clientY - dragRef.current.startY),
    });
  }

  function handlePanEnd() {
    dragRef.current = null;
  }

  if (!subnets || subnets.length === 0) return null;

  const nodes = layout(subnets, interfaces ?? []);
  // Only proxies wired to a NetworkService that resolves to a member on THIS
  // network count as fronting it: a NetworkService can belong to another
  // network the project also has.
  const servingNames = networkServiceNamesOnNetwork(networkServices ?? [], interfaces ?? []);
  const gateways = gatewayLayout(
    (httpProxies ?? []).filter((proxy) => proxy.networkServiceNames.some((name) => servingNames.has(name)))
  );

  return (
    <div
      className="border-border relative overflow-hidden rounded-lg border p-4"
      data-testid="networking-plugin-network-topology">
      <div className="bg-background/90 absolute top-2 right-2 z-10 flex gap-1 rounded-md border p-1 shadow-sm">
        <Button
          theme="outline"
          size="icon"
          htmlType="button"
          aria-label="Zoom in"
          onClick={() => setZoom((z) => clampZoom(z + ZOOM_STEP))}>
          <Icon icon={ZoomInIcon} size={16} />
        </Button>
        <Button
          theme="outline"
          size="icon"
          htmlType="button"
          aria-label="Zoom out"
          onClick={() => setZoom((z) => clampZoom(z - ZOOM_STEP))}>
          <Icon icon={ZoomOutIcon} size={16} />
        </Button>
        <Button
          theme="outline"
          size="icon"
          htmlType="button"
          aria-label="Reset view"
          onClick={() => {
            setZoom(1);
            setPan({ x: 0, y: 0 });
          }}>
          <Icon icon={RotateCcwIcon} size={16} />
        </Button>
      </div>

      <div
        ref={setViewportEl}
        data-testid="network-topology-viewport"
        className="mx-auto flex cursor-grab touch-none items-center justify-center active:cursor-grabbing"
        style={{ width: WIDTH, height: HEIGHT }}
        onPointerDown={handlePanStart}
        onPointerMove={handlePanMove}
        onPointerUp={handlePanEnd}
        onPointerLeave={handlePanEnd}>
        <div
          className="relative"
          data-testid="network-topology-stage"
          style={{
            width: WIDTH,
            height: HEIGHT,
            transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
            transformOrigin: 'center center',
          }}>
          <svg
            width={WIDTH}
            height={HEIGHT}
            className="absolute inset-0"
            role="img"
            aria-label={`Topology for ${networkName ?? 'this network'}: ${nodes.length} region${nodes.length === 1 ? '' : 's'}${gateways.length ? `, ${gateways.length} gateway${gateways.length === 1 ? '' : 's'} fronting it` : ''}`}>
            {gateways.map((gateway) => (
              <line
                key={`internet-${gateway.proxy.uid || gateway.proxy.name}`}
                x1={INTERNET_POSITION.x}
                y1={INTERNET_POSITION.y}
                x2={gateway.position.x}
                y2={gateway.position.y}
                stroke={STATUS_COLOR[gateway.proxy.programmedStatus]}
                strokeWidth={2}
              />
            ))}
            {gateways.map((gateway) => (
              <line
                key={`gateway-${gateway.proxy.uid || gateway.proxy.name}`}
                x1={gateway.position.x}
                y1={gateway.position.y}
                x2={CENTER.x}
                y2={CENTER.y}
                stroke={STATUS_COLOR[gateway.proxy.programmedStatus]}
                strokeWidth={2}
              />
            ))}
            {nodes.map((node) => (
              <line
                key={`hub-${node.subnet.uid || node.subnet.name}`}
                x1={CENTER.x}
                y1={CENTER.y}
                x2={node.position.x}
                y2={node.position.y}
                stroke={STATUS_COLOR[node.subnet.readyStatus]}
                strokeWidth={2}
              />
            ))}
            {nodes.flatMap((node) =>
              node.workloads.map(({ iface, position }) => (
                <line
                  key={`spoke-${iface.uid || iface.name}`}
                  x1={node.position.x}
                  y1={node.position.y}
                  x2={position.x}
                  y2={position.y}
                  stroke={STATUS_COLOR[iface.holderAvailableStatus]}
                  strokeWidth={1.5}
                />
              ))
            )}
          </svg>

          <div className="absolute inset-0">
            {gateways.length > 0 && (
              <NodeLabel
                position={INTERNET_POSITION}
                size={48}
                borderColor="var(--color-badge-muted)"
                icon={GlobeIcon}
                label="Internet"
                bold
              />
            )}

            {gateways.map((gateway) => (
              <NodeLabel
                key={gateway.proxy.uid || gateway.proxy.name}
                position={gateway.position}
                size={36}
                borderColor={STATUS_COLOR[gateway.proxy.programmedStatus]}
                icon={RouterIcon}
                label={gateway.proxy.name}
                labelWidth={80}
                popover={<GatewayInfo proxy={gateway.proxy} />}
              />
            ))}

            <NodeLabel
              position={CENTER}
              size={48}
              borderColor="var(--color-badge-muted)"
              icon={NetworkIcon}
              label={networkName ?? ''}
              bold
            />

            {nodes.map((node) => (
              <NodeLabel
                key={node.subnet.uid || node.subnet.name}
                position={node.position}
                size={40}
                borderColor={STATUS_COLOR[node.subnet.readyStatus]}
                icon={MapPinIcon}
                label={node.subnet.location ?? node.subnet.name}
              />
            ))}

            {nodes.flatMap((node) =>
              node.workloads.map(({ iface, position }) => (
                <NodeLabel
                  key={iface.uid || iface.name}
                  position={position}
                  size={28}
                  borderColor={STATUS_COLOR[iface.holderAvailableStatus]}
                  icon={BoxIcon}
                  label={iface.workloadName ?? iface.name}
                  labelWidth={60}
                  popover={<WorkloadInfo iface={iface} />}
                />
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
