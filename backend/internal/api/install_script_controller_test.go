package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestInstallScriptControllerBakedIn 复现线上 bug：被控执行
// `curl -fsSL -H "X-Install-Key: ..." <主控>/api/nodes/<id>/install-script | sudo bash`
// 时输出 `主控地址: ''`，随后 curl 报 `Could not resolve host: ''`。
//
// 根因（两层叠加）：
//  1. handleNodeInstallScript 向 buildAgentInstallScript 传空 controller，主控
//     地址没有烘焙进脚本，被控侧自动探测（SSH 来源/本机源 IP）拿到的都不是
//     主控地址；
//  2. 模板把 shellEscape（单引号包裹）的值放进双引号上下文，生成
//     INSTALL_KEY="'<key>'"（值带字面引号）与 [ -n "''" ]（非空恒真），
//     CONTROLLER 被赋成字面量 ''。
//
// 修复：controller 一律传 externalBaseURL(r)，模板改用 shellDQ 转义。
func TestInstallScriptControllerBakedIn(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	controller := "http://154.16.173.136:8999"
	key := "2a144b4e5fe1aa5ffc1232c718ffc3f0ec73e71b99941a7e5b6c922ca15108ed"
	script := buildAgentInstallScript(controller, key, "节点2", "")

	// 值不得带字面单引号（旧 bug：INSTALL_KEY="'<key>'"、[ -n "''" ]）
	if strings.Contains(script, `INSTALL_KEY="'`) {
		t.Fatalf("INSTALL_KEY polluted with literal quotes: %.80s", script)
	}
	if strings.Contains(script, `[ -n "''" ]`) {
		t.Fatalf("hardcoded controller check is tautologically true (empty shellEscape)")
	}
	if !strings.Contains(script, fmt.Sprintf("INSTALL_KEY=%q", key)) {
		t.Fatalf("INSTALL_KEY not baked cleanly, want %q", `INSTALL_KEY="`+key+`"`)
	}

	// 端到端：用真实 bash 执行脚本里的 detect_controller（无 --controller、
	// 无 SSH_CONNECTION —— 与 curl|sudo bash 场景一致），CONTROLLER 必须等于烘焙地址。
	got := runDetectController(t, script)
	if got != controller {
		t.Fatalf("detect_controller() = %q, want %q", got, controller)
	}

	// --controller 覆盖仍优先
	scriptOverride := runDetectControllerWithEnv(t, script, "CONTROLLER_OVERRIDE=https://override.example.com")
	if scriptOverride != "https://override.example.com" {
		t.Fatalf("CONTROLLER_OVERRIDE should win, got %q", scriptOverride)
	}
}

// TestInstallScriptControllerEmptyFallsBackToDetection controller 为空（兜底路径）
// 时不得产生字面量 ''：硬编码检查为 [ -n "" ]（恒假），继续走运行时探测。
func TestInstallScriptControllerEmptyFallsBackToDetection(t *testing.T) {
	script := buildAgentInstallScript("", "somekey", "n", "")
	if strings.Contains(script, `[ -n "''" ]`) {
		t.Fatalf("tautological check [ -n \"''\" ] must not appear")
	}
	if !strings.Contains(script, `if [ -n "" ]; then`) {
		t.Fatalf("empty controller must render a falsy check [ -n \"\" ], got neither form")
	}
	if strings.Contains(script, `INSTALL_KEY="'`) {
		t.Fatalf("INSTALL_KEY polluted with literal quotes")
	}
}

