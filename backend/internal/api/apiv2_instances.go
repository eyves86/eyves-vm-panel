package api

// apiv2_instances.go —— API v2：实例（LXC 容器 / KVM 虚拟机）资源与动作。
//
// 端点（全部位于 /api/v2）：
//
//	GET    /instances                      实例列表（分页/过滤/排序/搜索）
//	POST   /instances                      创建实例（支持批量 count、指定节点或自动调度）
//	GET    /instances/{id}                 实例详情
//	PATCH  /instances/{id}                 修改实例（资源配置/备注/到期时间）
//	DELETE /instances/{id}                 删除实例
//	POST   /instances/{id}/power           电源操作 {action: start|stop|restart|hard-stop|hard-restart}
//	POST   /instances/{id}/reinstall       重装系统
//	POST   /instances/{id}/reset-password  重置 root 密码
//	POST   /instances/{id}/console         获取 WebSSH/VNC 会话（一次性票据）
//	GET    /instances/{id}/metrics         实时指标（CPU/内存/网络/磁盘）
//	GET    /instances/{id}/usage           流量与配额用量
//	GET    /instances/{id}/events          实例相关审计事件
//	GET    /instances/{id}/xml             KVM libvirt XML（LXC 返回 400）
//	POST   /instances/{id}/snapshots       创建快照
//	GET    /instances/{id}/snapshots       快照列表
//	POST   /instances/{id}/snapshots/{sid}/restore  从快照恢复
//	DELETE /instances/{id}/snapshots/{sid} 删除快照
//	POST   /instances/{id}/backups         创建完整备份
//	GET    /instances/{id}/backups         备份列表
//	DELETE /instances/{id}/backups/{bid}   删除备份
//	POST   /instances/{id}/clone           克隆实例
//	POST   /instances/{id}/rescue          进入救援模式（KVM）
//	DELETE /instances/{id}/rescue          退出救援模式
//	POST   /instances/{id}/lock            锁定（禁止破坏性操作）
//	DELETE /instances/{id}/lock            解锁
//	PUT    /instances/{id}/network         网络配置（NAT 端口 / 公网 IP / IPv6）
//	POST   /instances/{id}/migrate         迁移到其它节点
//	POST   /instances/batch                批量操作 {action: power|delete|reinstall, ids: [...]}
//
// 字段契约见 docs/API-V2.md。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/scheduler"
)

func init() {
	// 采用 Go 1.22+ ServeMux 的「方法 + 路径变量」模式，避免手写路由匹配。
	registerV2("GET /api/v2/instances", v2Auth(v2InstancesList))
	registerV2("POST /api/v2/instances", v2Auth(v2InstancesCreate))
	registerV2("GET /api/v2/instances/{id}", v2Auth(v2InstanceGet))
	registerV2("PATCH /api/v2/instances/{id}", v2Auth(v2InstanceUpdate))
	registerV2("DELETE /api/v2/instances/{id}", v2Auth(v2InstanceDelete))
	registerV2("POST /api/v2/instances/{id}/power", v2Auth(v2InstancePower))
	registerV2("POST /api/v2/instances/{id}/reinstall", v2Auth(v2InstanceReinstall))
	registerV2("POST /api/v2/instances/{id}/reset-password", v2Auth(v2InstanceResetPassword))
	registerV2("POST /api/v2/instances/{id}/console", v2Auth(v2InstanceConsole))
	registerV2("GET /api/v2/instances/{id}/metrics", v2Auth(v2InstanceMetrics))
	registerV2("GET /api/v2/instances/{id}/usage", v2Auth(v2InstanceUsage))
	registerV2("GET /api/v2/instances/{id}/events", v2Auth(v2InstanceEvents))
	registerV2("GET /api/v2/instances/{id}/xml", v2Auth(v2InstanceXML))
	registerV2("GET /api/v2/instances/{id}/snapshots", v2Auth(v2InstanceSnapshotList))
	registerV2("POST /api/v2/instances/{id}/snapshots", v2Auth(v2InstanceSnapshotCreate))
	registerV2("POST /api/v2/instances/{id}/snapshots/{sid}/restore", v2Auth(v2InstanceSnapshotRestore))
	registerV2("DELETE /api/v2/instances/{id}/snapshots/{sid}", v2Auth(v2InstanceSnapshotDelete))
	registerV2("GET /api/v2/instances/{id}/backups", v2Auth(v2InstanceBackupList))
	registerV2("POST /api/v2/instances/{id}/backups", v2Auth(v2InstanceBackupCreate))
	registerV2("DELETE /api/v2/instances/{id}/backups/{bid}", v2Auth(v2InstanceBackupDelete))
	registerV2("POST /api/v2/instances/{id}/clone", v2Auth(v2InstanceClone))
	registerV2("POST /api/v2/instances/{id}/rescue", v2Auth(v2InstanceRescueEnter))
	registerV2("DELETE /api/v2/instances/{id}/rescue", v2Auth(v2InstanceRescueExit))
	registerV2("POST /api/v2/instances/{id}/lock", v2Auth(v2InstanceLock))
	registerV2("DELETE /api/v2/instances/{id}/lock", v2Auth(v2InstanceUnlock))
	registerV2("PUT /api/v2/instances/{id}/network", v2Auth(v2InstanceNetworkUpdate))
	registerV2("POST /api/v2/instances/batch", v2Auth(v2InstancesBatch))
}

// v2RegisterHooks 由 server.go 在启动时把 v2 路由挂到 mux 上。
var v2Routes = map[string]http.HandlerFunc{}

func registerV2(pattern string, handler http.HandlerFunc) {
	v2Routes[pattern] = handler
}

// RegisterAPIV2 注册全部 v2 端点（由 server.setupRoutes 调用）。
func RegisterAPIV2(mux *http.ServeMux) {
	for pattern, handler := range v2Routes {
		mux.HandleFunc(pattern, handler)
	}
}

// ---------------------------------------------------------------------------
// 实例视图
// ---------------------------------------------------------------------------

