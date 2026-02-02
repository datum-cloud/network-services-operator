'use client';

import { useParams, useRouter } from 'next/navigation';
import { useQuery, useMutation } from '@tanstack/react-query';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardContent } from '@/components/common/Card';
import { MultiStepForm } from '@/components/forms/MultiStepForm';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { SecurityPolicyFormProvider } from '@/components/security-policy/SecurityPolicyFormContext';
import { BasicInfoStep } from '@/components/security-policy/steps/BasicInfoStep';
import { AuthenticationStep } from '@/components/security-policy/steps/AuthenticationStep';
import { ReviewStep } from '@/components/security-policy/steps/ReviewStep';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { apiClient } from '@/api/client';
import type { SecurityPolicy } from '@/api/types';

function EditSecurityPolicyForm({ policy }: { policy: SecurityPolicy }) {
  const router = useRouter();
  const { validate, toSecurityPolicy, errors } = useSecurityPolicyForm();

  const { data: namespaces } = useQuery({
    queryKey: ['namespaces'],
    queryFn: async () => {
      const response = await apiClient.listNamespaces();
      return response.items.map(ns => ns.name);
    },
  });

  const updateMutation = useMutation({
    mutationFn: async () => {
      const updatedPolicy = toSecurityPolicy();
      // Preserve metadata from original policy
      updatedPolicy.metadata.uid = policy.metadata.uid;
      updatedPolicy.metadata.resourceVersion = policy.metadata.resourceVersion;
      updatedPolicy.metadata.creationTimestamp = policy.metadata.creationTimestamp;
      return apiClient.updateSecurityPolicy(updatedPolicy);
    },
    onSuccess: (policy) => {
      router.push(`/security-policies/${policy.metadata.namespace}/${policy.metadata.name}`);
    },
  });

  const steps = [
    {
      id: 'basic',
      title: 'Basic Info',
      description: 'Update policy targets',
      content: <BasicInfoStep namespaces={namespaces || []} isEdit />,
      validate: () => {
        validate();
        const hasTargetError = errors.some(e => e.field === 'targetRefs');
        return !hasTargetError;
      },
    },
    {
      id: 'authentication',
      title: 'Authentication',
      description: 'Configure authentication and security features',
      content: <AuthenticationStep />,
    },
    {
      id: 'review',
      title: 'Review',
      description: 'Review and update your security policy',
      content: <ReviewStep />,
      validate: () => {
        const isValid = validate();
        return isValid;
      },
    },
  ];

  return (
    <MultiStepForm
      steps={steps}
      onComplete={async () => { await updateMutation.mutateAsync(); }}
      onCancel={() => router.push(`/security-policies/${policy.metadata.namespace}/${policy.metadata.name}`)}
      submitLabel="Update Policy"
      loading={updateMutation.isPending}
    />
  );
}

export default function EditSecurityPolicyPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: policy, isLoading, error, refetch } = useQuery({
    queryKey: ['securityPolicy', namespace, name],
    queryFn: () => apiClient.getSecurityPolicy(name, namespace),
    enabled: !!namespace && !!name,
  });

  if (isLoading) {
    return <LoadingState message="Loading security policy..." />;
  }

  if (error || !policy) {
    return (
      <ErrorState
        title="Security policy not found"
        message="The security policy you're trying to edit doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Security Policies', href: '/security-policies' },
          { name: policy.metadata.name, href: `/security-policies/${namespace}/${name}` },
          { name: 'Edit' },
        ]}
      />

      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
          Edit Security Policy
        </h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
          Editing: {policy.metadata.name}
        </p>
      </div>

      <Card>
        <CardContent className="p-6">
          <SecurityPolicyFormProvider initialPolicy={policy}>
            <EditSecurityPolicyForm policy={policy} />
          </SecurityPolicyFormProvider>
        </CardContent>
      </Card>
    </div>
  );
}
