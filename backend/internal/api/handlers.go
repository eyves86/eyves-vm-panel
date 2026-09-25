package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/notify"
	"eyvescloud/internal/version"
)

var lxcManager = lxc.NewManager()

// HandleContainers handles container list and creation
func HandleContainers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "container:read") {
			return
		}
		listContainers(w, r)
	case http.MethodPost:
		if !requireScope(w, r, "container:create") {
			return
		}
		if isAccessRestrictedRequest(r) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Container-bound API keys cannot create containers"})
			return
		}
		if !requireSubUserWrite(w, r) {
			return
		}
		createContainer(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleContainerListAlias supports legacy integrations that call
// /api/containers/list or /api/v1/containers/list.
func HandleContainerListAlias(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	listContainers(w, r)
}

// HandleSingleContainer handles individual container operations by ID or name: /api/containers/{id-or-name}/...
func HandleSingleContainer(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/containers/")
	path = strings.TrimPrefix(path, "/api/containers/")
	parts := strings.SplitN(path, "/", 2)
	c := containerByIdentifier(parts[0])
	id := 0
	if c != nil {
		id = c.ID
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Snapshot delete/restore operations: allow even if the container was deleted
	isSnapshotDelete := strings.HasPrefix(action, "snapshots/") && r.Method == http.MethodDelete
	isSnapshotRestore := strings.HasPrefix(action, "snapshots/") && strings.HasSuffix(action, "/restore") && r.Method == http.MethodPost
	isSnapshotAction := isSnapshotDelete || isSnapshotRestore
	if !isSnapshotAction && c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !isSnapshotAction && !isContainerAllowedForRequest(r, parts[0]) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	if isSnapshotAction && id == 0 {
		// For orphaned snapshots, resolve containerID from the snapshot itself
		snapshotID := strings.TrimPrefix(action, "snapshots/")
		snapshotID = strings.TrimSuffix(snapshotID, "/restore")
		snapshot := config.FindSnapshot(snapshotID)
		if snapshot == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Snapshot not found"})
			return
		}
		id = snapshot.ContainerID
	}
	if isSnapshotAction {
		if c := config.FindContainer(id); c != nil && !isContainerAllowedForRequest(r, c.UUID) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
			return
		}
	}

	// 只读子用户（viewer）禁止一切写操作
	if r.Method != http.MethodGet && !requireSubUserWrite(w, r) {
		return
	}

	// 多节点路由：容器 NodeID 非空时，运行时操作转发到所属 agent。
	// 返回 true 表示已由 agent 处理，调用方应直接 return。
	routeToAgent := func(agentAction string, body io.Reader) bool {
		if c == nil || c.NodeID == "" {
			return false
		}
		node, ok := config.FindNode(c.NodeID)
		if !ok {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "容器所属节点不存在: " + c.NodeID})
			return true
		}
		if node.Address == "" {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "容器所属节点未配置地址"})
			return true
		}
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/%s", c.ID, agentAction), body)
		if err != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return true
	}

	switch {
	case action == "start" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("start", nil) {
			return
		}
		HandleSingleTaskAction(w, r, id, "start")
	case action == "stop" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("stop", nil) {
			return
		}
		HandleSingleTaskAction(w, r, id, "stop")
	case action == "restart" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("restart", nil) {
			return
		}
		HandleSingleTaskAction(w, r, id, "restart")
	case action == "reinstall" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:reinstall") {
			return
		}
		if routeToAgent("reinstall", r.Body) {
			return
		}
		HandleSingleTaskAction(w, r, id, "reinstall")
	case action == "suspend" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("suspend", nil) {
			return
		}
		suspendContainer(w, r, id, true)
	case action == "unsuspend" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("unsuspend", nil) {
			return
		}
		suspendContainer(w, r, id, false)
	case action == "delete" && r.Method == http.MethodDelete:
		if !requireScope(w, r, "container:delete") {
			return
		}
		if routeToAgent("destroy", nil) {
			return
		}
		HandleSingleTaskAction(w, r, id, "delete")
	case action == "reset-password" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:password") {
			return
		}
		if routeToAgent("reset-password", r.Body) {
			return
		}
		resetSSHPassword(w, r, id)
	case action == "create-account" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:account") {
			return
		}
		createContainerAccount(w, r, id)
	case action == "tenant" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:resize") {
			return
		}
		var req struct {
			Tenant string `json:"tenant"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		tenant := strings.TrimSpace(req.Tenant)
		config.SetContainerTenant(id, tenant)
		auditRequest(r, "container.tenant", c.Name, "tenant="+tenant, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"tenant": tenant}})
	case action == "owner" && r.Method == http.MethodPut:
		// 变更容器属主 SubUser（空字符串 = 解绑）。
		// 属主变更是管理操作：仅管理员/管理侧 API 密钥可执行。
		// 子用户（即使 operator）持有 container:power 也不得变更属主，
		// 否则可自行解绑或把容器转移给他人，破坏归属与计量完整性。
		if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeSubUser {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Sub-users cannot change container owner"})
			return
		}
		if !requireScope(w, r, "container:power") {
			return
		}
		var req struct {
			OwnerSubUserID string `json:"owner_sub_user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		newOwner := strings.TrimSpace(req.OwnerSubUserID)
		// 校验新 owner 存在（空字符串 = 解绑，允许）
		if newOwner != "" {
			config.AppConfigMu.RLock()
			found := false
			for i := range config.AppConfig.SubUsers {
				if config.AppConfig.SubUsers[i].ID == newOwner {
					found = true
					break
				}
			}
			config.AppConfigMu.RUnlock()
			if !found {
				jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
				return
			}
		}
		var oldOwnerID string
		var ownerUsername string
		var foundName string
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.Containers {
				if cfg.Containers[i].ID != id {
					continue
				}
				foundName = cfg.Containers[i].Name
				oldOwnerID = cfg.Containers[i].OwnerSubUserID
				cfg.Containers[i].OwnerSubUserID = newOwner
				// 同步更新 SubUser.ContainerUUIDs / ContainerNames
				// 1) 从旧 owner 移除
				if oldOwnerID != "" && oldOwnerID != newOwner {
					for j := range cfg.SubUsers {
						if cfg.SubUsers[j].ID != oldOwnerID {
							continue
						}
						cfg.SubUsers[j].ContainerUUIDs = removeString(cfg.SubUsers[j].ContainerUUIDs, cfg.Containers[i].UUID)
						cfg.SubUsers[j].ContainerNames = removeString(cfg.SubUsers[j].ContainerNames, cfg.Containers[i].Name)
						cfg.SubUsers[j].TokenVersion++ // 强制旧属主刷新可见容器列表
						break
					}
				}
				// 2) 追加到新 owner
				if newOwner != "" && newOwner != oldOwnerID {
					for j := range cfg.SubUsers {
						if cfg.SubUsers[j].ID != newOwner {
							continue
						}
						if !stringContains(cfg.SubUsers[j].ContainerUUIDs, cfg.Containers[i].UUID) {
							cfg.SubUsers[j].ContainerUUIDs = append(cfg.SubUsers[j].ContainerUUIDs, cfg.Containers[i].UUID)
						}
						cfg.SubUsers[j].ContainerNames = appendUniqueString(cfg.SubUsers[j].ContainerNames, cfg.Containers[i].Name)
						cfg.SubUsers[j].TokenVersion++
						ownerUsername = cfg.SubUsers[j].Username
						break
					}
				}
				return
			}
		})
		if foundName == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
			return
		}
		if err := config.SaveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
			return
		}
		auditDetail := fmt.Sprintf("old=%s new=%s", oldOwnerID, newOwner)
		auditRequest(r, "container.owner", foundName, auditDetail, true, "")
		resp := map[string]interface{}{"owner_sub_user_id": newOwner}
		if newOwner != "" {
			resp["owner_username"] = ownerUsername
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
	case action == "migrate" && r.Method == http.MethodPut:
		// 迁移容器到目标节点。轻量路径：更新 Container.NodeID + 审计 + 同步目标节点计数。
		// 跨节点实际数据移动由被控 Agent 拉指令执行；本端点作为控制面入口。
		if !requireScope(w, r, "container:resize") {
			return
		}
		var req struct {
			TargetNodeID string `json:"target_node_id"`
			Force        bool   `json:"force"` // 强制跨 Cluster 迁移（默认要求同 Cluster 或一方 Cluster 为空）
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		targetNodeID := strings.TrimSpace(req.TargetNodeID)
		if targetNodeID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "target_node_id is required"})
			return
		}
		targetNode, ok := config.FindNode(targetNodeID)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Target node not found"})
			return
		}
		if targetNode.Status != "online" {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Target node is not online"})
			return
		}
		// 当前容器
		container := config.FindContainer(id)
		if container == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
			return
		}
		if config.NodeSupportsVirt(targetNode, container.Virtualization) == false {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: fmt.Sprintf("Target node does not support %s", container.Virtualization)})
			return
		}
		// Cluster 一致性检查：源节点和目标节点都有 ClusterID 时要求同 Cluster（除非 Force=true）。
		if !req.Force {
			if container.NodeID != "" {
				if src, ok := config.FindNode(container.NodeID); ok && src.ClusterID != "" && targetNode.ClusterID != "" && src.ClusterID != targetNode.ClusterID {
					jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Source and target nodes are in different clusters; set force=true to proceed"})
					return
				}
			}
		}
		// 原子更新 NodeID + 节点容器计数
		var oldNodeID string
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.Containers {
				if cfg.Containers[i].ID != id {
					continue
				}
				oldNodeID = cfg.Containers[i].NodeID
				cfg.Containers[i].NodeID = targetNodeID
				break
			}
		})
		config.SaveConfig()
		auditDetail := fmt.Sprintf("from=%s to=%s force=%v virt=%s", oldNodeID, targetNodeID, req.Force, container.Virtualization)
		auditRequest(r, "container.migrate", container.Name, auditDetail, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"node_id": targetNodeID}})
	case action == "usage" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:read") {
			return
		}
		// 跨节点容器：实时用量由 agent 本机计算并返回
		if routeToAgent("usage", nil) {
			return
		}
		getUsage(w, r, id)
	case action == "history" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:read") {
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: getContainerMetricHistory(c)})
	case action == "traffic" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:read") {
			return
		}
		getTraffic(w, r, id)
	case action == "traffic-reset" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:traffic") {
			return
		}
		resetTraffic(w, r, id)
	case action == "traffic-limit" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:traffic") {
			return
		}
		updateTrafficLimit(w, r, id)
	case action == "resource-limit" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:resize") {
			return
		}
		updateResourceLimit(w, r, id)
	case action == "random-port" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:network") {
			return
		}
		getRandomPort(w, r, id)
	case action == "expiry" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:resize") {
			return
		}
		updateExpiry(w, r, id)
	case action == "ipv6" && r.Method == http.MethodPost:
		if !requireScope(w, r, "ipv6:assign") {
			return
		}
		assignIPv6(w, r, id)
	case action == "public-ipv4" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:network") {
			return
		}
		updatePublicIPv4(w, r, id)
	case action == "ipv6-addresses" && r.Method == http.MethodPut:
		if !requireScope(w, r, "ipv6:assign") {
			return
		}
		updateIPv6Addresses(w, r, id)
	case action == "migrate-export" && r.Method == http.MethodGet:
		HandleContainerMigrateExport(w, r, id)
	case action == "snapshots" || strings.HasPrefix(action, "snapshots/"):
		handleContainerSnapshots(w, r, id, action)
	case action == "backups" || strings.HasPrefix(action, "backups/"):
		handleContainerBackups(w, r, id, action)
	case action == "port-mappings" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:network") {
			return
		}
		addPortMapping(w, r, id)
	case strings.HasPrefix(action, "port-mappings/") && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:network") {
			return
		}
		updatePortMapping(w, r, id, strings.TrimPrefix(action, "port-mappings/"))
	case strings.HasPrefix(action, "port-mappings/") && r.Method == http.MethodDelete:
		if !requireScope(w, r, "container:network") {
			return
		}
		deletePortMapping(w, r, id, strings.TrimPrefix(action, "port-mappings/"))
	case action == "firewall" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:network") {
			return
		}
		getFirewall(w, r, id)
	case action == "firewall" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:network") {
			return
		}
		updateFirewall(w, r, id)
	case r.Method == http.MethodGet:
		if !requireScope(w, r, "container:read") {
			return
		}
		getContainer(w, r, id)
	default:
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Action not found"})
	}
}

