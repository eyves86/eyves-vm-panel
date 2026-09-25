# API Integration

EyvesCloud exposes a versioned HTTP API (`/api/v1`) covering the container lifecycle, networking and ports, snapshots and backups, images, the task queue, security, audit logs, and usage export. It is used by the web panel, the agent nodes, and third-party billing / operations systems.

This chapter documents authentication, request and response conventions, permission scopes, the full endpoint reference, and a billing integration walkthrough. The machine-readable contract is available at `GET /api/v1/openapi.json` (OpenAPI 3.0).

## Base URL and Versioning

- Versioned prefix: `/api/v1` (recommended; new endpoints land here first).
- Legacy prefix: `/api` (kept available for compatibility).
- Version info: `GET /api/version`.
- Health checks: `GET /api/v1/health`, `GET /api/v1/health/detail` (admin only).

All endpoints return `Content-Type: application/json`.

## Authentication

Three identity types are supported, all resolved from request headers:

| Identity | Header | Notes |
| --- | --- | --- |
| Administrator | `Authorization: Bearer <JWT>` | Issued by the login endpoint (optionally with 2FA); valid for 24 hours |
| API key | `X-API-Key: eyvescloud_sk_xxxx` | Also accepted as `Authorization: Bearer eyvescloud_sk_xxxx` |
| Sub-user | `Authorization: Bearer <JWT>` | Issued by the sub-user login endpoint; constrained by role and bound containers |

### Administrator login

```http
POST /api/v1/login
Content-Type: application/json

{ "username": "admin", "password": "your-password", "twofa_code": "" }
```

```json
{
  "success": true,
  "data": { "token": "eyJhbGciOi...", "username": "admin" }
}
```

When two-factor authentication is enabled and `twofa_code` is missing:

```json
{ "success": false, "message": "Two-factor verification required", "data": { "twofa_required": true } }
```

Use the returned `token` as `Authorization: Bearer <token>` on subsequent calls. `GET /api/v1/check-auth` validates the current identity and returns its type, role, bound containers, and effective scopes.

> Changing the administrator password increments the token version, so every previously issued administrator token is invalidated immediately.

### API keys

API keys are created on the **API Integration** page. The plaintext value is shown only once, formatted as `eyvescloud_sk_` plus 32 hex characters. Each key supports:

- `scopes`: permission scopes, including the `*`, `admin:*`, and `service:*` wildcards.
- `ip_whitelist`: newline-separated IPs or CIDRs; empty means no restriction.
- `expires_at`: expiry timestamp (`YYYY-MM-DD HH:MM:SS`); empty means never expires.
- `container_uuids`: bound containers; empty means all containers.
- `disabled`: whether the key is disabled.

A restricted key cannot escalate itself: when creating or updating a key, the caller may only grant a subset of its own scopes, and management scopes such as `apikey:*` and `admin:*` are rejected.

### Sub-users

Administrators create sub-users and bind containers to them. Roles are `operator` (read/write) or `viewer` (read only). A `viewer` is limited to read and connection scopes; power control, reinstall, and password changes are denied.

### Administrator roles

An administrator account also carries a role: `admin` (full), `operator` (write access, but not sensitive configuration management), or `readonly`. Unrecognized values fall back to `readonly`.

## Request and Response Conventions

### Response envelope

```json
{
  "success": true,
  "code": "NOT_FOUND",
  "message": "Container not found",
  "data": {}
}
```

- `success`: whether the call succeeded.
- `code`: machine-readable error code, **only present on failure**.
- `message`: human-readable message.
- `data`: payload; may be an object, an array, or omitted.

### Error codes

| Code | Meaning |
| --- | --- |
| `INVALID_REQUEST` | Invalid parameters |
| `NOT_FOUND` | Resource not found |
| `FORBIDDEN` | Permission denied |
| `INSUFFICIENT_SCOPE` | API key scope is insufficient |
| `RATE_LIMITED` | Rate limit exceeded |
| `BAD_GATEWAY` | Downstream agent node failure |
| `INTERNAL_ERROR` | Internal server error |

