import { asApiError } from './create-nlb-dialog';
import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { planDeleteListener, type DeleteListenerPlan } from './nlb-listener-changes';
import type { ListenerRow } from './nlb-overview-model';
import {
  deleteNetworkLoadBalancerRoute,
  detachRoute,
  patchNetworkLoadBalancer,
  PLUGIN_ID,
} from '../lib/api';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute } from '../schema';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useQueryClient } from '@tanstack/react-query';

export function describeDeleteListener(
  nlb: NetworkLoadBalancer,
  row: ListenerRow,
  plan: DeleteListenerPlan
): string {
  const label = `${row.listener.protocol} ${row.listener.port}`;
  const parts = [
    plan.releasedAddress
      ? `This removes ${label} from ${nlb.name} and releases its public address.`
      : `This removes ${label} from ${nlb.name}.`,
  ];
  if (plan.deleteRoutes.length > 0) {
    const names = plan.deleteRoutes.map((r) => r.name).join(', ');
    parts.push(
      `It also deletes ${plan.deleteRoutes.length === 1 ? 'its route' : 'its routes'} ${names}.`
    );
  }
  for (const { route } of plan.detachRoutes) {
    parts.push(`${route.name} also serves other listeners, so it stays for them.`);
  }
  parts.push("This can't be undone.");
  return parts.join(' ');
}

export function DeleteListenerDialog({
  projectId,
  nlb,
  routes,
  row,
  onOpenChange,
}: {
  projectId: string | undefined;
  nlb: NetworkLoadBalancer;
  routes: NetworkLoadBalancerRoute[];
  /** The listener to delete; the dialog is open while it's set. */
  row: ListenerRow | undefined;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const plan = row ? planDeleteListener(nlb, row.listener, routes) : undefined;

  const refresh = () => {
    for (const key of [
      'networkLoadBalancer',
      'networkLoadBalancers',
      'networkLoadBalancerRoutes',
    ]) {
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, key, projectId] });
    }
  };

  return (
    <ConfirmDeleteDialog
      open={row !== undefined}
      onOpenChange={onOpenChange}
      title={row ? `Delete ${row.listener.protocol} ${row.listener.port}?` : ''}
      description={row && plan ? describeDeleteListener(nlb, row, plan) : ''}
      onConfirm={async () => {
        if (!row || !plan) return;
        // Routes first, as when deleting an NLB: a failure part way leaves the
        // listener in place to retry from, not routes pointing at nothing.
        try {
          for (const { route, parents } of plan.detachRoutes) {
            await detachRoute(projectId as string, route, parents);
          }
          for (const route of plan.deleteRoutes) {
            await deleteNetworkLoadBalancerRoute(projectId as string, route.name);
          }
          await patchNetworkLoadBalancer(projectId as string, nlb.name, plan.patch);
          toast.success(`Deleted ${row.listener.protocol} ${row.listener.port} from ${nlb.name}`);
        } catch (err) {
          toast.error(asApiError(err).message);
          throw err;
        } finally {
          refresh();
        }
      }}
    />
  );
}
