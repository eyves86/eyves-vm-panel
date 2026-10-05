package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// isSuspendedBlockedManageAction 判断已挂起（欠费停机）容器是否拒绝该管理操作。
// 电源类（start/restart/reinstall）不在此列——它们还要额外拦截到期/流量超限，
// 由调用方单独处理。豁免项（suspend/unsuspend/stop/delete/计费字段/密码/主机名等）
// 是管理恢复路径，必须保持可用。
func isSuspendedBlockedManageAction(action, method string) bool {
	switch action {
	case "iso", "rescue", "recipes/execute", "processes/kill", "services":
		return method == http.MethodPost
	case "firewall", "port-mappings":
		return method == http.MethodPost || method == http.MethodPut
	case "snapshots", "backups":
		// 快照/备份列表本身是 GET，POST 为创建/还原。
		return method == http.MethodPost
	}
	if strings.HasPrefix(action, "port-mappings/") {
		// NAT 端口映射的更新/删除（PUT/DELETE）。
		return method == http.MethodPut || method == http.MethodDelete
	}
	if strings.HasPrefix(action, "snapshots/") && method == http.MethodPost {
		// 快照还原（snapshots/{id}/restore）。
		return true
	}
	if strings.HasPrefix(action, "backups/") && strings.HasSuffix(action, "/restore") {
		// 备份还原（backups/{id}/restore）。
		return true
	}
	return false
}

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
		// 快照可能属于已删除的容器（孤儿快照）。此时无法通过容器做属主校验，
		// 但涉及删除/恢复的高危操作必须限管理员，避免子用户/窄作用域 Key
		// 借由孤儿快照越权操作他人数据。
		if c := config.FindContainer(id); c != nil {
			if !isContainerAllowedForRequest(r, c.UUID) {
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
				return
			}
		} else if !isAdminRequest(r) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this snapshot"})
			return
		}
	}

	// 只读子用户（viewer）禁止一切写操作
	if r.Method != http.MethodGet && !requireSubUserWrite(w, r) {
		return
	}

	// 欠费停机（Suspended）与到期/流量超限拦截。
	// 本地容器的电源操作由任务队列执行侧兜底检查（taskqueue.go runOperationTask），
	// 但跨节点容器走 routeToAgent 代理路径不经过任务队列，必须在转发前统一拦截。
	// 管理必需操作（suspend/unsuspend/stop/delete/计费字段调整）不在此列。
	if c != nil && r.Method != http.MethodGet {
		if action == "start" || action == "restart" || action == "reinstall" {
			// 电源类操作：挂起/到期/流量超限三者全部拦截（与任务队列同一语义）。
			switch {
			case c.Suspended:
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已挂起（欠费停机），不允许此操作"})
				return
			case lxc.IsExpired(*c):
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已到期，不允许此操作"})
				return
			case lxc.IsTrafficExceeded(*c):
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器流量已超限，不允许此操作"})
				return
			}
		} else if c.Suspended && isSuspendedBlockedManageAction(action, r.Method) {
			// 挂起容器的破坏性管理操作：快照/备份还原、NAT、防火墙、ISO、救援、脚本执行等。
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已挂起（欠费停机），不允许此操作"})
			return
		}
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
			fmt.Sprintf("/api/agent/containers/%d/%s", nodeLocalID(c), agentAction), body)
		if err != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
			return true
		}
		// destroy 成功 = 被控已确认销毁：主控侧记录立即清除。
		if agentAction == "destroy" && status < 300 {
			removeNodeContainerRecord(c.UUID)
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
		// 回收站语义（同类商业面板/同类商业面板同款）：DELETE 默认软删除（进回收站，可恢复），
		// ?purge=true 才真正销毁。计费系统 Terminate 用 purge（释放资源语义）。
		if strings.EqualFold(r.URL.Query().Get("purge"), "true") {
			if routeToAgent("destroy", nil) {
				return
			}
			HandleSingleTaskAction(w, r, id, "delete")
			return
		}
		name, wasRunning, err := config.RecycleContainer(id, "deleted")
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if wasRunning {
			if c.NodeID != "" {
				// 节点容器：本地队列不认识它，代理被控停机（best-effort）。
				if node, ok := config.FindNode(c.NodeID); ok && node.Address != "" {
					_, _, _ = proxyNodeRequest(r, node, http.MethodPost,
						fmt.Sprintf("/api/agent/containers/%d/stop", nodeLocalID(c)), nil)
				}
			} else {
				// 本机容器：排停机任务（异步），回收站中的实例保持关机。
				globalQueue.EnqueueWithAudit(id, name, TaskStop, "", nil, requestActor(r), clientIP(r), r.UserAgent())
			}
		}
		auditRequest(r, "container.recycle", name, "移入回收站（可恢复）", true, "")
		jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "Container moved to recycle bin", Data: map[string]interface{}{
			"id": id, "name": name, "recycled": true,
			"hint": "彻底删除请调用 POST /api/containers/{id}/purge 或等待保留期自动清理",
		}})
	case action == "restore" && r.Method == http.MethodPost:
		// 回收站恢复（v2 为主契约；v1 保留同语义入口供旧集成使用）。
		restoreContainerAction(w, r, c)
	case action == "purge" && r.Method == http.MethodPost:
		// 彻底删除（真销毁）：回收站模型唯一的真删除入口之一。
		purgeContainerAction(w, r, c)
	case action == "reset-password" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:password") {
			return
		}
		if routeToAgent("reset-password", r.Body) {
			return
		}
		resetSSHPassword(w, r, id)
	case action == "rotate-access-code-password" && r.Method == http.MethodPost:
		// 重置本机器的「访问码口令」（分享凭据）。管理端与持有该机器的子用户
		// 都可重置；不触及任何账号密码。
		rotateContainerAccessCodePassword(w, r, c)
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
	case action == "resize" && r.Method == http.MethodPost:
		if !requireScope(w, r, "container:resize") {
			return
		}
		if routeToAgent("resize", r.Body) {
			return
		}
		handleContainerResize(w, r, id, c)
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
		saveErr := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
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
		if saveErr != nil {
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
		config.MutateGlobalLogged(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.Containers {
				if cfg.Containers[i].ID != id {
					continue
				}
				oldNodeID = cfg.Containers[i].NodeID
				cfg.Containers[i].NodeID = targetNodeID
				break
			}
		})
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
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshots/delete", nodeLocalID(c))
				b, _ := json.Marshal(map[string]interface{}{"snapshot_id": sid})
				agentBodyStr = string(b)
			case strings.HasSuffix(action, "/restore"):
				sid := strings.TrimSuffix(strings.TrimPrefix(action, "snapshots/"), "/restore")
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshots/restore", nodeLocalID(c))
				b, _ := json.Marshal(map[string]interface{}{"snapshot_id": sid})
				agentBodyStr = string(b)
			default:
				bodyBytes, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				agentPath = fmt.Sprintf("/api/agent/containers/%d/snapshot", nodeLocalID(c))
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
	// === 主流面板风格客户端容器自服务端点（v1.9.x） ===
	case action == "stats" && r.Method == http.MethodGet:
		// 一站式监控：CPU/RAM/Disk/Inodes/Uptime（主流面板 act=monitor）。
		handleContainerStats(w, r, c)
	case action == "bandwidth" && r.Method == http.MethodGet:
		// 流量明细（主流面板 act=bandwidth）。
		handleContainerBandwidth(w, r, c)
	case action == "processes" && r.Method == http.MethodGet:
		// 进程列表（主流面板 act=processes）。
		handleContainerProcesses(w, r, c)
	case action == "processes/kill" && r.Method == http.MethodPost:
		// 批量终止进程（主流面板 act=processes + sel_proc[]）。
		handleContainerProcessKill(w, r, c)
	case action == "services" && r.Method == http.MethodGet:
		// 服务列表（主流面板 act=services）。
		handleContainerServices(w, r, c)
	case action == "services" && r.Method == http.MethodPost:
		// 服务启停重启（主流面板 act=services + start_x/stop_x）。
		handleContainerServiceAction(w, r, c)
	case action == "hvm-settings" && r.Method == http.MethodGet:
		// KVM HVM 设置读取（主流面板 act=hvmsettings）。
		handleContainerHVMSettingsGet(w, r, c)
	case action == "hvm-settings" && r.Method == http.MethodPut:
		// KVM HVM 设置写入。
		handleContainerHVMSettingsPut(w, r, c)
	case action == "scheduled-actions" && r.Method == http.MethodGet:
		// 定时任务列表（主流面板 act=self_shutdown）。
		handleScheduledActionsList(w, r, c)
	case action == "scheduled-actions" && r.Method == http.MethodPost:
		// 创建定时任务。
		handleScheduledActionCreate(w, r, c)
	case strings.HasPrefix(action, "scheduled-actions/") && r.Method == http.MethodDelete:
		// 取消定时任务。scheduled-actions/ 子路径在 action 中以 `scheduled-actions/{id}` 形式传入。
		handleScheduledActionDelete(w, r, c, strings.TrimPrefix(action, "scheduled-actions/"))
	case action == "rescue" && r.Method == http.MethodPost:
		// KVM 救援模式进入/退出（主流面板 Rescue Mode）。
		// 请求体：{"enabled": true/false, "iso_id": "..."}
		if !requireScope(w, r, "container:power") {
			return
		}
		handleContainerRescuePost(w, r, c)
	case action == "iso" && r.Method == http.MethodPost:
		// KVM ISO 挂载/卸载（主流面板 Enduser ISO）。
		// 请求体：{"iso_id": "...", "attach": true/false}
		if !requireScope(w, r, "container:power") {
			return
		}
		handleContainerISOActionPost(w, r, c)
	case action == "vnc-ticket" && r.Method == http.MethodPost:
		// KVM VNC 控制台票据（主流面板 VNC Console）。
		// 请求体：{}（不需要参数，票据绑定当前容器）
		// 权限：terminal:vnc（和顶层 /api/vnc-ticket 一致）。
		if !requireScope(w, r, "terminal:vnc") {
			return
		}
		handleContainerVNCTicketPost(w, r, c)
	case action == "recipes/execute" && r.Method == http.MethodPost:
		// 在当前容器上执行 Recipe（主流面板 Startup Script）。
		// 请求体：{"recipe_id": "recipe-xxx", "timeout": 120}
		// 权限：container:power（执行脚本会改容器内文件）。
		if !requireScope(w, r, "container:power") {
			return
		}
		handleContainerRecipeExecutePost(w, r, c)
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
	// 回收站视图：?recycled=true 只看回收站；默认视图排除回收站实例。
	containers = listContainersFilterRecycled(containers, r)
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
	// 服务端筛选 / 排序（企业级大规模列表：万级容器下不下发全量到浏览器）。
	// 仅处理"纯数据"维度；任务态 / 排队占位由前端叠加。
	filterOptions := buildContainerFilterOptions(containers)
	containers = filterContainersByQuery(containers, r)
	sortContainersByQuery(containers, r)

	for i := range containers {
		sanitizeContainerResponse(r, &containers[i])
		// 列表为只读汇总视图，一律不回显登录口令（detail/console 需要时单独拉取）。
		containers[i].SSHPassword = ""
	}
	// 属主名派生填充：前端据此显示属主徽标/下拉，无需拉取全量子用户（万级可用性）。
	if ownerNames := config.SubUserUsernameByID(); len(ownerNames) > 0 {
		for i := range containers {
			if containers[i].OwnerSubUserID != "" {
				containers[i].OwnerUsername = ownerNames[containers[i].OwnerSubUserID]
			}
		}
	}
	// 分页：未传 page/page_size 时保持全量数组返回（向后兼容）。
	p := parsePagination(r)
	if p.Invalid {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST",
			"page must be >= 1 and page_size within [1, 200]")
		return
	}
	if p.Requested {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"items":          paginate(containers, p),
			"total":          len(containers),
			"page":           p.Page,
			"page_size":      p.PageSize,
			"filter_options": filterOptions,
		}})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: containers})
}

