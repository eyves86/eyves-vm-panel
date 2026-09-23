# Node Management (Controller-Agent)

EyvesCloud supports a "Controller-Agent" multi-node architecture (similar to the node mode of MagicCloud): one Controller panel manages multiple worker servers. After running the one-line install script, a worker server joins automatically, and the Controller can view and operate its containers directly.

## Workflow

```text
Controller panel            Worker server
   │                        │
   ├─ Add node              │
   ├─ Generate script ────► Run script
   │                        ├─ Download Controller binary
   │                        ├─ Register (install key → token)
   │                        └─ Start Agent + systemd auto-start
   │  ◄────── heartbeat (10s) ──┤
   ├─ Node becomes "Online" │
   ├─ View worker containers ──► /api/agent/* (token auth)
   └─ Operate worker containers (start/stop/restart)
```

## Add a Node

1. Go to "Node Management" and click "Add Node".
2. Fill in the node name (optional; auto-generated if left blank) and the worker panel address (optional; if left blank, it is specified by the second argument of the install script).
3. After creation, the node status is "Pending".

## One-line Install Script

Click "Install Script" on the node to generate a shell script. Run it as root on the worker server:

```bash
bash eyvescloud-agent-node1.sh
```

The script automatically:

1. Downloads the binary from the Controller (`GET /api/nodes/binary`, the same `eyvescloud` as the Controller).
2. Registers with the Controller using the install key (`POST /api/nodes/register`) and receives the worker node token.
3. Starts the local panel in `eyvescloud agent` mode and writes a systemd service for auto-start on boot.

The script also accepts override arguments: `bash eyvescloud-agent-node1.sh [node name] [worker panel address]`.

### Manual Registration

You can also run it manually on the worker server:

```bash
eyvescloud agent \
  --controller=http://MASTER_IP:8999 \
  --install-key=INSTALL_KEY \
  --name=node-1 \
  --addr=http://WORKER_IP:8999
```

After a successful registration, the configuration is stored in the worker's `agent.json`; on subsequent restarts the install key is no longer needed (unless you switch Controllers).

## Heartbeat and Status

The worker reports a heartbeat every 10 seconds, based on which the Controller updates:

- Node status (Online/Offline/Pending).
- Version and operating system.
- CPU cores, memory usage, disk usage.
- Container count.

A node that fails to report within the heartbeat interval is shown as "Offline".

## Viewing and Operating Worker Containers

When a node is "Online" and a panel address is configured, the Controller can:

- View the worker's container list (name, template, resources, status, IP).
- Start, stop, and restart the worker's containers.

These operations are performed by the Controller proxying the worker's `/api/agent/*` endpoints with the node token. The worker validates the token, so unregistered nodes cannot be accessed.

### Worker Node Auto-Update

The worker Agent can automatically check for and update its own binary in the background. Set the check interval in minutes with the environment variable `EYVESCLOUD_AUTO_UPDATE`; it only activates when the value is `>= 60` (to avoid hitting GitHub too frequently). The one-line install script passes this variable in, defaulting to `1440` (once a day). Once a new version is found and updated successfully, the service restarts automatically to apply it.

## Delete a Node

Deleting a node removes its record from the Controller. If the worker's systemd service is still running, it keeps trying to report heartbeats, but the Controller will no longer show it. To fully revoke access, also uninstall the service on the worker:

```bash
systemctl disable --now eyvescloud-agent
```

## Security Notes

- The install key is a 64-character random hex string, used only for the first registration to exchange for a token.
- The node token authenticates Controller ↔ worker API calls; do not expose it.
- When the Controller is behind an HTTPS reverse proxy, make sure the worker can still reach the Controller address.
- The worker panel address should be its publicly reachable address so the Controller can proxy access.

## Related Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/nodes` | List nodes (admin) |
| POST | `/api/nodes` | Create a node (admin) |
| DELETE | `/api/nodes/{id}` | Delete a node (admin) |
| GET | `/api/nodes/{id}/install-script` | Get the one-line install script (admin) |
| GET | `/api/nodes/binary` | Download the Controller binary (used by the install script) |
| POST | `/api/nodes/register` | Worker registration (install key) |
| POST | `/api/nodes/{id}/heartbeat` | Worker heartbeat (node token) |
| GET | `/api/nodes/{id}/containers` | Proxy view of worker containers (admin) |
| POST | `/api/nodes/{id}/containers/{cid}/{action}` | Proxy operation on worker containers (admin) |
| GET | `/api/agent/containers` | Worker container list (Controller token) |
| POST | `/api/agent/containers/{cid}/{action}` | Worker container operation (Controller token) |
