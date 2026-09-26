# 系统设置

系统设置页集中管理面板级配置，包含以下分组。

## SSL 证书

- 支持禁用、Let's Encrypt、自签名、上传证书四种模式。
- 配置目标域名和邮箱后可自动申请/续期 Let's Encrypt 证书。
- 保存后可以立即重启服务生效，或稍后手动重启 `systemd` 服务。

## 面板访问来源

- 可开启面板访问白名单，仅允许指定 IP/网段访问 Web 面板。
- 可配置可信反向代理地址，避免代理环境下误拦截。
- 与 `eyvescloud access-policy` CLI 等效，保存后立即生效。

## 通知推送

- 安全告警支持 Webhook 推送。
- 支持 SMTP 邮件推送，可配置服务器、端口、账号和收件人。

## 任务队列

- 配置任务队列并发数，控制同时执行的重装/创建/镜像任务数量。
- 保存后立即生效。

## 账号

- 修改管理员用户名和密码。
- 修改时需要输入当前密码确认。

## 其他

- WebSSH/WebVNC Origin 白名单。
- 语言设置（简体中文 / English）。

## 内存超售与 KSM

内存超售允许将「可分配内存上限 = 物理内存 × 超售比」提高到物理内存之上；KSM（Kernel Samepage Merging）通过合并重复内存页，降低超售场景下的真实内存占用。

```http
GET /api/overcommit/settings
PUT /api/overcommit/settings
```

`PUT` 请求体（仅管理员）：

| 字段 | 说明 |
| --- | --- |
| `memory_overcommit_enabled` | 是否启用内存超售 |
| `memory_overcommit_ratio` | 超售比，范围 `1.0`–`16.0` |
| `ksm_tuning` | 对象，含 `enabled`、`pages_to_scan`（0–1000000）、`sleep_millisecs`（1–60000）、`use_tune_ksm` |

响应额外包含 `physical_ram_mb`（物理内存）与 `allocatable_ram_mb`（可分配内存）。保存后立即尝试应用 KSM 内核参数，无法写入时返回警告而非失败。

## 指标留存

面板周期采集容器与主机指标，并聚合成小时级趋势。可配置保留天数，超过保留期的数据会被后台清理，避免磁盘无限增长。

```http
GET /api/metrics/retention
PUT /api/metrics/retention
```

`PUT` 请求体（仅管理员）：`{"retention_days": N}`，范围 `0`–`3650`，`0` 表示永久保留。响应还包含 `sample_interval_secs`、`raw_history_secs`。

## 面板版本检测与升级

版本检测与升级入口位于**侧边栏底部**（非系统设置页）：面板登录后自动检测一次新版本，有更新时侧边栏显示「有更新」角标；点击角标打开「面板更新」弹窗，可选择更新仓库（默认官方仓库，支持 `owner/name` 格式的第三方仓库）与目标版本，二次确认后升级。

```http
GET /api/v1/check-update
GET /api/v1/update/releases?repo=owner/name
POST /api/v1/update
```

- `GET /check-update` 仅管理员可调，返回 `current`（当前版本）、`latest`（最新版本）、`has_update`（是否有更新）、`err`（检测错误）。结果缓存 10 分钟。
- `GET /update/releases` 返回可选升级版本列表（默认最新 20 个），带 2 分钟短缓存；`repo` 参数经严格校验防 SSRF。
- `POST /update` 触发**面板内直接升级**：后台执行下载 → 解压 → 备份 → 就地替换二进制 → 自动重启服务，请求立即返回「已开始」。请求体可选 `{repo, tag}`；同一时间仅允许一个升级任务（并发触发返回 409）。升级只替换 `/usr/local/bin/eyvescloud`，`/root/.eyvescloud` 中的配置、容器数据与任务记录全部保留。

除面板内升级外，仍可通过 `install.sh` 或 CLI（`eyvescloud cli` 菜单）升级，三种方式等价。

## 账号密码恢复命令

若遗忘管理员密码，可通过 `eyvescloud account` CLI（配合 SSH 在服务器上直接执行）查看账号或重设密码：

```bash
eyvescloud account                   # 查看管理员账号、两步验证状态（也可用 eyvescloud kvm）
eyvescloud account reset             # 重设管理员密码（自动生成强密码，仅显示一次）
eyvescloud account reset --password <新密码>   # 显式指定新密码（至少 10 位）
```

> 提示：`eyvescloud kvm` 是 `account` 命令的等价别名，尤其方便在忘记账号/密码时快速调用（非交互，可在 SSH 终端直接执行）。

密码以 bcrypt 单向哈希存储，无法反查原密码；`reset` 会生成新强密码并打印一次，请妥善保存。若账号启用了两步验证，登录时仍需提供动态口令。

## 相关接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/PUT | `/api/ssl` | SSL 设置 |
| GET/PUT | `/api/access-policy` | 面板访问来源策略 |
| GET/PUT | `/api/notifications` | 通知推送设置 |
| GET/PUT | `/api/task-queue/settings` | 任务队列并发 |
| POST | `/api/change-password` | 修改密码 |
| POST | `/api/change-username` | 修改用户名 |
| GET/PUT | `/api/webssh-origins` | WebSSH/WebVNC Origin 白名单 |
| GET/PUT | `/api/overcommit/settings` | 内存超售与 KSM 调优 |
| GET/PUT | `/api/metrics/retention` | 指标留存策略 |
| GET | `/api/v1/check-update` | 面板版本检测 |
| GET | `/api/v1/update/releases` | 可选升级版本列表 |
| POST | `/api/v1/update` | 面板内直接升级 |