HTTP status codes mirror the semantics: `400` bad request, `401` unauthenticated, `403` forbidden, `404` not found, `405` method not allowed, `409` conflict (quota / port in use), `429` rate limited, `500` internal error.

### Pagination

List endpoints accept optional pagination parameters. **Without them the response stays unpaginated** (backward compatible):

```http
GET /api/v1/audit-logs?page=1&page_size=50
```

`page` starts at 1. `page_size` defaults to 50 and is capped at 200. When pagination is requested, `data` becomes `{ "items": [], "total": 0, "page": 1, "page_size": 50 }`.

### DryRun

Creation endpoints support `dry_run`, which runs every validation and returns the planned result without persisting anything or calling the runtime:

```http
POST /api/v1/containers?dry_run=true
```

Either a `{"dry_run": true}` body or a `?dry_run=true` query parameter activates it.

### Idempotent provisioning

Pass an `Idempotency-Key` header when creating a container to prevent duplicate provisioning when a billing callback times out and retries:

```http
POST /api/v1/containers
Idempotency-Key: order-20260101-0001

{ "name": "web-01", "template_id": "alpine-3.21", ... }
```

Retrying with the same key returns the existing container (`success=true`, `message` is `Container already exists (idempotent)`) instead of creating another one.

### Task queue

Long-running operations such as start/stop, reinstall, delete, and create are executed asynchronously through the task queue. Poll the returned task ID for progress:

```http
GET /api/v1/tasks
GET /api/v1/tasks/{task_id}
DELETE /api/v1/tasks/{task_id}
GET /api/v1/tasks/stats
```

A task carries `status` (`pending`/`running`/`completed`/`failed`), `stage`, `percent`, and `error`.

## Permission Scopes

The following scopes can be granted to an API key (`*` means everything):

| Group | Scope | Description |
| --- | --- | --- |
| Overview & read-only | `dashboard:read` | Dashboard statistics |
| | `host:read` | Host resources |
| | `routing:read` / `routing:write` | Routing read / write |
| | `ipv6:read` | IPv6 status |
| | `task:read` / `task:delete` | Task read / delete |
| | `image:read` | Image list |
| Containers | `container:read` | View containers |
| | `container:create` | Create containers |
| | `container:power` | Power on/off and reboot |
| | `container:reinstall` | Reinstall |
| | `container:delete` | Delete containers |
| | `container:resize` | Resources and expiry |
| | `container:traffic` | Traffic management |
| | `container:network` | Networking and port mappings |
| | `container:password` | Reset password |
| | `container:account` | In-container accounts |
| | `container:ssh-key` | Container SSH keys |
| | `ipv6:assign` | Assign IPv6 |
| Snapshots & terminal | `snapshot:read` / `snapshot:create` / `snapshot:delete` / `snapshot:restore` / `snapshot:schedule` | Snapshot view / create / delete / restore / schedule |
| | `terminal:ssh` / `terminal:vnc` | WebSSH / WebVNC tickets |
| Platform | `image:download` / `image:delete` / `image:toggle` | Image download / delete / toggle |
| | `security:read` / `security:check` / `security:settings` | Security data / scan / settings |
| | `swap:read` / `swap:manage` | Swap read / manage |
| | `subuser:read` / `subuser:create` / `subuser:update` | Sub-users |
| | `audit:read` / `loginlog:read` | Audit logs / login logs |
| | `usage:read` | Usage export (for billing) |
| | `apikey:read` / `apikey:create` / `apikey:update` / `apikey:delete` | API key management |
| | `admin:access` | Admin endpoints (equivalent to panel administration) |

`admin:access` and `apikey:*` are management scopes and can only be granted by an administrator session.

## Endpoint Reference

All paths below omit the `/api/v1` prefix. Endpoints marked **admin** require an administrator session or an API key with `admin:access`.

### Login and Accounts