func listContainers(w http.ResponseWriter, r *http.Request) {
	containers, _ := listByRuntime()
	containers = filterContainersForRequest(r, containers)
	for i := range containers {
		sanitizeContainerResponse(r, &containers[i])
		// 列表为只读汇总视图，一律不回显登录口令（detail/console 需要时单独拉取）。
		containers[i].SSHPassword = ""
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: containers})
}

func createContainer(w http.ResponseWriter, r *http.Request) {
	// 幂等键：同一 Idempotency-Key 重试开通时返回既有容器，避免计费系统
	// 回调超时后重复开通（双开）。
	idemKey := normalizeIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if idemKey != "" {
		if name, ok := containerIdemLookup(idemKey); ok {
			if existing := config.FindContainerByName(name); existing != nil {
				jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Container already exists (idempotent)", Data: map[string]interface{}{
					"id":     existing.ID,
					"name":   existing.Name,
					"uuid":   existing.UUID,
					"status": existing.Status,
				}})
				return
			}
			// 记录中的容器已不存在，允许以同名重建并清理旧记录。
			containerIdemRemove(idemKey)
		}
	}

	var cfg lxc.ContainerConfig
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &cfg); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	_ = json.Unmarshal(body, &fields)
	if err := normalizeCreateResourceLimits(&cfg, fields); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if cfg.Name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Container name is required"})
		return
	}
	cfg.Virtualization = runtimeFromRequest(cfg.Virtualization)
	if cfg.TemplateID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Template is required"})
		return
	}
	if !isImageEnabledAndDownloaded(cfg.TemplateID, cfg.Virtualization) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Template is not enabled or downloaded"})
		return
	}
	if ids, err := normalizeAllowedImageIDs(cfg.AllowedImageIDs); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	} else {
		cfg.AllowedImageIDs = ids
	}
	if cfg.VCPU <= 0 {
		cfg.VCPU = 1
	}
	if cfg.RAMMB < 128 {
		cfg.RAMMB = 512
	}
	if cfg.DiskGB <= 0 {
		cfg.DiskGB = 5
	}
	if cfg.PortMappingCount < 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Port mapping count cannot be negative"})
		return
	}
	if err := cfg.NormalizeCreateNATMappings(); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := lxc.ValidateCreateNATPortAvailability(cfg); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if cfg.PortMappingCount > 64 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Port mapping count cannot exceed 64"})
		return
	}
	if cfg.IPv4Count < 0 || cfg.IPv6Count < 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "IP address count cannot be negative"})
		return
	}
	if cfg.IPv4Count > 64 || cfg.IPv6Count > 64 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "IP address count cannot exceed 64"})
		return
	}
	if !cfg.AssignIPv4 && len(cfg.PublicIPv4s) == 0 {
		cfg.IPv4Count = 0
	}
	if !cfg.AssignIPv6 && len(cfg.IPv6Addresses) == 0 {
		cfg.IPv6Count = 0
	}
	if !hasRequestedNetwork(cfg) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: noNetworkSelectedMessage})
		return
	}
	if cfg.SnapshotLimit <= 0 {
		cfg.SnapshotLimit = config.DefaultSnapshotLimit
	}
	if err := validateRuntimeResourceRequest(cfg.Virtualization, cfg.TemplateID, cfg.VCPU, cfg.RAMMB, cfg.DiskGB); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := validateCumulativeDiskQuota(cfg.DiskGB, cfg.DataDiskGB); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := validateCreateStoragePool(&cfg); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := validateCreateSSHAuth(cfg); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if cfg.ExpiresAt != "" {
		expiresAt, ok := lxc.ParseExpiration(cfg.ExpiresAt)
		if !ok {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid expiration date"})
			return
		}
		if !time.Now().Before(expiresAt) {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Expiration date must be in the future"})
			return
		}
	}

	if err := checkTenantQuota(cfg.Tenant, cfg.VCPU, cfg.RAMMB, cfg.DiskGB); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}

	if err := createByRuntime(cfg); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if idemKey != "" {
		if created := config.FindContainerByName(cfg.Name); created != nil {
			containerIdemStore(idemKey, cfg.Name)
		}
	}
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Message: "Container created successfully"})
}

