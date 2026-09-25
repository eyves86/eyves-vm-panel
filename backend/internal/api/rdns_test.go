package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// setupRDNSTestStore 初始化一个临时 SQLite 配置库并写入测试容器，
// 以便 MutateGlobal / SaveConfig 能真正落库（否则返回 "database is not initialized"）。
func setupRDNSTestStore(t *testing.T, container config.Container) {
	t.Helper()
	dir := t.TempDir()
	previous := config.AppConfig
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	os.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.AppConfig = previous
		config.SetConfigPath("")
		os.Unsetenv("EYVESCLOUD_DATA_DIR")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Containers = []config.Container{container}
	}); err != nil {
		t.Fatalf("写入测试容器失败: %v", err)
	}
}

func decodeRDNSResponse(t *testing.T, rec *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// TestUpdateReverseDNSSetsOwnedAddress 验证可为容器自己的公网 IP 设置 PTR 并落库。
func TestUpdateReverseDNSSetsOwnedAddress(t *testing.T) {
	setupRDNSTestStore(t, config.Container{
		ID:            1,
		UUID:          "uuid-rdns-1",
		Name:          "rdns-vm",
		Status:        "running",
		PublicIPv4s:   []config.PublicIPv4Assignment{{Address: "203.0.113.10"}},
		IPv6Addresses: []config.IPv6Assignment{{Address: "2001:db8::10"}},
	})

	body := strings.NewReader(`{"records":[{"address":"203.0.113.10","hostname":"mail.example.com"},{"address":"2001:db8::10","hostname":"v6.example.com"}]}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/1/rdns", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	c := config.FindContainer(1)
	if c == nil {
		t.Fatal("容器丢失")
	}
	if got := c.PublicIPv4s[0].RDNS; got != "mail.example.com" {
		t.Fatalf("IPv4 RDNS = %q, want mail.example.com", got)
	}
	if got := c.IPv6Addresses[0].RDNS; got != "v6.example.com" {
		t.Fatalf("IPv6 RDNS = %q, want v6.example.com", got)
	}

	// 关闭并从 SQLite 重新加载，确认 RDNS 真正落库（而非仅内存生效）。
	config.CloseConfigDB()
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("重新加载配置库失败: %v", err)
	}
	reloaded := config.FindContainer(1)
	if reloaded == nil {
		t.Fatal("重新加载后容器丢失")
	}
	if got := reloaded.PublicIPv4s[0].RDNS; got != "mail.example.com" {
		t.Fatalf("重载后 IPv4 RDNS = %q, want mail.example.com", got)
	}
	if got := reloaded.IPv6Addresses[0].RDNS; got != "v6.example.com" {
		t.Fatalf("重载后 IPv6 RDNS = %q, want v6.example.com", got)
	}
}

// TestUpdateReverseDNSRejectsUnownedAddress 验证无法为不属于该容器的地址设置 PTR。
func TestUpdateReverseDNSRejectsUnownedAddress(t *testing.T) {
	setupRDNSTestStore(t, config.Container{
		ID:          2,
		UUID:        "uuid-rdns-2",
		Name:        "rdns-vm-2",
		Status:      "running",
		PublicIPv4s: []config.PublicIPv4Assignment{{Address: "203.0.113.20"}},
	})

	body := strings.NewReader(`{"records":[{"address":"198.51.100.99","hostname":"evil.example.com"}]}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/2/rdns", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	c := config.FindContainer(2)
	if c.PublicIPv4s[0].RDNS != "" {
		t.Fatalf("越权地址不应写入 PTR，got %q", c.PublicIPv4s[0].RDNS)
	}
}

// TestUpdateReverseDNSRejectsInvalidHostname 验证非法主机名被拒。
func TestUpdateReverseDNSRejectsInvalidHostname(t *testing.T) {
	setupRDNSTestStore(t, config.Container{
		ID:          3,
		UUID:        "uuid-rdns-3",
		Name:        "rdns-vm-3",
		Status:      "running",
		PublicIPv4s: []config.PublicIPv4Assignment{{Address: "203.0.113.30"}},
	})

	body := strings.NewReader(`{"records":[{"address":"203.0.113.30","hostname":"not a hostname"}]}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/3/rdns", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestGetReverseDNSListsRecords 验证查询返回容器所有公网 IP（含空 PTR）。
func TestGetReverseDNSListsRecords(t *testing.T) {
	setupRDNSTestStore(t, config.Container{
		ID:            4,
		UUID:          "uuid-rdns-4",
		Name:          "rdns-vm-4",
		Status:        "running",
		PublicIPv4s:   []config.PublicIPv4Assignment{{Address: "203.0.113.40", RDNS: "a.example.com"}},
		IPv6Addresses: []config.IPv6Assignment{{Address: "2001:db8::40"}},
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/4/rdns", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp := decodeRDNSResponse(t, rec)
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data 类型异常: %T", resp.Data)
	}
	records, ok := data["records"].([]interface{})
	if !ok || len(records) != 2 {
		t.Fatalf("records 异常: %#v", data["records"])
	}
	first := records[0].(map[string]interface{})
	if first["address"] != "203.0.113.40" || first["family"] != "ipv4" || first["hostname"] != "a.example.com" {
		t.Fatalf("首条记录异常: %#v", first)
	}
	second := records[1].(map[string]interface{})
	if second["family"] != "ipv6" || second["hostname"] != "" {
		t.Fatalf("IPv6 记录异常: %#v", second)
	}
}

// TestValidateRDNSHostname 覆盖 FQDN 校验的合法与非法输入。
func TestValidateRDNSHostname(t *testing.T) {
	valid := []string{"mail.example.com", "a-b.example.co.uk", "host1.example.com.", "x.y"}
	for _, host := range valid {
		if err := validateRDNSHostname(host); err != nil {
			t.Fatalf("%q 应通过校验: %v", host, err)
		}
	}
	invalid := []string{"", "localhost", "not a hostname", "-bad.example.com", "bad-.example.com", "bad_underscore.example.com", strings.Repeat("a", 64) + ".com"}
	for _, host := range invalid {
		if err := validateRDNSHostname(host); err == nil {
			t.Fatalf("%q 应被拒绝", host)
		}
	}
}
