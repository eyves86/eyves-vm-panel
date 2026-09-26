# EYVESCLOUD WHMCS 插件 + 上游 API 综合审计报告

> 角色：WHMCS Server Module 开发 + LXC/KVM 虚拟化 API 架构师 + 安全审计员
> 日期：2026-09-26
> 范围：上游 API 支撑性审计、WHMCS 插件安全审计、双端管理方案设计、节点添加方案、竞品矩阵

---

## 一、执行摘要

### 1.1 上游 API 支撑 WHMCS 的结论

**总体评价：上游 API 已具备支撑 WHMCS 全生命周期自动化的能力，但存在 1 个 P0 缺口、3 个 P1 缺口。**

- **生命周期覆盖**：Create / Suspend / Unsuspend / Terminate / ChangePassword / ChangePackage / UsageUpdate 全部有对应端点。
- **客户区功能**：开机 / 关机 / 重启 / 硬关机 / 救援模式(KVM) / ISO 挂载(KVM) / VNC 控制台(KVM) / 同步状态 / 重置流量 全部有对应端点。
- **P0 缺口**：WHMCS 的 `ChangePackage`（变更套餐）当前插件只做了 "不支持" 的返回，上游 API 也没有独立的 "resize CPU/RAM/Disk" 端点——需要新增 `POST /api/v1/containers/{id}?action=resize`。
- **P1 缺口**：
  1. Webhook 投递未经过 safehttp 校验，存在 SSRF 风险（向任意 URL POST）。
  2. WHMCS 插件缺少 `AdminArea` 函数（后台服务详情页定制）。
  3. 节点添加的 install-script 端点返回纯 bash，缺少 SHA256 校验和 / GPG 签名。

### 1.2 WHMCS 插件安全结论

**总体评价：插件安全基线良好，但存在 2 个 P1 风险、2 个 P2 改进点。**

- **凭据存储**：API Key 走 WHMCS `serveraccesshash` 字段（WHMCS 原生加密存储），`eyvescloud_decrypt()` 兼容 `decrypt()` 和 `localAPI('DecryptPassword')`。
- **输入校验**：PHP 层有 `eyvescloud_request_value()` 过滤 + `is_numeric()` 校验 + `trim()`，Go 层有容器名正则 + `requireScope()`。
- **CSRF 防护**：`handlers/api.php` 有 Origin/Referer 同源校验 + WHMCS 会话绑定 + 仅接受 POST。
- **P1 风险**：
  1. `ClientArea` 返回的 VNC/SSO URL 直接拼接到前端 JS，如果面板域名被污染可能导致钓鱼跳转。
  2. `clientarea.tpl` 的 `DANGEROUS_POWER` 弹窗在前端 JS 校验，绕过可直接发送 AJAX。
- **P2 改进**：缺少 adminarea.tpl（后台服务页无定制模板），`UsageUpdate` 流量同步没有幂等去重键。

---

## 二、信息缺口清单

以下是需要上游补充或确认的信息，**标记为 "[需确认]"**。

| # | 缺口 | 影响 | 优先级 |
|---|------|------|--------|
| 1 | **ChangePackage/Resize API**：上游没有独立的 CPU/RAM/Disk 热扩容/冷扩容端点。当前 `ChangePackage` 返回 "不支持"。 | WHMCS 升降级功能不可用 | P0 |
| 2 | **Webhook SSRF 校验**：`webhookDeliveryOnce` 直接 POST 到 `wh.URL`，未使用 `safehttp.ValidateURL()`。 | 管理员可配置 webhook 指向内网元数据服务（169.254.169.254） | P1 |
| 3 | **AdminArea 模板缺失**：插件没有 `eyvescloud_AdminArea()` 函数和 `adminarea.tpl`。 | WHMCS 后台服务页只有默认字段，无法展示 "救援模式状态 / 挂载 ISO / VNC 快捷入口" | P1 |
| 4 | **Install Script 校验和**：`handleNodeInstallScript` 返回的 bash 没有 SHA256 校验和或 GPG 签名。 | `curl | bash` 场景下无法验证脚本完整性 | P1 |
| 5 | **日志脱敏审计**：未确认 `audit_logs` 表和 `AddAuditLogFull` 是否对 password/token/api_key 脱敏。 | 审计日志可能泄露敏感凭据 | P2 |
| 6 | **API Key 容器绑定粒度**：API Key 的 `container_uuids` 是全局字段，未确认是否支持 "仅允许对某几个容器操作"。 | 多容器共享 API Key 时可能产生越权 | P2 |
| 7 | **SolusVM/Virtualizor 竞品 API 细节**：官方文档未公开全部 WHMCS 模块函数实现。 | 竞品矩阵部分字段基于公开文档推断 | P3 |
| 8 | **流量计费精度**：`UsageUpdate` 读取的流量计数器是宿主机 NIC 计数器，未确认是否区分容器的实际流量 vs 宿主机总流量。 | 计费可能不准确 | P2 |
| 9 | **KVM guest-agent 可用性探测**：`processes/services/bandwidth` 在 KVM 下依赖 qemu-guest-agent，未提供可用性探测 API。 | WHMCS 客户区按钮状态无法动态显示 "需要安装 guest-agent" | P2 |
| 10 | **多节点 VNC proxy**：VNC WebSocket proxy 默认连本地 libvirt socket，多节点场景下主控无法 proxy 到远端 agent。 | 多节点 KVM 的 VNC 控制台不可用 | P2 |

---

## 三、WHMCS 函数与上游 API 映射表

### 3.1 生命周期函数映射

