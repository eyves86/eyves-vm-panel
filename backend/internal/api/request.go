package api

import (
	"net"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

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
