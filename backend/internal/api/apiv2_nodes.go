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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/scheduler"
	"eyvescloud/internal/version"
)

func init() {
	registerV2("GET /api/v2/nodes", v2Auth(v2NodesList))
	registerV2("POST /api/v2/nodes", v2Auth(v2NodesCreate))
	registerV2("GET /api/v2/nodes/schedule", v2Auth(v2NodesSchedule))
	// 「一键升级被控节点」：注册在 /nodes/{id} 之前（字面量优先，Go 1.22 mux 也按此匹配）。
	registerV2("POST /api/v2/nodes/upgrade", v2Auth(v2NodesUpgrade))
	registerV2("GET /api/v2/nodes/{id}", v2Auth(v2NodeGet))
	registerV2("PATCH /api/v2/nodes/{id}", v2Auth(v2NodeUpdate))
	registerV2("DELETE /api/v2/nodes/{id}", v2Auth(v2NodeDelete))
	registerV2("POST /api/v2/nodes/{id}/maintenance", v2Auth(v2NodeMaintenance))
	registerV2("POST /api/v2/nodes/{id}/install-key", v2Auth(v2NodeInstallKey))
	registerV2("GET /api/v2/nodes/{id}/metrics", v2Auth(v2NodeMetrics))
	registerV2("GET /api/v2/nodes/{id}/instances", v2Auth(v2NodeInstances))
	registerV2("GET /api/v2/nodes/{id}/image-availability", v2Auth(v2NodeImageAvailability))
	registerV2("POST /api/v2/nodes/{id}/images/download", v2Auth(v2NodeImageDownload))

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
	view["public_host"] = n.PublicHost
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
	Mode          string `json:"mode"` // quick（默认，一键接入）| manual（手工放置 agent.json）
	TLSSkipVerify bool   `json:"tls_skip_verify"`
	AllowPrivate  bool   `json:"allow_private"`
	BindIP        string `json:"bind_ip"` // 可选：把一次性密钥绑定到被控出口 IP
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
		// PublicHost：客户接入地址（公网 IP/域名）。留空自动取 Address 的 host。
		PublicHost *string `json:"public_host"`
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
		if req.PublicHost != nil {
			n.PublicHost = normalizeAccessHost(*req.PublicHost)
			changed["public_host"] = n.PublicHost
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
		"node_id":     node.ID,
		"install_key": installKey,
		"expires_at":  time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
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
		view := v2RegionView(region)
		view["node_count"] = nodeCount
		view["node_online"] = onlineCount
		items = append(items, view)
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
		Name         string  `json:"name"`
		Location     string  `json:"location"`
		MaxInstances int     `json:"max_instances"`
		MaxRAMMB     int64   `json:"max_ram_mb"`
		MaxDiskGB    float64 `json:"max_disk_gb"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	if req.MaxInstances < 0 || req.MaxRAMMB < 0 || req.MaxDiskGB < 0 {
		v2BadRequest(w, r, "配额不能为负数", map[string]string{
			"max_instances": ">=0", "max_ram_mb": ">=0", "max_disk_gb": ">=0"})
		return
	}
	region := config.Region{
		ID:           "rg-" + randomHex(6),
		Name:         strings.TrimSpace(req.Name),
		Location:     strings.TrimSpace(req.Location),
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
		MaxInstances: req.MaxInstances,
		MaxRAMMB:     req.MaxRAMMB,
		MaxDiskGB:    req.MaxDiskGB,
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Regions = append(cfg.Regions, region)
	})
	if err := config.SaveConfig(); err != nil {
		v2Internal(w, r, "保存区域失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.region.create", region.Name, "", true, "")
	v2Created(w, r, v2RegionView(region))
}

// v2RegionView 区域对外契约（含配额与当前用量，便于前端显示 x/y）。
func v2RegionView(region config.Region) map[string]interface{} {
	usedInstances, usedRAM, usedDisk := config.RegionUsage(region.ID)
	return map[string]interface{}{
		"id": region.ID, "name": region.Name, "location": region.Location,
		"created_at":    v2Time(region.CreatedAt),
		"max_instances": region.MaxInstances, "max_ram_mb": region.MaxRAMMB, "max_disk_gb": region.MaxDiskGB,
		"used_instances": usedInstances, "used_ram_mb": usedRAM, "used_disk_gb": round2(usedDisk),
	}
}

// v2CheckRegionQuota 校验"目标节点所属区域"的配额是否还装得下新增实例。
// 返回 nil 表示通过；未归属区域或未设配额时一律通过（0 = 不限制）。
func v2CheckRegionQuota(nodeID string, additional int, ramMB int64, diskGB float64) error {
	node, ok := config.FindNode(nodeID)
	if !ok || strings.TrimSpace(node.RegionID) == "" {
		return nil
	}
	region, ok := config.FindRegion(node.RegionID)
	if !ok {
		return nil
	}
	if region.MaxInstances <= 0 && region.MaxRAMMB <= 0 && region.MaxDiskGB <= 0 {
		return nil
	}
	usedInstances, usedRAM, usedDisk := config.RegionUsage(region.ID)
	if region.MaxInstances > 0 && usedInstances+additional > region.MaxInstances {
		return fmt.Errorf("区域「%s」实例数配额已满（%d/%d）：请扩容区域配额或改选其它区域",
			region.Name, usedInstances, region.MaxInstances)
	}
	if region.MaxRAMMB > 0 && usedRAM+ramMB > region.MaxRAMMB {
		return fmt.Errorf("区域「%s」内存配额不足（已用 %d MB / 上限 %d MB，本次需要 %d MB）",
			region.Name, usedRAM, region.MaxRAMMB, ramMB)
	}
	if region.MaxDiskGB > 0 && usedDisk+diskGB > region.MaxDiskGB {
		return fmt.Errorf("区域「%s」磁盘配额不足（已用 %.1f GB / 上限 %.1f GB，本次需要 %.1f GB）",
			region.Name, usedDisk, region.MaxDiskGB, diskGB)
	}
	return nil
}

func v2RegionUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:write") {
		return
	}
	regionID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Name         *string  `json:"name"`
		Location     *string  `json:"location"`
		MaxInstances *int     `json:"max_instances"`
		MaxRAMMB     *int64   `json:"max_ram_mb"`
		MaxDiskGB    *float64 `json:"max_disk_gb"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if (req.MaxInstances != nil && *req.MaxInstances < 0) ||
		(req.MaxRAMMB != nil && *req.MaxRAMMB < 0) ||
		(req.MaxDiskGB != nil && *req.MaxDiskGB < 0) {
		v2BadRequest(w, r, "配额不能为负数", map[string]string{
			"max_instances": ">=0", "max_ram_mb": ">=0", "max_disk_gb": ">=0"})
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
			if req.MaxInstances != nil {
				cfg.Regions[i].MaxInstances = *req.MaxInstances
			}
			if req.MaxRAMMB != nil {
				cfg.Regions[i].MaxRAMMB = *req.MaxRAMMB
			}
			if req.MaxDiskGB != nil {
				cfg.Regions[i].MaxDiskGB = *req.MaxDiskGB
			}
			updated = cfg.Regions[i]
			return
		}
	})
	if updated.ID == "" {
		v2NotFound(w, r, "区域不存在："+regionID)
		return
	}
	_ = config.SaveConfig()
	auditRequest(r, "api.v2.region.update", updated.Name, "", true, "")
	v2OK(w, r, v2RegionView(updated))
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

