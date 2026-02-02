'use client';

import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardContent } from '@/components/common/Card';
import { MultiStepForm } from '@/components/forms/MultiStepForm';
import { GatewayFormProvider } from '@/components/gateway/GatewayFormContext';
import { useGatewayForm } from '@/hooks/useGatewayForm';
import { BasicInfoStep } from '@/components/gateway/steps/BasicInfoStep';
import { RoutingRulesStep } from '@/components/gateway/steps/RoutingRulesStep';
import { ReviewStep } from '@/components/gateway/steps/ReviewStep';
import { apiClient } from '@/api/client';
import type { HTTPProxy, Connector } from '@/api/types';

function CreateGatewayFormContent({
  namespaces,
  connectors,
}: {
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

  const createMutation = useMutation({
    mutationFn: (proxy: HTTPProxy) => apiClient.createHTTPProxy(proxy),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['httpproxies'] });
      router.push(`/gateways/${data.metadata.namespace}/${data.metadata.name}`);
    },
  });

  const namespaceOptions = (namespaces || []).map((ns) => ({
    value: ns.name,
    label: ns.name,
  }));

  const handleComplete = async () => {
    if (validate()) {
      createMutation.mutate(toHTTPProxy());
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
      content: <ReviewStep />,
      validate: () => validate(),
    },
  ];

  return (
    <Card>
      <CardContent className="p-6">
        <MultiStepForm
          steps={steps}
          onComplete={handleComplete}
          onCancel={() => router.push('/gateways')}
          submitLabel="Create Gateway"
          loading={createMutation.isPending}
        />

        {createMutation.error && (
          <div className="mt-4 p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg">
            <p className="text-sm text-red-600 dark:text-red-400">
              {createMutation.error instanceof Error
                ? createMutation.error.message
                : 'Failed to create gateway'}
            </p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default function CreateGatewayPage() {
  const { data: namespaces } = useQuery({
    queryKey: ['namespaces'],
    queryFn: () => apiClient.listNamespaces(),
  });

  const { data: connectors } = useQuery({
    queryKey: ['connectors'],
    queryFn: () => apiClient.listConnectors(),
  });

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Gateways', href: '/gateways' },
          { name: 'Create' },
        ]}
      />

      <div className="flex items-start gap-4">
        <Link
          href="/gateways"
          className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Create Gateway</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Create a new HTTP proxy gateway with advanced routing capabilities
          </p>
        </div>
      </div>

      <GatewayFormProvider>
        <CreateGatewayFormContent
          namespaces={namespaces?.items || []}
          connectors={connectors?.items || []}
        />
      </GatewayFormProvider>
    </div>
  );
}
