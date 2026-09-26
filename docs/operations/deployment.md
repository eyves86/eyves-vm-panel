# 部署建议

EyvesCloud 可以直接运行在宿主机上，也可以放在反向代理之后。生产环境建议先做好访问控制，再开放给管理员使用。支持单面板部署和「主控-被控」多节点部署。

## 服务暴露

默认 Web 端口为 `8999`：

```text
http://YOUR_SERVER_IP:8999
```

建议：

- 仅允许固定管理员 IP 访问。
- 使用反向代理配置 HTTPS。
- 不要在公开文档或截图里暴露真实登录地址。

## systemd

常用命令：

```bash
systemctl status eyvescloud
systemctl restart eyvescloud
systemctl enable eyvescloud
journalctl -u eyvescloud -f
```

## 主控-被控多节点部署

### 主控节点

在需要作为主控的服务器上正常安装面板即可。所有面板都可以充当主控。

### 被控节点

在被控服务器上执行主控「节点管理」生成的一键安装脚本：

```bash
bash eyvescloud-agent-node1.sh
```

脚本会安装二进制、注册到主控并配置 `eyvescloud-agent` systemd 服务。被控节点也可以直接手动安装面板后以 agent 模式启动：

```bash
eyvescloud agent --controller=http://MASTER_IP:8999 --install-key=INSTALL_KEY --name=node-1 --addr=http://NODE_IP:8999
```

### 网络要求

- 被控必须能从内网/公网访问主控的 `:8999`（注册与心跳）。
- 主控必须能访问被控的 `node.Address`（代理查看/操作容器）。
- 建议为被控配置其公网可访问地址作为 `--addr`。

### 扩容被控节点

重复「主控添加节点 → 获取脚本 → 被控执行」即可接入任意数量的被控节点。主控「节点管理」页面会统一展示各节点的在线状态、资源与容器数。

## 防火墙

至少确认：

- 面板端口只对可信来源开放。
- NAT 映射端口按需开放。
- SSH 管理端口不与容器映射冲突。
- IPv6 防火墙规则与 IPv4 同步规划。
- 主控与被控之间的端口（注册、心跳、代理）放通。

## 备份

建议定期备份：

- EyvesCloud 配置目录（含 SQLite 数据库）。
- 被控节点的 `agent.json`（更换被控磁盘后可直接恢复接入）。
- 容器配置。
- 关键容器的快照或外部数据备份。
