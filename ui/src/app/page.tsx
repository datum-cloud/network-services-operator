'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import {
  Globe,
  CheckCircle,
  Shield,
  Network,
  ArrowUpRight,
  Clock,
  AlertTriangle,
  Activity,
} from 'lucide-react';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { apiClient } from '@/api/client';
import type { DashboardStats, RecentActivity } from '@/api/types';

function formatRelativeTime(timestamp: string): string {
  const date = new Date(timestamp);
  const now = new Date();
  const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  if (diffInSeconds < 60) {
    return 'just now';
  } else if (diffInSeconds < 3600) {
    const minutes = Math.floor(diffInSeconds / 60);
    return `${minutes}m ago`;
  } else if (diffInSeconds < 86400) {
    const hours = Math.floor(diffInSeconds / 3600);
    return `${hours}h ago`;
  } else {
    const days = Math.floor(diffInSeconds / 86400);
    return `${days}d ago`;
  }
}

function StatCard({
  title,
  icon: Icon,
  total,
  details,
  href,
  color = 'primary',
}: {
  title: string;
  icon: React.ElementType;
  total: number;
  details: { label: string; value: number; status?: 'success' | 'warning' | 'error' }[];
  href: string;
  color?: 'primary' | 'green' | 'blue' | 'purple';
}) {
  const colorClasses = {
    primary: 'bg-primary-100 dark:bg-primary-900/30 text-primary-600 dark:text-primary-400',
    green: 'bg-green-100 dark:bg-green-900/30 text-green-600 dark:text-green-400',
    blue: 'bg-blue-100 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400',
    purple: 'bg-purple-100 dark:bg-purple-900/30 text-purple-600 dark:text-purple-400',
  };

  return (
    <Card className="hover:shadow-lg transition-shadow">
      <CardContent className="p-6">
        <div className="flex items-start justify-between">
          <div>
            <div className="flex items-center gap-3 mb-4">
              <div className={`w-10 h-10 rounded-lg flex items-center justify-center ${colorClasses[color]}`}>
                <Icon className="w-5 h-5" />
              </div>
              <div>
                <h3 className="text-sm font-medium text-gray-500 dark:text-dark-400">{title}</h3>
                <p className="text-2xl font-bold text-gray-900 dark:text-white">{total}</p>
              </div>
            </div>
            <div className="flex flex-wrap gap-4">
              {details.map((detail) => (
                <div key={detail.label} className="flex items-center gap-1.5">
                  <span
                    className={`w-2 h-2 rounded-full ${
                      detail.status === 'success'
                        ? 'bg-green-500'
                        : detail.status === 'warning'
                        ? 'bg-yellow-500'
                        : detail.status === 'error'
                        ? 'bg-red-500'
                        : 'bg-gray-400'
                    }`}
                  />
                  <span className="text-sm text-gray-600 dark:text-dark-300">
                    {detail.value} {detail.label}
                  </span>
                </div>
              ))}
            </div>
          </div>
          <Link
            href={href}
            className="p-2 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 transition-colors"
          >
            <ArrowUpRight className="w-5 h-5" />
          </Link>
        </div>
      </CardContent>
    </Card>
  );
}