// v2NodesUpgrade POST /api/v2/nodes/upgrade —— 一键升级被控节点。
//
// 请求体：
//
//	{
//	  "node_ids": ["node-xxx", ...],   // 省略 = 全部节点
//	  "target_version": "2.2.17",      // 省略 = 主控当前版本（对齐主控）
//	  "check_only": false              // true = 只回报节点当前版本与差异，不下发升级
//	}
//
// 语义：逐个向被控下发 /api/agent/self-update（被控受理后就地替换二进制并重启服务），
// 本接口立即返回**受理**结果。节点实际是否升级成功，通过节点心跳回报的 version 字段
// 确认（稍后 GET /api/v2/nodes 复查即可）。
func v2NodesUpgrade(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	var req struct {
		NodeIDs       []string `json:"node_ids"`
		TargetVersion string   `json:"target_version"`
		CheckOnly     bool     `json:"check_only"`
	}
	if r.Body != nil {
		if err := v2Decode(r, &req); err != nil {
			v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
			return
		}
	}
	target := strings.TrimSpace(req.TargetVersion)
	if target == "" {
		// 默认对齐主控版本：这是"节点跟主控同版本"的直观语义。
		target = version.Current()
	}
	if !v2ValidVersionTag(target) {
		v2BadRequest(w, r, "无效的目标版本", map[string]string{"target_version": target})
		return
	}
	// 主控先把版本号解析为发布里的规范 tag（如 2.2.23 → v2.2.23）再下发：
	// 被控可能运行较旧版本，其 tag 查询不带前缀兼容；由主控解析可让任何版本的
	// 被控都取到目标发布。解析失败说明该版本尚无发布产物，直接返回明确原因。
	if !req.CheckOnly {
		resolved, resolveErr := cli.ResolveReleaseTag(target)
		if resolveErr != nil {
			v2Precondition(w, r, resolveErr.Error())
			return
		}
		target = resolved
	}

	// 选定目标节点：显式列表，或全部节点。
	config.AppConfigMu.RLock()
	all := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()
	wanted := map[string]bool{}
	for _, id := range req.NodeIDs {
		wanted[strings.TrimSpace(id)] = true
	}
	nodes := make([]config.Node, 0, len(all))
	for _, n := range all {
		if len(wanted) > 0 && !wanted[n.ID] {
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		v2NotFound(w, r, "没有匹配的节点")
		return
	}

	// 版本兼容策略：被控版本低于「Codeberg tag 端点修复」引入的版本时，其自身按 tag
	// 查询不兼容 v 前缀写法（旧端点缺 /tags/ 段，必 404）——对这类节点改下发
	// 「最新发布版本」（空 target），先把它升到带修复的版本，后续即可精确升级。
	const v2TagCompatMinVersion = "2.2.24"

	accepted := make([]map[string]interface{}, 0, len(nodes))
	skipped := make([]map[string]interface{}, 0)
	failed := make([]map[string]interface{}, 0)
	upToDate := 0

	for _, n := range nodes {
		switch {
		case n.MaintenanceMode:
			skipped = append(skipped, map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "reason": "维护模式"})
			continue
		case n.Status != "" && n.Status != "online":
			skipped = append(skipped, map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "reason": "节点不在线（离线节点无法下发升级）"})
			continue
		case n.Address == "":
			skipped = append(skipped, map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "reason": "节点未配置地址"})
			continue
		}
		if sameVersionString(n.Version, target) {
			upToDate++
			skipped = append(skipped, map[string]interface{}{
				"node_id": n.ID, "node_name": n.Name, "current_version": n.Version, "reason": "已是目标版本",
			})
			continue
		}
		payloadTarget := target
		compatNote := ""
		if !req.CheckOnly && v2VersionLess(n.Version, v2TagCompatMinVersion) {
			payloadTarget = ""
			compatNote = "节点版本较旧（< " + v2TagCompatMinVersion + "），已按「最新发布版本」下发以完成首次升级"
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"target_version": payloadTarget,
			"check_only":     req.CheckOnly,
		})
		data, status, err := proxyNodeRequest(r, n, http.MethodPost, "/api/agent/self-update", bytes.NewReader(payload))
		if err != nil || status >= 300 {
			msg := fmt.Sprintf("HTTP %d: %v", status, err)
			if len(data) > 0 {
				msg = strings.TrimSpace(string(data))
			}
			// 引导提示：老版本被控尚无自更新端点（404/405），必须先在节点上手动升级一次。
			// 这是升级能力的引导限制（类似 kubelet 需手动 bootstrap 才能纳入滚动升级）。
			hint := ""
			if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
				hint = "该节点版本过旧，尚未提供自更新端点；请先在其「节点管理 → 换发安装密钥」生成一次性命令，" +
					"在节点上执行一次安装脚本完成初次升级，之后即可使用一键升级"
			}
			entry := map[string]interface{}{
				"node_id": n.ID, "node_name": n.Name, "current_version": n.Version, "error": msg,
			}
			if hint != "" {
				entry["hint"] = hint
				entry["needs_manual_bootstrap"] = true
			}
			failed = append(failed, entry)
			continue
		}
		entry := map[string]interface{}{
			"node_id": n.ID, "node_name": n.Name,
			"current_version": n.Version, "target_version": target,
			"check_only": req.CheckOnly,
		}
		if compatNote != "" {
			entry["note"] = compatNote
			entry["bootstrap_mode"] = "latest-release"
		}
		accepted = append(accepted, entry)
	}

	action := "upgrade"
	if req.CheckOnly {
		action = "check"
	}
	auditRequest(r, "api.v2.nodes."+action, fmt.Sprintf("%d 个节点", len(nodes)),
		fmt.Sprintf("target=%s 受理=%d 跳过=%d 失败=%d", target, len(accepted), len(skipped), len(failed)), true, "")
	v2Accepted(w, r, map[string]interface{}{
		"target_version": target,
		"check_only":     req.CheckOnly,
		"up_to_date":     upToDate,
		"accepted":       accepted,
		"skipped":        skipped,
		"failed":         failed,
		"note":           "已受理的节点将在数秒内替换二进制并重启服务；约 30 秒后可用 GET /api/v2/nodes 复查 version 字段确认结果。",
	})
}

