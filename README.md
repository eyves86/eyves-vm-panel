<p align="center">
  <img src="frontend/public/favicon.svg" width="96" alt="EyvesCloud">
</p>

<h1 align="center">EyvesCloud</h1>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=111111">
  <img alt="TypeScript" src="https://img.shields.io/badge/TypeScript-5-3178C6?style=flat-square&logo=typescript&logoColor=white">
  <img alt="Vite" src="https://img.shields.io/badge/Vite-5-646CFF?style=flat-square&logo=vite&logoColor=white">
  <img alt="Tailwind CSS" src="https://img.shields.io/badge/Tailwind_CSS-3-06B6D4?style=flat-square&logo=tailwindcss&logoColor=white">
  <img alt="LXC" src="https://img.shields.io/badge/LXC-Supported-111111?style=flat-square">
  <img alt="KVM" src="https://img.shields.io/badge/KVM-Supported-EE0000?style=flat-square">
  <img alt="SQLite" src="https://img.shields.io/badge/SQLite-Embedded-003B57?style=flat-square">
</p>

<p align="center">
  <img alt="WebSSH" src="https://img.shields.io/badge/WebSSH-Built--in-009688?style=flat-square">
  <img alt="VNC" src="https://img.shields.io/badge/VNC-Supported-7B1FA2?style=flat-square">
  <img alt="IPv6" src="https://img.shields.io/badge/IPv6-Native-1976D2?style=flat-square">
  <img alt="NAT" src="https://img.shields.io/badge/NAT-Port_Forwarding-FF9800?style=flat-square">
  <img alt="REST API" src="https://img.shields.io/badge/API-REST-4CAF50?style=flat-square">
  <img alt="Multi User" src="https://img.shields.io/badge/Multi_User-Supported-8E24AA?style=flat-square">
  <img alt="Traffic Control" src="https://img.shields.io/badge/Traffic-Control-795548?style=flat-square">
  <img alt="Security Alert" src="https://img.shields.io/badge/Security-Alert-orange?style=flat-square">
  <img alt="CLI" src="https://img.shields.io/badge/CLI-Mode-424242?style=flat-square">
  <img alt="TLS" src="https://img.shields.io/badge/TLS-Let's_Encrypt-003A70?style=flat-square&logo=letsencrypt&logoColor=white">
</p>

**EyvesCloud**（项目仓库 `eyves-vm-panel`）是一个面向 LXC / KVM 的轻量虚拟化管理面板。它将常见的宿主机运维动作收敛到 Web 控制台与命令行中，并额外提供「主控-被控」多节点、REST API、NAT/IPv6 网络、WebSSH/WebVNC、资源配额、流量限制、快照、安全告警和子用户授权等能力，适合 VPS 商家、实验室、开发者自建虚拟化节点，以及需要批量开通和分发容器访问权限的场景。

EyvesCloud is a lightweight virtualization management panel for LXC and KVM. It consolidates common host administration tasks into a web console and a CLI, and adds controller-agent multi-node management, a versioned REST API, NAT/IPv6 networking, WebSSH/WebVNC access, resource quotas, traffic limits, snapshots, security alerting, and delegated sub-user access.

![EyvesCloud 系统架构 / Architecture](/img/architecture.svg)

---

## 功能 / Features

| 模块 | 能力 |
| --- | --- |
| 虚拟化管理 | 在同一个面板里管理 LXC 容器和 KVM 虚拟机，支持创建、重装、开机、关机、重启、删除、重置密码、到期时间和批量操作。 |
| 主控-被控节点 | 在主控面板无限添加被控节点，生成一键安装脚本；被控接入后主控可直接查看并操作被控容器。 |
| 镜像与模板 | 内置 Ubuntu / Debian / Alpine / CentOS / Arch Linux / Fedora / Rocky Linux 等模板管理，支持启用、禁用、下载、取消下载、清理缓存与自定义镜像。 |
| 网络能力 | NAT4 端口配额、随机可用端口、TCP/UDP 端口映射、公网 IPv4 池、IPv6 前缀检测、IPv6 状态检查与容器级 IPv6 分配。 |
| 资源控制 | CPU、内存、磁盘、Swap、独立上下行带宽、读写 I/O 限速、流量重置与流量限制；到期或超额自动关机。 |
| 远程控制 | 内置 WebSSH / WebVNC 票据访问，浏览器直接打开终端或控制台，无需手动交换连接信息。 |
| 快照 | 快照总览、创建 / 删除 / 恢复、计划快照与快照配额。 |
| 安全告警 | 基于 conntrack 检测端口扫描、横向扫描、爆破倾向、SMTP 滥用、UDP 反射、挖矿端口、代理 / VPN / Tor 与 ARP 欺骗。 |
| 账号与审计 | 子用户管理链接、密码轮换、按容器授权、操作日志、登录日志与 API Key 管理。 |
| 自动化 | 全量接口统一 `/api/v1`，覆盖容器、镜像、网络、流量、安全、任务队列与批量操作；提供财务计费系统对接模块。 |
| 运维入口 | Dashboard 统计、主机资源、路由概览、Swap 管理、CLI-only 模式与自动发布产物。 |

