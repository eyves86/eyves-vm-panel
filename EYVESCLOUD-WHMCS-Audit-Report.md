# EYVESCLOUD × WHMCS 全量审计报告 v2

- **审计日期**：2026-09-26
- **审计基线**：main @ `37c2be2`（v1.8.2 之后，含品牌清理提交）
- **审计对象**：上游 LXC/KVM 平台（`/workspace/backend`，Go）+ WHMCS Server Module（`backend/internal/integrations/whmcs/module/`，PHP）
- **上一轮报告**：v1（v1.8.2 发布前）。本版为**重新全量审计**，v1 的 P0 缺口（Resize/ChangePackage）已修复并验证
- **验证状态**：`go build` / `go vet` clean，40 packages test + race clean，`php -l` 全过

---

## 一、执行摘要

**总体结论**：上游 API 已能完整支撑 WHMCS Server Module 全生命周期（17 个模块函数中 14 个直接实现、3 个有官方推荐等价物），v1 报告的 P0 缺口（Resize/ChangePackage）已闭环。本轮发现 **1 个新的 High 级问题（跨节点 Suspended 绕过）**，以及 6 个 Medium 级问题，无 Critical。

| 维度 | 结论 |
|------|------|
| WHMCS 生命周期支撑 | ✅ 完整（Create/Suspend/Terminate/ChangePassword/ChangePackage/UsageUpdate 均已实现且有幂等保护） |
| 鉴权体系 | ✅ JWT(HS256) + API Key(argon2 哈希 + IP 白名单 + 单 Key 限流) + token_version 三级轮换 |
| 租户隔离 | ✅ 单容器操作/列表/任务/票据/备份计划全链路归属校验（F6 已修复） |
| 欠费停机 (Suspended) 强制力 | ✅ 本地/跨节点双端拦截（F1 已修复，含快照/备份/NAT/防火墙/ISO/救援/流量重置，F1a）；WHMCS 侧非 Active 服务拒绝状态变更操作 |
| SQLi / XSS | ✅ 无风险（11 处 DB 访问全参数化；模板全 escape） |
| CSRF | ✅ 模块 AJAX 入口 session token + hash_equals（F5 已修复，待真机端到端） |
| SSRF | ✅ 面板侧 safehttp 到位；Webhook 投递改走 safehttp.Post，DNS 解析期 + 拨号期二次校验（F2 已修复） |
| 节点添加 | ✅ 一键（一次性 token + 24h TTL + IP 绑定 + SHA256 + systemd）+ 手动（admin-only）；注册审计日志已补（F3 已修复）；install_key 全链路走 `X-Install-Key` 头（F4 已修复） |
| 密钥管理 | ✅ WHMCS 侧加密字段存储、日志不打印 Key、curl 不跟随重定向（F10 已修复）；⚠️ 面板侧节点 Token 明文存 SQLite（F7，低，P2） |

**修复状态**：P0 全部 2 项 ✅ 已修复并通过回归（含 `-race`）；P1 全部 7 项 ✅ 已修复并通过全量回归（40/40 包 + race clean + php -l，见 10.5）；P2 已完成 3 项（F6 备份计划归属校验、F9 节点地址默认 https、F11 拒绝明文落库），余 5 项规划中。详见第九节路线图。

---

## 二、信息缺口清单与假设

| # | 缺口 | 影响 | 假设处理 |
|---|------|------|---------|
| G1 | WHMCS 版本、PHP 版本未提供 | AdminServicesTabFields / ClientAreaAllowedFunctions 需要 WHMCS 8+/9+ | 假设 WHMCS 8.0+（代码 APIVersion 1.1 佐证 eyvescloud.php:33） |
| G2 | 生产环境反代是否记录 query string 未知 | 影响 F4（install_key 进 URL）的实际暴露面 | 按"会记录"保守评估 |
| G3 | 魔方云 API 官方文档不可公开访问 | 竞品矩阵该列基于国内 IDCSystem 类系统通用能力 + 魂环/WHMCS 生态常见实现 | 标注"推断"，仅 Virtualizor/SolusVM 给官方文档来源 |
| G4 | agent 二进制分发渠道完整性未验证 | install.sh 下载 agent 二进制仅做可执行性自检 | ✅ 已修复（P1-7）：主控分发附带 X-Binary-SHA256 头，安装脚本强制 sha256sum 比对 |
| G5 | 多租户 (tenant) 功能的线上使用规模未知 | F8（内存/CPU 无累计配额）严重度依赖租户数量 | 按多租户已启用评估 |
| G6 | 面板部署拓扑（主控是否公网、agent 是否内网）未知 | F9（主控→agent 默认 http://）实际风险 | 按主控-agent 同内网保守评估为 Low |

---

## 三、WHMCS 模块函数与上游 API 映射表

模块：`backend/internal/integrations/whmcs/module/eyvescloud.php`（29 个入口函数）

