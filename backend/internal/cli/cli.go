package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/version"
)

var manager = lxc.NewManager()

const (
	eyvescloudBackupDir         = "/root/eyvescloud-backups"
	eyvescloudNewBinaryPath     = "/usr/local/bin/eyvescloud.new"
	libvirtDefaultNetworkMarker = "/var/lib/eyvescloud/kvm/default-network.created"
)

var cliEnglish = detectCLIEnglish()

var cliTranslations = map[string]string{
	"重新加载配置失败":               "Failed to reload config",
	"请选择操作":                  "Select an action",
	"再见":                     "Goodbye",
	"无效选择":                   "Invalid choice",
	"EyvesCloud - LXC 容器管理器": "EyvesCloud - Container Manager",
	"Web 面板":                 "Web panel",
	"端口":                     "port",
	"运行中":                    "running",
	"已停止":                    "stopped",
	"当前版本":                   "Current version",
	"查看容器列表":                 "List containers",
	"创建容器":                   "Create container",
	"开机容器":                   "Start container",
	"关机容器":                   "Stop container",
	"重启容器":                   "Restart container",
	"删除容器":                   "Delete container",
	"重装容器系统":                 "Reinstall container OS",
	"重置 Web 管理员密码":           "Reset web admin password",
	"启动":                     "Start",
	"停止":                     "Stop",
	"导入现有 LXC 容器":            "Import existing LXC containers",
	"检查并升级 EYVESCLOUD":       "Check and upgrade EYVESCLOUD",
	"卸载 EYVESCLOUD":          "Uninstall EYVESCLOUD",
	"面板访问白名单":                "Panel access allowlist",
	"系统信息":                   "System info",
	"退出":                     "Exit",
	"获取容器列表失败":               "Failed to get container list",
	"暂无容器":                   "No containers",
	"容器":                     "Container",
	"名称":                     "Name",
	"状态":                     "Status",
	"镜像":                     "Image",
	"内存(MB)":                 "Memory(MB)",
	"磁盘(GB)":                 "Disk(GB)",
	"容器名称":                   "Container name",
	"容器名称不能为空":               "Container name cannot be empty",
	"可用镜像":                   "Available images",
	"镜像选择无效":                 "Invalid image selection",
	"内存 (MB)":                "Memory (MB)",
	"磁盘 (GB)":                "Disk (GB)",
	"网络带宽 (Mbps)":            "Network bandwidth (Mbps)",
	"月流量 (GB)":               "Monthly traffic (GB)",
	"IO 速度 (MB/s)":           "IO speed (MB/s)",
	"额外 NAT 端口，多个用逗号分隔": "Extra NAT ports, comma-separated",
	"正在创建容器":            "Creating container",
	"创建失败":              "Create failed",
	"创建成功":              "created successfully",
	"端口未分配":             "port not assigned",
	"密码已保存，请在 Web 面板中查看或重置": "Password saved. View or reset it in the web panel",
	"开机失败":      "Start failed",
	"已开机":       "started",
	"关机失败":      "Stop failed",
	"已关机":       "stopped",
	"重启失败":      "Restart failed",
	"已重启":       "restarted",
	"开机":        "start",
	"关机":        "stop",
	"重启":        "restart",
	"删除":        "delete",
	"重装":        "reinstall",
	"确认删除容器":    "Delete container",
	"输入 yes 继续": "type yes to continue",
	"已取消":       "Cancelled",
	"删除失败":      "Delete failed",
	"已删除":       "deleted",
	"确认重装容器":    "Reinstall container",
	"重装失败":      "Reinstall failed",
	"已重装":       "reinstalled",
	"新的管理员密码（至少 6 位）": "New admin password (at least 6 characters)",
	"密码至少需要 6 位":      "Password must be at least 6 characters",
	"确认密码":            "Confirm password",
	"两次输入的密码不一致":      "Passwords do not match",
	"管理员密码已重置。":       "Admin password has been reset.",
	"按 Enter 返回菜单":    "Press Enter to return to menu",
	"选择要":             "Select a container to ",
	"的容器":             "",
	"选择无效":            "Invalid selection",
	"主机名":             "Hostname",
	"管理员用户":           "Admin user",
	"容器总数":            "Total containers",
	"切换语言":            "Switch language",
	"当前语言":            "Current language",
	"请选择语言":           "Select language",
	"语言已切换为":          "Language switched to",
	"保存语言失败":          "Failed to save language",
	"简体中文":            "Simplified Chinese",
	"重置失败":            "Reset failed",
	"停止 Web 面板失败":     "Failed to stop web panel",
	"Web 面板已停止，LXC 容器不会受影响。": "Web panel stopped. LXC containers are not affected.",
	"启动 Web 面板失败":            "Failed to start web panel",
	"Web 面板已启动":              "Web panel started",
	"升级只会替换 /usr/local/bin/eyvescloud，并保留 /root/.eyvescloud 里的配置、容器数据和任务记录。": "The upgrade only replaces /usr/local/bin/eyvescloud and keeps configuration, container data, and task records under /root/.eyvescloud.",
	"升级需要 root 权限。请使用: sudo eyvescloud cli":                                  "Upgrade requires root privileges. Use: sudo eyvescloud cli",
	"检查仓库":     "Checking repository",
	"检查最新版本失败": "Failed to check the latest version",
	"GitHub Release 缺少 tag_name，无法判断最新版本。": "The release is missing tag_name; cannot determine the latest version.",
	"最新版本":            "Latest version",
	"发布页面":            "Release page",
	"当前架构不支持自动升级":     "Automatic upgrade is not supported on the current architecture",
	"最新 Release 没有找到": "The latest release does not contain",
	"无法自动升级。":         "automatic upgrade is unavailable.",
	"当前已经是最新版本。":      "The current version is already the latest.",
	"是否仍然重新安装最新版本？输入 reinstall 继续":         "Reinstall the latest version anyway? Type reinstall to continue",
	"输入 upgrade 开始升级":                      "Type upgrade to start upgrade",
	"已取消。":                                 "Cancelled.",
	"升级失败":                                 "Upgrade failed",
	"升级完成":                                 "Upgrade completed",
	"原有数据已保留，Web 服务已重启。":                   "Existing data has been kept and the web service has been restarted.",
	"Release API 返回":                       "Release API returned",
	"GitHub API 被限流，已切换到备用检查方式。":           "GitHub API rate limit reached; switched to fallback check.",
	"GitHub API 不可用，已切换到备用检查方式。":           "GitHub API is unavailable; switched to fallback check.",
	"releases/latest 返回":                   "releases/latest returned",
	"无法从 releases/latest 跳转结果解析最新版本":       "Unable to parse the latest version from the GitHub releases/latest redirect",
	"正在下载升级包...":                           "Downloading upgrade package...",
	"正在解压升级包...":                           "Extracting upgrade package...",
	"解压失败":                                 "Extraction failed",
	"备份旧二进制失败":                             "Failed to back up old binary",
	"旧版本已备份":                               "Old version backed up",
	"正在替换二进制...":                           "Replacing binary...",
	"停止 Web 服务失败，继续尝试替换":                   "Failed to stop web service; continuing replacement attempt",
	"二进制已替换，但重启 Web 服务失败":                  "Binary was replaced, but restarting the web service failed",
	"下载失败，HTTP":                            "Download failed, HTTP",
	"升级包内未找到 eyvescloud 二进制":               "No eyvescloud binary found in the upgrade package",
	"将 /var/lib/lxc 里的容器导入 EYVESCLOUD 配置。": "Import containers under /var/lib/lxc into EYVESCLOUD configuration.",
	"导入后会保留真实 LXC 名称，Web 和 CLI 都能管理同一个容器。": "After import, real LXC names are kept and both Web and CLI can manage the same containers.",
	"导入失败":            "Import failed",
	"没有发现新的 ct-* 容器。": "No new ct-* containers found.",
	"已导入":             "Imported",
	"个容器":             "containers",
	"将删除 EYVESCLOUD 服务和 /usr/local/bin/eyvescloud。": "This will remove the EYVESCLOUD service and /usr/local/bin/eyvescloud.",
	"同时会删除 /root/.eyvescloud、/var/lib/lxc、/var/lib/eyvescloud、镜像缓存、备份、临时文件、/swapfile 和 EYVESCLOUD 网络规则。": "It will also remove /root/.eyvescloud, /var/lib/lxc, /var/lib/eyvescloud, image caches, backups, temporary files, /swapfile, and EYVESCLOUD network rules.",
	"卸载需要 root 权限。":                     "Uninstall requires root privileges.",
	"请运行: sudo eyvescloud cli --no-web": "Run: sudo eyvescloud cli --no-web",
	"输入 uninstall 继续卸载":                 "Type uninstall to continue uninstalling",
	"EYVESCLOUD 已卸载。":                   "EYVESCLOUD has been uninstalled.",
	"服务、二进制、配置、容器/虚拟机、本地镜像、缓存、备份、临时文件和 EYVESCLOUD 网络规则均已删除。":         "Service, binary, configuration, containers/VMs, local images, cache, backups, temporary files, and EYVESCLOUD network rules have been removed.",
	"检测到非 EYVESCLOUD 虚拟机仍在使用 libvirt default 网络，已保留 default/virbr0。": "Non-EYVESCLOUD VMs are still using the libvirt default network, so default/virbr0 has been kept.",
	"Web 面板重载跳过":          "Web panel reload skipped",
	"Web 面板已重载并应用配置变更。":   "Web panel reloaded and configuration changes applied.",
	"读取容器状态失败":            "Failed to read container status",
	"EYVESCLOUD 版本":       "EYVESCLOUD version",
	"Web 端口":              "Web port",
	"LXC 版本":              "LXC version",
	"暂无可用容器":              "No available containers",
	"忽略无效端口":              "Ignoring invalid port",
	"面板访问来源策略":            "Panel access source policy",
	"当前状态":                "Current status",
	"已启用":                 "enabled",
	"已关闭":                 "disabled",
	"允许来源":                "Allowed sources",
	"可信代理":                "Trusted proxies",
	"启用或修改白名单":            "Enable or update allowlist",
	"关闭白名单限制":             "Disable allowlist",
	"取消":                  "Cancel",
	"允许的 IP/CIDR，多个用逗号分隔": "Allowed IP/CIDR values, comma-separated",
	"可信代理 IP/CIDR，多个用逗号分隔，可留空": "Trusted proxy IP/CIDR values, comma-separated; optional",
	"白名单配置无效":           "Invalid allowlist configuration",
	"保存访问来源策略失败":        "Failed to save access source policy",
	"面板访问白名单已保存。":       "Panel access allowlist saved.",
	"面板访问白名单已关闭。":       "Panel access allowlist disabled.",
	"至少填写一个允许的 IP 或网段。": "Enter at least one allowed IP address or network.",
	"？": "? ",
	"。": ". ",
	"，": ", ",
	"：": ": ",
}