// v2InstanceView 是实例的对外契约结构（字段稳定，改名即破坏性变更）。
func v2InstanceView(c config.Container) map[string]interface{} {
	nodeName, nodeStatus := "", "local"
	if c.NodeID != "" {
		nodeName = idcNodeNameOfV2(c.NodeID)
		nodeStatus = idcNodeStatusOfV2(c.NodeID)
	} else {
		nodeName = "本机（主控）"
	}
	view := map[string]interface{}{
		"id":             c.ID,
		"uuid":           c.UUID,
		"name":           c.Name,
		"status":         v2InstanceStatus(c),
		"runtime":        c.Runtime(), // lxc | kvm
		"node_id":        c.NodeID,
		"node_name":      nodeName,
		"node_status":    nodeStatus,
		"template_id":    c.Template,
		"hostname":       c.Name,
		"vcpu":           c.VCPU,
		"memory_mb":      c.RAMMB,
		"disk_gb":        c.DiskGB,
		"data_disk_gb":   c.DataDiskGB,
		"primary_ip":     c.IP,
		"public_ipv4":    publicIPv4Addresses(c.PublicIPv4s),
		"ipv6_addresses": ipv6AddressStrings(c.IPv6Addresses),
		"ssh_port":       c.SSHPort,
		"mac_address":    c.MACAddress,
		"bandwidth": map[string]interface{}{
			"down_mbps": c.NetworkDownMbps,
			"up_mbps":   c.NetworkUpMbps,
			"total_mbps": c.NetworkBWMbps,
		},
		"traffic": map[string]interface{}{
			"quota_gb":   c.MonthlyTrafficGB,
			"used_rx_gb": round2(float64(c.TrafficUsedRX) / 1024 / 1024 / 1024),
			"used_tx_gb": round2(float64(c.TrafficUsedTX) / 1024 / 1024 / 1024),
			"mode":       c.TrafficMode,
		},
		"io_limits": map[string]interface{}{
			"read_mbps":  c.IOReadMBps,
			"write_mbps": c.IOWriteMBps,
		},
		"storage_pool_id": c.StoragePoolID,
		"suspended":       c.Suspended,
		"suspend_reason":  c.SuspendedReason,
		"suspended_at":    v2Time(c.SuspendedAt),
		"locked":          c.Locked,
		"remark":          c.Remark,
		"rescue_enabled":  c.RescueEnabled,
		"snapshot_limit":  c.SnapshotLimit,
		"snapshot_schedule": map[string]interface{}{
			"enabled":      c.SnapshotScheduleEnabled,
			"interval_hours": c.SnapshotScheduleIntervalHours,
			"time":         c.SnapshotScheduleTime,
			"last_run":     v2Time(c.SnapshotScheduleLastRun),
			"next_run":     v2Time(c.SnapshotScheduleNextRun),
		},
		"expires_at":  v2Time(c.ExpiresAt),
		"created_at":  v2Time(c.CreatedAt),
		"owner":       idcOwnerNameV2(c),
		"tenant":      c.Tenant,
		"nat_ports":   c.PortMappings,
		"port_limit":  c.PortMappingLimit,
		"policy_blocked": c.PolicyBlocked,
		"firewall_enabled": c.FirewallEnabled,
	}
	if c.IsKVM() {
		view["kvm"] = map[string]interface{}{
			"domain":      c.VirshName(),
			"vnc_port":    c.VNCPort,
			"boot_order":  c.HVMBootOrder,
			"nic_driver":  c.HVMNicDriver,
			"acceleration": c.HVMAcceleration,
			"disk_image":  c.DiskImage,
		}
	}
	return view
}

func idcNodeNameOfV2(nodeID string) string {
	if n, ok := config.FindNode(nodeID); ok {
		return n.Name
	}
	return nodeID
}

func idcNodeStatusOfV2(nodeID string) string {
	if n, ok := config.FindNode(nodeID); ok {
		return n.Status
	}
	return "unknown"
}

