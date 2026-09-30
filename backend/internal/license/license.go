// Package license —— EyvesCloud 授权许可（面向授权运营场景）。
//
// 商业模式：面板授权给其它公司运营，授权费对应"白标隐藏 Powered by"能力。
// 执行方式：**离线签名许可证文件**（license.dat，与发行版签名同一套 ed25519
// 公私钥体系，公钥编译期内嵌）。
//
//	签发方（授权方）：releasetool licensegen 用私钥签发 license 文件。
//	被授权方（客户）：把 license.dat 放入面板数据目录（或在设置页上传）。
//	校验：启动时加载 + 每次读取 /api/brand 时判定：
//	  - 签名无效 / 公钥不匹配 / 已过期 → 未授权（powered_hidden 强制为 false）
//	  - license 带机器指纹时，与面板自身指纹不匹配 → 未授权
//
// 文件格式（与 SHA256SUMS.minisig 同款信封）：
//
//	untrusted comment: EyvesCloud license
//	<base64: 2字节算法号0x0000 + JSON(payload)>
//	untrusted comment: <base64 pubkey>
//	<base64: 2字节算法号0x0000 + 64字节签名>
package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pubKeyB64 由 ldflags 注入（-X eyvescloud/internal/license.pubKeyB64=...），
// 与发行签名同一把公钥。为空 = 未配置（开发构建）：授权判定一律 false，
// 但不阻止面板运行（保持免费形态）。
var pubKeyB64 = ""

// State 是当前授权状态（缓存 + 惰性重载）。
type State struct {
	Valid     bool
	Licensee  string    // 授权对象（公司名）
	Expires   time.Time // 到期时间
	MachineID string    // 绑定的机器指纹（空=不限）
	Loaded    bool
	Error     string // 文件存在但无效的原因（给管理员看）
}

var (
	mu       sync.RWMutex
	cached   *State
	cachedAt time.Time
)

// payload 是 license 文件里的 JSON 明文结构。
type payload struct {
	Licensee  string   `json:"licensee"`
	MachineID string   `json:"machine_id,omitempty"` // 面板机器指纹（sha256 前 32 hex）；空=不绑定
	ExpiresAt string   `json:"expires_at"`           // RFC3339
	Features  []string `json:"features,omitempty"`   // 预留：white-label 等
}

// FileName 是 license 文件在数据目录下的固定名称。
const FileName = "license.dat"

// MachineID 计算面板机器指纹：取数据目录 config.db.tokenkey（安装时生成的
// 随机 64 字节文件）的 SHA256 前 32 hex。该文件每台机器唯一、随数据目录存在，
// 拷贝整个数据目录到另一台机器时指纹不变（授权跟数据目录走，符合"每台机器
// 一份授权"的销售口径——客户换机/重装需重新授权）。
func MachineID(dataDir string) string {
	keyFile := filepath.Join(dataDir, ".eyvescloud", "config.db.tokenkey")
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		// tokenkey 不存在（旧安装/异常）：退化为数据目录路径指纹。
		sum := sha256.Sum256([]byte(dataDir))
		return fmt.Sprintf("%x", sum[:16])
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:16])
}

// pubKey 解码内嵌公钥。
func pubKey() ed25519.PublicKey {
	raw := strings.TrimSpace(pubKeyB64)
	if raw == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// parseEnvelope 解析 minisign 信封：返回 payload（去 2 字节前缀的 JSON）与签名。
func parseEnvelope(content []byte) (payloadBytes, sig []byte, err error) {
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 4 {
		return nil, nil, fmt.Errorf("license 文件格式无效（应为 4 行信封）")
	}
	rawPayload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, nil, fmt.Errorf("payload base64 解码失败: %w", err)
	}
	// payload 的 minisign 前缀：2 字节算法号 0x0000。
	if len(rawPayload) > 2 {
		rawPayload = rawPayload[2:]
	}
	rawSig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil {
		return nil, nil, fmt.Errorf("签名 base64 解码失败: %w", err)
	}
	if len(rawSig) == ed25519.SignatureSize+2 {
		rawSig = rawSig[2:]
	}
	if len(rawSig) != ed25519.SignatureSize {
		return nil, nil, fmt.Errorf("签名长度无效：%d", len(rawSig))
	}
	return rawPayload, rawSig, nil
}

// validateFile 校验 license 文件内容并返回状态（不缓存）。
func validateFile(content []byte, dataDir string, now time.Time) State {
	st := State{Loaded: true}
	pub := pubKey()
	if pub == nil {
		st.Error = "构建未内嵌授权公钥（开发构建），授权功能不可用"
		return st
	}
	payloadBytes, sig, err := parseEnvelope(content)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if !ed25519.Verify(pub, payloadBytes, sig) {
		st.Error = "license 签名无效（不是授权方签发的文件）"
		return st
	}
	var p payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		st.Error = "license 内容解析失败: " + err.Error()
		return st
	}
	if p.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, p.ExpiresAt)
		if err != nil {
			st.Error = "license 到期时间格式无效"
			return st
		}
		if now.After(exp) {
			st.Error = fmt.Sprintf("license 已过期（到期 %s），已回落免费形态", exp.Format("2006-01-02"))
			return st
		}
		st.Expires = exp
	}
	if mid := strings.TrimSpace(p.MachineID); mid != "" {
		if mid != MachineID(dataDir) {
			st.Error = "license 绑定的机器指纹与本机不符（该授权文件发给了另一台机器）"
			return st
		}
		st.MachineID = mid
	}
	st.Valid = true
	st.Licensee = p.Licensee
	return st
}

// FilePath 返回 license 文件的绝对路径。
func FilePath(dataDir string) string {
	return filepath.Join(dataDir, FileName)
}

// LoadFrom 校验指定目录下的 license.dat（供设置页上传预览 / 启动加载共用）。
func LoadFrom(dataDir string) State {
	mu.Lock()
	defer mu.Unlock()
	// 缓存 60 秒：高频调用（/api/brand 每次页面加载）不重复读盘验签。
	if cached != nil && time.Since(cachedAt) < 60*time.Second {
		return *cached
	}
	raw, err := os.ReadFile(FilePath(dataDir))
	var st State
	if err != nil {
		st = State{Loaded: true, Error: "未安装 license 文件（免费形态：显示 Powered by）"}
	} else {
		st = validateFile(raw, dataDir, time.Now())
	}
	cached = &st
	cachedAt = time.Now()
	return st
}

// SaveTo 把 license 文件写入数据目录并立刻重新校验。
func SaveTo(dataDir string, content []byte) (State, error) {
	path := FilePath(dataDir)
	if err := os.WriteFile(path, content, 0600); err != nil {
		return State{}, fmt.Errorf("写入 license 文件失败: %w", err)
	}
	mu.Lock()
	cached = nil // 失效缓存
	mu.Unlock()
	st := LoadFrom(dataDir)
	if !st.Valid {
		return st, fmt.Errorf("license 校验未通过：%s", st.Error)
	}
	return st, nil
}

// IsWhiteLabelLicensed 判定当前是否允许隐藏 "Powered by"（授权核心开关）。
func IsWhiteLabelLicensed(dataDir string) bool {
	return LoadFrom(dataDir).Valid
}
