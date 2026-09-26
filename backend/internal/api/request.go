package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"eyvescloud/internal/config"
)

// paginationParams 描述列表端点的分页请求（企业级 API 契约）。
// page 从 1 起；page_size 默认 50、上限 200，非法值回退默认并报告。
type paginationParams struct {
	Page      int
	PageSize  int
	Requested bool // 客户端是否显式传了分页参数（未传则保持全量返回，向后兼容）
	Invalid   bool // 参数非法（page<1 或 page_size 越界且不可回退）
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// parsePagination 解析 ?page= 与 ?page_size= 查询参数。
func parsePagination(r *http.Request) paginationParams {
	pageRaw := strings.TrimSpace(r.URL.Query().Get("page"))
	sizeRaw := strings.TrimSpace(r.URL.Query().Get("page_size"))
	p := paginationParams{Page: 1, PageSize: defaultPageSize}
	if pageRaw == "" && sizeRaw == "" {
		return p
	}
	p.Requested = true
	if pageRaw != "" {
		n, err := strconv.Atoi(pageRaw)
		if err != nil || n < 1 {
			p.Invalid = true
			return p
		}
		p.Page = n
	}
	if sizeRaw != "" {
		n, err := strconv.Atoi(sizeRaw)
		if err != nil || n < 1 || n > maxPageSize {
			p.Invalid = true
			return p
		}
		p.PageSize = n
	}
	return p
}

// paginate 对已排序的切片做分页切片，返回当前页内容。
func paginate[T any](items []T, p paginationParams) []T {
	if !p.Requested {
		return items
	}
	start := (p.Page - 1) * p.PageSize
	if start >= len(items) {
		return []T{}
	}
	end := start + p.PageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// pagedEnvelope 构造分页响应 Data（企业级列表契约）。
func pagedEnvelope(items interface{}, total, page, pageSize int) map[string]interface{} {
	return map[string]interface{}{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}

// parseDryRun 解析 DryRun 标志（企业级 API 契约，类比 AWS DryRun 参数）：
// 请求体 {"dry_run": true} 或查询参数 ?dry_run=true 任一命中即生效。
// fields 是请求体的原始字段 map（可能为 nil，容忍）。
func parseDryRun(fields map[string]json.RawMessage, r *http.Request) bool {
	if v := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("dry_run"))); v == "true" || v == "1" {
		return true
	}
	if fields == nil {
		return false
	}
	raw, ok := fields["dry_run"]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil && b {
		return true
	}
	// 容忍字符串形式 "true"
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && strings.EqualFold(strings.TrimSpace(s), "true") {
		return true
	}
	return false
}

// clientIP 返回当前请求的真实客户端 IP。
//
// 优先级：
//  1. 直接对端地址（r.RemoteAddr）。
//  2. 仅当直接对端落在 PanelAccessPolicy.TrustedProxies 范围内时，
//     才信任 X-Forwarded-For / X-Real-IP / CF-Connecting-IP 头里的值，
//     沿信任链回溯到第一个不受信任的代理地址。
//
// 未配置 TrustedProxies 或直接对端不在白名单内时，一律退回 RemoteAddr，
// 避免被攻击者伪造 X-Forwarded-For 绕过 IP 白名单 / 登录限流 / 审计。
func clientIP(r *http.Request) string {
	direct := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(direct); err == nil {
		direct = host
	}
	direct = strings.TrimPrefix(strings.TrimSuffix(direct, "]"), "[")

	policy := config.SnapshotPanelAccessPolicy()
	if !policy.Enabled && len(policy.TrustedProxies) == 0 {
		return direct
	}

	if forwarded, ok := config.ResolveForwardedIPForRequest(direct, r); ok {
		return forwarded
	}
	return direct
}
