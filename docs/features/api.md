# API 集成

EyvesCloud 对外提供一套版本化 HTTP API（`/api/v1`），覆盖容器生命周期、网络与端口、快照与备份、镜像、任务队列、安全、审计与用量导出，供面板前端、被控 Agent 节点以及第三方计费 / 运维系统对接。

本章说明认证方式、请求与响应约定、权限范围（scope），并给出完整接口清单与计费系统对接示例。机器可读契约见 `GET /api/v1/openapi.json`（OpenAPI 3.0）。

## 基地址与版本

- 版本化前缀：`/api/v1`（推荐，新增接口优先在这里提供）。
- 兼容前缀：`/api`（历史路径，保留可用）。
- 版本信息：`GET /api/version`。
- 健康检查：`GET /api/v1/health`、`GET /api/v1/health/detail`（后者仅管理员）。

所有接口返回 `Content-Type: application/json`。

## 认证

API 支持三种身份，统一通过请求头识别：

| 身份 | 请求头 | 说明 |
| --- | --- | --- |
| 管理员 | `Authorization: Bearer <JWT>` | 账号密码（可叠加两步验证）登录后签发，有效期 24 小时 |
| API Key | `X-API-Key: eyvescloud_sk_xxxx` | 也可写成 `Authorization: Bearer eyvescloud_sk_xxxx` |
| 子用户 | `Authorization: Bearer <JWT>` | 由子用户登录接口签发，权限受角色与绑定容器约束 |

### 管理员登录

```http
POST /api/v1/login
Content-Type: application/json

{ "username": "admin", "password": "your-password", "twofa_code": "" }
```

```json
{
  "success": true,
  "data": { "token": "eyJhbGciOi...", "username": "admin" }
}
```

已启用两步验证时，未带 `twofa_code` 会返回：

```json
{ "success": false, "message": "Two-factor verification required", "data": { "twofa_required": true } }
```

拿到 `token` 后携带 `Authorization: Bearer <token>` 调用其他接口。可用 `GET /api/v1/check-auth` 校验当前身份，返回类型、角色、绑定容器与生效 scope。

> 修改管理员密码会递增令牌版本，此前签发的所有管理员令牌立即失效。

### API Key

API Key 在后台「API 集成」页创建，明文只在创建时返回一次，形如 `eyvescloud_sk_` + 32 位十六进制。可为 Key 配置：

- `scopes`：权限范围，支持 `*`、`admin:*` 与 `service:*` 通配。
- `ip_whitelist`：换行分隔的 IP 或 CIDR，留空不限制。
- `expires_at`：到期时间（`YYYY-MM-DD HH:MM:SS`），留空长期有效。
- `container_uuids`：绑定容器，留空表示全部容器。
- `disabled`：是否禁用。

受限 Key 不能自我提权：创建/更新 Key 时，只能授予调用方自身已拥有的 scope 子集，且禁止授予 `apikey:*`、`admin:*` 等管理类 scope。

### 子用户

子用户由管理员创建并绑定容器，角色为 `operator`（读写）或 `viewer`（只读）。`viewer` 仅允许读类与连接类 scope，拒绝电源控制、重装、改密等写操作。

### 管理员角色

登录管理员账号自身还带角色：`admin`（全权）、`operator`（可写但不可管理敏感配置）、`readonly`（只读）。未知取值一律降级为 `readonly`。

## 请求与响应约定

### 统一响应结构

```json
{
  "success": true,
  "code": "NOT_FOUND",
  "message": "Container not found",
  "data": {}
}
```

- `success`：是否成功。
- `code`：机器可读错误码，**仅失败时出现**。
- `message`：可读信息。
- `data`：业务数据，可能是对象、数组或省略。

### 错误码

| 错误码 | 含义 |
| --- | --- |
| `INVALID_REQUEST` | 请求参数非法 |
| `NOT_FOUND` | 资源不存在 |
| `FORBIDDEN` | 无权限 |
| `INSUFFICIENT_SCOPE` | API Key scope 不足 |
| `RATE_LIMITED` | 触发限流 |
| `BAD_GATEWAY` | 下游（节点 Agent）异常 |
| `INTERNAL_ERROR` | 服务端内部错误 |

