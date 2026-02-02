# Edge Gateway Manager UI

A comprehensive UI for managing edge gateways, built with Next.js 14 and the Kubernetes JavaScript client.

## Technology Stack

- **Next.js 14** with App Router
- **React 18** with TypeScript
- **TailwindCSS** for styling
- **React Query** (@tanstack/react-query) for client-side data fetching
- **@kubernetes/client-node** for server-side Kubernetes API access
- **Lucide React** for icons

## Features

### Dashboard
- Overview statistics for gateways, domains, policies, and connectors
- Recent activity feed
- System health monitoring
- Quick actions for common tasks

### Gateways (HTTPProxy)
- List view with search, sort, and filtering
- Detailed view with routes, hostnames, and status
- Multi-step form for creating new gateways
- YAML viewer for raw resource inspection

### Domains
- Domain verification management (DNS and HTTP methods)
- Registration information display
- Verification instructions with copy-to-clipboard

### Security Policies (TrafficProtectionPolicy)
- OWASP CRS configuration
- Paranoia level settings
- Rule exclusions management
- Mode switching (Observe/Enforce/Disabled)

### Connectors
- Connection status monitoring
- Advertised services display
- Capabilities overview

## Getting Started

### Prerequisites

- Node.js 20+
- npm

### Installation

```bash
cd ui
npm install
```

### Development (with mock data)

For local development without a Kubernetes cluster:

```bash
NEXT_PUBLIC_USE_MOCK_DATA=true npm run dev
```

### Development (with cluster)

If you have a kubeconfig configured:

```bash
npm run dev
```

The development server will start at http://localhost:3000.

### Build

```bash
npm run build
```

### Production

```bash
npm run build
npm start
```

## Project Structure

```
ui/
├── package.json
├── next.config.js
├── tsconfig.json
├── tailwind.config.js
├── postcss.config.js
├── Dockerfile
├── src/
│   ├── app/
│   │   ├── layout.tsx         # Root layout with providers
│   │   ├── providers.tsx      # Client providers (Theme, React Query)
│   │   ├── page.tsx           # Dashboard
│   │   ├── globals.css        # Global styles
│   │   ├── gateways/          # Gateway pages
│   │   ├── domains/           # Domain pages
│   │   ├── policies/          # Security policy pages
│   │   ├── connectors/        # Connector pages
│   │   └── api/               # API route handlers
│   │       ├── health/        # Health check endpoint
│   │       ├── namespaces/    # Namespace listing
│   │       ├── dashboard/     # Dashboard stats
│   │       ├── v1alpha/       # v1alpha API routes
│   │       └── v1alpha1/      # v1alpha1 API routes
│   ├── api/
│   │   ├── client.ts          # Frontend API client
│   │   └── types.ts           # TypeScript types for CRDs
│   ├── lib/
│   │   ├── k8s.ts             # Server-side Kubernetes client
│   │   └── mock-data.ts       # Mock data for development
│   ├── components/
│   │   ├── layout/            # Layout components (Sidebar, Header, etc.)
│   │   ├── common/            # Reusable UI components
│   │   └── forms/             # Form components
│   └── hooks/                 # Custom React hooks
└── README.md
```

## Kubernetes Deployment

The UI is designed to run in-cluster with automatic service account authentication.

### Deploy with kustomize

```bash
# From repository root
kubectl apply -k config/ui
```

Or include it with the main operator deployment by uncommenting `../ui` in `config/default/kustomization.yaml`.

### Build Docker Image

```bash
cd ui
docker build -t ghcr.io/datum-cloud/edge-gateway-ui:latest .
```

### RBAC

The UI deployment includes:
- **ServiceAccount**: `edge-gateway-ui`
- **ClusterRole** with permissions to:
  - Read namespaces
  - Full CRUD on HTTPProxy, Domain, TrafficProtectionPolicy
  - Read-only access to Connector, ConnectorAdvertisement

### In-Cluster Authentication

When deployed in a Kubernetes cluster, the UI server automatically uses the mounted service account token to authenticate with the Kubernetes API. No additional configuration is required.

## API Routes

The UI exposes the following API endpoints:

| Endpoint | Methods | Description |
|----------|---------|-------------|
| `/api/health` | GET | Health check |
| `/api/namespaces` | GET | List namespaces |
| `/api/dashboard/stats` | GET | Dashboard statistics |
| `/api/v1alpha/namespaces/{ns}/httpproxies` | GET, POST | HTTPProxy list/create |
| `/api/v1alpha/namespaces/{ns}/httpproxies/{name}` | GET, PUT, DELETE | HTTPProxy CRUD |
| `/api/v1alpha/namespaces/{ns}/domains` | GET, POST | Domain list/create |
| `/api/v1alpha/namespaces/{ns}/domains/{name}` | GET, PUT, DELETE | Domain CRUD |
| `/api/v1alpha/namespaces/{ns}/trafficprotectionpolicies` | GET, POST | Policy list/create |
| `/api/v1alpha/namespaces/{ns}/trafficprotectionpolicies/{name}` | GET, PUT, DELETE | Policy CRUD |
| `/api/v1alpha1/namespaces/{ns}/connectors` | GET | Connector list |
| `/api/v1alpha1/namespaces/{ns}/connectors/{name}` | GET | Connector detail |
| `/api/v1alpha1/namespaces/{ns}/connectoradvertisements` | GET | Advertisement list |

## CRD Types

The UI manages the following Kubernetes CRDs:

- **HTTPProxy** (networking.datumapis.com/v1alpha) - HTTP reverse proxy configuration
- **Domain** (networking.datumapis.com/v1alpha) - Domain verification
- **TrafficProtectionPolicy** (networking.datumapis.com/v1alpha) - WAF/security policies
- **Connector** (networking.datumapis.com/v1alpha1) - Edge connectors (read-only)
- **ConnectorAdvertisement** (networking.datumapis.com/v1alpha1) - Advertised services (read-only)

## Theming

The UI supports both light and dark themes:
1. Toggle using the sun/moon icon in the header
2. Preference is stored in localStorage
3. Falls back to system preference

## Components

### Common Components

- `DataTable` - Sortable, filterable, paginated data tables
- `StatusBadge` - Status indicators with icons
- `YamlViewer` - Syntax-highlighted YAML display
- `Card` / `StatCard` - Content containers
- `EmptyState` / `LoadingState` / `ErrorState` - State indicators
- `Modal` / `ConfirmModal` - Dialog windows
- `Tabs` - Tabbed content navigation
- `Button` - Styled buttons with variants

### Form Components

- `Input` / `Textarea` - Text inputs
- `Select` - Dropdown select
- `Checkbox` / `Toggle` - Boolean inputs
- `TagInput` / `KeyValueInput` - Multi-value inputs
- `MultiStepForm` - Wizard-style forms
- `FormSection` / `FormGrid` - Form layout helpers

## License

Copyright Datum Cloud, Inc.