// Run starts the CLI interface.
func Run() {
	reader := bufio.NewReader(os.Stdin)

	for {
		if _, err := config.InitConfig(); err != nil {
			cliPrintf("重新加载配置失败: %v\n", err)
			waitEnter(reader)
		}
		refreshCLILanguage()
		clearScreen()
		printMenu()
		cliPrint("\n请选择操作 [1-13,l,0/q]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch strings.ToLower(input) {
		case "1":
			clearScreen()
			cliListContainers()
			waitEnter(reader)
		case "2":
			clearScreen()
			cliCreateContainer(reader)
			waitEnter(reader)
		case "3":
			clearScreen()
			cliStartContainer(reader)
			waitEnter(reader)
		case "4":
			clearScreen()
			cliStopContainer(reader)
			waitEnter(reader)
		case "5":
			clearScreen()
			cliRestartContainer(reader)
			waitEnter(reader)
		case "6":
			clearScreen()
			cliDeleteContainer(reader)
			waitEnter(reader)
		case "7":
			clearScreen()
			cliReinstallContainer(reader)
			waitEnter(reader)
		case "8":
			clearScreen()
			cliResetPassword(reader)
			waitEnter(reader)
		case "9":
			clearScreen()
			cliToggleWebPanel()
			waitEnter(reader)
		case "10":
			clearScreen()
			cliImportExistingContainers()
			waitEnter(reader)
		case "11":
			clearScreen()
			cliUpgradeSystem(reader)
			waitEnter(reader)
		case "12":
			clearScreen()
			cliUninstall(reader)
			return
		case "13":
			clearScreen()
			cliConfigurePanelAccess(reader)
			waitEnter(reader)
		case "0":
			clearScreen()
			cliShowInfo()
			waitEnter(reader)
		case "l", "lang", "language":
			clearScreen()
			cliSwitchLanguage(reader)
			waitEnter(reader)
		case "q", "exit", "quit":
			cliPrintln("再见")
			return
		default:
			cliPrintln("无效选择")
		}
	}
}

func printMenu() {
	webStatus := "启动"
	if isWebPanelRunning() {
		webStatus = "停止"
	}
	cliPrintln("")
	cliPrintln("  ==========================================")
	cliPrintln("       EyvesCloud - LXC 容器管理器")
	cliPrintln("  ==========================================")
	cliPrintln("")
	cliPrintf("  Web 面板: %s (端口 %d)\n", func() string {
		if isWebPanelRunning() {
			return "运行中"
		}
		return "已停止"
	}(), config.AppConfig.Port)
	cliPrintf("  当前版本: %s\n", version.Current())
	cliPrintln("")
	cliPrintln("  1. 查看容器列表")
	cliPrintln("  2. 创建容器")
	cliPrintln("  3. 开机容器")
	cliPrintln("  4. 关机容器")
	cliPrintln("  5. 重启容器")
	cliPrintln("  6. 删除容器")
	cliPrintln("  7. 重装容器系统")
	cliPrintln("  8. 重置 Web 管理员密码")
	cliPrintf("  9. %s Web 面板\n", webStatus)
	cliPrintln("  10. 导入现有 LXC 容器")
	cliPrintln("  11. 检查并升级 EYVESCLOUD")
	cliPrintln("  12. 卸载 EYVESCLOUD")
	cliPrintln("  13. 面板访问白名单")
	cliPrintln("  0. 系统信息")
	cliPrintln("  l. 切换语言")
	cliPrintln("  q. 退出")
}

func cliConfigurePanelAccess(reader *bufio.Reader) {
	cliPrintf("\n--- %s ---\n", cliT("面板访问来源策略"))
	policy := config.AppConfig.PanelAccessPolicy
	status := cliT("已关闭")
	if policy.Enabled {
		status = cliT("已启用")
	}
	cliPrintf("%s: %s\n", cliT("当前状态"), status)
	cliPrintf("%s: %s\n", cliT("允许来源"), strings.Join(policy.AllowedSources, ", "))
	cliPrintf("%s: %s\n", cliT("可信代理"), strings.Join(policy.TrustedProxies, ", "))
	cliPrintf("\n  1. %s\n", cliT("启用或修改白名单"))
	cliPrintf("  2. %s\n", cliT("关闭白名单限制"))
	cliPrintf("  0. %s\n", cliT("取消"))

	choice := promptString(reader, "请选择操作", "0")
	next := policy
	switch strings.TrimSpace(choice) {
	case "1":
		allowed := promptString(reader, "允许的 IP/CIDR，多个用逗号分隔", strings.Join(policy.AllowedSources, ","))
		allowedSources := splitPanelAccessEntries(allowed)
		if len(allowedSources) == 0 {
			cliPrintln("至少填写一个允许的 IP 或网段。")
			return
		}
		trusted := promptString(reader, "可信代理 IP/CIDR，多个用逗号分隔，可留空", strings.Join(policy.TrustedProxies, ","))
		next = config.PanelAccessPolicy{
			Enabled:        true,
			AllowedSources: allowedSources,
			TrustedProxies: splitPanelAccessEntries(trusted),
		}
	case "2":
		next.Enabled = false
	case "0", "":
		cliPrintln("已取消")
		return
	default:
		cliPrintln("无效选择")
		return
	}

	normalized, err := config.NormalizePanelAccessPolicy(next)
	if err != nil {
		cliPrintf("%s: %v\n", cliT("白名单配置无效"), err)
		return
	}
	previous := config.AppConfig.PanelAccessPolicy
	config.AppConfig.PanelAccessPolicy = normalized
	if err := config.SaveConfig(); err != nil {
		config.AppConfig.PanelAccessPolicy = previous
		cliPrintf("%s: %v\n", cliT("保存访问来源策略失败"), err)
		return
	}
	if normalized.Enabled {
		cliPrintln("面板访问白名单已保存。")
	} else {
		cliPrintln("面板访问白名单已关闭。")
	}
	if isWebPanelRunning() {
		restartWebPanelForConfigChange()
	}
}

func splitPanelAccessEntries(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func cliSwitchLanguage(reader *bufio.Reader) {
	cliPrintf("\n--- %s ---\n", cliT("切换语言"))
	cliPrintf("%s: %s\n", cliT("当前语言"), cliLanguageLabel(config.NormalizeLanguage(config.AppConfig.Language)))
	cliPrintln("  1. 简体中文")
	cliPrintln("  2. English")
	choice := promptString(reader, "请选择语言 [1/2]", func() string {
		if config.NormalizeLanguage(config.AppConfig.Language) == "en" {
			return "2"
		}
		return "1"
	}())

	next := "zh"
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "2", "en", "english":
		next = "en"
	case "1", "zh", "cn", "chinese":
		next = "zh"
	default:
		cliPrintln("无效选择")
		return
	}

	config.AppConfig.Language = next
	if err := config.SaveConfig(); err != nil {
		cliPrintf("保存语言失败: %v\n", err)
		return
	}
	_ = os.Setenv("EYVESCLOUD_LANG", next)
	refreshCLILanguage()
	cliPrintf("%s: %s\n", cliT("语言已切换为"), cliLanguageLabel(next))
	if isWebPanelRunning() {
		restartWebPanelForConfigChange()
	}
}

func cliListContainers() {
	containers, err := manager.ListContainers()
	if err != nil {
		cliPrintf("获取容器列表失败: %v\n", err)
		return
	}

	if len(containers) == 0 {
		cliPrintln("\n暂无容器")
		return
	}

	fmt.Println()
	fmt.Printf("%-18s %-10s %-18s %-6s %-10s %-10s %-16s\n", cliT("名称"), cliT("状态"), cliT("镜像"), "vCPU", cliT("内存(MB)"), cliT("磁盘(GB)"), "SSH")
	fmt.Println(strings.Repeat("-", 94))
	for _, c := range containers {
		ssh := "-"
		if c.SSHPort > 0 {
			ssh = fmt.Sprintf("%d->22", c.SSHPort)
		}
		fmt.Printf("%-18s %-10s %-18s %-6.2f %-10d %-10.2f %-16s\n",
			c.Name, c.Status, c.Template, c.VCPU, c.RAMMB, c.DiskGB, ssh)
	}
}

func cliCreateContainer(reader *bufio.Reader) {
	cliPrintln("\n--- 创建容器 ---")

	name := promptString(reader, "容器名称", "")
	if name == "" {
		cliPrintln("容器名称不能为空")
		return
	}

	templates := lxc.GetTemplates()
	cliPrintln("\n可用镜像:")
	for i, template := range templates {
		fmt.Printf("  %d. %s\n", i+1, template.Name)
	}

	tmplIdx := promptInt(reader, fmt.Sprintf("镜像 [1-%d]", len(templates)), 1)
	if tmplIdx < 1 || tmplIdx > len(templates) {
		cliPrintln("镜像选择无效")
		return
	}

	cfg := lxc.ContainerConfig{
		Name:             name,
		TemplateID:       templates[tmplIdx-1].ID,
		VCPU:             promptFloat(reader, "vCPU", 1),
		RAMMB:            promptInt(reader, "内存 (MB)", 512),
		DiskGB:           promptFloat(reader, "磁盘 (GB)", 10),
		NetworkBWMbps:    promptInt(reader, "网络带宽 (Mbps)", 100),
		MonthlyTrafficGB: promptInt(reader, "月流量 (GB)", 1000),
		IOSpeedMBps:      promptInt(reader, "IO 速度 (MB/s)", 500),
		ExtraPorts:       promptPortList(reader, "额外 NAT 端口，多个用逗号分隔"),
	}
	cfg.NormalizeResourceAliases()

	cliPrintf("\n正在创建容器 %s ...\n", name)
	if err := manager.CreateContainer(cfg); err != nil {
		cliPrintf("创建失败: %v\n", err)
		return
	}

	container := config.FindContainerByName(name)
	cliPrintf("容器 %s 创建成功\n", name)
	if container != nil {
		cliPrint(formatSSHAccess(container.SSHPort))
	}
	restartWebPanelForConfigChange()
}

func formatSSHAccess(sshPort int) string {
	if sshPort <= 0 {
		return "SSH: root, 端口未分配。密码已保存，请在 Web 面板中查看或重置。\n"
	}
	return fmt.Sprintf("SSH: root, port %d -> 22。密码已保存，请在 Web 面板中查看或重置。\n", sshPort)
}

func cliStartContainer(reader *bufio.Reader) {
	id, name := selectContainer(reader, "开机")
	if id == 0 {
		return
	}
	if err := manager.StartContainer(id); err != nil {
		cliPrintf("开机失败: %v\n", err)
		return
	}
	cliPrintf("容器 %s 已开机\n", name)
}

func cliStopContainer(reader *bufio.Reader) {
	id, name := selectContainer(reader, "关机")
	if id == 0 {
		return
	}
	if err := manager.StopContainer(id); err != nil {
		cliPrintf("关机失败: %v\n", err)
		return
	}
	cliPrintf("容器 %s 已关机\n", name)
}

func cliRestartContainer(reader *bufio.Reader) {
	id, name := selectContainer(reader, "重启")
	if id == 0 {
		return
	}
	if err := manager.RestartContainer(id); err != nil {
		cliPrintf("重启失败: %v\n", err)
		return
	}
	cliPrintf("容器 %s 已重启\n", name)
}

func cliDeleteContainer(reader *bufio.Reader) {
	id, name := selectContainer(reader, "删除")
	if id == 0 {
		return
	}
	confirm := promptString(reader, fmt.Sprintf("确认删除容器 %s？输入 yes 继续", name), "no")
	if strings.ToLower(confirm) != "yes" {
		cliPrintln("已取消")
		return
	}
	if err := manager.DestroyContainer(id); err != nil {
		cliPrintf("删除失败: %v\n", err)
		return
	}
	cliPrintf("容器 %s 已删除\n", name)
	restartWebPanelForConfigChange()
}

func cliReinstallContainer(reader *bufio.Reader) {
	id, name := selectContainer(reader, "重装")
	if id == 0 {
		return
	}

	templates := lxc.GetTemplates()
	cliPrintln("\n可用镜像:")
	for i, template := range templates {
		fmt.Printf("  %d. %s\n", i+1, template.Name)
	}

	tmplIdx := promptInt(reader, fmt.Sprintf("镜像 [1-%d]", len(templates)), 1)
	if tmplIdx < 1 || tmplIdx > len(templates) {
		cliPrintln("镜像选择无效")
		return
	}

	confirm := promptString(reader, fmt.Sprintf("确认重装容器 %s？输入 yes 继续", name), "no")
	if strings.ToLower(confirm) != "yes" {
		cliPrintln("已取消")
		return
	}

	if err := manager.ReinstallContainer(id, templates[tmplIdx-1].ID); err != nil {
		cliPrintf("重装失败: %v\n", err)
		return
	}
	cliPrintf("容器 %s 已重装\n", name)
	restartWebPanelForConfigChange()
}

