package api

import (
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// usageExportItem 是单个容器在用量导出中的条目。
// 只包含财务计费所需字段，刻意排除 SSHPassword / SSHHostKey 等敏感字段。
type usageExportItem struct {
	UUID            string                 `json:"uuid"`
	Name            string                 `json:"name"`
	Tenant          string                 `json:"tenant,omitempty"`
	Virtualization  string                 `json:"virtualization,omitempty"`
	Node            string                 `json:"node,omitempty"`
	VCPU            float64                `json:"vcpu"`
	RAMMB           int                    `json:"ram_mb"`
	DiskGB          float64                `json:"disk_gb"`
	Status          string                 `json:"status"`
	Suspended       bool                   `json:"suspended"`
	SuspendedReason string                 `json:"suspended_reason,omitempty"`
	SuspendedAt     string                 `json:"suspended_at,omitempty"`
	ExpiresAt       string                 `json:"expires_at,omitempty"`
	CreatedAt       string                 `json:"created_at,omitempty"`
	Traffic         usageExportTraffic     `json:"traffic"`
	Usage           map[string]interface{} `json:"usage,omitempty"`
}

type usageExportTraffic struct {
	MonthlyLimitGB int    `json:"monthly_limit_gb"`
	Mode           string `json:"mode"`
	UsedRX         int64  `json:"used_rx_bytes"`
	UsedTX         int64  `json:"used_tx_bytes"`
	UsedRXGB       float64 `json:"used_rx_gb"`
	UsedTXGB       float64 `json:"used_tx_gb"`
	ResetDate      string `json:"reset_date"`
}

// HandleUsageExport 导出全量容器用量（GET /api/v1/usage?tenant=xxx），
// 供外部财务系统周期性拉取：
//   - 每容器：配置额度（vcpu/ram/disk）+ 实时用量 + 流量计数 + 生命周期状态
//     （到期/挂起），配合 suspend/unsuspend、reinstall、expiry 端点即可完成
//     "计量 → 出账 → 停复机" 的完整对接闭环。
//   - 权限：usage:read（管理员会话或显式授予该 scope 的 API Key；
//     子用户无此 scope，防止跨租户枚举）。
func HandleUsageExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "usage:read") {
		return
	}
	tenant := sanitizeTenantFilter(r.URL.Query().Get("tenant"))
	includeUsage := r.URL.Query().Get("include") != "config_only"

	containers := config.GetContainers()
	items := make([]usageExportItem, 0, len(containers))
	countByTenant := map[string]int{}
	for _, c := range containers {
		if tenant != "" && c.Tenant != tenant {
			continue
		}
		countByTenant[c.Tenant]++
		item := usageExportItem{
			UUID:            c.UUID,
			Name:            c.Name,
			Tenant:          c.Tenant,
			Virtualization:  c.Virtualization,
			VCPU:            c.VCPU,
			RAMMB:           c.RAMMB,
			DiskGB:          c.DiskGB,
			Status:          c.Status,
			Suspended:       c.Suspended,
			SuspendedReason: c.SuspendedReason,
			SuspendedAt:     c.SuspendedAt,
			ExpiresAt:       c.ExpiresAt,
			CreatedAt:       c.CreatedAt,
			Traffic: usageExportTraffic{
				MonthlyLimitGB: c.MonthlyTrafficGB,
				Mode:           c.TrafficMode,
				UsedRX:         c.TrafficUsedRX,
				UsedTX:         c.TrafficUsedTX,
				UsedRXGB:       float64(c.TrafficUsedRX) / (1 << 30),
				UsedTXGB:       float64(c.TrafficUsedTX) / (1 << 30),
				ResetDate:      c.TrafficResetDate,
			},
		}
		if includeUsage {
			// 实时用量（CPU/内存/磁盘 IO）：容器未运行时查询失败属正常，置空。
			if usage, err := usageByRuntime(c.ID); err == nil && usage != nil {
				item.Usage = usage
			}
		}
		items = append(items, item)
	}

	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"filter_tenant": tenant,
			"count":         len(items),
			"count_by_tenant": countByTenant,
			"containers":    items,
		},
	})
}

// sanitizeTenantFilter 租户过滤参数消毒：去空白、限长，防日志/响应注入。
func sanitizeTenantFilter(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 64 {
		return ""
	}
	return v
}
