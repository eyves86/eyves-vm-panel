package api

// apiv2_migrate.go —— API v2：实例迁移（跨节点重建）。
//
// 语义说明（重要）：
//
//	本产品的「迁移」是**配置迁移**：把实例的资源配置与网络参数在目标节点上重建，
//	磁盘数据不随迁移传输（当前未接入块级复制驱动，`internal/livemigrate` 仅有
//	编排骨架、需注入 storage/libvirt 驱动才可用）。
//
//	因此要求：源实例必须处于**停止**状态（避免"数据没过去但仍在跑"的误解），
//	目标节点在线且非维护模式，且不能是同一节点。
//
// 端点：
//
//	POST /api/v2/instances/{id}/migrate  {target_node_id, mode: "move"|"copy"}
//	     mode=move（默认）：目标节点创建成功后删除源实例；
//	     mode=copy：保留源实例（用于快速复制环境）。
//	GET  /api/v2/instances/{id}/migrate  查询可迁移目标节点与前置条件（不做任务状态跟踪）

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
)

func init() {
	registerV2("POST /api/v2/instances/{id}/migrate", v2Auth(v2InstanceMigrate))
	registerV2("GET /api/v2/instances/{id}/migrate", v2Auth(v2InstanceMigratePlan))
}

// v2MigrateRequest 迁移请求体。
type v2MigrateRequest struct {
	TargetNodeID string `json:"target_node_id"`
	// Mode: move（默认，迁移后删除源实例）/ copy（保留源实例）。
	Mode string `json:"mode"`
	// StartAfter 目标实例创建完成后是否立刻开机（默认 false）。
	StartAfter bool `json:"start_after"`
}

// v2InstanceMigratePlan GET：返回迁移前置条件与可选目标节点，便于集成方先探再迁。
func v2InstanceMigratePlan(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	targets := []map[string]interface{}{}
	config.AppConfigMu.RLock()
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()
	for _, n := range nodes {
		if n.ID == c.NodeID {
			continue
		}
		reason := ""
		switch {
		case n.MaintenanceMode:
			reason = "节点处于维护模式"
		case n.Status != "" && n.Status != "online":
			reason = "节点不在线"
		case n.Address == "":
			reason = "节点未配置地址"
		}
		targets = append(targets, map[string]interface{}{
			"node_id": n.ID, "node_name": n.Name, "status": n.Status,
			"healthy": reason == "", "reason": reason,
		})
	}
	blockers := []string{}
	if v2InstanceStatus(*c) == "running" {
		blockers = append(blockers, "源实例正在运行：配置迁移不传输磁盘数据，请先关机后再迁移")
	}
	if c.Locked {
		blockers = append(blockers, "源实例已锁定，请先解锁")
	}
	// 存储视角的迁移可行性（集成方最关心"数据会不会过去"）：
	//   - 共享存储（NFS/CephFS/RBD/共享 dir）：目标节点能看到同一份数据，
	//     理论上可免传输迁移；但"接管既有 rootfs"能力尚未实现，当前仍走配置重建。
	//   - 本地存储：必须自行同步数据。
	pool := config.StoragePoolByID(c.StoragePoolID)
	sourcePoolShared := pool != nil && pool.Shared
	dataPlan := map[string]interface{}{
		"mode":                  "config-only",
		"data_transferred":      false,
		"source_pool_id":        c.StoragePoolID,
		"source_pool_shared":    sourcePoolShared,
		"target_can_reuse_data": false,
	}
	if sourcePoolShared {
		dataPlan["note"] = "源实例位于共享存储池：目标节点可直接访问同一份数据，但接管既有 rootfs 的能力尚未实现，当前仍按配置重建处理"
	} else {
		dataPlan["note"] = "源实例位于本地存储：配置迁移不复制磁盘数据，请自行同步数据（如 rsync / 从快照恢复）后再启用"
	}
	v2OK(w, r, map[string]interface{}{
		"instance_id":       c.ID,
		"instance_name":     c.Name,
		"current_node_id":   c.NodeID,
		"current_node_name": idcNodeNameV2(c.NodeID),
		"migration_kind":    "config-only",
		"data_migrated":     false,
		"storage":           dataPlan,
		"note":              "本产品迁移为配置重建：实例规格与网络参数会复制到目标节点，磁盘内数据不传输（需自行同步数据）。",
		"blockers":          blockers,
		"targets":           targets,
	})
}

