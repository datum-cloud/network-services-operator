import { NetworkInterfaces } from './network-interfaces';
import { NetworkSubnets } from './network-subnets';
import { NetworkTopology } from './network-topology';

function ResourceSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <h2 className="text-lg font-semibold">{title}</h2>
      {children}
    </div>
  );
}

export function NetworkResources({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  return (
    <div className="flex flex-col gap-10" data-testid="networking-plugin-network-resources">
      <NetworkTopology projectId={projectId} networkName={networkName} />
      <ResourceSection title="Regions">
        <NetworkSubnets projectId={projectId} networkName={networkName} />
      </ResourceSection>
      <ResourceSection title="Connected workloads">
        <NetworkInterfaces projectId={projectId} networkName={networkName} />
      </ResourceSection>
    </div>
  );
}
