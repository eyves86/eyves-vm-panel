# API v2 开发进度与验收清单

> 更新：2026-09-29 · 说明：清单用于恢复环境后**逐项编译/测试/部署验收**，未打勾的即为尚未验证。

## 一、已合并到 main 并线上实测（52 个端点）

部署版本：主控 154.16.173.136 运行 v2.2.6，实测响应见各 PR 描述。

### 认证（3）— PR #9 ✅
- [x] `POST /api/v2/auth/login`（管理员/子用户，支持两端点验证 `code`）
- [x] `POST /api/v2/auth/logout`
- [x] `GET  /api/v2/auth/me`（16 个功能点，供前端按钮渲染）

### 实例（29）— PR #8 ✅
- [x] `GET/POST /api/v2/instances`、`GET/PATCH/DELETE /api/v2/instances/{id}`
- [x] `POST /api/v2/instances/{id}/power|reinstall|reset-password|console|clone|lock`
- [x] `DELETE /api/v2/instances/{id}/lock`、`POST/DELETE /api/v2/instances/{id}/rescue`
- [x] `GET /api/v2/instances/{id}/metrics|usage|events|xml`
- [x] `GET/POST /api/v2/instances/{id}/snapshots`、`POST .../{sid}/restore`、`DELETE .../{sid}`
- [x] `GET/POST /api/v2/instances/{id}/backups`、`DELETE .../{bid}`
- [x] `PUT /api/v2/instances/{id}/network`、`POST /api/v2/instances/batch`

### 节点 / 分组 / 区域 / 调度（20）— PR #10 ✅
- [x] `GET/POST /api/v2/nodes`、`GET/PATCH/DELETE /api/v2/nodes/{id}`
- [x] `GET /api/v2/nodes/schedule`（过滤+评分+候选理由）
- [x] `POST /api/v2/nodes/{id}/maintenance|install-key`、`GET /api/v2/nodes/{id}/metrics|instances`
- [x] `GET/POST /api/v2/node-groups`、`PATCH/DELETE /api/v2/node-groups/{id}`、`PUT /api/v2/node-groups/{id}/nodes`
- [x] `GET/POST /api/v2/regions`、`PATCH/DELETE /api/v2/regions/{id}`

## 二、代码已写、**待编译验证**（42 个端点，未提交）

来源文件：`backend/internal/api/apiv2_catalog.go`、`apiv2_system.go`、`apiv2_adapters.go`（适配层）。

### 镜像 / ISO / 存储 / SSH 密钥 / 安全组 / IP 池（23）— 待验证
- [ ] `GET/POST /api/v2/images`、`GET/PATCH/DELETE /api/v2/images/{id}`、`POST /api/v2/images/{id}/download`
- [ ] `GET /api/v2/iso-images`、`DELETE /api/v2/iso-images/{id}`
- [ ] `GET /api/v2/storage-pools`、`GET /api/v2/storage-pools/{id}`
- [ ] `GET/POST /api/v2/ssh-keys`、`DELETE /api/v2/ssh-keys/{id}`
- [ ] `GET/POST /api/v2/security-groups`、`GET/PATCH/DELETE /api/v2/security-groups/{id}`
- [ ] `GET/POST /api/v2/security-groups/{id}/rules`、`DELETE /api/v2/security-groups/{id}/rules/{rid}`
- [ ] `GET/POST/DELETE /api/v2/ip-pools`

### 任务 / 备份 / Webhook / 用户 / 管理员 / API Key / 审计 / 监控 / 系统（19）— 待验证
- [ ] `GET /api/v2/tasks`、`GET /api/v2/tasks/{id}`
- [ ] `GET /api/v2/backups`、`POST /api/v2/backups/{id}/restore`、`DELETE /api/v2/backups/{id}`
- [ ] `GET /api/v2/webhooks`
- [ ] `GET /api/v2/users`、`GET /api/v2/users/{id}`
- [ ] `GET /api/v2/admins`
- [ ] `GET/POST /api/v2/api-keys`、`DELETE /api/v2/api-keys/{id}`
- [ ] `GET /api/v2/audit-logs`
- [ ] `GET /api/v2/metrics/host|instances|summary`
- [ ] `GET /api/v2/system/info|health|update-check`

**本轮已修但未编译验证的问题**（用文件编辑直接改的）：
1. 任务日志：`config.AppConfig.TaskHistory` 不存在 → 改用 `config.ListTaskLogs(taskID)`，字段改 `entry.CreatedAt`
2. 备份/用户列表关键字过滤：`string(item["..."])` 类型断言错误 → 改用 `stringOrEmpty(...)`
3. 补回 `hostSummaryForIDC()`（宿主机指标，`/metrics/host` 与首页共用）
4. `validCIDROrAddrV2` 改用 `netip.ParsePrefix/ParseAddr`；`itoaV2` 用 `strconv.Itoa`；`runtimeGOARCHValue` 用 `runtime.GOARCH`
5. 安全组常量改名：`secgroup.DirIngress/DirEgress/ProtoTCP/ProtoUDP/ProtoICMP/ProtoAny`
6. `startKVMImageDownloadV2` 已补（复用 `kvm.DownloadImageWithProgress` + 进度状态表 + 完成后自动启用）

## 三、尚未编写（下一批）

- [ ] Webhook 创建/修改/删除（含 URL SSRF 校验，复用 `safehttp.ValidateURL`）
- [ ] 备份计划（backup-plans）CRUD
- [ ] 子用户创建/修改/删除/重置密码（含容器绑定同步）
- [ ] 管理员创建/修改/删除（含角色校验）
- [ ] 实例 ↔ 安全组绑定（`PUT /instances/{id}/security-groups`）
- [ ] 任务取消（需先确认任务队列是否支持按 ID 取消）
- [ ] 实例迁移（需接入节点迁移流水线）

## 四、恢复环境后的执行顺序

1. `go build ./...` → 按报错逐条修（预计集中在 catalog/system 两个文件）
2. `go vet ./...`、`go test ./internal/api ./internal/config`
3. 提交 + PR + 合并（沿用 `security/…` → `feat/api-v2-*` 分支命名与 PR 流程）
4. 部署到主控并实测：`/api/v2/images`、`/api/v2/tasks`、`/api/v2/metrics/summary`、`/api/v2/system/health`、`/api/v2/api-keys`（创建一次并删除）
5. 补齐第三批（上文"尚未编写"）
6. 前端改造（侧边栏模块化 / 按钮按 features / 创建向导接节点选择与调度）
7. 全量审计

## 五、环境故障记录

本轮沙箱 shell 通道卡死：起因是执行 `python3 -`（等待标准输入）导致进程挂起，之后所有 `shell_execute` 均超时（含 `echo`），而 `file_read/file_write` 仍可用。
恢复方式：重启 Minis App（或清理后台进程）后 shell 通道即复位。