| Method | Path | Description |
| --- | --- | --- |
| POST | `/login` | Administrator login, returns a JWT |
| GET | `/check-auth` | Validate the current identity and effective scopes |
| POST | `/sub-user/login` | Sub-user login |
| POST | `/sub-user/access` | Login with an access code |
| POST | `/sub-user/change-password` | Sub-user password change |
| POST | `/change-password` | Change the primary administrator password (own session only) |
| POST | `/change-username` | Change the primary administrator username (own session only) |
| GET | `/2fa/status` `/2fa/setup` `/2fa/enable` `/2fa/disable` `/2fa/regenerate-backup-codes` | Two-factor authentication (primary admin session only) |

### Overview and Host

| Method | Path | Description |
| --- | --- | --- |
| GET | `/dashboard` | Dashboard statistics |
| GET | `/host-info` | Host resources |
| GET | `/host-history` | Host metric history |
| GET | `/host-report` | Host hardware / network / environment report |
| GET | `/monitoring/containers` | Batch container metrics |
| GET | `/storage` / PUT `/storage` | Storage disks and pools (admin) |
| GET | `/routing` / PUT `/routing` | NAT / IPv4 / IPv6 routing (admin) |
| POST | `/routing/ipv4-scan` | Scan a public IPv4 range (admin) |
| GET | `/ipv6/status` | IPv6 status |
| GET | `/task-queue/settings` / PUT | Task concurrency settings (admin) |
| GET | `/overcommit/settings` | Overcommit settings (admin) |
| GET | `/metrics/retention` | Metric retention policy (admin) |

### Containers

| Method | Path | Description |
| --- | --- | --- |
| GET | `/containers` | List containers |
| GET | `/containers/list` | List alias (trimmed fields) |
| POST | `/containers` | Create a container (`Idempotency-Key`, `dry_run` supported) |
| GET | `/containers/{id\|uuid\|name}` | Container details |
| POST | `/containers/{id}/start` | Power on |
| POST | `/containers/{id}/stop` | Power off |
| POST | `/containers/{id}/restart` | Reboot |
| POST | `/containers/{id}/reinstall` | Reinstall |
| POST | `/containers/{id}/suspend` | Suspend (billing suspension; optional body `reason`) |
| POST | `/containers/{id}/unsuspend` | Unsuspend (does not power on automatically) |
| DELETE | `/containers/{id}/delete` | Delete |
| POST | `/containers/{id}/reset-password` | Reset SSH password (optional body `password`) |
| POST | `/containers/{id}/create-account` | Create an in-container account |
| PUT | `/containers/{id}/resource-limit` | Adjust resource limits (scale up only) |
| PUT | `/containers/{id}/traffic-limit` | Adjust traffic limits |
| POST | `/containers/{id}/traffic-reset` | Reset used traffic |
| PUT | `/containers/{id}/expiry` | Set expiry (body: `expires_at`) |
| PUT | `/containers/{id}/tenant` | Set tenant |
| PUT | `/containers/{id}/tags` | Replace resource tags (max 20, key/value ≤128 chars) |
| PUT | `/containers/{id}/owner` | Change owning sub-user (management side only) |
| POST | `/containers/{id}/clone` | Clone a container |
| POST | `/containers/{id}/hostname` | Change hostname |
| POST | `/containers/{id}/vnc-password` | Change the VNC password |
| GET | `/containers/{id}/usage` | Live usage |
| GET | `/containers/{id}/traffic` | Traffic statistics |
| GET | `/containers/{id}/history` | Historical metric series |
| POST | `/containers/rescue` | KVM rescue mode (admin) |
| POST | `/batch-create` | Batch create |
| POST | `/batch-action` | Batch power / delete / reinstall |

#### Create container request body

```json
{
  "name": "web-01",
  "virtualization": "lxc",
  "template_id": "alpine-3.21",
  "vcpu": 2,
  "ram_mb": 1024,
  "disk_gb": 20,
  "network_bw_mbps": 100,
  "monthly_traffic_gb": 500,
  "traffic_mode": "total",
  "assign_nat": true,
  "port_mapping_count": 2,
  "extra_ports": [80, 443],
  "assign_ipv4": false,
  "assign_ipv6": false,
  "ssh_auth_mode": "auto_password",
  "ssh_password": "",
  "ssh_public_key": "",
  "snapshot_limit": 3,
  "tenant": "customer-1001",
  "expires_at": "2026-12-31 23:59:59"
}
```

