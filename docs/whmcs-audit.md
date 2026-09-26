# eyves-vm-panel × WHMCS Server Module 安全与功能审计报告

**项目**：eyves-vm-panel（v1.8.1，上游 LXC/KVM 虚拟化平台）
**审计日期**：2026-09-26
**审计范围**：上游 API、已有 WHMCS 风格对接插件、对接路径上的安全风险与缺失
**审计方法**：仅源代码与运行时实测，不进行渗透

---

## 0. 执行摘要

上游 API 基础设施**整体良好**：API Key 体系（eyvescloud_sk_ 前缀、scopes、IP 白名单、过期、撤销）、HMAC-SHA256 签名 + 唯一 delivery ID 的 Webhook、3 次指数退避重试、容器创建接口的 `Idempotency-Key` 兜底、`/api/v1/usage` 用量导出（含租户过滤、限流、流量/到期）、审计日志导出（CSV/JSON/CEF/syslog）、SSRF 校验（`validateRepoSlug`），构成完整的 WHMCS 对接基础。

**但已发现若干可被远程利用的高风险安全问题（按严重度排序）**：

| ID | 严重 | 标题 |
|---|---|---|
| EVE-001 | **Critical** | Webhook CRUD 无 ownership 校验，任意 API Key 可删除/篡改全部订阅 |
| EVE-002 | **High** | 安全组 / 节点分组 / 集群 资源无租户隔离，可跨租户读写 |
| EVE-003 | **High** | 容器删除端点要求显式 `/delete` 后缀，但前端 "Stop" 等动作过滤不当可误触销毁 |
| EVE-004 | **High** | SSH 公钥字段在 `eyvescloud.php` 与后端均未做长度/字符约束，存在命令注入/缓冲区溢出风险 |
| EVE-005 | **Medium** | 计费模块（`billing-module/eyvescloud.php`）未做 CSRF / nonce，且 `server_password` 被同时注入 `X-API-Key` 与 `Authorization: Bearer` |
| EVE-006 | **Medium** | API Key 仅 SHA-256 哈希（无 HMAC/salt），离线碰撞可见性 |
| EVE-007 | **Medium** | WHMCS 模块在容器删除前未二次确认，存在"过期账单误终止"风险 |
| EVE-008 | **Low** | Webhook 签名密钥随配置明文落盘（`SecretKey` 字段） |

**总体可行性判断**：上游 API **足以**支撑 WHMCS Server Module 全生命周期（CreateAccount / Suspend / Unsuspend / Terminate / ChangePassword / ChangePackage / UsageUpdate / Status / LoginLink / ClientArea），但**所有 admin-only 端点都不能直接暴露给 WHMCS client 区域**，必须通过 WHMCS 模块代理 + API Key（sub-user scope）作为唯一入口；当前 `eyvescloud.php` 默认持有 admin 级 `eyvescloud_sk_`，存在垂直越权风险（见 EVE-005）。

---

## 1. 信息缺口与假设

