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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"eyvescloud/internal/api"
	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/server"
	"eyvescloud/internal/version"
)

// agentConfig 是被控节点保存的注册信息（与 config 包共享，面板 UI 也会读写它）。
type agentConfig = config.AgentConfig

// Run 启动被控节点 agent 模式：注册到主控、上报心跳、运行本地面板。
// 用法: eyvescloud agent --controller=https://master:18999 [--install-key=xxx] [--name=node1] [--addr=http://1.2.3.4:8999] [--allow-insecure-http] [--register-only]
func Run(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	controller := fs.String("controller", "", "主控地址，如 https://1.2.3.4:18999")
	installKey := fs.String("install-key", "", "安装密钥（首次注册时使用）")
	name := fs.String("name", "", "节点名称（默认使用主机名）")
	addr := fs.String("addr", "", "本节点面板地址，如 http://1.2.3.4:8999")
	allowInsecureHTTP := fs.Bool("allow-insecure-http", false, "允许与主控用明文 http 通信（不安全，仅在主控不提供 TLS 时使用）")
	registerOnly := fs.Bool("register-only", false, "仅完成注册并落盘后退出，不启动心跳/运维循环/本地面板（安装脚本的注册步骤使用）")
	_ = fs.Parse(args)

	if strings.TrimSpace(*controller) == "" {
		fmt.Fprintln(os.Stderr, "agent 模式需要 --controller 主控地址（建议用 https://）")
		os.Exit(1)
	}

	if _, err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化配置失败: %v\n", err)
		os.Exit(1)
	}

	ac := config.LoadAgentConfig()

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
		if err := config.SaveAgentConfig(ac); err != nil {
			fmt.Fprintf(os.Stderr, "保存 agent.json 失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已注册到主控 %s（节点 %s）\n", ac.Controller, ac.Name)
	}

	// --register-only：安装脚本 [2/3] 注册步骤使用。注册（或确认既有配置）
	// 落盘后立即退出 —— 完整 agent 会在最后进入 server.Run() 永久阻塞，
	// 若在前台执行会导致安装脚本卡死在注册步骤、永远走不到安装服务。
	if *registerOnly {
		if !needRegister {
			fmt.Printf("已存在注册配置（节点 %s），跳过注册\n", ac.Name)
		}
		return
	}

	config.SetAgentToken(ac.Token)
	fmt.Printf("EyvesCloud Agent 启动完成，主控: %s，节点: %s\n", ac.Controller, ac.Name)

	go heartbeatLoop(ac)

	// 与主控对称的本机运维循环：被控节点上的容器同样需要到期停机、
	// 流量统计、计划快照与网络自愈。主控的 expiry scanner 只扫主控本机
	// config，跨节点容器的生命周期治理必须在 agent 本机执行。
	startLocalRuntimeLoops()

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
	} else {
		addr = normalizeSelfAddress(addr)
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

// StartEmbeddedNodeSide 让"面板即被控节点"成为可能（同机部署）。
//
// 背景：agent 模式末尾会启动一个完整面板（server.Run），因此**无法与主控面板
// 同机共存**——两个进程抢同一个监听端口，谁先绑定谁赢，另一个无限重启。
// 同机场景改由主控面板自身承担被控职责：
//   - 从 agent.json 读取节点 token 并注入本进程，使面板自带的 /api/agent/*
//     端点可用（否则 AgentTokenMiddleware 因 token 为空返回 403）；
//   - 启动心跳循环，让主控把节点标记为在线并同步容器摘要。
//
// 未注册（无 agent.json）时不做任何事。由 main.go 的 server 模式调用。
func StartEmbeddedNodeSide() {
	ac := config.LoadAgentConfig()
	if ac == nil || strings.TrimSpace(ac.Token) == "" {
		return
	}
	config.SetAgentToken(ac.Token)
	fmt.Printf("被控注册信息已加载：主控 %s，节点 %s；面板内置被控端点已启用（心跳 10s）\n",
		ac.Controller, ac.Name)
	go heartbeatLoop(ac)
}

// heartbeatLoop 每 10s 上报一次心跳（主控据此判定节点在线并同步容器摘要）。
func heartbeatLoop(ac *agentConfig) {
	for {
		sendHeartbeat(ac)
		time.Sleep(10 * time.Second)
	}
}

// startLocalRuntimeLoops 启动被控节点的本机运维循环，与主控 server 模式
// （main.go）保持对称。缺了这些，agent 上的容器不会到期停机、流量不累计、
// 计划快照不执行、桥接网络故障不自愈。
func startLocalRuntimeLoops() {
	manager := lxc.NewManager()
	kvmManager := kvm.NewManager()

	// 到期/超流量停机扫描（每 30s）
	manager.StartExpiryScanner()
	kvmManager.StartExpiryScanner()

	// 用量采集（CPU/网络/磁盘速率，每 5s；流量累计写入本机 config）
	manager.StartUsageMonitor()
	kvmManager.StartUsageMonitor()
	kvmManager.StartNetworkSyncMonitor()
	kvmManager.StartIPv6Guard()

	// 计划快照
	manager.StartSnapshotScheduler()
	kvmManager.StartSnapshotScheduler()

	// 网络自愈：确保 LXC/KVM 桥就绪（网关 IP + DHCP + 转发/NAT）。
	lxc.EnsureForwardRules("lxcbr0")
	lxc.EnsureForwardRules("virbr0")
	if err := lxc.EnsureLXCBridgeNetwork(); err != nil {
		fmt.Printf("Warning: agent LXC bridge self-healing incomplete: %v\n", err)
	}
	if err := kvm.EnsureKVMDefaultNetwork(); err != nil {
		fmt.Printf("Warning: agent KVM default network self-healing incomplete: %v\n", err)
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
		// 容器摘要列表（供主控聚合展示）：只传轻量字段 + 最新指标 + 流量累计，
		// 完整详情由主控按需拉取。
		type containerSummary struct {
			ID             int    `json:"id"`
			UUID           string `json:"uuid"`
			Name           string `json:"name"`
			Status         string `json:"status"`
			Virtualization string `json:"virtualization"`
			IP             string `json:"ip,omitempty"`
			SSHPort        int    `json:"ssh_port,omitempty"`
			Suspended      bool   `json:"suspended,omitempty"`
			VCPU           float64 `json:"vcpu"`
			RAMMB          int    `json:"ram_mb"`
			DiskGB         float64 `json:"disk_gb"`
			ExpiresAt      string `json:"expires_at,omitempty"`
			TrafficUsedRX  int64  `json:"traffic_used_rx,omitempty"`
			TrafficUsedTX  int64  `json:"traffic_used_tx,omitempty"`
			// 最新实时指标（agent 本机 metric history 的尾采样点）
			CPU       float64 `json:"cpu,omitempty"`
			Memory    float64 `json:"memory,omitempty"`
			NetworkRx float64 `json:"network_rx,omitempty"`
			NetworkTx float64 `json:"network_tx,omitempty"`
			DiskRead  float64 `json:"disk_read,omitempty"`
			DiskWrite float64 `json:"disk_write,omitempty"`
			MetricTS  int64   `json:"metric_ts,omitempty"`
		}
		summaries := make([]containerSummary, 0, len(config.AppConfig.Containers))
		for _, c := range config.AppConfig.Containers {
			s := containerSummary{
				ID: c.ID, UUID: c.UUID, Name: c.Name, Status: c.Status,
				Virtualization: c.Virtualization, Suspended: c.Suspended,
				IP: c.IP, SSHPort: c.SSHPort,
				VCPU: c.VCPU, RAMMB: c.RAMMB, DiskGB: c.DiskGB,
				ExpiresAt: c.ExpiresAt, TrafficUsedRX: c.TrafficUsedRX,
				TrafficUsedTX: c.TrafficUsedTX,
			}
			if p, ok := latestLocalContainerMetric(c.UUID); ok {
				s.CPU, s.Memory = p.CPU, p.Memory
				s.NetworkRx, s.NetworkTx = p.NetworkRx, p.NetworkTx
				s.DiskRead, s.DiskWrite = p.DiskRead, p.DiskWrite
				s.MetricTS = p.TS
			}
			summaries = append(summaries, s)
		}
		status["containers"] = summaries
		config.AppConfigMu.RUnlock()
	}
	return status
}

// localMetricPoint 是 agent 本机内存中的最新指标采样点（与 api 包的
// ContainerMetricPoint 结构对齐，避免 agent 直接依赖 api 包）。
type localMetricPoint struct {
	TS        int64
	CPU       float64
	Memory    float64
	NetworkRx float64
	NetworkTx float64
	DiskRead  float64
	DiskWrite float64
}

// latestLocalContainerMetric 读取 agent 本机指标采样的最新点（由 server.Run
// 启动的 metric sampler 维护），心跳时随容器摘要上报主控。
func latestLocalContainerMetric(uuid string) (localMetricPoint, bool) {
	p, ok := api.LatestContainerMetricByUUID(uuid)
	if !ok {
		return localMetricPoint{}, false
	}
	return localMetricPoint{
		TS: p.TS, CPU: p.CPU, Memory: p.Memory,
		NetworkRx: p.NetworkRx, NetworkTx: p.NetworkTx,
		DiskRead: p.DiskRead, DiskWrite: p.DiskWrite,
	}, true
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
	return fmt.Sprintf("%s://%s:%d", selfPanelScheme(), local.IP.String(), port)
}

// selfPanelScheme 返回本机面板实际监听的 scheme：SSL 启用时为 https，否则 http。
// 注册上报的地址必须与本机面板真实协议一致，否则主控代理请求会连错协议。
func selfPanelScheme() string {
	if config.AppConfig != nil && config.AppConfig.SSL.Enabled {
		return "https"
	}
	return "http"
}

// normalizeSelfAddress 为无 scheme 的 --addr 补全本机面板真实协议。
// 主控侧 normalizeNodeAddress 默认补 https（F9），agent 必须显式声明 http 面板，
// 否则无 scheme 的 --addr 会被主控误存为 https 导致代理断链。
func normalizeSelfAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return selfPanelScheme() + "://" + addr
}
