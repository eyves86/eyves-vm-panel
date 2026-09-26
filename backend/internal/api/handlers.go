package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/notify"
	"eyvescloud/internal/version"
)

// containerNameRegex 容器名允许字符：首字符字母数字，后续允许字母数字 - _ .，
// 整体 1-64 字符。覆盖 LXC 与 KVM 内部命名 / cgroup 子系统 / cloud-init 主机名要求。
var containerNameRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validContainerName(name string) bool {
	return containerNameRegex.MatchString(name)
}

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
		if routeToAgent("create-account", r.Body) {
			return
		}
		createContainerAccount(w, r, id)
	case action == "hostname" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("hostname", r.Body) {
			return
		}
		changeContainerHostname(w, r, id, c)
	case action == "vnc-password" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		if routeToAgent("vnc-password", r.Body) {
			return
		}
		changeContainerVNCPassword(w, r, id, c)
	case action == "clone" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:power") {
			return
		}
		cloneContainer(w, r, id)
	case strings.HasPrefix(action, "security-groups"):
		if !requireScope(w, r, "container:power") {
			return
		}
		HandleContainerSecGroups(w, r)
	case action == "tags" && r.Method == http.MethodPut:
		// 资源标签管理（企业成本分摊 / 过滤）：整体替换容器标签集。
		// 子用户可管理自己容器的标签；key/value ≤128 字符，最多 20 个。
		if !requireScope(w, r, "container:power") {
			return
		}
		updateContainerTags(w, r, id)
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
		// 多节点路由：快照的运行时操作（创建/删除/恢复）需转发到被控 agent；
		// schedule / quota / list 是配置侧，由主控处理。
		if c.NodeID != "" && (
			(action == "snapshots" && r.Method == http.MethodPost) || // create
				strings.HasPrefix(action, "snapshots/") && strings.HasSuffix(action, "/restore") && r.Method == http.MethodPost || // restore
				strings.HasPrefix(action, "snapshots/") && r.Method == http.MethodDelete) { // delete
			node, ok := config.FindNode(c.NodeID)
			if !ok || node.Address == "" {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "容器所属节点不可用: " + c.NodeID})
				return
			}
			var agentPath string
			var agentBodyStr string
			switch {
			case r.Method == http.MethodDelete:
				sid := strings.TrimPrefix(action, "snapshots/")
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshots/delete", id)
				b, _ := json.Marshal(map[string]interface{}{"snapshot_id": sid})
				agentBodyStr = string(b)
			case strings.HasSuffix(action, "/restore"):
				sid := strings.TrimSuffix(strings.TrimPrefix(action, "snapshots/"), "/restore")
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshots/restore", id)
				b, _ := json.Marshal(map[string]interface{}{"snapshot_id": sid})
				agentBodyStr = string(b)
			default:
				bodyBytes, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshot", id)
				agentBodyStr = string(bodyBytes)
			}
			data, status, err := proxyNodeRequest(r, node, http.MethodPost,
				agentPath, strings.NewReader(agentBodyStr))
			if err != nil {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(data)
			return
		}
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
	case action == "rdns" && r.Method == http.MethodGet:
		if !requireScope(w, r, "container:network") {
			return
		}
		getReverseDNS(w, r, id)
	case action == "rdns" && r.Method == http.MethodPut:
		if !requireScope(w, r, "container:network") {
			return
		}
		updateReverseDNS(w, r, id)
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
	// 标签过滤（企业成本分摊 / 按标签过滤，类比 AWS DescribeInstances Filters）。
	// 支持两种形式：?tag=key:value（精确匹配）；?tag-key=key（存在性匹配）。
	if tagFilter := strings.TrimSpace(r.URL.Query().Get("tag")); tagFilter != "" {
		if k, v, ok := strings.Cut(tagFilter, ":"); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			filtered := containers[:0]
			for _, c := range containers {
				if c.Tags[k] == v {
					filtered = append(filtered, c)
				}
			}
			containers = filtered
		}
	}
	if tagKeyFilter := strings.TrimSpace(r.URL.Query().Get("tag-key")); tagKeyFilter != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if _, ok := c.Tags[tagKeyFilter]; ok {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}
	for i := range containers {
		sanitizeContainerResponse(r, &containers[i])
		// 列表为只读汇总视图，一律不回显登录口令（detail/console 需要时单独拉取）。
		containers[i].SSHPassword = ""
	}
	// 分页：未传 page/page_size 时保持全量数组返回（向后兼容）。
	p := parsePagination(r)
	if p.Invalid {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST",
			"page must be >= 1 and page_size within [1, 200]")
		return
	}
	if p.Requested {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true,
			Data: pagedEnvelope(paginate(containers, p), len(containers), p.Page, p.PageSize)})
		return
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
	// 容器名约束：首字符必须为字母/数字，后续允许字母数字 - _ . ，
	// 长度上限 64，与 LXC/KVM 内部命名空间与 cgroup 子系统兼容。
	if len(cfg.Name) > 64 || !validContainerName(cfg.Name) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Container name must match ^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$ and be ≤64 chars",
		})
		return
	}
	// SSH 公钥直接注入场景（cfg.SSHPublicKeys 中内联公钥字符串）：
	// 限制单把公钥最大 16 KB（OpenSSH 默认上限），总数不超过 16 把，
	// 防止恶意大字符串攻击底层 lxc-attach / cloud-init 写入路径。
	const maxSSHKeyBytes = 16 * 1024
	if len(cfg.SSHPublicKeys) > 16 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Too many SSH public keys (max 16)",
		})
		return
	}
	for i, k := range cfg.SSHPublicKeys {
		if len(k) > maxSSHKeyBytes {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: fmt.Sprintf("SSH public key #%d exceeds 16 KB", i+1),
			})
			return
		}
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

	// SSH 密钥托管：把用户在 ssh_key_ids 里指定的 SK-xxx 从 config 里查出实际公钥，
	// 合并到 cfg.SSHPublicKeys（供 ResolveCreateSSHAccess 聚合使用）。
	// 容器创建成功后，SSHKeyIDs 会被持久化到 config.Container.SSHKeyIDs。
	if len(cfg.SSHKeyIDs) > 0 {
		pubKeys, missing := ResolveSSHKeyIDs(cfg.SSHKeyIDs)
		if len(missing) > 0 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "部分 SSH Key 不存在: " + strings.Join(missing, ", "),
			})
			return
		}
		cfg.SSHPublicKeys = append(cfg.SSHPublicKeys, pubKeys...)
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

	// DryRun（企业级 API 契约）：仅执行全部校验并返回"将要创建"的规划结果，
	// 不落盘、不调用运行时层。集成方可用它在开通前验证参数（类比 AWS RunInstances DryRun）。
	if dryRun := parseDryRun(fields, r); dryRun {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Dry run passed (no container was created)", Data: map[string]interface{}{
			"dry_run":        true,
			"name":           cfg.Name,
			"virtualization": cfg.Virtualization,
			"template_id":    cfg.TemplateID,
			"vcpu":           cfg.VCPU,
			"ram_mb":         cfg.RAMMB,
			"disk_gb":        cfg.DiskGB,
			"tenant":         cfg.Tenant,
			"expires_at":     cfg.ExpiresAt,
		}})
		return
	}

	if err := createByRuntime(cfg); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	// 持久化 SSHKeyIDs 到 config.Container（运行时层不感知此业务字段，需额外同步）。
	if len(cfg.SSHKeyIDs) > 0 {
		if created := config.FindContainerByName(cfg.Name); created != nil {
			config.MutateContainerNoSave(created.ID, func(c *config.Container) {
				c.SSHKeyIDs = cfg.SSHKeyIDs
			})
			config.SaveConfig()
		}
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

// updateContainerTags 整体替换容器标签（企业成本分摊 / 按标签过滤）。
// 请求体：{"tags": {"team": "infra", "env": "prod"}}；传空对象清除全部标签。
// 校验：key/value 非空且 ≤128 字符（去除首尾空白），最多 20 个。
func updateContainerTags(w http.ResponseWriter, r *http.Request, id int) {
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	if len(req.Tags) > 20 {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "too many tags (max 20)")
		return
	}
	for k, v := range req.Tags {
		k = strings.TrimSpace(k)
		if k == "" || len(k) > 128 {
			errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "tag key must be non-empty and <= 128 chars")
			return
		}
		if len(v) > 128 {
			errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "tag value must be <= 128 chars")
			return
		}
	}
	normalized := make(map[string]string, len(req.Tags))
	for k, v := range req.Tags {
		normalized[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(normalized) == 0 {
		normalized = nil
	}
	updated, c := config.MutateContainerByID(id, func(c *config.Container) {
		c.Tags = normalized
	})
	if !updated {
		errResponse(w, http.StatusNotFound, "NOT_FOUND", "Container not found")
		return
	}
	if err := config.SaveConfig(); err != nil {
		errResponse(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save config")
		return
	}
	auditRequest(r, "container.tags", c.Name,
		fmt.Sprintf("tags=%d", len(normalized)), true, "")
	if c.Tags == nil {
		c.Tags = map[string]string{}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{"tags": c.Tags}})
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
			// 多节点路由：容器在被控节点上时，运行时扩容转发到所属 agent。
			if c.NodeID != "" {
				node, ok := config.FindNode(c.NodeID)
				if !ok || node.Address == "" {
					jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "容器所属节点不可用: " + c.NodeID})
					return
				}
				agentBody, _ := json.Marshal(map[string]interface{}{"disk_gb": newDiskGB})
				data, status, err := proxyNodeRequest(r, node, http.MethodPost,
					fmt.Sprintf("/api/agent/containers/%d/resize", id), strings.NewReader(string(agentBody)))
				if err != nil {
					jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
					return
				}
				if status < 200 || status >= 300 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write(data)
					return
				}
			} else {
				if err := resizeDiskByRuntime(c, newDiskGB); err != nil {
					jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
					return
				}
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

// changeContainerHostname 实时修改运行中容器的 hostname（类比 Virtualizor Change Hostname）。
// 运行中的 LXC 走 lxc-attach；运行中的 KVM 优先 virsh set-hostname，回退 SSH 进入。
// 停止的容器报错提示（hostname 需要运行时才能实时生效）。
func changeContainerHostname(w http.ResponseWriter, r *http.Request, id int, c *config.Container) {
	var req struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "hostname is required"})
		return
	}
	if len(hostname) > 63 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "hostname too long (max 63 chars)"})
		return
	}
	// 只允许 [a-z0-9-]（RFC 1123），避免 shell 注入
	for _, ch := range hostname {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "hostname must match [a-z0-9-] (RFC 1123)"})
			return
		}
	}

	if err := setHostnameByRuntime(id, hostname); err != nil {
		auditRequest(r, "container.hostname", c.Name, "new="+hostname+" err="+err.Error(), false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "container.hostname", c.Name, "new="+hostname, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "hostname changed successfully",
		Data:    map[string]string{"hostname": hostname},
	})
}

