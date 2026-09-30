# EyvesCloud API v2 规范（自研设计）

> 面向集成方：计费系统 / WHMCS 模块 / 运维自动化 / IaC 工具。
> 与面板内部使用的 `/api/v1/*` 并存：**v2 是给外部集成用的稳定契约**，v1 是面板前端自己的接口。
> 设计对标 Virtualizor / SolusVM / 魔方云的能力范围，但路径、参数、字段、错误码**均为自研**。

---

## 1. 设计原则

| 原则 | 说明 |
| --- | --- |
| 契约稳定 | v2 发布后路径 / 字段 / 错误码不再变更；破坏性调整只进 v3 |
| 一套凭据 | 复用面板既有认证（登录 JWT / 长期 API Key），不新增第二套 token 体系 |
| 响应可判定 | 统一信封 `{success, code, message, data, request_id}`，失败必须给出稳定 `code` |
| 分页一致 | 所有列表 `page` / `page_size` / `all`，响应统一 `data.pagination` |
| 幂等可重试 | 创建类接口支持 `Idempotency-Key`；节点通信失败返回 502 `UPSTREAM_ERROR` |
| 语义直连 | 字段直接映射 LXC/KVM 内部模型（不二次包装），状态枚举稳定 |

## 2. 认证

```http
Authorization: Bearer <access_token>    # POST /api/v2/auth/login 获取，24h 有效
X-API-Key: <api_key>                    # 长期密钥，带 scope、可绑定容器
```

- `access_token` 与面板登录共用签发体系（`iss`/`aud` 校验 + `token_version` 吊销），因此**改密/改绑定即时失效**。
- API Key 的 scope 决定可调用的接口；容器绑定（`container_uuids`）决定可见实例范围。
- 缺失/过期 → `401 UNAUTHENTICATED`；已认证但越权 → `403 PERMISSION_DENIED`。

## 3. 响应信封

成功：

```json
{ "success": true, "code": "OK", "data": { ... }, "request_id": "req_ab12cd34" }
```

列表：

```json
{
  "success": true,
  "code": "OK",
  "data": {
    "items": [ ... ],
    "pagination": { "page": 1, "page_size": 20, "total": 137, "pages": 7 },
    "summary": { "running": 120, "stopped": 17 }
  },
  "request_id": "req_ab12cd34"
}
```

失败：

```json
{ "success": false, "code": "INVALID_ARGUMENT", "message": "缺少必填字段",
  "details": { "name": "必填" }, "request_id": "req_ab12cd34" }
```

### 错误码表

| code | HTTP | 含义 |
| --- | --- | --- |
| `OK` | 200/201/202/204 | 成功 |
| `INVALID_ARGUMENT` | 400 | 参数缺失/非法（`details` 给字段级原因） |
| `UNAUTHENTICATED` | 401 | 未认证或凭据过期 |
| `PERMISSION_DENIED` | 403 | 无权限（scope / 容器绑定不满足） |
| `NOT_FOUND` | 404 | 资源不存在 |
| `METHOD_NOT_ALLOWED` | 405 | 方法不支持 |
| `CONFLICT` | 409 | 状态冲突（重名、节点不在线、重复操作） |
| `PRECONDITION_FAILED` | 412 | 前置条件不满足（实例锁定、配额不足、挂起/到期） |
| `RATE_LIMITED` | 429 | 触发限流（响应头 `X-RateLimit-*`） |
| `INTERNAL` | 500 | 服务内部错误 |
| `UPSTREAM_ERROR` | 502 | 被控节点通信/执行失败 |

## 4. 通用约定

- **分页**：`page`（默认 1）、`page_size`（默认 20，上限 200）、`all=true` 返回全量。
- **排序**：`sort=<field>` + `order=asc|desc`（默认 desc）；也接受 `sort=field:desc`。
- **搜索**：`q=<关键字>`（按实例名 / IP / UUID / 备注等）。
- **时间**：全部 RFC3339（如 `2026-09-29T02:10:00+08:00`）；无值返回空串。
- **状态枚举（实例）**：`running` / `stopped` / `creating` / `suspended` / `error` / `unknown`。
- **ID 解析**：路径中的 `{id}` 支持数字 ID、UUID、实例名三种写法。
- **请求体**：仅接受 `application/json`（`PATCH` 为部分更新语义）。
- **审计**：所有写操作记录到面板审计日志（含操作人、IP、UA）。

