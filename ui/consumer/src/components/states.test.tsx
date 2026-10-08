import { ErrorOrRestrictedState, ErrorState, LoadingSkeleton, RestrictedState } from './states';
import { ApiError } from '../lib/api';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

describe('LoadingSkeleton', () => {
  it('renders a content-only skeleton', () => {
    render(<LoadingSkeleton />);
    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });
});

describe('RestrictedState', () => {
  it('renders the restricted message', () => {
    render(<RestrictedState message="You don't have permission to view this project's networks." />);
    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
    expect(screen.getByText("You don't have permission to view this project's networks.")).toBeInTheDocument();
  });
});

describe('ErrorState', () => {
  it('renders the error message and a retry button', () => {
    const onRetry = vi.fn();
    render(<ErrorState error={new Error('network unreachable')} onRetry={onRetry} />);

    expect(screen.getByTestId('networking-plugin-error')).toBeInTheDocument();
    expect(screen.getByText('network unreachable')).toBeInTheDocument();
    screen.getByText('Retry').click();
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('falls back to a generic message for a non-Error value', () => {
    render(<ErrorState error={'boom'} onRetry={() => {}} />);
    expect(screen.getByText('An unknown error occurred')).toBeInTheDocument();
  });
});

describe('ErrorOrRestrictedState', () => {
  it('renders RestrictedState for a 403 ApiError', () => {
    render(
      <ErrorOrRestrictedState
        error={new ApiError(403, 'forbidden')}
        restrictedMessage="restricted message"
        onRetry={() => {}}
      />
    );
    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
    expect(screen.getByText('restricted message')).toBeInTheDocument();
  });

  it('renders ErrorState for a non-403 error', () => {
    render(
      <ErrorOrRestrictedState
        error={new ApiError(500, 'server error')}
        restrictedMessage="restricted message"
        onRetry={() => {}}
      />
    );
    expect(screen.getByTestId('networking-plugin-error')).toBeInTheDocument();
  });

  it('renders ErrorState for a generic non-ApiError error', () => {
    render(
      <ErrorOrRestrictedState
        error={new Error('boom')}
        restrictedMessage="restricted message"
        onRetry={() => {}}
      />
    );
    expect(screen.getByTestId('networking-plugin-error')).toBeInTheDocument();
  });
});