Key fields:

| Field | Type | Description |
| --- | --- | --- |
| `name` | string | Container name (required, unique) |
| `virtualization` | string | `lxc` or `kvm` |
| `template_id` | string | Template / image ID (required, must be enabled) |
| `vcpu` | number | vCPU count, defaults to 1 |
| `ram_mb` | int | Memory in MB, minimum 128, defaults to 512 |
| `disk_gb` | number | System disk in GB, defaults to 5 |
| `data_disk_gb` | number | Data disk in GB (optional) |
| `network_bw_mbps` | int | Bandwidth limit in Mbps, `0` means unlimited |
| `monthly_traffic_gb` | int | Monthly traffic in `total` mode, `0` means unlimited |
| `traffic_mode` | string | `total` or `in_out` |
| `traffic_in_gb` / `traffic_out_gb` | int | Inbound / outbound traffic in `in_out` mode |
| `io_speed_mbps` | int | Disk IO limit, `0` means unlimited |
| `assign_nat` | bool | Whether to allocate NAT port mappings |
| `port_mapping_count` | int | Number of NAT ports (≤64) |
| `extra_ports` | int[] | Extra container ports to map |
| `nat_port_mappings` | object[] | Explicit NAT mappings |
| `assign_ipv4` / `public_ipv4s` | bool / string[] | Dedicated public IPv4 |
| `assign_ipv6` / `ipv6_addresses` | bool / string[] | IPv6 addresses |
| `ssh_auth_mode` | string | `auto_password` / `password` / `key` / `keep` |
| `ssh_password` | string | Used in `password` mode |
| `ssh_public_key` | string | Used in `key` mode |
| `ssh_key_ids` | string[] | Platform-managed SSH key IDs (`SK-xxx`) |
| `snapshot_limit` | int | Maximum number of snapshots |
| `tenant` | string | Tenant identifier |
| `expires_at` | string | Expiry time; must be in the future |
| `allowed_image_ids` | string[] | Allowed image whitelist for the container |

A successful creation returns `201`; a DryRun returns `200` with `data.dry_run=true`.

### Networking and Ports

| Method | Path | Description |
| --- | --- | --- |
| GET | `/containers/{id}/random-port` | Get an available NAT port |
| POST | `/containers/{id}/port-mappings` | Add a port mapping |
| PUT | `/containers/{id}/port-mappings/{index}` | Update a port mapping |
| DELETE | `/containers/{id}/port-mappings/{index}` | Delete a port mapping |
| GET | `/containers/{id}/firewall` / PUT | Firewall rules |
| GET | `/containers/{id}/rdns` / PUT | Reverse DNS |
| POST | `/containers/{id}/ipv6` | Allocate IPv6 |
| PUT | `/containers/{id}/ipv6-addresses` | Update dedicated IPv6 |
| PUT | `/containers/{id}/public-ipv4` | Update dedicated public IPv4 |
| GET | `/security-groups` / POST | List / create security groups |
| GET/PUT/DELETE | `/security-groups/{id}` | Security group details |
| GET/POST/PUT/DELETE | `/security-groups/{id}/rules` | Security group rules |
| GET | `/ip-groups` / POST | IP groups (admin) |
| GET | `/regions` / POST | Regions (admin) |
| GET | `/isos` / POST `/isos/upload` | ISO list / upload (admin) |
| POST | `/isos/attach` | Attach / detach an ISO |

### Snapshots, Backups, and Migration

