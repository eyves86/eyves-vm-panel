<p align="center">
  <img src="frontend/public/favicon.svg" width="96" alt="EyvesCloud">
</p>

<h1 align="center">EyvesCloud</h1>

<p align="center">
  <strong>企业级多节点虚拟化云管理平台 · Enterprise Multi-Node Virtualization Platform</strong><br/>
  <sub>面向 LXC / KVM 混合工作负载的开箱即用基础设施管理方案</sub>
</p>

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

---

## 产品定位 / Product Positioning

**EyvesCloud** 将分散的宿主机运维动作收敛为统一控制平面：以 Web 控制台、CLI 与版本化 REST API 三种形态，对跨节点的 LXC 容器与 KVM 虚拟机实施全生命周期管理。平台内置调度引擎、多租户隔离、计量对接与安全审计能力，帮助 VPS 服务商、企业基础设施团队与研究实验室以极低的运维成本交付自服务平台（Self-Service Portal）。

EyvesCloud unifies host administration into a single control plane — delivering full lifecycle management of LXC containers and KVM virtual machines across nodes via a web console, a CLI, and a versioned REST API. With a built-in scheduler, multi-tenant isolation, metering integration, and security auditing, it enables VPS providers, enterprise infra teams, and labs to run self-service portals at minimal operational cost.

---

## 核心价值 / Why EyvesCloud

| | |
| --- | --- |
| **统一控制平面** | 单一主控纳管无限被控节点，控制台 / CLI / API 三端一致，跨节点操作无需逐台登录。 |
| **开箱即用的交付速度** | 一键安装脚本 + 一键被控接入，5 分钟内完成单节点到多节点集群的搭建。 |
| **企业级多租户** | 子用户（operator / viewer 角色）按容器授权、租户配额、独立面板入口、会话与令牌版本控制。 |
| **计量与财务就绪** | 全量用量导出 API（资源配置 + 实时用量 + 到期/流量），配合 `usage:read` scope 的 API Key 即可对接计费系统。 |
| **安全纵深** | JWT 双 Issuer/Audience 校验、scope 细粒度授权、conntrack 威胁检测、全量操作审计与登录日志。 |
| **平滑运维** | 节点维护模式（drain/evacuate）、主动探活、面板内自升级（可选仓库与目标版本）、快照与到期自动回收。 |

---

## 产品能力矩阵 / Capability Matrix

### 计算与编排 / Compute & Orchestration

- **统一工作负载管理**：同一控制台管理 LXC 容器与 KVM 虚拟机 —— 创建、重装、电源控制、删除、密码重置、到期策略与批量操作。
- **智能调度引擎**：过滤（在线 / 容量 / 存储后端 / 维护模式）→ 评分（RAM 60% + Disk 40% + 租户分散度）→ 决策留痕，支持诊断信息输出。
- **主控-被控架构**：主控一键生成被控安装脚本，Agent 注册后自动心跳接入，主控可直查并操作被控工作负载。
- **生命周期治理**：挂起/恢复（suspend）阻断启动与远程访问；到期或流量超额自动关机。

### 网络与存储 / Networking & Storage

- **NAT 网关**：NAT4 端口配额、随机可用端口分配、TCP/UDP 端口映射。
- **公网地址池**：公网 IPv4 池管理与容器级分配。
- **原生 IPv6**：前缀检测、状态巡检与容器级 IPv6 地址分配。
- **存储后端**：dir / ZFS / LVM / RBD / CephFS / NFS 多后端支持，按节点能力调度。

### 资源治理 / Resource Governance

- **配额体系**：CPU、内存、磁盘、Swap、独立上下行带宽、读写 I/O 限速。
- **流量管理**：流量重置、流量限制、超额自动处置。
- **快照策略**：总览、创建 / 删除 / 恢复、计划快照与配额控制。

### 安全与合规 / Security & Compliance

- **威胁检测**：基于 conntrack 的端口扫描、横向移动、爆破、SMTP 滥用、UDP 反射、挖矿端口、代理 / VPN / Tor 与 ARP 欺骗告警。
- **访问控制**：JWT 签发与校验（Issuer/Audience 绑定）、API Key scope 细粒度授权、容器绑定型密钥。
- **审计追溯**：全量操作日志（含 actor / IP / UA）、登录日志、面板访问策略与 WebSSH 来源白名单。
- **传输安全**：内置 Let's Encrypt 自动签发与续期。

### 计量与集成 / Metering & Integration

- **版本化 API**：全量接口统一 `/api/v1`，覆盖容器、镜像、网络、流量、安全、任务队列与批量操作。
- **财务对接**：`GET /api/v1/usage` 全量用量导出（租户过滤、配置 + 实时用量聚合），专为计费系统插件化对接设计。
- **幂等开通**：`Idempotency-Key` 机制防止计费回调超时导致的双开。
- **WHMCS 插件**：内置 WHMCS 9.0 服务器开通模块（LXC/KVM），管理员可在后台「API 集成」页一键下载 zip，解压到 WHMCS 根目录即可安装，计费仍由 WHMCS 负责。
- **运维入口**：Dashboard 统计、主机资源、路由概览、CLI-only 模式。

