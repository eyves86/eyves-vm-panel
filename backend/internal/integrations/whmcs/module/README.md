# EYVESCLOUD WHMCS 服务器开通模块

本模块把 WHMCS 9.0 的产品/服务生命周期映射到 EYVESCLOUD LXC/KVM 面板 API，支持实例开通、暂停、恢复、删除、开关机、重启、改密、变更套餐、同步状态、流量上报，以及客户区的实例信息、NAT 转发、防火墙、快照、备份、ISO 挂载（仅 KVM）和重装系统，并提供 WebSSH / VNC 控制台入口。

## 目录结构

```text
modules/servers/eyvescloud/
  eyvescloud.php              # 模块入口：WHMCS 生命周期与配置项
  helpers.php                 # 共享助手层：面板 API 封装 + 客户区 AJAX 分发
  README.md
  handlers/
    api.php                   # 客户区 AJAX 入口（会话与归属校验）
    webssh.php                # WebSSH 控制台页面
    vnc.php                   # WebVNC 控制台页面（KVM）
  templates/
    clientarea.tpl            # 客户区 Smarty 模板（选项卡 + AJAX）
```

安装时请保持目录结构不变，将整个 `eyvescloud` 目录放入：

```text
<WHMCS 根目录>/modules/servers/eyvescloud/
```

## 环境要求

- WHMCS 9.0（兼容 WHMCS 8.x）
- PHP 8.0+（推荐 8.2）
- PHP 扩展：`curl`、`json`、`openssl`
- EYVESCLOUD 面板建议启用 HTTPS

## 服务器配置

在 WHMCS 后台添加服务器时，模块选择 `eyvescloud`。

推荐直接在「主机名」中填写完整面板地址：

```text
主机名 = https://panel.example.com:8999
```

也可以拆分填写：

```text
IP 地址 = 0.0.0.0
端口     = 8999
Secure   = 勾选
```

API Key 可填写在以下任一字段：

```text
Access Hash
密码
```

模块请求 EYVESCLOUD 时会同时携带：

```text
X-API-Key: eyvescloud_sk_xxxx
Authorization: Bearer eyvescloud_sk_xxxx
Content-Type: application/json
```

## 产品配置项

配置项顺序与 `eyvescloud_ConfigOptions()` 中的定义一致：

| 字段 | 说明 |
| --- | --- |
| 虚拟化类型 | `lxc` 或 `kvm` |
| 镜像/模板 ID | EYVESCLOUD 模板 / 镜像 ID，例如 `alpine-3.21` |
| CPU 核心 | vCPU 数量，KVM 必须为整数 |
| CPU 百分比 | CPU 使用率限制，`0` 表示不额外限制 |
| 内存 MB | 容器内存，单位 MB |
| 硬盘 GB | 系统盘大小，支持 `0.5`、`0.75`、`1`、`5` 等浮点数 |
| 带宽 Mbps | 网络带宽限制，`0` 表示不限制 |
| 流量模式 | `total` 总流量，或 `in_out` 入/出分开 |
| 月流量 GB | `total` 模式下的月流量限制，`0` 表示不限制 |
| 入站流量 GB | `in_out` 模式使用 |
| 出站流量 GB | `in_out` 模式使用 |
| IO 速度 MB/s | 磁盘 IO 限制，`0` 表示不限制 |
| 分配 NAT | 开通时是否分配 NAT 端口映射 |
| NAT 端口数量 | 开通时分配的端口映射数量，最小 2 |
| 快照配额 | 每台实例允许保留的快照数量 |
| 额外端口 | 逗号分隔的容器端口，例如 `80,443` |
| 自动公网 IPv4 | 开通时是否从公网 IPv4 池分配独立 IPv4 |
| 公网 IPv4 数量 | 自动分配 IPv4 的数量，通常为 1 |
| 自动 IPv6 | 开通时是否自动分配 IPv6 |
| IPv6 数量 | 自动分配 IPv6 的数量，通常为 1 |
| SSH 鉴权模式 | `auto_password` / `password` / `key` |
| 指定 SSH 密码 | 鉴权模式为 `password` 时使用 |
| SSH 公钥 | 鉴权模式为 `key` 时使用 |
| 同步到期时间 | 开通/续费时把 WHMCS 到期日期同步到面板（转为 `YYYY-MM-DD`） |