// changeContainerVNCPassword 修改 KVM VM 的 VNC 密码（类比 Virtualizor Change VNC Password）。
// 请求体：{ password: "xxx" } 或 { password: "" } 清空密码。
// 同时持久化 VNCPassword 到 config。运行中 VM 尝试热更新。
func changeContainerVNCPassword(w http.ResponseWriter, r *http.Request, id int, c *config.Container) {
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
	password := req.Password
	if len(password) > 255 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "vnc password too long (max 255 chars)"})
		return
	}

	// 持久化 VNCPassword 到 config.Container
	config.MutateContainerNoSave(id, func(cc *config.Container) {
		cc.VNCPassword = password
	})
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
		return
	}

	err := setVNCPasswordByRuntime(id, password)
	if err != nil {
		auditRequest(r, "container.vnc_password", c.Name, "err="+err.Error(), false, err.Error())
		// 如果是 LXC 不支持，回滚 config 并返回错误
		if !c.IsKVM() {
			config.MutateContainerNoSave(id, func(cc *config.Container) {
				cc.VNCPassword = ""
			})
			config.SaveConfig()
		}
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}

	auditDetail := "cleared"
	if password != "" {
		auditDetail = "set"
	}
	auditRequest(r, "container.vnc_password", c.Name, auditDetail, true, "")

	msg := "VNC password changed successfully"
	if password == "" {
		msg = "VNC password cleared successfully"
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: msg})
}