| Area | What EyvesCloud provides |
| --- | --- |
| Virtualization | Manage LXC containers and KVM virtual machines from one panel: create, reinstall, start, stop, restart, delete, password reset, expiry control, and batch actions. |
| Controller-Agent nodes | Add unlimited worker nodes from the controller panel, get a one-click install script, then view and operate worker containers from the controller. |
| Images & templates | Built-in template/image management for Ubuntu, Debian, Alpine, CentOS, Arch, Fedora, Rocky Linux and more; enable, disable, download, cancel or purge cache. |
| Networking | NAT4 port quotas, random available ports, TCP/UDP port mappings, public IPv4 pool, IPv6 prefix detection/status, and per-container IPv6 assignment. |
| Resource control | CPU, memory, disk, swap, independent up/down bandwidth, R/W I/O rate limits, traffic reset and limits; auto-shutdown on expiry or quota breach. |
| Console access | Browser-based WebSSH and WebVNC ticket access without manually exchanging credentials. |
| Snapshots | Overview, per-container snapshots, create/delete/restore, scheduled snapshots, and quota controls. |
| Security | Conntrack-based alerts for port scans, lateral scans, brute-force, SMTP abuse, UDP reflection, mining ports, proxy/VPN/Tor and ARP spoofing. |
| Accounts & audit | Delegated sub-user links, password rotation, per-container permissions, audit logs, login logs, and API key management. |
| Automation | Versioned REST API under `/api/v1`, task queue, batch create/action, and a billing-system integration module. |
| Operations | Dashboard stats, host resource overview, routing overview, swap management, CLI-only mode, and CI-built release artifacts. |

## 技术栈 / Technology Stack

- 后端 / Backend：Go (`net/http`)，SQLite，LXC，KVM/libvirt，cgroup v2，iptables，conntrack
- 前端 / Frontend：React 18，TypeScript 5，Vite 5，Tailwind CSS，lucide-react，xterm.js，noVNC
- 运行时 / Runtime：Linux，systemd / OpenRC，LXC，KVM/QEMU
- 构建 / Build：GitHub Actions，Node.js 20，Go 1.25

## 安装 / Installation

一键安装（脚本默认从本仓库 Release 拉取发行版，也可通过 `EYVESCLOUD_REPO` 指定其它来源）：

```bash
# 安装 / Install
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh

# 卸载 / Uninstall（非交互：EYVESCLOUD_UNINSTALL_CONFIRM=1）
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh -s -- uninstall
```

常用环境变量：

```bash
EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel   # 发行版来源仓库（推荐显式指定）
EYVESCLOUD_VERSION=latest            # 或 v1.x.x 固定版本
EYVESCLOUD_LANG=zh|en                # 面板语言
EYVESCLOUD_LXC_SUBNET=10.0.3.0/24    # 手动指定 LXC NAT 网段
EYVESCLOUD_KVM_SUBNET=192.168.122.0/24
```

更多说明见 [docs/guide/installation.md](docs/guide/installation.md) 与 [DEPLOYMENT.md](DEPLOYMENT.md)。

## 界面截图 / Screenshots

> 以下均来自真实运行的 EyvesCloud Web 控制台（含 Docker/沙箱演示环境），点击可放大。更多运行细节可自行部署后体验。

| | |
| --- | --- |
| **登录页 / Login** | **控制面板 / Dashboard** |
| ![登录页](/img/screenshot-login.png) | ![控制面板](/img/screenshot-dashboard.png) |
| **容器列表 / Containers** | **容器详情 / Container Detail** |
| ![容器列表](/img/screenshot-containers.png) | ![容器详情](/img/screenshot-container-detail.png) |
| **节点管理 / Nodes** | **操作日志 / Audit Logs** |
| ![节点管理](/img/screenshot-nodes.png) | ![操作日志](/img/screenshot-audit.png) |

架构与数据流见文首的 [系统架构图](/img/architecture.svg)。

## 文档 / Documentation

- [部署文档（DEPLOYMENT.md）](DEPLOYMENT.md)
- [API 开发文档](docs/features/api.md)：`/api/v1` 全部接口、认证、字段表、返回样例与 Python 示例
- [文档站（VitePress）](docs/index.md)：安装 / 功能 / 运维 / 开发（中英双语）
- [构建流程](docs/developer/build.md) / [发布流程](docs/developer/release.md)

## 项目状态 / Status

- 构建：`go build` + `npm run build` 双端通过；GitHub Actions 自动构建 `Linux amd64/arm64` 产物并发布 Release（自动生成于 `v*` 版本标签，见 `.github/workflows`）。
- 文档站：VitePress 静态站，`main` 分支推送后自动部署到 GitHub Pages（见 `.github/workflows/docs.yml`）。
- 安全审计记录见 [docs/AUDIT.md](docs/AUDIT.md)。

## 免责声明 / Disclaimer

本开源软件不提供任何 Windows 操作系统镜像的分发服务，也不包含任何绕过、破解或免除 Windows 激活机制的功能。

软件内涉及的 Windows 系统下载链接均由微软官方提供。使用者在下载、安装和使用相关 Windows 系统时，应自行向微软或其授权渠道购买并获得相应的软件许可。本项目不会对安装后的 Windows 系统进行任何形式的激活绕过、破解或免激活处理。

对于使用者因使用本软件而产生的任何行为及其后果，包括但不限于软件许可、系统使用、数据丢失、法律责任或其他相关问题，本项目及其开发者不承担任何责任。

本开源软件仅供学习和研究 LXC、KVM 等虚拟化技术原理之目的使用，不得用于任何违反适用法律法规、软件许可协议或第三方权益的行为。本软件中涉及的 Windows 名称、标识、图标及相关知识产权均归 Microsoft Corporation 及其权利人所有；本项目与微软公司不存在任何关联、授权或合作关系。

This open-source software does not distribute Windows system images, nor does it provide any means to bypass or circumvent Windows activation mechanisms. All download links point to resources officially supplied by Microsoft. Users are responsible for obtaining appropriate licenses before use. This project is intended solely for educational purposes — learning the principles of LXC and KVM. Windows branding, marks, and icons belong to Microsoft Corporation.

## 鸣谢 / Thanks

- [Nodeseek.com](https://www.nodeseek.com) — 一个专注于服务器的社区
- [Linux.do](https://linux.do) — 一个充满灵感的科技社区