客户产品的 `domain` 会作为 EYVESCLOUD 容器名称，模块会自动把不适合作为容器名的字符替换为 `-`。

## 生命周期与后台功能

| WHMCS 入口 | 行为 |
| --- | --- |
| `TestConnection` | 调用 `GET /api/v1/dashboard` 测试连通性 |
| `CreateAccount` | 创建容器（异步任务，返回前有界轮询就绪并写回主机信息） |
| `SuspendAccount` / `Off` | 停止容器 |
| `UnsuspendAccount` / `On` | 启动容器 |
| `Reboot` | 重启容器 |
| `TerminateAccount` | 删除容器 |
| `ChangePassword` | 重置 SSH 密码并写回 WHMCS |
| `ChangePackage` | 同步资源限制、流量限制与到期时间 |
| `Sync`（后台按钮「同步状态」） | 拉取容器详情写回主机表并同步到期时间 |
| `TrafficReset`（后台按钮「重置流量」） | 重置容器流量 |
| `UsageUpdate` | 定期上报 `bwusage` / `bwlimit`（单位 MB） |
| `AdminServicesTabFields` | 后台服务页展示实例状态、SSH 地址、面板入口 |
| `ServiceSingleSignOn` | 后台跳转到面板用户门户实例详情页 |

开通、同步、重装、改密后，模块会从面板拉取最新信息并写回 WHMCS 主机表：

| WHMCS 字段 | 写入内容 |
| --- | --- |
| `dedicatedip` | NAT 外网 IP（优先使用 API 公网字段，否则使用服务器 IP） |
| `username` | 固定写入 `root` |
| `password` | 面板返回的 SSH 密码（经 WHMCS 加密函数存储） |
| `domainstatus` | 面板状态为 `running` 时为 `Active`，否则为 `Suspended` |

> WHMCS 主机表没有端口字段，因此模块不会写回 SSH 端口；SSH 端口在客户区「实例信息」中展示。

## 客户区功能

客户区提供一个选项卡式单页界面，所有数据通过 AJAX 请求 `handlers/api.php` 获取：

```text
实例信息
NAT 转发
防火墙
快照
备份
ISO 挂载（仅 KVM 产品展示）
重装系统
```

顶部工具栏提供：

```text
WebSSH
VNC 控制台（仅 KVM 产品展示）
开机 / 关机 / 重启
同步状态
```

- **实例信息**：实例名称、运行状态、SSH 地址、IPv6；CPU / 内存 / 负载 / 磁盘圆环；月流量进度；CPU、内存、网络、磁盘 IO 曲线；IPv4、SSH 端口、SSH 密码、资源配置、到期时间。首次打开加载一次，支持手动刷新与自动刷新（不刷新 / 10 秒 / 1 分钟 / 5 分钟 / 10 分钟）。图表优先使用面板历史指标接口绘制，缺失时回退为前端持续采样。
- **NAT 转发**：查看/添加/修改/删除端口映射，获取随机可用端口；删除使用页面内确认弹窗。
- **防火墙**：查看启用状态、默认动作与规则列表；开关、默认动作与规则编辑先在前端暂存，点击「保存设置」后统一同步。
- **快照 / 备份**：创建、还原、删除，配额与大小展示，操作使用页面内确认弹窗。
- **ISO 挂载（KVM）**：列出可用 ISO，挂载 / 卸载到实例光驱。
- **重装系统**：选择系统模板与重装范围（完整 / 仅系统盘），确认后提交。

## 控制台（WebSSH / VNC）

点击 WebSSH / VNC 按钮时，客户区通过 AJAX 请求模块创建一次性票据，模块返回控制台页面地址，前端在新窗口打开。

- WebSSH 页面：`handlers/webssh.php`，通过 WebSocket `/api/ssh` 连接。
- WebVNC 页面：`handlers/vnc.php`，通过 WebSocket `/api/vnc` 连接，使用 noVNC（CDN 动态加载）。

两个控制台页面在服务端做了目标校验，只允许转发到可信主机，避免被当作任意内网/公网目标的反向代理（SSRF）。可信主机列表为「当前请求域名」加上环境变量：

