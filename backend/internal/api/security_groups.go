package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/secgroup"
)

// HandleSecGroups 处理 /api/security-groups（列表 + 创建）。
func HandleSecGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listSecGroups(w, r)
	case http.MethodPost:
		createSecGroup(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleSecGroupItem 处理 /api/security-groups/{id}（获取 + 更新 + 删除）。
func HandleSecGroupItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/security-groups/")
	if rest == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "security group id required"})
		return
	}
	id := strings.SplitN(rest, "/", 2)[0]
	switch {
	case r.Method == http.MethodGet:
		getSecGroup(w, r, id)
	case r.Method == http.MethodPut:
		updateSecGroup(w, r, id)
	case r.Method == http.MethodDelete:
		deleteSecGroup(w, r, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleSecGroupRules 处理 /api/security-groups/{id}/rules（规则列表 + 添加）。
func HandleSecGroupRules(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/security-groups/")
	parts := strings.SplitN(path, "/", 3) // [id, "rules", ruleId?]
	if len(parts) < 1 || parts[0] == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "security group id required"})
		return
	}
	groupID := parts[0]
	if len(parts) >= 3 && parts[2] != "" {
		// /api/security-groups/{id}/rules/{ruleId}
		ruleID := strings.SplitN(parts[2], "/", 2)[0]
		handleSecGroupRuleItem(w, r, groupID, ruleID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		listSecGroupRules(w, r, groupID)
	case http.MethodPost:
		createSecGroupRule(w, r, groupID)
	case http.MethodPut:
		replaceAllSecGroupRules(w, r, groupID)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleContainerSecGroups 处理容器与安全组的绑定：
// GET  /api/containers/{id}/security-groups       → 返回绑定列表
// PUT  /api/containers/{id}/security-groups       → 设置绑定（替换）
// POST /api/containers/{id}/security-groups/{gid} → 绑定
// DELETE /api/containers/{id}/security-groups/{gid} → 解绑
func HandleContainerSecGroups(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/containers/")
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 3 || parts[1] != "security-groups" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "not found"})
		return
	}
	id := 0
	fmt.Sscanf(parts[0], "%d", &id)
	if id == 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container id required"})
		return
	}

	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}

	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied"})
		return
	}

	if len(parts) >= 4 && parts[3] != "" {
		groupID := strings.SplitN(parts[3], "/", 2)[0]
		switch r.Method {
		case http.MethodPost:
			bindSecGroupToContainer(w, r, id, groupID)
		case http.MethodDelete:
			unbindSecGroupFromContainer(w, r, id, groupID)
		default:
			jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"container_id": c.ID,
			"container":    c.Name,
			"sec_group_ids": c.SecGroupIDs,
		}})
	case http.MethodPut:
		setContainerSecGroups(w, r, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// ---- CRUD 实现 ----

func findSecGroup(id string) (secgroup.Group, bool) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, g := range config.AppConfig.SecGroups {
		if g.ID == id {
			return g, true
		}
	}
	return secgroup.Group{}, false
}

func findSecGroupRules(groupID string) []secgroup.Rule {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	out := []secgroup.Rule{}
	for _, r := range config.AppConfig.SecGroupRules {
		if r.GroupID == groupID {
			out = append(out, r)
		}
	}
	return out
}

func genGroupID() string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	mac.Write([]byte(randomHex(8)))
	return "sg-" + hex.EncodeToString(mac.Sum(nil))[:12]
}

func genRuleID() string {
	return "sr-" + randomHex(10)
}

func listSecGroups(w http.ResponseWriter, r *http.Request) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	groups := config.AppConfig.SecGroups
	if groups == nil {
		groups = []secgroup.Group{}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: groups})
}

func getSecGroup(w http.ResponseWriter, r *http.Request, id string) {
	g, ok := findSecGroup(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	rules := findSecGroupRules(id)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"group": g,
		"rules": rules,
	}})
}

func createSecGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		TenantID      string `json:"tenant_id,omitempty"`
		DefaultAction string `json:"default_action,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name is required"})
		return
	}
	if req.DefaultAction == "" {
		req.DefaultAction = string(secgroup.ActionAccept)
	}
	group := secgroup.Group{
		ID:            genGroupID(),
		TenantID:      strings.TrimSpace(req.TenantID),
		Name:          req.Name,
		DefaultAction: secgroup.Action(strings.ToLower(req.DefaultAction)),
	}
	if err := group.Validate(); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.AppConfigMu.Lock()
	config.AppConfig.SecGroups = append(config.AppConfig.SecGroups, group)
	config.SaveConfig()
	config.AppConfigMu.Unlock()
	auditRequest(r, "secgroup.create", group.Name, "id="+group.ID, true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: group})
}

func updateSecGroup(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name          *string `json:"name,omitempty"`
		DefaultAction *string `json:"default_action,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	_, ok := findSecGroup(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SecGroups {
			if cfg.SecGroups[i].ID == id {
				if req.Name != nil {
					cfg.SecGroups[i].Name = strings.TrimSpace(*req.Name)
				}
				if req.DefaultAction != nil {
					cfg.SecGroups[i].DefaultAction = secgroup.Action(strings.ToLower(strings.TrimSpace(*req.DefaultAction)))
				}
				break
			}
		}
	})
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "secgroup.update", id, "", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func deleteSecGroup(w http.ResponseWriter, r *http.Request, id string) {
	g, ok := findSecGroup(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	// 检查是否有容器绑定
	config.AppConfigMu.RLock()
	bound := []string{}
	for _, c := range config.AppConfig.Containers {
		for _, gid := range c.SecGroupIDs {
			if gid == id {
				bound = append(bound, c.Name)
				break
			}
		}
	}
	config.AppConfigMu.RUnlock()
	if len(bound) > 0 {
		jsonResponse(w, http.StatusConflict, APIResponse{
			Success: false,
			Message: fmt.Sprintf("Security group is bound to containers: %s", strings.Join(bound, ", ")),
		})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		newGroups := make([]secgroup.Group, 0, len(cfg.SecGroups))
		for _, g := range cfg.SecGroups {
			if g.ID != id {
				newGroups = append(newGroups, g)
			}
		}
		cfg.SecGroups = newGroups
		newRules := make([]secgroup.Rule, 0, len(cfg.SecGroupRules))
		for _, r := range cfg.SecGroupRules {
			if r.GroupID != id {
				newRules = append(newRules, r)
			}
		}
		cfg.SecGroupRules = newRules
	})
	config.SaveConfig()
	auditRequest(r, "secgroup.delete", g.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func listSecGroupRules(w http.ResponseWriter, r *http.Request, groupID string) {
	if _, ok := findSecGroup(groupID); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	rules := findSecGroupRules(groupID)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: rules})
}