---

## 典型场景 / Use Cases

| 场景 | 说明 |
| --- | --- |
| **VPS 服务商** | 主控纳管多机房节点，子用户自助管理名下实例，用量 API 对接财务计费，实现开通-计量-停复机闭环。 |
| **企业基础设施团队** | 多租户隔离 + 角色授权 + 全量审计，为开发/测试团队交付自服务平台，安全策略集中管控。 |
| **实验室与教育机构** | 批量开通 + 模板管理 + 到期自动回收，低成本支撑批量实验环境。 |
| **开发者自托管** | 单节点 5 分钟部署，WebSSH/WebVNC 浏览器直连，无需额外跳板机。 |

---

## 架构 / Architecture

![EyvesCloud 系统架构 / Architecture](/img/architecture.svg)

**技术栈 / Stack**

- **控制面**：Go（net/http）、SQLite、调度引擎
- **虚拟化层**：LXC、KVM/QEMU（libvirt）、cgroup v2、iptables、conntrack
- **数据面**：React 18、TypeScript 5、Vite 5、Tailwind CSS、xterm.js、noVNC
- **交付**：Linux（systemd / OpenRC）、GitHub Actions 自动构建 Linux amd64/arm64

---

## 快速开始 / Quick Start

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

---

## 界面 / Product Tour

> 以下均来自真实运行的 EyvesCloud Web 控制台（含 Docker/沙箱演示环境），点击可放大。

| | |
| --- | --- |
| **登录页 / Login** | **控制面板 / Dashboard** |
| ![登录页](/img/screenshot-login.png) | ![控制面板](/img/screenshot-dashboard.png) |
| **容器列表 / Containers** | **容器详情 / Container Detail** |
| ![容器列表](/img/screenshot-containers.png) | ![容器详情](/img/screenshot-container-detail.png) |
| **节点管理 / Nodes** | **操作日志 / Audit Logs** |
| ![节点管理](/img/screenshot-nodes.png) | ![操作日志](/img/screenshot-audit.png) |

---

## 文档 / Documentation

- [部署文档（DEPLOYMENT.md）](DEPLOYMENT.md)
- [API 开发文档](docs/features/api.md)：`/api/v1` 全部接口、认证、字段表、返回样例与 Python 示例
- [文档站（VitePress）](docs/index.md)：安装 / 功能 / 运维 / 开发（中英双语）
- [构建流程](docs/developer/build.md) / [发布流程](docs/developer/release.md)

## 项目状态 / Status

- 构建：`go build` + `npm run build` 双端通过；GitHub Actions 自动构建 `Linux amd64/arm64` 产物并发布 Release（自动生成于 `v*` 版本标签，见 `.github/workflows`）。
- 文档站：VitePress 静态站，`main` 分支推送后自动部署到 GitHub Pages（见 `.github/workflows/docs.yml`）。
- 安全审计记录见 [docs/AUDIT.md](docs/AUDIT.md)。

---

## 免责声明 / Disclaimer

本开源软件不提供任何 Windows 操作系统镜像的分发服务，也不包含任何绕过、破解或免除 Windows 激活机制的功能。

软件内涉及的 Windows 系统下载链接均由微软官方提供。使用者在下载、安装和使用相关 Windows 系统时，应自行向微软或其授权渠道购买并获得相应的软件许可。本项目不会对安装后的 Windows 系统进行任何形式的激活绕过、破解或免激活处理。

对于使用者因使用本软件而产生的任何行为及其后果，包括但不限于软件许可、系统使用、数据丢失、法律责任或其他相关问题，本项目及其开发者不承担任何责任。

本开源软件仅供学习和研究 LXC、KVM 等虚拟化技术原理之目的使用，不得用于任何违反适用法律法规、软件许可协议或第三方权益的行为。本软件中涉及的 Windows 名称、标识、图标及相关知识产权均归 Microsoft Corporation 及其权利人所有；本项目与微软公司不存在任何关联、授权或合作关系。

This open-source software does not distribute Windows system images, nor does it provide any means to bypass or circumvent Windows activation mechanisms. All download links point to resources officially supplied by Microsoft. Users are responsible for obtaining appropriate licenses before use. This project is intended solely for educational purposes — learning the principles of LXC and KVM. Windows branding, marks, and icons belong to Microsoft Corporation.

## 鸣谢 / Thanks

- [Nodeseek.com](https://www.nodeseek.com) — 一个专注于服务器的社区
- [Linux.do](https://linux.do) — 一个充满灵感的科技社区
