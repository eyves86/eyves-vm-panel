# Image Management

Image Management maintains templates used to create containers or virtual machines.

## Supported Template Types

The project includes common Linux distribution templates such as Debian, Ubuntu, Alpine, CentOS, Fedora, Arch Linux, and Rocky Linux. KVM templates use the corresponding distribution cloud image resources.

## Management Actions

```http
GET /api/v1/templates
GET /api/v1/images
POST /api/v1/images/download
POST /api/v1/images/cancel
DELETE /api/v1/images/delete
PUT /api/v1/images/toggle
```

- `templates` returns available template definitions.
- `images` returns local image status.
- `download` downloads a specific template.
- `cancel` cancels a download task.
- `delete` removes the local image cache.
- `toggle` controls whether a template can be used during creation.

## Windows Images

This project does not distribute Windows system images and does not provide features to bypass or avoid Windows activation. Windows download links should point to official Microsoft resources, and users must obtain valid licenses themselves.

## ISO Image Management

ISO images can be used to install an OS on a KVM VM or to mount a driver / rescue disc. Two sources are supported: downloading from a URL online, or uploading a local file. Attach/detach only works for KVM VMs.

```http
GET  /api/isos
POST /api/isos
POST /api/isos/upload
DELETE /api/isos/{id}
POST /api/isos/attach
```

Online download (`POST /api/isos`) request body:

| Field | Description |
| --- | --- |
| `name` | Image name (required) |
| `url` | Download URL (required, max 4096 characters) |
| `os` | Operating system identifier (optional) |

Local upload (`POST /api/isos/upload`, multipart/form-data) has optional `name` and `os` fields plus a required `file` field. Allowed extensions: `.iso`, `.img`, `.qcow2`, `.vhd`, `.tar`, `.gz`, `.zip`.

Attach/detach an ISO to a KVM VM (`POST /api/isos/attach`):

```json
{"container_id": 123, "iso_id": "iso-...", "attach": true}
```

`attach: true` mounts the ISO as a read-only optical drive (`sdb`, cdrom); `attach: false` detaches it. It only supports KVM VMs; LXC containers return an error. An ISO object contains `id`, `name`, `path`, `size_bytes`, `os`, and `created_at`. These endpoints are administrator-only.
