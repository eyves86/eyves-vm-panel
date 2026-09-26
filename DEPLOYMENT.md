# EyvesCloud 部署文档

EyvesCloud 是一个面向 LXC / KVM 的轻量虚拟化管理面板，提供自动化配置、节点迁移、精细权限、租户隔离、ARP 防护、Cloud-init、API 开放接口、CPU/带宽策略、监控告警、详细日志、静态 IP 与多维统计等能力，适合 VPS 商家、实验室、开发者自建虚拟化节点以及需要批量开通容器的场景。

---

## 一、环境要求

| 项目 | 要求 |
| --- | --- |
| 操作系统 | Debian / Ubuntu / CentOS / Rocky Linux / AlmaLinux / Fedora / Alpine，需 systemd 或 OpenRC |
| 架构 | x86_64 / amd64、aarch64 / arm64 |
| 磁盘 | 根分区建议至少 5GB 可用空间（镜像下载与创建容器/虚拟机需要） |
| 内核 | Linux 4.18+，建议开启 IPv4/IPv6 转发；KVM 需 `/dev/kvm`（支持硬件或嵌套虚拟化） |
| 权限 | 安装脚本需 root 权限 |

安装脚本会自动安装：LXC、LXC 模板、bridge-utils、iproute2、iptables、conntrack、dnsmasq，以及 KVM 相关（QEMU、libvirt、cloud-image-utils、genisoimage 等）。

---

## 二、一键安装

> 说明：`install.sh` 默认从本仓库 Release 拉取发行版；如需指向其它仓库或上游构建，可通过 `EYVESCLOUD_REPO` 覆盖（默认 `FenhaoLost/eyves-vm-panel`）。

```bash
# 安装本仓库发行版（国内网络可先配置代理或使用镜像）
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh
```

常用环境变量：

```bash
# 发行版来源仓库（推荐显式指定为本仓库）
EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel
# 指定版本（默认 latest）
EYVESCLOUD_VERSION=latest
# 手动指定 NAT 网段（默认自动检测可用私网）
EYVESCLOUD_LXC_SUBNET=10.0.3.0/24
EYVESCLOUD_KVM_SUBNET=192.168.122.0/24
# 指定面板语言
EYVESCLOUD_LANG=zh
```

卸载：

```bash
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh -s -- uninstall
# 非交互卸载
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel EYVESCLOUD_UNINSTALL_CONFIRM=1 sh -s -- uninstall
```

> 卸载仅删除名称形如 `ct-数字` 的 LXC 容器、`eyvescloud-img-dl-*` 下载临时容器和 `vm-数字` 的 KVM 域，不会误删其他生产数据；`/root/eyvescloud-backups` 备份目录会被保留。

---

## 三、手动编译安装

```bash
# 1. 安装系统依赖（以 Debian/Ubuntu 为例）
apt-get update
apt-get install -y lxc lxc-templates lxcfs bridge-utils uidmap iproute2 iptables conntrack \
  quota e2fsprogs xfsprogs dnsmasq-base python3
# KVM 可选
apt-get install -y qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients \
  cloud-image-utils genisoimage xorriso smartmontools virtinst ovmf

# 2. 编译后端
cd backend
go build -o eyvescloud .

# 3. 编译前端（可选，产物会自动内嵌到后端）
cd ../frontend
npm install
npm run build

# 4. 安装二进制与服务
install -m 0755 eyvescloud /usr/local/bin/eyvescloud
```

systemd 服务（供参考，一键安装脚本会自动生成）：

