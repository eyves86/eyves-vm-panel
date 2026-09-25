package api

import (
	"os/exec"
	"strings"
	"testing"
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