function RecentActivityItem({ activity }: { activity: RecentActivity }) {
  const typeIcons = {
    gateway: Globe,
    domain: CheckCircle,
    policy: Shield,
    connector: Network,
  };
  const Icon = typeIcons[activity.type] || Activity;

  const actionColors = {
    created: 'text-green-600 dark:text-green-400',
    updated: 'text-blue-600 dark:text-blue-400',
    deleted: 'text-red-600 dark:text-red-400',
  };

  return (
    <div className="flex items-start gap-4 py-3">
      <div className="w-8 h-8 rounded-full bg-gray-100 dark:bg-dark-800 flex items-center justify-center flex-shrink-0">
        <Icon className="w-4 h-4 text-gray-500 dark:text-dark-400" />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm text-gray-900 dark:text-white">
          <span className="font-medium">{activity.resourceName}</span>
          <span className={`ml-1 ${actionColors[activity.action]}`}>{activity.action}</span>
        </p>
        <div className="flex items-center gap-2 mt-1 text-xs text-gray-500 dark:text-dark-400">
          <Clock className="w-3 h-3" />
          <span>{formatRelativeTime(activity.timestamp)}</span>
          {activity.user && (
            <>
              <span>by</span>
              <span>{activity.user}</span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export default function Dashboard() {
  const { data: stats, isLoading, error, refetch } = useQuery<DashboardStats>({
    queryKey: ['dashboard-stats'],
    queryFn: () => apiClient.getDashboardStats(),
  });

  const { data: recentActivity } = useQuery<RecentActivity[]>({
    queryKey: ['recent-activity'],
    queryFn: () => apiClient.getRecentActivity(),
  });

  if (isLoading) {
    return <LoadingState message="Loading dashboard..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load dashboard data" onRetry={() => refetch()} />;
  }

  if (!stats) {
    return null;
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Dashboard</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
          Overview of your edge gateway resources
        </p>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-6">
        <StatCard
          title="Gateways"
          icon={Globe}
          total={stats.gateways.total}
          details={[
            { label: 'healthy', value: stats.gateways.healthy, status: 'success' },
            { label: 'unhealthy', value: stats.gateways.unhealthy, status: 'error' },
          ]}
          href="/gateways"
          color="primary"
        />
        <StatCard
          title="Domains"
          icon={CheckCircle}
          total={stats.domains.total}
          details={[
            { label: 'verified', value: stats.domains.verified, status: 'success' },
            { label: 'pending', value: stats.domains.pending, status: 'warning' },
          ]}
          href="/domains"
          color="green"
        />
        <StatCard
          title="Security Policies"
          icon={Shield}
          total={stats.policies.total}
          details={[
            { label: 'enforcing', value: stats.policies.enforcing, status: 'success' },
            { label: 'observing', value: stats.policies.observing, status: 'warning' },
          ]}
          href="/policies"
          color="blue"
        />
        <StatCard
          title="Connectors"
          icon={Network}
          total={stats.connectors.total}
          details={[
            { label: 'connected', value: stats.connectors.connected, status: 'success' },
            { label: 'disconnected', value: stats.connectors.disconnected, status: 'error' },
          ]}
          href="/connectors"
          color="purple"
        />
      </div>

      {/* Bottom Section */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Recent Activity */}
        <Card>
          <CardHeader>
            <CardTitle>Recent Activity</CardTitle>
          </CardHeader>
          <CardContent>
            {recentActivity && recentActivity.length > 0 ? (
              <div className="divide-y divide-gray-100 dark:divide-dark-700">
                {recentActivity.slice(0, 5).map((activity) => (
                  <RecentActivityItem key={activity.id} activity={activity} />
                ))}
              </div>
            ) : (
              <div className="py-8 text-center text-gray-500 dark:text-dark-400">
                <Activity className="w-8 h-8 mx-auto mb-2 opacity-50" />
                <p className="text-sm">No recent activity</p>
              </div>
            )}
          </CardContent>
        </Card>

        {/* Alerts/Issues */}
        <Card>
          <CardHeader>
            <CardTitle>Active Alerts</CardTitle>
          </CardHeader>
          <CardContent>
            {stats.gateways.unhealthy > 0 || stats.connectors.disconnected > 0 ? (
              <div className="space-y-3">
                {stats.gateways.unhealthy > 0 && (
                  <div className="flex items-start gap-3 p-3 bg-red-50 dark:bg-red-900/20 rounded-lg">
                    <AlertTriangle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
                    <div>
                      <p className="text-sm font-medium text-red-800 dark:text-red-200">
                        {stats.gateways.unhealthy} unhealthy gateway{stats.gateways.unhealthy > 1 ? 's' : ''}
                      </p>
                      <p className="text-xs text-red-600 dark:text-red-400 mt-0.5">
                        Check gateway configurations for issues
                      </p>
                    </div>
                    <Link
                      href="/gateways"
                      className="ml-auto text-xs text-red-600 dark:text-red-400 hover:underline"
                    >
                      View
                    </Link>
                  </div>
                )}
                {stats.connectors.disconnected > 0 && (
                  <div className="flex items-start gap-3 p-3 bg-yellow-50 dark:bg-yellow-900/20 rounded-lg">
                    <AlertTriangle className="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" />
                    <div>
                      <p className="text-sm font-medium text-yellow-800 dark:text-yellow-200">
                        {stats.connectors.disconnected} disconnected connector{stats.connectors.disconnected > 1 ? 's' : ''}
                      </p>
                      <p className="text-xs text-yellow-600 dark:text-yellow-400 mt-0.5">
                        Connectors may need attention
                      </p>
                    </div>
                    <Link
                      href="/connectors"
                      className="ml-auto text-xs text-yellow-600 dark:text-yellow-400 hover:underline"
                    >
                      View
                    </Link>
                  </div>
                )}
                {stats.domains.pending > 0 && (
                  <div className="flex items-start gap-3 p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg">
                    <Clock className="w-5 h-5 text-blue-500 flex-shrink-0 mt-0.5" />
                    <div>
                      <p className="text-sm font-medium text-blue-800 dark:text-blue-200">
                        {stats.domains.pending} domain{stats.domains.pending > 1 ? 's' : ''} pending verification
                      </p>
                      <p className="text-xs text-blue-600 dark:text-blue-400 mt-0.5">
                        Complete DNS verification to activate
                      </p>
                    </div>
                    <Link
                      href="/domains"
                      className="ml-auto text-xs text-blue-600 dark:text-blue-400 hover:underline"
                    >
                      View
                    </Link>
                  </div>
                )}
              </div>
            ) : (
              <div className="py-8 text-center text-gray-500 dark:text-dark-400">
                <CheckCircle className="w-8 h-8 mx-auto mb-2 text-green-500" />
                <p className="text-sm font-medium text-green-600 dark:text-green-400">All systems healthy</p>
                <p className="text-xs mt-1">No active alerts</p>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