| WHMCS 函数 | 存在 | 位置 | 上游 API | 状态 | 缺失风险 / 备注 |
|---|---|---|---|---|---|
| MetaData | ✅ | eyvescloud.php:26 | — | ✅ | APIVersion 1.1 |
| ConfigOptions | ✅ | eyvescloud.php:46 | — | ✅ | 24 个配置项（helpers.php:56-253），见 §3.1 |
| TestConnection | ✅ | eyvescloud.php:87 | `GET /api/v1/dashboard` | ✅ | — |
| CreateAccount | ✅ | eyvescloud.php:109 | `POST /api/v1/containers`（Idempotency-Key）+ 预检查 + 轮询 tasks + routing 解析公网 IP | ✅ | **三级幂等**：预检查(111-114) → 幂等键(122-123) → 容器名唯一兜底；后端命中幂等键返回既有容器（handlers.go:693-710） |
| SuspendAccount | ✅ | eyvescloud.php:162 | `POST /api/v1/containers/{name}/suspend` | ✅ | 面板侧阻断电源/控制台；⚠️ 跨节点绕过见 F1 |
| UnsuspendAccount | ✅ | eyvescloud.php:173 | `POST /api/v1/containers/{name}/unsuspend` | ✅ | 不自动开机（合理） |
| TerminateAccount | ✅ | eyvescloud.php:184 | `DELETE /api/v1/containers/{name}/delete` | ✅ | — |
| ChangePassword | ✅ | eyvescloud.php:200 | `POST /api/v1/containers/{name}/reset-password` | ✅ | 成功后写回 tblhosting.password（加密，helpers.php:2403） |
| **ChangePackage** | ✅ | eyvescloud.php:221 | `PUT resource-limit` + `PUT traffic-limit` + `PUT expiry`（helpers.php:2129-2183） | ✅ **v1 P0 已闭环** | ⚠️ 非原子（三步串行不回滚）；disk 仅扩容（handlers.go:1150-1155 拒绝缩容）；`array_filter` 剔除 0 值导致"0=不限"配置不推送（helpers.php:2141-2143） |
| ClientArea | ✅ | eyvescloud.php:425 | AJAX → handlers/api.php | ✅ | 数据不直连上游 |
| AdminArea | ⚠️ | — | — | 等价物 | 未实现；由 AdminServicesTabFields（eyvescloud.php:531-588，WHMCS 8+ 推荐方式）替代，调 `GET containers/{name}` |
| UsageUpdate | ✅ | eyvescloud.php:368 | `GET containers/{name}/usage` + `/traffic` 逐服务 | ✅ | Capsule 参数化查询 tblhosting（379-382） |
| ServiceStatus | ⚠️ | — | — | 等价物 | 未实现；由 eyvescloud_Sync（348-359）承担 |
| LoginLink | ✅ | eyvescloud.php:496 | 面板 `/user` 链接 | ✅ | — |
| AdminCustomButtonArray | ✅ | eyvescloud.php:595 | — | ✅ | 8 按钮：Sync/TrafficReset/HardOff/RescueMode/RescueExit/ISOAttach/ISODetach/VNC |
| ClientAreaCustomButtonArray | ⚠️ | — | — | 等价物 | 未实现；由 ClientAreaAllowedFunctions（457-472，WHMCS 9 白名单，11 函数）替代 |
| ServiceSingleSignOn | ✅ | eyvescloud.php:507 | `GET containers/{name}` | ✅ | — |

### 3.1 ConfigOptions → CreateAccount 参数映射

24 项配置（helpers.php:56-253）经 `eyvescloud_container_payload()`（helpers.php:1151-1200）组装：virtualization/template_id/vcpu/cpu_percent/ram_mb/disk_gb/network_bw_mbps/traffic_mode/monthly_traffic_gb/traffic_in_gb/traffic_out_gb/io_speed_mbps/assign_nat/port_mapping_count/snapshot_limit/extra_ports/assign_ipv4/ipv4_count/assign_ipv6/ipv6_count/ssh_auth_mode/ssh_password/ssh_public_key/sync_expiry。数值全部强转 + min 边界，容器名清洗（helpers.php:863-875），**无注入面**。

---

## 四、安全发现（按严重级别排序）

### F1 — 跨节点容器 Suspended 绕过

- **ID**：F1
- **标题**：已挂起（欠费停机）的跨节点容器可被直接开机
- **严重级别**：**High**
- **位置**：`backend/internal/api/handlers.go:151-170`（主控代理路径）、`backend/internal/api/agent_api.go:84-98`（agent 端 start 分支）
- **证据**：
  - 主控 `HandleSingleContainer` 的 start/restart 分支在 scope 检查后**直接 `routeToAgent(...)` 转发，无 Suspended 检查**（handlers.go:151-170）
  - agent 端 `HandleAgentContainerAction` 的 `case "start"` 直接 `startByRuntime(id)`，**同样无 Suspended 检查**（agent_api.go:84-98）
  - 对比：本地容器走任务队列有检查——`taskqueue.go:621-627` `"容器已挂起（欠费停机），不允许此操作"`
- **影响**：WHMCS SuspendAccount 后，欠费客户通过面板 API（或 WHMCS 客户区按钮，见 F1a）仍可开机跨节点容器 → **计费强制失效**，欠费服务持续消耗资源
- **复现步骤**：
  1. 创建 NodeID ≠ 空（跨节点）的容器，调 `POST /api/v1/containers/{id}/suspend`
  2. 以容器属主身份调 `POST /api/v1/containers/{id}?action=start`
  3. 主控 routeToAgent 转发 → agent 端直接开机成功（无拦截）
- **修复建议**：
  - agent 端 `HandleAgentContainerAction` 的 start/restart/reinstall 分支入口加 `if c.Suspended { 403 }`（与 taskqueue.go:621 同文案）
  - 主控 `HandleSingleContainer` 的电源类 action 在 routeToAgent **之前**统一加 Suspended 检查（防御纵深，双端都加）
- **验证方法**：单测构造 `Suspended=true` + `NodeID="node-1"` 的容器，断言 start 返回 403；集成测试双节点环境重复复现步骤第 2 步应 403
- **修复状态**：✅ **已修复（2026-09-26）**
  - 主控：`handlers.go` `HandleSingleContainer` 在 routeToAgent 之前统一拦截 Suspended/到期/流量超限的 start/restart/reinstall
  - agent：`agent_api.go` `HandleAgentContainerAction` 同口径防御纵深检查（unsuspend 分支豁免）
  - 回归：`suspended_enforcement_test.go` TestSuspendedContainerPowerOpsRejectedCrossNode / TestAgentStartRejectsSuspendedContainer / TestAgentUnsuspendStillWorks 全通过（`-race -count=3` clean）

### F1a — Suspended 容器的快照/备份/NAT/防火墙/ISO/救援仍可操作（含 WHMCS 客户区按钮）

