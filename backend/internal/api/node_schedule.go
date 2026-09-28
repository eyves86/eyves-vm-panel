package api

import (
	"net/http"
	"strconv"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/scheduler"
)

// node_schedule.go —— 多节点放置调度的 HTTP 入口。
//
// 背景：internal/scheduler 已实现完整的「过滤 → 评分 → 决策 → 留痕」流水线，
// 但此前没有任何调用方（创建容器只能落在主控本机）。本文件把它接到 API：
//   - GET /api/nodes/schedule  —— 只做调度决策，不创建任何东西（供创建页
//     「自动选择节点」预览/解释，以及排查"为什么没得选"）。
//
// 直发路径：调用方直接指定 node_id 时**不经过调度**（管理员直发），与
// scheduler 包的设计说明一致。

// nodeScheduleResponse 是调度结果（对外结构，含可读诊断）。
type nodeScheduleResponse struct {
	Chosen     *scheduledNode   `json:"chosen,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Candidates []scheduleCandid `json:"candidates"`
}

type scheduledNode struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Address        string  `json:"address"`
	Status         string  `json:"status"`
	RAMTotalMB     int64   `json:"ram_total_mb"`
	RAMUsedMB      int64   `json:"ram_used_mb"`
	DiskTotalGB    float64 `json:"disk_total_gb"`
	DiskUsedGB     float64 `json:"disk_used_gb"`
	ContainerCount int     `json:"container_count"`
	VCPUCount      int     `json:"cpu_count"`
}

type scheduleCandid struct {
	NodeID        string   `json:"node_id"`
	NodeName      string   `json:"node_name"`
	Passed        bool     `json:"passed"`
	Score         float64  `json:"score"`
	FilterReasons []string `json:"filter_reasons,omitempty"`
	ScoreReasons  []string `json:"score_reasons,omitempty"`
}

func nodeToScheduledNode(n config.Node) scheduledNode {
	return scheduledNode{
		ID:             n.ID,
		Name:           n.Name,
		Address:        n.Address,
		Status:         n.Status,
		RAMTotalMB:     n.RAMTotalMB,
		RAMUsedMB:      n.RAMUsedMB,
		DiskTotalGB:    n.DiskTotalGB,
		DiskUsedGB:     n.DiskUsedGB,
		ContainerCount: n.ContainerCount,
		VCPUCount:      n.CPUCount,
	}
}

// HandleNodeSchedule GET /api/nodes/schedule?ram_mb=&disk_gb=&vcpu=&virt=&storage=&tenant=&count=
//
// 返回调度器选中的节点与 top-N 候选诊断。不做任何写操作。
func HandleNodeSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "node:read") {
		return
	}

	q := r.URL.Query()
	req := scheduler.Request{
		RAMMB:               parseInt64Default(q.Get("ram_mb"), 0),
		DiskGB:              parseFloatDefault(q.Get("disk_gb"), 0),
		ContainerCountDelta: int(parseInt64Default(q.Get("count"), 1)),
		StorageBackend:      strings.TrimSpace(q.Get("storage")),
		VirtType:            strings.ToLower(strings.TrimSpace(q.Get("virt"))),
		ImageID:             strings.TrimSpace(q.Get("image")),
		TenantID:            strings.TrimSpace(q.Get("tenant")),
		RequestID:           "schedule-" + randomHex(6),
	}

	diag, err := scheduler.Place(req, scheduler.DefaultPolicy(), 5)
	resp := nodeScheduleResponse{
		Reason:     diag.Reason,
		Candidates: make([]scheduleCandid, 0, len(diag.Candidates)),
	}
	for _, c := range diag.Candidates {
		item := scheduleCandid{
			NodeID:        c.NodeID,
			Passed:        c.PassedFilter,
			Score:         c.Score,
			FilterReasons: c.FilterReasons,
			ScoreReasons:  c.ScoreReasons,
		}
		if n, ok := config.FindNode(c.NodeID); ok {
			item.NodeName = n.Name
		}
		resp.Candidates = append(resp.Candidates, item)
	}
	if diag.Chosen != "" {
		if n, ok := config.FindNode(diag.Chosen); ok {
			chosen := nodeToScheduledNode(n)
			resp.Chosen = &chosen
		}
	}

	if err != nil || resp.Chosen == nil {
		msg := diag.Reason
		if msg == "" {
			msg = "没有满足条件的节点（需在线、未维护、容量足够）"
		}
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: false,
			Code:    "NO_ELIGIBLE_NODE",
			Message: msg,
			Data:    resp,
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

func parseInt64Default(raw string, fallback int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		return v
	}
	return fallback
}

func parseFloatDefault(raw string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
		return v
	}
	return fallback
}
