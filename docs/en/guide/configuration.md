# Configuration

After installation, EyvesCloud runs as a systemd service, with runtime configuration and the database stored locally on the host. The exact paths may vary with install script options; for a default installation, start by checking `/root/.eyvescloud/`.

## Common Settings

| Setting | Description |
| --- | --- |
| Web port | Defaults to `8999`; the service listens on `0.0.0.0:8999`. |
| Administrator account | Used to log in to the web panel and manage API keys. |
| Database | SQLite, storing container metadata, nodes, sub-users, audit logs, API keys, and more. |
| NAT port range | Used for random ports and port mapping allocation. |
| IPv6 address range | Allocation policy configurable when the host has routable IPv6. |
| Storage pools | Multiple pools with content types (LXC/KVM/images/snapshots/backups). |
| Policy rules | Resource/traffic/expiry-triggered policies that run reclamation actions when matched. |
| Security alerts | Policies such as automatic shutdown can be configured. |

## Service Commands

```bash
systemctl status eyvescloud
systemctl restart eyvescloud
journalctl -u eyvescloud -n 100 --no-pager
```

## Panel Access Allowlist CLI

```bash
# Show the current policy
eyvescloud access-policy show

# Allow only the specified IPs/networks; fill in reverse proxy addresses as needed
eyvescloud access-policy set \
  --allow "203.0.113.10,192.168.1.0/24,2001:db8::/32" \
  --trusted-proxy "127.0.0.1"

# Disable the allowlist restriction
eyvescloud access-policy disable
```

You can also run `eyvescloud cli` and select "Panel access allowlist" from the interactive menu. Both the direct command and the interactive menu persist the configuration and restart the panel service automatically.

## Worker Node Configuration

The registration info of a worker node (Agent mode) is stored in `agent.json` under the configuration directory:

```json
{
  "controller": "http://MASTER_IP:8999",
  "node_id": "node-xxxx",
  "token": "worker node token",
  "name": "node-1",
  "address": "http://THIS_IP:8999"
}
```

This file is generated automatically by `eyvescloud agent` on the first registration. The `token` is used for API authentication between the Controller and the worker; do not expose it.

## Security Recommendations

- Do not expose the web panel directly to untrusted sources.
- Use a strong administrator password and rotate it regularly.
- Split API keys by purpose and avoid long-lived full-access keys.
- WebSSH and WebVNC tickets are short-lived credentials; do not write them to logs or share them externally.
- The worker node's install key is only used for the first registration; after registration, the Controller can delete the node at any time to revoke access.
- Do not paste real IPs, passwords, API keys, or tickets into public docs, screenshots, or support tickets.
