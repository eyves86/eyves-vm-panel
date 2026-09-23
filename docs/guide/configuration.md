# 配置说明

EyvesCloud 安装后会以 systemd 服务运行，运行时配置和数据库保存在宿主机本地。实际路径可能随安装脚本参数变化，默认安装建议以 `/root/.eyvescloud/` 为主要检查位置。

## 常见配置项

| 配置 | 说明 |
| --- | --- |
| Web 端口 | 默认 `8999`，服务启动时监听 `0.0.0.0:8999`。 |
| 管理员账号 | 用于登录 Web 面板和管理 API Key。 |
| 数据库 | SQLite，用于保存容器元数据、节点、子用户、审计日志、API Key 等。 |
| NAT 端口范围 | 用于随机端口和端口映射分配。 |
| IPv6 地址段 | 宿主机有可路由 IPv6 时可配置分配策略。 |
| 存储池 | 可配置多个存储池及内容类型（LXC/KVM/镜像/快照/备份）。 |
| 策略规则 | 可配置资源/流量/到期触发策略，命中后自动执行回收动作。 |
| 安全告警 | 可配置自动关机等策略。 |

## 服务命令

```bash
systemctl status eyvescloud
systemctl restart eyvescloud
journalctl -u eyvescloud -n 100 --no-pager
```

## 面板访问白名单 CLI

```bash
# 查看当前策略
eyvescloud access-policy show

# 仅允许指定 IP/网段；反向代理地址按需填写
eyvescloud access-policy set \
  --allow "203.0.113.10,192.168.1.0/24,2001:db8::/32" \
  --trusted-proxy "127.0.0.1"

# 关闭白名单限制
eyvescloud access-policy disable
```

也可以运行 `eyvescloud cli`，在交互菜单中选择“面板访问白名单”。直接命令和交互菜单都会保存配置，并在服务运行时自动重启面板。

## 被控节点配置

被控节点（Agent 模式）的注册信息保存在配置目录下的 `agent.json`：

```json
{
  "controller": "http://MASTER_IP:8999",
  "node_id": "node-xxxx",
  "token": "被控节点 token",
  "name": "node-1",
  "address": "http://THIS_IP:8999"
}
```

该文件由 `eyvescloud agent` 首次注册时自动生成，`token` 用于主控与被控之间的接口鉴权，请勿外泄。

## 安全建议

- 不要把 Web 面板直接暴露给不可信来源。
- 使用复杂管理员密码，并定期轮换。
- API Key 按用途拆分权限，避免长期使用全权限密钥。
- WebSSH、WebVNC 票据是短期凭证，不应写入日志或外发。
- 被控节点的安装密钥仅在首次注册时使用，注册完成后主控可随时删除节点以吊销接入。
- 对外文档、截图和工单里不要粘贴真实 IP、密码、API Key 或票据。
