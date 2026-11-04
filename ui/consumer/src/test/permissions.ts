import { NO_NETWORK_PERMISSIONS, type NetworkPermissions } from '../lib/api';

export const ALL_NETWORK_PERMISSIONS: NetworkPermissions & { isLoading: boolean } = {
  canCreateNetwork: true,
  canUpdateNetwork: true,
  canDeleteNetwork: true,
  canCreateService: true,
  canDeleteService: true,
  isLoading: false,
};

export const READ_ONLY_NETWORK_PERMISSIONS: NetworkPermissions & { isLoading: boolean } = {
  ...NO_NETWORK_PERMISSIONS,
  isLoading: false,
};
