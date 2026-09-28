# 安全修复报告（2026-09-28）

> 对象：EyvesCloud（`eyves-vm-panel`）v2.2.2 → 修复分支
> 范围：第三方安全审计发现的问题 + 仓库地址迁移（GitHub → Codeberg）
> 验证：`go build ./...`、`go vet ./...`、`go test ./internal/...`（40 个包全绿）、
> 前端 `tsc && vite build`、`gosec` 复扫（G404 / G112 / G117 / G124 已消除）

---

## 一、仓库地址迁移（官方源 → Codeberg）

| 位置 | 修改前 | 修改后 |
| --- | --- | --- |
| `backend/internal/version/version.go` | `Repo = "FenhaoLost/eyves-vm-panel"` | `Repo = "codeberg:fenhaolost/eyves-vm-panel"` |
| `backend/internal/cli/cli.go` | 默认平台 github / owner `FenhaoLost` | 默认 `codeberg` / `fenhaolost`；新增 `resolveUpdateRepo()` 统一解析（env → 面板配置 → 内置默认） |
| `backend/internal/config/config.go` | `NormalizeUpdateSource` 默认 github | 默认从 `version.Repo` 解析（单一来源，不再漂移） |
| `install.sh` | 默认 GitHub，仅支持 GitHub | 默认 `codeberg:fenhaolost/eyves-vm-panel`，新增 Codeberg / Gitee / GitLab 平台支持 |
| `frontend/src/components/Sidebar.tsx` | `DEFAULT_REPO = 'FenhaoLost/eyves-vm-panel'` | `'codeberg:fenhaolost/eyves-vm-panel'` |
| README / DEPLOYMENT / docs / ISSUE_TEMPLATE / WHMCS 模块 | 全部指向 GitHub 旧地址 | 统一指向 `https://codeberg.org/fenhaolost/eyves-vm-panel` |
| `.github/workflows/build-release.yml` | 只发 GitHub Release | 增加 `SHA256SUMS` 产物；新增 Codeberg Release 同步任务（`CODEBERG_TOKEN` 配置后生效） |

**背景（为何必须改）**：修复前 `github.com/FenhaoLost/eyves-vm-panel` 与其用户主页均返回 404，
而它仍是二进制内置的默认更新源与 README 的官方安装命令来源——既导致安装/更新失败，
又存在"用户名被他人注册后投毒"的供应链风险。

---

## 二、修复清单

### H-1 供应链：默认更新源指向可抢注仓库
见上表。现在默认源为 Codeberg 官方仓库，且 `EYVESCLOUD_REPO` 仍可覆盖（支持
`platform:owner/repo`、`https://host/owner/repo`、`owner/repo` 三种写法，字符集白名单校验）。

### H-2 升级/安装无完整性校验 + 任意第三方仓库 → root 代码执行
- **Go 侧**（`internal/cli/cli.go`）：新增 `verifyReleaseArchive()` / `fetchExpectedChecksum()` /
  `fileSHA256()`；面板内升级（`upgradeFromReleaseAssetInPlace`）与 CLI 升级
  （`upgradeFromReleaseAsset`）在解包前**强制比对 SHA-256**：
  - 支持 `SHA256SUMS` / `SHA256SUMS.txt` / `checksums.txt` 汇总清单，
    以及 `<asset>.sha256` 单文件校验值；
  - 支持 GNU（`<hash>  <file>`，按 basename 匹配）与 BSD（`SHA256 (file) = <hash>`）两种格式；
  - 哈希不匹配、清单损坏、清单缺失 → **中止升级**（fail closed）；
  - 缺清单时可用 `EYVESCLOUD_UPDATE_ALLOW_UNVERIFIED=1` 显式豁免（打印显著警告）。
- **Shell 侧**（`install.sh`）：新增 `compute_sha256()` / `verify_release_asset()`，
  归档与裸二进制两条下载路径都强制校验；`EYVESCLOUD_ALLOW_UNVERIFIED=1` /
  `EYVESCLOUD_SKIP_VERIFY=1` 为显式豁免开关（默认关闭）。
- **发布侧**：workflow 为每个架构产物生成 `*.sha256` 并汇总 `SHA256SUMS` 一起发布。
- 兼容：`install.sh` 同时接受 `eyvescloud-linux-<arch>/eyvescloud` 与扁平
  `./eyvescloud` 两种历史包布局。

### H-3 审计主体可伪造（`X-Original-Actor`）
- 主控侧（`api/nodes.go`）：始终用服务端判定的 actor 覆盖该 header，不再透传客户端值。
- agent 侧（`api/auth.go` + `api/agent_api.go`）：新增 `authTypeAgent` 认证类型，
  **仅**通过节点 token 校验的请求才采信该 header。
- 回归测试：`TestRequestActorPrefersXOriginalActor`（含"伪造被忽略"用例）。

### H-4 TOTP 可重放
- `api/totp.go`：新增 `verifyTOTPWithCounter()`（返回时间步，拒绝 `<= lastUsed` 的复用）、
  `totpCodeAtStep()`、`TOTPNow()`；校验改用 `subtle.ConstantTimeCompare`。
- 回归测试：`TestVerifyTOTPRejectsReplay`、`TestTOTPWindowAllowsAdjacentStep`（仍保留 ±1 周期时钟容差）。

### H-5 凭据明文落库 / 明文进备份
- 新增 `internal/config/secrets_at_rest.go`：复用节点 Token 的 AES-256-GCM 静态加密，
  覆盖容器 SSH 口令、任务配置口令、节点 Token/InstallKey、Turnstile 密钥、对接密钥、
  更新源 Token、SMTP 口令、**JWT 签名密钥**、**Webhook HMAC 密钥**、子用户口令/访问码。
