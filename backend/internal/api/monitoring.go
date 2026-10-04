package api

import (
	"net/http"
	"sort"
	"time"

	"eyvescloud/internal/config"
)

// ContainerMonitorRow 是管理端集中监控列表中的一行：容器基本信息 + 最新指标 + 滥用概况。
type ContainerMonitorRow struct {
	ID             int     `json:"id"`
	UUID           string  `json:"uuid"`
	Name           string  `json:"name"`
	Virtualization string  `json:"virtualization"`
	Status         string  `json:"status"`
	Tenant         string  `json:"tenant,omitempty"`
	Owner          string  `json:"owner,omitempty"`
	IP             string  `json:"ip,omitempty"`
	VCPU           float64 `json:"vcpu"`
	RAMMB          int     `json:"ram_mb"`
	DiskGB         float64 `json:"disk_gb"`
	CPU            float64 `json:"cpu"`
	Memory         float64 `json:"memory"`
	NetworkRx      float64 `json:"network_rx"`
	NetworkTx      float64 `json:"network_tx"`
	DiskRead       float64 `json:"disk_read"`
	DiskWrite      float64 `json:"disk_write"`
	TrafficUsedRX  float64 `json:"traffic_used_rx_gb"`
	TrafficUsedTX  float64 `json:"traffic_used_tx_gb"`
	MetricTS       int64   `json:"metric_ts,omitempty"`
	AbuseAlerts    int     `json:"abuse_alerts"`
	AbuseSeverity  string  `json:"abuse_severity,omitempty"`
	PolicyBlocked  bool    `json:"policy_blocked"`
}

// latestContainerMetric 返回内存中最新的一个采样点（轻量，不查库）。
func latestContainerMetric(key string) (ContainerMetricPoint, bool) {
	containerMetricMu.RLock()
	defer containerMetricMu.RUnlock()
	points := containerMetricHistory[key]
	if len(points) == 0 {
		return ContainerMetricPoint{}, false
	}
	return points[len(points)-1], true
}

// containerAbuseTotals 汇总每个容器的滥用告警数量与最高严重级别。
func containerAbuseTotals() (map[string]int, map[string]string) {
	counts := make(map[string]int)
	severities := make(map[string]string)
	for _, alert := range mergedSecurityAlerts() {
		count := alert.Count
		if count < 1 {
			count = 1
		}
		counts[alert.ContainerName] += count
		if severityRank(alert.Severity) > severityRank(severities[alert.ContainerName]) {
			severities[alert.ContainerName] = alert.Severity
		}
	}
	return counts, severities
}

// HandleContainerMonitoring 返回全部容器的实时指标与滥用概况，供管理端集中监控。
// 与单容器详情接口不同，这里一次性返回所有容器，便于管理端做全局态势查看。
func HandleContainerMonitoring(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}

	allowed, restricted := requestAllowedContainers(r)
	abuseCounts, abuseSeverities := containerAbuseTotals()

	containers := config.GetContainers()
	rows := make([]ContainerMonitorRow, 0, len(containers))
	for _, c := range containers {
		if restricted && !isContainerAllowed(allowed, &c) {
			continue
		}

		row := ContainerMonitorRow{
			ID:             c.ID,
			UUID:           c.UUID,
			Name:           c.Name,
			Virtualization: c.Virtualization,
			Status:         c.Status,
			Tenant:         c.Tenant,
			IP:             c.IP,
			VCPU:           c.VCPU,
			RAMMB:          c.RAMMB,
			DiskGB:         c.DiskGB,
			TrafficUsedRX:  bytesToGB(c.TrafficUsedRX),
			TrafficUsedTX:  bytesToGB(c.TrafficUsedTX),
			AbuseAlerts:    abuseCounts[c.Name],
			AbuseSeverity:  abuseSeverities[c.Name],
			PolicyBlocked:  c.PolicyBlocked,
		}
		if tenant, owner, _ := resolveAbuseOwnership(c.Name); owner != "" || tenant != "" {
			if row.Tenant == "" {
				row.Tenant = tenant
			}
			row.Owner = owner
		}
		if point, ok := latestContainerMetric(containerMetricKey(c)); ok {
			row.CPU = point.CPU
			row.Memory = point.Memory
			row.NetworkRx = point.NetworkRx
			row.NetworkTx = point.NetworkTx
			row.DiskRead = point.DiskRead
			row.DiskWrite = point.DiskWrite
			row.MetricTS = point.TS
		}
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].AbuseAlerts != rows[j].AbuseAlerts {
			return rows[i].AbuseAlerts > rows[j].AbuseAlerts
		}
		if rows[i].CPU != rows[j].CPU {
			return rows[i].CPU > rows[j].CPU
		}
		return rows[i].Name < rows[j].Name
	})

	total := len(rows)
	// 汇总（分页前全量口径）：容器总数 / 运行中 / 涉及滥用 / 滥用告警总数。
	summary := map[string]int{"running": 0, "abusers": 0, "abuse_alerts": 0}
	for _, row := range rows {
		if row.Status == "running" {
			summary["running"]++
		}
		if row.AbuseAlerts > 0 {
			summary["abusers"]++
		}
		summary["abuse_alerts"] += row.AbuseAlerts
	}
	payload := map[string]interface{}{
		"containers":   rows,
		"total":        total,
		"summary":      summary,
		"generated_at": time.Now().Format("2006-01-02 15:04:05"),
	}
	if p := parsePagination(r); p.Requested {
		if p.Invalid {
			errResponse(w, http.StatusBadRequest, "INVALID_REQUEST",
				"page must be >= 1 and page_size within [1, 200]")
			return
		}
		payload["containers"] = paginate(rows, p)
		payload["page"] = p.Page
		payload["page_size"] = p.PageSize
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: payload})
}

func bytesToGB(bytes int64) float64 {
	if bytes <= 0 {
		return 0
	}
	return float64(bytes) / (1024 * 1024 * 1024)
}
