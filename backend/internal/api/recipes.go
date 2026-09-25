package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// HandleRecipes 处理 /api/recipes（列表 + 创建）。
func HandleRecipes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listRecipes(w, r)
	case http.MethodPost:
		createRecipe(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleRecipeItem 处理 /api/recipes/{id}（获取 + 更新 + 删除 + 执行）。
func HandleRecipeItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/recipes/")
	// 处理 /api/recipes/{id}/execute 子路由
	if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 && parts[1] == "execute" {
		executeRecipe(w, r, parts[0])
		return
	}
	if rest == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "recipe id required"})
		return
	}
	id := strings.SplitN(rest, "/", 2)[0]
	switch {
	case r.Method == http.MethodGet:
		getRecipe(w, r, id)
	case r.Method == http.MethodPut:
		updateRecipe(w, r, id)
	case r.Method == http.MethodDelete:
		deleteRecipe(w, r, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// ---- helpers ----

func genRecipeID() string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	mac.Write([]byte(randomHex(8)))
	return "recipe-" + hex.EncodeToString(mac.Sum(nil))[:12]
}

func findRecipe(id string) (config.Recipe, bool) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, r := range config.AppConfig.Recipes {
		if r.ID == id {
			return r, true
		}
	}
	return config.Recipe{}, false
}

// canAccessRecipe 判断当前请求的用户是否可以访问该 recipe。
func canAccessRecipe(r *http.Request, recipe config.Recipe) bool {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		return false
	}
	// admin 可以访问所有
	if ctx.Type == authTypeAdmin || (ctx.Type == authTypeAPIKey && scopeAllowed(ctx.Scopes, "admin:access")) {
		return true
	}
	// subuser 或 subuser 类型 API key
	if ctx.Type == authTypeSubUser || ctx.Actor != "" {
		actor := ctx.Actor
		// 自己的 private
		if recipe.OwnerType == "subuser" && recipe.OwnerID == actor {
			return true
		}
		// admin 创建的 shared
		if recipe.OwnerType == "admin" && recipe.Scope == "shared" {
			return true
		}
	}
	return false
}

// currentUserRecipeScope 返回当前请求用户在 recipe 中的 owner_type 和 owner_id。
func currentUserRecipeScope(r *http.Request) (ownerType, ownerID string) {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		return "", ""
	}
	if ctx.Type == authTypeAdmin {
		return "admin", "admin"
	}
	// API Key 归 admin（除非绑定 subuser，但我们这里简化：API Key 作 admin 处理）
	if ctx.Type == authTypeAPIKey {
		if scopeAllowed(ctx.Scopes, "admin:access") {
			return "admin", "admin"
		}
		// API Key 持有 subuser 权限时，Actor 会是 subuser 的 user:xxx 格式
	}
	// subuser: Actor 就是 subuser 的 username
	return "subuser", ctx.Actor
}

// ---- CRUD ----

func listRecipes(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "container:read") {
		return
	}
	ownerType, ownerID := currentUserRecipeScope(r)
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()

	out := []config.Recipe{}
	for _, recipe := range config.AppConfig.Recipes {
		// admin 看所有
		if ownerType == "admin" {
			out = append(out, recipe)
			continue
		}
		// subuser: 自己的 private + admin 创建的 shared
		if recipe.OwnerType == "subuser" && recipe.OwnerID == ownerID {
			out = append(out, recipe)
			continue
		}
		if recipe.OwnerType == "admin" && recipe.Scope == "shared" {
			out = append(out, recipe)
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
}

func getRecipe(w http.ResponseWriter, r *http.Request, id string) {
	if !requireScope(w, r, "container:read") {
		return
	}
	recipe, ok := findRecipe(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Recipe not found"})
		return
	}
	if !canAccessRecipe(r, recipe) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: recipe})
}