// cloneContainer 克隆容器（类比 Virtualizor Clone VPS）。
// 请求体：{ name (必填), mode: "full"|"linked", start_after_clone: bool }
// 分配新 ID/UUID/MAC/VNCPort/SSHPort，拷贝源容器的配额/网络/快照/SSH Key 绑定等配置。
func cloneContainer(w http.ResponseWriter, r *http.Request, srcID int) {
	src := config.FindContainer(srcID)
	if src == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Source container not found"})
		return
	}

	var req struct {
		Name            string `json:"name"`
		Mode            string `json:"mode"`
		StartAfterClone bool   `json:"start_after_clone"`
		DryRun          bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "clone name is required"})
		return
	}
	if len(req.Name) > 63 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name too long (max 63 chars)"})
		return
	}
	if config.FindContainerByName(req.Name) != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "container with this name already exists"})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "linked" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "mode must be 'full' or 'linked'"})
		return
	}
	// 查询参数兜底：?dry_run=true（与请求体形式等价，保持与 create 一致）。
	if !req.DryRun {
		if v := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("dry_run"))); v == "true" || v == "1" {
			req.DryRun = true
		}
	}

	// DryRun（企业级 API 契约）：验证通过后仅返回克隆规划，不分配 ID、
	// 不占用端口段、不触碰运行时层。
	if req.DryRun {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Dry run passed (no clone was performed)", Data: map[string]interface{}{
			"dry_run":           true,
			"source_id":         src.ID,
			"source_name":       src.Name,
			"clone_name":        req.Name,
			"mode":              mode,
			"start_after_clone": req.StartAfterClone,
			"virtualization":    src.Virtualization,
			"vcpu":              src.VCPU,
			"ram_mb":            src.RAMMB,
			"disk_gb":           src.DiskGB,
			"ssh_key_ids":       len(src.SSHKeyIDs),
			"sec_group_ids":     len(src.SecGroupIDs),
			"tags":              len(src.Tags),
		}})
		return
	}

	// 从 config 分配新容器标识
	config.AppConfigMu.Lock()
	newID := config.AppConfig.NextContainerID
	config.AppConfig.NextContainerID++
	newVNCPort := config.AppConfig.NextVNCPort
	config.AppConfig.NextVNCPort++
	newSSHPort := config.AppConfig.NextSSHPort
	config.AppConfig.NextSSHPort++
	newUUID := "c-" + randomHex(12) + "-" + randomHex(4) + "-" + randomHex(4) + "-" + randomHex(4) + "-" + randomHex(12)
	// 新 MAC（前缀 02:00:00 保留给虚拟化）
	newMAC := fmt.Sprintf("02:00:00:%02x:%02x:%02x",
		rand.Intn(256), rand.Intn(256), rand.Intn(256))
	config.AppConfigMu.Unlock()

	newLxcName := fmt.Sprintf("ct-%d", newID)
	newVMName := fmt.Sprintf("vm-%d", newID)

	// 多节点路由：源容器在被控节点上时，运行时克隆转发到所属 agent。
	if src.NodeID != "" {
		node, ok := config.FindNode(src.NodeID)
		if !ok || node.Address == "" {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "源容器所属节点不可用: " + src.NodeID})
			return
		}
		agentBody, _ := json.Marshal(map[string]interface{}{
			"name":              req.Name,
			"new_id":            newID,
			"new_uuid":          newUUID,
			"new_lxc_name":      newLxcName,
			"new_vm_name":       newVMName,
			"new_vnc_port":      strconv.Itoa(newVNCPort),
			"new_ssh_port":      strconv.Itoa(newSSHPort),
			"new_mac":           newMAC,
			"mode":              mode,
			"start_after_clone": req.StartAfterClone,
		})
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/clone", src.ID), strings.NewReader(string(agentBody)))
		if err != nil {
			auditRequest(r, "container.clone", src.Name, "target="+req.Name+" agent-err="+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
			return
		}
		// agent 返回非 2xx 直接透传
		if status < 200 || status >= 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(data)
			return
		}
	} else {
		// 本机容器：本地运行时克隆
		if err := cloneByRuntime(src, req.Name, newLxcName, newVMName,
			newID, newUUID, strconv.Itoa(newVNCPort), strconv.Itoa(newSSHPort), newMAC,
			mode, req.StartAfterClone); err != nil {
			auditRequest(r, "container.clone", src.Name, "target="+req.Name+" err="+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}

	// 运行时克隆成功 → 创建 config.Container 记录（主控是配置权威）
	now := time.Now().UTC().Format(time.RFC3339)
	newContainer := *src // 复制源容器所有字段
	newContainer.ID = newID
	newContainer.UUID = newUUID
	newContainer.Name = req.Name
	newContainer.LXCName = newLxcName
	newContainer.KVMName = newVMName
	newContainer.MACAddress = newMAC
	newContainer.VNCPort = newVNCPort
	newContainer.SSHPort = newSSHPort
	newContainer.IP = ""                     // 新容器重新获取
	newContainer.LANIPv4Address = ""
	newContainer.PublicIPv4s = []config.PublicIPv4Assignment{} // 需要重新分配或从池取
	newContainer.IPv6 = ""
	newContainer.IPv6Addresses = []config.IPv6Assignment{}
	newContainer.TrafficUsedRX = 0
	newContainer.TrafficUsedTX = 0
	newContainer.TrafficResetDate = now
	newContainer.CreatedAt = now
	newContainer.ExpiresAt = ""   // 克隆不继承到期时间，由用户重新设置
	newContainer.Status = "stopped"
	newContainer.SnapshotScheduleLastRun = ""
	newContainer.SnapshotScheduleNextRun = ""

	// 拷贝 SSHKeyIDs 绑定（如果源容器有）
	if len(src.SSHKeyIDs) > 0 {
		newContainer.SSHKeyIDs = append([]string(nil), src.SSHKeyIDs...)
	}

	// 拷贝标签（深拷贝：结构体赋值是浅拷贝，共享 map 会让克隆的标签修改污染源容器）
	if len(src.Tags) > 0 {
		newContainer.Tags = make(map[string]string, len(src.Tags))
		for k, v := range src.Tags {
			newContainer.Tags[k] = v
		}
	}

	// 保存
	var ok bool
	ok = false
	config.AppConfigMu.Lock()
	config.AppConfig.Containers = append(config.AppConfig.Containers, newContainer)
	config.SaveConfig()
	config.AppConfigMu.Unlock()
	_ = ok

	// 给 PublicIPv4s 分配（从池中取，保持与创建流程一致）
	// 这里简化：不自动分配，让用户后续通过 /api/containers/{id}/public-ipv4 分配
	// 如果源容器有 public IP 且不冲突，可以复制

	auditRequest(r, "container.clone", src.Name,
		fmt.Sprintf("target=%s new_id=%d mode=%s", req.Name, newID, mode), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Container cloned successfully",
		Data: map[string]interface{}{
			"new_id":    newID,
			"new_name":  req.Name,
			"uuid":      newUUID,
			"mode":      mode,
			"lxc_name":  newLxcName,
			"vnc_port":  newVNCPort,
			"ssh_port":  newSSHPort,
		},
	})
}