```ini
# /etc/systemd/system/eyvescloud.service
[Unit]
Description=EyvesCloud - LXC/KVM Container Manager
After=network-online.target lxc.service lxcfs.service lxc-net.service libvirtd.service
Wants=network-online.target libvirtd.service

[Service]
Type=simple
ExecStart=/usr/local/bin/eyvescloud server
Restart=always
RestartSec=5
LimitNOFILE=1048576
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# 注意：不要使用 NoNewPrivileges / ProtectControlGroups / ProtectKernelTunables /
# ProtectKernelModules / RestrictSUIDSGID 加固——eyvescloud 通过子进程管理 LXC/KVM，
# 需要可写 cgroup、/proc/sys、可加载内核模块（br_netfilter/tun/vhost）及 setuid 辅助
# 程序（lxc-start、mount -o loop、nsenter、modprobe 等），上述加固会导致容器/虚拟机
# 无法启动。保留对功能无害的 PrivateTmp 与 RestrictRealtime 即可。
PrivateTmp=true
RestrictRealtime=true

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now eyvescloud
```

---

## 四、首次初始化

1. 浏览器访问 `http://服务器IP:8999`（默认端口 8999，可在面板设置中修改）。
2. 首次启动时后端会在日志中输出初始管理员账号与随机密码：
   ```bash
   journalctl -u eyvescloud --no-pager -n 80 | grep -E "Username:|Password:"
   ```
3. 登录后进入「面板设置」立即修改管理员密码。
4. 按需在「存储管理」中添加存储池、「镜像管理」中启用并下载所需模板镜像，然后即可在「容器管理」中创建实例。

---

## 五、基础配置

### 5.1 存储池
进入「存储管理」，添加 LXC / KVM 存储池（目录或挂载点）。创建容器时可选择存储池；磁盘限制支持 ext4 project quota，未启用时自动回退到 loopback 镜像磁盘限制模式。

### 5.2 镜像与模板
「镜像管理」内置 Ubuntu / Debian / Alpine / CentOS / Arch Linux / Fedora / Rocky Linux 等常见发行版模板，支持启用、禁用、下载、取消下载、清理缓存与自定义 KVM/LXC 镜像（自定义镜像支持校验 SHA256）。

### 5.3 网络
- NAT4：`/api` 面板「路由管理」可查看端口映射与公网 IPv4 池；LXC 默认网桥 `lxcbr0`，KVM 使用 libvirt `default`（virbr0）。
- IPv6：支持前缀检测、状态检查与容器级 IPv6 分配。
- 静态 IP：容器支持分配公网 IPv4 池地址与独立 IPv6 地址（见第十一章）。

### 5.4 HTTPS（TLS）
「面板设置 → SSL」支持启用 Let's Encrypt 或上传自定义证书；也可使用仓库自带的 `install-certbot.sh` 脚本自动签发。

---

## 六、12 大能力使用说明

### 01 自助控制面板 · 资源创建
「容器管理」支持创建、重装、开机、关机、重启、删除、重置密码、到期时间、批量操作与任务队列。创建向导可选网络（NAT / LAN / 公网 IPv4 / IPv6）、资源配额与镜像授权。

### 02 节点迁移 · 迁移调度
「节点迁移」页提供跨节点迁移调度：
1. 在源节点选择容器，点击「导出迁移包」，下载 `xxx.migrate.json` 文件（包含容器配置、网络、防火墙、SSH 凭据与 Cloud-init 配置）。
2. 在目标节点（已安装 EyvesCloud 且已启用对应模板镜像）点击「导入迁移包」上传文件。
3. 导入成功后核对配置，再在源节点删除原容器。

> 迁移包不包含系统盘数据。如需完整迁移数据，请先在源节点创建快照并随迁移包转移，或使用系统盘级备份工具。迁移流程不会中断源容器运行，属于「先导出 → 后导入 → 最后下线」的低影响调度方式。

### 03 精确权限控制 · 角色分层
「子用户管理」支持创建子用户并分配角色：
- **operator（操作员）**：可管理被授权容器。
- **viewer（只读）**：只能查看，所有写操作会被后端拦截，前端操作按钮也会隐藏。

同时支持按容器授权、访问码、密码轮换、到期时间与操作审计追溯。

### 04 租户隔离 · 独立边界
- 「子用户管理」可为子用户设置租户；容器可设置所属租户。
- 同一租户下的子用户可查看并管理该租户的全部容器，实现多租户独立边界。
- 租户信息会随容器列表、子用户管理界面展示，便于分组运营。