- **ID**：F1a
- **标题**：欠费停机仅阻断电源与控制台，破坏性管理操作全部放行
- **严重级别**：Medium
- **位置**：`backend/internal/api/handlers.go:492-536、606-619`、snapshots.go、backups.go:378-391；WHMCS 侧 handlers/api.php（全文无 domainstatus 检查）+ clientarea.tpl（按钮不按状态禁用）
- **证据**：快照创建/还原/删除、备份还原、NAT 增删改、防火墙保存、ISO 挂载、救援模式、流量重置各 handler 均无 `Suspended` 判断；WHMCS 模块侧 RescueMode/ISOAttach 按钮（eyvescloud.php:268-336）也不检查服务状态
- **影响**：欠费客户可执行**快照还原/备份还原**等破坏性操作；已付费快照配额/备份存储持续被免费占用（配额绕过的一种形态）
- **复现步骤**：suspend 容器 → 属主调 `POST ?action=recipes/execute` 或快照创建接口 → 成功
- **修复建议**：
  - 面板侧：上述 handler 入口统一加 Suspended 检查（建议抽 `requireNotSuspended(c)` helper，在 HandleSingleContainer switch 顶部对非只读 action 调用）
  - WHMCS 侧：handlers/api.php 加 `domainstatus != 'Active'` 时拒绝状态变更类 op（查询类放行）
- **验证方法**：suspend 后逐个调用上述端点应 403；WHMCS 客户区 suspend 状态下按钮点击应返回业务错误
- **修复状态**：✅ **面板侧已修复（2026-09-26）**；WHMCS 侧 domainstatus 检查待做
  - `handlers.go` 新增 `isSuspendedBlockedManageAction` 统一拦截：ISO/救援/脚本/进程/服务 POST，快照创建与还原 POST，备份还原 POST，NAT 增删改，防火墙写入
  - 豁免（不误伤）：unsuspend、stop、计费字段调整（traffic-limit/expiry）、快照删除（释放配额）、全部 GET
  - 回归：TestSuspendedContainerManageActionsRejected（11 个破坏性操作全 403）+ TestSuspendedContainerExemptActionsNotRejected + TestIsSuspendedBlockedManageAction 全通过

### F2 — Webhook 投递 SSRF 防护不完整

- **ID**：F2
- **标题**：私有网段放行 + DNS rebinding 无防护 + 投递时不复查
- **严重级别**：Medium
- **位置**：`backend/internal/api/notify.go:29-57`、`backend/internal/api/webhooks.go:364-381`
- **证据**：
  - notify.go:29-30 注释明言"回环与内网（RFC1918）地址保留"——内网自托管设计取舍，`http://10.0.0.5`、`http://127.0.0.1` 允许（security_hardening_test.go:221-246 断言）
  - notify.go:32 注释自认"按字面 IP 判定，DNS 解析到受限地址不在本函数覆盖范围"
  - 投递时点 `webhookDeliveryOnce`（364-367）仅语法复检，不再做 IP 校验；域名 URL 经 rebinding 可指向 169.254.169.254 等
  - 未复用已有 `internal/safehttp`（解析后校验 + Dialer 连接时二次校验 + 重定向逐跳校验，safehttp.go:83-158），投递用普通 `http.Client`（webhooks.go:381）
- **影响**：webhook 为 admin-only（server.go:177-178），攻击前提是管理员凭据被盗 → 仍可打通主控内网/云元数据端点读取敏感信息
- **复现步骤**：admin 创建 webhook 指向 `http://attacker.com/rebind`（DNS 第一次解析公网通过校验、TTL 过后解析到 169.254.169.254）→ 触发事件 → 投递请求打到元数据端点
- **修复建议**：webhook 投递改用 `safehttp` 客户端（连接时二次校验即可覆盖 rebinding）；至少在投递前重新跑完整 `validateWebhookURL` 而非语法子集
- **验证方法**：单测 mock DNS（先公网后私网解析）断言投递被拒；现有 security_hardening_test.go 补 rebinding 用例

### F3 — 节点注册无审计日志

- **ID**：F3
- **标题**：一次性 install_key 的消费事件不可追溯
- **严重级别**：Medium
- **位置**：`backend/internal/api/nodes.go:204-275`（handleNodeRegister 全函数无 auditRequest）
- **证据**：对比 node.create（nodes.go:327）/ delete（353）/ maintenance（1046）/ drain（1092）均有审计；注册（谁在何时用哪个 key 注册了哪个节点、来源 IP）零留痕
- **影响**：key 泄露被他人注册恶意节点时无法事后追责；不满足"注册过程审计日志"要求
- **修复建议**：handleNodeRegister 成功/失败路径各加 `auditRequest`（事件 `node.register`，detail 含 node name、来源 IP、key 指纹前 8 位——完整 key 已有脱敏正则覆盖 auth.go:180-199）
- **验证方法**：注册后查审计日志 API 应有 node.register 记录；失败（key 过期/IP 不匹配）同样留痕

### F4 — install_key 明文出现在 URL query

- **ID**：F4
- **标题**：安装脚本与 agent 二进制下载 URL 携带完整 key
- **严重级别**：Medium
- **位置**：`backend/internal/api/nodes.go:633`（install-script URL）、`nodes.go:823`（binary URL）、脚本正文内嵌（nodes.go:699）
- **证据**：curlURL 形如 `.../install-script?install_key=<32字节hex>`；应用层审计已脱敏（auth.go:194 的 `install_key=` query 正则），但**反代/访问日志记录完整 URL 时应用层无法覆盖**
- **影响**：key 在代理日志、浏览器历史、shell history 中留存；虽然 key 一次性 + 24h TTL + IP 绑定（nodes.go:225-242）大幅压缩利用窗口，仍是凭据卫生问题
- **修复建议**：改 POST 或自定义 header（`X-Install-Key`）传参；curl 管道场景可改为 `curl -fsSL -H "X-Install-Key: ..." <url> | sudo bash`，key 从环境变量读：`sudo INSTALL_KEY=... bash`
- **验证方法**：抓包确认 key 不在 URL；反代 access log 无 key 字样