// buildContainerFilterOptions 汇总筛选下拉的可选项（systems / tenants）。
// 取自"筛选前"的全量集合，保证翻页 / 筛选过程中下拉选项不抖动。
func buildContainerFilterOptions(containers []config.Container) map[string]interface{} {
	systemLabels := map[string]string{
		"ubuntu": "Ubuntu", "debian": "Debian", "alpine": "Alpine",
		"centos": "CentOS", "archlinux": "Arch Linux", "fedora": "Fedora",
		"rockylinux": "Rocky Linux", "windows": "Windows", "unknown": "未知系统",
	}
	seen := map[string]bool{}
	systems := make([]map[string]string, 0, 8)
	seenTenants := map[string]bool{}
	tenants := make([]string, 0, 8)
	for _, c := range containers {
		if g := containerSystemGroup(c.Template); !seen[g] {
			seen[g] = true
			label := systemLabels[g]
			if label == "" {
				label = g
			}
			systems = append(systems, map[string]string{"value": g, "label": label})
		}
		if t := strings.TrimSpace(c.Tenant); t != "" && !seenTenants[t] {
			seenTenants[t] = true
			tenants = append(tenants, t)
		}
	}
	sortSliceStable(systems, func(a, b map[string]string) bool { return a["label"] < b["label"] })
	sortSliceStable(tenants, func(a, b string) bool { return a < b })
	return map[string]interface{}{"systems": systems, "tenants": tenants}
}

