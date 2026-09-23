# Host Report

The host report summarizes the host runtime environment, resource status, and virtualization dependencies. It is useful for post-installation checks, troubleshooting, or sharing environment information with maintainers.

## Contents

- System version and kernel information.
- CPU, memory, disk, and Swap.
- Network status.
- LXC/KVM dependency status.
- EyvesCloud service status.

## Related APIs

```http
GET /api/v1/host-report
GET /api/v1/host-info
GET /api/v1/swap
```

Before sending a report externally, check whether it contains public IPs, private networks, usernames, keys, tickets, or business domains.