func getContainer(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if c.IsKVM() && c.Status == "running" {
		_, _ = kvmManager.RefreshVNCPort(c.ID)
		_, _ = kvmManager.RefreshNetwork(c.ID)
	}
	res := *c
	sanitizeContainerResponse(r, &res)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: res})
}

// suspendContainer 实现 suspend / unsuspend（欠费停机 / 复机，供财务系统或管理员调用）。
// suspend：标记容器为挂起态；若正在运行则排入 stop 任务强制停机。
// unsuspend：清除挂起标记（不自动开机，由调用方决定是否 start）。
func suspendContainer(w http.ResponseWriter, r *http.Request, id int, suspend bool) {
	var req struct {
		Reason string `json:"reason"`
	}
	if suspend && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	reason := strings.TrimSpace(req.Reason)

	var name string
	var wasRunning bool
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].ID != id {
				continue
			}
			name = cfg.Containers[i].Name
			wasRunning = cfg.Containers[i].Status == "running"
			if suspend {
				cfg.Containers[i].Suspended = true
				cfg.Containers[i].SuspendedAt = time.Now().Format(time.RFC3339)
				cfg.Containers[i].SuspendedReason = reason
			} else {
				cfg.Containers[i].Suspended = false
				cfg.Containers[i].SuspendedAt = ""
				cfg.Containers[i].SuspendedReason = ""
			}
			return
		}
	}); err != nil || name == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
		return
	}

	// suspend 时对运行中的容器排入强制停机任务（走统一任务队列，含审计）
	if suspend && wasRunning {
		globalQueue.EnqueueWithAudit(id, name, TaskStop, "", nil, requestActor(r), clientIP(r), r.UserAgent())
	}

	action := "container.suspend"
	message := "Container suspended"
	if !suspend {
		action = "container.unsuspend"
		message = "Container unsuspended"
	}
	auditRequest(r, action, name, "reason="+reason, true, "")
	// 邮件通知容器属主（配置了 SMTP 且属主有邮箱时）
	if suspend {
		notifyContainerOwner(name, "服务器已挂起："+name,
			"您的服务器 "+name+" 已被挂起。"+reasonNotice(reason)+"期间服务器将保持关机且无法开机。如有疑问请联系管理员。",
			notify.SeverityWarning)
	} else {
		notifyContainerOwner(name, "服务器已恢复："+name,
			"您的服务器 "+name+" 已解除挂起，现在可以正常开机使用了。",
			notify.SeverityInfo)
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: message})
}

