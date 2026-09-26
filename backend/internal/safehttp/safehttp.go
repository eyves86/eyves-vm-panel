package safehttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRedirects = 10

var blockedDownloadPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// ipResolver 抽象 DNS 解析器，使测试可以注入"先公网后受限地址"的假解析器
// 来模拟 DNS rebinding。生产路径一律传 net.DefaultResolver。
type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ValidateURL performs the URL checks that do not require DNS. Host addresses
// are checked again after resolution and immediately before every connection.
func ValidateURL(rawURL string) (*url.URL, error) {
	return validateURL(rawURL, false)
}

func validateURL(rawURL string, allowLoopback bool) (*url.URL, error) {
	if len(rawURL) == 0 || len(rawURL) > 4096 {
		return nil, fmt.Errorf("download URL must be between 1 and 4096 characters")
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid download URL: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("download URL must use HTTP or HTTPS")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("download URL must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("download URL must not include credentials")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("download URL must not include a fragment")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("download URL contains an invalid port")
		}
	}
	if addr, err := netip.ParseAddr(parsed.Hostname()); err == nil && !allowedAddress(addr, allowLoopback) {
		return nil, fmt.Errorf("download URL resolves to a blocked address")
	}
	return parsed, nil
}

// Get retrieves a resource only when every resolved destination is safe for
// image downloads. Private network image mirrors are allowed; loopback,
// link-local, metadata, multicast and reserved destinations remain blocked.
func Get(ctx context.Context, rawURL, userAgent string, timeout time.Duration) (*http.Response, error) {
	return doRequest(ctx, http.MethodGet, rawURL, userAgent, nil, nil, timeout, net.DefaultResolver, false)
}

// PostConfig tune Post 的地址策略。
type PostConfig struct {
	// AllowLoopback 允许 127.0.0.0/8 与 ::1 目标（自托管接收端的设计取舍，
	// 如指向本机服务的 webhook）。链路本地（含云元数据 169.254.169.254）、
	// 组播与保留段无论该开关如何都被拒绝。
	AllowLoopback bool
}

// Post 向外部端点投递一次 POST 请求，SSRF 防护与 Get 同口径：
// URL 语法与字面地址校验、DNS 解析期校验、拨号期二次校验（覆盖 DNS
// rebinding——校验时解析公网地址、连接时解析到受限地址的场景）、
// 重定向逐跳校验。webhook/通知投递必须走本入口，禁止直接用 http.Client。
func Post(ctx context.Context, rawURL string, headers map[string]string, body []byte, timeout time.Duration, cfg PostConfig) (*http.Response, error) {
	return doRequest(ctx, http.MethodPost, rawURL, "", headers, bytes.NewReader(body), timeout, net.DefaultResolver, cfg.AllowLoopback)
}

// doRequest 是 Get/Post 的共享实现。resolver 与 allowLoopback 作为参数注入，
// 便于测试模拟 DNS rebinding 与回环策略。
func doRequest(ctx context.Context, method, rawURL, userAgent string, headers map[string]string, body io.Reader, timeout time.Duration, resolver ipResolver, allowLoopback bool) (*http.Response, error) {
	parsed, err := validateURL(rawURL, allowLoopback)
	if err != nil {
		return nil, err
	}
	if err := validateHost(ctx, resolver, parsed.Hostname(), allowLoopback); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: restrictedTransport(resolver, allowLoopback),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			redirect, err := validateURL(req.URL.String(), allowLoopback)
			if err != nil {
				return err
			}
			if err := validateHost(req.Context(), resolver, redirect.Hostname(), allowLoopback); err != nil {
				return err
			}
			if len(via) > 0 {
				if ua := via[0].Header.Get("User-Agent"); ua != "" {
					req.Header.Set("User-Agent", ua)
				}
			}
			return nil
		},
	}

	// The URL, redirects, DNS answers and dial destinations are constrained
	// above and in restrictedTransport. CodeQL cannot infer those checks across
	// the custom transport boundary.
	// codeql[go/request-forgery]
	return client.Do(request)
}

func restrictedTransport(resolver ipResolver, allowLoopback bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid download destination: %v", err)
			}
			addresses, err := resolveAllowedHost(ctx, resolver, host, allowLoopback)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, addr := range addresses {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("host has no usable public addresses")
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 30 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
}

func validateHost(ctx context.Context, resolver ipResolver, host string, allowLoopback bool) error {
	_, err := resolveAllowedHost(ctx, resolver, host, allowLoopback)
	return err
}

func resolveAllowedHost(ctx context.Context, resolver ipResolver, host string, allowLoopback bool) ([]netip.Addr, error) {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if host == "" {
		return nil, fmt.Errorf("download URL host is empty")
	}
	// 回环豁免只针对自托管接收端（webhook/通知投递）：localhost 主机名在
	// AllowLoopback 下放行；镜像下载等其余场景一律拒绝。
	if !allowLoopback && (strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost")) {
		return nil, fmt.Errorf("download URL host is not public")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !allowedAddress(addr, allowLoopback) {
			return nil, fmt.Errorf("download URL resolves to a blocked address")
		}
		return []netip.Addr{addr}, nil
	}

	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve download host: %v", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("download host has no IP addresses")
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !allowedAddress(address, allowLoopback) {
			return nil, fmt.Errorf("download host resolves to a blocked address")
		}
		result = append(result, address)
	}
	return result, nil
}

// allowedAddress 在 isAllowedDownloadAddress 之上支持回环豁免（仅自托管
// 接收端场景）。链路本地/元数据/组播/保留段的拒绝不受豁免影响。
func allowedAddress(address netip.Addr, allowLoopback bool) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	if allowLoopback && address.IsLoopback() {
		return true
	}
	return isAllowedDownloadAddress(address)
}

func isAllowedDownloadAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" || !address.IsGlobalUnicast() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	address = address.Unmap()
	for _, prefix := range blockedDownloadPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
