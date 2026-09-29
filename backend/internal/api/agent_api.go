package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/version"
)

// 被控节点（agent 模式）专用 API，仅供主控（Controller）通过节点 token 调用。
//
// 路由：/api/agent/*（server.go 注册，AgentTokenMiddleware 鉴权）。
// 设计原则：agent 端只做本机运行时操作 + 返回结构化结果，不做审计/权限二次校验
// （主控已做过）。复杂编排（如迁移、批量操作）由主控调度。

// AgentTokenMiddleware 校验请求携带的主控 token。
// 比较使用常数时间，避免逐字节提前返回形成的时序侧信道。
func AgentTokenMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentToken := config.AgentToken()
		if agentToken == "" {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Agent API is not enabled on this node"})
			return
		}
		token := tokenFromRequest(r)
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(agentToken)) != 1 {
			jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid agent token"})
			return
		}
		// 标记认证类型：仅此类型的请求允许采信 X-Original-Actor（审计 H-3）。
		// actor 直接取主控写入的 header（agent 侧不解析 JWT），空值回退 "agent"。
		actor := strings.TrimSpace(r.Header.Get("X-Original-Actor"))
		if actor == "" {
			actor = "agent"
		}
		next(w, withAuthContext(r, AuthContext{Type: authTypeAgent, Actor: actor}))
	}
}

// HandleAgentContainers 返回本机容器列表（供主控查看）。
func HandleAgentContainers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	containers, err := listByRuntime()
	if err != nil {
		containers = config.AppConfig.Containers
	}
	if containers == nil {
		containers = []config.Container{}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: containers})
}

