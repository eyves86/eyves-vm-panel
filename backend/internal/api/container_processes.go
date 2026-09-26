package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// processInfo 单个进程条目，对齐 Virtualizor act=processes 输出。
type processInfo struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPUPct  float64 `json:"cpu_pct"`
	MemPct  float64 `json:"mem_pct"`
	RSSKB   int64   `json:"rss_kb"`
	Stat    string  `json:"stat"`
	Command string  `json:"command"`
}

type processesResponse struct {
	ContainerID   int           `json:"container_id"`
	ContainerName string        `json:"container_name"`
	Total         int           `json:"total"`
	Processes     []processInfo `json:"processes"`
	SampledAt     string        `json:"sampled_at"`
	Source        string        `json:"source"`
}

// handleContainerProcesses 容器进程列表（GET /api/containers/{id}/processes）。
// 对齐 Virtualizor act=processes。仅 LXC 可在主控本地 lxc-attach ps；
// KVM 需要 qemu-guest-agent 才能拿到容器内进程。
//
// 权限：container:read scope（只读）。
func handleContainerProcesses(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "processes") {
		return
	}
	resp := processesResponse{
		ContainerID:   c.ID,
		ContainerName: c.Name,
		SampledAt:     time.Now().UTC().Format(time.RFC3339),
		Source:        "local",
	}
	if c.IsKVM() {
		resp.Source = "unavailable"
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: false,
			Message: "KVM 进程查看需在容器内安装 qemu-guest-agent",
			Data:    resp,
		})
		return
	}
	lxcName := c.LxcName()
	if lxcName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "容器 LXC 内部名缺失"})
		return
	}
	// ps -eo pid,user,%cpu,%mem,rss,stat,comm,args — 按 CPU% 倒序
	cmd := exec.Command("lxc-attach", "-n", lxcName, "--",
		"ps", "-eo", "pid=,user=,%cpu=,%mem=,rss=,stat=,comm=,args=",
		"--sort=-%cpu")
	out, err := cmd.Output()
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false,
			Message: "在容器内执行 ps 失败: " + err.Error(),
		})
		return
	}
	resp.Processes = parsePSOutput(string(out))
	resp.Total = len(resp.Processes)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

func parsePSOutput(s string) []processInfo {
	var out []processInfo
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// ps 字段以空格分隔；前 6 列固定，comm + args（可含空格）拼成 command。
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		mem, _ := strconv.ParseFloat(fields[3], 64)
		rss, _ := strconv.ParseInt(fields[4], 10, 64)
		// comm（字段 6）是可执行文件名，args（字段 7+）是命令行参数；
		// 拼成 "comm args"，对带参数启动的进程（sshd -D / nginx worker）更直观。
		comm := fields[6]
		var cmd string
		if len(fields) > 7 {
			cmd = comm + " " + strings.Join(fields[7:], " ")
		} else {
			cmd = comm
		}
		out = append(out, processInfo{
			PID:     pid,
			User:    fields[1],
			CPUPct:  cpu,
			MemPct:  mem,
			RSSKB:   rss,
			Stat:    fields[5],
			Command: cmd,
		})
	}
	return out
}

// killProcessesRequest 杀进程请求体。
type killProcessesRequest struct {
	PIDs []int `json:"pids"`
	// Signal: TERM（默认）/ KILL / HUP / INT；空 = TERM
	Signal string `json:"signal"`
}

// handleContainerProcessKill 批量终止容器内进程（POST /api/containers/{id}/processes/kill）。
// 对齐 Virtualizor act=processes + sel_proc[]。
//
// 权限：container:power scope（写）。仅 LXC 支持；KVM 必须用 guest-agent。
//
// 安全：
//   - PID 白名单 1..4194304（PID_MAX_LIMIT）
//   - 最多批量 64 个
//   - Signal 白名单 TERM/KILL/HUP/INT
//   - 禁止杀 PID 1（init）
func handleContainerProcessKill(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:power") {
		return
	}
	if routeToAgent(w, r, c, "processes/kill") {
		return
	}
	var req killProcessesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	if len(req.PIDs) == 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "pids 不能为空"})
		return
	}
	if len(req.PIDs) > 64 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "pids 最多 64 个"})
		return
	}
	sig := strings.ToUpper(strings.TrimSpace(req.Signal))
	if sig == "" {
		sig = "TERM"
	}
	switch sig {
	case "TERM", "KILL", "HUP", "INT":
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "signal 必须为 TERM/KILL/HUP/INT",
		})
		return
	}
	for _, pid := range req.PIDs {
		if pid < 1 || pid > 4194304 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "pid 越界: " + strconv.Itoa(pid),
			})
			return
		}
		if pid == 1 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "禁止终止 PID 1 (init)",
			})
			return
		}
	}
	if c.IsKVM() {
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false, Message: "KVM 进程终止需 qemu-guest-agent，未实现",
		})
		return
	}
	lxcName := c.LxcName()
	if lxcName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "容器 LXC 内部名缺失"})
		return
	}
	// 在容器内 kill：lxc-attach -n NAME -- kill -SIG PID1 PID2 ...
	args := []string{"-n", lxcName, "--", "kill", "-" + sig}
	for _, pid := range req.PIDs {
		args = append(args, strconv.Itoa(pid))
	}
	cmd := exec.Command("lxc-attach", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false,
			Message: "kill 失败: " + strings.TrimSpace(string(out)),
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "已发送 " + sig + " 给 " + strconv.Itoa(len(req.PIDs)) + " 个进程",
	})
}