// reasonNotice 把挂起原因转成邮件里的自然语言片段。
func reasonNotice(reason string) string {
	if reason == "" {
		return ""
	}
	return "原因：" + reason + "。"
}

func getUsage(w http.ResponseWriter, r *http.Request, id int) {
	usage, err := usageByRuntime(id)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: usage})
}

func getTraffic(w http.ResponseWriter, r *http.Request, id int) {
	info := trafficByRuntime(id)
	if info == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: info})
}

func updateExpiry(w http.ResponseWriter, r *http.Request, id int) {
	var req struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	if ok := config.MutateContainerNoSave(id, func(c *config.Container) {
		c.ExpiresAt = req.ExpiresAt
	}); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Expiry updated"})
}

func resetTraffic(w http.ResponseWriter, r *http.Request, id int) {
	if !config.MutateContainerNoSave(id, func(c *config.Container) {
		c.TrafficUsedRX = 0
		c.TrafficUsedTX = 0
		c.TrafficResetDate = time.Now().Format("2006-01")
	}) {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	config.SaveConfig()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Traffic reset"})
}

func updateTrafficLimit(w http.ResponseWriter, r *http.Request, id int) {
	var req struct {
		Mode         string `json:"traffic_mode"`
		MonthlyGB    int    `json:"monthly_traffic_gb"`
		TrafficInGB  int    `json:"traffic_in_gb"`
		TrafficOutGB int    `json:"traffic_out_gb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	if !config.MutateContainerNoSave(id, func(c *config.Container) {
		c.TrafficMode = req.Mode
		c.MonthlyTrafficGB = req.MonthlyGB
		c.TrafficInGB = req.TrafficInGB
		c.TrafficOutGB = req.TrafficOutGB
	}) {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	config.SaveConfig()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Traffic limit updated"})
}

func updateResourceLimit(w http.ResponseWriter, r *http.Request, id int) {
	var req struct {
		VCPU            *float64 `json:"vcpu"`
		RAMMB           *int     `json:"ram_mb"`
		DiskGB          *float64 `json:"disk_gb"`
		IOMBps          *int     `json:"io_speed_mbps"`
		IOReadMBps      *int     `json:"io_read_mbps"`
		IOWriteMBps     *int     `json:"io_write_mbps"`
		BWMbps          *int     `json:"network_bw_mbps"`
		NetworkDownMbps *int     `json:"network_down_mbps"`
		NetworkUpMbps   *int     `json:"network_up_mbps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}

	// 系统盘扩容：仅允许扩大，缩小必须显式报错。物理扩容失败时不提交配置，
	// 防止配置与真实磁盘容量漂移。
	if req.DiskGB != nil {
		newDiskGB := *req.DiskGB
		if newDiskGB < c.DiskGB {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "disk_gb can only be expanded, change below current size is not allowed"})
			return
		}
		if newDiskGB > c.DiskGB {
			if err := resizeDiskByRuntime(c, newDiskGB); err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
	}

	// Update config
	nextVCPU := c.VCPU
	nextRAMMB := c.RAMMB
	if req.VCPU != nil {
		nextVCPU = *req.VCPU
	}
	if req.RAMMB != nil {
		nextRAMMB = *req.RAMMB
	}
	if err := validateRuntimeResourceRequest(c.Runtime(), c.Template, nextVCPU, nextRAMMB, c.DiskGB); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	for name, value := range map[string]*int{
		"network_bw_mbps":   req.BWMbps,
		"network_down_mbps": req.NetworkDownMbps,
		"network_up_mbps":   req.NetworkUpMbps,
		"io_speed_mbps":     req.IOMBps,
		"io_read_mbps":      req.IOReadMBps,
		"io_write_mbps":     req.IOWriteMBps,
	} {
		if err := rejectNegativeLimit(name, value); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}

	// 在写锁下原子更新容器限流字段并持久化，避免与指标采样/策略引擎并发读写共享切片。
	var ok bool
	ok, c = config.MutateContainerByID(id, func(cc *config.Container) {
		cc.VCPU = nextVCPU
		cc.RAMMB = nextRAMMB
		if req.DiskGB != nil {
			cc.DiskGB = *req.DiskGB
		}
		applyNetworkLimitPatch(cc, req.BWMbps, req.NetworkDownMbps, req.NetworkUpMbps)
		applyIOLimitPatch(cc, req.IOMBps, req.IOReadMBps, req.IOWriteMBps)
		config.NormalizeContainerResourceAliases(cc)
	})
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}

	// Re-apply persisted/runtime limits. LXC also uses this path to migrate
	// old managed config lines such as lxc.prlimit.nproc.
	if err := applyLimitsByRuntime(c); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	msg := "Resource limits updated"
	if c.IsKVM() && c.Status == "running" {
		msg = "资源已保存，请关机重启虚拟机后生效"
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: msg})
}