// HandleAgentContainerAction 执行本机容器的电源操作（供主控下发）。
func HandleAgentContainerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/agent/containers/")
	parts := strings.SplitN(rest, "/", 2)
	// /api/agent/containers/create 由主控用于在被控节点开通新容器。
	if len(parts) == 1 && parts[0] == "create" {
		agentCreateContainer(w, r)
		return
	}
	if len(parts) != 2 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid container action path"})
		return
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid container ID"})
		return
	}
	action := parts[1]
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	// 防御纵深：主控在转发前已拦截（handlers.go HandleSingleContainer），
	// 本地容器也由任务队列执行侧拦截（taskqueue.go runOperationTask）。
	// 这里对电源类操作做最后一道检查，确保主控漏检时挂起/到期/流量超限容器
	// 仍无法在被控节点上开机。unsuspend 走独立分支（先清标记再开机），不受影响。
	if action == "start" || action == "restart" || action == "reinstall" {
		if c.Suspended {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已挂起（欠费停机），不允许此操作"})
			return
		}
		if lxc.IsExpired(*c) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已到期，不允许此操作"})
			return
		}
		if lxc.IsTrafficExceeded(*c) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器流量已超限，不允许此操作"})
			return
		}
	}
	var runErr error
	switch action {
	case "start":
		runErr = startByRuntime(id)
	case "stop":
		runErr = stopByRuntime(id)
	case "restart":
		runErr = restartByRuntime(id)
	case "destroy":
		runErr = destroyByRuntime(id)
		if runErr != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: runErr.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "OK"})
		return
	case "reinstall":
		var req struct {
			TemplateID string `json:"template_id"`
			Password   string `json:"password"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.TemplateID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "template_id required"})
			return
		}
		auth := lxc.ContainerConfig{}
		if strings.TrimSpace(req.Password) != "" {
			auth.SSHPassword = strings.TrimSpace(req.Password)
		}
		runErr = reinstallByRuntime(id, req.TemplateID, auth)
	case "suspend", "unsuspend":
		// 挂起/恢复：agent 端做配置标记 + 电源操作。
		if action == "suspend" {
			_ = stopByRuntime(id)
			config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
				for i := range cfg.Containers {
					if cfg.Containers[i].ID == id {
						cfg.Containers[i].Suspended = true
						cfg.Containers[i].SuspendedAt = time.Now().Format(time.RFC3339)
						break
					}
				}
			})
			_ = config.SaveConfig()
		} else {
			config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
				for i := range cfg.Containers {
					if cfg.Containers[i].ID == id {
						cfg.Containers[i].Suspended = false
						cfg.Containers[i].SuspendedAt = ""
						cfg.Containers[i].SuspendedReason = ""
						break
					}
				}
			})
			_ = config.SaveConfig()
			_ = startByRuntime(id)
		}
	case "reset-password":
		newPassword, pwErr := agentResetPassword(id, r)
		if pwErr != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: pwErr.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "SSH password reset successfully", Data: map[string]string{"password": newPassword}})
		return
	case "hostname":
		var req struct {
			Hostname string `json:"hostname"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		req.Hostname = strings.TrimSpace(req.Hostname)
		if req.Hostname == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "hostname required"})
			return
		}
		if len(req.Hostname) > 63 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "hostname too long (max 63 chars)"})
			return
		}
		if err := setHostnameByRuntime(id, req.Hostname); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "hostname changed", Data: map[string]string{"hostname": req.Hostname}})
		return
	case "vnc-password":
		var req struct {
			Password string `json:"password"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		// 先持久化 VNCPassword 到本机 config.Container
		if c := config.FindContainer(id); c != nil {
			if c.IsKVM() {
				config.MutateContainerNoSave(id, func(cc *config.Container) {
					cc.VNCPassword = req.Password
				})
				_ = config.SaveConfig()
			} else {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "VNC password is only applicable to KVM VMs"})
				return
			}
		}
		if err := setVNCPasswordByRuntime(id, req.Password); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		msg := "VNC password changed"
		if req.Password == "" {
			msg = "VNC password cleared"
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: msg})
		return
	case "create-account":
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Sudo     bool   `json:"sudo"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if strings.TrimSpace(req.Username) == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "username required"})
			return
		}
		if err := createAccountByRuntime(id, req.Username, req.Password, req.Sudo); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Account created", Data: map[string]interface{}{"username": req.Username, "sudo": req.Sudo}})
		return
	case "usage":
		usage, uErr := usageByRuntime(id)
		if uErr != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: uErr.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: usage})
		return
	case "resize":
		var req struct {
			VCPU   int     `json:"vcpu"`
			RAMMB  int     `json:"ram_mb"`
			DiskGB float64 `json:"disk_gb"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.VCPU <= 0 && req.RAMMB <= 0 && req.DiskGB <= 0 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "At least one of vcpu, ram_mb, disk_gb must be > 0"})
			return
		}
		container := config.FindContainer(id)
		if container == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
			return
		}
		if container.IsKVM() {
			if err := kvmManager.ResizeContainer(id, req.VCPU, req.RAMMB, req.DiskGB); err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
				return
			}
		} else {
			if err := lxcManager.ResizeContainer(id, req.VCPU, req.RAMMB, req.DiskGB); err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
		if req.DiskGB > 0 && req.DiskGB > container.DiskGB {
			if err := resizeDiskByRuntime(container, req.DiskGB); err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
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
		_ = config.SaveConfig()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "resized", Data: map[string]interface{}{"vcpu": container.VCPU, "ram_mb": container.RAMMB, "disk_gb": container.DiskGB}})
		return
	case "snapshot":
		var req struct {
			Name string `json:"name"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		snap, err := createSnapshotByRuntime(id, "agent", false, 0)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: snap})
		return
	case "snapshots/delete":
		var req struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.SnapshotID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "snapshot_id required"})
			return
		}
		if err := deleteSnapshotByRuntime(req.SnapshotID); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "snapshot deleted"})
		return
	case "snapshots/restore":
		var req struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.SnapshotID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "snapshot_id required"})
			return
		}
		if err := restoreSnapshotByRuntime(req.SnapshotID); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "snapshot restored"})
		return
	case "clone":
		// 主控侧已分配好新容器的全部标识；agent 只执行运行时克隆。
		var req struct {
			Name            string `json:"name"`
			NewID           int    `json:"new_id"`
			NewUUID         string `json:"new_uuid"`
			NewLxcName      string `json:"new_lxc_name"`
			NewVMName       string `json:"new_vm_name"`
			NewVNCPort      string `json:"new_vnc_port"`
			NewSSHPort      string `json:"new_ssh_port"`
			NewMAC          string `json:"new_mac"`
			Mode            string `json:"mode"`
			StartAfterClone bool   `json:"start_after_clone"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.Name == "" || req.NewID <= 0 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name and new_id required"})
			return
		}
		src := config.FindContainer(id)
		if src == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Source container not found on agent node"})
			return
		}
		if err := cloneByRuntime(src, req.Name, req.NewLxcName, req.NewVMName,
			req.NewID, req.NewUUID, req.NewVNCPort, req.NewSSHPort, req.NewMAC,
			req.Mode, req.StartAfterClone); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Runtime clone failed: " + err.Error()})
			return
		}
		// agent 侧也更新本机 config（主控会同步拉回）
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			newC := *src
			newC.ID = req.NewID
			newC.UUID = req.NewUUID
			newC.Name = req.Name
			newC.LXCName = req.NewLxcName
			newC.KVMName = req.NewVMName
			newC.MACAddress = req.NewMAC
			if p, _ := strconv.Atoi(req.NewVNCPort); p > 0 {
				newC.VNCPort = p
			}
			if p, _ := strconv.Atoi(req.NewSSHPort); p > 0 {
				newC.SSHPort = p
			}
			newC.IP = ""
			newC.LANIPv4Address = ""
			newC.PublicIPv4s = []config.PublicIPv4Assignment{}
			newC.IPv6 = ""
			newC.IPv6Addresses = []config.IPv6Assignment{}
			newC.Status = "stopped"
			cfg.Containers = append(cfg.Containers, newC)
		})
		_ = config.SaveConfig()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "container cloned on agent node"})
		return
	case "recipes/execute":
		var req struct {
			Script  string `json:"script"`
			Timeout int    `json:"timeout"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if strings.TrimSpace(req.Script) == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "script required"})
			return
		}
		timeout := req.Timeout
		if timeout <= 0 {
			timeout = 300
		}
		c := config.FindContainer(id)
		if c == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "container not found"})
			return
		}
		output, execErr := agentExecuteRecipe(c, req.Script, timeout)
		if execErr != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{
				Success: false,
				Message: "Recipe execution failed: " + execErr.Error(),
				Data:    map[string]string{"output": output},
			})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Recipe executed successfully",
			Data: map[string]interface{}{
				"container": c.Name,
				"output":    output,
				"exit_code": 0,
			},
		})
		return
	// === Virtualizor 风格容器自服务端点（agent 本地实现）===
	case "stats":
		handleContainerStats(w, r, c)
		return
	case "bandwidth":
		handleContainerBandwidth(w, r, c)
		return
	case "processes":
		handleContainerProcesses(w, r, c)
		return
	case "processes/kill":
		handleContainerProcessKill(w, r, c)
		return
	case "services":
		handleContainerServices(w, r, c)
		return
	case "services/action":
		handleContainerServiceAction(w, r, c)
		return
	case "hvm-settings":
		if r.Method == http.MethodGet {
			handleContainerHVMSettingsGet(w, r, c)
			return
		}
		if r.Method == http.MethodPut {
			handleContainerHVMSettingsPut(w, r, c)
			return
		}
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	case "scheduled-actions":
		if r.Method == http.MethodGet {
			handleScheduledActionsList(w, r, c)
			return
		}
		if r.Method == http.MethodPost {
			handleScheduledActionCreate(w, r, c)
			return
		}
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	case "scheduled-actions/delete":
		var req struct {
			ActionID string `json:"action_id"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.ActionID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "action_id required"})
			return
		}
		handleScheduledActionDelete(w, r, c, req.ActionID)
		return
	case "rescue":
		// 主控转发过来的 rescue 请求。
		// 直接复用 handleContainerRescuePost 的解析 + doRescue 逻辑。
		handleContainerRescuePost(w, r, c)
		return
	case "iso":
		// 主控转发过来的 ISO 挂载请求。
		handleContainerISOActionPost(w, r, c)
		return
	case "vnc-ticket":
		// VNC ticket 必须在 agent 端生成（因为 libvirt VNC socket 就在 agent 上）。
		// 主控转发过来时已经 routeToAgent 过一次，这里不再二次转发。
		handleContainerVNCTicketPost(w, r, c)
		return
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unknown action: " + action})
		return
	}
	if runErr != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: runErr.Error()})
		return
	}
	// 同步状态到配置
	switch action {
	case "start", "restart":
		config.UpdateContainerStatus(id, "running")
	case "stop":
		config.UpdateContainerStatus(id, "stopped")
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "OK"})
}

// agentCreateContainer 由主控下发创建请求，在被控节点开通（发机）新容器。
func agentCreateContainer(w http.ResponseWriter, r *http.Request) {
	var req lxc.ContainerConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid create request: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container name required"})
		return
	}
	if req.TemplateID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "template required"})
		return
	}
	if err := validateRuntimeResourceRequest(req.Virtualization, req.TemplateID, req.VCPU, req.RAMMB, req.DiskGB); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := createByRuntime(req); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	created := config.FindContainerByName(req.Name)
	if created == nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "container created but not found in config"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "container created", Data: created})
}

// agentResetPassword 处理母控下发的子容器密码重置。未填密码时由运行时自动生成新密码。
func agentResetPassword(id int, r *http.Request) (string, error) {
	var req struct {
		Password string `json:"password"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			return "", err
		}
	}
	password := strings.TrimSpace(req.Password)
	if password != "" {
		if err := lxc.ValidateCustomSSHPassword(password); err != nil {
			return "", err
		}
	}
	return resetPasswordByRuntime(id, password)
}

