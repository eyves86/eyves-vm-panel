# 安全告警

EyvesCloud 内置基于连接行为的轻量安全告警能力。它不保存完整正常连接日志，而是关注异常行为和高风险模式。

## 覆盖场景

- 端口扫描。
- 横向扫描。
- 爆破倾向。
- SMTP 滥用。
- UDP 反射风险。
- 挖矿、代理、VPN、Tor 等可疑端口。

## 接口

```http
GET /api/v1/security/alerts
POST /api/v1/security/check
GET /api/v1/security/logs?container={name}
GET /api/v1/security/summary
GET /api/v1/security/settings
PUT /api/v1/security/settings
```

## 自动关机

安全设置中可配置告警后的自动关机策略。开启前建议先观察一段时间，确认规则不会影响正常业务。

## 日志建议

安全告警适合做风险提示，不应替代专业防火墙、入侵检测或集中日志系统。对公网暴露服务时，仍建议结合安全组、防火墙、Fail2ban 等工具。

---

# 两步验证（TOTP）

管理员登录可开启两步验证（Time-based One-Time Password，RFC 6238），从「仅密码」升级为「密码 + 动态口令」双因素认证，显著降低账号被撞库或口令泄露后的风险。

## 特性

- 基于标准 TOTP（HMAC-SHA1，30 秒周期，6 位动态码），支持 Google Authenticator、Microsoft Authenticator、1Password 等主流验证器 App。
- 启用时生成一批**一次性备份码**，登录时可直接用备份码代替动态口令。
- 备份码仅存储 bcrypt 哈希，且每个备份码仅可使用一次，用后即从哈希列表移除。
- 动态口令校验允许前后 1 个周期的时间偏差，容忍设备时钟轻微漂移。
- 关闭或换发备份码都需要提供当前动态口令（或有效备份码）二次确认。

## 登录流程

1. 提交用户名 + 密码；若已启用两步验证，后端返回 `401` 并带 `data.twofa_required: true`。
2. 前端切换到两步验证输入框，提示输入 6 位动态口令或一次性备份码。
3. 提交动态口令，校验通过后签发 JWT 完成登录。

## 接口

```http
GET  /api/2fa/status
POST /api/2fa/setup
POST /api/2fa/enable
POST /api/2fa/disable
POST /api/2fa/regenerate-backup-codes
```

- `GET /api/2fa/status`：返回 `{ enabled, has_secret }`。
- `POST /api/2fa/setup`：生成新的 TOTP 密钥，返回 `{ secret, otpauth_uri }`（仅未启用时）。
- `POST /api/2fa/enable`：携带动态口令 `code` 确认后启用，返回一批明文备份码 `{ backup_codes }`。
- `POST /api/2fa/disable`：携带动态口令或备份码 `code` 关闭。
- `POST /api/2fa/regenerate-backup-codes`：携带当前动态口令 `code` 换发一批新备份码。

> 以上接口仅管理员/持有 `admin:access` scope 的 Key 可调。前端入口在「设置 → 两步验证」，登录页会自动进入两步验证步骤。

## 密码强度策略

管理员修改密码时强制校验：**至少 10 位**，且**同时包含字母与数字**。弱口令会被 `validateStrongPassword` 拒绝。

## 登录风控

- 失败登录按「来源 IP + 用户名」在 10 分钟窗口内最多 5 次，超限返回 `429`（滑动窗口限流）。
- 两步验证校验失败同样记入失败次数并写入登录日志。
- 每次修改管理员密码会递增 `token_version`，使此前签发的全部管理员 token 立即失效。