| Method | Path | Description |
| --- | --- | --- |
| GET | `/snapshots` | Snapshot overview |
| GET | `/containers/{id}/snapshots` | Container snapshots |
| POST | `/containers/{id}/snapshots` | Create a snapshot |
| DELETE | `/containers/{id}/snapshots/{snapshotID}` | Delete a snapshot |
| POST | `/containers/{id}/snapshots/{snapshotID}/restore` | Restore a snapshot |
| GET | `/containers/{id}/backups` | Instance backups |
| POST | `/containers/{id}/backups` | Create an instance backup |
| GET | `/backup/list` | Backup archives (admin) |
| POST | `/backup` | Create a backup (admin) |
| POST | `/backup/restore` | Restore from backup (admin) |
| GET | `/backup/download` | Download a backup (admin) |
| GET/PUT | `/backup/settings` | Backup settings (admin) |
| GET/PUT | `/backup/remote-settings` | Remote backup settings (admin) |
| POST | `/backup/remote-test` | Remote backup connectivity test (admin) |
| GET | `/backup-plans` / POST | Backup plans |
| GET | `/containers/{id}/migrate-export` | Export a migration package |
| POST | `/migrate/import` | Import a migration package (admin) |

### Templates and Images

| Method | Path | Description |
| --- | --- | --- |
| GET | `/templates` | Available templates (LXC templates + KVM images) |
| GET | `/images` | Image list (admin) |
| GET | `/images/enabled` | Enabled images usable for create / reinstall |
| POST/DELETE | `/images/custom` | Add / remove third-party image sources (admin) |
| POST | `/images/download` / `/images/cancel` | Download / cancel (admin) |
| DELETE | `/images/delete` | Delete image cache (admin) |
| PUT | `/images/toggle` | Enable / disable an image (admin) |

### Tasks and Console

| Method | Path | Description |
| --- | --- | --- |
| GET | `/tasks` | Task queue |
| GET | `/tasks/history` | Task history |
| GET | `/tasks/stats` | Task statistics |
| GET/DELETE | `/tasks/{task_id}` | Task details / delete record |
| POST | `/ssh-ticket` | Create a one-time WebSSH ticket |
| POST | `/vnc-ticket` | Create a one-time WebVNC ticket |
| WS | `/api/ssh` | WebSSH WebSocket |
| WS | `/api/vnc` | WebVNC WebSocket |

Tickets are short-lived. Use them immediately to establish the connection and never persist them.

### Sub-users and Tenants

| Method | Path | Description |
| --- | --- | --- |
| GET | `/sub-users` | List sub-users |
| POST | `/sub-user/create` | Create a sub-user (optionally bound to containers / tenant) |
| GET/PUT/DELETE | `/sub-users/{id}` | Details / update / delete |
| POST | `/sub-users/{id}/rotate-password` | Rotate password |
| GET | `/sub-users/{id}/audit-logs` / `/login-logs` | Sub-user logs |
| GET | `/tenants` / POST | List / create tenants (admin) |
| GET/PUT/DELETE | `/tenants/{id}` | Tenant details (admin) |

### Security and Audit

| Method | Path | Description |
| --- | --- | --- |
| GET | `/security/alerts` | Security alerts (scope `security:read`) |
| POST | `/security/check` | Run a security check now (scope `security:check`) |
| GET | `/security/logs` | Security connection logs (scope `security:read`) |
| GET | `/security/summary` | Container security summary |
| GET | `/security/abuse-summary` | Abuse summary |
| GET/PUT | `/security/settings` | Security settings |
| GET | `/audit-logs` | Audit logs |
| GET | `/audit-logs/export` | Export audit logs (CSV/JSON) |
| GET | `/login-logs` | Login logs |
| GET | `/api-keys` / POST | List / create API keys |
| PATCH/DELETE | `/api-keys/{id}` | Update / delete an API key |

### Settings and Notifications

