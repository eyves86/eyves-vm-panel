# System Architecture

EyvesCloud consists of a Go backend, a React frontend, and host virtualization capabilities, and supports "Controller-Agent" multi-node deployments.

## Backend

The backend entrypoint is `backend/main.go`; HTTP routes are centralized in `backend/internal/server/server.go`. Main modules:

- `internal/api`: HTTP endpoints for the web panel and `/api/v1` (including the Controller node APIs and the Agent API on workers).
- `internal/config`: configuration and SQLite storage.
- `internal/agent`: Agent mode for worker nodes (registration, heartbeat, local panel).
- `internal/lxc`: LXC container management.
- `internal/kvm`: KVM/libvirt virtual machine management.
- `internal/cli`: command-line management entrypoint.
- `internal/server`: embedded static frontend and HTTP serving.
- `internal/safehttp`: HTTP-related security utilities.
- `internal/version`: version number.

## Controller-Agent Architecture

The same binary distinguishes roles by startup arguments:

```text
Controller                      Agent
eyvescloud server                    eyvescloud agent --controller=... --install-key=...
     │                                │
     ├─ /api/nodes (CRUD)             ├─ Register: POST /api/nodes/register
     ├─ /api/nodes/{id}/install-script├─ Heartbeat: POST /api/nodes/{id}/heartbeat
     ├─ /api/nodes/binary             ├─ Local panel: server.Run()
     └─ Proxy: /api/nodes/{id}/containers ──► /api/agent/* (node token auth)
```

- **Controller**: routes under `/api/nodes` handle node CRUD, install script generation, binary download, heartbeat reception, and container proxying.
- **Agent**: `internal/agent` handles registration and heartbeat; `agent_api.go` in `internal/api` provides the `/api/agent/*` endpoints, called by the Controller with the node token.
- **Authentication**: the Controller side uses an administrator JWT or API key scopes (`node:read`, `node:write`); the Agent side validates the Controller identity with the node token.

### Registration Flow

1. The Controller creates a node and generates an `install_key` and `token`.
2. The worker runs the install script, and `POST /api/nodes/register` carries the install key.
3. The Controller matches the node by install key and returns the `node_id` and `token`.
4. The worker saves the registration info to `agent.json` and then reports heartbeats with the node token.
5. The Controller updates the node status to online and can access the worker's containers through the proxy endpoints.

### Heartbeat

The worker reports a heartbeat every 10 seconds carrying version, OS, CPU, memory, disk, and container count. The Controller writes it to the node record and updates `last_seen`.

### Proxy Access

When the Controller receives a `/api/nodes/{id}/containers` request, it issues an `/api/agent/*` request to the worker's `node.Address` with the node token and returns the response to the browser as-is.

## Frontend

The frontend entrypoint is `frontend/src/main.tsx`; pages live in `frontend/src/pages` and shared components in `frontend/src/components`.

Main pages:

- Dashboard: `Dashboard.tsx`
- Container list: `Containers.tsx`
- Container details: `ContainerDetail.tsx`
- Image management: `ImageManagement.tsx`
- Node management: `NodeManagement.tsx` (Controller-Agent)
- Node migration: `NodeMigration.tsx`
- Policy management: `PolicyManagement.tsx`
- Storage: `Storage.tsx`
- Security alerts: `Security.tsx`
- Snapshots: `Snapshots.tsx`
- Routing: `Routing.tsx`
- Audit logs: `AuditLogs.tsx`
- API integration: `ApiIntegration.tsx`
- Host report: `HostReport.tsx`
- Settings: `Settings.tsx`
- Sub-user management: `SubUserManagement.tsx`

## Frontend Embedding

In production builds, the frontend output is placed in `backend/internal/server/web` and served by the backend through Go embed, which also returns the SPA entry for non-API routes.

## API Layers

- `/api/*`: web panel and compatibility endpoints (including the `/api/nodes` Controller endpoints and `/api/agent/*` worker endpoints).
- `/api/v1/*`: versioned endpoints recommended for external automation systems.
- WebSSH and WebVNC establish WebSocket connections after short-lived tickets.

## Data Storage

Configuration and business data are stored in SQLite (`config.db`). Containers, sub-users, API keys, audit logs, policies, nodes, etc. are all persisted to SQLite and survive restarts. Worker registration info (`agent.json`) is stored in the worker's own configuration directory.