func cliResetPassword(reader *bufio.Reader) {
	newPass := promptString(reader, "新的管理员密码（至少 6 位）", "")
	if len(newPass) < 6 {
		cliPrintln("密码至少需要 6 位")
		return
	}
	confirm := promptString(reader, "确认密码", "")
	if newPass != confirm {
		cliPrintln("两次输入的密码不一致")
		return
	}

	if err := config.ResetAdminPassword(newPass); err != nil {
		cliPrintf("重置失败: %v\n", err)
		return
	}
	cliPrintln("管理员密码已重置。")
	restartWebPanelForConfigChange()
}

func cliToggleWebPanel() {
	if isWebPanelRunning() {
		if err := stopService("eyvescloud"); err != nil {
			cliPrintf("停止 Web 面板失败: %v\n", err)
			return
		}
		cliPrintln("Web 面板已停止，LXC 容器不会受影响。")
		return
	}

	if err := startService("eyvescloud"); err != nil {
		cliPrintf("启动 Web 面板失败: %v\n", err)
		return
	}
	cliPrintln("Web 面板已启动")
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// githubReleaseListItem 用于面板版本列表展示（比升级用的 githubRelease 多带元数据）。
type githubReleaseListItem struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	HasAsset    bool   `json:"has_asset"`
}

// repoSource 描述多平台仓库的归一化视图，供 Release 抓取逻辑使用。
// 支持三种输入格式：
//   - "owner/name"          → 平台默认 github
//   - "codeberg:owner/name" → 显式指定平台（github/codeberg/gitee/gitlab）
//   - "https://codeberg.org/owner/name" → 从 URL 推断平台
type repoSource struct {
	Platform string // github | codeberg | gitee | gitlab
	Owner    string
	Repo     string
	// Raw 是用户原始输入（含 platform 前缀或完整 URL），用于校验日志。
	Raw string
}

// resolveRepoSource 把各种仓库标识格式归一化成 repoSource。
// 规则：
//   - 空 → 返回官方默认仓库（Codeberg: fenhaolost/eyves-vm-panel）
//   - "github:" / "gh:" 前缀 → GitHub
//   - "codeberg:" / "cb:" 前缀 → Codeberg
//   - "gitee:" / "gt:" 前缀 → Gitee
//   - "gitlab:" / "gl:" 前缀 → GitLab
//   - 完整 URL（https://xxx）→ 从 host 推断
//   - "owner/repo" → 默认 GitHub（第三方镜像/自建发布场景；官方仓库请用
//     "codeberg:fenhaolost/eyves-vm-panel" 或直接留空）
func resolveRepoSource(repo string) repoSource {
	raw := strings.TrimSpace(repo)
	platform := "github"
	var owner, name string

	// 带平台前缀。
	if idx := strings.Index(raw, ":"); idx > 0 && !strings.HasPrefix(raw, "http") {
		prefix := strings.ToLower(strings.TrimSpace(raw[:idx]))
		rest := strings.TrimSpace(raw[idx+1:])
		switch prefix {
		case "github", "gh":
			platform = "github"
		case "codeberg", "cb":
			platform = "codeberg"
		case "gitee", "gt":
			platform = "gitee"
		case "gitlab", "gl":
			platform = "gitlab"
		default:
			// 不认识的前缀 → 整体按 slug 处理（兼容用户把平台名写错的情况）。
			rest = raw
		}
		raw = rest
	}

	// 完整 URL。
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		// host 到第一个 / 之间的部分是 owner/repo。
		withoutScheme := raw
		if i := strings.Index(withoutScheme, "://"); i >= 0 {
			withoutScheme = withoutScheme[i+3:]
		}
		slash := strings.Index(withoutScheme, "/")
		if slash > 0 {
			host := strings.ToLower(withoutScheme[:slash])
			rest := withoutScheme[slash+1:]
			switch {
			case strings.Contains(host, "codeberg"):
				platform = "codeberg"
			case strings.Contains(host, "gitee"):
				platform = "gitee"
			case strings.Contains(host, "gitlab"):
				platform = "gitlab"
			default:
				platform = "github"
			}
			if idx := strings.Index(rest, "/"); idx > 0 {
				owner = rest[:idx]
				name = rest[idx+1:]
			} else {
				owner = rest
			}
		}
	} else if strings.Contains(raw, "/") {
		parts := strings.SplitN(raw, "/", 2)
		owner = parts[0]
		name = parts[1]
	}

	// 回退：没解析出 owner/repo → 官方默认仓库（Codeberg）。
	if owner == "" || name == "" {
		platform = "codeberg"
		owner = "fenhaolost"
		name = "eyves-vm-panel"
	}

	return repoSource{Platform: platform, Owner: owner, Repo: name, Raw: repo}
}

// platformReleaseURLs 返回该平台的 Release API 端点。
//
//	latest:  /releases/latest 或 tags fallback
//	tag:     /releases/tags/{tag} 或对应路径
//	list:    /releases?per_page=n 或 tags fallback
func platformReleaseURLs(s repoSource) (latest, tag, list string) {
	switch s.Platform {
	case "codeberg":
		latest = fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/releases/latest", s.Owner, s.Repo)
		// Gitea/Forgejo 按 tag 取发布必须带 /tags/ 段（/releases/{tag} 会 404）。
		tag = fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/releases/tags/%s", s.Owner, s.Repo, "%s")
		list = fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/releases?per_page=%%d", s.Owner, s.Repo)
	case "gitee":
		latest = fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/releases/latest", s.Owner, s.Repo)
		tag = fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/releases/%s", s.Owner, s.Repo, "%s")
		list = fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/releases?page=1&size=%%d", s.Owner, s.Repo)
	case "gitlab":
		// GitLab 没有 latest endpoint，latest 用 tags 排序；tag 也通过 releases/{tag}。
		latest = fmt.Sprintf("https://gitlab.com/api/v4/projects/%s%%2F%s/releases", s.Owner, s.Repo)
		tag = fmt.Sprintf("https://gitlab.com/api/v4/projects/%s%%2F%s/releases/%s", s.Owner, s.Repo, "%s")
		list = latest
	default: // github
		latest = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", s.Owner, s.Repo)
		tag = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", s.Owner, s.Repo, "%s")
		list = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=%%d", s.Owner, s.Repo)
	}
	return
}

// setPlatformRequestHeaders 给 Release API 请求设置通用头：User-Agent + 可选 token。
// Token 来源优先级：EYVESCLOUD_{PLATFORM}_TOKEN → 通用 EYVESCLOUD_GITHUB_TOKEN / GITHUB_TOKEN。
func setPlatformRequestHeaders(req *http.Request, platform string) {
	req.Header.Set("User-Agent", "eyvescloud-updater/"+version.Current())
	switch platform {
	case "codeberg":
		if t := strings.TrimSpace(os.Getenv("EYVESCLOUD_CODEBERG_TOKEN")); t != "" {
			req.Header.Set("Authorization", "token "+t)
			return
		}
	case "gitee":
		if t := strings.TrimSpace(os.Getenv("EYVESCLOUD_GITEE_TOKEN")); t != "" {
			req.Header.Set("Authorization", "token "+t)
			return
		}
	case "gitlab":
		if t := strings.TrimSpace(os.Getenv("EYVESCLOUD_GITLAB_TOKEN")); t != "" {
			req.Header.Set("PRIVATE-TOKEN", t)
			return
		}
	}
	// 回退通用 GitHub token（对非 GitHub 平台的请求即使带了也无害，Gitee 会忽略）。
	setGitHubRequestHeaders(req)
}

// buildRepoURL 把 repoSource 转成浏览器可访问的仓库 URL（面板提示用）。
func buildRepoURL(s repoSource) string {
	switch s.Platform {
	case "codeberg":
		return fmt.Sprintf("https://codeberg.org/%s/%s", s.Owner, s.Repo)
	case "gitee":
		return fmt.Sprintf("https://gitee.com/%s/%s", s.Owner, s.Repo)
	case "gitlab":
		return fmt.Sprintf("https://gitlab.com/%s/%s", s.Owner, s.Repo)
	default:
		return fmt.Sprintf("https://github.com/%s/%s", s.Owner, s.Repo)
	}
}

// parseReleaseJSON 把各平台 release JSON 统一成 githubRelease。
// 不同平台字段名不同：
//   - GitHub / Codeberg (Gitea) / Gitee: tag_name, name, html_url, assets[].name, assets[].browser_download_url
//   - GitLab:                           tag_name, name, description, assets.links[].name, assets.links[].direct_asset_url
func parseReleaseJSON(raw map[string]any, platform string) githubRelease {
	r := githubRelease{}
	if v, _ := raw["tag_name"].(string); v != "" {
		r.TagName = v
	}
	if v, _ := raw["name"].(string); v != "" {
		r.Name = v
	}
	if platform == "gitlab" {
		// GitLab HTML URL 不是 html_url，用 web_url（可选）。
		if v, _ := raw["web_url"].(string); v != "" {
			r.HTMLURL = v
		} else if v, _ := raw["description"].(string); v != "" {
			r.Name = v
		}
	} else {
		if v, _ := raw["html_url"].(string); v != "" {
			r.HTMLURL = v
		}
	}

	// 资产数组：GitHub/Codeberg/Gitee 顶层 assets；GitLab 在 assets.links。
	var rawAssets []any
	if arr, _ := raw["assets"].([]any); arr != nil {
		rawAssets = arr
	} else if m, _ := raw["assets"].(map[string]any); m != nil {
		if links, _ := m["links"].([]any); links != nil {
			rawAssets = links
		}
	}
	for _, a := range rawAssets {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		asset := struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{}
		if v, _ := m["name"].(string); v != "" {
			asset.Name = v
		}
		switch platform {
		case "gitlab":
			if v, _ := m["direct_asset_url"].(string); v != "" {
				asset.BrowserDownloadURL = v
			}
		default:
			if v, _ := m["browser_download_url"].(string); v != "" {
				asset.BrowserDownloadURL = v
			}
		}
		r.Assets = append(r.Assets, asset)
	}
	return r
}

// parseReleasesListJSON 把多平台 releases 数组统一成 []githubReleaseListItem。
func parseReleasesListJSON(raw []any, platform string) []githubReleaseListItem {
	items := make([]githubReleaseListItem, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		r := parseReleaseJSON(m, platform)
		item := githubReleaseListItem{
			TagName: r.TagName,
			Name:    r.Name,
			HTMLURL: r.HTMLURL,
		}
		if v, _ := m["published_at"].(string); v != "" {
			item.PublishedAt = v
		} else if v, _ := m["released_at"].(string); v != "" {
			item.PublishedAt = v // GitLab
		}
		if v, _ := m["prerelease"].(bool); v {
			item.Prerelease = true
		}

		// 检查目标资产是否存在。
		assetName, _ := releaseArchiveAssetName(runtime.GOARCH)
		if assetName != "" {
			item.HasAsset = findReleaseAsset(&r, assetName) != ""
		}
		items = append(items, item)
	}
	return items
}

// validateRepoSlug 扩展版：支持 platform:owner/repo / https://host/owner/repo / owner/repo。
// 严格白名单字符集，防止拼进 API URL 造成 SSRF/路径注入。
func validateRepoSlug(repo string) bool {
	// 空 = 默认仓库，合法（调用方会用 version.Repo 或 EYVESCLOUD_REPO）。
	if strings.TrimSpace(repo) == "" {
		return true
	}
	if len(repo) > 300 {
		return false
	}
	src := resolveRepoSource(repo)
	// 校验 owner + repo 段字符。
	for _, p := range []string{src.Owner, src.Repo} {
		if p == "" {
			return false
		}
		for _, ch := range p {
			switch {
			case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
			case ch == '-' || ch == '_' || ch == '.':
			default:
				return false
			}
		}
	}
	return true
}