| Method | Path | Description |
| --- | --- | --- |
| GET/PUT | `/ssl` | SSL certificate settings (admin) |
| GET/PUT | `/webssh-origins` | WebSSH / VNC origin allowlist (admin) |
| GET/PUT | `/access-policy` | Panel access policy (admin) |
| GET/PUT | `/admin-path` | Admin entry path |
| GET/PUT | `/notifications` | Notification settings (admin) |
| POST | `/notifications/test` | Notification test (admin) |
| GET/PUT | `/smtp` , POST `/smtp/test` | Mail settings (admin) |
| GET/PUT | `/language` | Panel language |
| GET/POST | `/admins` | Administrator accounts (admin) |
| GET/PUT/DELETE | `/admins/{id}` | Administrator details |
| GET/POST | `/policies` , `/policies/{id}` | Policy management (admin) |
| GET/POST | `/ssh-keys` , `/ssh-keys/{id}` | Platform-managed SSH keys |
| GET/POST | `/recipes` , `/recipes/{id}` | Custom script templates |
| GET | `/swap` / POST | Swap info / adjust |

### Webhooks (Event Subscriptions)

| Method | Path | Description |
| --- | --- | --- |
| GET | `/webhooks` | List webhooks (admin) |
| POST | `/webhooks` | Create a webhook (admin) |
| GET/PUT/DELETE | `/webhooks/{id}` | Details / update / delete (admin) |
| POST | `/webhooks/{id}/test` | Test delivery (admin) |

Callback contract:

```text
POST <callback_url>
Content-Type: application/json
X-EyvesCloud-Signature: sha256=<hex(HMAC-SHA256(secret, body))>
X-EyvesCloud-Delivery: <unique id per delivery, for receiver-side dedup>
```

```json
{
  "event_type": "container.status_changed",
  "timestamp": "2026-01-01T12:00:00Z",
  "data": { "container_id": 12, "name": "web-01", "old_status": "running", "new_status": "stopped" }
}
```

The receiver must return `2xx`. Non-`2xx` responses trigger retries (3 attempts with exponential backoff: 1s / 5s / 25s). After 10 consecutive failures the subscription is disabled automatically and the reason is recorded.

### OpenAPI Contract

```http
GET /api/v1/openapi.json
```

Returns an OpenAPI 3.0 document describing the versioned endpoints, authentication, and core data structures.

## Billing Integration

EyvesCloud provides a complete provisioning → metering → invoicing → suspend/reactivate loop. Billing itself can remain in an external system such as WHMCS.

### Usage export

```http
GET /api/v1/usage?tenant=customer-1001&include=config_only
```

- Scope: `usage:read` (administrator session or an API key explicitly granted that scope; sub-users do not have it).
- `tenant`: optional tenant filter.
- `include=config_only`: optional; return configuration only without querying live usage.

```json
{
  "success": true,
  "data": {
    "generated_at": "2026-01-01T12:00:00Z",
    "filter_tenant": "customer-1001",
    "count": 1,
    "count_by_tenant": { "customer-1001": 1 },
    "containers": [
      {
        "uuid": "c-1a2b3c",
        "name": "web-01",
        "tenant": "customer-1001",
        "virtualization": "lxc",
        "vcpu": 2,
        "ram_mb": 1024,
        "disk_gb": 20,
        "status": "running",
        "suspended": false,
        "expires_at": "2026-12-31 23:59:59",
        "created_at": "2026-01-01 10:00:00",
        "traffic": {
          "monthly_limit_gb": 500,
          "mode": "total",
          "used_rx_bytes": 1073741824,
          "used_tx_bytes": 536870912,
          "used_rx_gb": 1.0,
          "used_tx_gb": 0.5,
          "reset_date": "2026-01"
        }
      }
    ]
  }
}
```

The response deliberately excludes sensitive fields such as the SSH password. Combined with the endpoints below it completes the suspend/reactivate loop:

| Scenario | Endpoint |
| --- | --- |
| Suspension for non-payment | `POST /containers/{id}/suspend`, body `{"reason": "overdue"}` |
| Reactivation after payment | `POST /containers/{id}/unsuspend` |
| Expiry adjustment | `PUT /containers/{id}/expiry`, body `{"expires_at": "..."}` |
| Plan change | `PUT /containers/{id}/resource-limit`, `PUT /containers/{id}/traffic-limit` |
| Reinstall | `POST /containers/{id}/reinstall`, body `{"template_id": "..."}` |

