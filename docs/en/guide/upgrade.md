# Upgrade

The EyvesCloud install script and CLI are built around GitHub Release artifacts. Before upgrading, check the current version and back up configuration and the database.

## Check the Version

The current version is shown at the bottom of the web panel sidebar. You can also visit:

```bash
curl http://127.0.0.1:8999/api/version
```

Example response:

```json
{
  "success": true,
  "data": {
    "version": "1.1.29"
  }
}
```

## Upgrade with the Install Script

The install script uses the latest Release by default:

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh
```

Pin a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel EYVESCLOUD_VERSION=v1.1.29 sh
```

## Upgrading Worker Nodes

Worker nodes (Agent mode) can also be upgraded directly with the install script. After the upgrade, `eyvescloud agent` reports the new version on the next heartbeat, and the Controller's "Node Management" page shows it automatically.

## Pre-upgrade Checklist

- Make sure `/root/.eyvescloud/` or the actual configuration directory is backed up.
- Make sure the system service is not running critical tasks.
- If an image download or snapshot restore is in progress, wait for it to finish before upgrading.
- After upgrading, check `systemctl status eyvescloud` and the version shown in the web panel.
