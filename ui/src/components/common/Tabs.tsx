'use client';

import { ReactNode, useState, Children, isValidElement } from 'react';

interface Tab {
  id: string;
  label: string;
  icon?: ReactNode;
  content?: ReactNode;
}

interface TabPanelProps {
  id: string;
  children: ReactNode;
  className?: string;
}

export function TabPanel({ children, className = '' }: TabPanelProps) {
  return (
    <div className={className}>
      {children}
    </div>
  );
}

interface TabsProps {
  tabs: Tab[];
  defaultTab?: string;
  onChange?: (tabId: string) => void;
  children?: ReactNode;
}

export function Tabs({ tabs, defaultTab, onChange, children }: TabsProps) {
  const [activeTab, setActiveTab] = useState(defaultTab || tabs[0]?.id);

  const handleTabChange = (tabId: string) => {
    setActiveTab(tabId);
    onChange?.(tabId);
  };

  // If children are provided, find the matching TabPanel
  let activeContent: ReactNode = null;
  if (children) {
    Children.forEach(children, (child) => {
      if (isValidElement(child) && child.props.id === activeTab) {
        activeContent = child;
      }
    });
  } else {
    // Fall back to content from tabs array
    activeContent = tabs.find((tab) => tab.id === activeTab)?.content;
  }

  return (
    <div>
      {/* Tab Headers */}
      <div className="border-b border-gray-200 dark:border-dark-700">
        <nav className="flex gap-4 -mb-px">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => handleTabChange(tab.id)}
              className={`flex items-center gap-2 px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 dark:text-dark-400 hover:text-gray-700 dark:hover:text-dark-200 hover:border-gray-300 dark:hover:border-dark-600'
              }`}
            >
              {tab.icon}
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab Content */}
      <div className="py-6">{activeContent}</div>
    </div>
  );
}

interface VerticalTabsProps {
  tabs: Tab[];
  defaultTab?: string;
  onChange?: (tabId: string) => void;
  children?: ReactNode;
}

export function VerticalTabs({ tabs, defaultTab, onChange, children }: VerticalTabsProps) {
  const [activeTab, setActiveTab] = useState(defaultTab || tabs[0]?.id);

  const handleTabChange = (tabId: string) => {
    setActiveTab(tabId);
    onChange?.(tabId);
  };

  // If children are provided, find the matching TabPanel
  let activeContent: ReactNode = null;
  if (children) {
    Children.forEach(children, (child) => {
      if (isValidElement(child) && child.props.id === activeTab) {
        activeContent = child;
      }
    });
  } else {
    activeContent = tabs.find((tab) => tab.id === activeTab)?.content;
  }

  return (
    <div className="flex gap-6">
      {/* Tab Headers */}
      <nav className="w-48 flex-shrink-0 space-y-1">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => handleTabChange(tab.id)}
            className={`flex items-center gap-2 w-full px-4 py-2 text-sm font-medium rounded-lg transition-colors ${
              activeTab === tab.id
                ? 'bg-primary-50 dark:bg-primary-900/20 text-primary-600 dark:text-primary-400'
                : 'text-gray-600 dark:text-dark-400 hover:bg-gray-50 dark:hover:bg-dark-800 hover:text-gray-900 dark:hover:text-white'
            }`}
          >
            {tab.icon}
            {tab.label}
          </button>
        ))}
      </nav>

      {/* Tab Content */}
      <div className="flex-1">{activeContent}</div>
    </div>
  );
}
