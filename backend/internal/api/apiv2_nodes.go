package api

// apiv2_nodes.go —— API v2：节点 / 节点分组 / 区域 / 放置调度。
//
//	GET    /api/v2/nodes                      节点列表（状态/维护/分组/区域过滤 + summary）
//	POST   /api/v2/nodes                      添加节点（quick 一键接入 / manual 手工接入）
//	GET    /api/v2/nodes/schedule             放置调度决策（过滤+评分+候选理由）
//	GET    /api/v2/nodes/{id}                 节点详情（脱敏，不含 token）
//	PATCH  /api/v2/nodes/{id}                 修改节点（名称/地址/TLS/分组/区域）
//	DELETE /api/v2/nodes/{id}                 删除节点
//	POST   /api/v2/nodes/{id}/maintenance     维护模式开关 {enabled}
//	POST   /api/v2/nodes/{id}/install-key     换发一次性安装密钥（返回接入命令）
//	GET    /api/v2/nodes/{id}/metrics         节点资源指标（心跳上报值）
//	GET    /api/v2/nodes/{id}/instances       该节点上的实例（按主控记录）
//	GET    /api/v2/node-groups                分组列表
//	POST   /api/v2/node-groups                创建分组
//	PATCH  /api/v2/node-groups/{id}           修改分组
//	DELETE /api/v2/node-groups/{id}           删除分组
//	PUT    /api/v2/node-groups/{id}/nodes     设置分组成员（整体替换）
//	GET    /api/v2/regions                    区域列表
//	POST   /api/v2/regions                    创建区域
//	PATCH  /api/v2/regions/{id}               修改区域
//	DELETE /api/v2/regions/{id}               删除区域

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/scheduler"
)

func init() {
	registerV2("GET /api/v2/nodes", v2Auth(v2NodesList))
	registerV2("POST /api/v2/nodes", v2Auth(v2NodesCreate))
	registerV2("GET /api/v2/nodes/schedule", v2Auth(v2NodesSchedule))
	registerV2("GET /api/v2/nodes/{id}", v2Auth(v2NodeGet))
	registerV2("PATCH /api/v2/nodes/{id}", v2Auth(v2NodeUpdate))
	registerV2("DELETE /api/v2/nodes/{id}", v2Auth(v2NodeDelete))
	registerV2("POST /api/v2/nodes/{id}/maintenance", v2Auth(v2NodeMaintenance))
	registerV2("POST /api/v2/nodes/{id}/install-key", v2Auth(v2NodeInstallKey))
	registerV2("GET /api/v2/nodes/{id}/metrics", v2Auth(v2NodeMetrics))
	registerV2("GET /api/v2/nodes/{id}/instances", v2Auth(v2NodeInstances))

	registerV2("GET /api/v2/node-groups", v2Auth(v2NodeGroupsList))
	registerV2("POST /api/v2/node-groups", v2Auth(v2NodeGroupsCreate))
	registerV2("PATCH /api/v2/node-groups/{id}", v2Auth(v2NodeGroupUpdate))
	registerV2("DELETE /api/v2/node-groups/{id}", v2Auth(v2NodeGroupDelete))
	registerV2("PUT /api/v2/node-groups/{id}/nodes", v2Auth(v2NodeGroupSetNodes))

	registerV2("GET /api/v2/regions", v2Auth(v2RegionsList))
	registerV2("POST /api/v2/regions", v2Auth(v2RegionsCreate))
	registerV2("PATCH /api/v2/regions/{id}", v2Auth(v2RegionUpdate))
	registerV2("DELETE /api/v2/regions/{id}", v2Auth(v2RegionDelete))
}

