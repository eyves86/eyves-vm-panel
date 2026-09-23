# Troubleshooting

## Service Unreachable

Check the service status:

```bash
systemctl status eyvescloud
journalctl -u eyvescloud -n 100 --no-pager
```

Check the listening port:

```bash
ss -lntp | grep 8999
```

If you use a reverse proxy, also check the proxy logs and upstream address.

## Image Download Fails

- Make sure the host can reach the image sources and GitHub Releases.
- Check disk space.
- Look at the failure reason in the task queue.
- If a download is stuck, try canceling the task and downloading again.

## Container Has No Network

- Check the host NAT and forwarding rules.
- Check whether the container IP was assigned successfully.
- Check whether the firewall is blocking forwarded traffic.
- For IPv6, confirm the upstream has routed the address range to the host.

## WebSSH or WebVNC Connection Fails

- Make sure the container or VM is running.
- WebSSH requires an SSH service inside the container.
- WebVNC requires the KVM console to be reachable.
- Tickets are short-lived; create a new one after expiry.

## API Returns Unauthorized

- Make sure the API key is not disabled.
- Make sure the request header uses `X-API-Key` or `Authorization: Bearer`.
- Make sure the key's scopes cover the target endpoint.
- Do not use the panel login password as an API key.

## Worker Registration Fails

When the one-line install script reports "registration failed" or the worker logs show a registration error:

- Make sure the install key matches the one generated when the node was created on the Controller.
- Make sure the worker can reach the Controller: `curl -v http://MASTER_IP:8999/api/version`.
- If the worker was registered before but the Controller changed, delete `agent.json` in the worker's configuration directory and re-register.
- When the Controller returns "Invalid install key", re-fetch the install script for that node on the Controller.

## Worker Heartbeat Fails (Node Shows Offline)

- Check the worker's `eyvescloud-agent` service: `systemctl status eyvescloud-agent`.
- Check whether the worker can reach the Controller's `:8999`.
- Check whether the token in the Controller's node record matches the worker's `agent.json`; if not, delete the worker's `agent.json` and re-register.

## The Controller Cannot Proxy View Worker Containers

- Make sure the node status is "Online".
- Test directly on the worker: `curl http://NODE_IP:8999/api/version`.
- Test from the Controller server whether the worker address is reachable: `curl http://NODE_IP:8999/api/version`.
- If the worker panel address is unreachable, update the node address or delete and re-add the node on the Controller.

## Worker Agent Port Conflict

The worker's default panel port is the same as the Controller's `8999`. If another service already occupies it on the worker, use `--addr` to specify a different port and make sure that port is allowed in the firewall.