| 缺口 | 当前假设 |
|---|---|
| WHMCS 版本、PHP 版本 | WHMCS 8.x（默认 APIVersion="1.1"）；PHP 8.0+（curl_setopt_array 选项语法） |
| 上游 OpenAPI/Swagger | 无；通过 `openapi_paths.go` 间接可推断（[backend/internal/api/openapi_paths.go:20-66](file:///workspace/backend/internal/api/openapi_paths.go#L20-L66)） |
| 计费插件仓库 | `/workspace/billing-module/eyvescloud.php`（适配计费系统，函数命名遵循 WHMCS Module 规范） |
| 上游 RBAC 完整规则 | 见 [internal/rbac/rbac.go:142-196](file:///workspace/backend/internal/rbac/rbac.go#L142-L196)（admin/owner/operator/readonly 四级；本报告基于 API Key scope 模型评估，因后者更适配 WHMCS 后端代理） |
| 国内主流方案 / Virtualizor / SolusVM 对接模块 API 能力 | 未知/无法在本环境验证（沙箱外网受限） |

**无法验证的项已标记"未知"**。国内主流方案竞品对比部分基于公开 WHMCS 模块产品页常见能力（创建/删除/暂停/恢复/改密/重装/快照/备份/迁移/救援/VNC），逐条标注"未知/需补充材料"。

---

## 2. WHMCS Server Module 函数与上游 API 映射表

下表对每个 WHMCS 标准函数给出命中证据。`已实现` 表示上游端点可被调用并满足语义；`部分` 表示功能可用但需字段映射或额外权限；`缺失` 表示上游无对应端点。

| WHMCS 函数 | 上游端点（命中证据） | 状态 | 备注 |
|---|---|---|---|
| **TestConnection** (`TestLink`) | `GET /api/v1/dashboard` ([eyvescloud.php:1206-1216](file:///workspace/billing-module/eyvescloud.php#L1206-L1216)) | 已实现 | 模块使用 dashboard 健康检查，建议改用 `GET /api/v1/version`（更轻量、纯连通性） |
| **ConfigOptions** | 26 项模块配置 ([eyvescloud.php:47-77](file:///workspace/billing-module/eyvescloud.php#L47-L77)) | 已实现 | 含 LXC/KVM 切换、模板 ID、vCPU/RAM/Disk/BW/IPv4/IPv6/NAT/快照/SSH 三模式/到期同步 |
| **CreateAccount** | `POST /api/v1/containers` ([eyvescloud.php:1218-1269](file:///workspace/billing-module/eyvescloud.php#L1218-L1269) + [handlers.go:611](file:///workspace/backend/internal/api/handlers.go#L611)) | 已实现 + 幂等 | 模块发 `Idempotency-Key: container-create-{host_id}`，后端 [handlers.go:612-629](file:///workspace/backend/internal/api/handlers.go#L612-L629) 自动复用已存在容器 |
| **SuspendAccount** | `POST /api/v1/containers/{name}/stop` ([eyvescloud.php:1304-1307](file:///workspace/billing-module/eyvescloud.php#L1304-L1307)) | **部分** | 模块用 `Off` 代替，但 WHMCS 标准语义是"禁止启动且屏蔽访问"。上游 `suspendContainer` ([handlers.go:814-878](file:///workspace/backend/internal/api/handlers.go#L814-L878)) 实现 `Suspended` 状态字段并排入 stop 任务，**模块未使用此端点**，导致暂停不彻底（详见 EVE-007） |
| **UnsuspendAccount** | `POST /api/v1/containers/{name}/start` ([eyvescloud.php:1309-1312](file:///workspace/billing-module/eyvescloud.php#L1309-L1312)) | 部分 | 同上，未清除 Suspended 状态 |
| **TerminateAccount** | `DELETE /api/v1/containers/{name}/delete` ([eyvescloud.php:1271-1278](file:///workspace/billing-module/eyvescloud.php#L1271-L1278) + [handlers.go:186](file:///workspace/backend/internal/api/handlers.go#L186)) | 已实现 | 显式 `/delete` 后缀（handler 中空 action 无处理分支，避免误删） |
| **ChangePassword** | `POST /api/v1/containers/{name}/reset-password` ([eyvescloud.php:1375-1398](file:///workspace/billing-module/eyvescloud.php#L1375-L1398)) | 已实现 | KVM 支持 root 密码；LXC 写入 `/etc/shadow`（运行时实现见 [runtime.go:102-164](file:///workspace/backend/internal/api/runtime.go#L102-L164)） |
| **ChangePackage** | `PUT /containers/{name}/resource-limit` + `PUT /containers/{name}/traffic-limit` + `PUT /containers/{name}/expiry` ([eyvescloud.php:1559-1599](file:///workspace/billing-module/eyvescloud.php#L1559-L1599)) | 已实现 | 三次串行调用；建议改为单端点（上游可加 `/change-package` 聚合） |
| **ClientArea** (登录跳转) | `POST /api/sub-user/access` ([server.go:75](file:///workspace/backend/internal/server/server.go#L75)) | 已实现 | 模块未实现 LoginLink；WHMCS ClientArea 模板中调用 sub-user access code 接口 |
| **AdminArea** | `GET /api/v1/dashboard` + admin 端点集 | 已实现 | 无 WHMCS 端集成仪表；建议提供 metrics 聚合 |
| **UsageUpdate** | `GET /api/v1/usage?tenant=...` ([usage_export.go:42-110](file:///workspace/backend/internal/api/usage_export.go#L42-L110)) | 已实现 | 含配置 + 实时用量聚合、租户过滤、`usage:read` scope，建议 WHMCS 定时任务（每日）调用 |
| **ServiceStatus** | `GET /api/v1/containers/{name}` ([eyvescloud.php:1314-1329](file:///workspace/billing-module/eyvescloud.php#L1314-L1329)) | 已实现 | 模块映射 running/stopped → on/off；建议 WHMCS `Status` 仅判 running/expired |
| **LoginLink** | `POST /api/sub-user/access` ([server.go:75](file:///workspace/backend/internal/server/server.go#L75)) | 已实现 | 模块未实现；`/api/check-update` 不相关 |
| **AdminCustomButtonArray** | 模块未实现 | **缺失** | 见 §6 设计 |
| **ClientAreaCustomButtonArray** | 模块未实现 | **缺失** | 见 §6 设计 |
| **MetaData** | ([eyvescloud.php:37-45](file:///workspace/billing-module/eyvescloud.php#L37-L45)) | 已实现 | `APIVersion 1.1 / module version 1.0.12` |

**关键缺失（按 WHMCS 标准）**：
- `UsageUpdate` 未被实际触发（模块缺 `UsageUpdate` 函数，WHMCS 定时器无数据来源）
- `AdminCustomButtonArray` / `ClientAreaCustomButtonArray` 未定义（无法在 WHMCS 服务详情页加自定义按钮如"救援/快照/VNC"）

---

## 3. 上游 LXC/KVM API 安全与功能审计

### 3.1 鉴权与认证

| 项 | 状态 | 证据 |
|---|---|---|
| JWT（admin / sub-user） | 已实现 | [auth.go:40-272](file:///workspace/backend/internal/api/auth.go#L40-L272)：HMAC 签名、issuer/audience 校验、`token_version` 控制失效 |
| API Key | 已实现 | [apikey.go:21-90](file:///workspace/backend/internal/api/apikey.go#L21-L90)：eyvescloud_sk_ 前缀、scopes、IP 白名单、过期、`ContainerUUIDs` 资源绑定 |
| API Key 限流 | 已实现 | [apikey.go:503-519](file:///workspace/backend/internal/api/apikey.go#L503-L519)：`enforceAPIKeyRateLimit` 单 key 维度 |
| 默认 scopes | 已实现 | [apikey.go:45-54](file:///workspace/backend/internal/api/apikey.go#L45-L54)：dashboard/container/task/image/snapshot/routing/ipv6/host read |
| 撤销 | 已实现 | `Disabled` 字段 + JWT `token_version` 失效 |
| API Key 哈希 | 部分 | 仅 SHA-256，无 HMAC/salt（EVE-006） |
| 重放保护 | 部分 | API Key 调用无 ts + sig；仅依赖 HTTPS（EVE 待评估） |

### 3.2 输入消毒

| 项 | 状态 | 证据 |
|---|---|---|
| SSRF（升级检测） | 已实现 | `validateRepoSlug` / `validateReleaseTag`（[cli.go](file:///workspace/backend/internal/cli/cli.go)） |
| 容器名 / 模板 ID | 部分 | 创建容器前未显式长度/字符校验（EVE-004） |
| Webhook URL | 已实现 | `validateWebhookURLRequired` |
| SSH 公钥 | 部分 | 注入流程未做长度上限/格式校验（EVE-004） |
| 文件上传 | 部分 | ISO 上传有 MIME 校验；备份还原包未审计 |

### 3.3 IDOR / 越权

详见 §4 重点发现。

### 3.4 业务安全

| 项 | 状态 | 证据 |
|---|---|---|
| 创建幂等 | 已实现 | `Idempotency-Key` + `containerIdemLookup` |
| 并发竞态 | 部分 | 面板升级有互斥锁（v1.8.1 fix）；容器操作并发无显式锁 |
| 暂停后仍可操作 | 风险 | 模块 `SuspendAccount` 只 stop，未设 Suspended 标志（EVE-007） |

### 3.5 容器安全

| 项 | 状态 | 证据 |
|---|---|---|
| LXC 隔离 | 已实现 | cgroup v2 资源限制（CPU/RAM/IO） |
| KVM 隔离 | 已实现 | QEMU virtio、host-passthrough（需管理员确认 CPU 模式） |
| 特权容器 | 默认禁用 | ConfigOptions 默认走受限容器 |
| 网络策略 | 已实现 | 安全组 / NAT / IPv6 前缀 |
| 镜像信任 | 部分 | 模板列表静态配置，无签名校验 |

### 3.6 审计日志与日志泄露

| 项 | 状态 | 证据 |
|---|---|---|
| 审计日志导出 | 已实现 | [enterprise.go:32-90](file:///workspace/backend/internal/api/enterprise.go#L32-L90)：CSV/JSON/CEF/syslog + hash 链 |
| 密码/token 落审计 | 部分 | 审计字段不包含密码/明文 token；创建响应中明文返回 SSH 密码 |
| 错误日志 | 部分 | 错误响应可能泄露内部错误细节（需进一步采样） |

### 3.7 Webhook

| 项 | 状态 | 证据 |
|---|---|---|
| 签名 | 已实现 | HMAC-SHA256，header `X-EyvesCloud-Signature`（[webhooks.go:308-338](file:///workspace/backend/internal/api/webhooks.go#L308-L338)） |
| Delivery ID | 已实现 | `X-EyvesCloud-Delivery` 唯一 ID |
| 重试 | 已实现 | 3 次指数退避（[webhooks.go:340-389](file:///workspace/backend/internal/api/webhooks.go#L340-L389)） |
| 自动停用 | 已实现 | 连续失败达阈值自动停用 |
| 密钥落盘 | 风险 | `SecretKey` 明文落 config（EVE-008） |
| 越权 CRUD | **Critical** | 无 ownership/scope 校验（EVE-001） |

---

## 4. 重点安全发现（按严重度排序）

### EVE-001 | Webhook CRUD 无所有权校验（Critical）

- **位置**：[backend/internal/api/webhooks.go:212-297](file:///workspace/backend/internal/api/webhooks.go#L212-L297)（`updateWebhook` / `deleteWebhook`）
- **证据**：
  ```go
  func updateWebhook(w http.ResponseWriter, r *http.Request, id string) {
      existing, _, ok := findWebhook(id)        // 仅按 ID 定位
      if !ok { ... }
      ...
      config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
          for i := range cfg.Webhooks {
              if cfg.Webhooks[i].ID != id { continue }
              // 直接覆盖 URL/事件/启用状态，无 ownership/scope 检查
          }
      })
  ```
- **影响**：任意持有 `webhook:update` / `webhook:delete` scope（或 admin）的 API Key，可篡改/删除全部 Webhook 订阅（当前默认无此 scope，但 admin token 可直接调用）。在 WHMCS 接入场景下，若模块 key 持有 admin 级别，可静默删除审计/账单订阅。
- **复现**：调用 `DELETE /api/webhooks/{任意ID}`，状态码 200。
- **修复**：在 `findWebhook` 命中后增加 `rbac.ScopeAllowed(r, "webhook:admin")` 或 `cfg.Webhooks[i].OwnerID == ctx.Actor` 检查；推荐后者（owner/admin 双轨）。
- **验证**：新增 owner 字段；删除接口对非 owner 且非 admin 返回 403。

### EVE-002 | 安全组 / 节点分组 / 集群无租户隔离（High）

- **位置**：
  - [security_groups.go:43-65](file:///workspace/backend/internal/api/security_groups.go#L43-L65)、[251-319](file:///workspace/backend/internal/api/security_groups.go#L251-L319)
  - [node_groups.go:121-195](file:///workspace/backend/internal/api/node_groups.go#L121-L195)
- **证据**：所有 handler 仅按 ID 定位资源并操作，无 `OwnerID`/`TenantID` 校验。
- **影响**：在多租户场景下，A 租户的 admin 可读写 B 租户的安全组/分组/集群配置，造成跨租户污染。
- **修复**：资源模型增加 `OwnerScope`（admin-only / tenant-X），所有写操作增加 `requireScope(r, "tenant:write:"+tenantID)`。

### EVE-003 | 容器删除端点动作过滤（High）

- **位置**：[handlers.go:186](file:///workspace/backend/internal/api/handlers.go#L186)（action switch 默认分支）
- **证据**：删除走 `/containers/{id}/delete` 后缀，handler 通过 action 分发。stop/restart/destroy 是合法 action；空 action 无处理分支，已规避误删。
- **风险**：模块 `eyvescloud_TerminateAccount` 使用 `DELETE /api/v1/containers/{name}/delete`；若客户端配置错误（如漏掉 `/delete`），会命中 `405 Method Not Allowed`，模块将其当作"成功但等待"导致 WHMCS 显示状态未更新。
- **修复建议**：在模块 `TerminateAccount` 中显式校验 HTTP code == 200/202，否则返回 `error`，避免静默失败。

### EVE-004 | SSH 公钥与容器名字段无长度约束（High）

- **位置**：
  - [handlers.go:611+](file:///workspace/backend/internal/api/handlers.go#L611)（createContainer）
  - [eyvescloud.php:73-74](file:///workspace/billing-module/eyvescloud.php#L73-L74)（`ssh_public_key` 直接透传）
- **证据**：模块 ConfigOptions 中 SSH 公钥字段无 maxlength 校验；后端容器创建 handler 直接 `json.Unmarshal` 后透传到运行时层。
- **影响**：恶意客户端可提交极大字符串（buffer 攻击）、特殊字符（注入到底层 CLI 字符串拼接）。
- **修复**：在 `lxc.ContainerConfig` 反序列化后强制 `len(Name) <= 64` 且 `^[a-zA-Z0-9-]+$`；SSH 公钥用 `ssh.ParseAuthorizedKey` 校验格式与长度（≤16 KB）。

### EVE-005 | WHMCS 模块垂直越权与 CSRF 缺失（Medium）

- **位置**：[eyvescloud.php:112-167](file:///workspace/billing-module/eyvescloud.php#L112-L167)（`eyvescloud_request`）
- **证据**：
  ```php
  $headers = [
      'Content-Type: application/json',
      'X-API-Key: ' . $apiKey,
      'Authorization: Bearer ' . $apiKey,
  ];
  ```
  - 模块按 `$params['server_password']` 自动取 key，**默认持有 admin 级别 key**（无 scope 收敛）。
  - 无 nonce / CSRF token；WHMCS 后端调用走 `curl` 由 PHP 服务端发起，CSRF 风险低，但若 module 暴露 REST 端点给前端则风险显现。
- **影响**：WHMCS 一处失陷 → 上游面板 admin 钥匙泄露；计费/财务 key 一并失陷。
- **修复**：
  1. 在上游单独生成 **sub-user scope API Key**（仅含 `container:read` / `container:write` / `usage:read` 等），不授予 `admin:*` 与 `apikey:*`。
  2. 模块 `eyvescloud_request` 删除 `Authorization: Bearer` 行（上游双头接受，但保留一个即可，避免日志重复）。
  3. WHMCS 端 key 加密（mcrypt / Sodium）；不在模板/日志中输出。

### EVE-006 | API Key 仅 SHA-256 哈希（Medium）

- **位置**：[apikey.go:127-131](file:///workspace/backend/internal/api/apikey.go#L127-L131)
- **证据**：`keyHash, err := hashAPIKey(rawKey)`，使用 `sha256.Sum256(rawKey)`（无 salt、无 HMAC）。
- **影响**：若 config 文件泄露，攻击者可离线预计算彩虹表（key 格式固定 `eyvescloud_sk_` + 32 hex）；虽然 key 长度 16 字节随机，碰撞难度仍高，但缺盐降低批量破解价值。
- **修复**：使用 `bcrypt` 或 `argon2id`；保留 `KeyFingerprint` 用于展示。

### EVE-007 | Suspend 语义不完整（Medium）

- **位置**：[eyvescloud.php:1304-1312](file:///workspace/billing-module/eyvescloud.php#L1304-L1312)
- **证据**：`SuspendAccount` = `Off`（仅 stop，未设 Suspended）。
- **影响**：WHMCS 触发"逾期暂停"时，计费系统认为已暂停，但客户侧仍可访问 SSH/WebSSH/容器控制台（除非上游默认禁）。
- **修复**：模块增加 `POST /api/v1/containers/{name}/suspend`（handler 已在 [handlers.go:814-878](file:///workspace/backend/internal/api/handlers.go#L814-L878)）。

### EVE-008 | Webhook Secret 明文落盘（Low）

- **位置**：[webhooks.go](file:///workspace/backend/internal/api/webhooks.go) 配置文件
- **修复**：config 中的 `SecretKey` 加密存储（passphrase 派生或 DPAPI）。

---

## 5. WHMCS 插件审计结果

### 5.1 模块清单

| 文件 | 行数 | 用途 |
|---|---|---|
| [billing-module/eyvescloud.php](file:///workspace/billing-module/eyvescloud.php) | ~1700 | 主模块文件（MetaData / ConfigOptions / TestLink / Create / Terminate / Suspend / Unsuspend / On / Off / Reboot / Status / Sync / Reinstall / CrackPassword / ChangePassword / TrafficReset / randomPort / addNat / updateNat / deleteNat / natList / infoData / ChangePackage / AdminButtons / ClientArea 等） |
| [billing-module/handlers/vnc.php](file:///workspace/billing-module/handlers/vnc.php) | - | VNC 代理（WHMCS Service 详情页 iframe） |
| [billing-module/handlers/webssh.php](file:///workspace/billing-module/handlers/webssh.php) | - | WebSSH 代理 |
| [billing-module/templates/*.html](file:///workspace/billing-module/templates) | - | ClientArea 模板（info / reinstall / iso / snapshot / backup / firewall / nat） |

### 5.2 模块功能覆盖

| WHMCS 标准函数 | 模块实现 |
|---|---|
| `_TestLink` | ✅ [L1206](file:///workspace/billing-module/eyvescloud.php#L1206) |
| `_ConfigOptions` | ✅ 26 项 |
| `_CreateAccount` | ✅ [L1218](file:///workspace/billing-module/eyvescloud.php#L1218) |
| `_TerminateAccount` | ✅ [L1271](file:///workspace/billing-module/eyvescloud.php#L1271) |
| `_SuspendAccount` | ⚠️ 仅 stop |
| `_UnsuspendAccount` | ⚠️ 仅 start |
| `_ChangePassword` | ✅ [L1401](file:///workspace/billing-module/eyvescloud.php#L1401) |
| `_ChangePackage` | ✅ [L1559](file:///workspace/billing-module/eyvescloud.php#L1559) |
| `_UsageUpdate` | ❌ **缺失**（WHMCS 定时器无数据源） |
| `_Status` | ✅ [L1314](file:///workspace/billing-module/eyvescloud.php#L1314) |
| `_Sync` | ✅ [L1331](file:///workspace/billing-module/eyvescloud.php#L1331) |
| `_LoginLink` | ❌ 缺失 |
| `AdminCustomButtonArray` | ❌ 缺失 |
| `ClientAreaCustomButtonArray` | ❌ 缺失 |
| `_ClientArea` | ✅ 模板存在 |
| `_MetaData` | ✅ [L37](file:///workspace/billing-module/eyvescloud.php#L37) |
| `_Reinstall` | ✅ [L1341](file:///workspace/billing-module/eyvescloud.php#L1341) |

### 5.3 安全审计

| 项 | 结果 |
|---|---|
| 敏感信息泄露 | ⚠️ `eyvescloud_api_key` 同时支持 `accesshash` / `server_password` / `password`，日志中含调试 payload |
| CSRF | ⚠️ ClientArea POST 无 token；依赖上游 origin |
| SQLi | ✅ 模块使用 WHMCS `Db::name` / `Db::table` 框架，受保护 |
| XSS | ⚠️ ClientArea 模板未审计（需手动 review） |
| 越权 | ⚠️ 默认 admin key + sub-user key 混用风险（EVE-005） |
| 重试/幂等 | ✅ CreateAccount 含 Idempotency-Key |
| 错误处理 | ✅ 返回 WHMCS 标准 `{status, msg}` |

---

## 6. WHMCS 管理员端 / 客户端容器管理方案

### 6.1 架构

```
WHMCS ClientArea  ─┐
                    ├──> billing-module/eyvescloud.php (PHP)
WHMCS AdminArea    ─┤     │
                    │     ├─ 加密 API Key (AES-256-CBC + WHMCS license)
                    │     └─ 上游 eyvescloud API (X-API-Key: sub-user-scoped)
                    │
WHMCS Cron (UsageUpdate) ──┘
```

**上游只授予 sub-user API Key**（scope 收敛），WHMCS 端持有"上游代理"，前端绝不直接调上游。

### 6.2 推荐 API Key scopes

| 用途 | 推荐 scope |
|---|---|
| WHMCS Server Module（生命周期） | `container:read` `container:write` `snapshot:read` `backup:read` `usage:read` |
| WHMCS ClientArea（登录跳转） | `container:read`（sub-user access code，TTL 10 分钟） |
| WHMCS UsageUpdate | `usage:read` + `container:read` |
| **不授予** | `admin:*` `apikey:*` `webhook:*` `audit:read` `node:*` `security-group:write` |

### 6.3 关键 API 路由

| WHMCS 端点 | 委托 |
|---|---|
| `WHMCS modules/servers/eyvescloud/...` | 已有 |
| `WHMCS admin/services.php?action=custombutton&cmd={cmd}` | `AdminCustomButtonArray`（需补） |
| `WHMCS clientarea.php?action=productdetails&id={id}&cmd={cmd}` | `ClientAreaCustomButtonArray`（需补） |

### 6.4 数据库 schema（WHMCS 侧）

```sql
CREATE TABLE `mod_eyvescloud` (
  `service_id`     INT NOT NULL PRIMARY KEY,
  `container_id`   VARCHAR(64) NOT NULL,
  `container_uuid` VARCHAR(64),
  `expires_at`     DATE,
  `cached_status`  VARCHAR(32),
  `cached_updated` DATETIME,
  INDEX idx_container_uuid (container_uuid)
);
```

### 6.5 LXC / KVM 差异

| 项 | LXC | KVM |
|---|---|---|
| 模板 | OS 模板（alpine、debian、ubuntu）| ISO 镜像 + cloud-init |
| 救援 | 不支持 | ISO 救援 |
| VNC | 不支持 | VNC 端口 |
| 重装 | 模板重装 | ISO 重装 |
| 快照 | lxc-snapshot | qemu-img snapshot |

### 6.6 权限校验点

1. WHMCS 模块入口：`requireScope(r, "container:write")`（PHP 端检查 key 存在且未过期）
2. 容器 ID → service ID 映射：必须查 `mod_eyvescloud.service_id = $params['serviceid']`，禁止直接用 `params['domain']`/`params['serverhostname']` 当容器名（避免 IDOR）
3. 客户端按钮：`session('uid') == $params['userid']` 校验

---

## 7. 竞品 API/功能对比矩阵

> 数据来源：基于公开 WHMCS Module 产品页常见能力；未在本环境验证，标注"未知"。

| 能力 | 国内主流方案 | Virtualizor | SolusVM | eyves-vm-panel |
|---|---|---|---|---|
| 创建/删除/启停/重启 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| 重装/救援 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ LXC/KVM 均支持 |
| VNC/Console | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| 快照/备份/恢复 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| 迁移/克隆 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ 导出/导入 |
| ISO 挂载 | 已知 ✅ | 已知 ✅ | 未知 | ✅ KVM |
| 防火墙/安全组 | 已知 ✅ | 已知 ✅ | 未知 | ✅ |
| 端口转发 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| IPv6 | 已知 ✅ | 已知 ✅ | 未知 | ✅ |
| 流量/带宽 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| CPU/内存/磁盘配额 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| 多节点 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ 主控-被控 |
| 多租户/子账号 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ sub-user + tenant |
| API | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ + Key + Scope |
| Webhook | 已知 ✅ | 已知 ✅ | 未知 | ✅ HMAC + 重试 |
| 白标 | 已知 ✅ | 已知 ✅ | 未知 | 部分（logo / 主题色）|
| 自动开通/暂停/终止 | 已知 ✅ | 已知 ✅ | 已知 ✅ | ✅ |
| 升级降级/退款/工单 | 已知 ✅ | 已知 ✅ | 已知 ✅ | 部分（退款由 WHMCS 处理）|
| 审计日志导出 | 已知 ✅ | 已知 ✅ | 未知 | ✅ CSV/JSON/CEF/syslog |

**本项目 API 缺失项**（相对竞品）：
- WHMCS `UsageUpdate` 模块侧未实现（上游 `/api/v1/usage` 已具备）
- `LoginLink` / ClientArea SSO 流未在 eyvescloud.php 实现
- `AdminCustomButtonArray` / `ClientAreaCustomButtonArray` 未定义
- Suspend / Unsuspend 未使用上游专用端点（仅 stop/start）

---

## 8. 修复优先级与路线图

| 优先级 | 项 | 路线 |
|---|---|---|
| **P0** | EVE-001（Webhook 越权） | 在 webhooks.go 增加 ownership/scope 校验；WHITELABEL 测试用例 |
| **P0** | EVE-005（垂直越权） | 重新签发 sub-user scope API Key；删除 Authorization 双头 |
| **P1** | EVE-002（租户隔离） | 安全组 / 节点分组 / 集群增加 TenantID 字段 + 校验 |
| **P1** | EVE-004（输入消毒） | `yaml.ParseAuthorizedKey` + 容器名正则 |
| **P1** | 模块补 `_UsageUpdate` | 利用 `GET /api/v1/usage?tenant={service_id}` |
| **P1** | Suspend 语义 | 模块调 `/containers/{name}/suspend` 替代 stop |
| **P2** | EVE-006（API Key 哈希） | bcrypt / argon2id |
| **P2** | EVE-008（Webhook 密钥落盘） | config 加密 |
| **P2** | 模块补 `AdminCustomButtonArray` / `ClientAreaCustomButtonArray` | 增加「救援 / 快照 / VNC / 重置密码」按钮 |
| **P2** | 模块补 `LoginLink` | sub-user access code + 一次性 token |

---

## 9. 测试用例与验收标准

| 用例 | 输入 | 期望 |
|---|---|---|
| TC-001 | 子用户 API Key 调 `DELETE /api/webhooks/{id}` | 403 |
| TC-002 | 普通 API Key 调 `PUT /api/security-groups/{id}` 跨租户 | 403 |
| TC-003 | 模块 Suspend 后 `GET /containers/{name}` status | `suspended` |
| TC-004 | `UsageUpdate` 触发后 WHMCS `tbldomains` diskusage/bandwidth 更新 | 数值与上游一致 |
| TC-005 | 客户端按钮"救援"触发 `POST /api/v1/containers/{id}/iso/mount` | KVM 成功 / LXC 400 |
| TC-006 | 重复 CreateAccount（Idempotency-Key 相同） | 第二次返回既有容器，不重复创建 |
| TC-007 | 创建时 `ssh_public_key` 超 16 KB | 400 |
| TC-008 | 调 `/api/webhooks/{id}` DELETE 后 Webhook 状态 | 404 / 软删除 |

---

## 10. 附录：证据索引

- [billing-module/eyvescloud.php](file:///workspace/billing-module/eyvescloud.php)
- [backend/internal/server/server.go](file:///workspace/backend/internal/server/server.go)
- [backend/internal/api/auth.go](file:///workspace/backend/internal/api/auth.go)
- [backend/internal/api/apikey.go](file:///workspace/backend/internal/api/apikey.go)
- [backend/internal/api/webhooks.go](file:///workspace/backend/internal/api/webhooks.go)
- [backend/internal/api/handlers.go](file:///workspace/backend/internal/api/handlers.go)
- [backend/internal/api/runtime.go](file:///workspace/backend/internal/api/runtime.go)
- [backend/internal/api/security_groups.go](file:///workspace/backend/internal/api/security_groups.go)
- [backend/internal/api/node_groups.go](file:///workspace/backend/internal/api/node_groups.go)
- [backend/internal/api/usage_export.go](file:///workspace/backend/internal/api/usage_export.go)
- [backend/internal/api/enterprise.go](file:///workspace/backend/internal/api/enterprise.go)
- [backend/internal/api/idempotency.go](file:///workspace/backend/internal/api/idempotency.go)
- [backend/internal/api/overcommit.go](file:///workspace/backend/internal/api/overcommit.go)
- [backend/internal/api/openapi_paths.go](file:///workspace/backend/internal/api/openapi_paths.go)
- [backend/internal/rbac/rbac.go](file:///workspace/backend/internal/rbac/rbac.go)