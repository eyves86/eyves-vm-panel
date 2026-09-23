# 故障排查

## 服务无法访问

检查服务状态：

```bash
systemctl status eyvescloud
journalctl -u eyvescloud -n 100 --no-pager
```

检查端口监听：

```bash
ss -lntp | grep 8999
```

如果使用反向代理，请同时检查代理日志和上游地址。

## 镜像下载失败

- 确认宿主机可以访问镜像源和 GitHub Release。
- 检查磁盘空间。
- 在任务队列里查看失败原因。
- 如下载卡住，可尝试取消任务后重新下载。

## 容器无法联网

- 检查宿主机 NAT 和转发规则。
- 检查容器 IP 是否分配成功。
- 检查防火墙是否拦截转发流量。
- IPv6 场景下确认上游已经把地址段路由到宿主机。

## WebSSH 或 WebVNC 连接失败

- 确认容器或虚拟机正在运行。
- WebSSH 需要容器内 SSH 服务可用。
- WebVNC 需要 KVM 控制台可访问。
- 票据有效期很短，过期后重新创建即可。

## API 返回未授权

- 确认 API Key 没有被禁用。
- 确认请求头使用 `X-API-Key` 或 `Authorization: Bearer`。
- 确认密钥权限范围覆盖目标接口。
- 不要把面板登录密码当作 API Key 使用。

## 被控注册失败

一键安装脚本输出「注册失败」或被控日志提示注册报错时：

- 确认安装密钥与主控创建节点时生成的密钥一致。
- 确认被控能访问主控：`curl -v http://MASTER_IP:8999/api/version`。
- 被控已注册过但更换了主控时，需要删除被控配置目录下的 `agent.json` 后重新注册。
- 主控返回「Invalid install key」时，请在主控重新获取该节点的安装脚本。

## 被控心跳失败（节点显示离线）

- 检查被控的 `eyvescloud-agent` 服务：`systemctl status eyvescloud-agent`。
- 检查被控能否访问主控 `:8999`。
- 检查主控节点记录中的 token 是否与被控 `agent.json` 一致；不一致时删除被控 `agent.json` 重新注册。

## 主控无法代理查看被控容器

- 确认节点状态为「在线」。
- 直接在被控上测试：`curl http://NODE_IP:8999/api/version`。
- 在主控服务器上测试能否访问被控地址：`curl http://NODE_IP:8999/api/version`。
- 若被控面板地址不可达，更新节点地址或在主控删除后重新添加节点。

## 被控 Agent 端口冲突

被控默认面板端口与主控同为 `8999`。若被控本机已有其他服务占用，使用 `--addr` 指定其他端口，并确保该端口已在防火墙放行。