func idcOwnerNameV2(c config.Container) string {
	if c.OwnerSubUserID == "" {
		return "admin"
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, su := range config.AppConfig.SubUsers {
		if su.ID == c.OwnerSubUserID {
			return su.Username
		}
	}
	return c.OwnerSubUserID
}

// v2FindInstance 解析 {id}（支持数字 ID / UUID / 名称）并做权限与容器绑定校验。
func v2FindInstance(w http.ResponseWriter, r *http.Request) (*config.Container, bool) {
	identifier := strings.TrimSpace(r.PathValue("id"))
	c := containerByIdentifier(identifier)
	if c == nil {
		v2NotFound(w, r, "实例不存在："+identifier)
		return nil, false
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		v2Forbidden(w, r, "无权访问该实例")
		return nil, false
	}
	return c, true
}

// ---------------------------------------------------------------------------
// 列表 / 详情
// ---------------------------------------------------------------------------

func v2InstancesList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	query := v2ParsePage(r)
	containers, err := listByRuntime()
	if err != nil {
		containers = append([]config.Container(nil), config.AppConfig.Containers...)
	}
	containers = filterContainersForRequest(r, containers)

	// 过滤参数（命名自定，语义清晰）：
	//   status=running|stopped|...   runtime=lxc|kvm   node_id=   owner=
	//   locked=true|false            tenant=          template_id=
	//   ip=   name=   min_memory_mb=  max_memory_mb=
	params := r.URL.Query()
	if raw := strings.TrimSpace(params.Get("status")); raw != "" {
		wanted := map[string]bool{}
		for _, s := range strings.Split(raw, ",") {
			wanted[strings.ToLower(strings.TrimSpace(s))] = true
		}
		filtered := containers[:0]
		for _, c := range containers {
			if wanted[v2InstanceStatus(c)] {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.ToLower(strings.TrimSpace(params.Get("runtime"))); raw != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if c.Runtime() == raw {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("node_id")); raw != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if c.NodeID == raw || (raw == "local" && c.NodeID == "") {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("template_id")); raw != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if c.Template == raw {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("owner")); raw != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if idcOwnerNameV2(c) == raw {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("tenant")); raw != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if c.Tenant == raw {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("locked")); raw != "" {
		want := strings.EqualFold(raw, "true") || raw == "1"
		filtered := containers[:0]
		for _, c := range containers {
			if c.Locked == want {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	if raw := strings.TrimSpace(params.Get("min_memory_mb")); raw != "" {
		if min, err := strconv.Atoi(raw); err == nil {
			filtered := containers[:0]
			for _, c := range containers {
				if c.RAMMB >= min {
					filtered = append(filtered, c)
				}
			}
			containers = filtered
		}
	}
	if raw := strings.TrimSpace(params.Get("max_memory_mb")); raw != "" {
		if max, err := strconv.Atoi(raw); err == nil {
			filtered := containers[:0]
			for _, c := range containers {
				if c.RAMMB <= max {
					filtered = append(filtered, c)
				}
			}
			containers = filtered
		}
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if strings.Contains(strings.ToLower(c.Name), keyword) ||
				strings.Contains(strings.ToLower(c.IP), keyword) ||
				strings.Contains(strings.ToLower(c.UUID), keyword) ||
				strings.Contains(strings.ToLower(c.Remark), keyword) {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}

	v2SortInstances(containers, query.Sort, query.Desc)

	// 汇总（前端一次拿到状态分布，不必再请求一次）
	summary := map[string]int{"running": 0, "stopped": 0, "suspended": 0, "creating": 0, "error": 0, "unknown": 0}
	for _, c := range containers {
		summary[v2InstanceStatus(c)]++
	}

	total := len(containers)
	start, end := query.Slice(total)
	items := make([]map[string]interface{}, 0, end-start)
	for _, c := range containers[start:end] {
		items = append(items, v2InstanceView(c))
	}
	v2ListWithSummary(w, r, items, query, total, summary)
}

// v2SortInstances 支持 sort=id|name|vcpu|memory_mb|disk_gb|status|created_at|node_id。
func v2SortInstances(items []config.Container, sortKey string, desc bool) {
	key := strings.ToLower(strings.TrimSpace(sortKey))
	if key == "" {
		key = "id"
	}
	sortSliceStable(items, func(a, b config.Container) bool {
		var less bool
		switch key {
		case "name", "hostname":
			less = a.Name < b.Name
		case "vcpu":
			less = a.VCPU < b.VCPU
		case "memory_mb", "memory":
			less = a.RAMMB < b.RAMMB
		case "disk_gb", "disk":
			less = a.DiskGB < b.DiskGB
		case "status":
			less = v2InstanceStatus(a) < v2InstanceStatus(b)
		case "created_at":
			less = a.CreatedAt < b.CreatedAt
		case "node_id", "node":
			less = a.NodeID < b.NodeID
		default:
			less = a.ID < b.ID
		}
		if desc {
			return !less
		}
		return less
	})
}

func v2InstanceGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	view := v2InstanceView(*c)
	view["snapshots"] = v2SnapshotViews(*c)
	view["backups"] = v2BackupViews(*c)
	view["firewall_rules"] = c.FirewallRules
	view["cloud_init"] = c.CloudInitUserData != ""
	v2OK(w, r, view)
}

// ---------------------------------------------------------------------------
// 创建 / 修改 / 删除
// ---------------------------------------------------------------------------

// v2CreateInstanceRequest 创建实例请求体（v2 契约）。
type v2CreateInstanceRequest struct {
	Name         string  `json:"name"`
	Runtime      string  `json:"runtime"`       // lxc | kvm（默认 lxc）
	TemplateID   string  `json:"template_id"`   // 镜像/模板 ID（必填）
	NodeID       string  `json:"node_id"`       // 目标节点；空 = 本机；"auto" = 调度器选择
	NodePriority int     `json:"node_priority"` // 调度偏好：1 均衡(默认) 2 负载最低 3 内存最空
	Count        int     `json:"count"`         // 批量数量（默认 1，上限 50）
	VCPU         float64 `json:"vcpu"`
	MemoryMB     int     `json:"memory_mb"`
	DiskGB       float64 `json:"disk_gb"`
	DataDiskGB   float64 `json:"data_disk_gb"`
	DownMbps     int     `json:"down_mbps"`
	UpMbps       int     `json:"up_mbps"`
	TrafficQuotaGB int   `json:"traffic_quota_gb"`
	TrafficMode  string  `json:"traffic_mode"`  // total | in_out
	SSHPort      int     `json:"ssh_port"`      // 0 = 自动分配
	NATPorts     int     `json:"nat_ports"`     // NAT 端口映射数量
	AssignNAT    *bool   `json:"assign_nat"`    // 默认 true
	PublicIPv4Count int  `json:"public_ipv4_count"`
	IPv6Count    int     `json:"ipv6_count"`
	StoragePoolID string `json:"storage_pool_id"`
	Auth         struct {
		Mode      string `json:"mode"`       // password | ssh_key
		Password  string `json:"password"`   // 留空自动生成
		SSHKeyIDs []string `json:"ssh_key_ids"`
	} `json:"auth"`
	CloudInit    string   `json:"cloud_init"`
	SnapshotLimit int     `json:"snapshot_limit"`
	Owner        string   `json:"owner"`     // 子用户名（可选）
	Tenant       string   `json:"tenant"`    // 租户
	Remark       string   `json:"remark"`
	ExpiresAt    string   `json:"expires_at"` // RFC3339 或 YYYY-MM-DD
	FirewallEnabled bool  `json:"firewall_enabled"`
}

func v2InstancesCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:create") {
		return
	}
	if isAccessRestrictedRequest(r) {
		v2Forbidden(w, r, "容器绑定型密钥不能创建实例")
		return
	}
	var req v2CreateInstanceRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name, "template_id": req.TemplateID}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	if req.Count < 1 {
		req.Count = 1
	}
	if req.Count > 50 {
		v2BadRequest(w, r, "count 上限为 50", map[string]string{"count": "1-50"})
		return
	}
	runtime := strings.ToLower(strings.TrimSpace(req.Runtime))
	if runtime == "" {
		runtime = config.VirtualizationLXC
	}
	if runtime != config.VirtualizationLXC && runtime != config.VirtualizationKVM {
		v2BadRequest(w, r, "runtime 只能是 lxc 或 kvm", map[string]string{"runtime": runtime})
		return
	}
	if runtime == config.VirtualizationKVM && !hostKVMAvailable() && req.NodeID == "" {
		v2BadRequest(w, r, "本机不支持 KVM（缺少 /dev/kvm 或 virsh），请指定支持 KVM 的节点", nil)
		return
	}
	if req.VCPU <= 0 {
		req.VCPU = 1
	}
	if req.MemoryMB < 64 {
		req.MemoryMB = 512
	}
	if req.DiskGB < 1 {
		req.DiskGB = 10
	}
	if err := validateRuntimeResourceRequest(runtime, req.TemplateID, req.VCPU, req.MemoryMB, req.DiskGB); err != nil {
		v2BadRequest(w, r, err.Error(), nil)
		return
	}
	if config.FindContainerByName(req.Name) != nil {
		v2Conflict(w, r, "实例名称已存在："+req.Name)
		return
	}

	// 目标节点：指定节点 / auto 调度 / 本机。
	targetNodeID := strings.TrimSpace(req.NodeID)
	if targetNodeID == "auto" {
		placement := scheduler.Request{
			RAMMB:               int64(req.MemoryMB),
			DiskGB:              req.DiskGB + req.DataDiskGB,
			ContainerCountDelta: req.Count,
			VirtType:            runtime,
			StorageBackend:      "",
			RequestID:           "v2-" + v2RequestID(r),
		}
		diag, err := scheduler.Place(placement, v2NodePriorityPolicy(req.NodePriority), 3)
		if err != nil || diag.Chosen == "" {
			reason := "没有满足条件的节点"
			if diag.Reason != "" {
				reason = diag.Reason
			}
			v2Precondition(w, r, reason)
			return
		}
		targetNodeID = diag.Chosen
	}
	if targetNodeID != "" && targetNodeID != "local" {
		node, ok := config.FindNode(targetNodeID)
		if !ok {
			v2NotFound(w, r, "节点不存在："+targetNodeID)
			return
		}
		if node.Status != "" && node.Status != "online" {
			v2Conflict(w, r, "节点不在线："+node.Name)
			return
		}
		if node.MaintenanceMode {
			v2Conflict(w, r, "节点处于维护模式，禁止发机："+node.Name)
			return
		}
	}

	password := strings.TrimSpace(req.Auth.Password)
	if password == "" && strings.ToLower(req.Auth.Mode) != "ssh_key" {
		password = generateRandomStr(16)
	}
	assignNAT := true
	if req.AssignNAT != nil {
		assignNAT = *req.AssignNAT
	}
	natPorts := req.NATPorts
	if natPorts <= 0 && assignNAT {
		natPorts = 2
	}

	created := make([]map[string]interface{}, 0, req.Count)
	taskIDs := []string{}
	for i := 0; i < req.Count; i++ {
		name := req.Name
		if req.Count > 1 {
			name = fmt.Sprintf("%s-%d", req.Name, i+1)
		}
		if config.FindContainerByName(name) != nil {
			v2Conflict(w, r, "实例名称已存在："+name)
			return
		}
		cfg := lxc.ContainerConfig{
			Name:              name,
			Virtualization:    runtime,
			TemplateID:        req.TemplateID,
			StoragePoolID:     req.StoragePoolID,
			VCPU:              req.VCPU,
			RAMMB:             req.MemoryMB,
			DiskGB:            req.DiskGB,
			DataDiskGB:        req.DataDiskGB,
			NetworkDownMbps:   req.DownMbps,
			NetworkUpMbps:     req.UpMbps,
			MonthlyTrafficGB:  req.TrafficQuotaGB,
			TrafficMode:       idcTrafficModeOrDefault(req.TrafficMode),
			ManagementPort:    req.SSHPort,
			AssignNAT:         &assignNAT,
			PortMappingCount:  natPorts,
			AssignIPv4:        req.PublicIPv4Count > 0,
			IPv4Count:         req.PublicIPv4Count,
			AssignIPv6:        req.IPv6Count > 0,
			IPv6Count:         req.IPv6Count,
			SnapshotLimit:     idcLimitFromValueV2(req.SnapshotLimit),
			CloudInitUserData: req.CloudInit,
			SSHAuthMode:       idcAuthModeOrDefault(req.Auth.Mode),
			SSHPassword:       password,
			SSHKeyIDs:         req.Auth.SSHKeyIDs,
		}
		// 远程节点：直接代理创建（同步），本机：进任务队列（异步）。
		if targetNodeID != "" && targetNodeID != "local" {
			node, _ := config.FindNode(targetNodeID)
			body, _ := json.Marshal(cfg)
			data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/containers/create", strings.NewReader(string(body)))
			if err != nil {
				v2Upstream(w, r, "节点创建实例失败："+err.Error())
				return
			}
			if status >= 300 {
				v2Upstream(w, r, "节点创建实例失败："+strings.TrimSpace(string(data)))
				return
			}
			var payload struct {
				Data map[string]interface{} `json:"data"`
			}
			_ = json.Unmarshal(data, &payload)
			entry := map[string]interface{}{"name": name, "node_id": node.ID, "node_name": node.Name}
			if payload.Data != nil {
				entry["instance"] = payload.Data
			}
			created = append(created, entry)
			continue
		}
		ids := globalQueue.EnqueueBatchCreateWithAudit([]lxc.ContainerConfig{cfg}, v2AuthContext(r).Username, clientIP(r), r.UserAgent())
		taskIDs = append(taskIDs, ids...)
		created = append(created, map[string]interface{}{"name": name, "task_id": ids})
	}

	// 归属与备注等主控侧属性（远程创建的实例由被控心跳同步后补）
	if owner := strings.TrimSpace(req.Owner); owner != "" {
		config.AppConfigMu.RLock()
		ownerID := ""
		for _, su := range config.AppConfig.SubUsers {
			if strings.EqualFold(su.Username, owner) {
				ownerID = su.ID
				break
			}
		}
		config.AppConfigMu.RUnlock()
		if ownerID == "" {
			v2BadRequest(w, r, "归属用户不存在："+owner, nil)
			return
		}
		for i := range created {
			name, _ := created[i]["name"].(string)
			if c := config.FindContainerByName(name); c != nil {
				config.MutateContainerByID(c.ID, func(target *config.Container) { target.OwnerSubUserID = ownerID })
			}
		}
	}
	for i := range created {
		name, _ := created[i]["name"].(string)
		c := config.FindContainerByName(name)
		if c == nil {
			continue
		}
		config.MutateContainerByID(c.ID, func(target *config.Container) {
			target.Remark = req.Remark
			target.Tenant = req.Tenant
			target.FirewallEnabled = req.FirewallEnabled
			if strings.TrimSpace(req.ExpiresAt) != "" {
				target.ExpiresAt = normalizeV2Date(req.ExpiresAt)
			}
		})
	}

	auditRequest(r, "api.v2.instance.create", req.Name,
		fmt.Sprintf("创建 %d 个实例 runtime=%s node=%s", req.Count, runtime, targetNodeID), true, "")
	v2Created(w, r, map[string]interface{}{
		"count":    req.Count,
		"runtime":  runtime,
		"node_id":  targetNodeID,
		"task_ids": taskIDs,
		"items":    created,
		"password": password, // 仅此一次返回（自动生成时）
	})
}

func idcTrafficModeOrDefault(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "in_out", "in-out", "both":
		return "in_out"
	default:
		return "total"
	}
}

func idcAuthModeOrDefault(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "ssh_key") {
		return "ssh_key"
	}
	return "password"
}

func idcLimitFromValueV2(value int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return 3
	}
	return value
}

