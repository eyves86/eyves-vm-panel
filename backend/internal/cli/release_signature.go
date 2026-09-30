package cli

// release_signature.go —— 发行版签名校验（F-7）。
//
// 威胁模型：SHA256SUMS 与产物同源， attackers 拿到发布推送权即可同时替换两者；
// 哈希校验防运输损坏/串包，不防"发布源被攻陷"。 therefore 引入 ed25519 签名：
//
//	发布流程（CI）：EYVESCLOUD_SIGNING_KEY（ed25519 私钥，32B seed 的 hex）对
//	SHA256SUMS 内容签名，产出 SHA256SUMS.minisig 一并上传为 Release asset。
//	校验端（升级器/CLI）：内嵌公钥（编译期注入 EYVESCLOUD_RELEASE_PUBKEY），
//	拉取 SHA256SUMS + SHA256SUMS.minisig 用内嵌公钥验签。
//
// 公钥缺失（开发构建）时验签跳过并提示；签名文件缺失时默认**中止**（严格），
// 可用 EYVESCLOUD_UPDATE_ALLOW_UNSIGNED=1 显式放行（自签仓库/测试场景）。
//
// 签名格式（minisign 兼容信封）：
//
//	untrusted comment: minisign signature for SHA256SUMS
//	<base64 of "Ed" || 2-byte algorithm 0x0000 || 64-byte ed25519 signature>
//	untrusted comment: <hex pubkey>
//	<base64 pubkey>

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"eyvescloud/internal/version"
)

// releaseSignatureSuffix 是签名文件的 release asset 命名约定（minisign 官方后缀）。
// 修复历史笔误：曾写作 ".minisign"（多一个 n），导致升级器请求
// SHA256SUMS.minisign 404、所有签名发布都无法自动升级（生产实测踩坑）。
// 升级器对新旧两种命名都做尝试（".minisig" 优先，".minisign" 兼容）。
const releaseSignatureSuffix = ".minisig"

// releaseSignatureSuffixLegacy 是历史错误命名的兼容后缀。
const releaseSignatureSuffixLegacy = ".minisign"

// buildReleasePublicKey 由 ldflags 注入（-X eyvescloud/internal/cli.releasePubKeyHex=...）。
// 留空 = 未配置签发公钥（开发构建），验签跳过并提示。
var releasePubKeyHex = ""

// releaseSignatureFetchTimeout 下载签名文件的客户端超时。
const releaseSignatureFetchTimeout = 60 * time.Second

// ed25519SignatureSize 与 crypto/ed25519.SignatureSize 一致（64 字节）。
const ed25519SignatureSize = 64

// allowUnsigned 对"签名文件缺失"是否放行。
func allowUnsigned() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EYVESCLOUD_UPDATE_ALLOW_UNSIGNED"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// releasePubKey 解码内嵌公钥；未配置返回 nil。
func releasePubKey() ed25519.PublicKey {
	raw := strings.TrimSpace(releasePubKeyHex)
	if raw == "" {
		return nil
	}
	// 允许 minisign 信封第二行（hex pubkey）或裸 hex 两种输入。
	if strings.Contains(raw, "\n") {
		lines := strings.Split(strings.TrimSpace(raw), "\n")
		raw = strings.TrimSpace(lines[len(lines)-1])
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	// ed25519 公钥 32 字节；有些发布习惯带 2 字节算法前缀（minisign 格式）。
	if len(b) == ed25519.PublicKeySize+2 {
		b = b[2:]
	}
	if len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// fetchReleaseSignature 下载 SHA256SUMS.minisig（签名字段在发布清单之外，无法由
// 攻击者在仅攻陷产物目录时替换——与清单同 URL 空间，但校验基于内嵌公钥独立成立）。
func fetchReleaseSignature(signatureURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, signatureURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "eyvescloud-updater/"+version.Current())
	client := &http.Client{Timeout: releaseSignatureFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载签名文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载签名文件失败：HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<10))
}

// parseMinisignSignature 从 minisign 信封提取 ed25519 签名（第二行 base64）。
func parseMinisignSignature(content []byte) ([]byte, error) {
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("签名文件格式无效（缺少内容行）")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, fmt.Errorf("签名 base64 解码失败: %w", err)
	}
	// minisign: 2-byte algorithm + 64-byte signature（Ed25519 算法号 0x00 0x00）。
	if len(raw) == ed25519SignatureSize+2 {
		raw = raw[2:]
	}
	if len(raw) != ed25519SignatureSize {
		return nil, fmt.Errorf("签名长度无效：%d", len(raw))
	}
	return raw, nil
}

// verifyReleaseSignature 验签 SHA256SUMS。
//
//	内嵌公钥缺失（开发构建）：跳过并提示（不阻断——严格性由发布配置保证）。
//	签名文件缺失/下载失败/验签不通过：默认中止；ALLOW_UNSIGNED=1 显式放行。
func verifyReleaseSignature(checksumsURL string, checksumsBody []byte) error {
	pub := releasePubKey()
	if pub == nil {
		// 未配置公钥 = 开发构建或旧版升级器：退回纯 SHA-256 语义（不制造新故障）。
		return nil
	}
	sigURL := strings.TrimRight(checksumsURL, "/") + releaseSignatureSuffix
	sigBody, err := fetchReleaseSignature(sigURL)
	if err != nil {
		// 兼容历史命名：部分旧发布把签名传成了 SHA256SUMS.minisign。
		sigURL2 := strings.TrimRight(checksumsURL, "/") + releaseSignatureSuffixLegacy
		if body2, err2 := fetchReleaseSignature(sigURL2); err2 == nil {
			sigBody, err = body2, nil
		}
	}
	if err != nil {
		if allowUnsigned() {
			cliPrintf("警告：未获取到 %s（%v），已按 %s=1 放行无签名清单（不推荐）。\n",
				"SHA256SUMS"+releaseSignatureSuffix, err, "EYVESCLOUD_UPDATE_ALLOW_UNSIGNED")
			return nil
		}
		return fmt.Errorf("Release 未提供签名文件（%v）。生产升级默认要求签名校验；确属自签仓库请设置 EYVESCLOUD_UPDATE_ALLOW_UNSIGNED=1", err)
	}
	var sig []byte
	sig, err = parseMinisignSignature(sigBody)
	if err != nil {
		return fmt.Errorf("签名解析失败：%w", err)
	}
	// 对清单原始字节验签。
	ok := ed25519.Verify(pub, checksumsBody, sig)
	if !ok {
		return fmt.Errorf("SHA256SUMS 验签失败：签名与内嵌公钥不匹配。发布源可能被篡改，升级已中止")
	}
	cliPrintf("校验清单签名验证通过（ed25519）\n")
	return nil
}

// signURLFor 从校验清单 URL 推导签名 URL（同目录 + .minisig 后缀）。
func signURLFor(checksumsURL string) string {
	return strings.TrimRight(checksumsURL, "/") + releaseSignatureSuffix
}
