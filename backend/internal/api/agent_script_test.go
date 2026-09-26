package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestBuildAgentInstallScriptBashSyntax 确保生成的一键安装脚本始终是合法 bash。
// 脚本由 fmt.Sprintf 拼接，任何占位符/转义错误都可能产生语法损坏，此测试做回归防线。
func TestBuildAgentInstallScriptBashSyntax(t *testing.T) {
	cases := []struct {
		name, controller, key, nodeName, addr string
	}{
		{"default", "", "abc123", "node-1", ""},
		{"with-controller", "https://4.4.4.4:8999", "abc123", "node-1", "http://10.0.0.2:9000"},
		{"http-controller", "http://4.4.4.4:8999", "k", "n", ""},
		{"special-chars-in-name", "", "abc123", "node 'quoted'", "http://[fd00::2]:9000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := buildAgentInstallScript(tc.controller, tc.key, tc.nodeName, tc.addr)
			cmd := exec.Command("bash", "-n")
			cmd.Stdin = strings.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bash -n failed: %v\noutput: %s\nscript:\n%s", err, out, script)
			}
			// 关键内容断言：key/名称必须被安全转义后嵌入
			if !strings.Contains(script, tc.key) {
				t.Fatalf("script missing install key")
			}
		})
	}
}

// TestBuildAgentInstallScriptDetectLogic 验证主控地址探测的关键分支存在且正确。
func TestBuildAgentInstallScriptDetectLogic(t *testing.T) {
	script := buildAgentInstallScript("", "abc123", "node-1", "")
	// IPv6 提取必须按 src 字段定位（固定 $7 会错位拿到 dev 名）
	if !strings.Contains(script, `if($i=="src")`) {
		t.Fatal("route src extraction missing")
	}
	// SSH_CONNECTION 的 IPv6 客户端必须加方括号
	if !strings.Contains(script, `detected_ip="[$ssh_ip]"`) {
		t.Fatal("SSH IPv6 bracket wrapping missing")
	}
	// systemd unit 参数必须加双引号（防空格拆分）
	if !strings.Contains(script, `--controller="${CONTROLLER}"`) {
		t.Fatal("systemd ExecStart quoting missing")
	}
	// 无效 sed 替换不允许再出现
	if strings.Contains(script, `sed -i "s|\${CONTROLLER}`) {
		t.Fatal("stale no-op sed replacement present")
	}
}

// TestBuildAgentInstallScriptSHA256Verification 确保安装脚本对下载的
// agent 二进制执行 SHA256 完整性校验（G4 / P1-7），而不是仅靠 --version 自检。
func TestBuildAgentInstallScriptSHA256Verification(t *testing.T) {
	script := buildAgentInstallScript("", "abc123", "node-1", "")
	// 必须从响应头提取主控下发的摘要（tolower 匹配，mawk/gawk 均可移植）
	if !strings.Contains(script, `tolower($1)=="x-binary-sha256:"`) {
		t.Fatal("X-Binary-SHA256 header extraction missing")
	}
	// 必须对落盘文件做 sha256sum 计算
	if !strings.Contains(script, `sha256sum /usr/local/bin/eyvescloud`) {
		t.Fatal("binary sha256sum verification missing")
	}
	// 校验失败必须删除已下载产物并中止
	if !strings.Contains(script, "二进制 SHA256 校验失败") {
		t.Fatal("mismatch error handling missing")
	}
}

// TestExecutableSHA256 验证二进制哈希计算正确且缓存命中路径返回一致结果。
func TestExecutableSHA256(t *testing.T) {
	tmp := t.TempDir()
	bin := tmp + "/fake-agent"
	content := []byte("fake binary payload \x00\x01\x02")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	// 首次计算（缓存未命中）
	got, err := executableSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("executableSHA256 = %s, want %s", got, want)
	}
	// 二次计算（缓存命中，结果必须一致）
	got2, err := executableSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != want {
		t.Fatalf("cached executableSHA256 = %s, want %s", got2, want)
	}
	// 文件变更后缓存必须失效
	if err := os.WriteFile(bin, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	got3, err := executableSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum3 := sha256.Sum256([]byte("changed"))
	if got3 != hex.EncodeToString(sum3[:]) {
		t.Fatalf("executableSHA256 after mtime change = %s, want %s", got3, hex.EncodeToString(sum3[:]))
	}
}

// TestHandleNodeBinarySHA256Header 验证二进制分发端点附带 X-Binary-SHA256
// 响应头（G4 / P1-7），且值与 executableSHA256 一致；无效 key 仍被拒绝。
func TestHandleNodeBinarySHA256Header(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{Nodes: []config.Node{{
		ID: "node-1", Name: "node-1", InstallKey: "test-install-key",
	}}}

	// 有效 install key → 200 + 哈希头
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/nodes/binary", nil)
	r.Header.Set("X-Install-Key", "test-install-key")
	HandleNodeBinary(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := executableSHA256(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Header().Get("X-Binary-Sha256"); got != want {
		t.Fatalf("X-Binary-SHA256 = %s, want %s", got, want)
	}

	// 无效 install key → 401
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/nodes/binary", nil)
	r2.Header.Set("X-Install-Key", "wrong-key")
	HandleNodeBinary(w2, r2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status = %d, want 401", w2.Code)
	}
}

// TestNormalizeNodeAddressDefaultsHTTPS 回归 F9：无 scheme 的节点地址默认补
// https://（与 agent→主控方向的强制 https 对齐，防 Bearer token 明文传输）；
// 显式 scheme 原样保留；尾斜杠剔除。
func TestNormalizeNodeAddressDefaultsHTTPS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"10.0.0.2:8999", "https://10.0.0.2:8999"},
		{"node.example.com", "https://node.example.com"},
		{"http://10.0.0.2:8999", "http://10.0.0.2:8999"},
		{"https://10.0.0.2:8999", "https://10.0.0.2:8999"},
		{"https://10.0.0.2:8999/", "https://10.0.0.2:8999"},
	}
	for _, tc := range cases {
		if got := normalizeNodeAddress(tc.in); got != tc.want {
			t.Fatalf("normalizeNodeAddress(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
