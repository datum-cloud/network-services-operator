'use client';

import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardContent } from '@/components/common/Card';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { MultiStepForm } from '@/components/forms/MultiStepForm';
import { GatewayFormProvider, useGatewayFormContext } from '@/components/gateway/GatewayFormContext';
import { useGatewayForm } from '@/hooks/useGatewayForm';
import { BasicInfoStep } from '@/components/gateway/steps/BasicInfoStep';
import { RoutingRulesStep } from '@/components/gateway/steps/RoutingRulesStep';
import { ReviewStep } from '@/components/gateway/steps/ReviewStep';
import { apiClient } from '@/api/client';
import type { HTTPProxy, Connector } from '@/api/types';

function EditGatewayFormContent({
  proxy,
  namespaces,
  connectors,
}: {
  proxy: HTTPProxy;
  namespaces: { name: string }[];
  connectors: Connector[];
}) {
  const router = useRouter();
  const queryClient = useQueryClient();

  const {
    name,
    namespace,
    hostnames,
    setName,
    setNamespace,
    setHostnames,
    validate,
    toHTTPProxy,
    getErrors,
  } = useGatewayForm();

  const updateMutation = useMutation({
    mutationFn: (updatedProxy: HTTPProxy) =>
      apiClient.updateHTTPProxy(updatedProxy),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['httpproxies'] });
      queryClient.invalidateQueries({ queryKey: ['httpproxy', proxy.metadata.namespace, proxy.metadata.name] });
      router.push(`/gateways/${proxy.metadata.namespace}/${proxy.metadata.name}`);
    },
  });

  const namespaceOptions = (namespaces || []).map((ns) => ({
    value: ns.name,
    label: ns.name,
  }));

  const handleComplete = async () => {
    if (validate()) {
      const updatedProxy = toHTTPProxy();
      // Preserve original metadata
      updatedProxy.metadata.resourceVersion = proxy.metadata.resourceVersion;
      updateMutation.mutate(updatedProxy);
    }
  };

  const basicInfoErrors = {
    name: getErrors('name')[0],
    namespace: getErrors('namespace')[0],
    hostnames: getErrors('hostnames')[0],
  };

  const steps = [
    {
      id: 'basic',
      title: 'Basic Info',
      description: 'Configure gateway name, namespace, and hostnames',
      content: (
        <BasicInfoStep
          name={name}
          namespace={namespace}
          hostnames={hostnames}
          namespaceOptions={namespaceOptions}
          onNameChange={setName}
          onNamespaceChange={setNamespace}
          onHostnamesChange={setHostnames}
          errors={basicInfoErrors}
          isEdit={true}
        />
      ),
      validate: () => {
        return name.trim() !== '' && namespace.trim() !== '';
      },
    },
    {
      id: 'routing',
      title: 'Routing Rules',
      description: 'Configure path-based routing, filters, and backends',
      content: <RoutingRulesStep connectors={connectors} />,
      validate: () => true,
    },
    {
      id: 'review',
      title: 'Review',
      description: 'Review and submit your gateway configuration',
      content: <ReviewStep isEdit={true} />,
      validate: () => validate(),
    },
  ];

  return (
    <Card>
      <CardContent className="p-6">
        <MultiStepForm
          steps={steps}
          onComplete={handleComplete}
          onCancel={() => router.push(`/gateways/${proxy.metadata.namespace}/${proxy.metadata.name}`)}
          submitLabel="Update Gateway"
          loading={updateMutation.isPending}
        />

        {updateMutation.error && (
          <div className="mt-4 p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg">
            <p className="text-sm text-red-600 dark:text-red-400">
              {updateMutation.error instanceof Error
                ? updateMutation.error.message
                : 'Failed to update gateway'}
            </p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default function EditGatewayPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: proxy, isLoading: proxyLoading, error: proxyError, refetch } = useQuery({
    queryKey: ['httpproxy', namespace, name],
    queryFn: () => apiClient.getHTTPProxy(name, namespace),
    enabled: !!namespace && !!name,
  });

  const { data: namespaces } = useQuery({
    queryKey: ['namespaces'],
    queryFn: () => apiClient.listNamespaces(),
  });

  const { data: connectors } = useQuery({
    queryKey: ['connectors'],
    queryFn: () => apiClient.listConnectors(),
  });

  if (proxyLoading) {
    return <LoadingState message="Loading gateway..." />;
  }

  if (proxyError || !proxy) {
    return (
      <ErrorState
        title="Gateway not found"
        message="The gateway you're trying to edit doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Gateways', href: '/gateways' },
          { name: proxy.metadata.name, href: `/gateways/${namespace}/${name}` },
          { name: 'Edit' },
        ]}
      />

      <div className="flex items-start gap-4">
        <Link
          href={`/gateways/${namespace}/${name}`}
          className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Edit Gateway</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Modify the configuration for {proxy.metadata.name}
          </p>
        </div>
      </div>

      <GatewayFormProvider initialProxy={proxy}>
        <EditGatewayFormContent
          proxy={proxy}
          namespaces={namespaces?.items || []}
          connectors={connectors?.items || []}
        />
      </GatewayFormProvider>
    </div>
  );
}
