package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/manage"
	"eyvescloud/internal/version"
)

// RunAccountCommand 提供遗忘账号/密码时的非交互恢复命令（可通过 SSH 直接执行）。
//
// password 使用 bcrypt 单向哈希存储，**无法反查原密码**；因此该命令在展示账号
// 与状态之外，提供安全的「重设密码」路径：生成新强密码（或显式指定），落库后仅
// 打印一次。
//
// 用法：
//
//	eyvescloud account                   查看面板信息、管理员账号、两步验证状态
//	eyvescloud account rename <新账号>   更改管理员账号（旧会话全部失效）
//	eyvescloud account reset             重设管理员密码（自动生成强密码）
//	eyvescloud account reset --password <新密码>
func RunAccountCommand(args []string) error {
	action := "show"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}

	switch action {
	case "show":
		return accountShow()
	case "rename":
		return accountRename(args)
	case "reset", "set", "reset-password", "set-password":
		return accountReset(args)
	case "-h", "--help", "help":
		fmt.Println(accountUsage())
		return nil
	default:
		fmt.Fprintln(os.Stderr, accountUsage())
		return fmt.Errorf("未知 account 操作：%s", action)
	}
}

func accountShow() error {
	cfg := config.AppConfig
	if cfg == nil || cfg.AdminUser == "" {
		fmt.Println("EyvesCloud 尚未初始化（未找到管理员账号）。请先安装并访问 Web 面板完成初始化。")
		return nil
	}
	adminPath := config.CurrentAdminPath()
	panelDomain := strings.TrimSpace(cfg.PanelDomain)
	fmt.Println("=====================================")
	fmt.Println("  EyvesCloud 面板信息")
	fmt.Println("=====================================")
	fmt.Println("  管理员账号：", cfg.AdminUser)
	if cfg.AdminTOTPEnabled {
		fmt.Println("  两步验证：  已启用（登录需 TOTP 动态口令）")
	} else if cfg.AdminTOTPSecret != "" {
		fmt.Println("  两步验证：  已生成密钥但未启用")
	} else {
		fmt.Println("  两步验证：  未启用")
	}
	fmt.Println("  管理员路径：", adminPath)
	fmt.Println("  用户路径：  /user")
	if panelDomain != "" {
		fmt.Println("  绑定域名：  ", panelDomain)
		fmt.Println("  管理员入口：", panelDomain+adminPath)
		fmt.Println("  用户入口：  ", panelDomain+"/user")
	} else {
		fmt.Printf("  面板地址：  http(s)://<服务器IP或域名>:%d%s\n", cfg.Port, adminPath)
		fmt.Println("  绑定域名：  （未绑定，可在设置页或 vm 菜单配置）")
	}
	fmt.Println("  监听端口：  ", cfg.Port)
	fmt.Println("  数据目录：  ", cfg.DataDir)
	fmt.Println("  版本：      ", version.Current(), "(当前可执行文件)")
	fmt.Println("")
	fmt.Println("  注意：管理员密码以 bcrypt 单向哈希存储，无法反查原密码。")
	fmt.Println("  遗忘密码时请执行：eyvescloud account reset")
	fmt.Println("  快捷操作可执行：vm")
	fmt.Println("=====================================")
	return nil
}

// accountRename 更改管理员账号名（CLI 直改，等价服务器 root 权限）。
// 同步递增 AdminTokenVersion，使旧账号名签发的所有会话立即失效。
func accountRename(args []string) error {
	// 优先走本机管理通道（面板运行中时内存+数据库原子更新）。
	if ok, output, connected := manage.Call("account.rename", args); connected {
		if !ok {
			return fmt.Errorf("%s", output)
		}
		fmt.Print(output)
		return nil
	}
	// 面板未运行：回退直改数据库（不存覆写竞争）。
	cfg := config.AppConfig
	if cfg == nil || cfg.AdminUser == "" {
		fmt.Fprintln(os.Stderr, "EyvesCloud 尚未初始化，无法更改账号。")
		return fmt.Errorf("not initialized")
	}
	newUser := ""
	if len(args) > 0 {
		newUser = strings.TrimSpace(args[0])
	}
	if newUser == "" {
		// 兼容 --username <name> 形式
		flags := flag.NewFlagSet("eyvescloud account rename", flag.ContinueOnError)
		flags.SetOutput(new(strings.Builder))
		flags.StringVar(&newUser, "username", "", "新管理员账号")
		if err := flags.Parse(args); err != nil {
			return fmt.Errorf("无效参数：%w", err)
		}
		newUser = strings.TrimSpace(newUser)
	}
	if newUser == "" {
		fmt.Fprintln(os.Stderr, "用法：eyvescloud account rename <新账号>")
		return fmt.Errorf("缺少新账号")
	}
	if len(newUser) < 3 {
		return fmt.Errorf("账号长度至少 3 位")
	}
	if strings.ContainsAny(newUser, " \t\"'\\/<>") {
		return fmt.Errorf("账号含有非法字符")
	}
	if newUser == cfg.AdminUser {
		fmt.Println("新账号与当前账号相同，无需更改。")
		return nil
	}
	oldUser := cfg.AdminUser // 先记旧值：MutateGlobal 改的是同一个 AppConfig 指针
	if err := config.MutateGlobalMetaOnly(func(c *config.EyvescloudConfig) {
		c.AdminUser = newUser
		c.AdminTokenVersion++
	}); err != nil {
		return fmt.Errorf("更改管理员账号失败：%w", err)
	}
	fmt.Println("=====================================")
	fmt.Println("  管理员账号已更改")
	fmt.Println("=====================================")
	fmt.Println("  原账号：", oldUser)
	fmt.Println("  新账号：", newUser)
	fmt.Println("  提醒：所有已登录的管理员会话已失效，需重新登录。")
	fmt.Println("=====================================")
	return nil
}