### 05 ARP 风险防护 · 地址管理
「安全告警」页顶部提供「ARP 防护」开关：
- 开启后，安全扫描器会通过 `ip neigh show` 检查公网/独立 IP 容器绑定的 MAC 是否与邻居表一致。
- 发现 IP-MAC 冲突或欺骗时生成 `arp_spoof` 高危告警，记录源 IP、实际 MAC 与绑定 MAC，便于排查地址冲突与 ARP 欺骗。

### 06 Cloud-init · 启动即用
创建容器时可在向导中填写「Cloud-init 初始化配置」（可选）：
- **KVM**：自定义 user-data 会替代默认 cloud-config 写入 seed.iso，首次启动即执行。
- **LXC**：user-data 会写入容器 rootfs 的 NoCloud seed 目录（`/var/lib/cloud/seed/nocloud-net`），容器内 cloud-init 首次启动时自动读取。

支持 `#cloud-config` 语法或普通 shell 脚本（`#!/bin/bash` 开头），用于自动安装软件、注入 SSH、初始化业务数据。

### 07 API 接口 · 开放编排
「API 集成」页可创建 API Key（支持 scope 授权：如 `container:*`、`policy:*` 等），所有能力均暴露为 REST 接口：
- 容器：`GET/POST /api/containers`、`GET/POST/DELETE /api/containers/{id}/...`
- 策略：`GET/POST /api/policies`、`PUT/DELETE /api/policies/{id}`
- 迁移：`GET /api/containers/{id}/migrate-export`、`POST /api/migrate/import`
- 安全、监控、任务队列、批量创建/批量操作均可用。

携带 `Authorization: Bearer <token>` 即可对接自动化编排（Ansible、脚本、财务计费系统等）。

### 08 CPU / 带宽策略 · 条件触发
「策略管理」页支持创建自动调整策略，策略引擎每分钟采样运行中容器：
- **指标**：CPU 使用率(%)、内存使用率(%)、入站带宽(bps)、出站带宽(bps)、磁盘 IO(B/s)
- **条件**：大于 / 小于阈值
- **动作**：提升 CPU 核数、提升内存、调整带宽、自动关机
- **作用范围**：全部容器 / 指定租户（`tenant:xxx`）/ 指定容器（`container:xxx`）
- **冷却**：每条规则可设置冷却时间（默认 10 分钟），防止频繁触发

触发记录持久化保存（最近 500 条）并写入操作日志，管理员可在页面查看每次触发的指标值与执行详情。

### 09 监控告警 · 实时洞察
- 「安全告警」页基于 conntrack 检测端口扫描、横向扫描、暴力破解、DDoS、垃圾邮件、挖矿、代理/VPN/Tor、UDP 反射与 ARP 欺骗。
- 「面板设置 → 通知」可配置 **Webhook / 邮件** 推送渠道，告警触发时自动推送并带 5 分钟去重，避免刷屏。
- 支持告警自动关机开关，高危攻击可自动隔离容器。

### 10 详细日志 · 操作留痕
「操作日志」页记录管理员与子用户全部关键操作（创建、删除、迁移、策略变更、安全设置等），包含操作人、IP、User-Agent、动作、目标与结果；「面板设置」另提供登录日志，全程可追溯。

### 11 静态 IP · 公网资源
容器支持：
- 公网 IPv4 池（可分配独立公网 IP，`/api/routing` 管理池与路由）
- 独立 IPv6 地址（自动配置地址与路由）
- LAN 静态 IPv4（指定网段、网关）
- 静态 IP 与 MAC 绑定，配合 ARP 防护保障公网地址安全。

### 12 多维统计 · 趋势分析
- 指标采样器每 30 秒采集运行中容器的 CPU / 内存 / 网络 / 磁盘 IO，内存保留近期数据并持久化到 SQLite。
- 容器详情页提供历史趋势图表（`/api/containers/{id}/history`），面板重启后历史不丢失。
- 宿主机页面提供主机资源总览与历史趋势（`/api/host-history`）。