// containerSystemGroup 由模板 ID 推导系统分组（与前端 getSystemFilterValue 对齐）。
func containerSystemGroup(template string) string {
	normalized := strings.TrimPrefix(template, "kvm-")
	switch {
	case strings.HasPrefix(normalized, "ubuntu"):
		return "ubuntu"
	case strings.HasPrefix(normalized, "debian"):
		return "debian"
	case strings.HasPrefix(normalized, "alpine"):
		return "alpine"
	case strings.HasPrefix(normalized, "centos"):
		return "centos"
	case strings.HasPrefix(normalized, "archlinux"):
		return "archlinux"
	case strings.HasPrefix(normalized, "fedora"):
		return "fedora"
	case strings.HasPrefix(normalized, "rockylinux"):
		return "rockylinux"
	case strings.HasPrefix(normalized, "windows"):
		return "windows"
	default:
		if normalized == "" {
			return "unknown"
		}
		return normalized
	}
}

// filterContainersByQuery 应用 search / type / system / status / tenant / owner / node 筛选。
// 前缀为 "search=" 的关键字检索覆盖 名称 / ID / UUID / IP / IPv6 / 模板 / 租户 / 端口 / 备注。
func filterContainersByQuery(containers []config.Container, r *http.Request) []config.Container {
	q := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	typ := strings.ToLower(strings.TrimSpace(q.Get("type")))
	system := strings.ToLower(strings.TrimSpace(q.Get("system")))
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	tenant := strings.TrimSpace(q.Get("tenant"))
	owner := strings.TrimSpace(q.Get("owner"))
	node := strings.TrimSpace(q.Get("node"))

	if search == "" && (typ == "" || typ == "all") && (system == "" || system == "all") &&
		(status == "" || status == "all") && (tenant == "" || tenant == "all") &&
		(owner == "" || owner == "all") && (node == "" || node == "all") {
		return containers
	}

	filtered := containers[:0]
	for _, c := range containers {
		if typ != "" && typ != "all" && strings.ToLower(c.Runtime()) != typ {
			continue
		}
		if system != "" && system != "all" && containerSystemGroup(c.Template) != system {
			continue
		}
		if status != "" && status != "all" && !strings.Contains(","+status+",", ","+strings.ToLower(c.Status)+",") {
			continue
		}
		if tenant != "" && tenant != "all" && c.Tenant != tenant {
			continue
		}
		if owner != "" && owner != "all" {
			if owner == "__none__" {
				if c.OwnerSubUserID != "" {
					continue
				}
			} else if c.OwnerSubUserID != owner {
				continue
			}
		}
		if node != "" && node != "all" {
			if node == "local" {
				if c.NodeID != "" {
					continue
				}
			} else if c.NodeID != node {
				continue
			}
		}
		if search != "" && !containerMatchesSearch(c, search) {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}

func containerMatchesSearch(c config.Container, keyword string) bool {
	fields := []string{
		strconv.Itoa(c.ID), c.Name, c.UUID, c.IP, c.IPv6, c.Template,
		c.Tenant, c.Remark, strconv.Itoa(c.SSHPort),
	}
	for _, f := range fields {
		if f != "" && strings.Contains(strings.ToLower(f), keyword) {
			return true
		}
	}
	return false
}

// sortContainersByQuery 支持 sort=id|name|status|vcpu|ram_mb|disk_gb|node_id|created_at，
// order=asc|desc。未传 sort 时保持自然顺序（不改动）。
func sortContainersByQuery(containers []config.Container, r *http.Request) {
	key := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	if key == "" {
		return
	}
	desc := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("order")), "desc")
	sortSliceStable(containers, func(a, b config.Container) bool {
		var less bool
		switch key {
		case "name", "hostname":
			less = a.Name < b.Name
		case "status":
			less = a.Status < b.Status
		case "vcpu":
			less = a.VCPU < b.VCPU
		case "ram_mb", "memory":
			less = a.RAMMB < b.RAMMB
		case "disk_gb", "disk":
			less = a.DiskGB < b.DiskGB
		case "node_id", "node":
			less = a.NodeID < b.NodeID
		case "created_at":
			less = a.CreatedAt < b.CreatedAt
		default:
			less = a.ID < b.ID
		}
		if desc {
			return !less
		}
		return less
	})
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

	// KVM 能力前置校验（与 v2 的 createInstance 保持一致）。
	//
	// 必须放在模板校验**之前**：本机没有 /dev/kvm 时，模板校验会先报
	// "Template is not enabled or downloaded"——理由是错的，用户会以为是镜像
	// 没下载而去反复折腾。真实原因是宿主缺少 KVM 能力。
	//
	// 同时放在节点分流之前不影响正确性：指定了节点的请求会在下面更早地
	// 被转发出去（节点自身可能有 KVM）。
	if cfg.Virtualization == config.VirtualizationKVM && !hostKVMAvailable() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "本机不支持 KVM（缺少 /dev/kvm 或 virsh）；如需 KVM 实例，请在产品配置中指定支持 KVM 的节点",
		})
		return
	}

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
	// F8：内存累计配额检查（与磁盘同口径）
	if err := validateCumulativeRAMQuota(cfg.RAMMB); err != nil {
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

	// 指定节点：把创建请求整体代理到目标节点，与本面板 v2 的 target_node_id 行为一致。
	//
	// 为什么在这里（而不是在 lxc.CreateContainer 里）：本机创建流程会先做 NAT 端口
	// 预留、LXC 网络自愈等本机动作，这些对"建在别的节点上"的容器既不适用、
	// 还会无谓占用本机资源。所以在进入本机创建路径前就分流出去。
	//
	// 兼容性：不传 target_node_id 时行为完全不变（本机创建）。
	if raw, ok := fields["target_node_id"]; ok {
		var targetNodeID string
		_ = json.Unmarshal(raw, &targetNodeID)
		targetNodeID = strings.TrimSpace(targetNodeID)
		if targetNodeID != "" && !strings.EqualFold(targetNodeID, "local") {
			node, found := config.FindNode(targetNodeID)
			if !found {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "节点不存在: " + targetNodeID})
				return
			}
			payload, err := json.Marshal(cfg)
			if err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "序列化创建参数失败"})
				return
			}
			data, status, err := proxyNodeRequestWithTimeout(r, node, http.MethodPost, "/api/agent/containers/create", strings.NewReader(string(payload)), NodeCreateTimeout)
			if err != nil {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点创建实例失败：" + err.Error()})
				return
			}
			if status >= 300 {
				jsonResponse(w, status, APIResponse{Success: false, Message: strings.TrimSpace(string(data))})
				return
			}
			// 原样透传被控返回体（与 v2 的节点创建路径保持一致）。
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(data)
			return
		}
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
	// 机器级访问码凭据按需生成并落库，保证详情页/「管理链接」弹窗可回显。
	ensureContainerAccessCredentials(c)
	res := *c
	// 节点容器：主控只保存归属与心跳摘要，详情页需要的系统模板 / SSH 密码 /
	// 创建时间等字段按需向被控拉取（节点 token 鉴权通道；凭据不在主控落库）。
	if res.NodeID != "" {
		enrichNodeContainerDetail(r, &res)
	}
	sanitizeContainerResponse(r, &res)
	accessHost, accessPort, accessVia := containerAccessEndpoint(r, c)
	payload, _ := json.Marshal(res)
	data := map[string]interface{}{}
	if err := json.Unmarshal(payload, &data); err != nil {
		// 极端情况下退回结构化响应（不影响既有字段）。
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: res})
		return
	}
	data["access_host"] = accessHost
	data["access_ssh_port"] = accessPort
	data["access_via"] = accessVia
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: data})
}

