package agent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/server"
	"eyvescloud/internal/version"
)

// agentConfig 是被控节点保存的注册信息。
type agentConfig struct {
	Controller string `json:"controller"`
	NodeID     string `json:"node_id"`
	Token      string `json:"token"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	// AllowInsecureHTTP 允许与主控通过明文 http 通信（仅当主控不提供 TLS 时显式开启）。
	AllowInsecureHTTP bool `json:"allow_insecure_http,omitempty"`
}

// Run 启动被控节点 agent 模式：注册到主控、上报心跳、运行本地面板。
// 用法: eyvescloud agent --controller=https://master:18999 [--install-key=xxx] [--name=node1] [--addr=http://1.2.3.4:8999] [--allow-insecure-http]
func Run(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	controller := fs.String("controller", "", "主控地址，如 https://1.2.3.4:18999")
	installKey := fs.String("install-key", "", "安装密钥（首次注册时使用）")
	name := fs.String("name", "", "节点名称（默认使用主机名）")
	addr := fs.String("addr", "", "本节点面板地址，如 http://1.2.3.4:8999")
	allowInsecureHTTP := fs.Bool("allow-insecure-http", false, "允许与主控用明文 http 通信（不安全，仅在主控不提供 TLS 时使用）")
	_ = fs.Parse(args)

	if strings.TrimSpace(*controller) == "" {
		fmt.Fprintln(os.Stderr, "agent 模式需要 --controller 主控地址（建议用 https://）")
		os.Exit(1)
	}

	if _, err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化配置失败: %v\n", err)
		os.Exit(1)
	}

	cfgPath := filepath.Join(config.AppConfig.DataDir, "agent.json")
	ac := loadAgentConfig(cfgPath)

	needRegister := ac == nil
	if strings.TrimSpace(*installKey) != "" && (ac == nil || ac.Controller != strings.TrimSpace(*controller)) {
		needRegister = true
	}

	if !*allowInsecureHTTP && ac != nil && ac.AllowInsecureHTTP {
		// 已在本地做了不安全豁免，本次未显式要求关闭，沿用持久化的决定。
		*allowInsecureHTTP = true
	}
	if err := initSecureTransport(strings.TrimSpace(*controller), *allowInsecureHTTP); err != nil {
		fmt.Fprintf(os.Stderr, "主控通道安全检查失败: %v\n", err)
		os.Exit(1)
	}

	if needRegister {
		nc, err := register(strings.TrimSpace(*controller), strings.TrimSpace(*installKey), strings.TrimSpace(*name), strings.TrimSpace(*addr), *allowInsecureHTTP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "注册到主控失败: %v\n", err)
			os.Exit(1)
		}
		ac = nc
		saveAgentConfig(cfgPath, ac)
		fmt.Printf("已注册到主控 %s（节点 %s）\n", ac.Controller, ac.Name)
	}

	config.SetAgentToken(ac.Token)
	fmt.Printf("EyvesCloud Agent 启动完成，主控: %s，节点: %s\n", ac.Controller, ac.Name)

	go heartbeatLoop(ac)

	// 被控节点自动更新：可选。仅在非交互环境下、且以分钟级间隔启用。
	if autoUpdateMinutes() > 0 {
		go autoUpdateLoop(autoUpdateMinutes())
	}

	// 被控自身也是完整面板，运行本地 Web 服务。
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
		os.Exit(1)
	}
}

// autoUpdateMinutes 读取 EYVESCLOUD_AUTO_UPDATE 环境变量（分钟）。0 或未设置表示关闭。
func autoUpdateMinutes() int {
	raw := strings.TrimSpace(os.Getenv("EYVESCLOUD_AUTO_UPDATE"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 60 {
		// 至少 60 分钟一次，避免频繁检查 GitHub。
		return 0
	}
	return n
}

// autoUpdateLoop 定期非交互检查并更新被控节点自身二进制。
func autoUpdateLoop(minutes int) {
	interval := time.Duration(minutes) * time.Minute
	// 启动先等一个心跳周期，给注册和服务稳定留出时间。
	time.Sleep(30 * time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		latest, upgraded, err := cli.SelfUpdateOnce()
		if err != nil {
			fmt.Fprintf(os.Stderr, "自动更新检查失败（下次重试）: %v\n", err)
			continue
		}
		if upgraded {
			fmt.Printf("被控节点已自动更新到 %s\n", latest)
			// 二进制与配置已替换，重启服务后本 goroutine 所在进程结束。
			return
		}
	}
}

func register(controller, installKey, name, addr string, allowInsecureHTTP bool) (*agentConfig, error) {
	if installKey == "" {
		return nil, fmt.Errorf("首次注册需要 --install-key 安装密钥")
	}
	if name == "" {
		host, _ := os.Hostname()
		name = host
	}
	// 未显式指定本节点地址时，通过与主控建连的源地址推算本机可被主控访问的地址。
	if strings.TrimSpace(addr) == "" {
		addr = detectSelfAddress(controller)
	}
	payload := map[string]string{
		"install_key": installKey,
		"name":        name,
		"address":     addr,
		"version":     version.Current(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	client, err := secureControllerClient(controller, allowInsecureHTTP)
	if err != nil {
		return nil, err
	}
	client.Timeout = 15 * time.Second
	resp, err := client.Post(strings.TrimSuffix(controller, "/")+"/api/nodes/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			NodeID string `json:"node_id"`
			Token  string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Success || out.Data.NodeID == "" || out.Data.Token == "" {
		return nil, fmt.Errorf("主控返回: %s", out.Message)
	}
	return &agentConfig{
		Controller:        controller,
		NodeID:            out.Data.NodeID,
		Token:             out.Data.Token,
		Name:              name,
		Address:           addr,
		AllowInsecureHTTP: allowInsecureHTTP || !strings.HasPrefix(strings.ToLower(controller), "https://"),
	}, nil
}

func heartbeatLoop(ac *agentConfig) {
	for {
		sendHeartbeat(ac)
		time.Sleep(10 * time.Second)
	}
}

func sendHeartbeat(ac *agentConfig) {
	payload := collectNodeStatus()
	payload["version"] = version.Current()
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client, err := secureControllerClient(ac.Controller, ac.AllowInsecureHTTP)
	if err != nil {
		return
	}
	client.Timeout = 10 * time.Second
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(ac.Controller, "/")+"/api/nodes/"+ac.NodeID+"/heartbeat",
		bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+ac.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// initSecureTransport 对主控通道做传输层安全检查：默认要求 https，杜绝节点 token 明文传输。
// 仅当显式 `--allow-insecure-http` 时才允许 http（并打印醒目警告）。
func initSecureTransport(controller string, allowInsecureHTTP bool) error {
	u := strings.ToLower(controller)
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return fmt.Errorf("主控地址需以 http:// 或 https:// 开头")
	}
	if strings.HasPrefix(u, "https://") {
		return nil
	}
	if !allowInsecureHTTP {
		return fmt.Errorf("主控使用明文 http 将导致节点 token 明文传输，拒绝连接；请改用 https://，或显式传入 --allow-insecure-http（不推荐）")
	}
	fmt.Fprintln(os.Stderr, "警告：正在与主控通过明文 http 通信，节点 token 与心跳可能被中间人截获（仅适用于无 TLS 的主控）。")
	return nil
}

// secureControllerClient 构造指向主控的 HTTP 客户端。
// Go 的默认 Transport 对 https 总是校验证书；http（明文）仅在显式允许时放行。
func secureControllerClient(controller string, allowInsecureHTTP bool) (*http.Client, error) {
	if err := initSecureTransport(controller, allowInsecureHTTP); err != nil {
		return nil, err
	}
	return &http.Client{Transport: http.DefaultTransport}, nil
}

func collectNodeStatus() map[string]interface{} {
	status := map[string]interface{}{}
	status["cpu_count"] = runtime.NumCPU()
	if totalKB, availKB, ok := readMemInfo(); ok {
		status["ram_total_mb"] = totalKB / 1024
		status["ram_used_mb"] = (totalKB - availKB) / 1024
	}
	if totalBytes, freeBytes, ok := diskUsage("/"); ok {
		totalGB := float64(totalBytes) / 1024 / 1024 / 1024
		usedGB := float64(totalBytes-freeBytes) / 1024 / 1024 / 1024
		status["disk_total_gb"] = totalGB
		status["disk_used_gb"] = usedGB
	}
	status["os_name"] = osName()
	if config.AppConfig != nil {
		config.AppConfigMu.RLock()
		status["container_count"] = len(config.AppConfig.Containers)
		config.AppConfigMu.RUnlock()
	}
	return status
}

func readMemInfo() (totalKB, availableKB int64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value := int64(0)
		fmt.Sscanf(fields[1], "%d", &value)
		switch fields[0] {
		case "MemTotal:":
			totalKB = value
		case "MemAvailable:":
			availableKB = value
		}
	}
	return totalKB, availableKB, totalKB > 0
}

func diskUsage(path string) (total, free int64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize), true
}

func osName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "PRETTY_NAME="))
			value = strings.Trim(value, `"`)
			if value != "" {
				return value
			}
		}
	}
	return runtime.GOOS
}

func loadAgentConfig(path string) *agentConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ac agentConfig
	if err := json.Unmarshal(data, &ac); err != nil || ac.NodeID == "" || ac.Token == "" {
		return nil
	}
	return &ac
}

func saveAgentConfig(path string, ac *agentConfig) {
	data, err := json.MarshalIndent(ac, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// detectSelfAddress 推算本节点对主控可达的地址，优先使用面板监听端口。
// 通过向主控发起 TCP 连接拿到本机出站源 IP（比直接读网卡更贴合主控可达性）。
func detectSelfAddress(controller string) string {
	u, err := url.Parse(controller)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		// controller 未带端口时补默认 80/443，仅用于取源 IP。
		if u.Scheme == "https" {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok || local.IP == nil || local.IP.IsUnspecified() || local.IP.IsLoopback() {
		return ""
	}
	port := 8999
	if config.AppConfig != nil && config.AppConfig.Port > 0 {
		port = config.AppConfig.Port
	}
	return fmt.Sprintf("http://%s:%d", local.IP.String(), port)
}