### F5 — WHMCS 模块 AJAX 入口无 CSRF token

- **ID**：F5
- **标题**：客户区所有状态变更按钮仅靠 Origin/Referer + POST 防护
- **严重级别**：Medium
- **位置**：`whmcs/module/handlers/api.php:107-129`
- **证据**：仅有条件同源校验（Origin/Referer **存在时**比对 host）；未使用 WHMCS 标准 `check_token()`。浏览器省略 Origin/Referer 时完全依赖 cookie SameSite（不可控）
- **影响**：若客户浏览器对 WHMCS 域的 cookie SameSite 配置宽松，第三方页面可伪造开机/关机/重装/快照还原请求
- **复现步骤**：构造跨站页面 POST `handlers/api.php`（op=power&action=destroy），携带受害者 cookie（SameSite=None 场景）→ 操作生效
- **修复建议**：handlers/api.php 入口调 `check_token('WHMCS.default')`（或比对 `token` == `$_SESSION['csrfToken']`）；clientarea.tpl 所有 fetch body 附带 `{ token: csrfToken }`（Smarty 变量 WHMCS 已内置）
- **验证方法**：不带 token 的跨站 POST 应 403；正常客户区操作全通过回归
- **修复状态**：✅ **已修复（2026-09-26）**（自建 session token，未用 check_token——AJAX 入口非 WHMCS 标准表单流）
  - `helpers.php` 新增 `eyvescloud_csrf_token()`：`random_bytes(32)` 生成，绑定 `$_SESSION['eyvescloud_csrf']`
  - `eyvescloud.php` ClientArea 模板变量注入 `csrf_token`
  - `clientarea.tpl` `data-csrf` 属性 + 所有 AJAX body 附带 `token` 字段
  - `handlers/api.php` 客户会话（$uid>0）一律 `hash_equals` 常时比对，失败 403
  - `php -l` 三个改动文件全部无语法错误；端到端（TC-04/TC-05）需真机 WHMCS 环境确认

### F6 — 备份计划无归属校验

- **ID**：F6
- **标题**：backup:write 的 API Key 可管理任意容器的备份计划
- **严重级别**：Low
- **位置**：`backend/internal/api/backup_plans.go:198-271`、`server.go:118-119`（仅 AuthMiddleware）
- **证据**：无 `isContainerAllowedForRequest` / OwnerSubject 校验。子用户被 subUserScopeAllowed 天然挡住（backup:* 均 default:false，auth.go:113-126）；API Key 由管理员签发，属信任边界内
- **影响**：越权范围限于"管理员签发的非 admin:access Key"，跨租户面窄
- **修复建议**：backup-plans 的容器字段过 `isContainerAllowedForRequest`，与容器其他操作对齐
- **验证方法**：绑定容器 A 的 Key 尝试给容器 B 建计划应 403

### F7 — 节点凭据明文存储

- **ID**：F7
- **标题**：Node Token / InstallKey 明文存 SQLite（依赖 0600 文件权限）
- **严重级别**：Low
- **位置**：`backend/internal/config/config.go:1303-1310`、`store_sqlite.go:169`（chmod 0600）
- **证据**：JSON 字段无应用层加密；对比 API Key 已是 argon2 哈希（apikey.go:127-131）
- **影响**：宿主机文件读取（备份泄露、误配置）即得主控→agent 全部通道凭据
- **修复建议**：主控侧对 Node.Token 用主密钥（env/KMS）AES-GCM 加密存储；短期至少确认 DB 文件不被纳入常规备份明文流转
- **验证方法**：直接 sqlite3 查询 nodes 表，token 字段应为密文

### F8 — 内存/CPU 无累计配额

- **ID**：F8
- **标题**：仅磁盘有累计超售检查，内存/CPU 连续开多台可超卖
- **严重级别**：Low
- **位置**：`backend/internal/api/resource_validation.go:16-34`（仅磁盘 cumulative）
- **证据**：单容器上限有检查（36-74），租户四维配额有（enterprise.go:1094-1135），但宿主机级内存/CPU 累计无
- **影响**：超售开关（overcommit ratio）缓解后属容量风险而非安全漏洞
- **修复建议**：`resource_validation.go` 补 RAM cumulative 检查（CPU 可保留超售语义，加告警阈值即可）
- **验证方法**：连续创建至超宿主内存×超售比，下一次创建应 400

### F9 — 主控→agent 通道默认 http

- **ID**：F9
- **标题**：node address 无 scheme 时默认补 `http://`，Bearer token 可明文传输
- **严重级别**：Low
- **位置**：`backend/internal/api/nodes.go:1194-1203`（normalizeNodeAddress）、`nodes.go:1164-1192`（proxyNodeRequest 普通 http.Client）
- **证据**：agent→主控方向已强制 https（agent.go:277-279，`--allow-insecure-http` 显式豁免），主控→agent 反向无对称强制；无 mTLS / 跳过校验选项
- **影响**：同内网部署下风险低；跨公网部署 agent 时 token 可被链路窃听
- **修复建议**：normalizeNodeAddress 默认改 `https://`（与 agent 侧对齐）；管理台手动添加节点表单加 TLS 校验开关与提示
- **验证方法**：添加无 scheme 的节点地址，存储值应为 https 前缀

### F10 — curl FOLLOWLOCATION 可泄露 API Key

- **ID**：F10
- **标题**：跨主机重定向时 X-API-Key 头被 curl 保留转发
- **严重级别**：Low
- **位置**：`whmcs/module/helpers.php:588`（CURLOPT_FOLLOWLOCATION => true）
- **影响**：面板被入侵或返回恶意 30x 时 WHMCS 存储的 API Key 外泄
- **修复建议**：关闭 FOLLOWLOCATION（面板 API 无重定向场景），或改用 `CURLOPT_REDIR_PROTOCOLS` 限制 + `CURLOPT_HTTPHEADER` 重定向剥离（PHP curl 不支持 per-redirect header 剥离，直接关闭最简）
- **验证方法**：mock 一个 302 跳转到外部域的端点，断言第二跳请求头无 X-API-Key

