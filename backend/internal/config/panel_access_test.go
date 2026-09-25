package config

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizePanelAccessPolicy(t *testing.T) {
	policy, err := NormalizePanelAccessPolicy(PanelAccessPolicy{
		Enabled:        true,
		AllowedSources: []string{" 192.0.2.8 ", "10.20.30.44/24", "192.0.2.8", "2001:db8::1"},
		TrustedProxies: []string{"127.0.0.1", "2001:db8:1::/64"},
	})
	if err != nil {
		t.Fatalf("NormalizePanelAccessPolicy() error = %v", err)
	}
	if want := []string{"192.0.2.8", "10.20.30.0/24", "2001:db8::1"}; !reflect.DeepEqual(policy.AllowedSources, want) {
		t.Fatalf("AllowedSources = %#v, want %#v", policy.AllowedSources, want)
	}
	if want := []string{"127.0.0.1", "2001:db8:1::/64"}; !reflect.DeepEqual(policy.TrustedProxies, want) {
		t.Fatalf("TrustedProxies = %#v, want %#v", policy.TrustedProxies, want)
	}
}

func TestNormalizePanelAccessPolicyRejectsEmptyEnabledPolicy(t *testing.T) {
	if _, err := NormalizePanelAccessPolicy(PanelAccessPolicy{Enabled: true}); err == nil {
		t.Fatal("expected enabled empty policy to fail")
	}
}

func TestEvaluatePanelAccess(t *testing.T) {
	base := PanelAccessPolicy{
		Enabled:        true,
		AllowedSources: []string{"192.0.2.0/24", "2001:db8::/32"},
		TrustedProxies: []string{"10.0.0.1", "127.0.0.1"},
	}
	tests := []struct {
		name          string
		policy        PanelAccessPolicy
		remote        string
		headers       ForwardedClientHeaders
		allowed       bool
		current       string
		usedForwarded bool
	}{
		{
			name:    "disabled",
			policy:  PanelAccessPolicy{},
			remote:  "198.51.100.9:44321",
			allowed: true,
			current: "198.51.100.9",
		},
		{
			name:    "direct CIDR match",
			policy:  base,
			remote:  "192.0.2.25:44321",
			allowed: true,
			current: "192.0.2.25",
		},
		{
			name:    "direct denied",
			policy:  base,
			remote:  "198.51.100.9:44321",
			allowed: false,
			current: "198.51.100.9",
		},
		{
			name:   "spoofed forwarding header ignored",
			policy: base,
			remote: "198.51.100.9:44321",
			headers: ForwardedClientHeaders{
				ForwardedFor: "192.0.2.10",
			},
			allowed: false,
			current: "198.51.100.9",
		},
		{
			name:   "trusted proxy forwards allowed source",
			policy: base,
			remote: "10.0.0.1:44321",
			headers: ForwardedClientHeaders{
				ForwardedFor: "192.0.2.10",
			},
			allowed:       true,
			current:       "192.0.2.10",
			usedForwarded: true,
		},
		{
			name:   "trusted proxy forwards denied source",
			policy: base,
			remote: "10.0.0.1:44321",
			headers: ForwardedClientHeaders{
				RealIP: "198.51.100.20",
			},
			allowed:       false,
			current:       "198.51.100.20",
			usedForwarded: true,
		},
		{
			name:          "direct loopback recovery",
			policy:        base,
			remote:        "127.0.0.1:44321",
			allowed:       true,
			current:       "127.0.0.1",
			usedForwarded: false,
		},
		{
			name:   "trusted loopback proxy is enforced",
			policy: base,
			remote: "127.0.0.1:44321",
			headers: ForwardedClientHeaders{
				ForwardedFor: "198.51.100.20",
			},
			allowed:       false,
			current:       "198.51.100.20",
			usedForwarded: true,
		},
		{
			name:    "IPv6 source",
			policy:  base,
			remote:  "[2001:db8::88]:44321",
			allowed: true,
			current: "2001:db8::88",
		},
		{
			name:   "trusted proxy chain",
			policy: base,
			remote: "10.0.0.1:44321",
			headers: ForwardedClientHeaders{
				ForwardedFor: "192.0.2.70, 10.0.0.1",
			},
			allowed:       true,
			current:       "192.0.2.70",
			usedForwarded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluatePanelAccess(tt.policy, tt.remote, tt.headers)
			if got.Allowed != tt.allowed || got.CurrentSource != tt.current || got.UsedForwarded != tt.usedForwarded {
				t.Fatalf("EvaluatePanelAccess() = %#v", got)
			}
		})
	}
}