func normalizeV2Date(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed.Format("2006-01-02 15:04:05")
		}
	}
	return raw
}

func v2NodePriorityPolicy(priority int) scheduler.Policy {
	policy := scheduler.DefaultPolicy()
	switch priority {
	case 2:
		policy.Name = "lowest-load"
		policy.Score = func(n config.Node, _ scheduler.Request) (float64, []string) {
			total := n.RAMTotalMB
			if total <= 0 {
				total = 1
			}
			used := float64(n.RAMUsedMB) / float64(total)
			return (1 - used) * 100, []string{fmt.Sprintf("memory_used_ratio=%.3f", used)}
		}
	case 3:
		policy.Name = "most-free-memory"
		policy.Score = func(n config.Node, _ scheduler.Request) (float64, []string) {
			free := n.RAMTotalMB - n.RAMUsedMB
			if free < 0 {
				free = 0
			}
			return float64(free), []string{fmt.Sprintf("free_memory_mb=%d", free)}
		}
	}
	return policy
}

// v2InstanceUpdateRequest 修改实例（PATCH 语义：只改传入字段）。
type v2InstanceUpdateRequest struct {
	VCPU         *float64 `json:"vcpu"`
	MemoryMB     *int     `json:"memory_mb"`
	DiskGB       *float64 `json:"disk_gb"`
	DataDiskGB   *float64 `json:"data_disk_gb"`
	DownMbps     *int     `json:"down_mbps"`
	UpMbps       *int     `json:"up_mbps"`
	TrafficQuotaGB *int   `json:"traffic_quota_gb"`
	CPULimit     *int     `json:"cpu_percent"`
	SnapshotLimit *int    `json:"snapshot_limit"`
	Remark       *string  `json:"remark"`
	ExpiresAt    *string  `json:"expires_at"`
	Tenant       *string  `json:"tenant"`
	FirewallEnabled *bool `json:"firewall_enabled"`
}

