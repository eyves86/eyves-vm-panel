package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

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
			Name        string `json:"name"`
			Description string `json:"description"`
			RegionID    string `json:"region_id"`
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
		if err := config.AddNodeGroup(config.NodeGroup{
			Name:        name,
			Description: strings.TrimSpace(req.Description),
			RegionID:    strings.TrimSpace(req.RegionID),
		}); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.SaveConfig()
		auditRequest(r, "node_group.create", name, "", true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true})
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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
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
		config.SaveConfig()
		auditRequest(r, "node_group.update", id, "", true, "")
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
		if err := config.AddCluster(config.Cluster{
			Name:        name,
			Description: strings.TrimSpace(req.Description),
			RegionIDs:   req.RegionIDs,
		}); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.SaveConfig()
		auditRequest(r, "cluster.create", name, "", true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true})
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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
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
		config.SaveConfig()
		auditRequest(r, "cluster.update", id, "", true, "")
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