## 5. 端点清单（已实现）

### 5.1 认证与会话

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v2/auth/login` | 登录，返回 `access_token`（支持两步验证 `code`） |
| POST | `/api/v2/auth/logout` | 退出（无状态 token，客户端丢弃即可） |
| GET | `/api/v2/auth/me` | 当前身份、角色与权限点 |

### 5.2 实例（LXC 容器 / KVM 虚拟机）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v2/instances` | 列表：`status` `runtime` `node_id` `owner` `tenant` `template_id` `locked` `min_memory_mb` `max_memory_mb` `q` |
| POST | `/api/v2/instances` | 创建（见 5.2.1） |
| GET | `/api/v2/instances/{id}` | 详情（含快照/备份/防火墙规则） |
| PATCH | `/api/v2/instances/{id}` | 部分更新：`vcpu` `cpu_percent`(1-100) `memory_mb` `disk_gb` `data_disk_gb` `down_mbps` `up_mbps` `traffic_quota_gb` `snapshot_limit` `remark` `expires_at` `tenant` `firewall_enabled` |
| DELETE | `/api/v2/instances/{id}` | **软删除（进回收站）**；`?purge=true` 才真销毁（计费终止/资源释放） |
| GET | `/api/v2/recycle-bin` | 回收站列表（`?recycled=true` 同样适用于 instances 列表） |
| POST | `/api/v2/instances/{id}/restore` | 从回收站恢复（同名活跃实例冲突 → 409） |
| POST | `/api/v2/instances/{id}/purge` | 彻底删除（真销毁数据面；锁定实例 412） |
| GET | `/api/v2/instances/export.csv` | 实例 CSV 导出（尊重调用方可见范围；UTF-8 BOM） |
| POST | `/api/v2/instances/{id}/power` | `{"action": "start\|stop\|shutdown\|restart\|hard-stop\|hard-restart\|suspend\|unsuspend"}`。`hard-stop` = **强制断电（保留磁盘）**，绝不删除实例；`suspend` = 欠费停机（停机+标记，开机被拦）；`unsuspend` 解除 |
| POST | `/api/v2/instances/{id}/reinstall` | `{"template_id", "password"?}` |
| POST | `/api/v2/instances/{id}/reset-password` | `{"password"?}`（留空自动生成，仅返回一次） |
| POST | `/api/v2/instances/{id}/console` | `{"type": "ssh\|vnc"}` → 一次性票据（60s）+ 连接地址 |
| GET | `/api/v2/instances/{id}/metrics` | 实时 CPU/内存/网络/磁盘 |
| GET | `/api/v2/instances/{id}/usage` | 流量与配额用量、到期状态 |
| GET | `/api/v2/instances/{id}/events` | 该实例的审计事件（分页） |
| GET | `/api/v2/instances/{id}/xml` | KVM libvirt XML（LXC 返回 400） |
| GET/POST | `/api/v2/instances/{id}/snapshots` | 快照列表 / 创建 `{"name"?}` |
| POST | `/api/v2/instances/{id}/snapshots/{sid}/restore` | 从快照恢复 |
| DELETE | `/api/v2/instances/{id}/snapshots/{sid}` | 删除快照 |
| GET/POST | `/api/v2/instances/{id}/backups` | 备份列表 / 创建完整备份 |
| DELETE | `/api/v2/instances/{id}/backups/{bid}` | 删除备份 |
| POST | `/api/v2/instances/{id}/clone` | `{"name"}` 克隆实例 |
| POST/DELETE | `/api/v2/instances/{id}/rescue` | 进入（`{"iso_id"}`）/ 退出救援模式（KVM） |
| POST/DELETE | `/api/v2/instances/{id}/lock` | 锁定 / 解锁（锁定后禁止删除、重装等破坏性操作） |
| PUT | `/api/v2/instances/{id}/network` | `nat_ports[]` / `public_ipv4_count` / `ipv6_count` |
| POST | `/api/v2/instances/batch` | `{"action": "power\|delete\|reinstall\|reset-password\|remark\|expiry", "ids": [], "params"/"template_id"?}`。扩展动作：`reset-password`（params.password 省略=逐实例随机，响应逐实例返回新口令仅此一次）、`remark`（params.remark）、`expiry`（params.expires_at） |
| POST | `/api/v2/ip-pools/attach` | 弹性 IP 绑定：`{"address", "instance_id"}`（地址必须在池中且未被其它实例占用） |
| POST | `/api/v2/ip-pools/detach` | 弹性 IP 解绑：`{"address"}`（地址回到池中） |