Suspension force-stops the container and blocks `start`/`restart`/`reinstall`/WebSSH/VNC. Unsuspending does not power the container back on.

### Example (Python)

```python
import requests

BASE = "https://panel.example.com/api/v1"
HEADERS = {"X-API-Key": "eyvescloud_sk_xxxxxxxx"}

# 1. Fetch all usage
usage = requests.get(f"{BASE}/usage", headers=HEADERS, params={"tenant": "customer-1001"}).json()
for item in usage["data"]["containers"]:
    print(item["name"], item["status"], item["traffic"]["used_rx_gb"], "GB")

# 2. Suspend for non-payment (idempotent at the business layer)
cid = usage["data"]["containers"][0]["uuid"]
requests.post(f"{BASE}/containers/{cid}/suspend", headers=HEADERS, json={"reason": "overdue"})

# 3. Reactivate after payment
requests.post(f"{BASE}/containers/{cid}/unsuspend", headers=HEADERS)
```

### Idempotent provisioning

When a billing system creates containers after a payment callback, always send an `Idempotency-Key` (using the order ID is recommended) so a retried callback cannot provision a duplicate.

## WHMCS Server Module

The panel ships a built-in WHMCS 9.0 server provisioning module (LXC/KVM). Billing stays in WHMCS; the module maps the WHMCS product/service lifecycle to the panel API. Administrators download it from the **API Integration** page and, after installation, add it as a WHMCS server.

```http
GET /api/v1/integrations/whmcs
```

Returns module metadata: name, display name, version, install path, file list, and README.

```json
{
  "success": true,
  "data": {
    "name": "eyvescloud",
    "display_name": "EYVESCLOUD WHMCS Server Module",
    "version": "1.0.0",
    "install_path": "modules/servers/eyvescloud",
    "panel_version": "1.6.2",
    "files": [{ "path": "eyvescloud.php", "size": 12345 }],
    "readme": "# EYVESCLOUD WHMCS Server Module ..."
  }
}
```

```http
GET /api/v1/integrations/whmcs/download
```

Returns a zip archive (`Content-Type: application/zip`) whose top-level directory is `modules/servers/eyvescloud/`. Extract it into the WHMCS root to install.

Both endpoints are **administrator only**. Downloading with `curl`:

```bash
TOKEN="<administrator JWT>"
curl -H "Authorization: Bearer $TOKEN" \
  -o eyvescloud-whmcs-module-1.0.0.zip \
  https://panel.example.com/api/v1/integrations/whmcs/download
```

Module capabilities:

| Category | Content |
| --- | --- |
| Lifecycle | Create, suspend, unsuspend, terminate, power on/off, reboot, password change, package change, status sync, usage reporting |
| Client area | Instance info, NAT forwarding, firewall, snapshots, backups, ISO mount (KVM), reinstall |
| Console | WebSSH, VNC (KVM) |
| Auth | Put the panel API key in the server Access Hash (or password); requests carry `X-API-Key` and `Authorization: Bearer` |

The API key used by the module needs the following scopes (or simply `*`):

```text
container:read / container:create / container:power / container:delete
container:password / container:reinstall / container:resize / container:traffic / container:network
image:read            # list available images/templates when reinstalling
snapshot:read / snapshot:create / snapshot:restore / snapshot:delete
terminal:ssh / terminal:vnc / task:read / dashboard:read
admin:access   # only needed for ISO listing/attach in the KVM client area
```

When a scope is missing, the corresponding feature returns `INSUFFICIENT_SCOPE`; other features keep working.

For configuration and troubleshooting details, see the module's `README.md` (also returned by the metadata endpoint).

## Related Documentation

- [Container Management](./containers.md)
- [Networking & Routing](./networking.md)
- [Snapshots](./snapshots.md)
- [Instance Backups](./backups.md)
- [Security Alerts](./security.md)
- [Sub-users](./sub-users.md)