func createRecipe(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "container:power") {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Script      string `json:"script"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Script = strings.TrimSpace(req.Script)
	req.Scope = strings.ToLower(strings.TrimSpace(req.Scope))

	if req.Name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name is required"})
		return
	}
	if len(req.Name) > 128 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name too long (max 128 chars)"})
		return
	}
	if req.Script == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "script is required"})
		return
	}
	if len(req.Script) > 65536 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "script too large (max 64 KB)"})
		return
	}
	// 基本安全检查：禁止 rm -rf /、mkfs 等危险命令
	dangerous := []string{"rm -rf /", "mkfs.", ":(){ :|:& };:", "> /dev/sda", "dd if=", "> /dev/null", "chmod -R 777 /"}
	lowerScript := strings.ToLower(req.Script)
	for _, d := range dangerous {
		if strings.Contains(lowerScript, d) {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: fmt.Sprintf("script contains potentially dangerous command: %q", d),
			})
			return
		}
	}

	ownerType, ownerID := currentUserRecipeScope(r)
	// scope 默认值
	if req.Scope == "" {
		req.Scope = "private"
	}
	// subuser 只能创建 private
	if ownerType == "subuser" && req.Scope != "private" {
		req.Scope = "private"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	recipe := config.Recipe{
		ID:          genRecipeID(),
		Name:        req.Name,
		Description: req.Description,
		Script:      req.Script,
		OwnerID:     ownerID,
		OwnerType:   ownerType,
		Scope:       req.Scope,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	config.AppConfigMu.Lock()
	config.AppConfig.Recipes = append(config.AppConfig.Recipes, recipe)
	config.SaveConfig()
	config.AppConfigMu.Unlock()

	auditRequest(r, "recipe.create", recipe.Name, "id="+recipe.ID, true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: recipe})
}

func updateRecipe(w http.ResponseWriter, r *http.Request, id string) {
	if !requireScope(w, r, "container:power") {
		return
	}
	existing, ok := findRecipe(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Recipe not found"})
		return
	}
	if !canAccessRecipe(r, existing) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied"})
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		Script      *string `json:"script,omitempty"`
		Scope       *string `json:"scope,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Recipes {
			if cfg.Recipes[i].ID != id {
				continue
			}
			if req.Name != nil {
				name := strings.TrimSpace(*req.Name)
				if name != "" && len(name) <= 128 {
					cfg.Recipes[i].Name = name
				}
			}
			if req.Description != nil {
				cfg.Recipes[i].Description = strings.TrimSpace(*req.Description)
			}
			if req.Script != nil {
				script := strings.TrimSpace(*req.Script)
				if script != "" && len(script) <= 65536 {
					cfg.Recipes[i].Script = script
				}
			}
			if req.Scope != nil {
				// 只有原 owner 或 admin 能改 scope
				if existing.OwnerType == "admin" {
					scope := strings.ToLower(strings.TrimSpace(*req.Scope))
					if scope == "private" || scope == "shared" {
						cfg.Recipes[i].Scope = scope
					}
				}
			}
			cfg.Recipes[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			break
		}
	})
	config.SaveConfig()
	auditRequest(r, "recipe.update", existing.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func deleteRecipe(w http.ResponseWriter, r *http.Request, id string) {
	if !requireScope(w, r, "container:power") {
		return
	}
	existing, ok := findRecipe(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Recipe not found"})
		return
	}
	if !canAccessRecipe(r, existing) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied"})
		return
	}

	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := make([]config.Recipe, 0, len(cfg.Recipes))
		for _, recipe := range cfg.Recipes {
			if recipe.ID != id {
				out = append(out, recipe)
			}
		}
		cfg.Recipes = out
	})
	config.SaveConfig()
	auditRequest(r, "recipe.delete", existing.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

// executeRecipe 在指定容器上执行 recipe。
// 请求体：{ container_id: int }
// LXC: lxc-attach -n <name> -- bash -c <script>
// KVM: 先尝试 virsh qemu-agent-command guest-exec；不行回退 SSH（需要容器 IP）
func executeRecipe(w http.ResponseWriter, r *http.Request, recipeID string) {
	if !requireScope(w, r, "container:power") {
		return
	}

	recipe, ok := findRecipe(recipeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Recipe not found"})
		return
	}
	if !canAccessRecipe(r, recipe) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this recipe"})
		return
	}

	var req struct {
		ContainerID int `json:"container_id"`
		Timeout     int `json:"timeout,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	c := config.FindContainer(req.ContainerID)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	if c.Status != "running" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Container must be running to execute recipe"})
		return
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 300
	}
	if timeout > 3600 {
		timeout = 3600
	}

	var output string
	var execErr error

	// 多节点路由：容器在被控节点上时，脚本执行转发到所属 agent。
	if c.NodeID != "" {
		node, ok := config.FindNode(c.NodeID)
		if !ok || node.Address == "" {
			auditRequest(r, "recipe.execute", recipe.Name, "container="+c.Name+" node-err="+c.NodeID, false, "node unavailable")
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "容器所属节点不可用: " + c.NodeID})
			return
		}
		agentBody, _ := json.Marshal(map[string]interface{}{
			"script":  recipe.Script,
			"timeout": timeout,
		})
		data, status, err := proxyNodeRequest(r, node, http.MethodPost,
			fmt.Sprintf("/api/agent/containers/%d/recipes/execute", c.ID), strings.NewReader(string(agentBody)))
		if err != nil {
			auditRequest(r, "recipe.execute", recipe.Name, "container="+c.Name+" agent-err="+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理被控节点失败: " + err.Error()})
			return
		}
		// agent 返回非 2xx 直接透传（含 output）
		if status < 200 || status >= 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(data)
			return
		}
		auditRequest(r, "recipe.execute", recipe.Name, "container="+c.Name+" (via agent)", true, "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}

	if c.IsKVM() {
		output, execErr = executeRecipeOnKVM(c, recipe.Script, timeout)
	} else {
		output, execErr = executeRecipeOnLXC(c, recipe.Script, timeout)
	}

	if execErr != nil {
		auditRequest(r, "recipe.execute", recipe.Name, "container="+c.Name+" err="+execErr.Error(), false, execErr.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false,
			Message: "Recipe execution failed: " + execErr.Error(),
			Data:    map[string]string{"output": output},
		})
		return
	}
	auditRequest(r, "recipe.execute", recipe.Name, "container="+c.Name, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Recipe executed successfully",
		Data: map[string]interface{}{
			"recipe_name":   recipe.Name,
			"container":     c.Name,
			"output":        output,
			"exit_code":     0,
			"virtualization": c.Virtualization,
		},
	})
}

// executeRecipeOnLXC 通过 lxc-attach 执行脚本
func executeRecipeOnLXC(c *config.Container, script string, timeoutSec int) (string, error) {
	lxcName := c.LxcName()
	if lxcName == "" {
		return "", fmt.Errorf("LXC container name not configured")
	}
	cmd := exec.Command("lxc-attach", "-n", lxcName, "--", "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// executeRecipeOnKVM 通过 qemu-guest-agent + guest-exec 执行脚本
// 如果 guest-agent 不可用则回退 SSH
func executeRecipeOnKVM(c *config.Container, script string, timeoutSec int) (string, error) {
	vmName := c.VirshName()
	if vmName == "" {
		return "", fmt.Errorf("KVM VM name not configured")
	}

	// 先尝试 virsh qemu-agent-command guest-exec
	guestExecJSON := fmt.Sprintf(`{"execute":"guest-exec","arguments":{"path":"/bin/bash","arg":["-c",%q],"run-as":"root","cwd":"/"}}`, script)
	cmd := exec.Command("virsh", "qemu-agent-command", vmName, guestExecJSON)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}

	// 回退：SSH 进入
	if c.IP == "" {
		return "", fmt.Errorf("KVM qemu-guest-agent not available and no SSH IP configured; install qemu-guest-agent in the VM for recipe execution")
	}

	sshTarget := c.IP
	sshPort := 22
	if c.SSHPort > 0 {
		sshPort = c.SSHPort
	}

	if c.SSHPassword != "" {
		sshCmd := exec.Command("sshpass", "-p", c.SSHPassword,
			"ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10",
			"-p", fmt.Sprintf("%d", sshPort), "root@"+sshTarget, "bash", "-c", script)
		out2, err2 := sshCmd.CombinedOutput()
		return string(out2), err2
	}

	return "", fmt.Errorf("KVM recipe execution failed: qemu-guest-agent error (%v), and no SSH password configured for fallback", err)
}

// agentExecuteRecipe 是被控节点侧的脚本执行入口。
// 与 executeRecipe 不同：它不做 scope / owner / running 等主控侧校验（主控已做过），
// 只负责在本机容器上执行脚本并返回 stdout。
func agentExecuteRecipe(c *config.Container, script string, timeoutSec int) (string, error) {
	if c == nil {
		return "", fmt.Errorf("container nil")
	}
	if c.IsKVM() {
		return executeRecipeOnKVM(c, script, timeoutSec)
	}
	return executeRecipeOnLXC(c, script, timeoutSec)
}
