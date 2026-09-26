# Policy Management

Policy Management (Policy Engine) provides automatic adjustments driven by monitored metrics. When a container metric exceeds a threshold, the system can automatically raise CPU/memory/bandwidth, or shut the container down when resources are out of control.

## Policy Structure

A policy contains:

| Field | Description |
| --- | --- |
| Name | Policy name. |
| Metric | Monitoring metric: `cpu` (CPU usage), `memory` (memory usage), `network_rx` (inbound bandwidth), `network_tx` (outbound bandwidth), `disk_io` (disk I/O). |
| Comparator | `gt` (greater than), etc. |
| Threshold | The trigger threshold. |
| Action | `raise_cpu` (add CPU cores), `raise_ram` (add memory), `adjust_bw` (adjust bandwidth), `shutdown` (automatic shutdown). |
| Adjustment | CPU cores / memory MB / bandwidth Mbps added per trigger. |
| Cooldown | Minimum interval (minutes) between triggers, to avoid flapping. |
| Scope | `all` (all containers), `tenant` (a specific tenant), `container` (a specific container). |
| Enabled | Whether the policy is enabled. |

## Common Usage

### Auto-scale Resources During Peak Hours

```text
Metric: cpu usage > 80%
Action: add CPU cores +1
Cooldown: 10 minutes
Scope: specific container
```

Good for automatic scaling during business peaks; scale back manually or via policies during off-peak hours.

### Automatic Shutdown When Resources Are Out of Control

```text
Metric: memory usage > 95%
Action: automatic shutdown
Cooldown: 30 minutes
Scope: specific container
```

Good for preventing a single container's memory leak from dragging down the host.

## Trigger History

The bottom of the "Policy Management" page shows trigger history, including trigger time, matched policy, container, and action, for review and tuning.

## Related Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/policies` | List policies and trigger history |
| POST | `/api/policies` | Create a policy |
| PUT | `/api/policies/{id}` | Update a policy |
| DELETE | `/api/policies/{id}` | Delete a policy |

The policy engine periodically scans container metrics and executes matched actions; all triggers are written to the audit log.