HTTP 状态码同时表达语义：`400` 参数错误、`401` 未认证、`403` 无权限、`404` 不存在、`405` 方法不允许、`409` 冲突（配额 / 端口占用）、`429` 限流、`500` 内部错误。

### 分页

列表接口支持可选分页参数，**不传时保持全量返回**（向后兼容）：

```http
GET /api/v1/audit-logs?page=1&page_size=50
```

`page` 从 1 起，`page_size` 默认 50、上限 200。传入分页参数后，`data` 为 `{ "items": [], "total": 0, "page": 1, "page_size": 50 }`。

### DryRun 预检

创建类接口支持 `dry_run`，只做全部校验并返回规划结果，不落盘、不调用运行时：

```http
POST /api/v1/containers?dry_run=true
```

请求体 `{"dry_run": true}` 或查询参数 `?dry_run=true` 任一命中即生效。

### 幂等开通

在创建容器时携带 `Idempotency-Key` 请求头，可避免计费系统回调超时后重复开通（双开）：

```http
POST /api/v1/containers
Idempotency-Key: order-20260101-0001

{ "name": "web-01", "template_id": "alpine-3.21", ... }
```

同一 Key 重试时返回既有容器（`success=true`，`message` 为 `Container already exists (idempotent)`），不会再次创建。

### 任务队列

开关机、重装、删除、创建等耗时操作进入任务队列异步执行。提交后返回任务 ID，可轮询进度：

```http
GET /api/v1/tasks
GET /api/v1/tasks/{task_id}
DELETE /api/v1/tasks/{task_id}
GET /api/v1/tasks/stats
```

任务对象包含 `status`（`pending`/`running`/`completed`/`failed`）、`stage`、`percent`、`error` 等字段。

## 权限范围（scope）

创建 API Key 时可授予以下 scope（`*` 表示全部）：

| 分组 | scope | 说明 |
| --- | --- | --- |
| 总览与只读 | `dashboard:read` | 控制面板统计 |
| | `host:read` | 主机资源 |
| | `routing:read` / `routing:write` | 路由读取 / 配置 |
| | `ipv6:read` | IPv6 状态 |
| | `task:read` / `task:delete` | 任务读取 / 删除 |
| | `image:read` | 镜像列表 |
| 容器 | `container:read` | 查看容器 |
| | `container:create` | 创建容器 |
| | `container:power` | 开关机 / 重启 |
| | `container:reinstall` | 重装系统 |
| | `container:delete` | 删除容器 |
| | `container:resize` | 资源与到期 |
| | `container:traffic` | 流量管理 |
| | `container:network` | 网络与端口映射 |
| | `container:password` | 重置密码 |
| | `container:account` | 容器内账号 |
| | `container:ssh-key` | 容器 SSH 密钥 |
| | `ipv6:assign` | 分配 IPv6 |
| 快照与终端 | `snapshot:read` / `snapshot:create` / `snapshot:delete` / `snapshot:restore` / `snapshot:schedule` | 快照查看 / 创建 / 删除 / 恢复 / 计划 |
| | `terminal:ssh` / `terminal:vnc` | WebSSH / WebVNC 票据 |
| 平台管理 | `image:download` / `image:delete` / `image:toggle` | 镜像下载 / 删除 / 启停 |
| | `security:read` / `security:check` / `security:settings` | 安全数据 / 扫描 / 设置 |
| | `swap:read` / `swap:manage` | Swap 读取 / 管理 |
| | `subuser:read` / `subuser:create` / `subuser:update` | 子用户 |
| | `audit:read` / `loginlog:read` | 操作日志 / 登录日志 |
| | `usage:read` | 用量导出（计费用） |
| | `apikey:read` / `apikey:create` / `apikey:update` / `apikey:delete` | API Key 管理 |
| | `admin:access` | 管理员接口（等同于后台权限） |

`admin:access`、`apikey:*` 属于管理类 scope，只能由管理员会话授予。

## 接口参考

