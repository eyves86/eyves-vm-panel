package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

var _ = exec.Command

// statsResponse 容器一站式监控视图，对齐主流面板语义 的字段形态。
// cpu/disk/ram 都返回 used/limit/percent 三元组，前端可直接渲染进度条。
type statsResponse struct {
	CPU       cpuStats   `json:"cpu"`
	RAM       ramStats   `json:"ram"`
	Disk      diskStats  `json:"disk"`
	Inodes    inodesStats `json:"inodes"`
	Uptime    int64      `json:"uptime_sec"`
	SampledAt string     `json:"sampled_at"`
	Source    string     `json:"source"` // "agent" / "local" / "unavailable"
}

type cpuStats struct {
	UsedPercent float64 `json:"used_percent"`
	Cores       int     `json:"cores"`
	LimitCores  int     `json:"limit_cores"`
}

type ramStats struct {
	UsedMB  int64   `json:"used_mb"`
	LimitMB int64   `json:"limit_mb"`
	Percent float64 `json:"percent"`
	SwapMB  int64   `json:"swap_mb,omitempty"`
}

type diskStats struct {
	UsedGB  float64 `json:"used_gb"`
	LimitGB float64 `json:"limit_gb"`
	Percent float64 `json:"percent"`
}

type inodesStats struct {
	Used    int64   `json:"used"`
	Limit   int64   `json:"limit"`
	Percent float64 `json:"percent"`
}

// handleContainerStats 容器一站式监控（GET /api/containers/{id}/stats）。
// 对齐主流面板语义，合并 cpu/ram/disk/inodes/uptime。
//
// 实现策略：
//   - 多节点：转发到 agent（被控节点返回完整数据，主控不重复实现）
//   - 单机 LXC：主控本地 lxc-attach 拿数据
//   - KVM：无 guest-agent 时返回 partial 数据 + 提示
//
// 权限：container:read scope。子用户可访问自己容器（caller 校验已在上层完成）。
func handleContainerStats(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "stats") {
		return
	}
	resp := statsResponse{
		CPU: cpuStats{
			Cores:      int(c.VCPU),
			LimitCores: int(c.VCPU),
		},
		RAM: ramStats{
			LimitMB: int64(c.RAMMB),
		},
		Disk: diskStats{
			LimitGB: c.DiskGB,
		},
		SampledAt: time.Now().UTC().Format(time.RFC3339),
		Source:    "local",
	}
	if c.IsKVM() {
		resp.Source = "unavailable"
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: false,
			Message: "KVM 实时监控需在容器内安装 qemu-guest-agent；当前只返回规格上限",
			Data:    resp,
		})
		return
	}
	lxcName := c.LxcName()
	if lxcName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "容器 LXC 内部名缺失"})
		return
	}
	// RAM：lxc-attach -n NAME -- free -m
	if used, swap, err := lxcAttachFree(lxcName); err == nil {
		resp.RAM.UsedMB = used
		resp.RAM.SwapMB = swap
		if resp.RAM.LimitMB > 0 {
			resp.RAM.Percent = float64(used) / float64(resp.RAM.LimitMB) * 100.0
		}
	}
	// Disk：df -PBG /
	if usedGB, totalGB, err := lxcAttachDFBG(lxcName); err == nil && totalGB > 0 {
		resp.Disk.UsedGB = usedGB
		resp.Disk.LimitGB = totalGB
		resp.Disk.Percent = usedGB / totalGB * 100.0
	}
	// Inodes：df -Pi /
	if used, limit, err := lxcAttachDFInodes(lxcName); err == nil && limit > 0 {
		resp.Inodes.Used = used
		resp.Inodes.Limit = limit
		resp.Inodes.Percent = float64(used) / float64(limit) * 100.0
	}
	// CPU：lxc info NAME 解析 CPU usage（瞬时值，前端差分得到占用率）
	if used, err := lxcInfoCPUUsage(lxcName); err == nil {
		resp.CPU.UsedPercent = used
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

func lxcAttachFree(name string) (usedMB, swapMB int64, err error) {
	cmd, cancel := execLXCAttachWithTimeout(name, "free", "-m")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	usedMB = parseFreeUsedMB(string(out))
	swapMB = parseFreeSwapMB(string(out))
	return usedMB, swapMB, nil
}

func lxcAttachDFBG(name string) (usedGB, totalGB float64, err error) {
	cmd, cancel := execLXCAttachWithTimeout(name, "df", "-PBG", "/")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	used, total, perr := parseDFBG(string(out))
	if perr != nil {
		return 0, 0, perr
	}
	return used, total, nil
}

func lxcAttachDFInodes(name string) (used, limit int64, err error) {
	cmd, cancel := execLXCAttachWithTimeout(name, "df", "-Pi", "/")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	u, l, perr := parseDFInodes(string(out))
	if perr != nil {
		return 0, 0, perr
	}
	return u, l, nil
}

// lxcInfoCPUUsage 解析 `lxc info NAME` 输出中的 CPU usage（秒）。
// 返回的是累计 CPU 时间，不是百分比；前端按时间窗差分得占用率。
func lxcInfoCPUUsage(name string) (float64, error) {
	cmd, cancel := execWithTimeout("lxc", "info", name)
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CPU usage:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				v, _ := strconv.ParseFloat(strings.TrimSuffix(parts[2], "s"), 64)
				return v, nil
			}
		}
	}
	return 0, nil
}