func TestResolveForwardedIPWithPolicy(t *testing.T) {
	policy := PanelAccessPolicy{
		TrustedProxies: []string{"10.0.0.1", "10.0.0.2", "127.0.0.1", "2001:db8:1::/64"},
	}
	newReq := func(remoteAddr, xff, realIP, cfIP string) *http.Request {
		h := http.Header{}
		if xff != "" {
			h.Set("X-Forwarded-For", xff)
		}
		if realIP != "" {
			h.Set("X-Real-IP", realIP)
		}
		if cfIP != "" {
			h.Set("CF-Connecting-IP", cfIP)
		}
		return &http.Request{RemoteAddr: remoteAddr, Header: h}
	}
	tests := []struct {
		name      string
		policy    PanelAccessPolicy
		direct    string
		req       *http.Request
		want      string
		wantFound bool
	}{
		{
			name:      "direct peer not trusted spoofs X-Forwarded-For",
			policy:    policy,
			direct:    "198.51.100.9:54321",
			req:       newReq("198.51.100.9:54321", "192.0.2.10", "", ""),
			wantFound: false,
		},
		{
			name:      "no trusted proxies configured",
			policy:    PanelAccessPolicy{},
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "192.0.2.10", "", ""),
			wantFound: false,
		},
		{
			name:      "trusted peer with single X-Forwarded-For",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "192.0.2.10", "", ""),
			want:      "192.0.2.10",
			wantFound: true,
		},
		{
			name:      "trusted peer falls back to X-Real-IP",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "", "192.0.2.20", ""),
			want:      "192.0.2.20",
			wantFound: true,
		},
		{
			name:      "trusted peer falls back to CF-Connecting-IP before X-Forwarded-For",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "", "", "192.0.2.30"),
			want:      "192.0.2.30",
			wantFound: true,
		},
		{
			// 代理链: Client(192.0.2.8) → Proxy-A(10.0.0.2, trusted) → Proxy-B(10.0.0.1, trusted) → 应用
			// 应用直接对端是 Proxy-B(10.0.0.1)，XFF 链是 "192.0.2.8, 10.0.0.2"
			// 应回溯到 192.0.2.8（第一个不受信任的）。
			name:      "multi-hop trusted chain resolves to first untrusted",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "192.0.2.8, 10.0.0.2", "", ""),
			want:      "192.0.2.8",
			wantFound: true,
		},
		{
			// 链中有一个不受信任的代理（198.51.100.50），应停在它后面那个
			name:      "chain with mixed trusted and untrusted middle proxy",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "192.0.2.8, 198.51.100.50, 10.0.0.2", "", ""),
			want:      "198.51.100.50",
			wantFound: true,
		},
		{
			// 整个链都是可信的 → 返回最左端 XFF 条目（parts[0]，即声称的原始客户端）
			name:      "entire chain trusted returns leftmost XFF entry",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "10.0.0.2, 127.0.0.1", "", ""),
			want:      "10.0.0.2",
			wantFound: true,
		},
		{
			name:      "IPv6 trusted peer forwards IPv6 client",
			policy:    policy,
			direct:    "[2001:db8:1::1]:54321",
			req:       newReq("[2001:db8:1::1]:54321", "2001:db8::dead:beef", "", ""),
			want:      "2001:db8::dead:beef",
			wantFound: true,
		},
		{
			name:      "IPv6 trusted chain",
			policy:    policy,
			direct:    "[2001:db8:1::2]:54321",
			req:       newReq("[2001:db8:1::2]:54321", "2001:db8::cafe, 2001:db8:1::1", "", ""),
			want:      "2001:db8::cafe",
			wantFound: true,
		},
		{
			// XFF 值中有非 IP 垃圾 → 跳过，取它之前那个有效 IP
			name:      "malformed XFF entries are skipped",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "not-an-ip, 192.0.2.9, , 10.0.0.2", "", ""),
			want:      "192.0.2.9",
			wantFound: true,
		},
		{
			// 超长 XFF（超过 256 字符 + 全是垃圾 IP）→ 找不到有效转发，返回 found=false
			name:      "truncated or all-garbage XFF yields no result",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", strings.Repeat("zzz, ", 60)+"bogus", "", ""),
			wantFound: false,
		},
		{
			name:      "no forwarded headers at all",
			policy:    policy,
			direct:    "10.0.0.1:54321",
			req:       newReq("10.0.0.1:54321", "", "", ""),
			wantFound: false,
		},
		{
			// 畸形 remoteAddr
			name:      "unparseable remoteAddr yields found=false",
			policy:    policy,
			direct:    "garbage",
			req:       newReq("garbage", "192.0.2.8", "", ""),
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ResolveForwardedIPWithPolicy(tt.direct, tt.policy, tt.req)
			if found != tt.wantFound {
				t.Fatalf("ResolveForwardedIPWithPolicy() found = %v, want %v (got addr=%q)", found, tt.wantFound, got)
			}
			if tt.wantFound && got != tt.want {
				t.Fatalf("ResolveForwardedIPWithPolicy() addr = %q, want %q", got, tt.want)
			}
		})
	}
}