// v2ValidVersionTag 校验版本标签（允许 v 前缀，字符集与发布 tag 一致）。
func v2ValidVersionTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 200 {
		return false
	}
	for _, ch := range tag {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_' || ch == '.':
		default:
			return false
		}
	}
	return true
}

// v2VersionLess 比较版本号字面量（如 "2.2.19" < "2.2.24"）：忽略 v 前缀与
// 非数字后缀，缺省段按 0 处理。仅用于升级路径的"老版本判定"，不追求 semver 全语义。
func v2VersionLess(a, b string) bool {
	parse := func(v string) [3]int {
		var out [3]int
		v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
		parts := strings.Split(v, ".")
		for i := 0; i < len(out) && i < len(parts); i++ {
			num := 0
			for _, ch := range parts[i] {
				if ch < '0' || ch > '9' {
					break
				}
				num = num*10 + int(ch-'0')
			}
			out[i] = num
		}
		return out
	}
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// v2NodeImageAvailability GET /api/v2/nodes/{id}/image-availability
//
// 目标节点的镜像可用性（是否已下载 + 体积），供开通页在选好节点后标注
// 「该节点是否已有此镜像」，并在提交前自动补齐。
func v2NodeImageAvailability(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("id"))
	node, ok := config.FindNode(nodeID)
	if !ok {
		v2NotFound(w, r, "节点不存在："+nodeID)
		return
	}
	data, status, err := proxyNodeRequest(r, node, http.MethodGet, "/api/agent/images/availability", nil)
	if err != nil {
		v2Upstream(w, r, "查询节点镜像失败："+err.Error())
		return
	}
	if status >= 300 {
		v2Upstream(w, r, "节点返回错误（HTTP "+strconv.Itoa(status)+"）："+strings.TrimSpace(string(data)))
		return
	}
	var payload struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v2Upstream(w, r, "解析节点响应失败："+err.Error())
		return
	}
	result := payload.Data
	if result == nil {
		result = map[string]interface{}{}
	}
	result["node_id"] = node.ID
	result["node_name"] = node.Name
	v2OK(w, r, result)
}

