# Quick Start

The following is a common path from a fresh installation to creating your first container and onboarding a worker node.

## 1. Log In to the Console

Open `http://YOUR_SERVER_IP:8999` and sign in with the administrator account.

After entering the panel, check:

- Whether the dashboard shows host resources.
- Whether Image Management can list templates.
- Whether NAT and IPv6 status in Routing matches your host network.

## 2. Download an Image

Open "Image Management", choose a template, and download it. On hosts with limited resources, prefer lightweight images such as Alpine or Debian.

Image downloads run asynchronously. You can watch progress in the task queue.

## 3. Create a Container

Open "Container Management" and click Create:

- Select the virtualization type and template.
- Set CPU, memory, and disk.
- Set traffic limits and expiration time.
- If external access is required, add NAT port mappings or assign IPv6 from the container details page after creation.

## 4. Open a Terminal

After the container is created, open WebSSH from the details page. KVM virtual machines can use WebVNC to view the console.

## 5. Share with a Sub-user

If a container needs to be handed over to another user, create an access link in "Sub-user Management". Sub-users only see authorized containers and are limited to the operation scope configured by the administrator.

## 6. Onboard a Worker Node (Controller-Agent)

If you have multiple servers to manage centrally:

1. On the Controller panel, go to "Node Management" and click "Add Node".
2. Click "Install Script" on the new node and copy the one-line install script.
3. Run the script as root on the worker server (it downloads the binary, registers with the Controller, and configures a systemd service for auto-start).
4. Back in the Controller's "Node Management", once the node status becomes "Online", click the view button to browse and operate the worker's containers.

See [Node Management (Controller-Agent)](/en/features/nodes) for details.
