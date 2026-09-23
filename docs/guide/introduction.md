# 项目介绍

EyvesCloud 是一个面向 LXC/KVM 的轻量虚拟化管理面板。它把常见宿主机运维动作收敛到 Web 控制台和命令行里，并额外提供「主控-被控」多节点管理能力，适合 VPS 商家、实验室、开发者自建虚拟化节点以及需要批量分发容器访问权限的场景。

## 核心能力

- 管理 LXC 容器和 KVM 虚拟机，支持创建、开机、关机、重启、重装、删除、重置密码、批量操作。
- 主控-被控多节点：主控生成一键安装脚本，被控服务器执行后自动注册接入，主控可直接查看/操作被控的容器。
- 配置 CPU、内存、磁盘、Swap、独立上下行带宽、读写 I/O 限速、流量限制和到期时间，支持超额自动关机。
- 管理 NAT4 端口映射，并在宿主机具备 IPv6 路由时分配公网 IPv6。
- 在浏览器中打开 WebSSH 或 WebVNC。
- 管理镜像下载、启用状态和本地缓存。
- 创建、恢复、删除快照，配置计划快照和快照配额。
- 存储池管理、资源配额和策略引擎，支持租户隔离与按容器授权。
- 基于连接行为生成安全告警，并保留审计日志和登录日志。
- 为指定容器创建子用户访问链接，支持查看/操作范围限制。
- 通过 API Key 接入 `/api/v1` 自动化接口。

## 适用场景

- 一台宿主机上需要快速分配多个 Linux 容器。
- 需要把多台独立服务器统一接入一个主控面板统一管理（主控-被控节点模式）。
- 需要给用户临时发放容器控制台、SSH、VNC 或 NAT 端口管理权限。
- 希望用 API 自动化创建容器、调整资源、重置密码或回收资源。
- 需要一个比纯 CLI 更直观，但又不重型的平台面板。

## 技术栈

- 后端：Go、`net/http`、SQLite、systemd、LXC、KVM/libvirt、cgroup v2、iptables、conntrack。
- 前端：React、TypeScript、Vite、Tailwind CSS、lucide-react、xterm.js、noVNC。
- 发布：GitHub Actions 构建 Linux AMD64/ARM64 release 产物，安装脚本默认拉取最新 Release。

## 原创性说明

EyvesCloud 是**独立编写**的开源项目：后端（Go）、前端（React/TypeScript）、被控 Agent、主控-被控代理协议、REST API、策略引擎与安全引擎均为本仓库从零实现，不存在对任何既有面板的源代码复用或复制粘贴。

需要澄清的几点：

- **命名与标识**：产品名 "EyvesCloud" 为本项目自有命名，与任何第三方产品或商标无关。
- **可借鉴但不抄源码**：项目在「主控-被控节点」「NAT/IPv6 网络」「WebSSH/WebVNC」等常见场景的**功能形态**上与部分同类产品相似，这是行业通用需求，并非代码抄袭。其**实现方式**（[架构文档](../developer/architecture.md)）为自主设计，包括 SQLite 持久化模型、JWT + API Key（argon2id）鉴权体系、节点 token 代理协议、基于 TOTP 的两步验证、策略引擎与基于 conntrack 的安全引擎等均有单独落地。
- **依赖的第三方组件**：仅使用 Go 标准库/知名开源库（如 `golang.org/x/crypto`、modernc.org/sqlite）、React 生态与 Linux 系统组件（LXC、libvirt、iptables），各自遵循其开源许可。
- **文档**：本文档站与 [README](../../README.md)、[部署文档](../../DEPLOYMENT.md) 均为本项目原创撰写。

如果你在某处看到与本项目文字或代码高度雷同的内容，欢迎提交 Issue 反馈以便区分或移除。