func normalizeCreateResourceLimits(cfg *lxc.ContainerConfig, fields map[string]json.RawMessage) error {
	if cfg == nil {
		return nil
	}
	if err := rejectNegativeCreateLimits(*cfg); err != nil {
		return err
	}
	bwSet := hasJSONField(fields, "network_bw_mbps")
	downSet := hasJSONField(fields, "network_down_mbps")
	upSet := hasJSONField(fields, "network_up_mbps")
	if bwSet {
		if !downSet {
			cfg.NetworkDownMbps = cfg.NetworkBWMbps
		}
		if !upSet {
			cfg.NetworkUpMbps = cfg.NetworkBWMbps
		}
	}
	ioSet := hasJSONField(fields, "io_speed_mbps")
	readSet := hasJSONField(fields, "io_read_mbps")
	writeSet := hasJSONField(fields, "io_write_mbps")
	if ioSet {
		if !readSet {
			cfg.IOReadMBps = cfg.IOSpeedMBps
		}
		if !writeSet {
			cfg.IOWriteMBps = cfg.IOSpeedMBps
		}
	}
	if diskRaw, ok := fields["disk_gb"]; ok {
		var s string
		if err := json.Unmarshal(diskRaw, &s); err == nil {
			s = strings.TrimSpace(strings.ToUpper(s))
			if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "MB") {
				numStr := strings.TrimSuffix(strings.TrimSuffix(s, "MB"), "M")
				if mb, err := strconv.ParseFloat(numStr, 64); err == nil && mb > 0 {
					cfg.DiskGB = mb / 1024.0
				}
			} else if strings.HasSuffix(s, "G") || strings.HasSuffix(s, "GB") {
				numStr := strings.TrimSuffix(strings.TrimSuffix(s, "GB"), "G")
				if gb, err := strconv.ParseFloat(numStr, 64); err == nil && gb > 0 {
					cfg.DiskGB = gb
				}
			} else if val, err := strconv.ParseFloat(s, 64); err == nil && val > 0 {
				cfg.DiskGB = val
			}
		}
	}
	if cfg.DiskGB <= 0 {
		if diskMBRaw, ok := fields["disk_mb"]; ok {
			var mb float64
			if err := json.Unmarshal(diskMBRaw, &mb); err == nil && mb > 0 {
				cfg.DiskGB = mb / 1024.0
			}
		}
	}
	cfg.NormalizeResourceAliases()
	return nil
}

