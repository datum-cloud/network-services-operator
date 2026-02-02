'use client';

import { useRouter } from 'next/navigation';
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

function CreateSecurityPolicyForm() {
  const router = useRouter();
  const { validate, toSecurityPolicy, errors } = useSecurityPolicyForm();

  const { data: namespaces, isLoading: namespacesLoading } = useQuery({
    queryKey: ['namespaces'],
    queryFn: async () => {
      const response = await apiClient.listNamespaces();
      return response.items.map(ns => ns.name);
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => {
      const policy = toSecurityPolicy();
      return apiClient.createSecurityPolicy(policy);
    },
    onSuccess: (policy) => {
      router.push(`/security-policies/${policy.metadata.namespace}/${policy.metadata.name}`);
    },
  });

  const steps = [
    {
      id: 'basic',
      title: 'Basic Info',
      description: 'Configure policy name, namespace, and target references',
      content: <BasicInfoStep namespaces={namespaces || []} />,
      validate: () => {
        validate();
        // Check if basic info is valid (name, namespace, at least one target)
        const hasNameError = errors.some(e => e.field === 'name');
        const hasNamespaceError = errors.some(e => e.field === 'namespace');
        const hasTargetError = errors.some(e => e.field === 'targetRefs');
        return !hasNameError && !hasNamespaceError && !hasTargetError;
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
      description: 'Review and create your security policy',
      content: <ReviewStep />,
      validate: () => {
        const isValid = validate();
        return isValid;
      },
    },
  ];

  if (namespacesLoading) {
    return <LoadingState message="Loading..." />;
  }

  return (
    <MultiStepForm
      steps={steps}
      onComplete={async () => { await createMutation.mutateAsync(); }}
      onCancel={() => router.push('/security-policies')}
      submitLabel="Create Policy"
      loading={createMutation.isPending}
    />
  );
}

export default function CreateSecurityPolicyPage() {
  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Security Policies', href: '/security-policies' },
          { name: 'Create' },
        ]}
      />

      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
          Create Security Policy
        </h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
          Configure authentication and authorization for your gateways
        </p>
      </div>

      <Card>
        <CardContent className="p-6">
          <SecurityPolicyFormProvider>
            <CreateSecurityPolicyForm />
          </SecurityPolicyFormProvider>
        </CardContent>
      </Card>
    </div>
  );
}
