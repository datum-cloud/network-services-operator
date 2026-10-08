import NetworkList from './pages/network-list';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter, Route, Routes } from 'react-router';

const queryClient = new QueryClient();

const base = '/project/:projectId/services/:serviceSlug';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <div
        style={{ maxWidth: 960, margin: '2rem auto', padding: '0 1rem', fontFamily: 'system-ui' }}>
        <p style={{ opacity: 0.6 }}>
          Standalone preview — the portal loads this plugin via
          <code> /plugin-manifest.json</code> and <code>/remoteEntry.js</code>, not this page. Data
          pages show their error state here (no portal proxy). Run the full portal to see live
          data.
        </p>
        <MemoryRouter initialEntries={['/project/demo-project/services/networking/networks']}>
          <Routes>
            <Route path={`${base}/networks`} element={<NetworkList />} />
          </Routes>
        </MemoryRouter>
      </div>
    </QueryClientProvider>
  </StrictMode>
);
