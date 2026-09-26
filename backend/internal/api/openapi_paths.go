package api

// 本文件补全对外 OpenAPI 契约。
//
// 背景：HandleOpenAPI 原先只声明了 8 个路径，而实际可用（且计费系统模块已在用）
// 的接口有几十个。第三方照 spec 开发会以为 /containers/{id}/start 之类的接口不存在，
// 所以这里把真实路由补齐。合并逻辑见 enterprise.go 中的 openAPIMergeExtraPaths。

// openAPIOperation 构造一个精简的 OpenAPI operation。
func openAPIOperation(summary string) map[string]interface{} {
	return map[string]interface{}{
		"summary":   summary,
		"responses": map[string]interface{}{"200": map[string]string{"description": "OK"}},
	}
}

// openAPIExtraPaths 返回需要补充声明的路径 → (method → summary)。
func openAPIExtraPaths() map[string]map[string]string {
	return map[string]map[string]string{
		// ---- 容器：创建 / 列表 ----
		"/containers/list": {"get": "列出容器（别名，字段裁剪版）"},

		// ---- 容器：电源与生命周期 ----
		"/containers/{id}/start":          {"post": "开机"},
		"/containers/{id}/stop":           {"post": "关机"},
		"/containers/{id}/restart":        {"post": "重启"},
		"/containers/{id}/reinstall":      {"post": "重装系统（body: template_id）"},
		"/containers/{id}/suspend":        {"post": "挂起容器（欠费停机：强制关机并阻断 start/restart/reinstall/WebSSH/VNC，body: reason 可选）"},
		"/containers/{id}/unsuspend":      {"post": "解除挂起（复机，不自动开机）"},
		"/containers/{id}/delete":         {"delete": "删除容器"},
		"/containers/{id}/reset-password": {"post": "重置 SSH 密码（body: password 可选）"},
		"/containers/{id}/create-account": {"post": "创建容器内账号"},

		// ---- 容器：资源 / 配额 / 到期 ----
		"/containers/{id}/resource-limit": {"put": "调整资源限制（vcpu/ram_mb/io/带宽/disk_gb，仅允许扩容）"},
		"/containers/{id}/traffic-limit":  {"put": "调整流量限制"},
		"/containers/{id}/traffic-reset":  {"post": "重置已用流量"},
		"/containers/{id}/expiry":         {"put": "设置到期时间（body: expires_at）"},
		"/containers/{id}/tenant":         {"put": "设置所属租户"},

		// ---- 容器：用量 / 监控 ----
		"/containers/{id}/usage":   {"get": "实时用量（CPU/内存/磁盘/网络/磁盘 IO）"},
		"/containers/{id}/traffic": {"get": "流量统计（已用/上限）"},
		"/containers/{id}/history": {"get": "历史指标时序（原始采样 + 小时聚合），用于绘制曲线"},
		"/containers/{id}/stats":   {"get": "一站式监控：CPU/RAM/Disk/Inodes/Uptime（Virtualizor act=monitor）"},
		"/containers/{id}/bandwidth": {"get": "流量明细（按周期聚合，对齐 Virtualizor act=bandwidth）"},
		"/containers/{id}/processes": {"get": "容器内进程列表（对齐 Virtualizor act=processes，仅 LXC）"},
		"/containers/{id}/processes/kill": {"post": "批量终止容器内进程（对齐 Virtualizor act=processes + sel_proc[]）"},
		"/containers/{id}/services": {"get": "容器内 systemd 服务列表（对齐 Virtualizor act=services）"},
		"/containers/{id}/services/action": {"post": "服务启停/启用禁用（对齐 Virtualizor act=services + start_x/stop_x）"},
		"/containers/{id}/hvm-settings": {
			"get": "KVM HVM 设置（启动盘/网卡驱动/VNC 键位/加速/TUN PPP）",
			"put": "更新 KVM HVM 设置（白名单校验）",
		},
		"/containers/{id}/scheduled-actions": {
			"get":  "列出定时任务（对齐 Virtualizor act=self_shutdown）",
			"post": "创建定时任务（启/停/重启/硬关机 + 重复周期）",
		},
		"/containers/{id}/scheduled-actions/{actionID}": {
			"delete": "删除定时任务",
		},
		"/usage":                   {"get": "全量用量导出（供财务系统拉取：按租户过滤，含每容器配置/实时用量/流量/到期/挂起状态）"},

		// ---- 容器：网络 ----
		"/containers/{id}/random-port":   {"get": "获取一个可用 NAT 端口"},
		"/containers/{id}/port-mappings": {"post": "新增 NAT 端口映射"},
		"/containers/{id}/port-mappings/{index}": {
			"put":    "修改 NAT 端口映射",
			"delete": "删除 NAT 端口映射",
		},
		"/containers/{id}/firewall":       {"get": "查询防火墙规则", "put": "更新防火墙规则"},
		"/containers/{id}/rdns":           {"get": "查询反向 DNS 记录", "put": "更新反向 DNS 记录"},
		"/containers/{id}/ipv6":           {"post": "分配 IPv6"},
		"/containers/{id}/ipv6-addresses": {"put": "更新 IPv6 地址"},
		"/containers/{id}/public-ipv4":    {"put": "更新公网 IPv4 分配"},

		// ---- 容器：快照 / 备份 / 迁移 ----
		"/containers/{id}/snapshots":                      {"get": "列出快照", "post": "创建快照"},
		"/containers/{id}/snapshots/{snapshotID}":         {"delete": "删除快照"},
		"/containers/{id}/snapshots/{snapshotID}/restore": {"post": "恢复快照"},
		"/containers/{id}/backups":                        {"get": "列出实例备份", "post": "创建实例备份"},
		"/containers/{id}/migrate-export":                 {"get": "导出迁移包"},

		// ---- 模板 / 镜像 ----
		"/templates":      {"get": "列出可用模板（LXC 模板 + KVM 镜像）"},
		"/images":         {"get": "列出镜像"},
		"/images/enabled": {"get": "列出已启用镜像（子用户可用）"},

		// ---- 任务 ----
		"/tasks/{id}": {"delete": "删除任务记录"},

		// ---- 票据（控制台接入）----
		"/ssh-ticket": {"post": "创建 WebSSH 票据（body: container_name 或 container_id）"},
		"/vnc-ticket": {"post": "创建 VNC 票据（KVM 控制台）"},

		// ---- 子用户 / 租户 ----
		"/sub-users":       {"get": "列出子用户"},
		"/sub-user/create": {"post": "创建子用户（可绑定容器/租户）"},
		"/sub-users/{id}":  {"get": "子用户详情", "put": "更新子用户", "delete": "删除子用户"},

		// ---- 监控 / 主机 ----
		"/monitoring/containers": {"get": "批量容器监控指标"},
		"/host-info":             {"get": "宿主机信息"},
		"/host-history":          {"get": "宿主机历史指标"},
		"/host-report":           {"get": "宿主机报告"},
		"/dashboard":             {"get": "面板概览"},

		// ---- 安全 ----
		"/security/alerts":  {"get": "安全告警列表"},
		"/security/logs":    {"get": "安全日志"},
		"/security/summary": {"get": "容器安全摘要"},

		// ---- 审计 ----
		"/audit-logs":    {"get": "审计日志"},
		"/login-logs":    {"get": "登录日志"},
		"/api-keys/{id}": {"delete": "吊销 API Key"},

		// ---- 集成：内置 WHMCS 9.0 服务器开通模块（仅管理员）----
		"/integrations/whmcs":          {"get": "内置 WHMCS 服务器模块元信息（版本 / 安装路径 / 文件清单 / README）"},
		"/integrations/whmcs/download": {"get": "下载 WHMCS 服务器模块 zip（解压到 WHMCS 根目录即可安装）"},
	}
}
