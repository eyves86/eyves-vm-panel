<p align="center">
  <img src="frontend/public/favicon.svg" width="96" alt="EyvesCloud">
</p>

<h1 align="center">EyvesCloud</h1>

<p align="center">
  <strong>开箱即用的 VPS 管理面板</strong><br/>
  <sub>在你的服务器上开出自己的"云"，卖 VPS 或者管理公司内网的机器，都行。</sub>
</p>

<p align="center">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-blue?style=flat-square">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="LXC" src="https://img.shields.io/badge/LXC-支持-111111?style=flat-square">
  <img alt="KVM" src="https://img.shields.io/badge/KVM-支持-EE0000?style=flat-square">
</p>

---

## 这是什么？

一句话：**把一台（或多台）Linux 服务器，变成一个像阿里云一样的控制台。**

装好之后你会有：

- 一个网页后台：点鼠标就能创建/管理"云服务器"（LXC 容器和 KVM 虚拟机）
- 一个用户门户：卖给客户的页面，客户自己开机/关机/重装/看流量，不用找你
- 一套 API：用来接计费系统（WHMCS 插件内置）

**打个比方**：它就是"机房里的管理大屏"——你提供服务器，它负责把服务器切成一台台小主机分给用户，谁到期了、谁流量超了、谁该停机，面板自己管。

## 能干什么？

| 你想要 | 面板做的 |
| --- | --- |
| 开一台"小主机"给客户 | 选区域→选节点→选系统，点创建，一分钟出机（自动分配 IP/端口/密码） |
| 客户自己管理 | 用户门户：开机/关机/重启/重装/看流量/连控制台（WebSSH 直连，浏览器里就是终端） |
| 到期/欠费处理 | 到期自动关机；欠费挂起后客户开不了机；误删了还能从回收站恢复 |
| 多台服务器 | 主控-被控架构：装一台主控，其余机器一键接入，全部在一个面板里管 |
| 自动收费 | 配 WHMCS/财务系统（插件内置），下单→自动开通→到期自动停机 |
| 安全 | 每一步操作都有日志；密钥加密存储；升级包带签名，装错包直接拒绝 |

## 3 步开始

**① 装主控**（一台服务器上执行）：

```bash
curl -fsSL https://raw.githubusercontent.com/eyves86/eyves-vm-panel/main/install.sh | sudo sh
```

装完终端会打印**登录地址和初始密码**，浏览器打开登录。

**② 接入更多机器**（可选）：在面板「节点管理」点一下，拿到一条命令，去另一台服务器上粘贴执行——几秒后它就出现在面板里了。

**③ 用起来**：创建实例、配 WHMCS 自动卖、给客户开账号……想怎么用都行。

<details>
<summary>常用安装参数（点开）</summary>

```bash
EYVESCLOUD_LANG=zh          # 面板语言 zh / en
EYVESCLOUD_VERSION=latest   # 固定版本，如 v2.2.33
EYVESCLOUD_REPO=github:eyves86/eyves-vm-panel   # 更新源
```

卸载：`curl -fsSL https://raw.githubusercontent.com/eyves86/eyves-vm-panel/main/install.sh | sudo sh -s -- uninstall`

</details>

## 界面长什么样？

| | |
| --- | --- |
| **登录页** | **控制台首页** |
| ![登录页](/img/screenshot-login.png) | ![控制面板](/img/screenshot-dashboard.png) |
| **实例管理** | **实例详情** |
| ![容器列表](/img/screenshot-containers.png) | ![容器详情](/img/screenshot-container-detail.png) |
| **节点管理** | **操作日志** |
| ![节点管理](/img/screenshot-nodes.png) | ![操作日志](/img/screenshot-audit.png) |

## 适合谁？

- **卖 VPS 的**：接入财务系统后全自动化，你只管收钱和处理工单
- **公司 IT**：给各部门/开发自服务开虚拟机，配额、到期、审计全都有
- **折腾的人**：自己家里几台机器统一管理，浏览器里连终端

## 技术栈（给开发者）

Go 1.25（单二进制，内嵌前端）+ PostgreSQL（配置库与遥测）+ Redis（可选，分布式限流/吊销）+ React 18 / TypeScript / Tailwind；虚拟化走 LXC 与 KVM/QEMU（libvirt）。API 见 [docs/features/api.md](docs/features/api.md)（v1 面板自用）与 [docs/API-V2.md](docs/API-V2.md)（v2 给外部集成的稳定契约，117 端点，OpenAPI 自描述）。

## 更多文档

- [安装指南](docs/guide/installation.md) · [部署文档](DEPLOYMENT.md)
- [功能手册](docs/index.md)（中英双语）
- [常见问题](docs/operations/faq.md) · [故障排查](docs/operations/troubleshooting.md)

## 免责声明

本软件不分发 Windows 镜像，也不含任何绕过 Windows 激活的机制；涉及的 Windows 下载链接均指向微软官方源，请自行向微软或其授权渠道获得许可。使用本软件产生的一切后果由使用者自行承担。
