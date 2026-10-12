import { Tabs, TabsLinkTrigger, TabsList } from '@datum-cloud/datum-ui/tabs';
import { useMemo } from 'react';
import { Link, useLocation } from 'react-router';

export type PluginTab = {
  label: string;
  href: string;
};

export function PluginTabs({ tabs, testId }: { tabs: PluginTab[]; testId?: string }) {
  const { pathname } = useLocation();

  const activeHref = useMemo(() => {
    let best = '';
    for (const tab of tabs) {
      if (pathname === tab.href || pathname.startsWith(`${tab.href}/`)) {
        if (tab.href.length > best.length) best = tab.href;
      }
    }
    return best || tabs[0]?.href || '';
  }, [pathname, tabs]);

  return (
    <div className="border-border -mx-4 border-b px-4 md:-mx-9 md:px-9" data-testid={testId}>
      <Tabs value={activeHref} className="gap-0">
        <TabsList variant="line">
          {tabs.map((tab) => (
            <TabsLinkTrigger
              key={tab.href}
              value={tab.href}
              href={tab.href}
              linkComponent={Link}
              className="text-xs md:py-2">
              {tab.label}
            </TabsLinkTrigger>
          ))}
        </TabsList>
      </Tabs>
    </div>
  );
}