func rejectNegativeCreateLimits(cfg lxc.ContainerConfig) error {
	for name, value := range map[string]int{
		"network_bw_mbps":   cfg.NetworkBWMbps,
		"network_down_mbps": cfg.NetworkDownMbps,
		"network_up_mbps":   cfg.NetworkUpMbps,
		"io_speed_mbps":     cfg.IOSpeedMBps,
		"io_read_mbps":      cfg.IOReadMBps,
		"io_write_mbps":     cfg.IOWriteMBps,
	} {
		if value < 0 {
			return fmt.Errorf("%s cannot be negative", name)
		}
	}
	return nil
}

func hasJSONField(fields map[string]json.RawMessage, name string) bool {
	if fields == nil {
		return false
	}
	_, ok := fields[name]
	return ok
}

func rejectNegativeLimit(name string, value *int) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s cannot be negative", name)
	}
	return nil
}

func applyNetworkLimitPatch(c *config.Container, legacy *int, down *int, up *int) {
	if c == nil {
		return
	}
	config.NormalizeContainerResourceAliases(c)
	nextDown := c.NetworkDownMbps
	nextUp := c.NetworkUpMbps
	if legacy != nil {
		nextDown = *legacy
		nextUp = *legacy
	}
	if down != nil {
		nextDown = *down
	}
	if up != nil {
		nextUp = *up
	}
	c.NetworkDownMbps = nextDown
	c.NetworkUpMbps = nextUp
	c.NetworkBWMbps = config.LegacySymmetricLimit(nextDown, nextUp)
}