// validateReleaseTag 校验 release tag：禁止斜杠、空格与控制字符，
// 防止拼进 /releases/tags/{tag} 时篡改请求路径。
func validateReleaseTag(tag string) bool {
	if tag == "" || len(tag) > 200 {
		return false
	}
	for _, ch := range tag {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_' || ch == '.':
		default:
			return false
		}
	}
	return true
}

// fetchReleasesList 拉取仓库最近 release 列表（供面板选择目标版本）。
func fetchReleasesList(repo string, limit int) ([]githubReleaseListItem, error) {
	if !validateRepoSlug(repo) {
		return nil, fmt.Errorf("无效的仓库标识: %q", repo)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	src := resolveRepoSource(repo)
	_, _, listURL := platformReleaseURLs(src)
	listURL = fmt.Sprintf(listURL, limit)
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	setPlatformRequestHeaders(req, src.Platform)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// 网络失败时尝试 fallback（latest release，只拿最新版一条）。
		if fallback, fbErr := fetchReleasesListFallback(repo); fbErr == nil {
			return fallback, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// 403/429 = 未认证限流 → fallback 到 latest release。
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if fallback, fbErr := fetchReleasesListFallback(repo); fbErr == nil {
				return fallback, nil
			}
		}
		return nil, fmt.Errorf("%s Release API 返回 %s: %s", strings.Title(src.Platform), resp.Status, strings.TrimSpace(string(body)))
	}

	var raw []any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return parseReleasesListJSON(raw, src.Platform), nil
}

// fetchReleasesListFallback 是版本列表的降级通道：api.github.com 限流 / 不可达时，
// 通过 github.com/{repo}/releases/latest 的 302 跳转解析最新版 tag，
// 合成仅含最新版的单条列表（资产下载走 releases/latest/download/ 固定地址）。
// 用户至少能看到并升级到最新版，而不是整个升级页报错。
func fetchReleasesListFallback(repo string) ([]githubReleaseListItem, error) {
	rel, err := fetchLatestReleaseFallback(repo, "")
	if err != nil {
		return nil, err
	}
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		assetName = ""
	}
	return []githubReleaseListItem{{
		TagName:     rel.TagName,
		Name:        rel.Name,
		HTMLURL:     rel.HTMLURL,
		PublishedAt: "",
		Prerelease:  false,
		HasAsset:    assetName != "",
	}}, nil
}

// fetchReleaseByTag 获取指定 tag 的 release（供面板升级到非最新版本）。
func fetchReleaseByTag(repo, tag string) (*githubRelease, error) {
	if !validateRepoSlug(repo) {
		return nil, fmt.Errorf("无效的仓库标识: %q", repo)
	}
	if !validateReleaseTag(tag) {
		return nil, fmt.Errorf("无效的版本标签: %q", tag)
	}
	src := resolveRepoSource(repo)
	_, tagURL, _ := platformReleaseURLs(src)
	url := fmt.Sprintf(tagURL, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	setPlatformRequestHeaders(req, src.Platform)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if fallback := releaseByTagFallback(repo, tag); fallback != nil {
			return fallback, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if fallback := releaseByTagFallback(repo, tag); fallback != nil {
				return fallback, nil
			}
		}
		return nil, fmt.Errorf("%s Release API 返回 %s: %s", strings.Title(src.Platform), resp.Status, strings.TrimSpace(string(body)))
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	release := parseReleaseJSON(raw, src.Platform)
	return &release, nil
}

// releaseByTagFallback 在 API 限流时解析最新版：目标 tag 与最新版一致才返回合成
// release（资产走 releases/latest/download/ 固定地址），否则返回 nil 表示无法降级。
func releaseByTagFallback(repo, tag string) *githubRelease {
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return nil
	}
	rel, err := fetchLatestReleaseFallback(repo, assetName)
	if err != nil {
		return nil
	}
	if !sameVersion(rel.TagName, tag) && rel.TagName != tag {
		return nil
	}
	return rel
}

// ValidateRepoSlug / ValidateReleaseTag / FetchReleasesList 是给 api 包用的导出包装。
func ValidateRepoSlug(repo string) bool { return validateRepoSlug(repo) }

func ValidateReleaseTag(tag string) bool { return validateReleaseTag(tag) }

type GithubReleaseListItem = githubReleaseListItem

func FetchReleasesList(repo string, limit int) ([]GithubReleaseListItem, error) {
	return fetchReleasesList(repo, limit)
}

// resolveUpdateRepo 解析本次更新使用的仓库标识，优先级：
//  1. 环境变量 EYVESCLOUD_REPO（部署级覆盖 / 紧急切换源）
//  2. 面板配置 update_source（platform + owner/repo，管理员可在设置中指定镜像）
//  3. 二进制内置官方仓库 version.Repo（codeberg:fenhaolost/eyves-vm-panel）
//
// 三者统一走 resolveRepoSource，因此都支持 "owner/name" 与 "platform:owner/name"。
func resolveUpdateRepo() string {
	if repo := strings.TrimSpace(os.Getenv("EYVESCLOUD_REPO")); repo != "" {
		return repo
	}
	config.AppConfigMu.RLock()
	var src config.UpdateSource
	if config.AppConfig != nil {
		// 防御性判空：配置尚未加载（CLI 早期路径 / 测试）或加载失败时不能 panic。
		src = config.NormalizeUpdateSource(config.AppConfig.UpdateSource)
	}
	config.AppConfigMu.RUnlock()
	if strings.TrimSpace(src.Platform) != "" && strings.TrimSpace(src.Owner) != "" && strings.TrimSpace(src.Repo) != "" {
		return src.Platform + ":" + src.Owner + "/" + src.Repo
	}
	return version.Repo
}

func cliUpgradeSystem(reader *bufio.Reader) {
	cliPrintln("\n--- 检查并升级 EyvesCloud ---")
	cliPrintln("升级只会替换 /usr/local/bin/eyvescloud，并保留 /root/.eyvescloud 里的配置、容器数据和任务记录。")

	if os.Geteuid() != 0 {
		cliPrintln("升级需要 root 权限。请使用: sudo eyvescloud cli")
		return
	}

	repo := resolveUpdateRepo()
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		cliPrintf("当前架构不支持自动升级: %s\n", runtime.GOARCH)
		return
	}
	current := version.Current()
	cliPrintf("当前版本: %s\n", current)
	cliPrintf("检查仓库: %s\n", buildRepoURL(resolveRepoSource(repo)))

	release, err := fetchLatestRelease(repo, assetName)
	if err != nil {
		cliPrintf("检查最新版本失败: %v\n", err)
		return
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		cliPrintln("GitHub Release 缺少 tag_name，无法判断最新版本。")
		return
	}
	cliPrintf("最新版本: %s\n", latest)
	if release.HTMLURL != "" {
		cliPrintf("发布页面: %s\n", release.HTMLURL)
	}

	assetURL := findReleaseAsset(release, assetName)
	if assetURL == "" {
		cliPrintf("最新 Release 没有找到 %s，无法自动升级。\n", assetName)
		return
	}

	if sameVersion(current, latest) {
		cliPrintln("当前已经是最新版本。")
		confirm := promptString(reader, "是否仍然重新安装最新版本？输入 reinstall 继续", "no")
		if strings.ToLower(confirm) != "reinstall" {
			cliPrintln("已取消。")
			return
		}
	} else {
		confirm := promptString(reader, "输入 upgrade 开始升级", "no")
		if strings.ToLower(confirm) != "upgrade" {
			cliPrintln("已取消。")
			return
		}
	}

	if err := upgradeFromReleaseAsset(assetURL, latest, assetName, findChecksumsURL(release, assetName)); err != nil {
		cliPrintf("升级失败: %v\n", err)
		return
	}
	cliPrintf("升级完成: %s -> %s\n", current, latest)
	cliPrintln("原有数据已保留，Web 服务已重启。")
}

func fetchLatestRelease(repo, assetName string) (*githubRelease, error) {
	src := resolveRepoSource(repo)
	latestURL, _, _ := platformReleaseURLs(src)
	req, err := http.NewRequest(http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	setPlatformRequestHeaders(req, src.Platform)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if fallback, fallbackErr := fetchLatestReleaseFallback(repo, assetName); fallbackErr == nil {
			return fallback, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		apiErr := fmt.Errorf("%s Release API 返回 %s: %s", strings.Title(src.Platform), resp.Status, strings.TrimSpace(string(body)))
		if fallback, fallbackErr := fetchLatestReleaseFallback(repo, assetName); fallbackErr == nil {
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
				cliPrintln(fmt.Sprintf("%s API 被限流，已切换到备用检查方式。", strings.Title(src.Platform)))
			} else {
				cliPrintln(fmt.Sprintf("%s API 不可用，已切换到备用检查方式。", strings.Title(src.Platform)))
			}
			return fallback, nil
		}
		return nil, apiErr
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		// GitLab / Codeberg 在某些场景返回数组（按 tag 排序），取第一条。
		var arr []any
		if arrErr := json.NewDecoder(strings.NewReader(readAllString(resp.Body))).Decode(&arr); arrErr == nil && len(arr) > 0 {
			first, _ := arr[0].(map[string]any)
			raw = first
		} else {
			return nil, err
		}
	}
	release := parseReleaseJSON(raw, src.Platform)
	return &release, nil
}

// readAllString 把 io.Reader 读成 string（fetchLatestRelease 里 resp.Body 已被读一次，
// 但上面 resp.Body.Close 已经执行过——实际上 resp.Body 是新的 request，这里需要的是上面 json.Decoder 失败时。
// 简化处理：resp.Body 在 json.Decoder 失败时位置未知，直接返回数组 decode 结果即可。
func readAllString(r io.Reader) string { b, _ := io.ReadAll(r); return string(b) }

func fetchLatestReleaseFallback(repo, assetName string) (*githubRelease, error) {
	src := resolveRepoSource(repo)
	web := buildRepoURL(src)

	// GitLab 无 /releases/latest；用 permalink/latest，其余平台用 /releases/latest 302。
	latestPath := "/releases/latest"
	if src.Platform == "gitlab" {
		latestPath = "/-/releases/permalink/latest"
	}

	req, err := http.NewRequest(http.MethodGet, web+latestPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "eyvescloud-updater/"+version.Current())

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s releases/latest 返回 %s", web, resp.Status)
	}

	tag := latestTagFromPath(resp.Request.URL.Path)
	if tag == "" {
		return nil, fmt.Errorf("无法从 %s releases/latest 跳转结果解析最新版本", web)
	}

	return &githubRelease{
		TagName: tag,
		Name:    tag,
		HTMLURL: releaseTagPageURL(src, tag),
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               assetName,
				BrowserDownloadURL: platformReleaseDownloadURL(src, "latest", assetName),
			},
		},
	}, nil
}

// platformReleaseDownloadURL 返回某平台 Releases 产物的下载地址。
// tag 传 "latest" 时使用各平台的 latest 便捷路径。
func platformReleaseDownloadURL(s repoSource, tag, asset string) string {
	web := buildRepoURL(s)
	if s.Platform == "gitlab" {
		// GitLab：/‑/releases/{tag}/downloads/{asset}（tag 为 latest 时用 permalink）。
		if tag == "latest" {
			return web + "/-/releases/permalink/latest/downloads/" + asset
		}
		return web + "/-/releases/" + tag + "/downloads/" + asset
	}
	return web + "/releases/download/" + tag + "/" + asset
}

