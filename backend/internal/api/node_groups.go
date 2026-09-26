package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// validateNodeIDs 校验一组节点 ID 均存在（成员绑定的前置校验）。
func validateNodeIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	existing := make(map[string]bool, len(config.AppConfig.Nodes))
	for _, n := range config.AppConfig.Nodes {
		existing[n.ID] = true
	}
	for _, id := range ids {
		if !existing[id] {
			return fmt.Errorf("Node not found: %s", id)
		}
	}
	return nil
}

// setNodeGroupMembers 整体替换分组成员：写入新集合中节点的 NodeGroupID，
// 并清掉不在新集合中但原先属于该组的节点引用（NodeGroupID 单值，天然互斥）。
func setNodeGroupMembers(groupID string, nodeIDs []string) {
	member := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		member[id] = true
	}
	_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Nodes {
			if member[cfg.Nodes[i].ID] {
				cfg.Nodes[i].NodeGroupID = groupID
			} else if cfg.Nodes[i].NodeGroupID == groupID {
				cfg.Nodes[i].NodeGroupID = ""
			}
		}
	})
}

// setClusterMembers 整体替换集群成员（写 Node.ClusterID），语义同上。
func setClusterMembers(clusterID string, nodeIDs []string) {
	member := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		member[id] = true
	}
	_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Nodes {
			if member[cfg.Nodes[i].ID] {
				cfg.Nodes[i].ClusterID = clusterID
			} else if cfg.Nodes[i].ClusterID == clusterID {
				cfg.Nodes[i].ClusterID = ""
			}
		}
	})
}

// HandleNodeGroups 处理 /api/v1/node-groups 的列表与创建。
func HandleNodeGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		config.AppConfigMu.RLock()
		groups := append([]config.NodeGroup(nil), config.AppConfig.NodeGroups...)
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: groups})
	case http.MethodPost:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			RegionID    string   `json:"region_id"`
			// NodeIDs 可选：创建分组后立即绑定这些成员节点（整体写入 NodeGroupID）。
			NodeIDs []string `json:"node_ids,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name is required"})
			return
		}
		if err := validateNodeIDs(req.NodeIDs); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		created, err := config.AddNodeGroup(config.NodeGroup{
			Name:        name,
			Description: strings.TrimSpace(req.Description),
			RegionID:    strings.TrimSpace(req.RegionID),
		})
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if len(req.NodeIDs) > 0 {
			setNodeGroupMembers(created.ID, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "node_group.create", name, fmt.Sprintf("id=%s members=%d", created.ID, len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: created})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleNodeGroupItem 处理 /api/v1/node-groups/{id} 的 GET/PUT/DELETE。
func HandleNodeGroupItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		ng, ok := config.FindNodeGroup(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "NodeGroup not found"})
			return
		}
		nodeIDs := config.ListNodeGroupNodes(id)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"group": ng,
			"nodes": nodeIDs,
		}})
	case http.MethodPut:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			RegionID    string `json:"region_id"`
			// NodeIDs 非 nil 时整体替换成员集合：传数组（含空数组）即生效，
			// 不传则保持现有成员不变。
			NodeIDs []string `json:"node_ids,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.NodeIDs != nil {
			if err := validateNodeIDs(req.NodeIDs); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
		_, ok := config.UpdateNodeGroup(id, func(ng *config.NodeGroup) {
			if v := strings.TrimSpace(req.Name); v != "" {
				ng.Name = v
			}
			ng.Description = strings.TrimSpace(req.Description)
			ng.RegionID = strings.TrimSpace(req.RegionID)
		})
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "NodeGroup not found"})
			return
		}
		if req.NodeIDs != nil {
			setNodeGroupMembers(id, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "node_group.update", id, fmt.Sprintf("members=%d", len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	case http.MethodDelete:
		if !requireScope(w, r, "node:write") {
			return
		}
		ng, ok := config.FindNodeGroup(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "NodeGroup not found"})
			return
		}
		if config.RemoveNodeGroup(id) {
			config.SaveConfig()
			auditRequest(r, "node_group.delete", ng.Name, "", true, "")
			jsonResponse(w, http.StatusOK, APIResponse{Success: true})
			return
		}
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "NodeGroup not found"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleClusters 处理 /api/v1/clusters 的列表与创建。
func HandleClusters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		config.AppConfigMu.RLock()
		clusters := append([]config.Cluster(nil), config.AppConfig.Clusters...)
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: clusters})
	case http.MethodPost:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			RegionIDs   []string `json:"region_ids"`
			// NodeIDs 可选：创建集群后立即绑定这些成员节点（整体写入 ClusterID）。
			NodeIDs []string `json:"node_ids,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name is required"})
			return
		}
		if err := validateNodeIDs(req.NodeIDs); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		created, err := config.AddCluster(config.Cluster{
			Name:        name,
			Description: strings.TrimSpace(req.Description),
			RegionIDs:   req.RegionIDs,
		})
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if len(req.NodeIDs) > 0 {
			setClusterMembers(created.ID, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "cluster.create", name, fmt.Sprintf("id=%s members=%d", created.ID, len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: created})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleClusterItem 处理 /api/v1/clusters/{id} 的 GET/PUT/DELETE。
func HandleClusterItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		cl, ok := config.FindCluster(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cluster not found"})
			return
		}
		nodeIDs := config.ListClusterNodes(id)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"cluster": cl,
			"nodes":   nodeIDs,
		}})
	case http.MethodPut:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			RegionIDs   []string `json:"region_ids"`
			// NodeIDs 非 nil 时整体替换成员集合（写 Node.ClusterID）；
			// 不传则保持现有成员不变。
			NodeIDs []string `json:"node_ids,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.NodeIDs != nil {
			if err := validateNodeIDs(req.NodeIDs); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
		_, ok := config.UpdateCluster(id, func(cl *config.Cluster) {
			if v := strings.TrimSpace(req.Name); v != "" {
				cl.Name = v
			}
			cl.Description = strings.TrimSpace(req.Description)
			cl.RegionIDs = req.RegionIDs
		})
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cluster not found"})
			return
		}
		if req.NodeIDs != nil {
			setClusterMembers(id, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "cluster.update", id, fmt.Sprintf("members=%d", len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	case http.MethodDelete:
		if !requireScope(w, r, "node:write") {
			return
		}
		cl, ok := config.FindCluster(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cluster not found"})
			return
		}
		if config.RemoveCluster(id) {
			config.SaveConfig()
			auditRequest(r, "cluster.delete", cl.Name, "", true, "")
			jsonResponse(w, http.StatusOK, APIResponse{Success: true})
			return
		}
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cluster not found"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}