以下路径均省略 `/api/v1` 前缀。标注「管理员」的接口需要管理员会话或持 `admin:access` 的 API Key。

### 登录与账号

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/login` | 管理员登录，返回 JWT |
| GET | `/check-auth` | 校验当前身份与生效 scope |
| POST | `/sub-user/login` | 子用户登录 |
| POST | `/sub-user/access` | 使用访问码登录 |
| POST | `/sub-user/change-password` | 子用户改密（需登录） |
| POST | `/change-password` | 修改主管理员密码（仅本人会话） |
| POST | `/change-username` | 修改主管理员用户名（仅本人会话） |
| GET | `/2fa/status` `/2fa/setup` `/2fa/enable` `/2fa/disable` `/2fa/regenerate-backup-codes` | 两步验证（仅主管理员会话） |

### 总览与主机

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/dashboard` | 面板概览统计 |
| GET | `/host-info` | 宿主机资源 |
| GET | `/host-history` | 宿主机历史指标 |
| GET | `/host-report` | 宿主机硬件 / 网络 / 运行环境报告 |
| GET | `/monitoring/containers` | 批量容器监控指标 |
| GET | `/storage` / PUT `/storage` | 存储盘与存储池（管理员） |
| GET | `/routing` / PUT `/routing` | NAT / IPv4 / IPv6 路由（管理员） |
| POST | `/routing/ipv4-scan` | 扫描公网 IPv4 段（管理员） |
| GET | `/ipv6/status` | IPv6 状态 |
| GET | `/task-queue/settings` / PUT | 任务并发设置（管理员） |
| GET | `/overcommit/settings` | 超分设置（管理员） |
| GET | `/metrics/retention` | 指标保留策略（管理员） |

