import { Card, CardContent, CardField, CardHeader } from '@datum-cloud/datum-ui/card';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { cn } from '@datum-cloud/datum-ui/utils';

const NETWORK_COLUMNS = ['Name', 'Status', 'IPv6 prefix', 'IP families', 'MTU', 'Created'];
const SERVICE_COLUMNS = ['Name', 'Ports', 'Regions', 'Members', 'Healthy', 'Resolved', 'Ready', 'Created'];
const RESOURCE_COLUMNS = ['Workload', 'Address', 'Status', 'Created'];

function Loading({ kind, children }: { kind: string; children: React.ReactNode }) {
  return (
    <div
      className="flex flex-col gap-6"
      aria-busy="true"
      aria-label="Loading"
      data-testid="networking-plugin-loading"
      data-skeleton={kind}>
      {children}
    </div>
  );
}

function StatTilesSkeleton() {
  return (
    <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))' }}>
      {Array.from({ length: 4 }).map((_, i) => (
        <Card key={i}>
          <CardContent className="flex flex-col gap-2">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-8 w-12" />
            <Skeleton className="h-3 w-20" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function TableSkeleton({
  columns,
  rows = 5,
  action,
}: {
  columns: string[];
  rows?: number;
  action?: boolean;
}) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Skeleton className="h-9 w-full sm:max-w-xs" />
        {action && <Skeleton className="h-8 w-32" />}
      </div>
      <div className="overflow-hidden rounded-lg border">
        <div className="bg-muted/40 flex gap-4 border-b px-4 py-3">
          {columns.map((column) => (
            <span key={column} className="text-muted-foreground min-w-0 flex-1 truncate text-xs font-medium">
              {column}
            </span>
          ))}
        </div>
        {Array.from({ length: rows }).map((_, row) => (
          <div key={row} className="flex items-center gap-4 border-b px-4 py-3 last:border-b-0">
            {columns.map((column) => (
              <Skeleton key={column} className="h-4 min-w-0 flex-1" />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

function HealthStripSkeleton() {
  return (
    <Card size="sm">
      <CardContent className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <Skeleton className="size-9 rounded-full" />
          <div className="flex flex-col gap-2">
            <Skeleton className="h-4 w-36" />
            <Skeleton className="h-3 w-64" />
          </div>
        </div>
        <div className="hidden gap-2 sm:flex">
          <Skeleton className="h-6 w-20" />
          <Skeleton className="h-6 w-24" />
        </div>
      </CardContent>
    </Card>
  );
}

function DetailCardSkeleton({ fields, className }: { fields: number; className?: string }) {
  return (
    <Card size="sm" sectioned className={cn('w-full overflow-hidden', className)}>
      <CardHeader size="sm" bordered>
        <Skeleton className="h-4 w-32" />
      </CardHeader>
      <CardContent padding="none">
        {Array.from({ length: fields }).map((_, i) => (
          <CardField key={i}>
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-4 w-40" />
          </CardField>
        ))}
      </CardContent>
    </Card>
  );
}

function ResourcesTableSkeleton() {
  return (
    <>
      <Skeleton className="h-4 w-72" />
      <Card size="sm" sectioned className="overflow-hidden">
        <CardContent padding="none">
          <div className="flex gap-4 border-b px-5 py-3">
            {RESOURCE_COLUMNS.map((column) => (
              <span key={column} className="min-w-0 flex-1 truncate text-xs font-medium">
                {column}
              </span>
            ))}
          </div>
          {Array.from({ length: 2 }).map((_, group) => (
            <div key={group}>
              <div className="bg-muted/40 flex items-center justify-between border-b px-5 py-3">
                <Skeleton className="h-4 w-40" />
                <Skeleton className="h-4 w-24" />
              </div>
              {Array.from({ length: 3 }).map((_, row) => (
                <div key={row} className="flex items-center gap-4 border-b px-5 py-3">
                  {RESOURCE_COLUMNS.map((column) => (
                    <Skeleton key={column} className="h-4 min-w-0 flex-1" />
                  ))}
                </div>
              ))}
            </div>
          ))}
        </CardContent>
      </Card>
    </>
  );
}

export function ResourcesSkeleton() {
  return (
    <Loading kind="resources">
      <ResourcesTableSkeleton />
    </Loading>
  );
}

export function NetworkListSkeleton() {
  return (
    <Loading kind="network-list">
      <TableSkeleton columns={NETWORK_COLUMNS} />
    </Loading>
  );
}

export function NetworkOverviewSkeleton() {
  return (
    <Loading kind="overview">
      <HealthStripSkeleton />
      <ResourcesTableSkeleton />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <DetailCardSkeleton fields={3} />
        <DetailCardSkeleton fields={4} />
      </div>
    </Loading>
  );
}

export function ServicesSkeleton() {
  return (
    <Loading kind="services">
      <StatTilesSkeleton />
      <TableSkeleton columns={SERVICE_COLUMNS} rows={4} action />
    </Loading>
  );
}

export function ConfigurationSkeleton() {
  return (
    <Loading kind="configuration">
      <div className="flex flex-col gap-8 lg:flex-row lg:items-start lg:gap-10">
        <div className="flex flex-col gap-2 lg:w-56 lg:shrink-0">
          <Skeleton className="h-3 w-16" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-6">
          <DetailCardSkeleton fields={3} />
          <DetailCardSkeleton fields={4} />
          <Skeleton className="h-24 w-full rounded-lg" />
        </div>
      </div>
    </Loading>
  );
}
