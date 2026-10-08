package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// setCellMembers 整体替换 cell 成员：写入新集合中节点的 CellID，并把原先归属该
// cell 但不在新集合中的节点清回默认 cell（CellID 单值，天然互斥）。改 CellID 会
// 触发行级落库把节点（及其容器）在控制库与 cell 库之间物理搬迁（P3-c）。
func setCellMembers(cellID string, nodeIDs []string) {
	member := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		member[id] = true
	}
	_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Nodes {
			nowIn := member[cfg.Nodes[i].ID]
			switch {
			case nowIn:
				cfg.Nodes[i].CellID = cellID
			case cfg.Nodes[i].CellID == cellID:
				cfg.Nodes[i].CellID = ""
			}
		}
	})
}

// HandleCells 处理 /api/cells 的列表与创建。
func HandleCells(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		config.AppConfigMu.RLock()
		cells := append([]config.Cell(nil), config.AppConfig.Cells...)
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: cells})
	case http.MethodPost:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			// NodeIDs 可选：创建后立即绑定这些成员节点（整体写入 CellID）。
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
		created, err := config.AddCell(config.Cell{
			Name:        name,
			Description: strings.TrimSpace(req.Description),
		})
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if len(req.NodeIDs) > 0 {
			setCellMembers(created.ID, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "cell.create", name, fmt.Sprintf("id=%s members=%d", created.ID, len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: created})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleCellItem 处理 /api/cells/{id} 的 GET/PUT/DELETE。
func HandleCellItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		cell, ok := config.FindCell(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cell not found"})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"cell":  cell,
			"nodes": config.ListNodeCellNodes(id),
		}})
	case http.MethodPut:
		if !requireScope(w, r, "node:write") {
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
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
		_, ok := config.UpdateCell(id, func(c *config.Cell) {
			if v := strings.TrimSpace(req.Name); v != "" {
				c.Name = v
			}
			c.Description = strings.TrimSpace(req.Description)
		})
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cell not found"})
			return
		}
		if req.NodeIDs != nil {
			setCellMembers(id, req.NodeIDs)
		}
		config.SaveConfig()
		auditRequest(r, "cell.update", id, fmt.Sprintf("members=%d", len(req.NodeIDs)), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	case http.MethodDelete:
		if !requireScope(w, r, "node:write") {
			return
		}
		cell, ok := config.FindCell(id)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cell not found"})
			return
		}
		// 与分组不同：清成员引用会把节点及其容器从 cell 库物理搬回控制库（批量
		// 迁移），所以有成员时拒绝删除，由管理员先迁空。
		if n := config.CountCellNodes(id); n > 0 {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: fmt.Sprintf("Cell still has %d member nodes; move them out before deleting", n)})
			return
		}
		if config.RemoveCell(id) {
			config.SaveConfig()
			auditRequest(r, "cell.delete", cell.Name, "", true, "")
			jsonResponse(w, http.StatusOK, APIResponse{Success: true})
			return
		}
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Cell not found"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}
