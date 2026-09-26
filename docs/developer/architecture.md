# 系统架构

EyvesCloud 由 Go 后端、React 前端和宿主机虚拟化能力组成，并支持「主控-被控」多节点部署。

## 后端

后端入口在 `backend/main.go`，HTTP 服务路由集中在 `backend/internal/server/server.go`。主要模块：

- `internal/api`：Web 面板和 `/api/v1` 的 HTTP 接口（含主控节点 API 与被控 Agent API）。
- `internal/config`：配置和 SQLite 存储。
- `internal/agent`：被控节点 Agent 模式（注册、心跳、本地面板）。
- `internal/lxc`：LXC 容器管理。
- `internal/kvm`：KVM/libvirt 虚拟机管理。
- `internal/cli`：命令行管理入口。
- `internal/server`：静态前端嵌入和 HTTP 服务。
- `internal/safehttp`：HTTP 相关安全工具。
- `internal/version`：版本号。

## 主控-被控架构

同一份二进制通过启动参数区分角色：

```text
主控（Controller）              被控（Agent）
eyvescloud server                    eyvescloud agent --controller=... --install-key=...
     │                                │
     ├─ /api/nodes (CRUD)             ├─ 注册: POST /api/nodes/register
     ├─ /api/nodes/{id}/install-script├─ 心跳: POST /api/nodes/{id}/heartbeat
     ├─ /api/nodes/binary             ├─ 本地面板: server.Run()
     └─ 代理: /api/nodes/{id}/containers ──► /api/agent/*（节点 token 鉴权）
```

- **主控**：`/api/nodes` 相关路由负责节点 CRUD、安装脚本生成、二进制下载、心跳接收和容器代理。
- **被控**：`internal/agent` 负责注册与心跳；`internal/api` 中的 `agent_api.go` 提供 `/api/agent/*` 接口，由主控通过节点 token 调用。
- **鉴权**：主控侧使用管理员 JWT / API Key scope（`node:read`、`node:write`）；被控侧使用节点 token 校验主控身份。

### 注册流程

1. 主控创建节点，生成 `install_key` 和 `token`。
2. 被控执行安装脚本，`POST /api/nodes/register` 携带安装密钥。
3. 主控按安装密钥匹配节点，返回 `node_id` 与 `token`。
4. 被控将注册信息保存到 `agent.json`，之后以节点 token 上报心跳。
5. 主控更新节点状态为在线，并可通过代理接口访问被控容器。

### 心跳

被控每 10 秒上报一次心跳，携带版本、OS、CPU、内存、磁盘和容器数量。主控写入节点记录并更新 `last_seen`。

### 代理访问

主控收到 `/api/nodes/{id}/containers` 请求后，以节点 token 向被控的 `node.Address` 发起 `/api/agent/*` 请求，并把响应原样返回给浏览器。

## 前端

前端入口在 `frontend/src/main.tsx`，页面位于 `frontend/src/pages`，通用组件位于 `frontend/src/components`。

主要页面：

- 控制面板：`Dashboard.tsx`
- 容器列表：`Containers.tsx`
- 容器详情：`ContainerDetail.tsx`
- 镜像管理：`ImageManagement.tsx`
- 节点管理：`NodeManagement.tsx`（主控-被控）
- 节点迁移：`NodeMigration.tsx`
- 策略管理：`PolicyManagement.tsx`
- 存储管理：`Storage.tsx`
- 安全告警：`Security.tsx`
- 快照管理：`Snapshots.tsx`
- 路由管理：`Routing.tsx`
- 审计日志：`AuditLogs.tsx`
- API 集成：`ApiIntegration.tsx`
- 主机报告：`HostReport.tsx`
- 系统设置：`Settings.tsx`
- 子用户管理：`SubUserManagement.tsx`

## 前端嵌入

生产构建时，前端产物会放入 `backend/internal/server/web`，后端通过 Go embed 提供静态文件，并对非 API 路由返回 SPA 入口。

## 接口分层

- `/api/*`：Web 面板和兼容接口（含 `/api/nodes` 主控节点接口、`/api/agent/*` 被控接口）。
- `/api/v1/*`：推荐给外部自动化系统使用的版本化接口。
- WebSSH 和 WebVNC 使用短期票据后建立 WebSocket 连接。

## 数据存储

配置与业务数据存储在 SQLite（`config.db`）。容器、子用户、API Key、审计日志、策略、节点等均持久化到 SQLite，重启不丢失。被控节点注册信息（`agent.json`）保存在被控自身的配置目录。