### 容器

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/containers` | 容器列表 |
| GET | `/containers/list` | 列表别名（字段裁剪版） |
| POST | `/containers` | 创建容器（支持 `Idempotency-Key`、`dry_run`） |
| GET | `/containers/{id\|uuid\|name}` | 容器详情 |
| POST | `/containers/{id}/start` | 开机 |
| POST | `/containers/{id}/stop` | 关机 |
| POST | `/containers/{id}/restart` | 重启 |
| POST | `/containers/{id}/reinstall` | 重装系统 |
| POST | `/containers/{id}/suspend` | 挂起（欠费停机，body 可选 `reason`） |
| POST | `/containers/{id}/unsuspend` | 解除挂起（复机，不自动开机） |
| DELETE | `/containers/{id}/delete` | 删除容器 |
| POST | `/containers/{id}/reset-password` | 重置 SSH 密码（body 可选 `password`） |
| POST | `/containers/{id}/create-account` | 创建容器内账号 |
| PUT | `/containers/{id}/resource-limit` | 调整资源限制（仅允许扩容） |
| PUT | `/containers/{id}/traffic-limit` | 调整流量限制 |
| POST | `/containers/{id}/traffic-reset` | 重置已用流量 |
| PUT | `/containers/{id}/expiry` | 设置到期时间（body: `expires_at`） |
| PUT | `/containers/{id}/tenant` | 设置所属租户 |
| PUT | `/containers/{id}/tags` | 整体替换资源标签（最多 20 个，key/value ≤128 字符） |
| PUT | `/containers/{id}/owner` | 变更属主子用户（仅管理侧） |
| POST | `/containers/{id}/clone` | 克隆容器 |
| POST | `/containers/{id}/hostname` | 修改主机名 |
| POST | `/containers/{id}/vnc-password` | 修改 VNC 密码 |
| GET | `/containers/{id}/usage` | 实时用量 |
| GET | `/containers/{id}/traffic` | 流量统计 |
| GET | `/containers/{id}/history` | 历史指标时序 |
| POST | `/containers/rescue` | KVM 救援模式（管理员） |
| POST | `/batch-create` | 批量创建 |
| POST | `/batch-action` | 批量开关机 / 删除 / 重装 |

#### 创建容器请求体

```json
{
  "name": "web-01",
  "virtualization": "lxc",
  "template_id": "alpine-3.21",
  "vcpu": 2,
  "ram_mb": 1024,
  "disk_gb": 20,
  "network_bw_mbps": 100,
  "monthly_traffic_gb": 500,
  "traffic_mode": "total",
  "assign_nat": true,
  "port_mapping_count": 2,
  "extra_ports": [80, 443],
  "assign_ipv4": false,
  "assign_ipv6": false,
  "ssh_auth_mode": "auto_password",
  "ssh_password": "",
  "ssh_public_key": "",
  "snapshot_limit": 3,
  "tenant": "customer-1001",
  "expires_at": "2026-12-31 23:59:59"
}
```

主要字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string | 容器名（必填，唯一） |
| `virtualization` | string | `lxc` 或 `kvm` |
| `template_id` | string | 模板 / 镜像 ID（必填，且需已启用） |
| `vcpu` | number | vCPU，默认 1 |
| `ram_mb` | int | 内存（MB），最小 128，默认 512 |
| `disk_gb` | number | 系统盘（GB），默认 5 |
| `data_disk_gb` | number | 数据盘（GB，可选） |
| `network_bw_mbps` | int | 带宽限制（Mbps），`0` 不限 |
| `monthly_traffic_gb` | int | `total` 模式月流量（GB），`0` 不限 |
| `traffic_mode` | string | `total` 或 `in_out` |
| `traffic_in_gb` / `traffic_out_gb` | int | `in_out` 模式入 / 出流量 |
| `io_speed_mbps` | int | 磁盘 IO 限制，`0` 不限 |
| `assign_nat` | bool | 是否分配 NAT 端口映射 |
| `port_mapping_count` | int | NAT 端口数量（≤64） |
| `extra_ports` | int[] | 额外映射的容器端口 |
| `nat_port_mappings` | object[] | 指定 NAT 映射 |
| `assign_ipv4` / `public_ipv4s` | bool / string[] | 独立公网 IPv4 |
| `assign_ipv6` / `ipv6_addresses` | bool / string[] | IPv6 地址 |
| `ssh_auth_mode` | string | `auto_password` / `password` / `key` / `keep` |
| `ssh_password` | string | `password` 模式使用 |
| `ssh_public_key` | string | `key` 模式使用 |
| `ssh_key_ids` | string[] | 平台托管 SSH 公钥 ID（`SK-xxx`） |
| `snapshot_limit` | int | 快照数量上限 |
| `tenant` | string | 租户标识 |
| `expires_at` | string | 到期时间，必须是未来时间 |
| `allowed_image_ids` | string[] | 容器可用的镜像白名单 |

创建成功后返回 `201`；DryRun 返回 `200` 且 `data.dry_run=true`。

### 网络与端口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/containers/{id}/random-port` | 获取一个可用 NAT 端口 |
| POST | `/containers/{id}/port-mappings` | 新增端口映射 |
| PUT | `/containers/{id}/port-mappings/{index}` | 修改端口映射 |
| DELETE | `/containers/{id}/port-mappings/{index}` | 删除端口映射 |
| GET | `/containers/{id}/firewall` / PUT | 防火墙规则 |
| GET | `/containers/{id}/rdns` / PUT | 反向 DNS |
| POST | `/containers/{id}/ipv6` | 分配 IPv6 |
| PUT | `/containers/{id}/ipv6-addresses` | 更新独立 IPv6 |
| PUT | `/containers/{id}/public-ipv4` | 更新独立公网 IPv4 |
| GET | `/security-groups` / POST | 安全组列表 / 创建 |
| GET/PUT/DELETE | `/security-groups/{id}` | 安全组详情 |
| GET/POST/PUT/DELETE | `/security-groups/{id}/rules` | 安全组规则 |
| GET | `/ip-groups` / POST | IP 分组（管理员） |
| GET | `/regions` / POST | 区域（管理员） |
| GET | `/isos` / POST `/isos/upload` | ISO 列表 / 上传（管理员） |
| POST | `/isos/attach` | 挂载 / 卸载 ISO 到容器 |

