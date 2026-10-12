import { ApiError } from '../lib/api';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { LockIcon, ServerCrashIcon } from 'lucide-react';

export function RestrictedState({ message }: { message: string }) {
  return (
    <div data-testid="networking-plugin-restricted" className="flex flex-col gap-4">
      <Card className="max-w-md">
        <CardContent className="flex flex-col items-start gap-3">
          <Icon icon={LockIcon} size={24} className="text-muted-foreground" />
          <div>
            <p className="text-sm font-semibold">Access restricted</p>
            <p className="text-muted-foreground mt-1 text-sm">{message}</p>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const message = error instanceof Error ? error.message : 'An unknown error occurred';

  return (
    <div data-testid="networking-plugin-error" className="flex flex-col gap-4">
      <Card className="max-w-md">
        <CardContent className="flex flex-col items-start gap-3">
          <Icon icon={ServerCrashIcon} size={24} className="text-destructive" />
          <div>
            <p className="text-sm font-semibold">Failed to load</p>
            <p className="text-muted-foreground mt-1 text-sm">{message}</p>
          </div>
          <Button type="secondary" theme="outline" size="small" htmlType="button" onClick={onRetry}>
            Retry
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

export function ErrorOrRestrictedState({
  error,
  restrictedMessage,
  onRetry,
}: {
  error: unknown;
  restrictedMessage: string;
  onRetry: () => void;
}) {
  if (error instanceof ApiError && error.status === 403) {
    return <RestrictedState message={restrictedMessage} />;
  }
  return <ErrorState error={error} onRetry={onRetry} />;
}

export function SectionEmpty({
  title,
  description,
  action,
  testId,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
  testId?: string;
}) {
  return (
    <Card size="sm" data-testid={testId}>
      <CardContent className="flex flex-col items-center gap-1 py-8 text-center">
        <p className="text-sm font-medium">{title}</p>
        {description && <p className="text-muted-foreground max-w-md text-sm">{description}</p>}
        {action && <div className="mt-3">{action}</div>}
      </CardContent>
    </Card>
  );
}
