package api

// access_endpoint.go —— 容器"客户接入端点"的统一计算（安全设计）。
//
// 设计原则（WHMCS/计费系统对接）：
//   - 面板是接入地址的唯一权威来源：容器在哪个节点，端点就指向哪个节点。
//     NAT 端口映射挂在**节点**上，主控只是控制面，不能拿主控地址冒充端点。
//   - 计费系统（WHMCS 模块）只消费面板返回的 access_host/access_ssh_port，
//     不自行推断，避免"插件猜地址"导致的错连。
//   - 节点可用 public_host 显式指定客户接入地址（面板管理网与业务网分离的场景）；
//     未指定时从节点 Address 的 host 推导。
//   - 凭据（SSH 密码）按需从被控拉取，不在主控落库（见 enrichNodeContainerDetail）。

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"eyvescloud/internal/config"
)

// containerAccessEndpoint 返回容器对客户可见的接入主机与 SSH 端口。
//   via = "node"  容器在被控节点：接入地址是该节点
//   via = "panel" 容器在主控本机：接入地址是面板对外地址
func containerAccessEndpoint(r *http.Request, c *config.Container) (host string, sshPort int, via string) {
	sshPort = c.SSHPort
	if c.NodeID != "" {
		if node, ok := config.FindNode(c.NodeID); ok {
			if h := normalizeAccessHost(node.PublicHost); h != "" {
				return h, sshPort, "node"
			}
			if h := hostFromURL(node.Address); h != "" {
				return h, sshPort, "node"
			}
		}
		// 节点信息缺失：仍标记 node，交由调用方降级展示。
		return "", sshPort, "node"
	}
	config.AppConfigMu.RLock()
	panelDomain := strings.TrimSpace(config.AppConfig.PanelDomain)
	config.AppConfigMu.RUnlock()
	if h := hostFromURL(panelDomain); h != "" {
		return h, sshPort, "panel"
	}
	if r != nil {
		if h := requestHostOnly(r); h != "" {
			return h, sshPort, "panel"
		}
	}
	return "", sshPort, "panel"
}

func normalizeAccessHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return hostFromURL(raw)
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return strings.Trim(raw, "[]")
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		return normalizeAccessHost(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func requestHostOnly(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}