func accountReset(args []string) error {
	cfg := config.AppConfig
	if cfg == nil || cfg.AdminUser == "" {
		fmt.Fprintln(os.Stderr, "EyvesCloud 尚未初始化，无法重设密码。")
		return fmt.Errorf("not initialized")
	}

	flags := flag.NewFlagSet("eyvescloud account reset", flag.ContinueOnError)
	flags.SetOutput(new(strings.Builder))
	var custom string
	flags.StringVar(&custom, "password", "", "显式指定新密码（留空则自动生成强密码）")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Println(accountUsage())
			return nil
		}
		return fmt.Errorf("无效参数：%w", err)
	}

	newPassword := strings.TrimSpace(custom)
	if newPassword == "" {
		newPassword = randomStrongPassword()
		// 面板运行中：必须走管理通道（直改库会被回写覆盖）。
		if _, _, connected := manage.Call("ping", nil); connected {
			if ok, output, _ := manage.Call("account.reset", []string{newPassword, "--display-password"}); connected {
				if !ok {
					return fmt.Errorf("%s", output)
				}
				fmt.Print(output)
				return nil
			}
		}
	} else if len(newPassword) < 10 {
		return fmt.Errorf("密码长度至少 10 位")
	}

	// 非空自定义密码也优先走管理通道（面板运行期间直改库=被覆写，"假的"）。
	if ok, output, connected := manage.Call("account.reset", []string{newPassword}); connected {
		if !ok {
			return fmt.Errorf("%s", output)
		}
		fmt.Print(output)
		return nil
	}

	if err := config.ResetAdminPassword(newPassword); err != nil {
		return fmt.Errorf("重设管理员密码失败：%w", err)
	}

	fmt.Println("=====================================")
	fmt.Println("  管理员密码已重设（仅显示一次，请妥善保存）")
	fmt.Println("=====================================")
	fmt.Println("  账号：  ", cfg.AdminUser)
	fmt.Println("  密码：  ", newPassword)
	if cfg.AdminTOTPEnabled {
		fmt.Println("  提醒：该账号启用了两步验证，登录时还需动态口令。")
	}
	fmt.Println("=====================================")
	return nil
}

func accountUsage() string {
	return `usage:
  eyvescloud account                   查看面板信息、管理员账号、两步验证状态
  eyvescloud account rename <新账号>   更改管理员账号（旧会话全部失效）
  eyvescloud account reset             重设管理员密码（自动生成强密码）
  eyvescloud account reset --password <新密码>
`
}

// randomStrongPassword 生成 18 位包含大小写、数字与符号的强密码。
func randomStrongPassword() string {
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const lower = "abcdefghijklmnopqrstuvwxyz"
	const digits = "0123456789"
	const symbols = "!@#$%^&*-_=+"
	sets := []string{upper, lower, digits, symbols}
	all := upper + lower + digits + symbols

	randomIndex := func(n int) int {
		if n <= 0 {
			return 0
		}
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return 0
		}
		val := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		return val % n
	}

	var sb strings.Builder
	// 确保每个字符集至少出现一次。
	for _, set := range sets {
		chars := []byte(set)
		idx := randomIndex(len(chars))
		sb.WriteByte(chars[idx])
	}
	remain := 18 - sb.Len()
	chars := []byte(all)
	for i := 0; i < remain; i++ {
		idx := randomIndex(len(chars))
		sb.WriteByte(chars[idx])
	}
	return sb.String()
}