// AgentContainerActionFromQuery 兼容 /api/agent/containers?action=start&id=1 的调用形式。
func AgentContainerActionFromQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		ID     int    `json:"id"`
		Action string `json:"action"`
	}
	body := r.Body
	if body != nil {
		_ = json.NewDecoder(body).Decode(&req)
	}
	if req.ID <= 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container id required"})
		return
	}
	rr := r.Clone(r.Context())
	rr.URL.Path = "/api/agent/containers/" + strconv.Itoa(req.ID) + "/" + req.Action
	HandleAgentContainerAction(w, rr)
}

// HandleAgentNodeBackup 对被控本机的全部容器做一份完整备份（节点级冷备份）。
// 用于被控节点重装/重建前的数据保全。返回成功/失败明细。
func HandleAgentNodeBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	containers := config.AppConfig.Containers
	done := 0
	failed := []string{}
	var totalBytes int64
	for i := range containers {
		c := &containers[i]
		b, err := createInstanceBackup(c.ID, "node-cold", 0, false)
		if err != nil {
			failed = append(failed, c.Name+": "+err.Error())
			continue
		}
		if b != nil {
			done++
			totalBytes += b.SizeBytes
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"backed_up":     done,
		"failed":        failed,
		"total_bytes":   totalBytes,
		"container_cnt": len(containers),
	}})
}

