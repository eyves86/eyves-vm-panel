# Installation

EyvesCloud provides a one-line install script. By default, the script installs the latest version from GitHub Releases; you can also pin a specific version with an environment variable.

## Requirements

- Linux x86_64/amd64 or ARM64/aarch64 host.
- Root privileges.
- systemd.
- Network access to GitHub Release downloads.
- LXC runtime support on the host if you want to use LXC.
- Virtualization enabled with libvirt/QEMU installed if you want to use KVM.

## Install the Latest Version

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo sh
```

> Note: `install.sh` pulls releases from this repository (`FenhaoLost/eyves-vm-panel`) by default; set `EYVESCLOUD_REPO` to override the source.

The installer asks separately for the LXC and KVM NAT private subnets. Press Enter to scan the host routes, interfaces, bridges, and libvirt networks and automatically select non-conflicting RFC1918 `/24` subnets; you can also enter a CIDR such as `172.28.40.0/24`. For unattended installation, set `EYVESCLOUD_LXC_SUBNET` and `EYVESCLOUD_KVM_SUBNET`.

The script defaults to `EYVESCLOUD_VERSION=latest` and downloads `eyvescloud-linux-amd64.tar.gz` or `eyvescloud-linux-arm64.tar.gz` from `releases/latest` according to the host architecture.

## Install a Specific Version

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel EYVESCLOUD_VERSION=v1.1.29 sh
```

Replace `v1.1.29` with the Release tag you want to install.

## Access the Panel

After installation, open the panel in your browser:

```text
http://YOUR_SERVER_IP:8999
```

For the first login, use the administrator credentials printed by the install script. In production, restrict access at the firewall or reverse proxy layer and change the default account and password as soon as possible.

## Run Modes

The same binary supports three run modes:

| Mode | Description |
| --- | --- |
| Panel mode (default) | Run `eyvescloud server` directly, or let systemd manage it after installation; acts as a standalone panel. |
| Controller mode | Also panel mode, with the extra "Node Management" page for generating worker install scripts. Any panel can act as a Controller. |
| Agent mode (Worker) | After running the one-line install script from the Controller's "Node Management", the worker runs as `eyvescloud agent`, auto-registers with the Controller, and reports heartbeats. |

Start in Agent mode manually:

```bash
eyvescloud agent --controller=http://MASTER_IP:8999 --install-key=INSTALL_KEY --name=node-1 --addr=http://THIS_IP:8999
```

Arguments:

- `--controller`: the Controller panel address.
- `--install-key`: the install key generated after creating a node in the Controller's "Node Management" (used for the first registration).
- `--name`: the worker node name; defaults to the hostname.
- `--addr`: the worker's own panel address, through which the Controller proxies access to the worker's containers.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh -s -- uninstall
```

Before uninstalling, decide whether you need to keep containers, image cache, database, and configuration files.