### F11 — store_password 加密降级

- **ID**：F11
- **标题**：encrypt() 不可用时 SSH 密码明文落库
- **严重级别**：Info
- **位置**：`whmcs/module/helpers.php:516-533`
- **证据**：`if (function_exists('encrypt')) {...} return $password;` 正常 WHMCS 环境必有 encrypt()，仅非常规嵌入场景降级
- **修复建议**：降级分支加日志并返回失败（拒绝明文落库）
- **验证方法**：临时移除 encrypt 定义，store_password 应抛错而非返回原文

### 已验证无风险项（事实）

| 检查项 | 结论 | 证据 |
|---|---|---|
| SQL 注入 | ✅ 11 处 DB 访问全 Capsule 参数化，无原始 SQL 拼接 | eyvescloud.php:134/379-382/404；helpers.php:2114/2409/2439/2447/2455/2500 |
| XSS | ✅ 模板全 `|escape:'html'`；JS 渲染统一 escapeHtml()；消息用 textContent | clientarea.tpl:147-520/578-597/653-660 |
| 密钥日志泄露 | ✅ debug 仅记录 url/method/http_code；不打印 API Key | helpers.php:612 |
| 重复开通 | ✅ 三级幂等（预检查 + Idempotency-Key + 名字唯一兜底） | eyvescloud.php:111-128；handlers.go:693-710 |
| IDOR（容器主链路） | ✅ 单容器/列表/任务/票据/批量全链路归属校验 | subuser.go:895-905/1014-1026；taskqueue.go:1092-1097/1184；vnc.go:50；ssh.go:58 |
| 审计脱敏 | ✅ password/token/api_key/install_key/secret 正则脱敏，全 auditRequest 覆盖 | auth.go:180-204 |
| API Key 存储 | ✅ argon2 哈希 + 指纹；仅回显一次；IP 白名单 + 单 Key 限流 | apikey.go:127-143/370-373；auth.go:608-615 |
| token_version 轮换 | ✅ 主管理员改密全吊销；子用户容器属主变更双侧吊销 | auth.go:517-520/250-299；handlers.go:331/345 |
| 命令注入 | ✅ 容器名正则白名单 + exec.Command 不走 shell | 全局模式（多轮审计确认） |

---

## 五、WHMCS 插件审计结果

### 5.1 功能完整性

| 能力 | 状态 | 说明 |
|---|---|---|
| 生命周期 8 函数 | ✅ | 见 §三映射表 |
| 幂等/竞态 | ✅ | CreateAccount 三级幂等；PUT 语义端点可安全重试 |
| 暂停后客户端操作 | ⚠️ | 面板侧电源/控制台已断（F1 除外）；**WHMCS 侧 handlers/api.php 无 domainstatus 检查**（F1a） |
| 容器定位健壮性 | ⚠️ | 全靠 `$params['domain']` 匹配容器名；客户改 hostname 后失配。建议 CreateAccount 成功时把 container_id 写 serviceid 自定义字段（自定义字段持久化缺失） |
| ChangePackage 原子性 | ⚠️ | 三步串行不回滚；disk 缩容会被面板拒绝导致降级失败（WHMCS 侧应预检 disk 只增） |
| 0 值配置语义 | ⚠️ | `array_filter` 剔除 0 → "0=不限"的套餐参数不推送（helpers.php:2141-2143） |
| 日志 | ✅ | debug 数组只含端点/耗时/http_code |
| 升级/卸载 | ⚠️ 未知 | 未发现 eyvescloud_ActivateDeactivate/升级钩子（需补充材料确认是否有 schema 变更需求） |

### 5.2 安全结论

SQLi ✅ 无 | XSS ✅ 无 | CSRF ⚠️ F5 | SSRF ✅（面板 URL 管理员可控 + webssh/vnc 白名单 + 私网拒绝，仅 F10 重定向头残留）| 密钥 ✅（F11 降级 Info）| 竞态 ✅ | 计费绕过 ⚠️ F1/F1a（面板侧根因）

---

## 六、双端管理方案（管理员端 + 客户端）

### 6.1 权限边界（现状 + 目标）

| 操作面 | 主体 | 现状 | 目标 |
|---|---|---|---|
| WHMCS 客户区按钮 | 服务属主 | 11 个白名单函数（电源/救援/ISO/VNC/同步/流量重置） | 保持；suspend 态禁用写操作（F1a 修复后自动收敛） |
| WHMCS 后台按钮 | WHMCS admin | 8 个（含 RescueMode/ISO/VNC） | 保持 |
| 面板用户前台 | 面板账号 | 全功能（快照/备份/NAT/防火墙） | 保持 |
| 面板子账号 | sub_user | viewer 只读+终端；operator 含电源/重装/密码/网络/快照写 | backup:*/node:*/apikey:* 保持禁用 ✅ |
| API Key | 签发时定 scope | 通配 + 容器绑定 | backup-plans 补容器绑定校验（F6） |

### 6.2 实现路径

1. **短期（纯插件侧）**：handlers/api.php 加 domainstatus 检查 + CSRF token（F5）；ChangePackage 前置 disk 只增预检
2. **中期（面板侧）**：F1/F1a 的 Suspended 统一拦截；F3 注册审计；F4 key 传输改造
3. **长期**：快照/备份/防火墙管理挂到 WHMCS 客户区（面板 handlers/api.php 已具备对应端点，仅缺 UI 入口——对齐 Virtualizor 客户区能力）

---

## 七、节点添加方案对比与最终选择

### 7.1 四方案对比

