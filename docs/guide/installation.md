# 安装

EyvesCloud 提供一键安装脚本。脚本默认安装 GitHub Releases 的最新版本，也可以通过环境变量指定固定版本。

## 环境要求

- Linux x86_64/amd64 或 ARM64/aarch64 宿主机。
- root 权限。
- systemd（或 OpenRC）。
- 网络可访问 GitHub Release 下载地址。
- 如果要使用 LXC，需要宿主机支持 LXC 运行环境。
- 如果要使用 KVM，需要宿主机开启虚拟化并安装 libvirt/QEMU。

## 安装最新版本

```bash
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo sh
```

> 说明：`install.sh` 默认从本仓库 Release（`codeberg:fenhaolost/eyves-vm-panel`）拉取发行版；如需覆盖来源，可设置 `EYVESCLOUD_REPO`。

安装器会分别询问 LXC 与 KVM 的 NAT 私网网段。直接回车时，脚本会扫描宿主机路由、网卡、网桥和 libvirt 网络，自动选择未冲突的 RFC1918 `/24` 网段；也可以输入 `172.28.40.0/24` 这类 CIDR。非交互安装可设置 `EYVESCLOUD_LXC_SUBNET` 和 `EYVESCLOUD_KVM_SUBNET`。

脚本默认 `EYVESCLOUD_VERSION=latest`，会按宿主架构下载 `releases/latest` 对应的 `eyvescloud-linux-amd64.tar.gz` 或 `eyvescloud-linux-arm64.tar.gz`。

## 安装指定版本

```bash
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo EYVESCLOUD_REPO=codeberg:fenhaolost/eyves-vm-panel EYVESCLOUD_VERSION=v1.1.32 sh
```

把 `v1.1.32` 替换成需要安装的 Release 标签即可。

### 列出所有版本并交互式选择

在交互终端（`[ -t 0 ]`）直接运行安装脚本且未指定 `EYVESCLOUD_VERSION` 时，脚本会从 GitHub 拉取项目全部 Release，列出最近 20 个版本供选择：

```text
  1) v1.1.32
  2) v1.1.31
  3) v1.1.29
  ...
  Enter) 最新版本 (latest)
  请输入版本号或编号 [Enter=latest]:
```

- 直接回车：安装最新版本（`latest`）。
- 输入编号（`1`/`2`/…）：安装对应版本。
- 输入版本号（如 `v1.1.30` 或 `1.1.30`）：精确安装该版本。

> 说明：非交互环境（`curl | sh` 管道、CI）不会触发选择，始终使用 `latest`；如需指定版本请显式设置 `EYVESCLOUD_VERSION`。

## 访问面板

安装完成后，浏览器访问：

```text
http://YOUR_SERVER_IP:8999
```

首次登录请使用安装脚本输出的管理员账号信息。生产环境建议在防火墙或反向代理层限制访问来源，并尽快修改默认账号和密码。

## 运行模式

同一份二进制支持三种运行模式：

| 模式 | 说明 |
| --- | --- |
| 面板模式（默认） | 直接运行 `eyvescloud server` 或安装后由 systemd 托管，作为独立面板。 |
| 主控模式 | 同样是面板模式，额外进入「节点管理」页面生成被控安装脚本。任何面板都可以是主控。 |
| 被控模式（Agent） | 在主控「节点管理」里执行一键安装脚本后，被控以 `eyvescloud agent` 方式运行，自动注册到主控并上报心跳。 |

手动以被控模式启动：

```bash
eyvescloud agent --controller=http://MASTER_IP:8999 --install-key=INSTALL_KEY --name=node-1 --addr=http://THIS_IP:8999
```

参数说明：

- `--controller`：主控面板地址。
- `--install-key`：主控「节点管理」中创建节点后生成的安装密钥（首次注册使用）。
- `--name`：被控节点名称，默认取主机名。
- `--addr`：被控自身面板地址，主控会通过该地址代理访问被控容器。

## 卸载

```bash
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo EYVESCLOUD_REPO=codeberg:fenhaolost/eyves-vm-panel sh -s -- uninstall
# 非交互卸载
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo EYVESCLOUD_REPO=codeberg:fenhaolost/eyves-vm-panel EYVESCLOUD_UNINSTALL_CONFIRM=1 sh -s -- uninstall
```

卸载前请确认是否需要保留容器、镜像缓存、数据库和配置文件。
