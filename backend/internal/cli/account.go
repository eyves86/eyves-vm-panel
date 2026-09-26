package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

// RunAccountCommand 提供遗忘账号/密码时的非交互恢复命令（可通过 SSH 直接执行）。
//
// password 使用 bcrypt 单向哈希存储，**无法反查原密码**；因此该命令在展示账号
// 与状态之外，提供安全的「重设密码」路径：生成新强密码（或显式指定），落库后仅
// 打印一次。
//
// 用法：
//   eyvescloud account                   查看管理员账号、两步验证状态
//   eyvescloud account reset             重设管理员密码（自动生成强密码）
//   eyvescloud account reset --password <新密码>
func RunAccountCommand(args []string) error {
	action := "show"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}

	switch action {
	case "show":
		return accountShow()
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
	fmt.Println("=====================================")
	fmt.Println("  EyvesCloud 账号信息")
	fmt.Println("=====================================")
	fmt.Println("  管理员账号：", cfg.AdminUser)
	if cfg.AdminTOTPEnabled {
		fmt.Println("  两步验证：  已启用（登录需 Google Authenticator 动态口令）")
	} else if cfg.AdminTOTPSecret != "" {
		fmt.Println("  两步验证：  已生成密钥但未启用")
	} else {
		fmt.Println("  两步验证：  未启用")
	}
	fmt.Println("  数据目录：  ", cfg.DataDir)
	fmt.Println("  版本：      ", version.Current(), "(当前可执行文件)")
	fmt.Println("")
	fmt.Println("  注意：管理员密码以 bcrypt 单向哈希存储，无法反查原密码。")
	fmt.Println("  遗忘密码时请执行：eyvescloud account reset")
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
	} else if len(newPassword) < 10 {
		return fmt.Errorf("密码长度至少 10 位")
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
  eyvescloud account                   查看管理员账号、两步验证状态
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