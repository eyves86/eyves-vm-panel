# Instance Backups

Instance backups archive the full disk data of an LXC / KVM container so it can be preserved for disaster recovery, data protection before a reinstall, or moving data across nodes. Unlike a "snapshot", which only lives in the snapshot storage, a backup is stored as a tar archive in a dedicated backup directory and stays intact even if the instance is deleted or reinstalled.

## Backup Storage Location

Archives are stored under `{DataDir}/instance-backups`, one sub-directory per container, with filenames like `bak-{containerID}-{timestamp}.tar`. Creating a backup first takes a consistency snapshot (cold copy), then archives it, and finally cleans up the temporary snapshot.

## Create and List Backups

```http
GET  /api/v1/containers/{id}/backups
POST /api/v1/containers/{id}/backups
```

`GET` returns all backup records for the container. When creating a backup you may pass a `keep` parameter:

| Field | Description |
| --- | --- |
| `keep` | Retention count. When `keep > 0`, only the newest `keep` backups are kept and older ones are pruned automatically. When `keep <= 0` (default), everything is kept and nothing is pruned automatically. |

## Restore a Backup

```http
POST /api/v1/containers/{id}/backups/{backupId}/restore
```

Restoring stops the container, extracts the archive into a protected snapshot base, and reuses the "stop → swap → restart" restore logic, then starts the container again. Restoring changes the container state, so confirm that the workload can tolerate downtime in production.

## Delete a Backup

```http
DELETE /api/v1/containers/{id}/backups/{backupId}
```

Deletion removes both the archive file and the backup record.

## Permissions

- List: `snapshot:read`; create: `snapshot:create`; restore: `snapshot:restore`; delete: `snapshot:delete`.
- Sub-users cannot create instance backups (returns `403`).

## Backup Record Fields

| Field | Description |
| --- | --- |
| `id` | Backup ID (`bak-...`) |
| `container_id` / `container_name` | The owning container |
| `kind` | `lxc` or `kvm` |
| `created_at` / `created_by` | Creation time and operator |
| `path` | Archive file path |
| `size_bytes` | Archive size in bytes |