---

## 七、数据存储与备份

面板数据保存在 SQLite 数据库（默认 `/root/.eyvescloud/config.db`），包含：
- 容器、子用户、API Key、操作日志、登录日志、任务队列
- 安全设置、通知渠道、面板访问策略、存储池
- 策略规则与触发历史
- 容器指标采样

备份（建议配合 cron 每日执行）：

```bash
# 停服备份最安全；也可在线复制（WAL 模式下建议先执行 checkpoint）
sqlite3 /root/.eyvescloud/config.db "PRAGMA wal_checkpoint(TRUNCATE);"
cp /root/.eyvescloud/config.db /root/eyvescloud-backups/config.$(date +%F).db
```

恢复：停止 eyvescloud 服务，将备份文件覆盖到 `/root/.eyvescloud/config.db`，然后启动服务。

---

## 八、升级

```bash
# 一键安装的节点直接重跑安装脚本即可升级
curl -fsSL https://raw.githubusercontent.com/FenhaoLost/eyves-vm-panel/main/install.sh | sudo EYVESCLOUD_REPO=FenhaoLost/eyves-vm-panel sh

# 手动编译方式
cd backend && go build -o eyvescloud . && systemctl restart eyvescloud
```

数据库结构会在启动时自动执行增量迁移（新增字段/表），无需手工处理。

---

## 九、常用运维命令

```bash
# systemd
systemctl status eyvescloud
journalctl -u eyvescloud -f

# OpenRC
rc-service eyvescloud status
tail -f /var/log/eyvescloud.log /var/log/eyvescloud.err

# 查看默认端口与监听
ss -tlnp | grep 8999

# CLI 模式（不启动 Web，便于排查）
eyvescloud --help
```

---

## 十、常见问题

**Q1：首次启动没有看到初始密码？**
说明服务器上已存在 `/root/.eyvescloud/config.db`（旧数据）。管理员密码使用 bcrypt 存储，无法反查；请在面板内「修改密码」，或备份后删除数据库重新初始化。

**Q2：KVM 创建失败？**
检查 `ls /dev/kvm` 是否存在（VPS 需开启嵌套虚拟化）、`systemctl status libvirtd` 是否正常、`cloud-localds` 是否安装（cloud-image-utils）。

**Q3：容器无法上网？**
确认 `net.ipv4.ip_forward=1` 已生效（`/etc/sysctl.d/99-eyvescloud.conf`）、`lxcbr0`/`virbr0` 网桥存在、NAT 网段未与宿主机其他网段冲突；如启用 UFW，需放行 lxcbr0/virbr0 的入站与转发。

**Q4：策略自动关机后如何恢复？**
在「容器管理」手动开机即可。策略触发记录可在「策略管理 → 触发记录」中查看；如不需要该行为，关闭对应策略。

**Q5：跨节点迁移导入失败？**
目标节点必须已「启用并下载」对应的模板镜像（「镜像管理」），且不存在同名容器；公网 IP / 端口可能冲突，请先在目标节点释放同名资源。

**Q6：ARP 告警频繁？**
说明有 IP-MAC 冲突或欺骗。请检查该公网 IP 是否被重复分配（「路由管理 → 公网 IPv4 池」），确认容器绑定的 MAC 与网关邻居表一致。

**Q7：指标历史不显示？**
确认容器处于运行状态（仅运行中容器采样）；面板默认保留近 24 小时趋势并可回溯 SQLite 中的持久化历史。

---

## 十一、免责声明

本软件为开源项目，仅供学习和研究 LXC、KVM 等虚拟化技术原理之目的使用；不提供 Windows 系统镜像分发或任何绕过激活机制的功能。请确保你拥有所使用系统与软件（包括 Microsoft Windows 等）的合法授权，并遵守适用的法律法规。