// TestInstallScriptRegisterStepNotBlocking 复现线上 bug：脚本 [2/3] 注册步骤
// 在前台直接运行完整 agent —— agent 注册后进入 server.Run() 永久阻塞，
// 安装脚本卡死在注册步骤，永远走不到 [3/3] 安装 systemd 服务。
//
// 修复：注册步骤使用 --register-only（注册落盘即退出）；systemd ExecStart
// 保持完整 agent（心跳 + 本地面板是服务的职责）。
func TestInstallScriptRegisterStepNotBlocking(t *testing.T) {
	script := buildAgentInstallScript("http://154.16.173.136:8999", "somekey", "节点1", "")

	// [2/3] 注册步骤必须带 --register-only（前台执行、要求退出）
	regIdx := strings.Index(script, "[2/3] 注册被控节点")
	if regIdx < 0 {
		t.Fatalf("register step not found in script")
	}
	execIdx := strings.Index(script, "/usr/local/bin/eyvescloud agent ")
	if execIdx < 0 {
		t.Fatalf("agent invocation not found in script")
	}
	if execIdx < regIdx {
		t.Fatalf("unexpected script order: agent invocation before register step")
	}
	// 注册步骤的命令行（从注册标记到 ExecStart heredoc 之间）必须包含 --register-only
	registerCmd := script[execIdx:strings.Index(script[execIdx:], "UNITEOF")+execIdx]
	if !strings.Contains(registerCmd, "--register-only") {
		t.Fatalf("register step must run with --register-only, got: %.200s", registerCmd)
	}

	// systemd ExecStart 保持完整 agent（不带 --register-only）
	execStart := script[strings.Index(script, "ExecStart="):]
	if !strings.Contains(execStart, "ExecStart=/usr/local/bin/eyvescloud agent ") {
		t.Fatalf("systemd ExecStart must run the full agent")
	}
	if strings.Contains(execStart, "--register-only") {
		t.Fatalf("systemd service must NOT use --register-only (it must keep running)")
	}
}

// TestInstallScriptHandlerBakesRequestHost 集成层：通过 X-Install-Key 认证请求
// install-script，脚本必须烘焙本次请求推导出的主控地址（Host 头）。
func TestInstallScriptHandlerBakesRequestHost(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	key := "test-install-key-123456"
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser: "admin",
		JWTSecret: "test-secret",
		Nodes:     []config.Node{{ID: "node-1", Name: "node-1", InstallKey: key}},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/install-script", nil)
	r.Host = "154.16.173.136:8999"
	r.Header.Set("X-Install-Key", key)
	HandleNodeSubRoutes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("install-script status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Host 推导 → http://154.16.173.136:8999 烘焙为硬编码兜底
	if !strings.Contains(body, `if [ -n "http://154.16.173.136:8999" ]; then`) {
		t.Fatalf("script should bake request-derived controller, got:\n%.400s", body)
	}
	// install_key 不得带字面引号（否则后续 X-Install-Key 认证失败）
	if !strings.Contains(body, fmt.Sprintf("INSTALL_KEY=%q", key)) {
		t.Fatalf("INSTALL_KEY not baked cleanly, got:\n%.400s", body)
	}
}

// TestInstallScriptHandlerPrefersPanelDomain 「面板绑定域名」配置后，
// 安装脚本一律使用该域名（反代/多入口环境下地址正确）。
func TestInstallScriptHandlerPrefersPanelDomain(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	key := "test-install-key-123456"
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:   "admin",
		JWTSecret:   "test-secret",
		PanelDomain: "https://panel.example.com",
		Nodes:       []config.Node{{ID: "node-1", Name: "node-1", InstallKey: key}},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/install-script", nil)
	r.Host = "154.16.173.136:8999"
	r.Header.Set("X-Install-Key", key)
	HandleNodeSubRoutes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("install-script status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `if [ -n "https://panel.example.com" ]; then`) {
		t.Fatalf("script should bake PanelDomain, got:\n%.400s", w.Body.String())
	}
}

// runDetectController 从生成的脚本中提取 detect_controller 函数并用真实 bash
// 执行（模拟 curl|bash：无参数、无 SSH_CONNECTION），返回解析出的主控地址。
func runDetectController(t *testing.T, script string) string {
	t.Helper()
	return runDetectControllerWithEnv(t, script, "")
}

func runDetectControllerWithEnv(t *testing.T, script, envLine string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent.sh")
	fnPath := filepath.Join(dir, "fn.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if envLine != "" {
		envLine += "; "
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`sed -n '/^detect_controller() {/,/^}$/p' %q > %q && env -u SSH_CONNECTION bash -c '%ssource %q; detect_controller'`,
		scriptPath, fnPath, envLine, fnPath))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("detect_controller failed: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