### 快照、备份与迁移

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/snapshots` | 快照总览 |
| GET | `/containers/{id}/snapshots` | 容器快照列表 |
| POST | `/containers/{id}/snapshots` | 创建快照 |
| DELETE | `/containers/{id}/snapshots/{snapshotID}` | 删除快照 |
| POST | `/containers/{id}/snapshots/{snapshotID}/restore` | 恢复快照 |
| GET | `/containers/{id}/backups` | 实例备份列表 |
| POST | `/containers/{id}/backups` | 创建实例备份 |
| GET | `/backup/list` | 备份归档列表（管理员） |
| POST | `/backup` | 创建备份（管理员） |
| POST | `/backup/restore` | 从备份恢复（管理员） |
| GET | `/backup/download` | 下载备份（管理员） |
| GET/PUT | `/backup/settings` | 备份设置（管理员） |
| GET/PUT | `/backup/remote-settings` | 异地备份设置（管理员） |
| POST | `/backup/remote-test` | 异地备份连通性测试（管理员） |
| GET | `/backup-plans` / POST | 备份计划 |
| GET | `/containers/{id}/migrate-export` | 导出迁移包 |
| POST | `/migrate/import` | 导入迁移包（管理员） |

### 模板与镜像

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/templates` | 可用模板（LXC 模板 + KVM 镜像） |
| GET | `/images` | 镜像列表（管理员） |
| GET | `/images/enabled` | 已启用镜像（可用于创建 / 重装） |
| POST/DELETE | `/images/custom` | 添加 / 移除第三方镜像源（管理员） |
| POST | `/images/download` / `/images/cancel` | 下载 / 取消下载（管理员） |
| DELETE | `/images/delete` | 删除镜像缓存（管理员） |
| PUT | `/images/toggle` | 启用 / 禁用镜像（管理员） |

### 任务与控制台

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/tasks` | 任务队列 |
| GET | `/tasks/history` | 历史任务 |
| GET | `/tasks/stats` | 任务统计 |
| GET/DELETE | `/tasks/{task_id}` | 任务详情 / 删除记录 |
| POST | `/ssh-ticket` | 创建 WebSSH 一次性票据 |
| POST | `/vnc-ticket` | 创建 WebVNC 一次性票据 |
| WS | `/api/ssh` | WebSSH WebSocket |
| WS | `/api/vnc` | WebVNC WebSocket |

票据短时有效，返回后应立即用于建立连接，不要持久化保存。

### 子用户与租户

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/sub-users` | 子用户列表 |
| POST | `/sub-user/create` | 创建子用户（可绑定容器 / 租户） |
| GET/PUT/DELETE | `/sub-users/{id}` | 子用户详情 / 更新 / 删除 |
| POST | `/sub-users/{id}/rotate-password` | 轮换密码 |
| GET | `/sub-users/{id}/audit-logs` / `/login-logs` | 子用户日志 |
| GET | `/tenants` / POST | 租户列表 / 创建（管理员） |
| GET/PUT/DELETE | `/tenants/{id}` | 租户详情（管理员） |