| 维度 | A. 动态 install.sh + 一次性 token | B. 静态 install.sh + 环境变量 | C. 主控 SSH/cloud-init 推送 | D. 主控生成签名命令 |
|---|---|---|---|---|
| 免密钥入库 | ✅ key 服务端生成不下发脚本正文 | ⚠️ key 走环境变量仍入 shell history | ❌ 主控存 SSH 私钥（新增高价值凭据） | ✅ |
| 一次性/短效 | ✅ 24h TTL + 用后清空 + IP 绑定 | ❌ 静态 key 长期有效 | — | ⚠️ 签名可带过期但实现复杂 |
| 防重放 | ✅ 注册即作废 | ❌ | — | ⚠️ |
| 部署摩擦 | ✅ curl 一行 | ✅ | ❌ 需主控可达被控 SSH/云 API | ✅ |
| 审计追溯 | ✅ key→节点一一对应 | ❌ | ⚠️ | ⚠️ |
| 实现复杂度 | 低 | 低 | 高（多云适配） | 高（签名/验签体系） |

### 7.2 选择：方案 A（当前已实现，符合选择）

当前实现证据核对：

| 要求 | 状态 | 证据 |
|---|---|---|
| 一键生成安装脚本 URL | ✅ 动态生成 | nodes.go:620 buildAgentInstallScript |
| token 一次性 | ✅ 注册成功即清空 | nodes.go:259-261 |
| 短有效期 | ✅ 24h | nodes.go:225-232 |
| 绑定 IP | ✅ 创建者 IP + /24 容差 | nodes.go:319/234-242（sameIPv4Prefix24:278-290） |
| HTTPS | ✅ agent 侧强制，脚本默认 https + HSTS | agent.go:269-282；nodes.go:743/624 |
| SHA256 校验 | ✅ 脚本 X-content-SHA256 + 独立校验端点 | nodes.go:621-627/645-678 |
| systemd 自启 | ✅ Restart=always + StartLimitBurst=5 | nodes.go:841-859 |
| 审计日志 | ❌ **缺失（F3）** | nodes.go:204-275 |
| 不硬编码密钥 | ⚠️ key 进 URL query（F4） | nodes.go:633/699 |
| 手动添加（地址/认证/TLS/SSRF/加密/日志） | ⚠️ | name+address 输入、admin-only + 审计 ✅（nodes.go:327）；Token 服务端生成 ✅（316-317）；TLS 开关 ❌（F9）；SSRF 校验 ❌（仅 scheme 校验，1205-1214）；加密存储 ❌（F7） |

**结论**：方案 A 架构正确，安全要求满足 7/10；F3/F4/F7/F9 四项补齐后达 10/10。

---

## 八、竞品矩阵

> 竞品标注：✅ 支持 / ⚠️ 部分 / ❌ 不支持 / 未知。Virtualizor 文档：virtualizor.com/docs；SolusVM 2：docs.solusvm.com；魔方云列基于国内 IDCSystem 类通用能力（**推断**，G3）。

### 8.1 API 能力矩阵

| 能力 | EYVESCLOUD | 魔方云(推断) | Virtualizor | SolusVM 2 |
|------|-----------|--------|-------------|-----------|
| 虚拟化 | LXC + KVM | KVM | OpenVZ/KVM/LXC | KVM |
| Resize（升降级） | ✅ **本轮已闭环**（resource-limit/traffic-limit/expiry） | ✅ | ✅ | ✅ |
| 欠费停机强制 | ⚠️ 本地✅/跨节点❌（F1） | ✅ | ✅ | ✅ |
| 快照/备份/计划 | ✅ | ✅ | ✅ | ✅ |
| ISO/救援/VNC/WebSSH | ✅ | ✅ | ✅ | ✅ |
| Scope-based API Key + 限流 | ✅ | ⚠️ Token | ⚠️ 单一 Key/Secret | ✅ |
| 审计日志（SHA-256 链 + 脱敏） | ✅ | ⚠️ | ⚠️ | ✅ |
| Webhook（HMAC 签名 + 重试 + 熔断） | ✅（SSRF 见 F2） | ⚠️ | ❌ | ✅ |
| 多节点 agent 架构 | ✅ | ✅ | ✅ | ✅ |
| 流量计费（多模式） | ✅ | ✅ | ✅ | ✅ |
| 迁移（跨节点热迁） | ⚠️ 仅 Clone/导出导入 | ✅ | ✅ | ✅ |
| Guest Agent | ⚠️ 部分（processes/services 需 qemu-guest-agent） | ✅ | ✅ | ✅ |

### 8.2 WHMCS 模块能力矩阵

| WHMCS 功能 | EYVESCLOUD | 魔方云(推断) | Virtualizor | SolusVM 2 |
|-----------|-----------|--------|-------------|-----------|
| 全生命周期 8 函数 | ✅（本轮 ChangePackage 闭环） | ✅ | ✅ | ✅ |
| UsageUpdate 流量同步 | ✅ | ✅ | ✅ | ✅ |
| 客户区管理面板 | ✅ 11 按钮 | ✅ | ✅ | ✅ |
| 后台按钮 + 服务页字段 | ✅ 8 按钮 + 10 字段 | ✅ | ✅ | ✅ |
| Rescue/ISO/VNC 客户区直达 | ✅ | ✅ | ✅ | ✅ |
| 快照/备份/防火墙挂 WHMCS | ❌ 仅面板（P2） | ✅ | ✅ | ✅ |
| Additional Disk / 弹性 IP 计费 | ❌（P2） | ✅ | ✅ | ✅ |
| PAYG 按量计费 | ❌ | ❌ | ❌ | ✅ |
| Reseller 模块 | ❌ | ✅ | ✅ | ✅ |
| SSO | ✅ | ✅ | ✅ | ✅ |

**差距结论**：核心生命周期已无差距；剩余差距集中在**快照/备份/防火墙的 WHMCS 客户区入口**、**Additional Disk/弹性 IP 计费项**、**跨节点热迁移**、**PAYG**。

---

## 九、修复优先级与路线图

### P0（阻塞 WHMCS 商用上线）

