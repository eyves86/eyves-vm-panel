# 节点管理（主控-被控）

EyvesCloud 支持「主控-被控」多节点架构：主控面板统一管理多台被控服务器，被控服务器执行一键安装脚本后自动接入，主控可直接查看和操作被控的容器。

## 工作流程

```text
主控面板                被控服务器
   │                        │
   ├─ 添加节点               │
   ├─ 生成安装脚本 ────────► 执行脚本
   │                        ├─ 下载主控二进制
   │                        ├─ 注册（安装密钥换 token）
   │                        └─ 启动 Agent + systemd 自启
   │  ◄────── 心跳上报（10s）──┤
   ├─ 节点变“在线”            │
   ├─ 查看被控容器 ─────────► /api/agent/*（token 鉴权）
   └─ 操作被控容器（开关机/重启）
```

## 添加节点

1. 进入「节点管理」，点击「添加节点」。
2. 填写节点名称（可选，留空自动生成）和被控面板地址（可选，留空时由安装脚本第二个参数指定）。
3. 创建成功后，节点状态为「待接入」。

## 一键安装脚本

点击节点的「安装脚本」，会生成一段 shell 脚本。在被控服务器上以 root 执行即可：

```bash
bash eyvescloud-agent-node1.sh
```

脚本会自动完成：

1. 从主控下载二进制（`GET /api/nodes/binary`，与主控同一份 `eyvescloud`）。
2. 使用安装密钥注册到主控（`POST /api/nodes/register`），换取被控节点 token。
3. 以 `eyvescloud agent` 模式启动本地面板，并写入 systemd 服务实现开机自启。

脚本也支持参数覆盖：`bash eyvescloud-agent-node1.sh [节点名称] [被控面板地址]`。

### 手动注册

也可以在被控服务器上手动执行：

```bash
eyvescloud agent \
  --controller=http://MASTER_IP:8999 \
  --install-key=安装密钥 \
  --name=node-1 \
  --addr=http://被控IP:8999
```

注册成功后，配置保存在被控的 `agent.json`，之后重启无需再传安装密钥（除非更换主控）。

## 心跳与状态

被控每 10 秒上报一次心跳，主控据此更新：

- 节点状态（在线/离线/待接入）。
- 版本号、操作系统。
- CPU 核数、内存用量、磁盘用量。
- 容器数量。
- **容器清单增量同步**：心跳携带被控全部容器的轻量摘要（UUID/名称/状态/资源/到期时间/流量累计/实时指标），主控按 UUID 增量合并——被控新建的容器自动出现在主控容器列表，被控已删除的容器标记 `orphaned`，实时指标（CPU/内存/网络/磁盘速率）写入主控指标历史，监控页与容器详情页跨节点数据与本机一致。

超过心跳间隔未上报的节点会显示为「离线」。主控另有主动探活循环（每 30 秒 HTTP 探测，连续 3 次失败判定离线并告警）。

## 查看与操作被控容器

节点状态为「在线」且配置了面板地址时，主控可以：

- 查看被控的容器列表（名称、模板、资源、状态、IP）——心跳同步后与主控本机容器在统一列表展示，并显示来源节点。
- 对被控容器执行**全生命周期操作**：开机、关机、重启、删除、重装系统、挂起/恢复、重置密码、磁盘扩容、快照创建/恢复/删除、实时用量查询。

这些操作由主控以被控节点 token 代理调用被控的 `/api/agent/*` 接口完成，被控会校验 token，未接入的节点无法被访问。

### 控制台级联（WebSSH / VNC）

跨节点容器同样支持 Web 终端与 VNC 控制台：主控作为透明 WebSocket 中继（浏览器 ↔ 主控 ↔ 被控），浏览器侧票据与子协议流程与本机容器完全一致，前端无需任何改动。链路：

```text
浏览器 ──WS── 主控 /api/ssh|/api/vnc ──WS── 被控 /api/ssh|/api/vnc（本机容器）
```

主控先凭节点 token 向被控申请一次性控制台票据（`/api/agent/{ssh,vnc}-ticket`，60 秒有效），再以 WS 客户端身份拨被控。挂起状态的容器在票据发行与连接两层均被拦截。

### 被控本机自治

被控 Agent 与主控运行对称的本机运维循环，即使与主控断连也能自治：

- 到期/超流量停机扫描（每 30 秒）。
- 用量采集（CPU/网络/磁盘速率，每 5 秒）与流量累计。
- 计划快照调度。
- LXC/KVM 桥接网络自愈（网关 IP + DHCP + 转发/NAT）。

### 在主控开通被控容器（发机）