func v2InstanceUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:account") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.Locked {
		v2Precondition(w, r, "实例已锁定，请先解锁")
		return
	}
	var req v2InstanceUpdateRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	changed := map[string]interface{}{}
	config.MutateContainerByID(c.ID, func(target *config.Container) {
		if req.VCPU != nil && *req.VCPU > 0 {
			target.VCPU = *req.VCPU
			changed["vcpu"] = target.VCPU
		}
		if req.MemoryMB != nil && *req.MemoryMB >= 64 {
			target.RAMMB = *req.MemoryMB
			changed["memory_mb"] = target.RAMMB
		}
		if req.DiskGB != nil && *req.DiskGB >= 1 {
			target.DiskGB = *req.DiskGB
			changed["disk_gb"] = target.DiskGB
		}
		if req.DataDiskGB != nil {
			target.DataDiskGB = *req.DataDiskGB
			changed["data_disk_gb"] = target.DataDiskGB
		}
		if req.DownMbps != nil {
			target.NetworkDownMbps = *req.DownMbps
			changed["down_mbps"] = target.NetworkDownMbps
		}
		if req.UpMbps != nil {
			target.NetworkUpMbps = *req.UpMbps
			changed["up_mbps"] = target.NetworkUpMbps
		}
		if req.TrafficQuotaGB != nil {
			target.MonthlyTrafficGB = *req.TrafficQuotaGB
			changed["traffic_quota_gb"] = target.MonthlyTrafficGB
		}
		if false {


		}
		if req.SnapshotLimit != nil {
			target.SnapshotLimit = idcLimitFromValueV2(*req.SnapshotLimit)
			changed["snapshot_limit"] = target.SnapshotLimit
		}
		if req.Remark != nil {
			target.Remark = *req.Remark
			changed["remark"] = target.Remark
		}
		if req.Tenant != nil {
			target.Tenant = *req.Tenant
			changed["tenant"] = target.Tenant
		}
		if req.ExpiresAt != nil {
			target.ExpiresAt = normalizeV2Date(*req.ExpiresAt)
			changed["expires_at"] = target.ExpiresAt
		}
		if req.FirewallEnabled != nil {
			target.FirewallEnabled = *req.FirewallEnabled
			changed["firewall_enabled"] = target.FirewallEnabled
		}
	})
	if len(changed) == 0 {
		v2BadRequest(w, r, "没有需要修改的字段", nil)
		return
	}
	detail, _ := json.Marshal(changed)
	auditRequest(r, "api.v2.instance.update", c.Name, string(detail), true, "")
	updated := config.FindContainer(c.ID)
	if updated == nil {
		v2NotFound(w, r, "实例不存在")
		return
	}
	v2OK(w, r, v2InstanceView(*updated))
}

func v2InstanceDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.Locked {
		v2Precondition(w, r, "实例已锁定，请先解锁")
		return
	}
	taskIDs := globalQueue.EnqueueBatchWithAudit("destroy", []int{c.ID}, "", v2AuthContext(r).Username, clientIP(r), r.UserAgent())
	auditRequest(r, "api.v2.instance.delete", c.Name, "删除实例", true, "")
	v2Accepted(w, r, map[string]interface{}{"task_ids": taskIDs, "id": c.ID, "name": c.Name})
}

// ---------------------------------------------------------------------------
// 动作
// ---------------------------------------------------------------------------

// v2InstancePower POST /instances/{id}/power {action}
// action: start | stop | restart | hard-stop | hard-restart | shutdown
func v2InstancePower(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:power") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	actionMap := map[string]string{
		"start": "start", "stop": "stop", "shutdown": "stop", "restart": "restart",
		"hard-stop": "hardoff", "hardoff": "hardoff", "hard-restart": "hard_reboot",
	}
	taskAction, valid := actionMap[action]
	if !valid {
		v2BadRequest(w, r, "action 取值非法", map[string]string{
			"action": "可选值：start, stop, shutdown, restart, hard-stop, hard-restart",
		})
		return
	}
	// 挂起/到期/超流量拦截（与面板一致：电源类操作全部禁止）。
	if taskAction == "start" || taskAction == "restart" {
		switch {
		case c.Suspended:
			v2Precondition(w, r, "实例已挂起（欠费停机），不允许开机")
			return
		case lxc.IsExpired(*c):
			v2Precondition(w, r, "实例已到期，不允许开机")
			return
		case lxc.IsTrafficExceeded(*c):
			v2Precondition(w, r, "实例流量已超限，不允许开机")
			return
		}
	}
	// 跨节点实例：代理到被控执行。
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			v2Upstream(w, r, "实例所属节点不可用")
			return
		}
		agentAction := map[string]string{"start": "start", "stop": "stop", "restart": "restart", "hardoff": "destroy", "hard_reboot": "restart"}[taskAction]
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/%s", c.ID, agentAction), nil)
		if err != nil {
			v2Upstream(w, r, "代理节点失败："+err.Error())
			return
		}
		auditRequest(r, "api.v2.instance.power", c.Name, "action="+action+" node="+node.Name, status < 300, "")
		if status >= 300 {
			v2Upstream(w, r, "节点执行失败："+strings.TrimSpace(string(data)))
			return
		}
		v2Accepted(w, r, map[string]interface{}{"id": c.ID, "action": action, "node_id": node.ID})
		return
	}
	// 本机：入任务队列（异步执行，返回 task_ids 供轮询）。
	taskIDs := globalQueue.EnqueueWithAudit(c.ID, c.Name, taskActionOfV2(taskAction), "", nil, v2AuthContext(r).Username, clientIP(r), r.UserAgent())
	auditRequest(r, "api.v2.instance.power", c.Name, "action="+action, true, "")
	v2Accepted(w, r, map[string]interface{}{"id": c.ID, "action": action, "task_ids": taskIDs})
}

func taskActionOfV2(action string) TaskType {
	switch action {
	case "start":
		return "start"
	case "stop":
		return "stop"
	case "restart":
		return "restart"
	case "hardoff":
		return "destroy"
	case "hard_reboot":
		return "restart"
	}
	return TaskType(action)
}

