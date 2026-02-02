'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { ChevronRight, Home } from 'lucide-react';

interface BreadcrumbItem {
  name: string;
  href?: string;
}

interface BreadcrumbProps {
  items?: BreadcrumbItem[];
}

const routeNames: Record<string, string> = {
  gateways: 'Gateways',
  domains: 'Domains',
  policies: 'Security Policies',
  connectors: 'Connectors',
  settings: 'Settings',
  create: 'Create',
  edit: 'Edit',
};

export function Breadcrumb({ items }: BreadcrumbProps) {
  const pathname = usePathname();

  // Auto-generate breadcrumbs from path if not provided
  const breadcrumbs: BreadcrumbItem[] = items || (() => {
    const pathSegments = pathname.split('/').filter(Boolean);
    const crumbs: BreadcrumbItem[] = [];
    let currentPath = '';

    pathSegments.forEach((segment, index) => {
      currentPath += `/${segment}`;
      const isLast = index === pathSegments.length - 1;

      crumbs.push({
        name: routeNames[segment] || segment.charAt(0).toUpperCase() + segment.slice(1),
        href: isLast ? undefined : currentPath,
      });
    });

    return crumbs;
  })();

  if (breadcrumbs.length === 0) {
    return null;
  }

  return (
    <nav className="flex items-center gap-2 text-sm mb-6">
      <Link
        href="/"
        className="text-gray-400 dark:text-dark-400 hover:text-gray-600 dark:hover:text-dark-200 transition-colors"
      >
        <Home className="w-4 h-4" />
      </Link>

      {breadcrumbs.map((item, index) => (
        <div key={index} className="flex items-center gap-2">
          <ChevronRight className="w-4 h-4 text-gray-400 dark:text-dark-500" />
          {item.href ? (
            <Link
              href={item.href}
              className="text-gray-500 dark:text-dark-400 hover:text-gray-700 dark:hover:text-dark-200 transition-colors"
            >
              {item.name}
            </Link>
          ) : (
            <span className="text-gray-900 dark:text-white font-medium">
              {item.name}
            </span>
          )}
        </div>
      ))}
    </nav>
  );
}