// v2NodeView 节点对外契约（不含 token / install_key 等凭据，密钥最小化）。
func v2NodeView(n config.Node) map[string]interface{} {
	view := sanitizeNode(n)
	view["maintenance"] = n.MaintenanceMode
	view["maintenance_since"] = v2Time(n.MaintenanceSince)
	view["tls_skip_verify"] = n.TLSSkipVerify
	view["last_seen"] = v2Time(n.LastSeen)
	view["created_at"] = v2Time(n.CreatedAt)
	view["region_name"] = v2RegionName(n.RegionID)
	view["node_group_name"] = v2NodeGroupName(n.NodeGroupID)
	if n.RAMTotalMB > 0 {
		view["memory_used_percent"] = round2(float64(n.RAMUsedMB) / float64(n.RAMTotalMB) * 100)
	}
	if n.DiskTotalGB > 0 {
		view["disk_used_percent"] = round2(n.DiskUsedGB / n.DiskTotalGB * 100)
	}
	return view
}

func v2RegionName(regionID string) string {
	if regionID == "" {
		return ""
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, region := range config.AppConfig.Regions {
		if region.ID == regionID {
			return region.Name
		}
	}
	return regionID
}

func v2NodeGroupName(groupID string) string {
	if groupID == "" {
		return ""
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, group := range config.AppConfig.NodeGroups {
		if group.ID == groupID {
			return group.Name
		}
	}
	return groupID
}

func v2FindNode(w http.ResponseWriter, r *http.Request) (config.Node, bool) {
	nodeID := strings.TrimSpace(r.PathValue("id"))
	node, ok := config.FindNode(nodeID)
	if !ok {
		v2NotFound(w, r, "节点不存在："+nodeID)
		return config.Node{}, false
	}
	return node, true
}

func v2NodesList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	query := v2ParsePage(r)
	params := r.URL.Query()

	config.AppConfigMu.RLock()
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()

	if raw := strings.TrimSpace(params.Get("status")); raw != "" {
		wanted := map[string]bool{}
		for _, s := range strings.Split(raw, ",") {
			wanted[strings.ToLower(strings.TrimSpace(s))] = true
		}
		filtered := nodes[:0]
		for _, n := range nodes {
			if wanted[strings.ToLower(n.Status)] {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if raw := strings.TrimSpace(params.Get("maintenance")); raw != "" {
		want := strings.EqualFold(raw, "true") || raw == "1"
		filtered := nodes[:0]
		for _, n := range nodes {
			if n.MaintenanceMode == want {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if raw := strings.TrimSpace(params.Get("region_id")); raw != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if n.RegionID == raw {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if raw := strings.TrimSpace(params.Get("node_group_id")); raw != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if n.NodeGroupID == raw {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if strings.Contains(strings.ToLower(n.Name), keyword) ||
				strings.Contains(strings.ToLower(n.Address), keyword) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	sortSliceStable(nodes, func(a, b config.Node) bool {
		switch strings.ToLower(query.Sort) {
		case "name":
			if query.Desc {
				return a.Name > b.Name
			}
			return a.Name < b.Name
		case "containers", "container_count":
			if query.Desc {
				return a.ContainerCount > b.ContainerCount
			}
			return a.ContainerCount < b.ContainerCount
		}
		if query.Desc {
			return a.ID > b.ID
		}
		return a.ID < b.ID
	})

	summary := map[string]int{"online": 0, "offline": 0, "pending": 0, "maintenance": 0}
	for _, n := range nodes {
		switch strings.ToLower(n.Status) {
		case "online":
			summary["online"]++
		case "offline":
			summary["offline"]++
		default:
			summary["pending"]++
		}
		if n.MaintenanceMode {
			summary["maintenance"]++
		}
	}

	total := len(nodes)
	start, end := query.Slice(total)
	items := make([]map[string]interface{}, 0, end-start)
	for _, n := range nodes[start:end] {
		items = append(items, v2NodeView(n))
	}
	v2ListWithSummary(w, r, items, query, total, summary)
}

// v2NodesCreate POST /api/v2/nodes
type v2CreateNodeRequest struct {
	Name          string `json:"name"`
	Address       string `json:"address"`
	Mode          string `json:"mode"`       // quick（默认，一键接入）| manual（手工放置 agent.json）
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	AllowPrivate  bool   `json:"allow_private"`
	BindIP        string `json:"bind_ip"`    // 可选：把一次性密钥绑定到被控出口 IP
	RegionID      string `json:"region_id"`
	NodeGroupID   string `json:"node_group_id"`
}

func v2NodesCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	var req v2CreateNodeRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "quick"
	}
	if mode != "quick" && mode != "manual" {
		v2BadRequest(w, r, "mode 只能是 quick 或 manual", map[string]string{"mode": mode})
		return
	}
	address := strings.TrimSpace(req.Address)
	if mode == "manual" {
		if address == "" {
			v2BadRequest(w, r, "manual 模式必须提供被控面板地址", map[string]string{"address": "必填"})
			return
		}
		if err := validateNodeAddress(normalizeNodeAddress(address), req.AllowPrivate); err != nil {
			v2BadRequest(w, r, err.Error(), map[string]string{"address": address})
			return
		}
	}
	if req.RegionID != "" {
		config.AppConfigMu.RLock()
		found := false
		for _, region := range config.AppConfig.Regions {
			if region.ID == req.RegionID {
				found = true
				break
			}
		}
		config.AppConfigMu.RUnlock()
		if !found {
			v2NotFound(w, r, "区域不存在："+req.RegionID)
			return
		}
	}

	node := config.Node{
		ID:               newNodeID(),
		Name:             strings.TrimSpace(req.Name),
		Address:          normalizeNodeAddress(address),
		Token:            randomNodeSecret(32),
		Status:           "pending",
		CreatedAt:        time.Now().Format("2006-01-02 15:04:05"),
		TLSSkipVerify:    req.TLSSkipVerify,
		AllowPrivateAddr: req.AllowPrivate,
		RegionID:         req.RegionID,
		NodeGroupID:      req.NodeGroupID,
	}
	if mode == "quick" {
		node.InstallKey = randomNodeSecret(32)
		node.InstallKeyCreatedAt = time.Now().UTC().Format(time.RFC3339)
		node.InstallKeyIP = strings.TrimSpace(req.BindIP)
	}
	if err := config.AddNode(node); err != nil {
		v2Internal(w, r, "保存节点失败："+err.Error())
		return
	}

	data := map[string]interface{}{"node": v2NodeView(node), "mode": mode}
	if mode == "quick" {
		baseURL := externalBaseURL(r)
		script := buildAgentInstallScript(baseURL, node.InstallKey, node.Name, "")
		sum := sha256.Sum256([]byte(script))
		data["install_key"] = node.InstallKey
		data["install_command"] = fmt.Sprintf(
			"curl -fsSL -H \"X-Install-Key: %s\" %s/api/nodes/%s/install-script | sudo bash",
			node.InstallKey, baseURL, node.ID)
		data["install_script_sha256"] = hex.EncodeToString(sum[:])
		data["install_key_expires_at"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	} else {
		data["agent_config"] = map[string]interface{}{
			"controller": baseURLForNode(r),
			"node_id":    node.ID,
			"token":      node.Token, // 仅此一次明文下发，用于手工放置 agent.json
			"name":       node.Name,
			"address":    node.Address,
		}
		data["deploy_hint"] = "将被控机数据目录下 agent.json 写入上述内容，然后启动 eyvescloud agent 服务"
	}
	auditRequest(r, "api.v2.node.create", node.Name, "模式 "+mode, true, "")
	v2Created(w, r, data)
}

func baseURLForNode(r *http.Request) string {
	base := externalBaseURL(r)
	if strings.HasPrefix(strings.ToLower(base), "https://") {
		return base
	}
	return base
}

// v2NodesSchedule GET /api/v2/nodes/schedule?ram_mb=&disk_gb=&virt=&storage=&count=
func v2NodesSchedule(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	params := r.URL.Query()
	request := scheduler.Request{
		RAMMB:               int64(parseIntDefaultV2(params.Get("ram_mb"), 0)),
		DiskGB:              parseFloatDefaultV2(params.Get("disk_gb"), 0),
		ContainerCountDelta: parseIntDefaultV2(params.Get("count"), 1),
		StorageBackend:      strings.TrimSpace(params.Get("storage")),
		VirtType:            strings.ToLower(strings.TrimSpace(params.Get("virt"))),
		ImageID:             strings.TrimSpace(params.Get("image_id")),
		TenantID:            strings.TrimSpace(params.Get("tenant")),
		RequestID:           v2RequestID(r),
	}
	priority := parseIntDefaultV2(params.Get("node_priority"), 1)
	diag, err := scheduler.Place(request, v2NodePriorityPolicy(priority), 5)

	candidates := make([]map[string]interface{}, 0, len(diag.Candidates))
	for _, c := range diag.Candidates {
		entry := map[string]interface{}{
			"node_id":        c.NodeID,
			"node_name":      v2NodeNameSafe(c.NodeID),
			"passed":         c.PassedFilter,
			"score":          round2(c.Score),
			"filter_reasons": c.FilterReasons,
			"score_reasons":  c.ScoreReasons,
		}
		candidates = append(candidates, entry)
	}
	if err != nil || diag.Chosen == "" {
		message := diag.Reason
		if message == "" {
			message = "没有满足条件的节点（需在线、未维护、容量足够、支持所需虚拟化）"
		}
		v2Write(w, r, http.StatusPreconditionFailed, v2Envelope{
			Success: false, Code: v2CodePreconditionFailed, Message: message,
			Data: map[string]interface{}{"candidates": candidates},
		})
		return
	}
	node, _ := config.FindNode(diag.Chosen)
	v2OK(w, r, map[string]interface{}{
		"chosen":     v2NodeView(node),
		"reason":     diag.Reason,
		"candidates": candidates,
	})
}

func v2NodeNameSafe(nodeID string) string {
	if n, ok := config.FindNode(nodeID); ok {
		return n.Name
	}
	return nodeID
}

func parseIntDefaultV2(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return fallback
	}
	return value
}

func parseFloatDefaultV2(raw string, fallback float64) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil {
		return fallback
	}
	return value
}

func v2NodeGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	view := v2NodeView(node)
	containers := v2InstancesOnNode(node.ID)
	view["instance_count"] = len(containers)
	view["instances"] = containers
	v2OK(w, r, view)
}

func v2InstancesOnNode(nodeID string) []map[string]interface{} {
	config.AppConfigMu.RLock()
	containers := append([]config.Container(nil), config.AppConfig.Containers...)
	config.AppConfigMu.RUnlock()
	items := []map[string]interface{}{}
	for _, c := range containers {
		if c.NodeID != nodeID {
			continue
		}
		items = append(items, map[string]interface{}{
			"id": c.ID, "name": c.Name, "status": v2InstanceStatus(c),
			"runtime": c.Runtime(), "primary_ip": c.IP,
		})
	}
	return items
}

// v2NodeUpdate PATCH /api/v2/nodes/{id}
func v2NodeUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		Address       *string `json:"address"`
		TLSSkipVerify *bool   `json:"tls_skip_verify"`
		RegionID      *string `json:"region_id"`
		NodeGroupID   *string `json:"node_group_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if req.Address != nil {
		normalized := normalizeNodeAddress(*req.Address)
		if err := validateNodeAddress(normalized, node.AllowPrivateAddr); err != nil {
			v2BadRequest(w, r, err.Error(), map[string]string{"address": *req.Address})
			return
		}
	}
	changed := map[string]interface{}{}
	config.UpdateNode(node.ID, func(n *config.Node) {
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			n.Name = strings.TrimSpace(*req.Name)
			changed["name"] = n.Name
		}
		if req.Address != nil {
			n.Address = normalizeNodeAddress(*req.Address)
			changed["address"] = n.Address
		}
		if req.TLSSkipVerify != nil {
			n.TLSSkipVerify = *req.TLSSkipVerify
			changed["tls_skip_verify"] = n.TLSSkipVerify
		}
		if req.RegionID != nil {
			n.RegionID = *req.RegionID
			changed["region_id"] = n.RegionID
		}
		if req.NodeGroupID != nil {
			n.NodeGroupID = *req.NodeGroupID
			changed["node_group_id"] = n.NodeGroupID
		}
	})
	if len(changed) == 0 {
		v2BadRequest(w, r, "没有需要修改的字段", nil)
		return
	}
	auditRequest(r, "api.v2.node.update", node.Name, fmt.Sprintf("%v", changed), true, "")
	updated, _ := config.FindNode(node.ID)
	v2OK(w, r, v2NodeView(updated))
}

func v2NodeDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	// 该节点上仍有实例时拒绝删除（避免出现"孤儿实例"）。
	onNode := v2InstancesOnNode(node.ID)
	force := strings.EqualFold(r.URL.Query().Get("force"), "true")
	if len(onNode) > 0 && !force {
		v2Conflict(w, r, fmt.Sprintf("该节点上仍有 %d 个实例，如需强制删除请加 ?force=true", len(onNode)))
		return
	}
	if !config.RemoveNode(node.ID) {
		v2NotFound(w, r, "节点不存在")
		return
	}
	auditRequest(r, "api.v2.node.delete", node.Name, "删除节点", true, "")
	v2NoContent(w, r)
}

// v2NodeMaintenance POST /api/v2/nodes/{id}/maintenance {enabled}
func v2NodeMaintenance(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if req.Enabled == nil {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"enabled": "true/false"})
		return
	}
	config.UpdateNode(node.ID, func(n *config.Node) {
		n.MaintenanceMode = *req.Enabled
		if *req.Enabled {
			n.MaintenanceSince = time.Now().Format("2006-01-02 15:04:05")
		} else {
			n.MaintenanceSince = ""
		}
	})
	auditRequest(r, "api.v2.node.maintenance", node.Name, fmt.Sprintf("enabled=%v", *req.Enabled), true, "")
	updated, _ := config.FindNode(node.ID)
	v2OK(w, r, v2NodeView(updated))
}

// v2NodeInstallKey POST /api/v2/nodes/{id}/install-key：换发一次性安装密钥并返回接入命令。
func v2NodeInstallKey(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	installKey := randomNodeSecret(32)
	config.UpdateNode(node.ID, func(n *config.Node) {
		n.InstallKey = installKey
		n.InstallKeyCreatedAt = time.Now().UTC().Format(time.RFC3339)
	})
	baseURL := externalBaseURL(r)
	script := buildAgentInstallScript(baseURL, installKey, node.Name, "")
	sum := sha256.Sum256([]byte(script))
	auditRequest(r, "api.v2.node.install_key", node.Name, "换发安装密钥", true, "")
	v2OK(w, r, map[string]interface{}{
		"node_id":        node.ID,
		"install_key":    installKey,
		"expires_at":     time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		"install_command": fmt.Sprintf("curl -fsSL -H \"X-Install-Key: %s\" %s/api/nodes/%s/install-script | sudo bash",
			installKey, baseURL, node.ID),
		"sha256": hex.EncodeToString(sum[:]),
	})
}

// v2NodeMetrics GET /api/v2/nodes/{id}/metrics：节点资源与容器统计（心跳上报值）。
func v2NodeMetrics(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	containers := v2InstancesOnNode(node.ID)
	running := 0
	for _, item := range containers {
		if item["status"] == "running" {
			running++
		}
	}
	v2OK(w, r, map[string]interface{}{
		"node_id":          node.ID,
		"node_name":        node.Name,
		"status":           node.Status,
		"last_seen":        v2Time(node.LastSeen),
		"version":          node.Version,
		"os_name":          node.OSName,
		"cpu_count":        node.CPUCount,
		"memory_total_mb":  node.RAMTotalMB,
		"memory_used_mb":   node.RAMUsedMB,
		"disk_total_gb":    node.DiskTotalGB,
		"disk_used_gb":     node.DiskUsedGB,
		"instance_total":   len(containers),
		"instance_running": running,
	})
}

// v2NodeInstances GET /api/v2/nodes/{id}/instances：该节点上的实例（分页）。
func v2NodeInstances(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	node, ok := v2FindNode(w, r)
	if !ok {
		return
	}
	query := v2ParsePage(r)
	items := v2InstancesOnNode(node.ID)
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

// ---------------------------------------------------------------------------
// 节点分组
// ---------------------------------------------------------------------------

func v2NodeGroupsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	groups := append([]config.NodeGroup(nil), config.AppConfig.NodeGroups...)
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()

	items := make([]map[string]interface{}, 0, len(groups))
	for _, group := range groups {
		members := []map[string]interface{}{}
		for _, n := range nodes {
			if n.NodeGroupID == group.ID {
				members = append(members, map[string]interface{}{"id": n.ID, "name": n.Name, "status": n.Status})
			}
		}
		items = append(items, map[string]interface{}{
			"id": group.ID, "name": group.Name, "description": group.Description,
			"region_id": group.RegionID, "region_name": v2RegionName(group.RegionID),
			"node_count": len(members), "nodes": members,
			"created_at": v2Time(group.CreatedAt),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2NodeGroupsCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		RegionID    string `json:"region_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	group, err := config.AddNodeGroup(config.NodeGroup{
		ID:          "ng-" + randomHex(6),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		RegionID:    strings.TrimSpace(req.RegionID),
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		v2Internal(w, r, "创建分组失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.node_group.create", group.Name, "", true, "")
	v2Created(w, r, map[string]interface{}{"id": group.ID, "name": group.Name})
}

func v2NodeGroupUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		RegionID    *string `json:"region_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	updated, ok := config.UpdateNodeGroup(groupID, func(g *config.NodeGroup) {
		if req.Name != nil {
			g.Name = strings.TrimSpace(*req.Name)
		}
		if req.Description != nil {
			g.Description = strings.TrimSpace(*req.Description)
		}
		if req.RegionID != nil {
			g.RegionID = strings.TrimSpace(*req.RegionID)
		}
	})
	if !ok {
		v2NotFound(w, r, "分组不存在："+groupID)
		return
	}
	auditRequest(r, "api.v2.node_group.update", updated.Name, "", true, "")
	v2OK(w, r, map[string]interface{}{
		"id": updated.ID, "name": updated.Name,
		"description": updated.Description, "region_id": updated.RegionID,
	})
}

func v2NodeGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	// 成员节点自动解除分组（不删除节点本身）。
	config.AppConfigMu.RLock()
	members := []config.Node{}
	for _, n := range config.AppConfig.Nodes {
		if n.NodeGroupID == groupID {
			members = append(members, n)
		}
	}
	config.AppConfigMu.RUnlock()
	for _, n := range members {
		config.UpdateNode(n.ID, func(node *config.Node) { node.NodeGroupID = "" })
	}
	if !config.RemoveNodeGroup(groupID) {
		v2NotFound(w, r, "分组不存在："+groupID)
		return
	}
	auditRequest(r, "api.v2.node_group.delete", groupID,
		fmt.Sprintf("解除 %d 个节点的分组归属", len(members)), true, "")
	v2NoContent(w, r)
}

// v2NodeGroupSetNodes PUT /api/v2/node-groups/{id}/nodes {node_ids: [...]}（整体替换语义）
func v2NodeGroupSetNodes(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		NodeIDs []string `json:"node_ids"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if _, ok := config.FindNodeGroup(groupID); !ok {
		v2NotFound(w, r, "分组不存在："+groupID)
		return
	}
	targets := map[string]bool{}
	for _, id := range req.NodeIDs {
		if _, ok := config.FindNode(id); !ok {
			v2NotFound(w, r, "节点不存在："+id)
			return
		}
		targets[id] = true
	}
	config.AppConfigMu.RLock()
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()
	changed := 0
	for _, n := range nodes {
		shouldJoin := targets[n.ID]
		if shouldJoin && n.NodeGroupID != groupID {
			config.UpdateNode(n.ID, func(node *config.Node) { node.NodeGroupID = groupID })
			changed++
		}
		if !shouldJoin && n.NodeGroupID == groupID {
			config.UpdateNode(n.ID, func(node *config.Node) { node.NodeGroupID = "" })
			changed++
		}
	}
	auditRequest(r, "api.v2.node_group.set_nodes", groupID,
		fmt.Sprintf("成员整体替换为 %d 个节点，变更 %d", len(targets), changed), true, "")
	v2OK(w, r, map[string]interface{}{"node_group_id": groupID, "node_ids": req.NodeIDs, "changed": changed})
}

// ---------------------------------------------------------------------------
// 区域
// ---------------------------------------------------------------------------

func v2RegionsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	regions := append([]config.Region(nil), config.AppConfig.Regions...)
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(regions))
	for _, region := range regions {
		nodeCount, onlineCount := 0, 0
		for _, n := range nodes {
			if n.RegionID != region.ID {
				continue
			}
			nodeCount++
			if n.Status == "online" {
				onlineCount++
			}
		}
		items = append(items, map[string]interface{}{
			"id": region.ID, "name": region.Name, "location": region.Location,
			"node_count": nodeCount, "node_online": onlineCount,
			"created_at": v2Time(region.CreatedAt),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2RegionsCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	region := config.Region{
		ID:        "rg-" + randomHex(6),
		Name:      strings.TrimSpace(req.Name),
		Location:  strings.TrimSpace(req.Location),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Regions = append(cfg.Regions, region)
	})
	if err := config.SaveConfig(); err != nil {
		v2Internal(w, r, "保存区域失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.region.create", region.Name, "", true, "")
	v2Created(w, r, map[string]interface{}{"id": region.ID, "name": region.Name, "location": region.Location})
}

func v2RegionUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	regionID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Name     *string `json:"name"`
		Location *string `json:"location"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	var updated config.Region
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Regions {
			if cfg.Regions[i].ID != regionID {
				continue
			}
			if req.Name != nil {
				cfg.Regions[i].Name = strings.TrimSpace(*req.Name)
			}
			if req.Location != nil {
				cfg.Regions[i].Location = strings.TrimSpace(*req.Location)
			}
			updated = cfg.Regions[i]
			return
		}
	})
	if updated.ID == "" {
		v2NotFound(w, r, "区域不存在："+regionID)
		return
	}
	auditRequest(r, "api.v2.region.update", updated.Name, "", true, "")
	v2OK(w, r, map[string]interface{}{"id": updated.ID, "name": updated.Name, "location": updated.Location})
}

func v2RegionDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	regionID := strings.TrimSpace(r.PathValue("id"))
	config.AppConfigMu.RLock()
	hasNodes := false
	for _, n := range config.AppConfig.Nodes {
		if n.RegionID == regionID {
			hasNodes = true
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if hasNodes {
		v2Conflict(w, r, "该区域下仍有节点，请先迁移或解除归属")
		return
	}
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.Regions[:0]
		for _, region := range cfg.Regions {
			if region.ID == regionID {
				removed = true
				continue
			}
			out = append(out, region)
		}
		cfg.Regions = out
	})
	if !removed {
		v2NotFound(w, r, "区域不存在："+regionID)
		return
	}
	_ = config.SaveConfig()
	auditRequest(r, "api.v2.region.delete", regionID, "", true, "")
	v2NoContent(w, r)
}
