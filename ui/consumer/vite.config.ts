import { federation } from '@module-federation/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The host (cloud-portal) loads this remote via @module-federation/runtime and
// provides react / react-dom / react-router / @tanstack/react-query as shared
// singletons, so the plugin renders with the host's exact React, router, and
// query client. `shared` below marks those as singletons with the host's
// version so the host copy always wins and React is never duplicated (two
// React instances break hooks).
//
// Assets are fetched server-side by the portal's asset proxy and served under
// /api/plugins/<slug>/…, so plain http://localhost during dev is fine and the
// browser never contacts this origin directly. MF's automatic publicPath makes
// federated chunks resolve relative to remoteEntry.js, which is what lets them
// load correctly through that same-origin proxy prefix.
export default defineConfig({
  server: {
    port: 7779,
    strictPort: true,
    // Allow cross-origin fetches of the manifest/remote during Tier 0/standalone.
    cors: true,
  },
  preview: {
    port: 7779,
    strictPort: true,
    cors: true,
  },
  build: {
    target: 'esnext',
    // Keep the plugin readable when inspecting the built bundle.
    minify: false,
  },
  plugins: [
    react(),
    federation({
      // MUST equal the manifest `name` — the host keys the remote by this id.
      name: 'network.networking.datumapis.com',
      // The manifest's `remoteEntry` field points the host at this filename,
      // requested through the asset proxy as /api/plugins/networking/remoteEntry.js.
      filename: 'remoteEntry.js',
      manifest: true,
      // Exposed keys map 1:1 to the manifest's `exposedModules` keys / $codeRefs.
      // The host loads e.g. loadRemote('network.networking.datumapis.com/NetworkList').
      exposes: {
        './NetworkList': './src/pages/network-list.tsx',
        './NetworkDetail': './src/pages/network-detail.tsx',
      },
      // Host-pinned singletons. requiredVersion tracks the host's majors
      // (react 19, react-router 7, react-query 5). singleton:true guarantees
      // one instance — the host provides all of these, so plugin queries share
      // the host's QueryClient cache.
      shared: {
        react: { singleton: true, requiredVersion: '^19.0.0' },
        'react-dom': { singleton: true, requiredVersion: '^19.0.0' },
        'react-router': { singleton: true, requiredVersion: '^7.0.0' },
        '@tanstack/react-query': { singleton: true, requiredVersion: '^5.0.0' },
        // Curated datum-ui subset shared by the host (see the host's
        // federation-host.ts DATUM_UI_SHARED). requiredVersion:false — the
        // host's copy always wins, which is what keeps styling identical to
        // built-in pages; the local install is types + standalone fallback.
        //
        // Every datum-ui subpath this plugin imports MUST be listed here.
        // Anything left out (e.g. `toast`) bundles its own copy with its own
        // React context/portal root instead of the host's — for `toast`
        // specifically, that means the plugin's toasts render into a
        // <Toaster> the host never mounted and never appear at all.
        '@datum-cloud/datum-ui/badge': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/breadcrumb': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/button': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/card': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/checkbox': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/dialog': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/empty-content': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/hooks': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/icons': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/input': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/input-group': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/input-number': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/page-title': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/popover': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/select': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/skeleton': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/table': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/tabs': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/toast': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/utils': { singleton: true, requiredVersion: false },
      },
    }),
  ],
});
