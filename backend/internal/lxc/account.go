package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"eyvescloud/internal/config"
)

// accountUsernamePattern 限制登录账号名形态：小写字母/下划线开头，
// 其余为小写字母、数字、下划线、连字符。既避免以 "-" 开头造成选项注入，
// 也避免任何 shell 元字符进入命令行或脚本。
var accountUsernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ValidateAccountUsername 校验待创建的登录账号名。
func ValidateAccountUsername(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if !accountUsernamePattern.MatchString(username) {
		return fmt.Errorf("invalid username: use lowercase letters, digits, underscore or hyphen, starting with a letter or underscore (max 32 chars)")
	}
	return nil
}

// ValidateAccountPassword 校验账号密码（沿用 root 密码的控制字符限制）。
func ValidateAccountPassword(password string) error {
	return validateRootPassword(password)
}

// CreateAccount 在 LXC 容器内创建一个新的登录账号，并设置其密码。
// sudo=true 时，若容器内存在 sudo/wheel 组，则把新账号加入以授予提权能力。
func (m *Manager) CreateAccount(id int, username, password string, sudo bool) error {
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found: %d", id)
	}
	if err := ValidateAccountUsername(username); err != nil {
		return err
	}
	if err := ValidateAccountPassword(password); err != nil {
		return err
	}
	username = strings.TrimSpace(username)

	lxcName := c.LxcName()
	if err := m.ensureDiskImageMounted(lxcName); err != nil {
		return err
	}
	rootfsPath := filepath.Join(m.LxcPath, lxcName, "rootfs")

	// 创建账号；已存在则视为幂等成功。
	if cmd, err := m.rootfsCommand(rootfsPath, "useradd", "-m", "-s", "/bin/bash", username); err != nil {
		return err
	} else if output, err := cmd.CombinedOutput(); err != nil {
		if !m.rootfsUserExists(rootfsPath, username) {
			return fmt.Errorf("failed to create user %s: %v, output: %s", username, err, string(output))
		}
	}

	// 通过 stdin 设置密码，避免密码出现在命令行或脚本中。
	if cmd, err := m.rootfsCommand(rootfsPath, "chpasswd"); err != nil {
		return err
	} else {
		cmd.Stdin = strings.NewReader(username + ":" + password + "\n")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to set password for %s: %v, output: %s", username, err, string(output))
		}
	}

	if sudo {
		for _, group := range []string{"sudo", "wheel"} {
			if !m.rootfsGroupExists(rootfsPath, group) {
				continue
			}
			if cmd, err := m.rootfsCommand(rootfsPath, "usermod", "-aG", group, username); err == nil {
				_, _ = cmd.CombinedOutput()
			}
		}
	}
	return nil
}

func (m *Manager) rootfsUserExists(rootfsPath, username string) bool {
	cmd, err := m.rootfsCommand(rootfsPath, "id", username)
	if err != nil {
		return false
	}
	return cmd.Run() == nil
}

// rootfsGroupExists 直接读取 rootfs 内的 /etc/group 判断组是否存在，
// 避免依赖 chroot 内可能缺失的 getent。
func (m *Manager) rootfsGroupExists(rootfsPath, group string) bool {
	data, err := os.ReadFile(filepath.Join(rootfsPath, "etc", "group"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == group {
			return true
		}
	}
	return false
}