// HandleAgentSelfUpdate POST /api/agent/self-update
//
// 主控「一键升级被控节点」下发入口（agent token 鉴权）。请求体：
//
//	{"target_version": "2.2.17", "check_only": false}
//
// 语义：
//   - check_only=true → 只回报本机版本与目标版本是否一致；
//   - 否则立即返回 202（accepted=true），**升级在响应落地 2 秒后执行**：
//     就地替换二进制 → detached 重启服务。主控通过节点心跳里的版本字段确认结果。
//
// 之所以延迟执行：升级会重启本进程服务，先回响应才能让主控拿到"已受理"，
// 否则主控只会看到连接被重置。
func HandleAgentSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		TargetVersion string `json:"target_version"`
		CheckOnly     bool   `json:"check_only"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body: " + err.Error()})
			return
		}
	}
	target := strings.TrimSpace(req.TargetVersion)
	current := version.Current()

	if req.CheckOnly {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"current_version": current,
			"target_version":  target,
			"up_to_date":      target == "" || sameVersionString(current, target),
		}})
		return
	}

	// 目标版本格式先校验，避免把非法 tag 带进下载路径。
	if target != "" && !cli.ValidateReleaseTag(target) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的目标版本标签: " + target})
		return
	}

	go func() {
		// 留出时间让 202 响应完整落回主控，再执行替换 + 重启。
		time.Sleep(2 * time.Second)
		newVersion, upgraded, err := cli.SelfUpdateToVersion(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "被控自更新失败（目标 %s）: %v\n", target, err)
			return
		}
		if upgraded {
			fmt.Printf("被控已按主控指令升级到 %s\n", newVersion)
		}
	}()

	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "升级已受理，节点将在数秒内重启服务", Data: map[string]interface{}{
		"current_version": current,
		"target_version":  target,
		"accepted":        true,
	}})
}

// sameVersionString 比较版本号字符串（去掉 v 前缀与两端空白）。
func sameVersionString(a, b string) bool {
	norm := func(v string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	}
	return norm(a) == norm(b)
}