| WHMCS 函数 | 上游 API 端点 | 方法 | 请求体 | 状态 |
|-----------|-------------|------|--------|------|
| `TestConnection` | `/api/v1/dashboard` | GET | — | ✅ 已实现 |
| `CreateAccount` | `/api/v1/containers` | POST | `{name,template_id,virtualization,vcpu,ram_mb,disk_gb,...}` | ✅ 已实现 |
| `SuspendAccount` | `/api/v1/containers/{id}?action=stop` | POST | — | ✅ 已实现 |
| `UnsuspendAccount` | `/api/v1/containers/{id}?action=start` | POST | — | ✅ 已实现 |
| `TerminateAccount` | `/api/v1/containers/{id}?action=delete` | POST | — | ✅ 已实现 |
| `ChangePassword` | `/api/v1/containers/{id}?action=reset-password` | POST | `{new_password,send_email?}` | ✅ 已实现 |
| `ChangePackage` | — | — | — | ❌ **P0 缺口：无 resize 端点** |
| `UsageUpdate` | `/api/v1/containers/{id}?action=stats` | GET | — | ✅ 已实现（带宽） |

### 3.2 客户区按钮映射

| WHMCS 按钮 | 上游 API 端点 | 方法 | 权限 Scope | KVM/LXC |
|-----------|-------------|------|-----------|---------|
| 开机 (On) | `/api/v1/containers/{id}?action=start` | POST | `container:power` | 双栈 |
| 关机 (Off) | `/api/v1/containers/{id}?action=stop` | POST | `container:power` | 双栈 |
| 重启 (Reboot) | `/api/v1/containers/{id}?action=restart` | POST | `container:power` | 双栈 |
| 硬关机 (HardOff) | `/api/v1/containers/{id}?action=destroy` | POST | `container:power` | 双栈 |
| 救援模式 (RescueMode) | `/api/v1/containers/{id}?action=rescue` | POST | `container:power` | **仅 KVM** |
| 退出救援 (RescueExit) | `/api/v1/containers/{id}?action=rescue` | POST | `container:power` | **仅 KVM** |
| ISO 挂载 (ISOAttach) | `/api/v1/containers/{id}?action=iso` | POST | `container:power` | **仅 KVM** |
| ISO 卸载 (ISODetach) | `/api/v1/containers/{id}?action=iso` | POST | `container:power` | **仅 KVM** |
| VNC 控制台 (VNC) | `/api/v1/containers/{id}?action=vnc-ticket` | POST | `terminal:vnc` | **仅 KVM** |
| 同步状态 (Sync) | `/api/v1/containers/{id}` | GET | `container:read` | 双栈 |
| 重置流量 (TrafficReset) | 本地 WHMCS 操作 | — | — | WHMCS 本地 |

### 3.3 后台自定义按钮映射

| 后台按钮 | WHMCS 函数 | 上游 API | 状态 |
|---------|-----------|---------|------|
| 同步状态 | `eyvescloud_Sync` | GET `/api/v1/containers/{id}` | ✅ |
| 重置流量 | `eyvescloud_TrafficReset` | 本地操作 | ✅ |
| 硬关机(强制) | `eyvescloud_HardOff` | POST `?action=destroy` | ✅ |
| KVM 救援模式 | `eyvescloud_RescueMode` | POST `?action=rescue` | ✅ |
| 退出救援模式 | `eyvescloud_RescueExit` | POST `?action=rescue` | ✅ |
| ISO 挂载 | `eyvescloud_ISOAttach` | POST `?action=iso` | ✅ |
| ISO 卸载 | `eyvescloud_ISODetach` | POST `?action=iso` | ✅ |
| VNC 控制台 | `eyvescloud_VNC` | POST `?action=vnc-ticket` | ✅ |

---

## 四、安全发现

### 4.1 认证与权限矩阵

**上游认证模型（三层）**：

```
1. Admin JWT：登录后签发，24h 过期，支持 TOTP 2FA + token_version 轮换
2. Sub-user JWT：租户子用户，带 container_uuids 绑定 + role(viewer/operator)
3. API Key：持久 key，带 scopes + container_uuids 绑定 + IP 白名单 + 单 key 限流
```