func applyIOLimitPatch(c *config.Container, legacy *int, read *int, write *int) {
	if c == nil {
		return
	}
	config.NormalizeContainerResourceAliases(c)
	nextRead := c.IOReadMBps
	nextWrite := c.IOWriteMBps
	if legacy != nil {
		nextRead = *legacy
		nextWrite = *legacy
	}
	if read != nil {
		nextRead = *read
	}
	if write != nil {
		nextWrite = *write
	}
	c.IOReadMBps = nextRead
	c.IOWriteMBps = nextWrite
	c.IOSpeedMBps = config.LegacySymmetricLimit(nextRead, nextWrite)
}

func getRandomPort(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	hostIP := strings.TrimSpace(r.URL.Query().Get("host_ip"))
	start, end := config.NATPortRange()
	capacity := end - start + 1
	offset := 0
	if capacity > 0 {
		offset = int(time.Now().UnixNano() % int64(capacity))
	}
	for tries := 0; tries < capacity; tries++ {
		port := start + ((offset + tries) % capacity)
		if lxc.HostPortAvailable(c, hostIP, port, "tcp") {
			jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]int{"port": port}})
			return
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]int{"port": 0}})
}

// HandleTemplates returns available LXC templates
func HandleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "image:read") {
		return
	}
	if isSubUserRequest(r) {
		HandleEnabledImages(w, r)
		return
	}
	templates := lxc.GetTemplates()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: templates})
}