// releaseTagPageURL 返回某个 release tag 的网页地址（GitLab 路径不同）。
func releaseTagPageURL(s repoSource, tag string) string {
	web := buildRepoURL(s)
	if s.Platform == "gitlab" {
		return web + "/-/releases/" + tag
	}
	return web + "/releases/tag/" + tag
}

func latestTagFromPath(path string) string {
	for _, marker := range []string{"/releases/tag/", "/-/releases/", "/releases/"} {
		if marker == "/releases/" && strings.Contains(path, "/releases/tag/") {
			continue
		}
		idx := strings.Index(path, marker)
		if idx < 0 {
			continue
		}
		tag := strings.TrimSpace(path[idx+len(marker):])
		if slash := strings.Index(tag, "/"); slash >= 0 {
			tag = tag[:slash]
		}
		if tag == "" || tag == "latest" || tag == "permalink" {
			continue
		}
		return tag
	}
	return ""
}

func setGitHubRequestHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "eyvescloud-updater/"+version.Current())
	token := strings.TrimSpace(os.Getenv("EYVESCLOUD_GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func releaseArchiveAssetName(goarch string) (string, error) {
	switch goarch {
	case "amd64", "arm64":
		return fmt.Sprintf("eyvescloud-linux-%s.tar.gz", goarch), nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}
}

func findReleaseAsset(release *githubRelease, name string) string {
	for _, asset := range release.Assets {
		if asset.Name == name && asset.BrowserDownloadURL != "" {
			return asset.BrowserDownloadURL
		}
	}
	return ""
}

// ---- 升级包完整性校验（审计 H-2 修复）----
//
// 发行版流程会为每个架构产物生成 SHA256SUMS 并作为 release asset 发布。
// 升级路径策略（2026-09-30 修订，F-7：默认严格）：
//   - 能取到校验清单：比对 SHA-256，不匹配即中止；且若构建内嵌了签发公钥，
//     还会对清单做 ed25519 验签（签名缺失/无效均中止）。
//   - 完全取不到校验清单：默认**中止**（生产面板的自升级不允许无校验的替换）；
//     自签仓库/测试可用 EYVESCLOUD_UPDATE_ALLOW_UNVERIFIED=1 显式放行。
const (
	updateAllowUnverifiedEnv = "EYVESCLOUD_UPDATE_ALLOW_UNVERIFIED"
	updateRequireVerifyEnv   = "EYVESCLOUD_REQUIRE_VERIFY"
)

// checksumAssetNames 是各平台可能使用的校验清单文件名的候选（大小写不敏感）。
var checksumAssetNames = []string{"SHA256SUMS", "SHA256SUMS.txt", "sha256sums.txt", "checksums.txt", "checksums.sha256"}

// findChecksumsURL 从 release 产物中找出校验清单下载地址；没有则返回空串。
// 支持两种发布习惯：汇总清单（SHA256SUMS 等）与单文件校验值（<asset>.sha256）。
func findChecksumsURL(release *githubRelease, assetName string) string {
	if release == nil {
		return ""
	}
	perAsset := strings.ToLower(strings.TrimSpace(assetName)) + ".sha256"
	for _, asset := range release.Assets {
		name := strings.TrimSpace(asset.Name)
		if strings.EqualFold(name, perAsset) {
			return strings.TrimSpace(asset.BrowserDownloadURL)
		}
		for _, candidate := range checksumAssetNames {
			if strings.EqualFold(name, candidate) {
				return strings.TrimSpace(asset.BrowserDownloadURL)
			}
		}
	}
	return ""
}

func updateAllowUnverified() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(updateAllowUnverifiedEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// verifyReleaseArchive 校验升级包 SHA-256 + 清单签名；checksumsURL 为空表示该
// Release 未发布校验清单 —— 默认中止（严格模式），ALLOW_UNVERIFIED=1 显式放行。
func verifyReleaseArchive(archivePath, assetName, checksumsURL string) error {
	if strings.TrimSpace(checksumsURL) == "" {
		if updateAllowUnverified() {
			cliPrintf("警告：该 Release 未提供 %s 校验清单，已按 %s=1 放行（不推荐；仅限自签仓库/测试）。\n",
				checksumAssetNames[0], updateAllowUnverifiedEnv)
			return nil
		}
		return fmt.Errorf("目标 Release 未提供 %s 校验清单，升级已中止（生产升级不允许无校验替换；自签仓库/测试请设置 %s=1）",
			checksumAssetNames[0], updateAllowUnverifiedEnv)
	}
	sums, err := fetchExpectedChecksumWithBody(checksumsURL, assetName)
	if err != nil {
		if updateAllowUnverified() {
			cliPrintf("警告：获取校验清单失败（%v），已按 %s=1 放行（不推荐）。\n", err, updateAllowUnverifiedEnv)
			return nil
		}
		return fmt.Errorf("获取校验清单失败，升级已中止：%w（自签仓库/测试可设置 %s=1）", err, updateAllowUnverifiedEnv)
	}
	// F-7：构建内嵌签发公钥时，对清单做 ed25519 验签（签名缺失/无效均中止）。
	if err := verifyReleaseSignature(checksumsURL, sums.raw); err != nil {
		return err
	}
	expected := sums.checksum
	actual, err := fileSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("计算升级包校验值失败: %w", err)
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("升级包完整性校验失败（SHA-256 不匹配）：期望 %s，实际 %s，已中止升级", expected, actual)
	}
	cliPrintf("升级包校验通过（SHA-256 %s）\n", actual)
	return nil
}

// updateRequireVerify 严格模式开关：取不到校验清单时中止升级。
func updateRequireVerify() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(updateRequireVerifyEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// releaseChecksums 是校验清单的解析结果：期望哈希 + 清单原始字节（用于验签）。
type releaseChecksums struct {
	checksum string
	raw      []byte
}

// fetchExpectedChecksumWithBody 下载校验清单，返回 assetName 的期望 SHA-256 与清单原文。
func fetchExpectedChecksumWithBody(checksumsURL, assetName string) (releaseChecksums, error) {
	req, err := http.NewRequest(http.MethodGet, checksumsURL, nil)
	if err != nil {
		return releaseChecksums{}, err
	}
	req.Header.Set("User-Agent", "eyvescloud-updater/"+version.Current())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return releaseChecksums{}, fmt.Errorf("下载校验清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseChecksums{}, fmt.Errorf("下载校验清单失败：HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return releaseChecksums{}, fmt.Errorf("读取校验清单失败: %w", err)
	}
	want := strings.TrimSpace(filepath.Base(assetName))
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 2 && isHexSHA256(fields[0]):
			// 标准格式：<hash>  <filename>（filename 可能带路径，按 basename 比较）
			if strings.TrimSpace(filepath.Base(fields[len(fields)-1])) == want {
				return releaseChecksums{checksum: strings.ToLower(fields[0]), raw: body}, nil
			}
		case len(fields) == 4 && strings.EqualFold(fields[1], "("+want+")") && strings.HasSuffix(fields[2], "="):
			// BSD 格式：SHA256 (filename) = <hash>
			if isHexSHA256(fields[3]) {
				return releaseChecksums{checksum: strings.ToLower(fields[3]), raw: body}, nil
			}
		}
	}
	return releaseChecksums{}, fmt.Errorf("校验清单中未找到 %s 的 SHA-256 记录，已中止升级", want)
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9', ch >= 'a' && ch <= 'f', ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

// fileSHA256 计算本地文件 SHA-256（小写 hex）。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func upgradeFromReleaseAsset(assetURL, latest, assetName, checksumsURL string) error {
	tmpDir, err := os.MkdirTemp("", "eyvescloud-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	cliPrintln("正在下载升级包...")
	if err := downloadFile(assetURL, archivePath); err != nil {
		return err
	}
	if err := verifyReleaseArchive(archivePath, assetName, checksumsURL); err != nil {
		return err
	}

	cliPrintln("正在解压升级包...")
	if out, err := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir).CombinedOutput(); err != nil {
		return fmt.Errorf("解压失败: %v, output: %s", err, string(out))
	}

	newBinary, err := findFile(tmpDir, "eyvescloud")
	if err != nil {
		return err
	}

	backupDir := eyvescloudBackupDir
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	backupName := fmt.Sprintf("eyvescloud.%s.%s", safeReleaseBackupComponent(latest), time.Now().Format("20060102-150405"))
	if _, err := os.Stat("/usr/local/bin/eyvescloud"); err == nil {
		backupPath, err := copyFileToBackup("/usr/local/bin/eyvescloud", backupName, 0755)
		if err != nil {
			return fmt.Errorf("备份旧二进制失败: %w", err)
		}
		cliPrintf("旧版本已备份: %s\n", backupPath)
	}

	cliPrintln("正在替换二进制...")
	if err := stopService("eyvescloud"); err != nil {
		cliPrintf("停止 Web 服务失败，继续尝试替换: %v\n", err)
	}
	tmpBin := eyvescloudNewBinaryPath
	if err := copyFileToUpgradeTemp(newBinary, 0755); err != nil {
		return err
	}
	if err := os.Rename(tmpBin, "/usr/local/bin/eyvescloud"); err != nil {
		return err
	}
	if err := os.Chmod("/usr/local/bin/eyvescloud", 0755); err != nil {
		return err
	}

	if err := restartService("eyvescloud"); err != nil {
		return fmt.Errorf("二进制已替换，但重启 Web 服务失败: %w", err)
	}
	return nil
}

// SelfUpdateOnce 非交互地检查并升级到最新版本（供被控节点自动更新调用）。
// 与菜单的「检查并升级」复用同一套逻辑，但不需要终端确认。
// 返回 latestVersion 表示升级完成，nil 表示无需升级或已是最新。
func SelfUpdateOnce() (newVersion string, upgraded bool, err error) {
	repo := resolveUpdateRepo()
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return "", false, err
	}
	current := version.Current()

	release, err := fetchLatestRelease(repo, assetName)
	if err != nil {
		return "", false, fmt.Errorf("检查最新版本失败: %w", err)
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		return "", false, fmt.Errorf("GitHub Release 缺少 tag_name，无法判断最新版本")
	}
	if sameVersion(current, latest) {
		return latest, false, nil
	}
	assetURL := findReleaseAsset(release, assetName)
	if assetURL == "" {
		return "", false, fmt.Errorf("最新 Release 没有找到 %s，无法自动升级", assetName)
	}
	if err := upgradeFromReleaseAsset(assetURL, latest, assetName, findChecksumsURL(release, assetName)); err != nil {
		return "", false, err
	}
	return latest, true, nil
}

// PanelSelfUpdateOnce 供面板「有更新 → 立即更新」按钮调用。
// 与 SelfUpdateOnce 的区别在于升级顺序：本机面板进程就是被升级的服务，
// 若先 stopService 再替换，会把正在执行升级的自身进程杀掉，导致替换中断。
// 因此这里采用「先就地替换二进制、再 detached 式触发 systemctl restart」，
// 由 systemd 统一完成"停旧起新"，新二进制在重启前就已就位。
func PanelSelfUpdateOnce() (newVersion string, upgraded bool, err error) {
	repo := resolveUpdateRepo()
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return "", false, err
	}
	current := version.Current()

	release, err := fetchLatestRelease(repo, assetName)
	if err != nil {
		return "", false, fmt.Errorf("检查最新版本失败: %w", err)
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		return "", false, fmt.Errorf("GitHub Release 缺少 tag_name，无法判断最新版本")
	}
	if sameVersion(current, latest) {
		return latest, false, nil
	}
	assetURL := findReleaseAsset(release, assetName)
	if assetURL == "" {
		return "", false, fmt.Errorf("最新 Release 没有找到 %s，无法自动升级", assetName)
	}
	if err := upgradeFromReleaseAssetInPlace(assetURL, latest, assetName, findChecksumsURL(release, assetName)); err != nil {
		return "", false, err
	}
	return latest, true, nil
}