```text
EYVESCLOUD_WS_ALLOW=panel.example.com,10.0.0.5
```

若面板地址不在当前站点域名下，请把面板主机名加入该环境变量，否则控制台会返回 403。

## 使用的面板 API

```text
GET    /api/v1/dashboard
GET    /api/v1/containers
POST   /api/v1/containers
GET    /api/v1/containers/{id|uuid|name}
DELETE /api/v1/containers/{name}/delete
POST   /api/v1/containers/{name}/start
POST   /api/v1/containers/{name}/stop
POST   /api/v1/containers/{name}/restart
POST   /api/v1/containers/{name}/reset-password
PUT    /api/v1/containers/{name}/resource-limit
PUT    /api/v1/containers/{name}/traffic-limit
POST   /api/v1/containers/{name}/traffic-reset
PUT    /api/v1/containers/{name}/expiry
GET    /api/v1/containers/{name}/usage
GET    /api/v1/containers/{name}/traffic
GET    /api/v1/containers/{id}/history
GET    /api/v1/containers/{id}/random-port
POST   /api/v1/containers/{id}/port-mappings
PUT    /api/v1/containers/{id}/port-mappings/{index}
DELETE /api/v1/containers/{id}/port-mappings/{index}
GET    /api/v1/containers/{id}/firewall
PUT    /api/v1/containers/{id}/firewall
GET    /api/v1/containers/{name}/snapshots
POST   /api/v1/containers/{name}/snapshots
POST   /api/v1/containers/{name}/snapshots/{id}/restore
DELETE /api/v1/containers/{name}/snapshots/{id}
GET    /api/v1/containers/{name}/backups
POST   /api/v1/containers/{name}/backups
POST   /api/v1/containers/{name}/backups/{id}/restore
DELETE /api/v1/containers/{name}/backups/{id}
GET    /api/v1/templates
POST   /api/v1/containers/{name}/reinstall
POST   /api/v1/ssh-ticket
POST   /api/v1/vnc-ticket
GET    /api/isos
POST   /api/isos/attach
GET    /api/ssh           (WebSocket)
GET    /api/vnc           (WebSocket)
```

## 客户区 AJAX 约定

入口：`handlers/api.php`（仅接受 POST）

请求（`application/x-www-form-urlencoded`，兼容 JSON 请求体）：

```text
service_id  服务 ID（tblhosting.id）
func        动作名
其他业务字段随请求提交（快照/备份资源 ID 使用 id）
```

响应：

```json
{ "success": true, "message": "获取成功", "data": {} }
```

该入口会自行完成：载入 WHMCS 引导文件、校验会话（客户或管理员）、同源校验、服务归属校验，然后交给 `helpers.php` 的 `eyvescloud_dispatch()` 处理。

## 故障排查

| 现象 | 排查方向 |
| --- | --- |
| 后台「测试连接」失败 | 检查面板地址、端口、Secure 与 API Key；确认服务器可访问面板 |
| 开通失败提示缺少模板 ID | 产品配置「镜像/模板 ID」未填写 |
| 开通成功但状态一直非 Active | 面板开通为异步任务，稍后在后台点击「同步状态」 |
| 客户区提示「登录状态已失效」 | WHMCS 会话过期，刷新页面重新登录 |
| 客户区提示「无权操作该服务」 | 非本人服务或管理员未登录 |
| 控制台返回 403 | 面板主机名不在可信列表，配置 `EYVESCLOUD_WS_ALLOW` |
| VNC 提示无法加载 noVNC | 浏览器无法访问 CDN，检查网络策略 |

调试日志默认关闭。如需开启，在 `helpers.php` 之前定义常量：

```php
define('EYVESCLOUD_DEBUG', true);
```

## 安全说明

- `handlers/api.php` 不信任前端传入的服务归属，一律从数据库重建参数并校验 `userid`。
- 客户区 AJAX 仅接受 POST，并做同源校验（Origin/Referer 主机一致性）。
- 控制台页面校验 WebSocket 目标主机，防止 SSRF。
- API Key 优先使用服务器 Access Hash，其次使用服务器密码；数据库读取时统一解密。
- 面板返回的密码若为 `***` 等脱敏值，模块不会覆盖 WHMCS 已有密码。