// HandleDashboard returns dashboard stats
func HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "dashboard:read") {
		return
	}
	containers, _ := listByRuntime()
	containers = filterContainersForRequest(r, containers)
	running := 0
	stopped := 0
	for _, c := range containers {
		if c.Status == "running" {
			running++
		} else {
			stopped++
		}
	}
	stats := map[string]interface{}{
		"total_containers": len(containers),
		"running":          running,
		"stopped":          stopped,
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: stats})
}

// HandleHostInfo returns host machine resource info
func HandleHostInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "host:read") {
		return
	}
	info := getHostInfo()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: info})
}

// HandleHostHistory returns host resource samples collected by the server.
func HandleHostHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "host:read") {
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: getHostMetricHistory()})
}

func resetSSHPassword(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c != nil && lxc.IsExpired(*c) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已到期，不允许此操作"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
	}
	password := strings.TrimSpace(req.Password)
	if password != "" {
		if err := validateSSHPassword(password); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	newPassword, err := resetPasswordByRuntime(id, password)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "SSH password reset successfully",
		Data:    map[string]string{"password": newPassword},
	})
}

func validateSSHPassword(password string) error {
	return lxc.ValidateCustomSSHPassword(password)
}

// createContainerAccount 在容器内创建新的登录账号（LXC 走 chroot，KVM 走 guest-agent/SSH）。
func createContainerAccount(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c != nil && lxc.IsExpired(*c) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已到期，不允许此操作"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Sudo     bool   `json:"sudo"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
	}
	if err := lxc.ValidateAccountUsername(req.Username); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := lxc.ValidateAccountPassword(req.Password); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := createAccountByRuntime(id, req.Username, req.Password, req.Sudo); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	name := ""
	if c != nil {
		name = c.Name
	}
	auditRequest(r, "container.create_account", name,
		fmt.Sprintf("user=%s sudo=%v", strings.TrimSpace(req.Username), req.Sudo), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Account created successfully",
		Data:    map[string]interface{}{"username": strings.TrimSpace(req.Username), "sudo": req.Sudo},
	})
}

func addPortMapping(w http.ResponseWriter, r *http.Request, id int) {
	var pm config.PortMapping
	if err := json.NewDecoder(r.Body).Decode(&pm); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	mappings, err := lxcManager.AddPortMapping(id, pm)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: mappings})
}

func updatePortMapping(w http.ResponseWriter, r *http.Request, id int, indexStr string) {
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid port mapping index"})
		return
	}
	var pm config.PortMapping
	if err := json.NewDecoder(r.Body).Decode(&pm); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if isSubUserRequest(r) {
		c := config.FindContainer(id)
		if c == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
			return
		}
		if index < 0 || index >= len(c.PortMappings) {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid port mapping index"})
			return
		}
		if pm.ContainerPort < 1 || pm.ContainerPort > 65535 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container port must be 1-65535"})
			return
		}
		existing := c.PortMappings[index]
		pm = config.PortMapping{
			ContainerPort: pm.ContainerPort,
			HostPort:      existing.HostPort,
			Protocol:      existing.Protocol,
			Description:   existing.Description,
		}
	}
	mappings, err := lxcManager.UpdatePortMapping(id, index, pm)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: mappings})
}

func deletePortMapping(w http.ResponseWriter, r *http.Request, id int, indexStr string) {
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid port mapping index"})
		return
	}
	mappings, err := lxcManager.DeletePortMapping(id, index)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: mappings})
}

// HandleVersion returns the current EYVESCLOUD version.
func HandleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"version": version.Current(),
	}})
}