// PanelSelfUpdateTo 供面板「选择仓库 + 指定版本」升级调用。
// repo 为 "owner/name"（空 = 默认仓库）；tag 为目标版本（空 = 最新版本）。
// 显式指定 tag 时允许同版本重装（用户主动选择的回滚/修复场景）。
func PanelSelfUpdateTo(repo, tag string) (newVersion string, upgraded bool, err error) {
	if repo == "" {
		repo = resolveUpdateRepo()
	}
	if !validateRepoSlug(repo) {
		return "", false, fmt.Errorf("无效的仓库标识: %q", repo)
	}
	if tag != "" && !validateReleaseTag(tag) {
		return "", false, fmt.Errorf("无效的版本标签: %q", tag)
	}
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return "", false, err
	}
	current := version.Current()

	var release *githubRelease
	if tag == "" {
		release, err = fetchLatestRelease(repo, assetName)
	} else {
		release, err = fetchReleaseByTag(repo, tag)
	}
	if err != nil {
		return "", false, fmt.Errorf("获取目标版本失败: %w", err)
	}
	target := strings.TrimSpace(release.TagName)
	if target == "" {
		return "", false, fmt.Errorf("GitHub Release 没有 tag_name，无法升级")
	}
	// 显式指定 tag 时不做同版本短路（允许重装）；仅"最新版"路径保持原语义。
	if tag == "" && sameVersion(current, target) {
		return target, false, nil
	}
	assetURL := findReleaseAsset(release, assetName)
	if assetURL == "" {
		return "", false, fmt.Errorf("目标 Release %s 没有找到 %s，无法升级", target, assetName)
	}
	if err := upgradeFromReleaseAssetInPlace(assetURL, target, assetName, findChecksumsURL(release, assetName)); err != nil {
		return "", false, err
	}
	return target, true, nil
}

// upgradeFromReleaseAssetInPlace 与 upgradeFromReleaseAsset 功能相同，
// 但先替换二进制、后触发 systemctl restart，避免先停服务导致自身进程被杀、
// 替换动作无法完成。替换成功后用 detached 命令触发 restart（不阻塞、不等待），
// 由 systemd 完成新旧进程切换。
func upgradeFromReleaseAssetInPlace(assetURL, latest, assetName, checksumsURL string) error {
	if !commandExists("systemctl") {
		return fmt.Errorf("未检测到 systemctl，无法在面板内自动重启服务；请使用 install.sh 或 CLI 升级")
	}
	tmpDir, err := os.MkdirTemp("", "eyvescloud-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	cliPrintln("正在下载升级包...")
	if err := downloadFile(assetURL, archivePath); err != nil {
		return err
	}
	if err := verifyReleaseArchive(archivePath, assetName, checksumsURL); err != nil {
		return err
	}

	cliPrintln("正在解压升级包...")
	if out, err := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir).CombinedOutput(); err != nil {
		return fmt.Errorf("解压失败: %v, output: %s", err, string(out))
	}

	newBinary, err := findFile(tmpDir, "eyvescloud")
	if err != nil {
		return err
	}

	backupDir := eyvescloudBackupDir
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	backupName := fmt.Sprintf("eyvescloud.%s.%s", safeReleaseBackupComponent(latest), time.Now().Format("20060102-150405"))
	if _, err := os.Stat("/usr/local/bin/eyvescloud"); err == nil {
		backupPath, err := copyFileToBackup("/usr/local/bin/eyvescloud", backupName, 0755)
		if err != nil {
			return fmt.Errorf("备份旧二进制失败: %w", err)
		}
		cliPrintf("旧版本已备份: %s\n", backupPath)
	}

	// 就地替换：先写入临时文件再 rename 覆盖，正在运行的进程不受影响。
	tmpBin := eyvescloudNewBinaryPath
	if err := copyFileToUpgradeTemp(newBinary, 0755); err != nil {
		return err
	}
	if err := os.Rename(tmpBin, "/usr/local/bin/eyvescloud"); err != nil {
		return err
	}
	if err := os.Chmod("/usr/local/bin/eyvescloud", 0755); err != nil {
		return err
	}

	// detached 触发 systemctl restart：不等待，避免本进程被杀导致调用栈中断。
	cliPrintln("已替换二进制，正在重启 Web 服务...")
	cmd := exec.Command("systemctl", "restart", "eyvescloud")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("二进制已替换，但触发重启失败: %w", err)
	}
	return nil
}

// CheckUpdateResult 描述一次版本检查的结论，供面板 API 展示，无需改动二进制。
type CheckUpdateResult struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// HasUpdate 为 true 说明仓库存在比当前更新的版本；
	// 仅在与最新版本比较有意义且不相等时为 true。
	HasUpdate bool `json:"has_update"`
	// Err 在检查失败（如网络不可达、API 限流）时非空，供调用方决定如何提示。
	Err string `json:"err,omitempty"`
}

// CheckForUpdate 只做版本检测，不下载、不替换、不重启。
// 该函数供面板「系统设置」的版本检测使用；真正的升级由菜单或 install.sh 完成。
func CheckForUpdate() CheckUpdateResult {
	repo := resolveUpdateRepo()
	current := version.Current()
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return CheckUpdateResult{Current: current, Err: err.Error()}
	}
	release, err := fetchLatestRelease(repo, assetName)
	if err != nil {
		return CheckUpdateResult{Current: current, Err: err.Error()}
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		return CheckUpdateResult{Current: current, Err: "Release 缺失 tag_name"}
	}
	// 主动将当前版本与“目标”版本比较（同为大版本路径），返回是否需要更新。
	hasUpdate := !sameVersion(current, latest)
	return CheckUpdateResult{Current: current, Latest: latest, HasUpdate: hasUpdate}
}

func downloadFile(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	setGitHubRequestHeaders(req)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，HTTP %s", resp.Status)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func findFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("升级包内未找到 eyvescloud 二进制")
	}
	return found, nil
}

func copyFileToBackup(src, fileName string, mode os.FileMode) (string, error) {
	if fileName == "" || strings.Contains(fileName, "/") || strings.Contains(fileName, "\\") || strings.Contains(fileName, "..") {
		return "", fmt.Errorf("unsafe backup file name: %s", fileName)
	}
	dst := filepath.Join(eyvescloudBackupDir, fileName)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return "", err
	}
	if err := copyIntoOpenFile(src, out, mode); err != nil {
		return "", err
	}
	return dst, nil
}

func copyFileToUpgradeTemp(src string, mode os.FileMode) error {
	out, err := os.OpenFile(eyvescloudNewBinaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	return copyIntoOpenFile(src, out, mode)
}

func copyIntoOpenFile(src string, out *os.File, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		out.Close()
		return err
	}
	defer in.Close()

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

func safeReleaseBackupComponent(tag string) string {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "v")
	var b strings.Builder
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	component := strings.Trim(b.String(), "._-")
	if component == "" {
		return "unknown"
	}
	if len(component) > 64 {
		return component[:64]
	}
	return component
}

func sameVersion(current, latest string) bool {
	c := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(current)), "v")
	l := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(latest)), "v")
	return c != "" && c == l
}

func isWebPanelRunning() bool {
	if commandExists("systemctl") {
		cmd := exec.Command("systemctl", "is-active", "eyvescloud")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "active" {
			return true
		}
	}
	if commandExists("rc-service") {
		cmd := exec.Command("rc-service", "eyvescloud", "status")
		return cmd.Run() == nil
	}
	return false
}

func cliImportExistingContainers() {
	cliPrintln("\n--- 导入现有 LXC 容器 ---")
	cliPrintln("将 /var/lib/lxc 里的容器导入 EYVESCLOUD 配置。")
	cliPrintln("导入后会保留真实 LXC 名称，Web 和 CLI 都能管理同一个容器。")

	imported, err := manager.ImportExistingEyvescloudContainers()
	if err != nil {
		cliPrintf("导入失败: %v\n", err)
		return
	}
	if len(imported) == 0 {
		cliPrintln("没有发现新的 ct-* 容器。")
		return
	}

	cliPrintf("已导入 %d 个容器:\n", len(imported))
	for _, c := range imported {
		fmt.Printf("  [%d] %s [%s]\n", c.ID, c.Name, c.Status)
	}
	restartWebPanelForConfigChange()
}

func cliUninstall(reader *bufio.Reader) {
	cliPrintln("\n--- 卸载 EyvesCloud ---")
	cliPrintln("将删除 EYVESCLOUD 服务和 /usr/local/bin/eyvescloud。")
	cliPrintln("同时会删除 /root/.eyvescloud、/var/lib/lxc、/var/lib/eyvescloud、镜像缓存、备份、临时文件、/swapfile 和 EYVESCLOUD 网络规则。")

	if os.Geteuid() != 0 {
		cliPrintln("卸载需要 root 权限。")
		cliPrintln("请运行: sudo eyvescloud cli --no-web")
		return
	}

	confirm := promptString(reader, "输入 uninstall 继续卸载", "no")
	if strings.ToLower(confirm) != "uninstall" {
		cliPrintln("已取消")
		return
	}

	destroyAllLXCContainers()
	destroyAllKVMDomains()
	removeEYVESCLOUDLibvirtDefaultNetwork()
	cleanupEYVESCLOUDNetworking()
	removeEYVESCLOUDHostHooks()
	removeEYVESCLOUDQuotaRecords()
	stopAndRemoveService()
	removePath("/usr/local/bin/eyvescloud")
	removePath("/etc/sysctl.d/99-eyvescloud.conf")
	removePath("/var/log/eyvescloud.log")
	removePath("/var/log/eyvescloud.err")
	removePath("/root/.eyvescloud")
	removePath("/var/lib/lxc")
	removePath("/var/lib/eyvescloud")
	removePath("/var/cache/lxc")
	removePath("/var/cache/eyvescloud")
	removePath("/root/eyvescloud-backups")
	removeEYVESCLOUDTmpFiles()
	removeEYVESCLOUDSwapfile()

	reloadSysctl()

	fmt.Println()
	cliPrintln("EYVESCLOUD 已卸载。")
	cliPrintln("服务、二进制、配置、容器/虚拟机、本地镜像、缓存、备份、临时文件和 EYVESCLOUD 网络规则均已删除。")
}

func destroyAllLXCContainers() {
	entries, err := os.ReadDir("/var/lib/lxc")
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		fmt.Printf("Destroying LXC container %s...\n", name)
		runQuiet("lxc-stop", "-n", name, "-k")
		runQuiet("lxc-destroy", "-n", name, "-f")
		removeLXCContainerPath("/var/lib/lxc/" + name)
	}
}

func destroyAllKVMDomains() {
	if !commandExists("virsh") {
		return
	}
	out, err := exec.Command("virsh", "list", "--all", "--name").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if isEYVESCLOUDKVMDomain(name) {
			removeKVMDomain(name)
		}
	}
}

func isEYVESCLOUDKVMDomain(name string) bool {
	if !strings.HasPrefix(name, "vm-") || len(name) <= len("vm-") {
		return false
	}
	for _, r := range strings.TrimPrefix(name, "vm-") {
		if r < '0' || r > '9' {
			return false
		}
	}
	if dirExists("/var/lib/eyvescloud/kvm/instances/" + name) {
		return true
	}
	out, err := exec.Command("virsh", "dumpxml", name).Output()
	return err == nil && strings.Contains(string(out), "/var/lib/eyvescloud/kvm/")
}

func removeKVMDomain(name string) {
	fmt.Printf("Removing KVM domain %s...\n", name)
	runQuiet("virsh", "destroy", name)
	if runCommandOK("virsh", "undefine", name, "--remove-all-storage", "--nvram") {
		return
	}
	if runCommandOK("virsh", "undefine", name, "--nvram") {
		return
	}
	runQuiet("virsh", "undefine", name)
}