### 安全与审计

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/security/alerts` | 安全告警（scope `security:read`） |
| POST | `/security/check` | 立即安全检查（scope `security:check`） |
| GET | `/security/logs` | 安全连接日志（scope `security:read`） |
| GET | `/security/summary` | 容器安全摘要 |
| GET | `/security/abuse-summary` | 滥用汇总 |
| GET/PUT | `/security/settings` | 安全设置 |
| GET | `/audit-logs` | 操作日志 |
| GET | `/audit-logs/export` | 导出审计日志（CSV/JSON） |
| GET | `/login-logs` | 登录日志 |
| GET | `/api-keys` / POST | API Key 列表 / 创建 |
| PATCH/DELETE | `/api-keys/{id}` | 更新 / 删除 API Key |

### 设置与通知

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/PUT | `/ssl` | SSL 证书配置（管理员） |
| GET/PUT | `/webssh-origins` | WebSSH / VNC Origin 白名单（管理员） |
| GET/PUT | `/access-policy` | 面板访问来源策略（管理员） |
| GET/PUT | `/admin-path` | 后台入口路径 |
| GET/PUT | `/notifications` | 通知设置（管理员） |
| POST | `/notifications/test` | 通知测试（管理员） |
| GET/PUT | `/smtp` , POST `/smtp/test` | 邮件设置（管理员） |
| GET/PUT | `/language` | 面板语言 |
| GET/POST | `/admins` | 管理员账号（管理员） |
| GET/PUT/DELETE | `/admins/{id}` | 管理员详情 |
| GET/POST | `/policies` , `/policies/{id}` | 策略管理（管理员） |
| GET/POST | `/ssh-keys` , `/ssh-keys/{id}` | 平台托管 SSH 公钥 |
| GET/POST | `/recipes` , `/recipes/{id}` | 自定义脚本模板 |
| GET | `/swap` / POST | Swap 信息 / 调整 |

### Webhooks（事件订阅）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/webhooks` | Webhook 列表（管理员） |
| POST | `/webhooks` | 创建 Webhook（管理员） |
| GET/PUT/DELETE | `/webhooks/{id}` | 详情 / 更新 / 删除（管理员） |
| POST | `/webhooks/{id}/test` | 测试投递（管理员） |

回调契约：

```text
POST <callback_url>
Content-Type: application/json
X-EyvesCloud-Signature: sha256=<hex(HMAC-SHA256(secret, body))>
X-EyvesCloud-Delivery: <每次投递唯一 ID，用于接收方去重>
```

```json
{
  "event_type": "container.status_changed",
  "timestamp": "2026-01-01T12:00:00Z",
  "data": { "container_id": 12, "name": "web-01", "old_status": "running", "new_status": "stopped" }
}
```

接收方须返回 `2xx`；非 `2xx` 触发重试（3 次指数退避 1s / 5s / 25s）。连续失败 10 次自动停用该订阅并记录原因。

### OpenAPI 契约

```http
GET /api/v1/openapi.json
```

返回 OpenAPI 3.0 文档，描述版本化接口、认证方式与核心数据结构。

## 计费系统对接

EyvesCloud 提供完整的「开通 → 计量 → 出账 → 停复机」对接闭环，计费可继续由 WHMCS 等外部系统负责。

### 用量导出

```http
GET /api/v1/usage?tenant=customer-1001&include=config_only
```

- scope：`usage:read`（管理员会话或显式授予该 scope 的 API Key；子用户无此 scope）。
- `tenant`：可选，按租户过滤。
- `include=config_only`：可选，只返回配置，不查询实时用量。

```json
{
  "success": true,
  "data": {
    "generated_at": "2026-01-01T12:00:00Z",
    "filter_tenant": "customer-1001",
    "count": 1,
    "count_by_tenant": { "customer-1001": 1 },
    "containers": [
      {
        "uuid": "c-1a2b3c",
        "name": "web-01",
        "tenant": "customer-1001",
        "virtualization": "lxc",
        "vcpu": 2,
        "ram_mb": 1024,
        "disk_gb": 20,
        "status": "running",
        "suspended": false,
        "expires_at": "2026-12-31 23:59:59",
        "created_at": "2026-01-01 10:00:00",
        "traffic": {
          "monthly_limit_gb": 500,
          "mode": "total",
          "used_rx_bytes": 1073741824,
          "used_tx_bytes": 536870912,
          "used_rx_gb": 1.0,
          "used_tx_gb": 0.5,
          "reset_date": "2026-01"
        }
      }
    ]
  }
}
```

响应刻意排除 SSH 密码等敏感字段。配合以下接口即可完成停复机闭环：

| 场景 | 接口 |
| --- | --- |
| 欠费停机 | `POST /containers/{id}/suspend`，body `{"reason": "overdue"}` |
| 缴费复机 | `POST /containers/{id}/unsuspend` |
| 到期调整 | `PUT /containers/{id}/expiry`，body `{"expires_at": "..."}` |
| 套餐变更 | `PUT /containers/{id}/resource-limit`、`PUT /containers/{id}/traffic-limit` |
| 重装系统 | `POST /containers/{id}/reinstall`，body `{"template_id": "..."}` |