| # | 任务 | 侧 | 关联发现 | 状态 |
|---|------|----|---------|------|
| 1 | agent 端电源操作 + 主控代理路径补 Suspended 检查（双端防御） | 面板 | F1 | ✅ 已修复并通过回归（含 `-race`） |
| 2 | WHMCS handlers/api.php 补 CSRF token（check_token）+ clientarea.tpl 附带 token | 插件 | F5 | ✅ 已修复（session token + hash_equals），待真机端到端 |

### P1（上线后两周内）

| # | 任务 | 侧 | 关联 | 状态 |
|---|------|----|------|------|
| 3 | 快照/备份还原/NAT/防火墙/ISO/救援/流量重置补 Suspended 拦截 + WHMCS domainstatus 检查 | 双侧 | F1a | ✅ 已修复：面板侧 `isSuspendedBlockedManageAction`；WHMCS 侧 api.php 非 Active 服务拒绝状态变更类操作（查询白名单放行） |
| 4 | Webhook 投递改 safehttp / 投递前完整复查 + rebinding 用例 | 面板 | F2 | ✅ 已修复：投递走 `safehttp.Post`（DNS 解析期 + 拨号期二次校验，AllowLoopback 受控放开）；`TestPostBlocksDNSRebindingAtDial` 覆盖 rebinding |
| 5 | handleNodeRegister 补审计日志（成功/失败双路径） | 面板 | F3 | ✅ 已修复：成功/过期 key/IP 不匹配等路径均落 `node.register` 审计 |
| 6 | install_key 改 header/POST 传参，移出 URL query | 面板 | F4 | ✅ 已修复：install-script / binary / 注册流程统一 `X-Install-Key` 头，key 不再出现在 URL |
| 7 | agent 二进制下载补 SHA256（当前仅可执行性自检） | 面板 | G4 | ✅ 已修复：主控分发附带 `X-Binary-SHA256` 响应头（按 mtime+size 缓存）；安装脚本 sha256sum 强制比对，失败删产物并中止；`--version` 自检保留为第二道防线 |
| 8 | 关闭 curl FOLLOWLOCATION | 插件 | F10 | ✅ 已修复：`CURLOPT_FOLLOWLOCATION = false`，3xx 显式报错 |
| 9 | ChangePackage 前置 disk 只增预检（避免降级订单半途失败） | 插件 | §5.1 | ✅ 已修复：`eyvescloud_changepackage_disk_precheck` 在发起任何变更请求前拦截 disk 缩容 |

### P2（规划中）

| # | 任务 | 关联 | 状态 |
|---|------|------|------|
| 10 | backup-plans 归属校验 | F6 | ✅ 已修复：计划 CRUD/run 全链路过 `isContainerAllowedForRequest` 同口径校验；受限请求（绑定容器的 Key/子用户）不能跨容器操作、不能建/见全局计划（container_id=0）；列表按绑定范围过滤。回归 `TestBackupPlanOwnershipEnforced` |
| 11 | 节点 Token AES-GCM 加密存储 | F7 | 待做 |
| 12 | RAM 累计配额检查 | F8 | 待做 |
| 13 | node address 默认 https + TLS 开关 | F9 | ✅ 已修复：主控 `normalizeNodeAddress` 无 scheme 默认补 `https://`；agent 侧注册时按自身面板 SSL 状态显式补全 scheme（`normalizeSelfAddress`/`selfPanelScheme`），无 scheme 的 `--addr` 不会被误存 https 断链。回归 `TestNormalizeNodeAddressDefaultsHTTPS`/`TestNormalizeSelfAddressByPanelScheme` |
| 14 | store_password 降级分支拒绝明文落库 | F11 | ✅ 已修复：`eyvescloud_store_password` 加密不可用时返回 null（记日志），两处调用方（reset-password / update_host_from_container）跳过 password 字段写库，保留库中旧值 |
| 15 | CreateAccount 持久化 container_id 自定义字段（抗 hostname 失配） | §5.1 | 待做 |
| 16 | 快照/备份/防火墙挂 WHMCS 客户区（对齐竞品） | §8.2 | 待做 |
| 17 | Additional Disk / 弹性 IP 计费项（Configurable Options 扩展） | §8.2 | 待做 |

---

## 十、测试用例与验收标准

### 10.1 P0 修复验收

| 用例 | 步骤 | 预期 |
|---|---|---|
| TC-01 跨节点 Suspended | 双节点环境，suspend 跨节点容器 → 属主 `POST ?action=start` | 403 "容器已挂起"（与本地容器同文案） |
| TC-02 本地回归 | suspend 本地容器 → start | 403（现有行为不回归） |
| TC-03 admin 豁免 | admin 对 suspended 容器 start | 按设计决定（建议 admin 放行并在审计记录） |
| TC-04 CSRF | 跨站 POST handlers/api.php（无 token） | 403 |
| TC-05 CSRF 正常路径 | 客户区全部 11 按钮回归 | 全通过 |

### 10.2 P1 验收

| 用例 | 预期 |
|---|---|
| TC-06 suspend 后快照创建/还原、备份还原、NAT/防火墙保存、ISO 挂载、救援、流量重置 | 全部 403 |
| TC-07 webhook 指向 rebinding 域名（先公网后 169.254.169.254 解析） | 投递被拒 + 记录失败原因 |
| TC-08 节点注册（成功/过期 key/IP 不匹配） | 三种结果均有 node.register 审计记录 |
| TC-09 安装脚本 URL | 抓包无 install_key query 参数 |
| TC-10 302 跳转外部域的 mock 端点 | 第二跳请求头无 X-API-Key |
| TC-11 ChangePackage 降级套餐（disk 变小） | WHMCS 预检即报错，不发部分请求 |

### 10.3 回归基线

```
go build ./... && go vet ./...          → clean
go test -short -count=1 ./...           → 40/40 packages ok
go test -race ./internal/api/...        → race clean
php -l（模块全部 PHP 文件）              → No syntax errors
```