func removeEYVESCLOUDLibvirtDefaultNetwork() {
	if !commandExists("virsh") || !fileExists(libvirtDefaultNetworkMarker) {
		return
	}
	if libvirtDefaultUsedByNonEYVESCLOUDDomain() {
		cliPrintln("检测到非 EYVESCLOUD 虚拟机仍在使用 libvirt default 网络，已保留 default/virbr0。")
		return
	}
	fmt.Println("Removing EYVESCLOUD-created libvirt default network...")
	runQuiet("virsh", "net-destroy", "default")
	runQuiet("virsh", "net-undefine", "default")
	removePath(libvirtDefaultNetworkMarker)
}

func libvirtDefaultUsedByNonEYVESCLOUDDomain() bool {
	if !commandExists("virsh") {
		return false
	}
	out, err := exec.Command("virsh", "list", "--all", "--name").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || isEYVESCLOUDKVMDomain(name) {
			continue
		}
		if usesLibvirtDefaultNetwork(name) {
			return true
		}
	}
	return false
}

func usesLibvirtDefaultNetwork(domain string) bool {
	out, err := exec.Command("virsh", "domiflist", domain).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for _, field := range fields {
			if field == "default" || field == "virbr0" {
				return true
			}
		}
	}
	return false
}

func cleanupEYVESCLOUDNetworking() {
	removeEYVESCLOUDNATRules()
	cleanupEYVESCLOUDIPv6Runtime()
	cleanupEYVESCLOUDIPv6BridgeRoutes()
	for _, bridge := range []string{"lxcbr0", "virbr0"} {
		deleteFilterRule("FORWARD", "-i", bridge, "-j", "ACCEPT")
		deleteFilterRule("FORWARD", "-o", bridge, "-j", "ACCEPT")
		deleteFilterRule("FORWARD", "-i", bridge, "-o", bridge, "-j", "ACCEPT")
		deleteIP6TablesBridgeRules(bridge)
	}
}

func cleanupEYVESCLOUDIPv6Runtime() {
	if config.AppConfig == nil {
		return
	}
	for _, c := range config.AppConfig.Containers {
		cleanupEYVESCLOUDContainerIPv6(c)
	}
}

func cleanupEYVESCLOUDContainerIPv6(c config.Container) {
	bridge := "lxcbr0"
	if c.IsKVM() {
		bridge = "virbr0"
	}
	mac := strings.ToLower(strings.TrimSpace(c.MACAddress))
	if mac != "" && bridge == "virbr0" {
		deleteIP6FilterRule("FORWARD", "-i", bridge, "-m", "mac", "--mac-source", mac, "-j", "DROP")
	}
	if strings.TrimSpace(c.IPv6) == "" {
		return
	}

	addr := strings.TrimSpace(c.IPv6)
	if slash := strings.Index(addr, "/"); slash >= 0 {
		addr = addr[:slash]
	}
	source := strings.TrimSpace(c.IPv6)
	if !strings.Contains(source, "/") {
		source += "/128"
	}

	deleteIP6NATSource(source)
	deleteIP6FilterRule("FORWARD", "-i", bridge, "-s", source, "-j", "ACCEPT")
	deleteIP6FilterRule("FORWARD", "-o", bridge, "-d", source, "-j", "ACCEPT")
	if mac != "" && bridge == "virbr0" {
		deleteIP6FilterRule("FORWARD", "-i", bridge, "-m", "mac", "--mac-source", mac, "-s", source, "-j", "ACCEPT")
		deleteIP6FilterRule("FORWARD", "-i", bridge, "-m", "mac", "--mac-source", mac, "-j", "DROP")
	}

	runQuiet("ip", "-6", "route", "del", source, "dev", bridge)
	if strings.TrimSpace(c.IPv6Interface) != "" {
		runQuiet("ip", "-6", "neigh", "del", "proxy", addr, "dev", c.IPv6Interface)
	}
}

func cleanupEYVESCLOUDIPv6BridgeRoutes() {
	if !commandExists("ip") {
		return
	}
	for _, bridge := range []string{"lxcbr0", "virbr0"} {
		out, err := exec.Command("ip", "-6", "route", "show", "dev", bridge).Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 || !strings.HasSuffix(fields[0], "/128") {
					continue
				}
				source := fields[0]
				addr := strings.TrimSuffix(source, "/128")
				deleteIP6NATSource(source)
				deleteIP6FilterRule("FORWARD", "-i", bridge, "-s", source, "-j", "ACCEPT")
				deleteIP6FilterRule("FORWARD", "-o", bridge, "-d", source, "-j", "ACCEPT")
				removeProxyNDPForAddress(addr)
				runQuiet("ip", "-6", "route", "del", source, "dev", bridge)
			}
		}
		runQuiet("ip", "-6", "addr", "del", "fe80::1/64", "dev", bridge)
	}
}

func removeProxyNDPForAddress(addr string) {
	out, err := exec.Command("ip", "-6", "neigh", "show", "proxy").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != addr {
			continue
		}
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "dev" {
				runQuiet("ip", "-6", "neigh", "del", "proxy", addr, "dev", fields[i+1])
			}
		}
	}
}

func deleteIP6NATSource(source string) {
	if !commandExists("ip6tables") || strings.TrimSpace(source) == "" {
		return
	}
	for {
		out, err := exec.Command("ip6tables", "-t", "nat", "-S", "POSTROUTING").Output()
		if err != nil {
			return
		}
		deleted := false
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, "-s "+source) || !strings.Contains(line, " -j MASQUERADE") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != "-A" {
				continue
			}
			fields[0] = "-D"
			args := append([]string{"-t", "nat"}, fields...)
			deleted = runCommandOK("ip6tables", args...)
			break
		}
		if !deleted {
			return
		}
	}
}

func removeEYVESCLOUDNATRules() {
	if commandExists("iptables") {
		for {
			out, err := exec.Command("sh", "-c", "iptables -t nat -L PREROUTING -n --line-numbers 2>/dev/null | grep 'eyvescloud-' | awk '{print $1}' | head -n 1").Output()
			line := strings.TrimSpace(string(out))
			if err != nil || line == "" {
				break
			}
			if !runCommandOK("iptables", "-t", "nat", "-D", "PREROUTING", line) {
				break
			}
		}
		deleteNATRule("POSTROUTING", "-s", config.LXCNATNetwork().Subnet, "-o", "eth+", "-j", "MASQUERADE")
		deleteNATRule("POSTROUTING", "-s", config.KVMNATNetwork().Subnet, "-o", "eth+", "-j", "MASQUERADE")
	}
}

func deleteNATRule(args ...string) {
	fullArgs := append([]string{"-t", "nat", "-D"}, args...)
	for runCommandOK("iptables", fullArgs...) {
	}
}

func deleteFilterRule(args ...string) {
	fullArgs := append([]string{"-D"}, args...)
	for runCommandOK("iptables", fullArgs...) {
	}
}

func deleteIP6FilterRule(args ...string) {
	fullArgs := append([]string{"-D"}, args...)
	for runCommandOK("ip6tables", fullArgs...) {
	}
}

func deleteIP6TablesBridgeRules(bridge string) {
	if !commandExists("ip6tables") {
		return
	}
	for {
		cmd := fmt.Sprintf("ip6tables -S FORWARD 2>/dev/null | grep -- %s | sed 's/^-A /-D /' | head -n 1", shellQuote(bridge))
		out, err := exec.Command("sh", "-c", cmd).Output()
		rule := strings.TrimSpace(string(out))
		if err != nil || rule == "" {
			return
		}
		if !runCommandOK("sh", "-c", "ip6tables "+rule) {
			return
		}
	}
}

func removeEYVESCLOUDHostHooks() {
	runQuiet("systemctl", "stop", "eyvescloud-kvm-ipv6.service")
	runQuiet("systemctl", "disable", "eyvescloud-kvm-ipv6.service")
	runQuiet("rc-service", "eyvescloud-kvm-ipv6", "stop")
	runQuiet("rc-update", "del", "eyvescloud-kvm-ipv6", "default")
	removePath("/usr/local/sbin/eyvescloud-kvm-ipv6-init")
	removePath("/etc/systemd/system/eyvescloud-kvm-ipv6.service")
	removePath("/etc/local.d/eyvescloud-kvm-ipv6.start")
	removePath("/etc/network/if-up.d/eyvescloud-kvm-ipv6")
}

func removeEYVESCLOUDQuotaRecords() {
	for _, path := range []string{"/etc/projects", "/etc/projid"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var kept []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" || strings.Contains(line, "eyvescloud-") {
				continue
			}
			kept = append(kept, line)
		}
		_ = os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0644)
	}
}

func removeEYVESCLOUDTmpFiles() {
	for _, pattern := range []string{"/tmp/eyvescloud-*", "/tmp/eyvescloud.*"} {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			removePath(path)
		}
	}
}

func removeEYVESCLOUDSwapfile() {
	if !fileExists("/swapfile") {
		return
	}
	runQuiet("swapoff", "/swapfile")
	removePath("/swapfile")
}

func removeLXCContainerPath(path string) {
	unmountPathTree(path)
	detachLoopDevices(path)
	if err := os.RemoveAll(path); err == nil {
		fmt.Printf("Removed %s\n", path)
		return
	}

	runQuiet("fuser", "-km", path+"/rootfs")
	runQuiet("fuser", "-km", path)
	unmountPathTree(path)
	detachLoopDevices(path)
	removePath(path)
}

func unmountPathTree(path string) {
	if commandExists("findmnt") {
		out, err := exec.Command("findmnt", "-R", "-n", "-o", "TARGET", path).Output()
		if err == nil {
			mounts := strings.Split(strings.TrimSpace(string(out)), "\n")
			for i := len(mounts) - 1; i >= 0; i-- {
				mountpoint := strings.TrimSpace(mounts[i])
				if mountpoint != "" {
					runQuiet("umount", "-R", "-l", mountpoint)
					runQuiet("umount", "-l", mountpoint)
				}
			}
		}
	}
	runQuiet("umount", "-R", "-l", path+"/rootfs")
	runQuiet("umount", "-l", path+"/rootfs")
	runQuiet("umount", "-R", "-l", path)
	runQuiet("umount", "-l", path)
}

func detachLoopDevices(path string) {
	if !commandExists("losetup") {
		return
	}
	images := []string{path + "/rootfs.img"}
	if entries, err := os.ReadDir(path); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".img") {
				images = append(images, path+"/"+entry.Name())
			}
		}
	}
	for _, image := range images {
		out, err := exec.Command("losetup", "-j", image).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if idx := strings.Index(line, ":"); idx > 0 {
				runQuiet("losetup", "-d", line[:idx])
			}
		}
	}
}

func stopAndRemoveService() {
	if commandExists("systemctl") {
		runQuiet("systemctl", "stop", "eyvescloud")
		runQuiet("systemctl", "disable", "eyvescloud")
		removePath("/etc/systemd/system/eyvescloud.service")
		runQuiet("systemctl", "daemon-reload")
		runQuiet("systemctl", "reset-failed", "eyvescloud")
	}

	if commandExists("rc-service") {
		runQuiet("rc-service", "eyvescloud", "stop")
	}
	if commandExists("rc-update") {
		runQuiet("rc-update", "del", "eyvescloud", "default")
	}
	removePath("/etc/init.d/eyvescloud")
}

