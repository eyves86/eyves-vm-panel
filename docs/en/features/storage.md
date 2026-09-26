# Storage Management

Storage Management configures and manages storage pools on the host. A storage pool is where containers, images, snapshots, and backups actually live.

## Storage Pools

The default installation creates one primary storage pool:

| Setting | Default |
| --- | --- |
| ID | `disk-root` |
| Name | `system (/)` |
| Path | `/var/lib/eyvescloud` |
| Mount point | `/` |
| Content types | LXC, KVM, images, snapshots, backups |

Storage pools support the following content types:

- `lxc`: LXC container root filesystems.
- `kvm`: KVM disk images.
- `images`: image cache.
- `snapshots`: snapshots.
- `backups`: backups.

## Multiple Storage Pools

You can add more storage pools on the "Storage Management" page, for example to place containers on a data disk:

| Setting | Example |
| --- | --- |
| ID | `disk-data` |
| Name | `data disk (/data)` |
| Path | `/data/eyvescloud` |
| Mount point | `/data` |
| Content types | LXC, images |

Each storage pool can set an independent "default content type". When creating containers or downloading images, the default pool is preferred, then pools are selected by remaining space in descending order.

## Selection Logic

When creating a container or downloading an image, the system selects a storage pool in the following order:

1. The pool explicitly specified by the user (if it has enough space).
2. A pool configured as default with enough space.
3. Any pool with enough space (by available space descending).

Pools with insufficient space (a 256MB safety margin is reserved) or that are not mounted are skipped. Mount point detection relies on `findmnt`; make sure the target mount point is actually mounted.

## Related Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/storage` | View storage pools and disk status |
| PUT/POST | `/api/storage` | Update storage pool configuration |

`/api/v1/storage` provides a versioned endpoint.