// v2InstanceReinstall POST /instances/{id}/reinstall {template_id, password?}
func v2InstanceReinstall(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:reinstall") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.Locked {
		v2Precondition(w, r, "实例已锁定，请先解锁")
		return
	}
	var req struct {
		TemplateID string `json:"template_id"`
		Password   string `json:"password"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if strings.TrimSpace(req.TemplateID) == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"template_id": "必填"})
		return
	}
	if lxc.FindTemplate(req.TemplateID) == nil && kvmImageExistsV2(req.TemplateID) == false {
		v2BadRequest(w, r, "镜像不存在："+req.TemplateID, nil)
		return
	}
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			v2Upstream(w, r, "实例所属节点不可用")
			return
		}
		body, _ := json.Marshal(map[string]string{"template_id": req.TemplateID, "password": req.Password})
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/reinstall", c.ID), strings.NewReader(string(body)))
		if err != nil {
			v2Upstream(w, r, "代理节点失败："+err.Error())
			return
		}
		if status >= 300 {
			v2Upstream(w, r, "节点重装失败："+strings.TrimSpace(string(data)))
			return
		}
		auditRequest(r, "api.v2.instance.reinstall", c.Name, "template="+req.TemplateID+" node="+node.Name, true, "")
		v2Accepted(w, r, map[string]interface{}{"id": c.ID, "template_id": req.TemplateID, "node_id": node.ID})
		return
	}
	taskIDs := globalQueue.EnqueueWithAudit(c.ID, c.Name, "reinstall", req.TemplateID, nil, v2AuthContext(r).Username, clientIP(r), r.UserAgent())
	auditRequest(r, "api.v2.instance.reinstall", c.Name, "template="+req.TemplateID, true, "")
	v2Accepted(w, r, map[string]interface{}{"id": c.ID, "template_id": req.TemplateID, "task_ids": taskIDs})
}

// v2InstanceResetPassword POST /instances/{id}/reset-password {password?, ssh_key_ids?}
func v2InstanceResetPassword(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:password") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		Password  string   `json:"password"`
		SSHKeyIDs []string `json:"ssh_key_ids"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	password := strings.TrimSpace(req.Password)
	if password == "" {
		password = generateRandomStr(16)
	}
	if err := lxc.ValidateCustomSSHPassword(password); err != nil {
		v2BadRequest(w, r, "密码不符合要求："+err.Error(), nil)
		return
	}
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			v2Upstream(w, r, "实例所属节点不可用")
			return
		}
		body, _ := json.Marshal(map[string]string{"password": password})
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/reset-password", c.ID), strings.NewReader(string(body)))
		if err != nil {
			v2Upstream(w, r, "代理节点失败："+err.Error())
			return
		}
		if status >= 300 {
			v2Upstream(w, r, "节点重置密码失败："+strings.TrimSpace(string(data)))
			return
		}
		auditRequest(r, "api.v2.instance.reset_password", c.Name, "node="+node.Name, true, "")
		v2OK(w, r, map[string]interface{}{"id": c.ID, "password": password, "node_id": node.ID})
		return
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) {
		target.SSHPassword = password
	})
	if err := quickSetContainerPassword(c.ID, password); err != nil {
		v2Upstream(w, r, "写入容器失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.reset_password", c.Name, "重置 root 密码", true, "")
	v2OK(w, r, map[string]interface{}{"id": c.ID, "password": password})
}

// v2InstanceConsole POST /instances/{id}/console {type: ssh|vnc}
// 返回一次性票据与连接地址（前端/客户端用票据换取 WebSocket 会话）。
func v2InstanceConsole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
	}
	_ = v2Decode(r, &req)
	consoleType := strings.ToLower(strings.TrimSpace(req.Type))
	if consoleType == "" {
		consoleType = "ssh"
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	switch consoleType {
	case "ssh", "webssh", "terminal":
		if !v2RequireScope(w, r, "terminal:ssh") {
			return
		}
		if c.Suspended {
			v2Precondition(w, r, "实例已挂起，控制台不可用")
			return
		}
		if c.Status != "running" {
			v2Precondition(w, r, "实例未运行，控制台不可用")
			return
		}
		ticket := randomHex(32)
		webSSHTickets.Lock()
		cleanupExpiredWebSSHTicketsLocked(time.Now())
		webSSHTickets.items[ticket] = webSSHTicket{
			ContainerName: c.Name,
			SubUser:       v2AuthContext(r).Type == authTypeSubUser,
			ClientIP:      clientIP(r),
			UserAgent:     r.UserAgent(),
			ExpiresAt:     time.Now().Add(60 * time.Second),
		}
		webSSHTickets.Unlock()
		scheme := "ws"
		if requestIsHTTPS(r) {
			scheme = "wss"
		}
		v2OK(w, r, map[string]interface{}{
			"type":      "ssh",
			"ticket":    ticket,
			"expires_in": 60,
			"url":       fmt.Sprintf("%s://%s/api/ssh?container=%s", scheme, r.Host, urlQueryEscapeV2(c.Name)),
			"subprotocol": "eyvescloud-ticket." + ticket,
			"container": c.Name,
		})
	case "vnc":
		if !v2RequireScope(w, r, "terminal:vnc") {
			return
		}
		if !c.IsKVM() {
			v2BadRequest(w, r, "仅 KVM 实例支持 VNC", map[string]string{"runtime": c.Runtime()})
			return
		}
		ticket := newVNCTicketV2(c, r)
		scheme := "ws"
		if requestIsHTTPS(r) {
			scheme = "wss"
		}
		v2OK(w, r, map[string]interface{}{
			"type":       "vnc",
			"ticket":     ticket,
			"expires_in": 60,
			"url":        fmt.Sprintf("%s://%s/api/vnc?container=%s&ticket=%s", scheme, r.Host, urlQueryEscapeV2(c.Name), ticket),
			"container":  c.Name,
		})
	default:
		v2BadRequest(w, r, "type 只能是 ssh 或 vnc", map[string]string{"type": consoleType})
	}
}

// ---------------------------------------------------------------------------
// 指标 / 用量 / 事件 / XML
// ---------------------------------------------------------------------------

func v2InstanceMetrics(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	point, has := latestContainerMetric(c.UUID)
	metrics := map[string]interface{}{
		"id":        c.ID,
		"name":      c.Name,
		"status":    v2InstanceStatus(*c),
		"timestamp": "",
	}
	if has {
		metrics["cpu_percent"] = point.CPU
		metrics["memory_percent"] = point.Memory
		metrics["network_rx_bps"] = point.NetworkRx
		metrics["network_tx_bps"] = point.NetworkTx
		metrics["disk_read_bps"] = point.DiskRead
		metrics["disk_write_bps"] = point.DiskWrite
		if point.TS > 0 {
			metrics["timestamp"] = time.Unix(point.TS, 0).Format(time.RFC3339)
		}
	}
	v2OK(w, r, metrics)
}

func v2InstanceUsage(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	usedGB := float64(c.TrafficUsedRX+c.TrafficUsedTX) / 1024 / 1024 / 1024
	percent := 0.0
	if c.MonthlyTrafficGB > 0 {
		percent = round2(usedGB / float64(c.MonthlyTrafficGB) * 100)
	}
	v2OK(w, r, map[string]interface{}{
		"id":              c.ID,
		"traffic_quota_gb": c.MonthlyTrafficGB,
		"traffic_used_gb":  round2(usedGB),
		"traffic_rx_gb":    round2(float64(c.TrafficUsedRX) / 1024 / 1024 / 1024),
		"traffic_tx_gb":    round2(float64(c.TrafficUsedTX) / 1024 / 1024 / 1024),
		"traffic_percent":  percent,
		"traffic_mode":     c.TrafficMode,
		"reset_date":       c.TrafficResetDate,
		"expires_at":       v2Time(c.ExpiresAt),
		"expired":          lxc.IsExpired(*c),
		"traffic_exceeded": lxc.IsTrafficExceeded(*c),
	})
}

