# 镜像管理

镜像管理用于维护可创建容器或虚拟机的模板。

## 支持的模板类型

项目内置了常见 Linux 发行版模板，例如 Debian、Ubuntu、Alpine、CentOS、Fedora、Arch Linux、Rocky Linux 等。KVM 模板会使用对应发行版的云镜像资源。

## 管理动作

```http
GET /api/v1/templates
GET /api/v1/images
POST /api/v1/images/download
POST /api/v1/images/cancel
DELETE /api/v1/images/delete
PUT /api/v1/images/toggle
```

- `templates` 返回可用模板定义。
- `images` 返回本地镜像状态。
- `download` 下载指定模板。
- `cancel` 取消下载任务。
- `delete` 删除本地镜像缓存。
- `toggle` 控制模板是否对创建流程可用。

## Windows 镜像说明

本项目不分发 Windows 系统镜像，也不提供绕过或规避 Windows 激活机制的功能。涉及 Windows 的下载链接应指向微软官方资源，使用者需要自行获得合法授权。

## ISO 镜像管理

ISO 镜像可用于 KVM 虚拟机安装系统或挂载驱动/救援盘。支持通过 URL 在线下载或本地上传两种来源；挂载/卸载只支持 KVM 虚拟机。

```http
GET  /api/isos
POST /api/isos
POST /api/isos/upload
DELETE /api/isos/{id}
POST /api/isos/attach
```

在线下载（`POST /api/isos`）请求体：

| 字段 | 说明 |
| --- | --- |
| `name` | 镜像名称（必填） |
| `url` | 下载地址（必填，不超过 4096 字符） |
| `os` | 操作系统标识（可选） |

本地上传（`POST /api/isos/upload`，multipart/form-data）包含可选字段 `name`、`os` 以及必填的 `file` 文件。允许的扩展名：`.iso`、`.img`、`.qcow2`、`.vhd`、`.tar`、`.gz`、`.zip`。

挂载/卸载 ISO 到 KVM 虚拟机（`POST /api/isos/attach`）：

```json
{"container_id": 123, "iso_id": "iso-...", "attach": true}
```

`attach: true` 将 ISO 以只读光驱（`sdb`，cdrom）挂载，`attach: false` 卸载。仅支持 KVM 虚拟机，对 LXC 容器会返回错误。ISO 对象包含 `id`、`name`、`path`、`size_bytes`、`os`、`created_at`。以上接口仅管理员可调。
