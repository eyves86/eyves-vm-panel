package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

type scheduledActionsResponse struct {
	Actions []config.ScheduledAction `json:"actions"`
	Total   int                      `json:"total"`
}

type scheduledActionRequest struct {
	Type      string `json:"type"`      // start/stop/restart/poweroff
	ExecuteAt string `json:"execute_at"` // RFC3339
	Repeat    string `json:"repeat"`    // none/daily/weekly/monthly
}

// handleScheduledActionsList 列出定时任务（GET /api/containers/{id}/scheduled-actions）。
// 对齐主流面板语义。
//
// 权限：container:read scope。
func handleScheduledActionsList(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "scheduled-actions") {
		return
	}
	actions := config.ListScheduledActions(c.ID)
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    scheduledActionsResponse{Actions: actions, Total: len(actions)},
	})
}

// handleScheduledActionCreate 创建定时任务（POST /api/containers/{id}/scheduled-actions）。
// 对齐主流面板语义 + selfshutdown=1。
//
// 权限：container:power scope。
//
// 安全：
//   - type 白名单 [start stop restart poweroff]
//   - execute_at 必须是 RFC3339 且在未来 5 年内
//   - repeat 白名单 [none daily weekly monthly]
//   - 单容器最多 10 个定时任务
func handleScheduledActionCreate(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:power") {
		return
	}
	if routeToAgent(w, r, c, "scheduled-actions") {
		return
	}
	var req scheduledActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	switch req.Type {
	case "start", "stop", "restart", "poweroff":
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "type 必须为 start/stop/restart/poweroff",
		})
		return
	}
	switch req.Repeat {
	case "", "none", "daily", "weekly", "monthly":
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "repeat 必须为 none/daily/weekly/monthly",
		})
		return
	}
	if req.Repeat == "" {
		req.Repeat = "none"
	}
	t, err := time.Parse(time.RFC3339, req.ExecuteAt)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "execute_at 必须是 RFC3339 格式: " + err.Error(),
		})
		return
	}
	if t.Before(time.Now()) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "execute_at 必须在未来",
		})
		return
	}
	if t.After(time.Now().Add(5 * 365 * 24 * time.Hour)) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "execute_at 距今不得超过 5 年",
		})
		return
	}
	existing := config.ListScheduledActions(c.ID)
	if len(existing) >= 10 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "单容器最多 10 个定时任务",
		})
		return
	}
	actor := scheduledActionCreatedBy(r)
	id := fmt.Sprintf("sca-%d-%d", c.ID, time.Now().UnixNano())
	action := config.ScheduledAction{
		ID:            id,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Type:          req.Type,
		Repeat:        req.Repeat,
		ExecuteAt:     t.UTC().Format(time.RFC3339),
		Enabled:       true,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		CreatedBy:     actor,
	}
	if err := config.SaveScheduledAction(action); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{
			Success: false, Message: "保存失败: " + err.Error(),
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "已创建定时任务 " + id,
		Data:    action,
	})
	auditRequest(r, "container.scheduled_action.create", strconv.Itoa(c.ID),
		id+" type="+req.Type+" repeat="+req.Repeat, true, "")
}

// handleScheduledActionDelete 删除定时任务（DELETE /api/containers/{id}/scheduled-actions/{actionID}）。
//
// 权限：container:power scope。
func handleScheduledActionDelete(w http.ResponseWriter, r *http.Request, c *config.Container, actionID string) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:power") {
		return
	}
	if routeToAgent(w, r, c, "scheduled-actions/delete") {
		return
	}
	if !strings.HasPrefix(actionID, "sca-") {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "actionID 格式错误",
		})
		return
	}
	if err := config.DeleteScheduledAction(c.ID, actionID); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{
			Success: false, Message: "删除失败: " + err.Error(),
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "已删除 " + actionID,
	})
	auditRequest(r, "container.scheduled_action.delete", strconv.Itoa(c.ID),
		actionID, true, "")
}

func scheduledActionCreatedBy(r *http.Request) string {
	if ctx, ok := authContextFromRequest(r); ok {
		return ctx.Actor
	}
	return ""
}