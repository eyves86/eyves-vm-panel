package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"eyvescloud/internal/config"
)

func TestReconcileImageCatalog(t *testing.T) {
	prev := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prev })

	config.AppConfig = &config.EyvescloudConfig{
		CustomLXCImages: []config.CustomLXCImage{
			{ID: "old-lxc", Name: "Old", SHA256: "aaaa"},
		},
		CustomKVMImages: []config.CustomKVMImage{
			{ID: "keep-kvm", Name: "Keep", SHA256: "cccc"},
		},
	}

	incoming := agentImageCatalog{
		LXC: []config.CustomLXCImage{
			{ID: "old-lxc", Name: "Old", SHA256: "bbbb"},   // 已存在且 hash 变化 -> update
			{ID: "new-lxc", Name: "New", SHA256: "dddd"},   // 不存在 -> add
			{ID: "same-lxc", Name: "Same", SHA256: "eeee"}, // 不存在 -> add
		},
		KVM: []config.CustomKVMImage{
			{ID: "keep-kvm", Name: "Keep", SHA256: "cccc"}, // hash 相同 -> no change
			{ID: "new-kvm", Name: "NewKvm", SHA256: "ffff"}, // 不存在 -> add
		},
	}

	added, updated := reconcileImageCatalog(incoming)
	if added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	// 空 hash 不触发 update（未知哈希视为无需更新）。
	incoming2 := agentImageCatalog{
		LXC: []config.CustomLXCImage{{ID: "old-lxc", Name: "Old", SHA256: ""}},
	}
	added2, updated2 := reconcileImageCatalog(incoming2)
	if added2 != 0 || updated2 != 0 {
		t.Fatalf("empty-hash catalog: added=%d updated=%d, want 0/0", added2, updated2)
	}
}

func TestAgentImageSyncHandlerRejectsNonPost(t *testing.T) {
	prev := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prev })
	config.AppConfig = &config.EyvescloudConfig{}

	req := httptest.NewRequest("GET", "/api/agent/images/sync", nil)
	rec := httptest.NewRecorder()
	HandleAgentImageSync(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestDownloadImageFileRejectsSSRF guarantees the agent-side image sync honors
// the same SSRF guard as the rest of the product: loopback / link-local /
// metadata / reserved destinations must be rejected before any network is
// attempted, so a compromised controller cannot turn an agent into a scanner.
func TestDownloadImageFileRejectsSSRF(t *testing.T) {
	cases := []struct{ name, url string }{
		{"loopback", "http://127.0.0.1:6379/foo"},
		{"localhost", "http://localhost/image.tar"},
		{"link-local", "http://169.254.169.254/latest/meta-data"},
		{"reserved", "http://240.0.0.1/x"},
		{"ftp-scheme", "ftp://ftp.example.com/x.iso"},
		{"creds", "http://user:pass@1.2.3.4/x"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := downloadImageFile(tc.url, filepath.Join(t.TempDir(), "img"))
			if err == nil {
				t.Fatalf("downloadImageFile(%q) = nil error, want SSRF rejection", tc.url)
			}
		})
	}
}