func v2InstanceEvents(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	logs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()
	events := []map[string]interface{}{}
	for i := len(logs) - 1; i >= 0; i-- {
		entry := logs[i]
		if !strings.Contains(entry.Target, c.Name) && !strings.Contains(entry.Detail, c.Name) {
			continue
		}
		events = append(events, map[string]interface{}{
			"time":    v2Time(entry.Time),
			"action":  entry.Action,
			"target":  entry.Target,
			"detail":  entry.Detail,
			"actor":   entry.User,
			"ip":      entry.IP,
			"success": entry.Success,
			"error":   entry.Error,
		})
	}
	total := len(events)
	start, end := query.Slice(total)
	v2List(w, r, events[start:end], query, total)
}

func v2InstanceXML(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if !c.IsKVM() {
		v2BadRequest(w, r, "仅 KVM 实例提供 libvirt XML", map[string]string{"runtime": c.Runtime()})
		return
	}
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			v2Upstream(w, r, "实例所属节点不可用")
			return
		}
		data, status, err := proxyNodeRequest(r, node, http.MethodGet,
			fmt.Sprintf("/api/agent/containers/%d/xml", c.ID), nil)
		if err != nil {
			v2Upstream(w, r, "代理节点失败："+err.Error())
			return
		}
		if status >= 300 {
			v2Upstream(w, r, "节点读取 XML 失败："+strings.TrimSpace(string(data)))
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	xml, err := kvmDomainXMLV2(c.VirshName())
	if err != nil {
		v2Upstream(w, r, "读取 libvirt XML 失败："+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml))
}

// ---------------------------------------------------------------------------
// 快照 / 备份 / 克隆 / 救援 / 锁定 / 网络 / 迁移
// ---------------------------------------------------------------------------

func v2InstanceSnapshotList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	query := v2ParsePage(r)
	items := v2SnapshotViews(*c)
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2InstanceSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.Suspended {
		v2Precondition(w, r, "实例已挂起，不允许创建快照")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = v2Decode(r, &req)
	if c.SnapshotLimit > 0 && len(config.ContainerSnapshots(c.ID)) >= c.SnapshotLimit {
		v2Precondition(w, r, fmt.Sprintf("快照数量已达上限（%d）", c.SnapshotLimit))
		return
	}
	snapshot, err := createSnapshotForContainer(c.ID, req.Name, v2AuthContext(r).Username)
	if err != nil {
		v2Internal(w, r, "创建快照失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.snapshot.create", c.Name, "快照 "+snapshot.ID, true, "")
	v2Created(w, r, v2SnapshotView(snapshot))
}

func v2InstanceSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:restore") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	snapshotID := strings.TrimSpace(r.PathValue("sid"))
	if err := restoreSnapshotForContainer(c.ID, snapshotID); err != nil {
		v2BadRequest(w, r, "恢复快照失败："+err.Error(), nil)
		return
	}
	auditRequest(r, "api.v2.instance.snapshot.restore", c.Name, "快照 "+snapshotID, true, "")
	v2Accepted(w, r, map[string]interface{}{"id": c.ID, "snapshot_id": snapshotID})
}

func v2InstanceSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:delete") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	snapshotID := strings.TrimSpace(r.PathValue("sid"))
	if err := deleteSnapshotForContainer(c.ID, snapshotID); err != nil {
		v2BadRequest(w, r, "删除快照失败："+err.Error(), nil)
		return
	}
	auditRequest(r, "api.v2.instance.snapshot.delete", c.Name, "快照 "+snapshotID, true, "")
	v2NoContent(w, r)
}

func v2InstanceBackupList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	query := v2ParsePage(r)
	items := v2BackupViews(*c)
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2InstanceBackupCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	backup, err := createBackupForContainer(c.ID, v2AuthContext(r).Username)
	if err != nil {
		v2Internal(w, r, "创建备份失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.backup.create", c.Name, "备份 "+backup.ID, true, "")
	v2Created(w, r, v2BackupView(backup))
}

func v2InstanceBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:delete") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	backupID := strings.TrimSpace(r.PathValue("bid"))
	if err := deleteBackupForContainer(c.ID, backupID); err != nil {
		v2BadRequest(w, r, "删除备份失败："+err.Error(), nil)
		return
	}
	auditRequest(r, "api.v2.instance.backup.delete", c.Name, "备份 "+backupID, true, "")
	v2NoContent(w, r)
}

func v2InstanceClone(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"name": "必填"})
		return
	}
	if !config.IsValidContainerNameSyntax(req.Name) {
		v2BadRequest(w, r, "实例名称不合法（1-63 位，字母数字与 . _ -）", map[string]string{"name": req.Name})
		return
	}
	if config.FindContainerByName(req.Name) != nil {
		v2Conflict(w, r, "实例名称已存在："+req.Name)
		return
	}
	if err := cloneContainerForAPI(c.ID, req.Name, v2AuthContext(r).Username); err != nil {
		v2Internal(w, r, "克隆失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.clone", c.Name, "克隆为 "+req.Name, true, "")
	v2Accepted(w, r, map[string]interface{}{"source_id": c.ID, "name": req.Name})
}

func v2InstanceRescueEnter(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:reinstall") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if !c.IsKVM() {
		v2BadRequest(w, r, "救援模式仅 KVM 实例支持", map[string]string{"runtime": c.Runtime()})
		return
	}
	var req struct {
		ISOID string `json:"iso_id"`
	}
	_ = v2Decode(r, &req)
	isoID := strings.TrimSpace(req.ISOID)
	if isoID == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"iso_id": "必填（ISO 目录 ID）"})
		return
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) {
		target.RescueEnabled = true
		target.RescueISOID = isoID
	})
	if err := applyRescueForContainer(c.ID, true); err != nil {
		v2Upstream(w, r, "进入救援模式失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.rescue.enter", c.Name, "ISO "+isoID, true, "")
	v2Accepted(w, r, map[string]interface{}{"id": c.ID, "rescue": true, "iso_id": isoID})
}

func v2InstanceRescueExit(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:reinstall") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if err := applyRescueForContainer(c.ID, false); err != nil {
		v2Upstream(w, r, "退出救援模式失败："+err.Error())
		return
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) {
		target.RescueEnabled = false
		target.RescueISOID = ""
		target.RescueISOPath = ""
	})
	auditRequest(r, "api.v2.instance.rescue.exit", c.Name, "退出救援模式", true, "")
	v2OK(w, r, map[string]interface{}{"id": c.ID, "rescue": false})
}

func v2InstanceLock(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) { target.Locked = true })
	auditRequest(r, "api.v2.instance.lock", c.Name, "锁定实例", true, "")
	v2OK(w, r, map[string]interface{}{"id": c.ID, "locked": true})
}