#### 5.2.1 创建实例请求体

```json
{
  "name": "web-01",
  "runtime": "lxc",                 // lxc | kvm
  "template_id": "debian-bookworm",
  "node_id": "auto",                // 空=本机; "auto"=调度器挑选; 或具体节点 ID
  "node_priority": 1,               // 1 均衡(默认) 2 负载最低 3 内存最空
  "count": 1,                       // 批量 1-50
  "vcpu": 2, "memory_mb": 2048, "disk_gb": 20, "data_disk_gb": 0,
  "down_mbps": 100, "up_mbps": 100,
  "traffic_quota_gb": 1000, "traffic_mode": "total",     // total | in_out
  "ssh_port": 0,                    // 0 = 自动分配
  "nat_ports": 2, "assign_nat": true,
  "public_ipv4_count": 0, "ipv6_count": 1,
  "storage_pool_id": "",
  "auth": { "mode": "password", "password": "", "ssh_key_ids": [] },
  "cloud_init": "",
  "snapshot_limit": 3,
  "owner": "alice", "tenant": "acme", "remark": "客户A 生产",
  "expires_at": "2027-01-01",
  "firewall_enabled": false
}
```

响应（201）：

```json
{ "success": true, "code": "OK", "data": {
  "count": 1, "runtime": "lxc", "node_id": "node-xxx", "task_ids": ["t-xxx"],
  "items": [ { "name": "web-01", "task_id": ["t-xxx"] } ],
  "password": "自动生成时仅此一次返回"
} }
```

## 6. 规划中（下一批）

| 模块 | 端点前缀 | 状态 |
| --- | --- | --- |
| 节点 | `/api/v2/nodes`、`/nodes/{id}/metrics`、`/nodes/{id}/maintenance`、`/nodes/schedule` | 待实现（调度决策已具备内部能力） |
| 节点分组 / 区域 | `/api/v2/node-groups`、`/regions` | 待实现 |
| 镜像与模板 | `/api/v2/images`、`/images/{id}/download`、`/iso-images` | 待实现 |
| 存储池 | `/api/v2/storage-pools`、`/storage-pools/{id}/status` | 待实现 |
| 安全组与规则 | `/api/v2/security-groups`、`/security-groups/{id}/rules` | 待实现 |
| IP 池 / IP 分组 | `/api/v2/ip-pools` | 待实现 |
| SSH 密钥 | `/api/v2/ssh-keys` | 待实现 |
| 任务 | `/api/v2/tasks`、`/tasks/{id}`、`/tasks/{id}/cancel` | 待实现 |
| 备份计划 | `/api/v2/backup-plans` | 待实现 |
| 用户与管理员 | `/api/v2/users`、`/admins`、`/api-keys` | 待实现 |
| 审计日志 | `/api/v2/audit-logs` | 待实现 |
| 监控与仪表盘 | `/api/v2/metrics/*`、`/summary` | 待实现 |
| 系统 | `/api/v2/system/info`、`/health`、`/update` | 待实现 |
| 事件订阅 | `/api/v2/webhooks` | 待实现 |
| 实例迁移 | `POST /api/v2/instances/{id}/migrate` | 待实现（需接入节点迁移流水线） |

> 范围裁剪（**有意不实现**，与 LXC/KVM 无关）：财务/账单、ADSL 拨号云、Hyper-V 专属参数、
> VPC 专有网络、数据库实例、负载均衡、独立产品与资源包、授权与插件市场。
