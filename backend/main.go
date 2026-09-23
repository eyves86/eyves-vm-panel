package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"eyvescloud/internal/api"
	"eyvescloud/internal/agent"
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
	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))

	isServerMode := false
	isCliMode := false
	noWebAutostart := false
	isAgentMode := false
	isAccessPolicyCommand := len(os.Args) > 1 && os.Args[1] == "access-policy"
	isAccountCommand := len(os.Args) > 1 && (os.Args[1] == "account" || os.Args[1] == "kvm")
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

	// Initialize config
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

	// 版本查询：`eyvescloud --version | -v | version`。必须早于配置初始化，
	// 以便在只读/最小化环境中也能安全打印版本（也用于被控安装脚本的完整性校验）。
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" || arg == "version" {
			fmt.Println("EyvesCloud " + version.Current())
			return
		}
	}

	// 被控节点 agent 模式：注册到主控 + 心跳 + 本地面板
	if isAgentMode {
		agent.Run(os.Args[2:])
		return
	}

	if isServerMode || (!isTerminal && !isCliMode) {
		installShutdownStateCapture()

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