func createSecGroupRule(w http.ResponseWriter, r *http.Request, groupID string) {
	if _, ok := findSecGroup(groupID); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	var req struct {
		Direction  string `json:"direction"`
		Protocol   string `json:"protocol"`
		SrcMask    string `json:"src_mask,omitempty"`
		DstMask    string `json:"dst_mask,omitempty"`
		SrcPort    int    `json:"src_port,omitempty"`
		DstPort    int    `json:"dst_port,omitempty"`
		Action     string `json:"action"`
		Priority   int    `json:"priority"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	rule := secgroup.Rule{
		ID:          genRuleID(),
		GroupID:     groupID,
		Direction:   secgroup.Direction(strings.ToLower(req.Direction)),
		Protocol:    secgroup.Protocol(strings.ToLower(req.Protocol)),
		SrcMask:     strings.TrimSpace(req.SrcMask),
		DstMask:     strings.TrimSpace(req.DstMask),
		SrcPort:     req.SrcPort,
		DstPort:     req.DstPort,
		Action:      secgroup.Action(strings.ToLower(req.Action)),
		Priority:    req.Priority,
		Description: strings.TrimSpace(req.Description),
	}
	if err := rule.Validate(); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.AppConfigMu.Lock()
	config.AppConfig.SecGroupRules = append(config.AppConfig.SecGroupRules, rule)
	config.SaveConfig()
	config.AppConfigMu.Unlock()
	auditRequest(r, "secgroup.rule_create", groupID, "rule_id="+rule.ID, true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: rule})
}

func replaceAllSecGroupRules(w http.ResponseWriter, r *http.Request, groupID string) {
	if _, ok := findSecGroup(groupID); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	var req struct {
		Rules []secgroup.Rule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	for i := range req.Rules {
		req.Rules[i].ID = genRuleID()
		req.Rules[i].GroupID = groupID
		if err := req.Rules[i].Validate(); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: fmt.Sprintf("rule #%d invalid: %s", i, err.Error()),
			})
			return
		}
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		remaining := make([]secgroup.Rule, 0, len(cfg.SecGroupRules))
		for _, r := range cfg.SecGroupRules {
			if r.GroupID != groupID {
				remaining = append(remaining, r)
			}
		}
		cfg.SecGroupRules = append(remaining, req.Rules...)
	})
	config.SaveConfig()
	auditRequest(r, "secgroup.rule_replace", groupID, fmt.Sprintf("count=%d", len(req.Rules)), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: fmt.Sprintf("Replaced %d rules", len(req.Rules))})
}

func handleSecGroupRuleItem(w http.ResponseWriter, r *http.Request, groupID, ruleID string) {
	if _, ok := findSecGroup(groupID); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		// 更新单条规则
		var req struct {
			Direction   *string `json:"direction,omitempty"`
			Protocol    *string `json:"protocol,omitempty"`
			SrcMask     *string `json:"src_mask,omitempty"`
			DstMask     *string `json:"dst_mask,omitempty"`
			SrcPort     *int    `json:"src_port,omitempty"`
			DstPort     *int    `json:"dst_port,omitempty"`
			Action      *string `json:"action,omitempty"`
			Priority    *int    `json:"priority,omitempty"`
			Description *string `json:"description,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SecGroupRules {
				if cfg.SecGroupRules[i].ID == ruleID && cfg.SecGroupRules[i].GroupID == groupID {
					if req.Direction != nil {
						cfg.SecGroupRules[i].Direction = secgroup.Direction(strings.ToLower(*req.Direction))
					}
					if req.Protocol != nil {
						cfg.SecGroupRules[i].Protocol = secgroup.Protocol(strings.ToLower(*req.Protocol))
					}
					if req.SrcMask != nil {
						cfg.SecGroupRules[i].SrcMask = strings.TrimSpace(*req.SrcMask)
					}
					if req.DstMask != nil {
						cfg.SecGroupRules[i].DstMask = strings.TrimSpace(*req.DstMask)
					}
					if req.SrcPort != nil {
						cfg.SecGroupRules[i].SrcPort = *req.SrcPort
					}
					if req.DstPort != nil {
						cfg.SecGroupRules[i].DstPort = *req.DstPort
					}
					if req.Action != nil {
						cfg.SecGroupRules[i].Action = secgroup.Action(strings.ToLower(*req.Action))
					}
					if req.Priority != nil {
						cfg.SecGroupRules[i].Priority = *req.Priority
					}
					if req.Description != nil {
						cfg.SecGroupRules[i].Description = strings.TrimSpace(*req.Description)
					}
					break
				}
			}
		})
		config.SaveConfig()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	case http.MethodDelete:
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			remaining := make([]secgroup.Rule, 0, len(cfg.SecGroupRules))
			for _, r := range cfg.SecGroupRules {
				if !(r.ID == ruleID && r.GroupID == groupID) {
					remaining = append(remaining, r)
				}
			}
			cfg.SecGroupRules = remaining
		})
		config.SaveConfig()
		auditRequest(r, "secgroup.rule_delete", ruleID, "group="+groupID, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// 容器绑定安全组
func bindSecGroupToContainer(w http.ResponseWriter, r *http.Request, containerID int, groupID string) {
	if _, ok := findSecGroup(groupID); !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Security group not found"})
		return
	}
	var added bool
	config.MutateContainerByID(containerID, func(c *config.Container) {
		for _, gid := range c.SecGroupIDs {
			if gid == groupID {
				return
			}
		}
		c.SecGroupIDs = append(c.SecGroupIDs, groupID)
		added = true
	})
	if !added {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Already bound"})
		return
	}
	config.SaveConfig()
	auditRequest(r, "secgroup.bind", groupID, fmt.Sprintf("container_id=%d", containerID), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func unbindSecGroupFromContainer(w http.ResponseWriter, r *http.Request, containerID int, groupID string) {
	var removed bool
	config.MutateContainerByID(containerID, func(c *config.Container) {
		newList := make([]string, 0, len(c.SecGroupIDs))
		for _, gid := range c.SecGroupIDs {
			if gid == groupID {
				removed = true
				continue
			}
			newList = append(newList, gid)
		}
		c.SecGroupIDs = newList
	})
	if !removed {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Not bound"})
		return
	}
	config.SaveConfig()
	auditRequest(r, "secgroup.unbind", groupID, fmt.Sprintf("container_id=%d", containerID), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func setContainerSecGroups(w http.ResponseWriter, r *http.Request, containerID int) {
	var req struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	// 校验所有 group 存在
	for _, gid := range req.GroupIDs {
		if _, ok := findSecGroup(gid); !ok {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Security group not found: " + gid})
			return
		}
	}
	config.MutateContainerByID(containerID, func(c *config.Container) {
		c.SecGroupIDs = append([]string(nil), req.GroupIDs...)
	})
	config.SaveConfig()
	auditRequest(r, "secgroup.bind_set", fmt.Sprintf("%d", containerID),
		fmt.Sprintf("groups=%s", strings.Join(req.GroupIDs, ",")), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}