// v2NodeImageDownload POST /api/v2/nodes/{id}/images/download {template_id}
//
// 在目标节点补齐指定镜像（幂等）：已下载返回 already_downloaded，下载中返回
// already_downloading，否则 started/queued。开通页在提交前据此自动补镜像。
func v2NodeImageDownload(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "image:download") {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("id"))
	node, ok := config.FindNode(nodeID)
	if !ok {
		v2NotFound(w, r, "节点不存在："+nodeID)
		return
	}
	var req struct {
		TemplateID string `json:"template_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"template_id": req.TemplateID}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	payload, _ := json.Marshal(map[string]string{"template_id": strings.TrimSpace(req.TemplateID)})
	data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/images/download", bytes.NewReader(payload))
	if err != nil {
		v2Upstream(w, r, "下发镜像下载失败："+err.Error())
		return
	}
	if status >= 300 {
		v2Upstream(w, r, "节点返回错误（HTTP "+strconv.Itoa(status)+"）："+strings.TrimSpace(string(data)))
		return
	}
	var payloadResp struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &payloadResp)
	auditRequest(r, "api.v2.node.image_download", node.Name+"/"+req.TemplateID, payloadResp.Message, true, "")
	v2Accepted(w, r, map[string]interface{}{
		"node_id": node.ID, "node_name": node.Name,
		"template_id": strings.TrimSpace(req.TemplateID), "status": payloadResp.Message,
	})
}