// enrichNodeContainerDetail 用被控的完整记录补齐主控侧缺失字段。
// 失败时静默保留主控已有数据（详情页降级显示，不阻断）。
func enrichNodeContainerDetail(r *http.Request, c *config.Container) {
	node, ok := config.FindNode(c.NodeID)
	if !ok || node.Address == "" {
		return
	}
	data, status, err := proxyNodeRequest(r, node, http.MethodGet, "/api/agent/containers", nil)
	if err != nil || status >= 300 {
		return
	}
	var payload struct {
		Data []config.Container `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	for _, remote := range payload.Data {
		if remote.UUID != c.UUID {
			continue
		}
		// 仅补齐展示/操作用字段；归属（NodeID）等主控权威字段不覆盖。
		if c.Template == "" {
			c.Template = remote.Template
		}
		if c.CreatedAt == "" {
			c.CreatedAt = remote.CreatedAt
		}
		if c.SSHPassword == "" {
			c.SSHPassword = remote.SSHPassword
		}
		if c.SSHHostKey == "" {
			c.SSHHostKey = remote.SSHHostKey
		}
		if remote.IP != "" {
			c.IP = remote.IP
		}
		if remote.SSHPort > 0 {
			c.SSHPort = remote.SSHPort
		}
		if remote.Remark != "" && c.Remark == "" {
			c.Remark = remote.Remark
		}
		return
	}
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
					fmt.Sprintf("/api/agent/containers/%d/resize", nodeLocalID(c)), strings.NewReader(string(agentBody)))
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
	// F8：内存调增时补累计配额检查（按增量计——容器自身存量已计入总和，
	// 扩容路径只需校验新增部分；缩容不产生新增占用，无需校验）。
	if nextRAMMB > c.RAMMB {
		if err := validateCumulativeRAMQuota(nextRAMMB - c.RAMMB); err != nil {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
			return
		}
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
	// 排除回收站实例（软删除不计入运营指标）。
	containers = listContainersFilterRecycled(containers, r)
	running := 0
	stopped := 0
	suspended := 0
	nodesTotal := 0
	nodesOnline := 0
	config.AppConfigMu.RLock()
	nodesTotal = len(config.AppConfig.Nodes)
	for _, n := range config.AppConfig.Nodes {
		if n.Status == "online" {
			nodesOnline++
		}
	}
	config.AppConfigMu.RUnlock()
	for _, c := range containers {
		if c.Suspended {
			suspended++
		}
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
		"suspended":        suspended,
		"nodes_total":      nodesTotal,
		"nodes_online":     nodesOnline,
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

// changeContainerHostname 实时修改运行中容器的 hostname（类比 主流面板 Change Hostname）。
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

// changeContainerVNCPassword 修改 KVM VM 的 VNC 密码（类比 主流面板 Change VNC Password）。
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

// cloneContainer 克隆容器（类比 主流面板 Clone VPS）。
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
	// 新 MAC（前缀 02:00:00 保留给虚拟化）。
	// 审计 H-8：改用 crypto/rand 生成，避免 math/rand 可预测（同宿主多租户下
	// 可预测的 MAC 便于伪造/碰撞，尤其在按 MAC 做 DHCP/IPv6 绑定的场景）。
	newMAC := randomVirtualMAC()
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
			fmt.Sprintf("/api/agent/containers/%d/clone", nodeLocalID(src)), strings.NewReader(string(agentBody)))
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
	config.MutateGlobalLogged(func(cfg *config.EyvescloudConfig) {
		cfg.Containers = append(cfg.Containers, newContainer)
	})

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

// handleContainerResize 处理容器规格变更（CPU/RAM/Disk）。
// LXC 支持在线/离线调整 CPU 和 RAM；Disk 扩容可在线（loopback rootfs），
// 缩容需要停机。KVM 的 CPU/RAM 调整需要停机后通过 virsh setvcpus/setmem
// 修改 domain XML，再 start；Disk 通过 qemu-img resize。
func handleContainerResize(w http.ResponseWriter, r *http.Request, id int, c *config.Container) {
	var req struct {
		VCPU   int     `json:"vcpu"`
		RAMMB  int     `json:"ram_mb"`
		DiskGB float64 `json:"disk_gb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if req.VCPU <= 0 && req.RAMMB <= 0 && req.DiskGB <= 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "At least one of vcpu, ram_mb, disk_gb must be > 0"})
		return
	}
	if req.VCPU < 0 || req.RAMMB < 0 || req.DiskGB < 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Resource values must be non-negative"})
		return
	}
	if req.VCPU > 0 && req.VCPU > 256 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "vcpu exceeds maximum allowed (256)"})
		return
	}
	if req.RAMMB > 0 && req.RAMMB > 1024*1024 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "ram_mb exceeds maximum allowed (1TB)"})
		return
	}
	if req.DiskGB > 0 && req.DiskGB > 10240 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "disk_gb exceeds maximum allowed (10TB)"})
		return
	}

	c = config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}

	var err error
	if c.IsKVM() {
		err = kvmManager.ResizeContainer(id, req.VCPU, req.RAMMB, req.DiskGB)
	} else {
		err = lxcManager.ResizeContainer(id, req.VCPU, req.RAMMB, req.DiskGB)
	}
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	// 更新 config 中的规格（runtime 层已更新，这里同步持久化）
	config.MutateGlobalLogged(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].ID != id {
				continue
			}
			if req.VCPU > 0 {
				cfg.Containers[i].VCPU = float64(req.VCPU)
			}
			if req.RAMMB > 0 {
				cfg.Containers[i].RAMMB = req.RAMMB
			}
			if req.DiskGB > 0 {
				cfg.Containers[i].DiskGB = req.DiskGB
			}
			break
		}
	})

	detail := fmt.Sprintf("vcpu=%d ram_mb=%d disk_gb=%.1f", req.VCPU, req.RAMMB, req.DiskGB)
	auditRequest(r, "container.resize", c.Name, detail, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Container resized successfully",
		Data: map[string]interface{}{
			"vcpu":    c.VCPU,
			"ram_mb":  c.RAMMB,
			"disk_gb": c.DiskGB,
		},
	})
}