挂起会强制关机并阻断 `start`/`restart`/`reinstall`/WebSSH/VNC，解除挂起不会自动开机。

### 对接示例（Python）

```python
import requests

BASE = "https://panel.example.com/api/v1"
HEADERS = {"X-API-Key": "eyvescloud_sk_xxxxxxxx"}

# 1. 拉取全量用量
usage = requests.get(f"{BASE}/usage", headers=HEADERS, params={"tenant": "customer-1001"}).json()
for item in usage["data"]["containers"]:
    print(item["name"], item["status"], item["traffic"]["used_rx_gb"], "GB")

# 2. 欠费停机（幂等语义由业务侧保证，重复调用安全）
cid = usage["data"]["containers"][0]["uuid"]
requests.post(f"{BASE}/containers/{cid}/suspend", headers=HEADERS, json={"reason": "overdue"})

# 3. 缴费复机
requests.post(f"{BASE}/containers/{cid}/unsuspend", headers=HEADERS)
```

### 开通幂等

计费系统在收到支付回调后调用创建接口时，务必带上 `Idempotency-Key`（建议使用订单号），避免回调重试导致重复开通。

## WHMCS 服务器模块

面板内置 WHMCS 9.0 服务器开通模块（LXC/KVM），计费仍在 WHMCS 完成，模块负责把 WHMCS 的产品/服务生命周期映射到面板 API。管理员可在后台「API 集成」页直接下载，安装后即可在 WHMCS 中添加服务器使用。

```http
GET /api/v1/integrations/whmcs
```

返回模块元信息：模块名、展示名、版本、安装路径、文件清单与 README。

```json
{
  "success": true,
  "data": {
    "name": "eyvescloud",
    "display_name": "EYVESCLOUD WHMCS Server Module",
    "version": "1.0.0",
    "install_path": "modules/servers/eyvescloud",
    "panel_version": "1.6.2",
    "files": [{ "path": "eyvescloud.php", "size": 12345 }],
    "readme": "# EYVESCLOUD WHMCS 服务器开通模块 ..."
  }
}
```

```http
GET /api/v1/integrations/whmcs/download
```

返回 zip 归档（`Content-Type: application/zip`），顶层目录固定为 `modules/servers/eyvescloud/`，解压到 WHMCS 根目录即可完成安装。

两个接口均**仅管理员可用**。使用 `curl` 下载示例：

```bash
TOKEN="<管理员 JWT>"
curl -H "Authorization: Bearer $TOKEN" \
  -o eyvescloud-whmcs-module-1.0.0.zip \
  https://panel.example.com/api/v1/integrations/whmcs/download
```

模块能力概览：

| 类别 | 内容 |
| --- | --- |
| 生命周期 | 开通、暂停、恢复、删除、开关机、重启、改密、变更套餐、状态同步、用量上报 |
| 客户区 | 实例信息、NAT 转发、防火墙、快照、备份、ISO 挂载（KVM）、重装系统 |
| 控制台 | WebSSH、VNC（KVM） |
| 认证 | 服务器 Access Hash（或密码）填写面板 API Key，请求携带 `X-API-Key` 与 `Authorization: Bearer` |

模块使用的 API Key 需要授予以下 scope（也可直接授予 `*`）：

```text
container:read / container:create / container:power / container:delete
container:password / container:reinstall / container:resize / container:traffic / container:network
image:read            # 重装系统时读取可用镜像/模板列表
snapshot:read / snapshot:create / snapshot:restore / snapshot:delete
terminal:ssh / terminal:vnc / task:read / dashboard:read
admin:access   # 仅 KVM 客户区的 ISO 列表与挂载需要
```

缺少某个 scope 时，对应功能返回 `INSUFFICIENT_SCOPE`，其余功能不受影响。

详细配置与排障见模块内 `README.md`（随元信息接口一并返回）。

## 相关文档

- [容器管理](./containers.md)
- [网络与路由](./networking.md)
- [快照管理](./snapshots.md)
- [实例级备份](./backups.md)
- [安全告警](./security.md)
- [子用户](./sub-users.md)