### 10.4 P0 修复回归记录（2026-09-26）

| 命令 | 结果 |
|------|------|
| `go build ./...` | ✅ |
| `go vet ./internal/api/ ./internal/config/` | ✅ clean |
| `go test ./internal/api/ ./internal/config/ -count=1` | ✅ 全通过 |
| `go test ./internal/api/ -run 'TestSuspended\|TestAgentStart\|TestAgentUnsuspend\|TestIsSuspendedBlocked' -race -count=3` | ✅ 全通过，race clean |
| `php -l helpers.php / eyvescloud.php / handlers/api.php` | ✅ No syntax errors |

修复过程中发现并一并处理的**测试基础设施并发缺陷**（不修则任何 api 包测试都可能随机 panic/race）：

1. `store_sqlite.go`：`saveConfigToDB` 与卷记录函数在 `dbMu` **锁外**判 `db == nil`、锁内使用——与 `CloseConfigDB`（锁内置 nil）构成 check-then-act 竞态，后台任务队列 goroutine 命中时对 nil `*sql.DB` 调 `Begin()` panic。修复：nil 检查移入锁内（`openConfigDB` 的无锁写 `db = next` 同步加锁）。
2. `config.go`：`FindContainer`/`SaveConfig`/`SaveTasks`/`AddAuditLog`/`AddAuditLogFull` 在测试 teardown 把 `AppConfig` 还原为 nil 后被后台 goroutine 调用时解引用 nil。修复：各入口加 nil 防护，后台任务优雅失败而非崩溃。
3. `container_virtualizor_test.go`：cleanup 裸写 `config.AppConfig = previous` 与后台 goroutine 读构成数据竞争（`-race` 必报）。修复：改持 `AppConfigMu` 恢复。

### 10.5 P1 修复回归记录（2026-09-26）

P0 全部 + P1 全部 7 项修复完成后的全量回归：

| 命令 | 结果 |
|------|------|
| `go build ./...` | ✅ |
| `go vet ./...` | ✅ clean |
| `go test -short -count=1 ./...` | ✅ 40/40 packages ok |
| `go test -race -count=1 ./internal/api/... ./internal/safehttp/...` | ✅ 全通过，race clean（api 包 84s） |
| `php -l helpers.php / eyvescloud.php / handlers/api.php` | ✅ No syntax errors |
| `go test -race ./internal/api/ -run 'TestBuildAgentInstallScript\|TestExecutableSHA256\|TestHandleNodeBinary'` | ✅ 全通过（G4 新增 3 个用例） |

G4（P1-7）实现要点：

- 主控 `HandleNodeBinary` 分发二进制时附带 `X-Binary-SHA256` 响应头，摘要由 `executableSHA256` 计算（按 路径+大小+mtime 缓存，避免每次请求全量读自身二进制）；
- 安装脚本 `curl -D` 落盘响应头，`awk 'tolower($1)=="x-binary-sha256:"'` 提取摘要（mawk/gawk 可移植），对产物 `sha256sum` 强制比对，失败删除产物并以非零退出；
- `--version` 可执行性自检保留为第二道防线（防架构错配）。

**剩余待真机验证项**（无法在单测环境覆盖）：

- TC-04/TC-05：WHMCS 真机 CSRF 端到端（跨站 POST 无 token → 403；客户区 11 按钮全回归）；
- TC-09：抓包确认安装链路全程无 install_key 出现在 URL；
- TC-11：真机下单降级套餐，确认 WHMCS 预检即报错、面板侧零请求。

---

## 十一、证据索引

| 证据 | 位置 |
|---|---|
| agent 端电源无 Suspended 检查 | backend/internal/api/agent_api.go:84-98 |
| 主控代理路径无 Suspended 检查 | backend/internal/api/handlers.go:151-170 |
| 本地任务队列 Suspended 拦截（对照） | backend/internal/api/taskqueue.go:621-627 |
| install_key 一次性清空 / TTL / IP 绑定 | backend/internal/api/nodes.go:259-261 / 225-232 / 234-242 / 319 |
| 安装脚本 SHA256 + HSTS + systemd | backend/internal/api/nodes.go:621-627 / 624 / 841-859 |
| agent 侧强制 HTTPS | backend/internal/agent/agent.go:269-282 |
| webhook 校验与投递 | backend/internal/api/notify.go:29-57、webhooks.go:364-381、security_hardening_test.go:221-246 |
| safehttp 完整实现（未复用） | backend/internal/safehttp/safehttp.go:83-158 |
| scope 白名单 / 通配 / requireScope | backend/internal/api/auth.go:113-159 |
| 容器归属校验链 | backend/internal/api/subuser.go:895-905、1014-1026 |
| 审计脱敏正则 | backend/internal/api/auth.go:180-204 |
| API Key argon2 + 限流 | backend/internal/api/apikey.go:127-143、auth.go:608-615 |
| ChangePackage 三端点 | whmcs/module/helpers.php:2129-2183、eyvescloud.php:221-239 |
| CreateAccount 幂等 | whmcs/module/eyvescloud.php:111-128、backend handlers.go:693-710 |
| CSRF 现状 | whmcs/module/handlers/api.php:107-129 |
| FOLLOWLOCATION | whmcs/module/helpers.php:588 |
| store_password 降级 | whmcs/module/helpers.php:516-533 |
| 磁盘累计配额（仅磁盘） | backend/internal/api/resource_validation.go:16-34 |
| 租户配额四维 | backend/internal/api/enterprise.go:1094-1135 |
| Virtualizor Rescue/ISO/Enduser 文档 | virtualizor.com/docs/enduser/rescue-mode、/end-user-iso |
| SolusVM 2 文档 | docs.solusvm.com |

> 事实/推断标注：F1/F1a 的绕过路径为"代码路径成立"的推断（未起双节点实测），建议按 TC-01 实测确认；其余发现均有直接代码证据（事实）。魔方云列为推断（G3）。