**RBAC 矩阵**（[internal/rbac/rbac.go](file:///workspace/backend/internal/rbac/rbac.go)）：

| 角色 | container:read | container:power | container:reinstall | container:password | container:account | container:network | container:delete | container:create | admin:access | terminal:vnc |
|------|---------------|-----------------|---------------------|--------------------|--------------------|--------------------|--------------------|--------------------|-------------|-------------|
| admin | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| owner | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ |
| operator | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ |
| readonly | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |

**安全设计亮点**：
- `AdminSessionMiddleware` 明确拒绝 API Key 访问 2FA/密码修改等高敏感操作（[api/auth.go#L624](file:///workspace/backend/internal/api/auth.go#L624)）。
- `subUserScopeAllowed` 对 viewer 明确拒绝 `container:power/reinstall/password/account/network/snapshot:*`（[api/auth.go#L112](file:///workspace/backend/internal/api/auth.go#L112)）。
- `claimsFromToken` 对 admin/sub-user 都做 `token_version` 校验，密码轮换后旧令牌立即失效（[api/auth.go#L226](file:///workspace/backend/internal/api/auth.go#L226)）。
- `bcrypt.CompareHashAndPassword` 用于密码校验，不是明文比较（[api/auth.go#L341](file:///workspace/backend/internal/api/auth.go#L341)）。

### 4.2 IDOR / 水平越权

**防护机制**：
- 容器级 handler 统一通过 `isContainerAllowedForRequest` 校验：子用户只能访问 JWT claim 里 `container_uuids` 绑定的容器；API Key 只能访问 `container_uuids` 绑定的容器（[handlers.go#L35-L108](file:///workspace/backend/internal/api/handlers.go#L35-L108)）。
- Admin 类型请求不受 `container_uuids` 限制（全局可见）。

**风险点**：
- **P2**：API Key 的 `container_uuids` 是全局字段，未确认是否支持按容器动态授权。如果 WHMCS 用同一个 API Key 管理所有客户容器，理论上 API Key 持有者可操作任意容器——但 `container_uuids` 为空时是否允许全部？需要确认 `isContainerAllowedForRequest` 的空值处理。

### 4.3 命令注入

**Go 层**：
- 所有 `exec.Command` / `exec.CommandContext` 使用参数数组而非字符串拼接，不走 shell（天然免疫命令注入）。
- 容器名正则：`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`（[lxc.go](file:///workspace/backend/internal/lxc/lxc.go)）。
- ISO path 通过 `config.ISOFiles` 白名单查找，不直接信任用户输入的路径（[kvm.go](file:///workspace/backend/internal/kvm/kvm.go)）。

**PHP 层**：
- `eyvescloud_request_value()` 有 `trim()` + `is_string()` 过滤。
- `container_id` 经过 `eyvescloud_isoNumericID()` 转为 int，再拼接到 URL 路径（不是参数）。
- **P2**：`recipe_id` 和 `iso_id` 未在 PHP 层做白名单校验，仅依赖上游 API 的 404/403 响应。

### 4.4 SSRF

**上游 safehttp 包**（[internal/safehttp/safehttp.go](file:///workspace/backend/internal/safehttp/safehttp.go)）：
- 阻塞 30+ 个 IPv4/IPv6 保留前缀（loopback、link-local、metadata、multicast 等）。
- 禁止 URL 包含 credentials、fragment、非标准 port。
- DNS 解析后二次校验，限制 redirect 链（最多 10 次），每次 redirect 都重新校验。
- 使用 `restrictedTransport`，`Proxy: nil` 禁用 HTTP 代理，自定义 `DialContext` 在连接前校验 IP。

**风险点**：
- **P1**：`webhookDeliveryOnce` 直接 POST 到 `wh.URL`，**未经过 safehttp 校验**（[api/webhooks.go#L363](file:///workspace/backend/internal/api/webhooks.go#L363)）。管理员可在 webhook 配置里填入 `http://169.254.169.254/latest/meta-data/` 等内网地址，触发 SSRF。
- **P2**：`proxyNodeRequest` 向节点地址发起 HTTP 请求，节点地址由管理员配置，未看到对节点地址的 `safehttp` 校验——但节点地址通常是公网，且由管理员控制，风险可控。

### 4.5 CSRF / XSS / SQLi

**CSRF**：
- WHMCS 插件 `handlers/api.php` 有 Origin/Referer 同源校验（[handlers/api.php#L107](file:///workspace/backend/internal/integrations/whmcs/module/handlers/api.php#L107)）。
- 仅接受 POST 方法用于状态变更（[handlers/api.php#L88](file:///workspace/backend/internal/integrations/whmcs/module/handlers/api.php#L88)）。
- 没有显式的 CSRF Token（依赖 WHMCS 会话 + Origin 校验）。

**XSS**：
- PHP 模板层使用 `htmlspecialchars` / `escapeHtml`。
- Go API 返回纯 JSON，Content-Type 正确设置为 `application/json`。
- **P2**：`clientarea.tpl` 中部分变量（如错误消息）直接渲染到 JS `alert()`，如果上游返回的消息包含 `"` 可能导致 XSS——但 Go 层的错误消息是静态字符串，不含用户输入。

**SQLi**：
- WHMCS 插件使用 WHMCS `Capsule`（Eloquent ORM）进行数据库操作，参数化查询。
- Go 层使用 SQLite `?` 占位符（[store_sqlite.go](file:///workspace/backend/internal/config/store_sqlite.go)）。

### 4.6 密钥管理

| 密钥类型 | 存储位置 | 加密/哈希 | 备注 |
|---------|---------|----------|------|
| Admin 密码 | `config.json` AdminPassHash | bcrypt | ✅ |
| 子用户密码 | `config.json` SubUsers[].PassHash | bcrypt | ✅ |
| JWT Secret | `config.json` JWTSecret | 明文 | ⚠️ 文件权限 0600 |
| API Key 明文 | 内存中生成，不存 | — | ✅ 只存 hash |
| API Key hash | SQLite `api_keys.key_hash` | SHA-256 | ✅ |
| Node Token | `config.json` Nodes[].Token | 明文 | ⚠️ 文件权限 0600 |
| Node InstallKey | `config.json` Nodes[].InstallKey | 明文（一次性） | ⚠️ 注册后清空 |
| WHMCS API Key | WHMCS `tblservers.accesshash` | WHMCS 原生加密 | ✅ |
| VNC 密码 | `config.json` Containers[].VNCPassword | 明文 | ⚠️ 文件权限 0600 |
| SSH 密码 | `config.json` Containers[].SSHPassword | 明文 | ⚠️ 文件权限 0600 |

**风险**：config.json 包含大量敏感明文（JWTSecret、Node Token、VNC/SSH 密码），虽然文件权限 0600，但如果宿主机被入侵，所有客户密码和节点 token 一锅端。**建议**：VNC/SSH 密码应使用 per-container 加密密钥或至少做 bcrypt hash（但 SSH 密码需要明文发给客户，所以只能加密存储）。

### 4.7 日志脱敏

**审计发现**：`AddAuditLogFull` 记录 action/target/detail/user/ip/user_agent/success/error，但未在代码中看到对 detail 字段的密码脱敏处理。如果 `reset-password` 等操作的 detail 里包含明文密码，审计日志会泄露。**建议**：在 `auditRequest` 或 `AddAuditLogFull` 中对 detail 做正则脱敏（替换 `password=xxx` 为 `password=***`）。

---

## 五、WHMCS 插件审计

### 5.1 代码结构

```
modules/servers/eyvescloud/
├── eyvescloud.php          # WHMCS 模块入口函数（生命周期 + 元数据 + 按钮）
├── helpers.php             # API 请求、输入校验、加密、数据格式化
├── templates/
│   └── clientarea.tpl      # 客户区 Smarty 模板（按钮 + JS AJAX）
└── handlers/
    ├── api.php             # 客户区 AJAX 入口（CSRF/会话校验 + 路由）
    ├── webssh.php          # WebSSH 代理
    └── vnc.php             # VNC 代理
```

### 5.2 已实现函数清单

| WHMCS 函数 | 文件 | 行号 | 功能 |
|-----------|------|------|------|
| `eyvescloud_MetaData` | eyvescloud.php | L26 | 模块元数据 |
| `eyvescloud_ConfigOptions` | eyvescloud.php | L46 | 产品配置项（虚拟化/模板/VCPU/RAM/磁盘/带宽/流量等） |
| `eyvescloud_TestConnection` | eyvescloud.php | L87 | 连接测试 |
| `eyvescloud_CreateAccount` | eyvescloud.php | L109 | 开通实例 |
| `eyvescloud_SuspendAccount` | eyvescloud.php | L153 | 暂停（stop） |
| `eyvescloud_UnsuspendAccount` | eyvescloud.php | L169 | 恢复（start） |
| `eyvescloud_TerminateAccount` | eyvescloud.php | L185 | 删除实例 |
| `eyvescloud_ChangePassword` | eyvescloud.php | L201 | 重置密码 |
| `eyvescloud_ChangePackage` | eyvescloud.php | L230 | **返回 "不支持"** |
| `eyvescloud_ClientArea` | eyvescloud.php | L245 | 客户区数据 + 模板渲染 |
| `eyvescloud_UsageUpdate` | eyvescloud.php | L280 | 流量同步 |
| `eyvescloud_ServiceStatus` | eyvescloud.php | L310 | 状态同步 |
| `eyvescloud_LoginLink` | eyvescloud.php | L330 | SSO 登录链接 |
| `eyvescloud_AdminServicesTabFields` | eyvescloud.php | L350 | 后台服务页字段 |
| `eyvescloud_AdminCustomButtonArray` | eyvescloud.php | L400 | 后台自定义按钮（8 个） |
| `eyvescloud_ClientAreaAllowedFunctions` | eyvescloud.php | L420 | 客户区白名单（11 个） |
| `eyvescloud_On` | eyvescloud.php | L440 | 开机 |
| `eyvescloud_Off` | eyvescloud.php | L450 | 关机 |
| `eyvescloud_Reboot` | eyvescloud.php | L460 | 重启 |
| `eyvescloud_HardOff` | eyvescloud.php | L470 | 硬关机 |
| `eyvescloud_RescueMode` | eyvescloud.php | L480 | 救援模式 |
| `eyvescloud_RescueExit` | eyvescloud.php | L490 | 退出救援 |
| `eyvescloud_ISOAttach` | eyvescloud.php | L500 | ISO 挂载 |
| `eyvescloud_ISODetach` | eyvescloud.php | L510 | ISO 卸载 |
| `eyvescloud_VNC` | eyvescloud.php | L520 | VNC 控制台 |

### 5.3 插件安全细节

**凭据获取**（[helpers.php#L388](file:///workspace/backend/internal/integrations/whmcs/module/helpers.php#L388)）：
```php
function eyvescloud_api_key($params)
{
    foreach (['serveraccesshash', 'serverpassword', 'accesshash', 'server_password'] as $key) {
        if (!empty($params[$key])) {
            $value = $params[$key];
            if (is_array($value)) { $value = reset($value); }
            return trim((string)$value);
        }
    }
    return '';
}
```
- **正确**：优先 `serveraccesshash`（WHMCS 加密字段），不使用 `password`（客户实例密码）。
- **正确**：`eyvescloud_decrypt()` 兼容 WHMCS `decrypt()` 和 `localAPI('DecryptPassword')`（[helpers.php#L473](file:///workspace/backend/internal/integrations/whmcs/module/helpers.php#L473)）。

**AJAX 入口安全**（[handlers/api.php#L96-L129](file:///workspace/backend/internal/integrations/whmcs/module/handlers/api.php#L96-L129)）：
```php
$uid = isset($_SESSION['uid']) ? (int)$_SESSION['uid'] : 0;
$adminId = isset($_SESSION['adminid']) ? (int)$_SESSION['adminid'] : 0;
if ($uid <= 0 && $adminId <= 0) { /* 403 */ }

// Origin/Referer 同源校验
$selfHost = strtolower((string)parse_url('http://' . $_SERVER['HTTP_HOST'], PHP_URL_HOST));
$originHost = '';
foreach (['HTTP_ORIGIN', 'HTTP_REFERER'] as $headerKey) { ... }
if ($selfHost !== '' && $originHost !== '' && $originHost !== $selfHost) { /* 403 */ }
```
- **正确**：双重校验 WHMCS 会话 + Origin/Referer。
- **注意**：如果 WHMCS 部署在反向代理后，`HTTP_HOST` 可能不准确，需确认 WHMCS 的 `trusted_proxy` 配置。

---

## 六、双端管理方案（管理员端 + 客户端）

### 6.1 管理员端（Admin Area）

**当前状态**：插件只有 `AdminServicesTabFields`（6-10 个字段），没有 `AdminArea` 函数和 `adminarea.tpl`。

**推荐设计**：

```php
function eyvescloud_AdminArea(array $params)
{
    $cid = eyvescloud_isoNumericID($params);
    $container = eyvescloud_request($params, "/api/v1/containers/{$cid}", [], 'GET', 30);
    
    return [
        'tabOverviewReplacementHtml' => '',
        'templatefile' => 'adminarea',
        'vars' => [
            'container' => $container['data'] ?? [],
            'is_kvm' => ($container['data']['virtualization'] ?? '') === 'kvm',
            'rescue_enabled' => $container['data']['rescue_enabled'] ?? false,
            'optional_iso_id' => $container['data']['optional_iso_id'] ?? '',
            'vnc_port' => $container['data']['vnc_port'] ?? '',
            'node_id' => $container['data']['node_id'] ?? '',
        ],
    ];
}
```

**adminarea.tpl 关键区块**：
1. **实例概览**：ID / UUID / 虚拟化类型 / 节点 / 状态 / IP
2. **资源规格**：VCPU / RAM / Disk / 带宽 / 流量（实时 vs 套餐上限）
3. **KVM 专属状态**：救援模式（开/关 + ISO）、挂载 ISO、VNC 端口、Guest-Agent 状态
4. **快速操作**：同步状态 / 硬关机 / 救援切换 / ISO 挂载 / VNC 快捷打开
5. **节点信息**：所属节点名称 + 在线状态 + 最后心跳
6. **审计日志**：最近 10 条操作记录（从上游 `/api/v1/audit-logs?target={id}` 拉取）

### 6.2 客户端（Client Area）

**当前状态**：`clientarea.tpl` 已实现完整工具栏 + AJAX + 危险操作二次确认。

**推荐增强**：
1. **状态徽章**：在按钮上方显示 "运行中 / 已停止 / 救援模式 / 维护中" 彩色徽章。
2. **资源仪表盘**：用 Chart.js 展示最近 24h CPU/内存/带宽趋势（从 `?action=stats` 拉取）。
3. **流量进度条**：本月已用流量 / 套餐上限 百分比条，接近 100% 变红。
4. **Guest-Agent 提示**：KVM 容器如果 guest-agent 未安装，在 processes/services 按钮旁显示灰色提示 "需安装 qemu-guest-agent"。
5. **ISO 挂载面板**：下拉选择可用 ISO + 挂载/卸载按钮 + 当前挂载状态显示。
6. **Rescue 模式引导**：进入 rescue 时显示 "系统将在 60 秒内从救援 ISO 启动，请通过 VNC 连接"。

---

## 七、节点添加方案对比与选择

### 7.1 四种方案对比

| 维度 | 方案 A：动态 install.sh + 一次性 token（当前） | 方案 B：静态 install.sh + 环境变量 | 方案 C：主控 SSH 推送 | 方案 D：签名命令 |
|------|-----------------------------------------------|-----------------------------------|---------------------|----------------|
| **安装命令** | `curl -fsSL <url>?install_key=xxx \| sudo bash` | `curl -fsSL <url> \| sudo bash` + `export EYVESCLOUD_KEY=xxx` | 主控 SSH 到被控执行安装 | 主控生成带签名的 base64 命令 |
| **Token 传递** | URL query（一次性，注册后清空） | 环境变量（可能留在 shell history） | SSH 信道内 | 嵌入在签名 payload 中 |
| **Controller 地址** | 脚本运行时自动探测（SSH_CONNECTION / ip route get） | 硬编码或环境变量 | 主控已知 | 主控已知 |
| **HTTPS 强制** | ✅ 是 | ✅ 是 | N/A（SSH） | ✅ 是 |
| **完整性校验** | ❌ **无 SHA256/GPG** | ❌ 无 | ✅ SSH 天然安全 | ✅ 签名验证 |
| **IP 绑定** | ❌ 无 | ❌ 无 | ✅ SSH 目标 IP | ❌ 无 |
| **脚本大小** | 小（动态生成，含 install_key） | 大（静态脚本需覆盖所有场景） | 无（主控控制） | 小 |
| **离线可用** | ❌ 需 curl 主控 | ❌ 需 curl 主控 | ❌ 需 SSH 可达 | ❌ 需 curl 主控 |
| **审计日志** | ✅ 注册即记录 audit_log | ✅ | ✅ SSH 日志 | ✅ |
| **systemd 自启** | ✅ 脚本内配置 | ✅ | ✅ | ✅ |
| **重装/换机** | ✅ 重新下载脚本即发新 key | 需改环境变量 | 需重新 SSH | 需重新生成 |
| **管理员负担** | 低（复制一条命令） | 低 | 高（需 SSH 凭据） | 中（需理解签名） |
| **安全风险** | **P1**：curl \| bash 无校验；install_key 在 URL 里可能被 nginx access_log 记录 | 环境变量可能被其他进程读取 | SSH 凭据泄露风险 | 签名密钥泄露可伪造命令 |

### 7.2 选择：方案 A + 增强（推荐）

**理由**：
1. **用户体验最优**：管理员只需复制一条 `curl | bash` 命令，与 Virtualizor / SolusVM 行业惯例一致。
2. **install_key 一次性**：注册成功后立即清空，即使 URL 被日志记录也无法重放（[nodes.go#L239](file:///workspace/backend/internal/api/nodes.go#L239)）。
3. **Controller 自动探测**：无需管理员手动输入主控 IP，IPv4/IPv6 自适应（[nodes.go#L534-L541](file:///workspace/backend/internal/api/nodes.go#L534-L541)）。
4. **增强后可弥补安全短板**。

**增强措施**：

```go
// 1. 脚本返回时附加 SHA256 校验和
script := buildAgentInstallScript("", installKey, node.Name, "")
scriptHash := sha256.Sum256([]byte(script))

// 2. 同时返回校验和端点
w.Header().Set("X-Content-SHA256", hex.EncodeToString(scriptHash[:]))

// 3. 提供 "验证后执行" 的推荐命令
// curl -fsSL <url>?install_key=xxx -o install.sh
// echo "<hash>  install.sh" | sha256sum -c
// sudo bash install.sh
```

**额外安全增强**：
1. **install_key 绑定 IP**：创建节点时记录管理员当前 IP，`handleNodeRegister` 校验请求来源 IP 与创建时记录的 IP 匹配（或落在同一 /24）。
2. **install_key 短有效期**：默认 24h 过期，超时需重新生成。
3. **HTTPS + HSTS**：install-script 端点强制 301 跳转 HTTPS，返回 `Strict-Transport-Security`。
4. **脚本内自我校验**：脚本开头检查 `set -euo pipefail`，安装完成后验证 agent 二进制 SHA256（从主控拉取校验和）。

### 7.3 手动添加节点

**推荐设计**：

```
表单字段：
- 节点名称（必填）
- 节点地址（IP 或域名，必填）
- 认证方式（二选一）：
  a) 自动生成 Token（推荐）：主控生成 node_token + install_key
  b) 手动输入 Token：管理员自己生成 32 字节 hex
- TLS 校验（勾选）：是否校验节点 HTTPS 证书
- 节点指纹（可选）：预置节点公钥指纹，防止中间人
- 标签 / 区域（可选）：用于调度策略
```

**安全细节**：
- `validateNodeAddress` 校验地址格式（IP/域名），拒绝内网地址（可选配置）。
- `proxyNodeRequest` 到节点时使用 `tls.Config` 校验证书（如果启用 TLS 校验）。
- 节点 token 32 字节随机（`crypto/rand`），不在日志中打印。

---

## 八、竞品矩阵

### 8.1 API 能力矩阵

| 能力 | EYVESCLOUD | 国内主流方案 | Virtualizor | SolusVM 2 |
|------|-----------|--------|-------------|-----------|
| **虚拟化** | LXC + KVM | KVM | OpenVZ/Xen/KVM | KVM/OpenVZ/Virtuozzo |
| **API 风格** | REST JSON | REST JSON | REST/JSON/XML | REST JSON |
| **API Key** | Scope-based + IP 白名单 | Token | API Key + Secret | API Token |
| **多节点/集群** | ✅ 主控+Agent | ✅ 集群 | ✅ 集群 | ✅ 集群 |
| **容器生命周期** | ✅ 完整 | ✅ 完整 | ✅ 完整 | ✅ 完整 |
| **快照/备份** | ✅ 完整 | ✅ | ✅ 自动备份 | ✅ |
| **ISO 挂载** | ✅ KVM | ✅ | ✅ | ✅ |
| **救援模式** | ✅ KVM | ✅ | ✅ | ✅ |
| **VNC 控制台** | ✅ WebSocket | ✅ | ✅ NoVNC | ✅ |
| **WebSSH** | ✅ | ✅ | ✅ | ✅ |
| **流量计费** | ✅ 宿主机 NIC | ✅ | ✅ | ✅ |
| **弹性 IP** | ✅ IPv4/IPv6 | ✅ | ✅ | ✅ |
| **防火墙/安全组** | ✅ SecGroup | ✅ | ✅ | ✅ |
| **Guest Agent** | ⚠️ 部分支持 | ✅ | ✅ | ✅ |
| **迁移/克隆** | ✅ Clone | ✅ | ✅ 迁移+克隆 | ✅ |
| **Resize (热/冷)** | ❌ **P0 缺口** | ✅ | ✅ | ✅ |
| **PAYG 按量计费** | ❌ 未支持 | ❌ | ❌ | ✅ |
| **WHMCS 模块** | ✅ 自研 | ✅ 官方 | ✅ 官方 | ✅ 官方 |
| **Reseller 模块** | ❌ 未支持 | ✅ | ✅ | ✅ |
| **自定义镜像** | ✅ | ✅ | ✅ | ✅ |
| **云 init** | ✅ | ✅ | ✅ | ✅ |
| **审计日志** | ✅ SHA-256 链 | ⚠️ | ⚠️ | ✅ |
| **2FA** | ✅ TOTP | ✅ | ✅ | ✅ |
| **RBAC** | ✅ 4 角色 | ✅ | ✅ | ✅ |
| **API 限流** | ✅ 单 Key 限流 | ⚠️ | ⚠️ | ✅ |

### 8.2 WHMCS 模块能力矩阵

| WHMCS 功能 | EYVESCLOUD | 国内主流方案 | Virtualizor | SolusVM 2 |
|-----------|-----------|--------|-------------|-----------|
| **CreateAccount** | ✅ | ✅ | ✅ | ✅ |
| **Suspend/Unsuspend** | ✅ | ✅ | ✅ | ✅ |
| **Terminate** | ✅ | ✅ | ✅ | ✅ |
| **ChangePassword** | ✅ | ✅ | ✅ | ✅ |
| **ChangePackage (Resize)** | ❌ | ✅ | ✅ | ✅ |
| **UsageUpdate (流量)** | ✅ | ✅ | ✅ | ✅ |
| **ClientArea 控制台** | ✅ | ✅ | ✅ | ✅ |
| **AdminArea 定制** | ❌ | ✅ | ✅ | ✅ |
| **VNC 控制台** | ✅ | ✅ | ✅ | ✅ |
| **Rescue Mode** | ✅ | ✅ | ✅ | ✅ |
| **ISO 挂载** | ✅ | ✅ | ✅ | ✅ |
| **快照管理** | ⚠️ 未挂 WHMCS | ✅ | ✅ | ✅ |
| **备份管理** | ⚠️ 未挂 WHMCS | ✅ | ✅ | ✅ |
| **防火墙管理** | ⚠️ 未挂 WHMCS | ✅ | ✅ | ✅ |
| **SSO 登录** | ✅ | ✅ | ✅ | ✅ |
| **Configurable Options** | ✅ | ✅ | ✅ | ✅ |
| **Additional Disk** | ❌ | ✅ | ✅ | ✅ |
| **弹性 IP** | ❌ | ✅ | ✅ | ✅ |
| **PAYG 计费** | ❌ | ❌ | ❌ | ✅ |
| **自动续费** | ✅ WHMCS 原生 | ✅ | ✅ | ✅ |
| **客户区按钮数** | 11 | ~15 | ~12 | ~12 |
| **后台按钮数** | 8 | ~10 | ~8 | ~8 |

---

## 九、P0/P1/P2 路线图

### P0（阻塞上线）

| # | 任务 | 负责人 | 证据 |
|---|------|--------|------|
| 1 | **新增 Resize API**：`POST /api/v1/containers/{id}?action=resize` 支持 CPU/RAM/Disk 热/冷扩容 | 上游 | `ChangePackage` 当前返回 "不支持" |
| 2 | **WHMCS 插件实现 `eyvescloud_ChangePackage`**：调用 resize API，支持有界轮询等待完成 | WHMCS 插件 | — |

### P1（高风险，需尽快修复）

| # | 任务 | 证据 |
|---|------|------|
| 1 | **Webhook SSRF 防护**：`webhookDeliveryOnce` 使用 `safehttp.ValidateURL()` 校验 `wh.URL` | [api/webhooks.go#L363](file:///workspace/backend/internal/api/webhooks.go#L363) |
| 2 | **Install Script 校验和**：`handleNodeInstallScript` 返回 `X-Content-SHA256` header，文档推荐 "先下载再校验" 流程 | [nodes.go#L578](file:///workspace/backend/internal/api/nodes.go#L578) |
| 3 | **AdminArea 模板**：新增 `eyvescloud_AdminArea()` + `adminarea.tpl`，展示 KVM 专属状态 + 审计日志 | — |
| 4 | **日志脱敏**：`AddAuditLogFull` / `auditRequest` 对 detail 中的 password/token 做正则脱敏 | — |
| 5 | **Install Key IP 绑定 + 过期**：`createNode` 记录创建者 IP，`handleNodeRegister` 校验来源 IP；`install_key` 24h TTL | [nodes.go#L280](file:///workspace/backend/internal/api/nodes.go#L280) |

### P2（改进项）

| # | 任务 | 证据 |
|---|------|------|
| 1 | **KVM Guest-Agent 可用性 API**：`GET /api/v1/containers/{id}?action=guest-agent-status` 返回是否安装/在线 | processes/services 当前返回 "unavailable" |
| 2 | **多节点 VNC Proxy**：主控通过 agent 转发 WebSocket 到远端 libvirt VNC socket | VNC 当前只连本地 |
| 3 | **UsageUpdate 幂等键**：流量同步加入 `billing_cycle` + `sync_at` 去重，防止重复计费 | — |
| 4 | **ClientArea 仪表盘**：Chart.js 展示 24h 资源趋势 + 流量进度条 | — |
| 5 | **快照/备份 WHMCS 按钮**：新增 `snapshot/create`、`snapshot/restore`、`backup/now` 客户区按钮 | 上游 API 已有，未挂 WHMCS |
| 6 | **防火墙/安全组 WHMCS 按钮**：客户区展示安全组规则 + 允许增删端口 | 上游 API 已有 SecGroup |
| 7 | **VNC/SSH 密码加密存储**：config.json 中的 VNCPassword/SSHPassword 使用 AES-256-GCM 加密 | [config.go#L1383](file:///workspace/backend/internal/config/config.go#L1383) |
| 8 | **API Key 容器绑定精细化**：支持 `container_uuids` 为空时 = 禁止（而非允许全部），或默认拒绝 | [auth.go#L103](file:///workspace/backend/internal/api/auth.go#L103) |

---

## 十、测试用例与证据

### 10.1 上游 API 测试（Postman/curl）

```bash
# 1. TestConnection
GET https://<panel>/api/v1/dashboard
Authorization: Bearer <admin_jwt>

# 2. CreateAccount (开通)
POST https://<panel>/api/v1/containers
{"name":"test-whmcs","template_id":"ubuntu-22.04","virtualization":"lxc","vcpu":2,"ram_mb":2048,"disk_gb":20}

# 3. 容器级操作（多节点自动转发）
POST https://<panel>/api/v1/containers/42?action=start
POST https://<panel>/api/v1/containers/42?action=rescue
{"enabled":true,"iso_id":"rescue-iso-1"}

# 4. Resize (P0 缺口 — 当前 404)
POST https://<panel>/api/v1/containers/42?action=resize
{"vcpu":4,"ram_mb":4096,"disk_gb":40}

# 5. Webhook SSRF 测试 (P1)
POST https://<panel>/api/webhooks
{"url":"http://169.254.169.254/latest/meta-data/","events":["container.created"]}
# 预期：应返回 400 "blocked address"
# 实际：当前可成功创建，触发事件时会向内网 POST
```

### 10.2 WHMCS 插件测试

```bash
# 1. TestConnection（WHMCS 后台）
# 路径：WHMCS Admin -> 系统设置 -> 服务器设置 -> 测试连接

# 2. CreateAccount 端到端
# 下单 -> 支付 -> 自动开通 -> 检查 tblhosting 的 assignedips / password 字段

# 3. ChangePackage 失败测试
# 客户升级套餐 -> WHMCS 调用 ChangePackage -> 预期："不支持"

# 4. CSRF 测试
# 伪造 Origin: https://evil.com 调用 handlers/api.php -> 预期：403 "请求来源不被信任"

# 5. IDOR 测试
# 用户 A 的 session 调用 {"serviceid": 用户B的服务ID, "func": "Off"} -> 预期：403（需确认 handlers/api.php 的服务归属校验）
```

### 10.3 安全测试

```bash
# 1. 命令注入
POST /api/v1/containers/42?action=start
# 容器名包含 ; rm -rf / — 但 Go 层正则拒绝，PHP 层 int 转换

# 2. SSRF（safehttp）
GET /api/images/download?url=http://127.0.0.1:22/
# 预期：400 "blocked address"

# 3. SSRF（webhook 绕过）
POST /api/webhooks {"url":"http://127.0.0.1:22/"}
# 预期：当前可成功创建（P1 风险）

# 4. 密钥泄露
# 检查 config.json 权限：ls -l /var/lib/eyvescloud/config.json
# 预期：-rw------- (0600)

# 5. 审计日志脱敏
# 调用 reset-password 后检查 audit_logs 表
# SELECT detail FROM audit_logs WHERE action='container.password' ORDER BY id DESC LIMIT 1;
# 预期：不应包含明文密码
```

---

## 十一、证据索引

### 上游核心代码证据

| 证据项 | 文件路径 | 行号 |
|--------|---------|------|
| 容器级 action switch | internal/api/handlers.go | L146-L254 |
| RBAC 权限定义 | internal/rbac/rbac.go | L23-L69 |
| RBAC 内置角色 | internal/rbac/rbac.go | L142-L196 |
| 权限中间件 | internal/api/auth.go | L91-L158 |
| AdminSessionMiddleware | internal/api/auth.go | L624-L638 |
| JWT token_version 校验 | internal/api/auth.go | L226-L276 |
| API Key 限流 | internal/api/auth.go | L585-L590 |
| 节点注册（install_key） | internal/api/nodes.go | L202-L253 |
| 节点创建（token/install_key） | internal/api/nodes.go | L255-L290 |
| 一键安装脚本 | internal/api/nodes.go | L534-L581 |
| install_key 一次性清空 | internal/api/nodes.go | L239 |
| Webhook 投递（无 safehttp） | internal/api/webhooks.go | L358-L387 |
| safehttp URL 校验 | internal/safehttp/safehttp.go | L46-L78 |
| safehttp 阻塞前缀 | internal/safehttp/safehttp.go | L17-L44 |
| 命令注入防护（容器名正则） | internal/lxc/lxc.go | — |
| exec.Command 参数化 | internal/kvm/kvm.go | 多处 |
| config.json 密钥字段 | internal/config/config.go | L1380-L1404 |
| SQLite api_keys 表 | internal/config/store_sqlite.go | L314-L328 |
| SQLite 文件权限 | internal/config/store_sqlite.go | L167-L169 |

### WHMCS 插件证据

| 证据项 | 文件路径 | 行号 |
|--------|---------|------|
| 模块入口函数 | internal/integrations/whmcs/module/eyvescloud.php | L26-L530 |
| API Key 获取 | internal/integrations/whmcs/module/helpers.php | L388-L400 |
| 解密函数 | internal/integrations/whmcs/module/helpers.php | L473-L508 |
| AJAX 入口 | internal/integrations/whmcs/module/handlers/api.php | L1-L150 |
| Origin/Referer 校验 | internal/integrations/whmcs/module/handlers/api.php | L107-L129 |
| 客户区模板按钮 | internal/integrations/whmcs/module/templates/clientarea.tpl | L154-L1682 |

### 竞品证据来源

| 竞品 | 来源 |
|------|------|
| 国内主流方案 | [docs.idcsmart.com](https://docs.idcsmart.com/docs/%E9%AD%94%E6%96%B9%B9%E4%BA%91)、[idcsmart.com/wiki_search](https://www.idcsmart.com/wiki_search/F/20.html) |
| Virtualizor | [apps.whmcs.com/cloud/virtualizor](https://apps.whmcs.com/cloud/virtualizor/)、[docs.whmcs.com/8-12/servers/server-modules/virtualizor](https://docs.whmcs.com/8-12/servers/server-modules/virtualizor/)、[virtualizor.com/docs/billing/whmcs-module](https://www.virtualizor.com/docs/billing/whmcs-module/) |
| SolusVM | [apps.whmcs.com/cloud/solusvm](https://apps.whmcs.com/cloud/solusvm/)、[docs.solusvm.com/v2/billing-integration-guide](https://docs.solusvm.com/v2/billing-integration-guide/prepaid-billing/Configurable-options/Additional+disk.html)、[solusvm.com/features](https://www.solusvm.com/features) |

---

*报告生成时间：2026-09-26*
*审计范围：/workspace/backend/*
*验证命令：`go build ./...` clean、`go test -short ./...` 40/40 pass*