func v2InstanceUnlock(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) { target.Locked = false })
	auditRequest(r, "api.v2.instance.unlock", c.Name, "解锁实例", true, "")
	v2OK(w, r, map[string]interface{}{"id": c.ID, "locked": false})
}

// v2InstanceNetworkUpdate PUT /instances/{id}/network
// 支持三类变更：NAT 端口映射整体替换、公网 IPv4 数量、IPv6 数量。
func v2InstanceNetworkUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:network") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		NATPorts []struct {
			ContainerPort int    `json:"container_port"`
			HostPort      int    `json:"host_port"`
			Protocol      string `json:"protocol"`
			Description   string `json:"description"`
		} `json:"nat_ports"`
		PublicIPv4Count *int `json:"public_ipv4_count"`
		IPv6Count       *int `json:"ipv6_count"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	changed := map[string]interface{}{}
	if req.NATPorts != nil {
		mappings := make([]config.PortMapping, 0, len(req.NATPorts))
		for _, item := range req.NATPorts {
			protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
			if protocol != "tcp" && protocol != "udp" {
				v2BadRequest(w, r, "nat_ports.protocol 只能是 tcp 或 udp",
					map[string]string{"protocol": item.Protocol})
				return
			}
			if item.ContainerPort <= 0 || item.ContainerPort > 65535 {
				v2BadRequest(w, r, "nat_ports.container_port 非法", map[string]string{"container_port": strconv.Itoa(item.ContainerPort)})
				return
			}
			mappings = append(mappings, config.PortMapping{
				ContainerPort: item.ContainerPort,
				HostPort:      item.HostPort,
				Protocol:      protocol,
				Description:   item.Description,
			})
		}
		if c.PortMappingLimit > 0 && len(mappings) > c.PortMappingLimit {
			v2Precondition(w, r, fmt.Sprintf("端口映射数量超出上限（%d）", c.PortMappingLimit))
			return
		}
		config.MutateContainerByID(c.ID, func(target *config.Container) { target.PortMappings = mappings })
		if err := applyPortMappingsForContainer(c.ID); err != nil {
			v2Upstream(w, r, "应用端口映射失败："+err.Error())
			return
		}
		changed["nat_ports"] = len(mappings)
	}
	if req.PublicIPv4Count != nil {
		if err := setPublicIPv4CountForContainer(c.ID, *req.PublicIPv4Count); err != nil {
			v2BadRequest(w, r, "调整公网 IPv4 失败："+err.Error(), nil)
			return
		}
		changed["public_ipv4_count"] = *req.PublicIPv4Count
	}
	if req.IPv6Count != nil {
		if err := setIPv6CountForContainer(c.ID, *req.IPv6Count); err != nil {
			v2BadRequest(w, r, "调整 IPv6 失败："+err.Error(), nil)
			return
		}
		changed["ipv6_count"] = *req.IPv6Count
	}
	if len(changed) == 0 {
		v2BadRequest(w, r, "没有需要修改的网络配置", nil)
		return
	}
	auditRequest(r, "api.v2.instance.network", c.Name, fmt.Sprintf("%v", changed), true, "")
	updated := config.FindContainer(c.ID)
	if updated == nil {
		v2NotFound(w, r, "实例不存在")
		return
	}
	v2OK(w, r, v2InstanceView(*updated))
}

// ---------------------------------------------------------------------------
// 批量操作
// ---------------------------------------------------------------------------

// v2InstancesBatch POST /instances/batch {action, ids: [...]}
// action: power（需 params.action）/ delete / reinstall（需 template_id）
func v2InstancesBatch(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:power") {
		return
	}
	var req struct {
		Action     string          `json:"action"`
		IDs        []int           `json:"ids"`
		Params     json.RawMessage `json:"params"`
		TemplateID string          `json:"template_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if len(req.IDs) == 0 {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"ids": "必填（实例 ID 数组）"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	allowed := []int{}
	skipped := []map[string]interface{}{}
	for _, id := range req.IDs {
		c := config.FindContainer(id)
		if c == nil {
			skipped = append(skipped, map[string]interface{}{"id": id, "reason": "不存在"})
			continue
		}
		if !isContainerAllowedForRequest(r, c.UUID) {
			skipped = append(skipped, map[string]interface{}{"id": id, "reason": "无权访问"})
			continue
		}
		if c.Locked && action != "power" {
			skipped = append(skipped, map[string]interface{}{"id": id, "reason": "已锁定"})
			continue
		}
		allowed = append(allowed, id)
	}
	if len(allowed) == 0 {
		v2Precondition(w, r, "没有可操作的实例")
		return
	}

	switch action {
	case "power":
		var params struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(req.Params, &params)
		taskAction, valid := map[string]string{
			"start": "start", "stop": "stop", "shutdown": "stop", "restart": "restart",
			"hard-stop": "hardoff", "hard-restart": "hard_reboot",
		}[strings.ToLower(strings.TrimSpace(params.Action))]
		if !valid {
			v2BadRequest(w, r, "params.action 取值非法",
				map[string]string{"action": "start, stop, shutdown, restart, hard-stop, hard-restart"})
			return
		}
		taskIDs := globalQueue.EnqueueBatchWithAudit(taskActionOfV2(taskAction), allowed, "", v2AuthContext(r).Username, clientIP(r), r.UserAgent())
		auditRequest(r, "api.v2.instances.batch.power", fmt.Sprintf("%v", allowed), "批量 "+params.Action, true, "")
		v2Accepted(w, r, map[string]interface{}{"task_ids": taskIDs, "count": len(allowed), "skipped": skipped})
	case "delete":
		if !v2RequireScope(w, r, "container:create") {
			return
		}
		taskIDs := globalQueue.EnqueueBatchWithAudit("destroy", allowed, "", v2AuthContext(r).Username, clientIP(r), r.UserAgent())
		auditRequest(r, "api.v2.instances.batch.delete", fmt.Sprintf("%v", allowed), "批量删除", true, "")
		v2Accepted(w, r, map[string]interface{}{"task_ids": taskIDs, "count": len(allowed), "skipped": skipped})
	case "reinstall":
		if !v2RequireScope(w, r, "container:reinstall") {
			return
		}
		if strings.TrimSpace(req.TemplateID) == "" {
			v2BadRequest(w, r, "缺少必填字段", map[string]string{"template_id": "批量重装必填"})
			return
		}
		taskIDs := globalQueue.EnqueueBatchWithAudit("reinstall", allowed, req.TemplateID, v2AuthContext(r).Username, clientIP(r), r.UserAgent())
		auditRequest(r, "api.v2.instances.batch.reinstall", fmt.Sprintf("%v", allowed), "批量重装 "+req.TemplateID, true, "")
		v2Accepted(w, r, map[string]interface{}{"task_ids": taskIDs, "count": len(allowed), "skipped": skipped})
	default:
		v2BadRequest(w, r, "action 取值非法", map[string]string{"action": "power, delete, reinstall"})
	}
}
