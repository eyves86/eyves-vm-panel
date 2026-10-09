# Introduction

**EyvesCloud** is an enterprise-grade multi-node virtualization platform for LXC / KVM workloads. It unifies scattered host operations into a single control plane, delivering full lifecycle management of cross-node workloads through a web console, a CLI, and a versioned REST API. With a built-in scheduling engine, multi-tenant isolation, metering integration, and security auditing, it serves VPS providers, enterprise infrastructure teams, research labs, and scenarios where container access needs to be distributed in batches.

## Positioning

- **Unified control plane**: one controller manages unlimited worker nodes with a consistent web console, CLI, and API.
- **Metering & billing ready**: full usage export API (resource config + live usage + expiry/traffic) plus idempotent provisioning, ready for direct billing integration.
- **Enterprise multi-tenancy**: sub-users with operator/viewer roles, per-container authorization, tenant quotas, dedicated portal entry, and token version control.
- **Defense in depth**: conntrack threat detection, JWT issuer/audience binding, fine-grained API key scopes, and full operation & login auditing.

## Core Capabilities

### Compute & Orchestration

- Unified management of LXC containers and KVM virtual machines: create, start, stop, restart, reinstall, delete, reset passwords, suspend/unsuspend, and batch operations.
- Intelligent scheduling: filtering (online / capacity / storage backend / maintenance) → scoring (RAM + disk + tenant spread) → decision trail.
- Controller-Agent multi-node: the Controller generates a one-line install script; workers register automatically, and the Controller can view/operate their workloads directly.
- Lifecycle governance: automatic shutdown on expiry or traffic overage.

### Networking & Storage

- NAT4 port quotas, random available ports, TCP/UDP port mappings, and public IPv4 pool management.
- Public IPv6 assignment when the host has IPv6 routing, with prefix detection and status checks.
- Storage pools (dir / ZFS / LVM / RBD / CephFS / NFS), resource quotas, and a policy engine with tenant isolation and per-container authorization.

### Resource Governance

- CPU, memory, disk, Swap, independent download/upload bandwidth, read/write I/O limits, and traffic limits.
- Snapshot create/restore/delete, scheduled snapshots, and snapshot quotas.

### Security & Compliance

- Connection-behavior security alerts (port scans, brute force, SMTP abuse, mining ports, etc.).
- Operation audit logs and login logs, WebSSH origin whitelisting, and panel access policies.

### Self-Service & Integration

- Sub-user access links for specific containers, with view/operate roles and image restrictions.
- Automation via API keys and the `/api/v1` interface; `GET /api/v1/usage` for metering.
- WebSSH and WebVNC directly in the browser.

## Use Cases

| Scenario | Description |
| --- | --- |
| VPS providers | One controller across datacenters; sub-users self-manage their instances; usage API feeds billing for a provision-meter-suspend loop. |
| Enterprise infra teams | Multi-tenant isolation, role-based authorization, and full auditing deliver a self-service portal for dev/test teams. |
| Labs & education | Batch provisioning, template management, and automatic expiry reclamation at low cost. |
| Developer self-hosting | Single-node deployment in minutes; browser-based WebSSH/WebVNC with no jump host. |

## Tech Stack

- **Control plane**: Go (`net/http`), PostgreSQL (config store and telemetry), scheduling engine.
- **Virtualization layer**: LXC, KVM/libvirt, cgroup v2, iptables, conntrack.
- **Data plane**: React, TypeScript, Vite, Tailwind CSS, lucide-react, xterm.js, noVNC.
- **Delivery**: Linux (systemd / OpenRC); GitHub Actions builds Linux AMD64/ARM64 release artifacts; the install script fetches the latest Release by default.

## Originality

EyvesCloud is an **independently written** open-source project: the Go backend, React/TypeScript frontend, agent, controller-agent proxy protocol, REST API, policy engine, and security engine are all implemented from scratch in this repository — there is no source reuse or copy-paste from any existing panel.

A few clarifications:

- **Naming**: The product name "EyvesCloud" is original to this project and unrelated to any third-party product or trademark.
- **Common features, original code**: Where functional shape (controller-agent nodes, NAT/IPv6 networking, WebSSH/WebVNC) resembles other products, that reflects common industry requirements rather than code copying. The implementation (see the [architecture doc](../developer/architecture.md)) is original — including the PostgreSQL persistence model, the JWT + API-key (argon2id) auth system, the node-token proxy protocol, TOTP two-factor auth, the policy engine, and the conntrack-based security engine.
- **Dependencies**: Only the Go standard library / well-known open-source libraries (e.g. `golang.org/x/crypto`, `github.com/jackc/pgx/v5`), the React ecosystem, and Linux system components (LXC, libvirt, iptables) are used, each under its own open-source license.
- **Docs**: This documentation site, the [README](../../README.md), and the [deployment guide](../../DEPLOYMENT.md) are all written originally for this project.

If you find content here that closely matches Project E material, please open an Issue so we can distinguish or remove it.