主控可以在被控上创建新容器（发机）：「节点管理 → 节点详情 → 开通容器」，代理调用被控的 `/api/agent/containers/create`，复用被控本地的 LXC/KVM 创建逻辑。节点不在线或未配置面板地址时会被拒绝。

### 同步镜像

被控会自动/手动同步主控的自定义镜像清单（`GET /api/nodes/{id}/images`）与镜像文件（`POST /api/nodes/{id}/images/sync`）。主控将自身 LXC/KVM 自定义镜像清单下发，被控对比 SHA256 后拉取缺失或已变更的镜像文件，保证被控上可用镜像与主控一致。

### 被控节点自动更新

被控 Agent 可在后台自动检查并更新自身二进制。通过环境变量 `EYVESCLOUD_AUTO_UPDATE` 以「分钟」为单位设定检查间隔，仅在 `>= 60` 时才会启用（避免频繁请求 GitHub）。一键安装脚本会通过该变量传入，默认 `1440`（每天一次）。检查到新版本并更新成功后，服务会自动重启生效。

### 节点级冷备份

主控可对被控执行「节点冷备份」（`POST /api/nodes/{id}/backup`）：被控对全部 LXC/KVM 容器逐个创建完整磁盘备份（写入被控 `instance-backups` 目录）。用于节点重装/迁移前的数据保全，备份内容包括容器磁盘、元数据与 keep-N 保留策略。

## 删除节点

删除节点会从主控移除该节点记录。若被控的 systemd 服务仍在运行，它会继续尝试上报心跳，主控将不再显示。如需彻底吊销，请同时在被控上卸载服务：

```bash
systemctl disable --now eyvescloud-agent
```

## 安全说明

- 安装密钥为 64 位随机十六进制，仅用于首次注册换取 token。
- 节点 token 用于主控 ↔ 被控之间的接口鉴权，请勿外泄。
- 主控通过 HTTPS 反向代理时，请确保被控也能访问主控地址。
- 被控面板地址建议使用其公网可访问地址，主控才能代理访问。

## 相关接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/nodes` | 列出节点（管理员） |
| POST | `/api/nodes` | 创建节点（管理员） |
| DELETE | `/api/nodes/{id}` | 删除节点（管理员） |
| GET | `/api/nodes/{id}/install-script` | 获取一键安装脚本（管理员） |
| GET | `/api/nodes/binary` | 下载主控二进制（安装脚本使用） |
| POST | `/api/nodes/register` | 被控注册（安装密钥） |
| POST | `/api/nodes/{id}/heartbeat` | 被控心跳（节点 token） |
| GET | `/api/nodes/{id}/containers` | 代理查看被控容器（管理员） |
| POST | `/api/nodes/{id}/containers` | 主控在被控开通容器（发机） |
| POST | `/api/nodes/{id}/containers/{cid}/{action}` | 代理操作被控容器（管理员） |
| GET | `/api/nodes/{id}/images` | 代理查看被控镜像清单 |
| POST | `/api/nodes/{id}/images/sync` | 下发主控镜像清单并触发被控同步 |
| POST | `/api/nodes/{id}/backup` | 节点级冷备份（被控全量容器备份） |
| GET | `/api/agent/containers` | 被控容器列表（主控 token） |
| POST | `/api/agent/containers/{cid}/{action}` | 被控容器操作（主控 token）。action：`start`/`stop`/`restart`/`destroy`/`reinstall`/`suspend`/`unsuspend`/`reset-password`/`usage`/`resize`/`snapshot`/`snapshots/delete`/`snapshots/restore` |
| POST | `/api/agent/ssh-ticket` | 发行被控 WebSSH 一次性票据（主控 token，级联拨号用） |
| POST | `/api/agent/vnc-ticket` | 发行被控 VNC 一次性票据（主控 token，级联拨号用） |

### 主控统一容器 API（跨节点自动路由）

对容器的标准操作无需感知节点位置——`Container.node_id` 非空时主控自动转发到所属被控：

| 方法 | 路径 | 跨节点行为 |
| --- | --- | --- |
| POST | `/api/containers/{id}/start|stop|restart` | 转发被控执行 |
| POST | `/api/containers/{id}/reinstall` | 转发被控执行 |
| POST | `/api/containers/{id}/suspend|unsuspend` | 转发被控执行 |
| DELETE | `/api/containers/{id}` | 转发被控销毁 |
| POST | `/api/containers/{id}/reset-password` | 转发被控执行 |
| GET | `/api/containers/{id}/usage` | 被控本机计算实时用量并返回 |
| GET | `/api/containers/{id}/history` | 心跳聚合的指标历史（主控本地） |
| GET | `/api/containers/{id}/traffic` | 心跳同步的流量累计（主控本地） |
| WS | `/api/ssh`、`/api/vnc` | 主控透明中继到被控控制台 |
