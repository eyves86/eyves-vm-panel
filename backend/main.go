package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"eyvescloud/internal/agent"
	"eyvescloud/internal/api"
	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/server"
	"eyvescloud/internal/version"

	"golang.org/x/term"
)

var shutdownCaptureOnce sync.Once

func main() {
	// 版本查询：`eyvescloud --version | -v | version`。
	// 必须**早于配置初始化**：否则在全新机器上仅查询版本就会触发首启流程
	// （生成 config.db、管理员账号与首启凭据文件），在只读文件系统上还会直接报错。
	// 安装脚本用该输出做已装版本检测（形如 "EyvesCloud 2.2.3"），因此输出保持单行。
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" || arg == "version" {
			fmt.Println("EyvesCloud " + version.Current())
			return
		}
	}

	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))

	isServerMode := false
	isCliMode := false
	noWebAutostart := false
	isAgentMode := false
	isAccessPolicyCommand := len(os.Args) > 1 && os.Args[1] == "access-policy"
	isAccountCommand := len(os.Args) > 1 && (os.Args[1] == "account" || os.Args[1] == "kvm")
	isNodeLinkCommand := len(os.Args) > 1 && (os.Args[1] == "node-link" || os.Args[1] == "pairing-key")
	isSelfUpdateCommand := len(os.Args) > 1 && os.Args[1] == "self-update"
	for _, arg := range os.Args[1:] {
		if arg == "server" || arg == "-s" || arg == "--server" {
			isServerMode = true
		}
		if arg == "agent" || arg == "--agent" {
			isAgentMode = true
		}
		if arg == "cli" || arg == "-c" || arg == "--cli" {
			isCliMode = true
		}
		if arg == "--no-web" || arg == "--cli-only" {
			noWebAutostart = true
			isCliMode = true
		}
	}

	// 初始化配置
	cfg, err := config.InitConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize config: %v\n", err)
		os.Exit(1)
	}
	_ = cfg

	if isAccessPolicyCommand {
		if err := cli.RunAccessPolicyCommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Access policy error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 快捷账号命令：遗忘账号/密码时的非交互恢复（也可用 `kvm` 作为别名）。
	// 必须先于本质上的 server/CLI 分支执行，config 初始化在此前已完成。
	if isAccountCommand {
		if err := cli.RunAccountCommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Account command error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 节点对接快捷命令：查看/生成本面板作为被控的对接密钥
	//（安装脚本用它把地址+密钥展示给运维）。
	if isNodeLinkCommand {
		if err := cli.RunNodeLinkCommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Node link command error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 被控节点 agent 模式：注册到主控 + 心跳 + 本地面板
	if isAgentMode {
		agent.Run(os.Args[2:])
		return
	}

	// 自更新快捷命令：`eyvescloud self-update [--check]`。
	// 与「被控自动更新」「主控下发的一键升级」走同一条代码路径
	// （SelfUpdateToVersion → SHA-256 校验 → 就地替换 → detached 重启），
	// 供运维脚本手动触发或验证升级链路。
	if isSelfUpdateCommand {
		if err := cli.RunSelfUpdateCommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Self update error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if isServerMode || (!isTerminal && !isCliMode) {
		installShutdownStateCapture()

		// 同机"面板即被控节点"：若本机已通过配对/安装注册为被控节点，
		// 由面板进程承担被控职责（注入节点 token + 心跳），无需独立 agent 进程
		// （独立 agent 会再起一个面板并抢占同一端口）。
		agent.StartEmbeddedNodeSide()

		// Restore persisted state
		api.ConfigureTaskQueue(cfg.TaskConcurrency)
		api.RestoreTasks()
		api.RestoreLoginLogs()

		// Start security scanner
		api.InitScanner()
		api.StartSSLRenewalMonitor()

		// Ensure iptables FORWARD rules allow managed bridge traffic.
		lxc.EnsureForwardRules("lxcbr0")
		lxc.EnsureForwardRules("virbr0")
		lxc.EnsureAllAssignedPublicIPv4s()

		// 网络自愈：确保 LXC 侧 lxcbr0 就绪（网关 IP + dnsmasq DHCP + 转发/NAT）。
		// KVM 侧由 ensureDefaultNetwork() 在 KVM 操作时自动完成；此处补上 LXC 侧对称逻辑。
		if err := lxc.EnsureLXCBridgeNetwork(); err != nil {
			fmt.Printf("Warning: LXC bridge self-healing incomplete: %v\n", err)
		}
		// 对称地预检 KVM 侧 libvirt 默认网络（virbr0）。
		if err := kvm.EnsureKVMDefaultNetwork(); err != nil {
			fmt.Printf("Warning: KVM default network self-healing incomplete: %v\n", err)
		}

		// Start expiry scanners (stops expired/over-traffic workloads every 30s)
		manager := lxc.NewManager()
		kvmManager := kvm.NewManager()
		manager.StartExpiryScanner()
		kvmManager.StartExpiryScanner()

		// Start usage monitors (computes CPU/network/disk rates every 5s)
		manager.StartUsageMonitor()
		kvmManager.StartUsageMonitor()
		kvmManager.StartNetworkSyncMonitor()
		kvmManager.StartIPv6Guard()

		// Start scheduled snapshot scanners.
		manager.StartSnapshotScheduler()
		kvmManager.StartSnapshotScheduler()

		// Clean up stale container configs (LXC dir was deleted but config remains)
		config.CleanStaleContainers()
		api.StartHostBootRestore()
		api.InitSMTPSender()
		lxc.EnsureAllRunningPortMappings()

		// Pre-warm SSH for containers already running after host boot or service restart.
		manager.StartSSHWarmupScanner()

		// Run in server mode (frontend embedded in binary)
		if err := server.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// CLI mode normally keeps the web panel available. Use --no-web to avoid
		// starting the systemd web service on locked-down hosts.
		if !noWebAutostart && !isWebPanelSystemdRunning() {
			startWebPanelSystemd()
		}

		// Run CLI interface
		cli.Run()
	}
}

func installShutdownStateCapture() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-signals
		fmt.Fprintf(os.Stderr, "Received %s, capturing workload restore state...\n", sig)
		shutdownCaptureOnce.Do(api.CaptureRuntimeRestoreState)
		os.Exit(0)
	}()
}

func isWebPanelSystemdRunning() bool {
	cmd := exec.Command("systemctl", "is-active", "eyvescloud")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "active"
}

func startWebPanelSystemd() {
	cmd := exec.Command("systemctl", "start", "eyvescloud")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", mainT("警告: 自动启动 Web 面板失败"), err)
	} else {
		fmt.Println(mainT("Web 面板已自动启动"))
	}
}

func mainT(text string) string {
	if !mainEnglish() {
		return text
	}
	switch text {
	case "警告: 自动启动 Web 面板失败":
		return "Warning: failed to auto-start web panel"
	case "Web 面板已自动启动":
		return "Web panel auto-started"
	default:
		return text
	}
}

func mainEnglish() bool {
	lang := strings.ToLower(strings.TrimSpace(os.Getenv("EYVESCLOUD_LANG")))
	if lang == "en" || strings.HasPrefix(lang, "en_") || strings.HasPrefix(lang, "en-") {
		return true
	}
	if lang == "zh" || strings.HasPrefix(lang, "zh_") || strings.HasPrefix(lang, "zh-") {
		return false
	}
	return config.AppConfig != nil && config.NormalizeLanguage(config.AppConfig.Language) == "en"
}