func parseFreeUsedMB(s string) int64 {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && (fields[0] == "Mem:" || fields[0] == "总计") {
			v, _ := strconv.ParseInt(fields[2], 10, 64)
			return v
		}
	}
	return 0
}

func parseFreeSwapMB(s string) int64 {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && (fields[0] == "Swap:" || fields[0] == "交换:") {
			v, _ := strconv.ParseInt(fields[2], 10, 64)
			return v
		}
	}
	return 0
}

func parseDFBG(s string) (used, total float64, err error) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasSuffix(fields[0], "/") {
			continue
		}
		t, terr := strconv.ParseFloat(strings.TrimSuffix(fields[1], "G"), 64)
		u, uerr := strconv.ParseFloat(strings.TrimSuffix(fields[2], "G"), 64)
		if terr != nil || uerr != nil {
			return 0, 0, fmt.Errorf("parse df -PBG: %w/%w", terr, uerr)
		}
		return u, t, nil
	}
	return 0, 0, fmt.Errorf("no df row matched")
}

func parseDFInodes(s string) (used, limit int64, err error) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasSuffix(fields[0], "/") {
			continue
		}
		u, uerr := strconv.ParseInt(fields[2], 10, 64)
		l, lerr := strconv.ParseInt(fields[1], 10, 64)
		if uerr != nil || lerr != nil {
			return 0, 0, fmt.Errorf("parse df -i: %w/%w", uerr, lerr)
		}
		return u, l, nil
	}
	return 0, 0, fmt.Errorf("no df row matched")
}

// routeToAgent 容器级转发：与主 handler 内联 routeToAgent 类似。
// 如果容器没有 NodeID（非多节点场景）直接返回 false，由调用方走本地实现。
// agent 不可达或返回 5xx 也返回 false，让本地 fallback 接管（避免 agent 单点故障）。
//
// 对 POST/PUT 必须把原始 r.Body 透传给 agent，否则 agent 端会收到空 body 导致
// 杀进程 / 服务操作 / HVM 写入 / 定时任务创建等写动作全部丢失入参。
func routeToAgent(w http.ResponseWriter, r *http.Request, c *config.Container, action string) bool {
	if c == nil || c.NodeID == "" {
		return false
	}
	node, ok := config.FindNode(c.NodeID)
	if !ok || node.Address == "" {
		return false
	}
	method := http.MethodGet
	switch action {
	case "processes/kill", "services/action", "rescue", "iso":
		method = http.MethodPost
	case "scheduled-actions", "scheduled-actions/delete":
		// 这两个 action 主控端允许 GET（list）和 POST（create）；
		// 用 r.Method 透传，避免误把 list 也当作 create。
		if r.Method == http.MethodPost {
			method = http.MethodPost
		}
	case "hvm-settings":
		// hvm-settings 既可以是 GET（读）也可以是 PUT（写），透传原方法。
		if r.Method == http.MethodPut {
			method = http.MethodPut
		}
	}
	var body io.Reader
	if method != http.MethodGet && r.Body != nil {
		// body 是一次性的 ReadCloser，必须读到 []byte 再包装成 Reader 才能转发。
		// 8MB 上限对齐 proxyNodeRequest 的 8MB 限制，防止 OOM。
		buf, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
		if err != nil {
			// 主体读取失败：本机 fallback 不要再去解析损坏的 body（已读到 EOF 后的状态机），
			// 直接返回 false 让调用方按"无 body"语义自行处理（GET 类无影响）。
			return false
		}
		body = bytes.NewReader(buf)
		// 关键：读完后立刻用完整缓存重建 r.Body（不消耗原 reader 的状态），
		// 这样转发失败返回 false 时，本地 fallback handler 还能再读一次。
		r.Body = io.NopCloser(bytes.NewReader(buf))
	}
	data, status, err := proxyNodeRequest(r, node, method,
		fmt.Sprintf("/api/agent/containers/%d/%s", nodeLocalID(c), action), body)
	if err != nil || status >= 500 {
		// 转发失败，但 r.Body 已经被上面重建过，handler 继续读没问题。
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	return true
}

// 抑制未使用 fmt 告警（routeToAgent 内部用）
var _ = fmt.Sprintf
// ---- 命令执行超时工具 ----
// 容器内命令超时（秒）。30s 足够 ps/free/df/systemctl 完成，同时能避免容器 stopped
// 或 lxc-attach 挂住时 handler 永远阻塞（HTTP per-request goroutine 会被占住）。
const execCommandTimeoutSec = 30

func execLXCAttachWithTimeout(lxcName string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), execCommandTimeoutSec*time.Second)
	fullArgs := append([]string{"-n", lxcName, "--"}, args...)
	return exec.CommandContext(ctx, "lxc-attach", fullArgs...), cancel
}

func execWithTimeout(name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), execCommandTimeoutSec*time.Second)
	return exec.CommandContext(ctx, name, args...), cancel
}

// execWithTimeoutCustom 允许调用方指定不同的 timeout（秒），
// 用于 tar / certbot / resize2fs 等耗时可能较长的底层工具。
func execWithTimeoutCustom(timeoutSec int, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	return exec.CommandContext(ctx, name, args...), cancel
}
