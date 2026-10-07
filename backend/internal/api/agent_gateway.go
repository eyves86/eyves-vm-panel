package api

import "net/http"

// agent_gateway.go —— 被控节点入站专用网关（P2 剩余：独立 Agent 网关 / 入站削峰）。
//
// 背景：30k 节点 × 10s 心跳 = 约 3000 次/秒的入站请求。改版前这些请求与浏览器/
// 管理 API 共用同一个监听器与连接池，节点侧的连接洪峰（或某个节点异常重连风暴）
// 会挤占面板会话的连接与 goroutine，表现为「面板变慢/超时」。
//
// 设计取舍：
//   - 本网关把**节点 → 主控**的上报接口单独暴露在一个可选监听器上，让入站洪峰与
//     面板 API 在连接层就隔离；运维也可以把它绑到内网网卡/独立端口，只对节点网段开放。
//   - 网关**只**暴露心跳（节点 token 认证的入站上报），不含任何管理员/浏览器接口——
//     即使误绑公网端口，暴露面也只有带 token 校验的入站上报。
//   - 默认不启用（env 为空）：节点心跳仍走主监听器，行为与旧版完全一致。
//
// 注意：网关仍是同一进程、同一个 SQLite，落库走既有的延后批处理（config/store_batch.go）。
// 它解决的是「接入层隔离」，不是「写库隔离」——后者由 P1-c 遥测分库与 P3 cell 分库承担。

// AgentGatewayMux 返回「被控节点入站」专用复用器，仅供独立网关监听器使用。
func AgentGatewayMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes/", agentIngestSubroute)
	mux.HandleFunc("/api/agent/gateway/health", handleAgentGatewayHealth)
	return mux
}

// agentIngestSubroute 只放行节点子路由中的 heartbeat，其余一律 404。
// 这样网关不会成为管理员接口（/api/nodes/{id}/containers 等）的第二入口。
func agentIngestSubroute(w http.ResponseWriter, r *http.Request) {
	nodeID, rest, ok := splitNodeSubPath(r.URL.Path)
	if !ok || rest != "heartbeat" {
		http.NotFound(w, r)
		return
	}
	// 心跳 handler 自身用节点 token 做常量时间校验并负责 method 判定，
	// 这里不重复鉴权，保持与主监听器完全一致的语义。
	handleNodeHeartbeat(w, r, nodeID)
}

// handleAgentGatewayHealth 是网关的无状态存活探针，供 LB / systemd 探活。
// 不读库、不鉴权，也不泄露任何面板信息。
func handleAgentGatewayHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "agent gateway ok"})
}