// ---------------------------------------------------------------------------
// 回收站（软删除，同类商业面板/同类商业面板同款能力）
// ---------------------------------------------------------------------------

// listContainers 增加回收站视图：?recycled=true 只返回回收站实例。
// 默认视图（不传参）排除回收站实例——所有列表调用方（UI/WHMCS/集成）自动获得
// "回收站不可见"语义；按 ID/名称的定位（find）不受影响，计费挂起/删除仍可命中。
func listContainersFilterRecycled(containers []config.Container, r *http.Request) []config.Container {
	wantRecycled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("recycled")), "true")
	filtered := containers[:0]
	for _, c := range containers {
		if wantRecycled == (c.RecycledAt != "") {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// restoreContainerAction POST /api/containers/{id}/restore：从回收站恢复。
func restoreContainerAction(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if !requireScope(w, r, "container:create") {
		return
	}
	if c.RecycledAt == "" {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Container is not in recycle bin"})
		return
	}
	if err := config.RestoreContainer(c.ID); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "container.restore", c.Name, "从回收站恢复", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Container restored", Data: map[string]interface{}{
		"id": c.ID, "name": c.Name,
	}})
}

// purgeContainerAction POST /api/containers/{id}/purge：彻底删除（真销毁数据面）。
// 这是回收站模型里唯一的真删除入口；WHMCS Terminate 与到期清理走这里。
func purgeContainerAction(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if !requireScope(w, r, "container:delete") {
		return
	}
	if c.Locked {
		jsonResponse(w, http.StatusPreconditionFailed, APIResponse{Success: false, Message: "Container is locked"})
		return
	}
	// 节点实例：真删除由被控 destroy 执行。
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "实例所属节点不可用"})
			return
		}
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/destroy", nodeLocalID(c)), nil)
		if err != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理节点失败：" + err.Error()})
			return
		}
		if status >= 300 {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点执行失败：" + strings.TrimSpace(string(data))})
			return
		}
		// 被控已确认销毁：主控侧记录立即清除（否则列表残留到重启）。
		removeNodeContainerRecord(c.UUID)
		auditRequest(r, "container.purge", c.Name, "彻底删除（节点 "+node.Name+"）", true, "")
		jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "Purge accepted", Data: map[string]interface{}{
			"id": c.ID, "name": c.Name, "node": node.Name,
		}})
		return
	}
	taskIDs := globalQueue.EnqueueBatchWithAudit(TaskDelete, []int{c.ID}, "", requestActor(r), clientIP(r), r.UserAgent())
	auditRequest(r, "container.purge", c.Name, "彻底删除", true, "")
	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "Purge accepted", Data: map[string]interface{}{
		"id": c.ID, "name": c.Name, "task_ids": taskIDs,
	}})
}

// HandleRecycleBin GET /api/recycle-bin：回收站列表（管理员与按容器绑定过滤）。
func HandleRecycleBin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	items := config.RecycledContainers()
	items = filterContainersForRequest(r, items)
	for i := range items {
		sanitizeContainerResponse(r, &items[i])
		items[i].SSHPassword = ""
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: items})
}

// StartRecyclePurgeWorker 启动回收站自动清理：每小时检查一次，超过保留期
// （EYVESCLOUD_RECYCLE_DAYS，默认 7 天）的回收站实例入真删除任务。
func StartRecyclePurgeWorker() {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		purgeOnce := func() {
			retention := config.RecycleRetentionDays
			if v := strings.TrimSpace(os.Getenv("EYVESCLOUD_RECYCLE_DAYS")); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					retention = n
				}
			}
			for _, id := range config.RecyclePurgeDue(retention) {
				if c := config.FindContainer(id); c != nil && !c.Locked {
					globalQueue.EnqueueBatchWithAudit(TaskDelete, []int{id}, "", "system:recycle-purge", "", "")
				}
			}
		}
		purgeOnce()
		for range ticker.C {
			purgeOnce()
		}
	}()
}
