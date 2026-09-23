# FAQ

## Which version does the install script install by default?

The latest version from GitHub Releases. The script defaults to `EYVESCLOUD_VERSION=latest` and downloads the Linux AMD64 or ARM64 artifact from `releases/latest` according to the host architecture.

## Can I pin a specific version?

Yes:

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel EYVESCLOUD_VERSION=v1.1.29 sh
```

## What is the relationship between the Controller and workers?

The Controller is the server running the panel that centrally manages multiple workers. A worker is a server running `eyvescloud agent`; after registering with the Controller, it reports heartbeats automatically, and the Controller can view and operate the worker's containers directly. The same binary can be either a Controller or a worker.

## A worker node keeps showing "Pending". What should I do?

- Make sure the worker has run the one-line install script and the output reported a successful registration.
- Make sure the worker can reach the Controller's `:8999` (registration and heartbeat).
- Wait for the next heartbeat within 10 seconds, then refresh the page.

## The Controller cannot see a worker's containers. What should I do?

- Make sure the worker node status is "Online".
- Make sure a reachable "worker panel address" was entered when creating the node, or specified as the second argument of the install script.
- Make sure the Controller can reach that address (`curl http://NODE_IP:8999/api/version`).
- If an internal IP was used as the worker address, change it to a publicly reachable address.

## Can a deleted node rejoin?

Yes. Add the node again on the Controller to generate a new install script, then run it on the worker again (a new install key and token are generated).

## Can sub-users see all containers?

No. Sub-users only see the containers the administrator authorized for them.

## Are API keys the same as the login password?

No. API keys are created on the "API Integration" page for programmatic API calls. The login password is for the web panel.

## What happens when a container reaches its traffic limit?

The container is automatically shut down to avoid further overage traffic. Administrators can adjust the limit or reset the traffic.

## Why is my public IPv6 not reachable after assignment?

IPv6 reachability depends on the host and the upstream network. The host must have a routable IPv6 range, and the routing, firewall, neighbor discovery, or proxy configuration must be correct.