- 落库（`internal/config/store_sqlite.go`）：`containers.ssh_password`、`tasks.cfg_ssh_password`
  以 `enc:v1:` 密文存储，读取时解密；存量明文透明兼容。
- 配置备份（`api/enterprise.go`）：导出前深拷贝并加密，还原时解密；无法解密的字段置空
  并在响应中列出字段名（跨密钥场景不会把密文当明文写回）。
- 迁移包（`api/migrate.go`）：导出前加密 SSH 口令；导入时解密，失败则置空并提示重置。
- 说明：子用户登录口令此前已不落库（写入时固定为空串），本次一并纳入导出加密；访问码
  因分享链接特性必须可逆，改为加密存储而非哈希。
- 回归测试：`internal/config/secrets_at_rest_test.go`（往返、密文不含明文、损坏密文置空、
  跨密钥导入回报字段名）。

### H-6 管理端 JWT 存 localStorage
- 新增 `api/session_cookie.go`：登录成功下发 `HttpOnly; SameSite=Lax`（HTTPS 下加 `Secure`）
  的会话 Cookie；`tokenFromRequest` 支持 Cookie 回退（`Authorization` 仍优先且完全兼容）；
  新增 `POST /api/logout` 清除 Cookie。
- CSRF 纵深防御：Cookie 认证的状态变更请求校验 `Origin`/`Referer` 是否同站（`cookieCSRFGuard`），
  配合 `SameSite=Lax` 双重阻断。
- 前端（`services/api.ts`、`contexts/AuthContext.tsx`、`Settings.tsx`、`ApiIntegration.tsx`）：
  令牌只保留在内存，不再写 `localStorage`；刷新页面依赖 Cookie 会话（`/api/check-auth` 校验）；
  登出调用 `/api/logout`。
- 回归测试：`TestSessionCookieAuthAndCSRF`、`TestCookieCSRFGuardUsesConfiguredOrigins`。

### H-7 HTTP 服务无超时（Slowloris）
- `internal/server/server.go`：`ReadHeaderTimeout=15s`、`ReadTimeout=30s`、`IdleTimeout=120s`、
  `MaxHeaderBytes=1MiB`；不使用 `WriteTimeout`（避免误杀 WebSSH/VNC 长连接）。

### H-8 低危项
- 对接密钥、子用户访问码比较改为常量时间（`crypto/subtle`）。
- 容器 MAC 生成由 `math/rand` 改为 `crypto/rand`（`randomVirtualMAC()`，24 位可预测 → 密码学随机）。
- LAN 上联接口名增加字符集与长度校验（15 字符上限，防配置注入，纵深防御）。
- 镜像资源校验不再架构相关：`IsWindowsImage` / `IsWindows11Image` 增加按 ID 回退识别，
  arm64 上也不会跳过 Windows 最小磁盘/内存校验；新增 `ImagesForArch()` 供跨架构查询。

---

## 三、测试与环境适配

- 修复前存在的 3 项失败测试已处理：
  - `TestFetchReleasesListLive`（默认仓库 404）→ 改为使用 `version.Repo`，现指向 Codeberg 并通过；
  - `TestWindows11ImageDefinition` / `TestWindowsMinimumResources` / `.../KVM_Windows_10G_fails`
    （测试与实现均架构相关）→ 实现侧改为架构无关，测试改用 `ImagesForArch("amd64")` 断言；
  - `TestGetAllowsPrivateNetworkMirror`（受限环境无法枚举网卡）→ 环境能力缺失时 `t.Skip` 而非失败。
- 新增回归测试：`internal/config/secrets_at_rest_test.go`、`internal/api/security_fixes_test.go`、
  `internal/cli/checksum_test.go`，并更新 `internal/api/audit_actor_test.go` 为新的安全语义。

---

## 四、升级部署注意（破坏性变更提示）

1. **务必先发布带 `SHA256SUMS` 的 Release**（本分支的 workflow 已自动生成）。否则：
   - 面板内升级/CLI 升级会被 fail-closed 拒绝（符合预期）；
   - `install.sh` 需临时设置 `EYVESCLOUD_ALLOW_UNVERIFIED=1` 才能安装旧 Release。
   仓库内 `release-checksums/SHA256SUMS.v2.2.2` 是本次为**现有 v2.2.2 产物**生成的校验清单，
   可直接上传到对应 Release（注意：v2.2.2 产物为扁平布局，安装脚本已兼容）。
2. **at-rest 密钥**：首次运行会自动生成 `<db>.tokenkey`（0600）或使用
   `EYVESCLOUD_NODE_TOKEN_KEY`（64 位 hex）。**请与数据库一同备份**——丢失后节点 Token、
   容器口令等均无法解密（需重新注册/重置）。
3. **配置备份文件格式**：新版备份中的凭据为 `enc:v1:` 密文，只能被**同一密钥**的面板还原；
   旧版（明文）备份仍可正常导入。
4. **HTTP 部署**：会话 Cookie 在纯 HTTP 下不带 `Secure`（否则浏览器直接丢弃），
   生产环境仍强烈建议启用 HTTPS。
5. **浏览器会话**：前端不再持久化 JWT，登录态完全依赖 Cookie；如需跨域部署，
   必须配置 `withCredentials` 与 CORS 白名单（本仓库同源部署无需改动）。
