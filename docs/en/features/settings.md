# System Settings

The System Settings page centralizes panel-level configuration, with the following groups.

## SSL Certificates

- Supports four modes: disabled, Let's Encrypt, self-signed, and uploaded certificate.
- After configuring the target domain and email, Let's Encrypt certificates can be issued/renewed automatically.
- After saving, restart the service immediately to apply, or restart the `systemd` service manually later.

## Panel Access Sources

- Enables the panel access allowlist, allowing only specified IPs/networks to reach the web panel.
- Configures trusted reverse proxy addresses to avoid false blocks behind a proxy.
- Equivalent to the `eyvescloud access-policy` CLI; takes effect immediately after saving.

## Notifications

- Security alerts can be pushed via Webhook.
- SMTP email push is supported; server, port, account, and recipients are configurable.

## Task Queue

- Configure the task queue concurrency to control how many reinstall/create/image tasks run at the same time.
- Takes effect immediately after saving.

## Account

- Change the administrator username and password.
- The current password is required for confirmation.

## Miscellaneous

- WebSSH/WebVNC Origin allowlist.
- Language setting (Simplified Chinese / English).

## Memory Overcommit and KSM

Memory overcommit raises the "allocatable memory = physical memory × overcommit ratio" above the physical RAM; KSM (Kernel Samepage Merging) merges duplicate memory pages to reduce real RAM usage under overcommit scenarios.

```http
GET /api/overcommit/settings
PUT /api/overcommit/settings
```

`PUT` request body (administrator only):

| Field | Description |
| --- | --- |
| `memory_overcommit_enabled` | Whether to enable memory overcommit |
| `memory_overcommit_ratio` | Overcommit ratio, range `1.0`–`16.0` |
| `ksm_tuning` | Object with `enabled`, `pages_to_scan` (0–1000000), `sleep_millisecs` (1–60000), and `use_tune_ksm` |

The response also includes `physical_ram_mb` (physical RAM) and `allocatable_ram_mb` (allocatable RAM). After saving, the KSM kernel parameters are applied immediately; if they cannot be written, a warning is returned instead of failing.

## Metric Retention

The panel samples container and host metrics periodically and rolls them up into hourly trends. You can configure how many days to keep; data older than the retention period is pruned in the background to prevent unbounded disk growth.

```http
GET /api/metrics/retention
PUT /api/metrics/retention
```

`PUT` request body (administrator only): `{"retention_days": N}` with range `0`–`3650`; `0` means keep forever. The response also includes `sample_interval_secs` and `raw_history_secs`.

## Panel Version Check

The "Version" page in settings checks whether an update is available.

```http
GET /api/v1/check-update
```

Administrator only. Returns `current` (current version), `latest` (latest version), `has_update` (whether an update exists), and `err` (any check error). The result is cached for 10 minutes. It only checks and never downloads; upgrades are performed by install.sh / the CLI.

## Account / Password Recovery Commands

If you forget the administrator password, use the `eyvescloud account` CLI (run directly on the server over SSH) to view the account or reset the password:

```bash
eyvescloud account                                    # Show admin account & 2FA status (alias: eyvescloud kvm)
eyvescloud account reset                              # Reset admin password (auto-generates a strong one, shown once)
eyvescloud account reset --password <new-password>    # Set a custom new password (at least 10 characters)
```

> Note: `eyvescloud kvm` is an alias for the `account` command, handy for quick recovery of a forgotten admin credential (non-interactive, run directly over SSH).

Passwords are stored as one-way bcrypt hashes and cannot be recovered. `reset` generates a new strong password and prints it once, so save it carefully. If 2FA is enabled, the dynamic code is still required at login.

## Related Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET/PUT | `/api/ssl` | SSL settings |
| GET/PUT | `/api/access-policy` | Panel access source policy |
| GET/PUT | `/api/notifications` | Notification push settings |
| GET/PUT | `/api/task-queue/settings` | Task queue concurrency |
| POST | `/api/change-password` | Change password |
| POST | `/api/change-username` | Change username |
| GET/PUT | `/api/webssh-origins` | WebSSH/WebVNC Origin allowlist |
| GET/PUT | `/api/overcommit/settings` | Memory overcommit and KSM tuning |
| GET/PUT | `/api/metrics/retention` | Metric retention policy |
| GET | `/api/v1/check-update` | Panel version check |