func removePath(path string) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return
	}
	if err := os.RemoveAll(path); err != nil {
		fmt.Printf("Failed to remove %s: %v\n", path, err)
		return
	}
	fmt.Printf("Removed %s\n", path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func reloadSysctl() {
	if commandExists("sysctl") {
		runQuiet("sysctl", "--system")
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCommandOK(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}

func runQuiet(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func restartWebPanelForConfigChange() {
	if err := restartService("eyvescloud"); err != nil {
		cliPrintf("Web 面板重载跳过: %v\n", err)
		return
	}
	cliPrintln("Web 面板已重载并应用配置变更。")
}

func stopService(name string) error {
	if commandExists("systemctl") {
		return exec.Command("systemctl", "stop", name).Run()
	}
	if commandExists("rc-service") {
		return exec.Command("rc-service", name, "stop").Run()
	}
	return fmt.Errorf("no supported service manager found")
}

func startService(name string) error {
	if commandExists("systemctl") {
		return exec.Command("systemctl", "start", name).Run()
	}
	if commandExists("rc-service") {
		return exec.Command("rc-service", name, "start").Run()
	}
	return fmt.Errorf("no supported service manager found")
}

func restartService(name string) error {
	if commandExists("systemctl") {
		return exec.Command("systemctl", "restart", name).Run()
	}
	if commandExists("rc-service") {
		return exec.Command("rc-service", name, "restart").Run()
	}
	return fmt.Errorf("no supported service manager found")
}

func cliShowInfo() {
	containers, err := manager.ListContainers()
	if err != nil {
		cliPrintf("读取容器状态失败: %v\n", err)
	}

	total := len(containers)
	running := 0
	for _, container := range containers {
		if container.Status == "running" {
			running++
		}
	}

	cliPrintln("\n--- 系统信息 ---")
	cliPrintf("EYVESCLOUD 版本: %s\n", version.Current())
	cliPrintf("Web 端口: %d\n", config.AppConfig.Port)
	cliPrintf("管理员用户: %s\n", config.AppConfig.AdminUser)
	cliPrintf("容器总数: %d\n", total)
	cliPrintf("运行中: %d\n", running)
	cliPrintf("已停止: %d\n", total-running)

	if hostname, err := os.Hostname(); err == nil {
		cliPrintf("主机名: %s\n", hostname)
	}

	cmd := exec.Command("lxc-info", "--version")
	output, err := cmd.Output()
	if err == nil {
		cliPrintf("LXC 版本: %s", string(output))
	}
}

func selectContainer(reader *bufio.Reader, action string) (int, string) {
	containers, err := manager.ListContainers()
	if err != nil {
		cliPrintf("获取容器列表失败: %v\n", err)
		return 0, ""
	}
	if len(containers) == 0 {
		cliPrintln("暂无可用容器")
		return 0, ""
	}

	cliPrintf("\n--- 选择要%s的容器 ---\n", cliT(action))
	for i, container := range containers {
		fmt.Printf("  %d. [%d] %s [%s]\n", i+1, container.ID, container.Name, container.Status)
	}

	idx := promptInt(reader, "容器", 0)
	if idx < 1 || idx > len(containers) {
		cliPrintln("选择无效")
		return 0, ""
	}

	c := containers[idx-1]
	return c.ID, c.Name
}

func promptString(reader *bufio.Reader, label string, fallback string) string {
	label = cliT(label)
	if fallback == "" {
		fmt.Printf("%s: ", label)
	} else {
		fmt.Printf("%s [%s]: ", label, fallback)
	}

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return fallback
	}
	return input
}

func promptInt(reader *bufio.Reader, label string, fallback int) int {
	input := promptString(reader, label, strconv.Itoa(fallback))
	value, err := strconv.Atoi(input)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func promptFloat(reader *bufio.Reader, label string, fallback float64) float64 {
	input := promptString(reader, label, strconv.FormatFloat(fallback, 'f', -1, 64))
	value, err := strconv.ParseFloat(input, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func waitEnter(reader *bufio.Reader) {
	cliPrint("\n按 Enter 返回菜单...")
	reader.ReadString('\n')
}

func promptPortList(reader *bufio.Reader, label string) []int {
	input := promptString(reader, label, "")
	if input == "" {
		return nil
	}

	var ports []int
	for _, part := range strings.Split(input, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 || value > 65535 {
			cliPrintf("忽略无效端口: %s\n", strings.TrimSpace(part))
			continue
		}
		ports = append(ports, value)
	}
	return ports
}

func detectCLIEnglish() bool {
	lang := strings.ToLower(strings.TrimSpace(os.Getenv("EYVESCLOUD_LANG")))
	if lang == "en" || strings.HasPrefix(lang, "en_") || strings.HasPrefix(lang, "en-") {
		return true
	}
	if lang == "zh" || strings.HasPrefix(lang, "zh_") || strings.HasPrefix(lang, "zh-") {
		return false
	}
	if config.AppConfig != nil {
		return config.NormalizeLanguage(config.AppConfig.Language) == "en"
	}
	env := strings.ToLower(os.Getenv("LC_ALL") + " " + os.Getenv("LC_MESSAGES") + " " + os.Getenv("LANG"))
	return strings.Contains(env, "en_") || strings.Contains(env, "en-") || strings.Contains(env, "english")
}

func refreshCLILanguage() {
	cliEnglish = detectCLIEnglish()
}

func cliLanguageLabel(language string) string {
	if config.NormalizeLanguage(language) == "en" {
		return "English"
	}
	return cliT("简体中文")
}

func cliT(text string) string {
	if !cliEnglish {
		return text
	}
	translated := text
	keys := make([]string, 0, len(cliTranslations))
	for zh := range cliTranslations {
		keys = append(keys, zh)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	for _, zh := range keys {
		en := cliTranslations[zh]
		translated = strings.ReplaceAll(translated, zh, en)
	}
	return translated
}

func cliPrint(args ...interface{}) {
	if cliEnglish {
		for i, arg := range args {
			if s, ok := arg.(string); ok {
				args[i] = cliT(s)
			}
		}
	}
	fmt.Print(args...)
}

func cliPrintln(args ...interface{}) {
	if cliEnglish {
		for i, arg := range args {
			if s, ok := arg.(string); ok {
				args[i] = cliT(s)
			}
		}
	}
	fmt.Println(args...)
}

func cliPrintf(format string, args ...interface{}) {
	if cliEnglish {
		for i, arg := range args {
			if s, ok := arg.(string); ok {
				args[i] = cliT(s)
				continue
			}
			if err, ok := arg.(error); ok {
				args[i] = cliT(err.Error())
			}
		}
	}
	fmt.Printf(cliT(format), args...)
}

// RunSelfUpdateCommand 是非交互式自更新入口（供运维脚本 / CI / 手动触发）：
//
//	eyvescloud self-update          检查并升级到最新版本（有更新才动）
//	eyvescloud self-update --check  只检查并打印版本对比，不升级
//
// 与被控自动更新循环调用的是同一条路径（SelfUpdateOnce → 校验 SHA-256 →
// 备份旧二进制 → 原子替换 → 重启服务），因此可用来验证自动更新是否可用。
func RunSelfUpdateCommand(args []string) error {
	checkOnly := false
	for _, arg := range args {
		switch arg {
		case "--check", "-c":
			checkOnly = true
		case "--help", "-h":
			fmt.Println("用法: eyvescloud self-update [--check]")
			fmt.Println("  --check  只检查版本，不执行升级")
			return nil
		}
	}

	if checkOnly {
		result := CheckForUpdate()
		if result.Err != "" {
			return fmt.Errorf("版本检测失败: %s", result.Err)
		}
		fmt.Printf("当前版本: %s\n最新版本: %s\n是否有更新: %v\n", result.Current, result.Latest, result.HasUpdate)
		return nil
	}

	fmt.Printf("当前版本: %s，正在检查更新…\n", version.Current())
	latest, upgraded, err := SelfUpdateOnce()
	if err != nil {
		return err
	}
	if !upgraded {
		fmt.Printf("已是最新版本（%s），无需升级。\n", version.Current())
		return nil
	}
	fmt.Printf("升级完成: %s（服务已重启）\n", latest)
	return nil
}

// SelfUpdateToVersion 把本机升级到指定版本（空 = 最新版本）。
//
// 与 PanelSelfUpdateOnce 同一策略（就地替换 + detached 重启）：本机服务就是被升级
// 的对象，若先 stopService 再替换会把正在执行升级的进程杀掉。
// 供主控「一键升级被控节点」下发调用（/api/agent/self-update）。
//
// 校验：目标版本的发布产物必须带 SHA256SUMS（缺失仅告警，不匹配则中止——与
// 面板升级同一策略）。
func SelfUpdateToVersion(tag string) (newVersion string, upgraded bool, err error) {
	repo := resolveUpdateRepo()
	tag = strings.TrimSpace(tag)
	if tag != "" && !validateReleaseTag(tag) {
		return "", false, fmt.Errorf("无效的版本标签: %q", tag)
	}
	assetName, err := releaseArchiveAssetName(runtime.GOARCH)
	if err != nil {
		return "", false, err
	}
	current := version.Current()

	var release *githubRelease
	if tag == "" {
		release, err = fetchLatestRelease(repo, assetName)
	} else {
		release, err = fetchReleaseByTagAnyPrefix(repo, tag)
	}
	if err != nil {
		return "", false, fmt.Errorf("获取目标版本失败: %w", err)
	}
	target := strings.TrimSpace(release.TagName)
	if target == "" {
		return "", false, fmt.Errorf("Release 缺少 tag_name，无法升级")
	}
	if sameVersion(current, target) {
		// 已在该版本：幂等返回，不重复安装。
		return target, false, nil
	}
	assetURL := findReleaseAsset(release, assetName)
	if assetURL == "" {
		return "", false, fmt.Errorf("Release %s 没有找到 %s，无法升级", target, assetName)
	}
	if err := upgradeFromReleaseAssetInPlace(assetURL, target, assetName, findChecksumsURL(release, assetName)); err != nil {
		return "", false, err
	}
	return target, true, nil
}

// fetchReleaseByTagAnyPrefix 按 tag 取发布，兼容"版本号 vs v 前缀 tag"两种写法：
// 主控下发的通常是 2.2.21 这类版本号，而发布 tag 习惯写 v2.2.21。
// 先按原样查，404 再依次尝试加/去 v 前缀，避免"版本确实存在却取不到"。
func fetchReleaseByTagAnyPrefix(repo, tag string) (*githubRelease, error) {
	release, err := fetchReleaseByTag(repo, tag)
	if err == nil {
		return release, nil
	}
	candidates := []string{"v" + strings.TrimPrefix(tag, "v"), strings.TrimPrefix(tag, "v")}
	for _, alt := range candidates {
		if alt == tag {
			continue
		}
		if r2, err2 := fetchReleaseByTag(repo, alt); err2 == nil {
			return r2, nil
		}
	}
	return nil, err
}

// ResolveReleaseTag 把版本号解析为**发布里的规范 tag**（供主控下发给被控）。
//
// 为什么需要：被控可能运行较旧版本，其 tag 查询不带前缀兼容（例如它只会按
// "2.2.22" 精确匹配，而发布 tag 是 "v2.2.22"）。主控先把 tag 解析成发布里真实存在
// 的写法（v2.2.22）再下发，就能让任何版本的被控都能取到目标发布。
func ResolveReleaseTag(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", fmt.Errorf("版本号为空")
	}
	repo := resolveUpdateRepo()
	if !validateReleaseTag(tag) {
		return "", fmt.Errorf("无效的版本标签: %q", tag)
	}
	release, err := fetchReleaseByTagAnyPrefix(repo, tag)
	if err != nil {
		return "", fmt.Errorf("目标版本 %s 没有对应发布产物（请先发布该版本）: %w", tag, err)
	}
	resolved := strings.TrimSpace(release.TagName)
	if resolved == "" {
		return "", fmt.Errorf("发布缺少 tag_name")
	}
	return resolved, nil
}