// v2InstanceMigrate POST：在目标节点重建实例（可选删除源实例）。
func v2InstanceMigrate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req v2MigrateRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	targetID := strings.TrimSpace(req.TargetNodeID)
	if targetID == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"target_node_id": "必填"})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "move"
	}
	if mode != "move" && mode != "copy" {
		v2BadRequest(w, r, "mode 只能是 move 或 copy", map[string]string{"mode": req.Mode})
		return
	}

	// —— 前置条件 ——
	if c.Locked {
		v2Precondition(w, r, "源实例已锁定，请先解锁再迁移")
		return
	}
	if c.NodeID == targetID {
		v2Conflict(w, r, "目标节点与当前节点相同，无需迁移")
		return
	}
	status := v2InstanceStatus(*c)
	if status == "running" {
		v2Precondition(w, r, "源实例正在运行：配置迁移不传输磁盘数据，请先关机后再迁移")
		return
	}
	node, ok := config.FindNode(targetID)
	if !ok {
		v2NotFound(w, r, "目标节点不存在："+targetID)
		return
	}
	if node.MaintenanceMode {
		v2Precondition(w, r, "目标节点处于维护模式，禁止发机")
		return
	}
	if node.Status != "" && node.Status != "online" {
		v2Precondition(w, r, "目标节点不在线："+node.Name)
		return
	}
	if node.Address == "" {
		v2Precondition(w, r, "目标节点未配置地址，无法代理访问")
		return
	}
	// 目标节点上不能已有同名实例（避免覆盖既有工作负载）。
	if existing := findInstanceOnNodeV2(node.ID, c.Name); existing != nil {
		v2Conflict(w, r, fmt.Sprintf("目标节点上已存在同名实例 %s（ID %d）", c.Name, existing.ID))
		return
	}

	// —— 组装目标节点的创建配置（可移植子集 + 明文口令，保证迁移后仍可登录）——
	portable := buildMigrateContainer(*c)
	cfg := v2MigrateConfigFrom(portable)
	body, err := json.Marshal(cfg)
	if err != nil {
		v2Internal(w, r, "序列化迁移配置失败："+err.Error())
		return
	}
	data, statusCode, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/containers/create", bytes.NewReader(body))
	if err != nil || statusCode >= 300 {
		msg := fmt.Sprintf("目标节点创建失败（HTTP %d）：%v", statusCode, err)
		if len(data) > 0 {
			msg = fmt.Sprintf("目标节点创建失败：%s", strings.TrimSpace(string(data)))
		}
		v2Upstream(w, r, msg)
		return
	}
	var created struct {
		Data map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(data, &created)

	// —— move：删除源实例（异步任务，等容器真正销毁）——
	var deleteTasks []string
	if mode == "move" {
		deleteTasks = globalQueue.EnqueueBatchWithAudit(TaskDelete, []int{c.ID}, "", v2AuthContext(r).Username, clientIP(r), r.UserAgent())
	}
	// —— start_after：目标实例创建后立刻开机 ——
	if req.StartAfter {
		_ = proxyStartOnNode(r, node, created.Data)
	}

	auditRequest(r, "api.v2.instance.migrate", c.Name,
		fmt.Sprintf("mode=%s → 节点 %s（配置迁移，数据不迁移）", mode, node.Name), true, "")
	v2Accepted(w, r, map[string]interface{}{
		"instance_id":      c.ID,
		"instance_name":    c.Name,
		"mode":             mode,
		"target_node_id":   node.ID,
		"target_node_name": node.Name,
		"migration_kind":   "config-only",
		"data_migrated":    false,
		"new_instance":     created.Data,
		"source_deleted":   mode == "move",
		"delete_task_ids":  deleteTasks,
		"note":             "实例已在目标节点重建；磁盘数据未迁移，请自行同步数据后启用。",
		"storage": map[string]interface{}{
			"source_pool_id":     c.StoragePoolID,
			"source_pool_shared": func() bool { p := config.StoragePoolByID(c.StoragePoolID); return p != nil && p.Shared }(),
			"data_transferred":   false,
		},
	})
}

