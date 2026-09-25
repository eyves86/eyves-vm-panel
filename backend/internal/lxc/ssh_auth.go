package lxc

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/ssh"
)

const (
	SSHAuthAutoPassword = "auto_password"
	SSHAuthPassword     = "password"
	SSHAuthKey          = "key"
	SSHAuthKeep         = "keep"
)

type SSHAccess struct {
	Mode      string
	Password  string
	PublicKey string
}

func HasSSHAuthOptions(cfg ContainerConfig) bool {
	return strings.TrimSpace(cfg.SSHAuthMode) != "" ||
		strings.TrimSpace(cfg.SSHPassword) != "" ||
		strings.TrimSpace(cfg.SSHPublicKey) != "" ||
		len(cfg.SSHPublicKeys) > 0
}

// CollectAllSSHPublicKeys 聚合 ContainerConfig 里所有公钥源（SSHPublicKey 单 key +
// SSHPublicKeys 多 key + SSHKeyIDs 已提前解析到 SSHPublicKeys），去重后返回规范化列表。
// 返回的每一行都是合法的 OpenSSH authorized_keys 单行格式。
func CollectAllSSHPublicKeys(cfg ContainerConfig) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(cfg.SSHPublicKeys)+1)
	add := func(key string) error {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		normalized, err := NormalizeSSHPublicKey(key)
		if err != nil {
			return err
		}
		if seen[normalized] {
			return nil
		}
		seen[normalized] = true
		out = append(out, normalized)
		return nil
	}
	if err := add(cfg.SSHPublicKey); err != nil {
		return nil, err
	}
	for _, k := range cfg.SSHPublicKeys {
		if err := add(k); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// JoinSSHPublicKeys 把多公钥列表合并成 authorized_keys 多行字符串（\n 分隔）。
func JoinSSHPublicKeys(keys []string) string {
	return strings.Join(keys, "\n")
}

func ResolveCreateSSHAccess(cfg ContainerConfig) (SSHAccess, error) {
	// 预处理：如果单 SSHPublicKey 为空但多 SSHPublicKeys 有值，用第一个 key 让
	// resolveSSHAuthMode 能正确判断出 SSHAuthKey 模式；后续再聚合所有 key。
	if strings.TrimSpace(cfg.SSHPublicKey) == "" && len(cfg.SSHPublicKeys) > 0 {
		cfg.SSHPublicKey = cfg.SSHPublicKeys[0]
	}
	mode, err := resolveSSHAuthMode(cfg.SSHAuthMode, cfg.SSHPassword, cfg.SSHPublicKey, SSHAuthAutoPassword)
	if err != nil {
		return SSHAccess{}, err
	}
	if mode == SSHAuthKeep {
		mode = SSHAuthAutoPassword
	}

	switch mode {
	case SSHAuthAutoPassword:
		return SSHAccess{Mode: mode, Password: generateRandomString(16)}, nil
	case SSHAuthPassword:
		password := strings.TrimSpace(cfg.SSHPassword)
		if password == "" {
			return SSHAccess{}, fmt.Errorf("请填写自定义 SSH 密码")
		}
		if err := ValidateCustomSSHPassword(password); err != nil {
			return SSHAccess{}, err
		}
		return SSHAccess{Mode: mode, Password: password}, nil
	case SSHAuthKey:
		allKeys, err := CollectAllSSHPublicKeys(cfg)
		if err != nil {
			return SSHAccess{}, err
		}
		if len(allKeys) == 0 {
			return SSHAccess{}, fmt.Errorf("请填写 SSH 公钥或绑定 SSH Key")
		}
		password := strings.TrimSpace(cfg.SSHPassword)
		if password == "" {
			password = generateRandomString(16)
		} else if err := ValidateCustomSSHPassword(password); err != nil {
			return SSHAccess{}, err
		}
		return SSHAccess{Mode: mode, Password: password, PublicKey: JoinSSHPublicKeys(allKeys)}, nil
	default:
		return SSHAccess{}, fmt.Errorf("不支持的 SSH 登录方式: %s", mode)
	}
}

func ResolveReinstallSSHAccess(currentPassword string, cfg ContainerConfig) (SSHAccess, error) {
	if strings.TrimSpace(cfg.SSHPublicKey) == "" && len(cfg.SSHPublicKeys) > 0 {
		cfg.SSHPublicKey = cfg.SSHPublicKeys[0]
	}
	mode, err := resolveSSHAuthMode(cfg.SSHAuthMode, cfg.SSHPassword, cfg.SSHPublicKey, SSHAuthKeep)
	if err != nil {
		return SSHAccess{}, err
	}

	switch mode {
	case SSHAuthKeep:
		password := strings.TrimSpace(currentPassword)
		if password == "" {
			password = generateRandomString(16)
		}
		if err := validateRootPassword(password); err != nil {
			return SSHAccess{}, err
		}
		return SSHAccess{Mode: mode, Password: password}, nil
	case SSHAuthAutoPassword:
		return SSHAccess{Mode: mode, Password: generateRandomString(16)}, nil
	case SSHAuthPassword:
		password := strings.TrimSpace(cfg.SSHPassword)
		if password == "" {
			return SSHAccess{}, fmt.Errorf("请填写自定义 SSH 密码")
		}
		if err := ValidateCustomSSHPassword(password); err != nil {
			return SSHAccess{}, err
		}
		return SSHAccess{Mode: mode, Password: password}, nil
	case SSHAuthKey:
		allKeys, err := CollectAllSSHPublicKeys(cfg)
		if err != nil {
			return SSHAccess{}, err
		}
		if len(allKeys) == 0 {
			return SSHAccess{}, fmt.Errorf("请填写 SSH 公钥或绑定 SSH Key")
		}
		password := strings.TrimSpace(cfg.SSHPassword)
		if password != "" {
			if err := ValidateCustomSSHPassword(password); err != nil {
				return SSHAccess{}, err
			}
		} else {
			password = strings.TrimSpace(currentPassword)
			if password == "" {
				password = generateRandomString(16)
			}
		}
		if err := validateRootPassword(password); err != nil {
			return SSHAccess{}, err
		}
		return SSHAccess{Mode: mode, Password: password, PublicKey: JoinSSHPublicKeys(allKeys)}, nil
	default:
		return SSHAccess{}, fmt.Errorf("不支持的 SSH 登录方式: %s", mode)
	}
}

func ValidateCustomSSHPassword(password string) error {
	if len(password) < 8 || len(password) > 64 {
		return fmt.Errorf("密码长度必须为 8-64 位")
	}
	hasLetter := false
	hasDigit := false
	for _, r := range password {
		if unicode.IsSpace(r) {
			return fmt.Errorf("密码不能包含空白字符")
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("密码至少需要包含字母和数字")
	}
	return validateRootPassword(password)
}

func NormalizeSSHPublicKey(publicKey string) (string, error) {
	key := strings.TrimSpace(publicKey)
	if key == "" {
		return "", nil
	}
	if len(key) > 8192 {
		return "", fmt.Errorf("SSH 公钥长度不能超过 8192 字符")
	}
	if strings.ContainsAny(key, "\r\n") || strings.ContainsRune(key, '\x00') {
		return "", fmt.Errorf("SSH 公钥只能填写一行")
	}

	fields := strings.Fields(key)
	if len(fields) < 2 {
		return "", fmt.Errorf("SSH 公钥格式不正确")
	}
	if !isSupportedSSHKeyType(fields[0]) {
		return "", fmt.Errorf("不支持的 SSH 公钥类型: %s", fields[0])
	}
	parsed, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return "", fmt.Errorf("SSH 公钥格式不正确")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return "", fmt.Errorf("一次只能填写一个 SSH 公钥")
	}
	if !isSupportedSSHKeyType(parsed.Type()) {
		return "", fmt.Errorf("不支持的 SSH 公钥类型: %s", parsed.Type())
	}
	return key, nil
}

func resolveSSHAuthMode(rawMode, password, publicKey, defaultMode string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(rawMode))
	mode = strings.ReplaceAll(mode, "-", "_")
	if mode == "" {
		if strings.TrimSpace(publicKey) != "" {
			return SSHAuthKey, nil
		}
		if strings.TrimSpace(password) != "" {
			return SSHAuthPassword, nil
		}
		return defaultMode, nil
	}

	switch mode {
	case "auto", "auto_password", "generated", "generate":
		return SSHAuthAutoPassword, nil
	case "password", "custom_password":
		return SSHAuthPassword, nil
	case "key", "ssh_key", "public_key":
		return SSHAuthKey, nil
	case "keep", "retain", "keep_password":
		return SSHAuthKeep, nil
	default:
		return "", fmt.Errorf("不支持的 SSH 登录方式: %s", rawMode)
	}
}

func isSupportedSSHKeyType(keyType string) bool {
	switch keyType {
	case "ssh-ed25519",
		"ssh-rsa",
		"ecdsa-sha2-nistp256",
		"ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com",
		"sk-ecdsa-sha2-nistp256@openssh.com":
		return true
	default:
		return false
	}
}
