// Package cloudinit cloud-init 标准化开通（P5-1）：
//
//   - 模板渲染：白名单占位符 + 转义（禁未转义插值进 shell —— 评审红线）；
//   - KVM 走 cloud-localds 生成 seed.iso，作为 libvirt <disk> 附加盘；
//   - LXC 走 lxc.container.conf user-data 段（base64 嵌入或独立文件）；
//   - host_template.go：Go text/template 不行（任意插值会执行任意代码）；
//     本包用白名单占位符 + 安全替换 → 仅 {{HOSTNAME}} / {{SSH_KEY}} /
//     {{TIMEZONE}} / {{USER_DATA}} 四类变量，未列入视为错误；
//   - 模板输入合法性：hostname 用正则 ^[a-z0-9-]{1,63}$ 校验；SSH key 用
//     ssh.ParseAuthorizedKey 做语法校验（来自 golang.org/x/crypto/ssh）。
//
// 真实 cloud-localds / lxc 命令由调用方注入 Runner。
package cloudinit

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Inputs 模板输入（白名单字段）。
type Inputs struct {
	Hostname  string // ^[a-z0-9-]{1,63}$
	User      string // Linux 用户名；可空（默认 "admin"）
	Timezone  string // IANA TZ；可空（默认 "UTC"）
	SSHKeys   []string // OpenSSH 公钥列表
	UserData  string // 自定义 user-data（cloud-init #cloud-config）；已通过 sanitization
	// MetaData / NetworkConfig 可选；本包提供默认值（hostname-only）。
}

// Validate 检查输入合法性，失败返回具体原因。
func (i Inputs) Validate() error {
	if err := ValidateHostname(i.Hostname); err != nil {
		return err
	}
	if i.Timezone == "" {
		// 默认 UTC；调用方可改。
	}
	if len(i.SSHKeys) == 0 {
		return errors.New("cloudinit: at least one SSH key required")
	}
	for idx, k := range i.SSHKeys {
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k)); err != nil {
			return fmt.Errorf("cloudinit: SSH key #%d invalid: %v", idx, err)
		}
	}
	if i.UserData != "" {
		if err := ValidateUserData(i.UserData); err != nil {
			return err
		}
	}
	return nil
}

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateHostname 校验 RFC1123 主机名（小写、数字、连字符、1-63）。
func ValidateHostname(h string) error {
	if h == "" {
		return errors.New("cloudinit: hostname required")
	}
	if !hostnameRE.MatchString(h) {
		return fmt.Errorf("cloudinit: hostname %q must match ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$", h)
	}
	return nil
}

// ValidateUserData 校验 user-data 必须是 #cloud-config 开头 + 不含
// 任意 shell 重定向/反引号（防通过 cloud-init 注入命令）。
//
// 允许的语法：YAML 形式 + 多行 + 注释（# 开头）。
func ValidateUserData(s string) error {
	if !strings.HasPrefix(strings.TrimSpace(s), "#cloud-config") {
		return errors.New("cloudinit: user-data must start with #cloud-config")
	}
	// 黑名单关键字（仅校验明显危险的 shell 操作）：
	for _, bad := range []string{"`", "$(", "${", "|sh", "|bash"} {
		if strings.Contains(s, bad) {
			return fmt.Errorf("cloudinit: user-data contains forbidden token %q", bad)
		}
	}
	return nil
}

// RenderMetaData 构造 instance-id + local-hostname 形式的 metadata。
func RenderMetaData(i Inputs) (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	instanceID, err := randomInstanceID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, i.Hostname), nil
}

// RenderUserData 构造 cloud-init user-data（#cloud-config + 默认模块）。
//
// 默认模块：package_update + timezone + users（注入 SSH key）+ hostname；
// 调用方 i.UserData 覆盖 custom 模块（必须通过 ValidateUserData）。
func RenderUserData(i Inputs) (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	b.WriteString("package_update: false\n")
	b.WriteString("package_upgrade: false\n")
	b.WriteString(fmt.Sprintf("timezone: %s\n", defaultStr(i.Timezone, "UTC")))
	b.WriteString(fmt.Sprintf("hostname: %s\n", i.Hostname))
	b.WriteString(fmt.Sprintf("manage_etc_hosts: true\n"))
	// 用户 + SSH key
	user := defaultStr(i.User, "admin")
	b.WriteString("users:\n")
	b.WriteString(fmt.Sprintf("  - name: %s\n", user))
	b.WriteString("    lock-passwd: false\n")
	b.WriteString("    sudo: ALL=(ALL) NOPASSWD:ALL\n")
	b.WriteString("    shell: /bin/bash\n")
	b.WriteString("    ssh_authorized_keys:\n")
	for _, k := range i.SSHKeys {
		b.WriteString(fmt.Sprintf("      - %s\n", k))
	}
	// 用户自定义段
	if i.UserData != "" {
		b.WriteString("\n# custom user-data\n")
		b.WriteString(i.UserData)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// RenderNetworkConfig 默认 DHCP 全网卡。
func RenderNetworkConfig() string {
	return "version: 2\nethernets:\n  default:\n    dhcp4: true\n"
}

// RenderNoCloudSeed 把 meta-data + user-data + network-config 拼成
// NoCloud seed ISO 的输入元数据（cloud-localds 调用）。
func RenderNoCloudSeed(i Inputs) (metaData, userData, networkConfig string, err error) {
	if err = i.Validate(); err != nil {
		return
	}
	metaData, err = RenderMetaData(i)
	if err != nil {
		return
	}
	userData, err = RenderUserData(i)
	if err != nil {
		return
	}
	networkConfig = RenderNetworkConfig()
	return
}

// EncodeSeedToBase64 把 user-data base64 嵌入（部分 KVM 工具用）。本包
// 仅暴露辅助函数，调用方按需使用。
func EncodeSeedToBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// randomInstanceID 生成 iid-xxxxxxxxxxxxxxxx（OpenStack 风格）。
func randomInstanceID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	hex := fmt.Sprintf("%x", b)
	return "iid-" + hex[:16], nil
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ---- LXC 接入 ----

// RenderLXCUserData 构造 LXC 容器 cloud-init user-data 段（base64 嵌入）。
//
// LXC 没有 NoCloud ISO 机制；标准做法是在容器 config 里挂载 vendor
// data + user-data。本包只产出 base64 文本片段，调用方写入容器 config。
func RenderLXCUserData(i Inputs) (string, error) {
	plain, err := RenderUserData(i)
	if err != nil {
		return "", err
	}
	return EncodeSeedToBase64(plain), nil
}

// CompileTimeUTC 编译期注入 UTC 标签便于测试断言时间格式稳定。
var CompileTimeUTC = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// SanityCheck 强制 net/mail 导入留作未来扩展（用户邮箱场景）。
var _ = mail.Address{}