// v2MigrateConfigFrom 把可移植配置转换为目标节点的创建请求。
// 刻意不带：存储池 ID（目标节点池不同）、公网 IP 分配（地址池按节点独立）、
// NAT 端口映射（主机端口由目标节点重新分配）。
func v2MigrateConfigFrom(mc migrateContainer) lxc.ContainerConfig {
	assignNAT := true
	cfg := lxc.ContainerConfig{
		Name:              mc.Name,
		Virtualization:    config.NormalizeVirtualization(mc.Virtualization),
		TemplateID:        mc.Template,
		VCPU:              mc.VCPU,
		RAMMB:             mc.RAMMB,
		DiskGB:            mc.DiskGB,
		DataDiskGB:        mc.DataDiskGB,
		DataDiskMountPath: mc.DataDiskMountPath,
		NetworkBWMbps:     mc.NetworkBWMbps,
		NetworkDownMbps:   mc.NetworkDownMbps,
		NetworkUpMbps:     mc.NetworkUpMbps,
		MonthlyTrafficGB:  mc.MonthlyTrafficGB,
		TrafficMode:       mc.TrafficMode,
		TrafficInGB:       mc.TrafficInGB,
		TrafficOutGB:      mc.TrafficOutGB,
		IOSpeedMBps:       mc.IOSpeedMBps,
		IOReadMBps:        mc.IOReadMBps,
		IOWriteMBps:       mc.IOWriteMBps,
		LANIPv4Mode:       mc.LANIPv4Mode,
		LANInterface:      mc.LANInterface,
		LANIPv4Address:    mc.LANIPv4Address,
		LANIPv4PrefixLen:  mc.LANIPv4PrefixLen,
		LANIPv4Gateway:    mc.LANIPv4Gateway,
		AssignNAT:         &assignNAT,
		PortMappingCount:  2,
		SnapshotLimit:     mc.SnapshotLimit,
		SSHAuthMode:       mc.SSHAuthMode,
		SSHPassword:       mc.SSHPassword, // 明文随请求下发到被控（被控侧设置到客户机）
		SSHPublicKey:      mc.SSHPublicKey,
		CloudInitUserData: mc.CloudInitUserData,
	}
	if cfg.SSHAuthMode == "" {
		cfg.SSHAuthMode = "password"
	}
	return cfg
}

// findInstanceOnNodeV2 在指定节点上按名称查找实例（主控侧记录）。
func findInstanceOnNodeV2(nodeID, name string) *config.Container {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		if c.NodeID == nodeID && strings.EqualFold(c.Name, name) {
			copyC := *c
			return &copyC
		}
	}
	return nil
}

// proxyStartOnNode 在目标节点上开机（best-effort，失败不影响迁移结果返回）。
func proxyStartOnNode(r *http.Request, node config.Node, created map[string]interface{}) error {
	idFloat, ok := created["id"].(float64)
	if !ok {
		return fmt.Errorf("目标实例 ID 缺失")
	}
	_, _, err := proxyNodeRequest(r, node, http.MethodPost,
		fmt.Sprintf("/api/agent/containers/%d/start", int(idFloat)), nil)
	return err
}

// idcNodeNameV2 节点名称（找不到时回退为 ID 或“本机”）。
func idcNodeNameV2(nodeID string) string {
	if nodeID == "" {
		return "本机"
	}
	if n, ok := config.FindNode(nodeID); ok {
		return n.Name
	}
	return nodeID
}
