package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyReleaseArchiveEnforced 验证升级包完整性校验（审计 H-2）：
//   - 校验清单缺失 → 默认拒绝（fail closed）；
//   - 明文豁免开关 → 放行；
//   - 哈希匹配 → 通过；哈希不匹配 → 拒绝；
//   - 标准/BSD 两种校验清单格式均可解析。
func TestVerifyReleaseArchiveEnforced(t *testing.T) {
	const assetName = "eyvescloud-linux-amd64.tar.gz"
	payload := []byte("fake release archive content")
	dir := t.TempDir()
	archive := filepath.Join(dir, assetName)
	if err := os.WriteFile(archive, payload, 0600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	serve := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
	}

	t.Run("missing checksums is refused by default", func(t *testing.T) {
		t.Setenv(updateAllowUnverifiedEnv, "")
		if err := verifyReleaseArchive(archive, assetName, ""); err == nil {
			t.Fatal("expected refusal when no checksums asset exists")
		}
	})

	t.Run("explicit opt-out allows unverified installs", func(t *testing.T) {
		t.Setenv(updateAllowUnverifiedEnv, "1")
		if err := verifyReleaseArchive(archive, assetName, ""); err != nil {
			t.Fatalf("opt-out should allow the upgrade: %v", err)
		}
	})

	t.Run("matching checksum passes (gnu format)", func(t *testing.T) {
		srv := serve(hexSum + "  " + assetName + "\n" + strings.Repeat("a", 64) + "  other.tar.gz\n")
		defer srv.Close()
		if err := verifyReleaseArchive(archive, assetName, srv.URL); err != nil {
			t.Fatalf("matching checksum rejected: %v", err)
		}
	})

	t.Run("mismatching checksum aborts", func(t *testing.T) {
		srv := serve(strings.Repeat("b", 64) + "  " + assetName + "\n")
		defer srv.Close()
		if err := verifyReleaseArchive(archive, assetName, srv.URL); err == nil {
			t.Fatal("expected checksum mismatch to abort the upgrade")
		}
	})

	t.Run("bsd format is parsed", func(t *testing.T) {
		srv := serve("SHA256 (" + assetName + ") = " + hexSum + "\n")
		defer srv.Close()
		if err := verifyReleaseArchive(archive, assetName, srv.URL); err != nil {
			t.Fatalf("bsd-format checksum rejected: %v", err)
		}
	})

	t.Run("asset missing from checksum file aborts", func(t *testing.T) {
		srv := serve(strings.Repeat("c", 64) + "  unrelated.tar.gz\n")
		defer srv.Close()
		if err := verifyReleaseArchive(archive, assetName, srv.URL); err == nil {
			t.Fatal("expected missing entry to abort the upgrade")
		}
	})

	t.Run("path-prefixed filenames match by basename", func(t *testing.T) {
		srv := serve(hexSum + "  dist/" + assetName + "\n")
		defer srv.Close()
		if err := verifyReleaseArchive(archive, assetName, srv.URL); err != nil {
			t.Fatalf("basename matching failed: %v", err)
		}
	})
}

// TestFindChecksumsURL 验证汇总清单与单文件校验值两种发布习惯都能被识别。
func TestFindChecksumsURL(t *testing.T) {
	release := &githubRelease{}
	release.Assets = append(release.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{Name: "eyvescloud-linux-amd64.tar.gz.sha256", BrowserDownloadURL: "https://example.com/per-asset"})
	if got := findChecksumsURL(release, "eyvescloud-linux-amd64.tar.gz"); got != "https://example.com/per-asset" {
		t.Fatalf("per-asset checksum not found, got %q", got)
	}

	release2 := &githubRelease{}
	release2.Assets = append(release2.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{Name: "SHA256SUMS", BrowserDownloadURL: "https://example.com/sums"})
	if got := findChecksumsURL(release2, "eyvescloud-linux-arm64.tar.gz"); got != "https://example.com/sums" {
		t.Fatalf("aggregate checksums asset not found, got %q", got)
	}

	if got := findChecksumsURL(nil, "x"); got != "" {
		t.Fatalf("nil release must yield empty URL, got %q", got)
	}
}

// TestLatestTagFromPathMultiPlatform GitLab 的 permalink 重定向路径也要能解析 tag。
func TestLatestTagFromPathMultiPlatform(t *testing.T) {
	cases := map[string]string{
		"/owner/repo/releases/tag/v1.2.3":                    "v1.2.3",
		"/owner/repo/releases/tag/v1.2.3/":                   "v1.2.3",
		"/group/repo/-/releases/v2.0.2":                      "v2.0.2",
		"/group/repo/-/releases/permalink/latest":            "",
		"/owner/repo/releases/latest":                        "",
	}
	for path, want := range cases {
		if got := latestTagFromPath(path); got != want {
			t.Fatalf("latestTagFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
