package cli

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// RunNodeLinkCommand 「节点对接」快捷命令：查看/生成本面板作为被控时的对接密钥。
//
// 全新安装（主控+被控开箱模式）首启即自动生成密钥，安装脚本会把
// `node-link` 的输出直接展示给运维；密钥一次性、24h 有效，填到目标主控的
// 「节点管理 → 添加节点 → 对接已有面板」表单即可把本面板接入该主控。
//
// 用法：
//   eyvescloud node-link             查看本面板地址与对接密钥状态
//   eyvescloud node-link generate     重新生成对接密钥（旧密钥立即作废）
func RunNodeLinkCommand(args []string) error {
	action := "show"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}

	switch action {
	case "show":
		return nodeLinkShow()
	case "generate", "regen", "new":
		return nodeLinkGenerate(args)
	case "-h", "--help", "help":
		fmt.Println(nodeLinkUsage())
		return nil
	default:
		fmt.Fprintln(os.Stderr, nodeLinkUsage())
		return fmt.Errorf("未知 node-link 操作：%s", action)
	}
}

// nodeLinkValid 读取当前生效的对接密钥（存在 + 未过期）。
func nodeLinkValid() (key, expiry string, ok bool) {
	cfg := config.AppConfig
	if cfg == nil {
		return "", "", false
	}
	if cfg.AgentPairingKey == "" {
		return "", "", false
	}
	t, err := time.Parse(time.RFC3339, cfg.AgentPairingKeyExpiry)
	if err != nil || time.Now().After(t) {
		return "", "", false
	}
	return cfg.AgentPairingKey, cfg.AgentPairingKeyExpiry, true
}

// detectPanelAddress 面板对外地址：优先绑定域名，否则用出口 IP + 监听端口推导。
func detectPanelAddress() string {
	cfg := config.AppConfig
	if cfg == nil {
		return ""
	}
	if domain := strings.TrimSpace(cfg.PanelDomain); domain != "" {
		if !strings.HasPrefix(strings.ToLower(domain), "http://") &&
			!strings.HasPrefix(strings.ToLower(domain), "https://") {
			return "http://" + domain
		}
		return domain
	}
	host := detectOutboundIP()
	if host == "" {
		host = "YOUR_SERVER_IP"
	}
	scheme := "http"
	if cfg.SSL.Enabled {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, cfg.Port)
}

// detectOutboundIP 通过 UDP dial 探测默认路由出口 IP（不产生真实流量）。
func detectOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func nodeLinkShow() error {
	cfg := config.AppConfig
	if cfg == nil || cfg.AdminUser == "" {
		fmt.Println("EyvesCloud 尚未初始化（未找到配置）。请先安装并访问 Web 面板完成初始化。")
		return nil
	}
	fmt.Println("=====================================")
	fmt.Println("  节点对接信息（本面板作为被控）")
	fmt.Println("=====================================")
	fmt.Println("  本面板地址：", detectPanelAddress())
	if key, expiry, ok := nodeLinkValid(); ok {
		fmt.Println("  对接密钥：  ", key)
		fmt.Println("  有效期至：  ", expiry, "（一次性，使用后作废）")
		fmt.Println("")
		fmt.Println("  想接入其他主控：在目标主控「节点管理 → 添加节点 →")
		fmt.Println("  对接已有面板」中填入以上「面板地址」与「对接密钥」。")
	} else {
		fmt.Println("  对接密钥：  （无有效密钥）")
		fmt.Println("")
		fmt.Println("  执行 eyvescloud node-link generate 生成新密钥，")
		fmt.Println("  或在本面板「节点管理 → 节点接入」中生成。")
	}
	fmt.Println("=====================================")
	return nil
}

func nodeLinkGenerate(args []string) error {
	cfg := config.AppConfig
	if cfg == nil || cfg.AdminUser == "" {
		fmt.Fprintln(os.Stderr, "EyvesCloud 尚未初始化，无法生成对接密钥。")
		return fmt.Errorf("not initialized")
	}
	_ = args
	flags := flag.NewFlagSet("eyvescloud node-link generate", flag.ContinueOnError)
	flags.SetOutput(new(strings.Builder))
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("无效参数：%w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("生成密钥失败：%w", err)
	}
	key := hex.EncodeToString(buf)
	expiry := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	if err := config.MutateGlobalMetaOnly(func(c *config.EyvescloudConfig) {
		c.AgentPairingKey = key
		c.AgentPairingKeyExpiry = expiry
	}); err != nil {
		return fmt.Errorf("保存密钥失败：%w", err)
	}

	fmt.Println("=====================================")
	fmt.Println("  对接密钥已生成（24h 内有效，一次性）")
	fmt.Println("=====================================")
	fmt.Println("  本面板地址：", detectPanelAddress())
	fmt.Println("  对接密钥：  ", key)
	fmt.Println("  有效期至：  ", expiry)
	fmt.Println("")
	fmt.Println("  想接入其他主控：在目标主控「节点管理 → 添加节点 →")
	fmt.Println("  对接已有面板」中填入以上「面板地址」与「对接密钥」。")
	fmt.Println("")
	fmt.Println("  注意：若面板服务正在运行，网页端生成的密钥与本命令互不感知，")
	fmt.Println("  请以最近一次生成/重置的密钥为准（推荐在网页端操作）。")
	fmt.Println("=====================================")
	return nil
}

func nodeLinkUsage() string {
	return `usage:
  eyvescloud node-link             查看本面板地址与对接密钥状态
  eyvescloud node-link generate    重新生成对接密钥（旧密钥立即作废）
`
}
