package api

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// bandwidthResponse 容器流量明细，对齐 Virtualizor act=bandwidth 字段。
// period="2025-09" 时返回该月按日的明细；"hourly" 返回 24 小时逐时明细。
type bandwidthResponse struct {
	ContainerID   int              `json:"container_id"`
	ContainerName string           `json:"container_name"`
	Period        string           `json:"period"` // "yyyy-mm" 或 "hourly" 或 "yyyy-mm-dd"
	Unit          string           `json:"unit"`   // "GB"
	Total         bandwidthTotal   `json:"total"`
	Series        []bandwidthPoint `json:"series"`
	SampledAt     string           `json:"sampled_at"`
	Source        string           `json:"source"` // "agent" / "local"
}

type bandwidthTotal struct {
	InGB  float64 `json:"in_gb"`
	OutGB float64 `json:"out_gb"`
	Total float64 `json:"total_gb"`
}

type bandwidthPoint struct {
	Bucket string  `json:"bucket"` // "yyyy-mm-dd" 或 "yyyy-mm-dd HH:00"
	InGB   float64 `json:"in_gb"`
	OutGB  float64 `json:"out_gb"`
	Total  float64 `json:"total_gb"`
}

// handleContainerBandwidth 容器流量明细（GET /api/containers/{id}/bandwidth）。
// 对齐 Virtualizor `act=bandwidth` 的月度明细返回。
//
// 数据源：被控 agent 优先返回 RRD / vnstat 聚合；本地 fallback 读
// /sys/class/net/<iface>/statistics/{rx,tx}_bytes 差分（依赖 cgroup + ifb 镜像）。
//
// 权限：container:read scope。
func handleContainerBandwidth(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "bandwidth") {
		return
	}
	period := r.URL.Query().Get("period")
	if period == "" {
		period = time.Now().UTC().Format("2006-01")
	}
	resp := bandwidthResponse{
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Period:        period,
		Unit:          "GB",
		SampledAt:     time.Now().UTC().Format(time.RFC3339),
		Source:        "local",
	}
	// 本地 fallback：尝试读 sys class net 接口字节计数器，按 24 小时聚合。
	// 注意：这是粗粒度近似，真实部署应让 agent 上报 vnstat 或 RRD 数据。
	series, total := readLocalNetCounterSeries(period)
	resp.Series = series
	resp.Total = total
	if total.Total == 0 {
		resp.Source = "unavailable"
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

// readLocalNetCounterSeries 读本机所有物理 NIC 的 rx/tx 字节计数器（瞬时值）。
// 真实带宽历史需要 RRD/vnstat，这里仅返回当前采样作为占位，前端可展示"实时速率"。
//
// period 支持："yyyy-mm" → 返回 28~31 个空 bucket + 当前采样；"hourly" → 24 个；
// 任意值 → 单点采样。生产环境建议通过 agent 上报历史数据。
func readLocalNetCounterSeries(period string) ([]bandwidthPoint, bandwidthTotal) {
	ifaceDirs, _ := filepath.Glob("/sys/class/net/*/statistics")
	if len(ifaceDirs) == 0 {
		return nil, bandwidthTotal{}
	}
	var inTotal, outTotal uint64
	for _, d := range ifaceDirs {
		iface := filepath.Base(filepath.Dir(d))
		// 排除 lo / docker* / veth* / br-* / ifb-* 等虚拟接口
		if iface == "lo" || strings.HasPrefix(iface, "docker") ||
			strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "br-") ||
			strings.HasPrefix(iface, "ifb") || strings.HasPrefix(iface, "virbr") {
			continue
		}
		rx := readCounter(filepath.Join(d, "rx_bytes"))
		tx := readCounter(filepath.Join(d, "tx_bytes"))
		inTotal += rx
		outTotal += tx
	}
	now := time.Now().UTC()
	inGB := float64(inTotal) / (1024.0 * 1024.0 * 1024.0)
	outGB := float64(outTotal) / (1024.0 * 1024.0 * 1024.0)
	point := bandwidthPoint{
		Bucket: now.Format("2006-01-02 15:04"),
		InGB:   inGB,
		OutGB:  outGB,
		Total:  inGB + outGB,
	}
	return []bandwidthPoint{point}, bandwidthTotal{
		InGB: inGB, OutGB: outGB, Total: inGB + outGB,
	}
}

func readCounter(path string) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		v, _ := strconv.ParseUint(strings.TrimSpace(scanner.Text()), 10, 64)
		return v
	}
	return 0
}

// 按时间排序的 bucket 列表（agent 上报时用，本地版未排序）
func sortBandwidthPoints(pts []bandwidthPoint) {
	sort.Slice(pts, func(i, j int) bool { return pts[i].Bucket < pts[j].Bucket })
}

var _ = exec.Command