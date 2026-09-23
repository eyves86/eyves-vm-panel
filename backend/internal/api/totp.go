package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpPeriod   = 30 // 秒
	totpDigits   = 6
	totpAlgo     = "SHA1"
	totpIssuer   = "EyvesCloud"
	totpWindowDeviationSteps = 1 // 允许前后 1 个周期的时间偏差
)

// generateTOTPSecret 生成 20 字节随机密钥并返回 Base32 编码。
func generateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// totpCodeAt 计算给定时间下的 TOTP 6 位码（RFC 6238 / HMAC-SHA1）。
func totpCodeAt(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("invalid TOTP secret")
	}
	counter := uint64(t.Unix() / totpPeriod)
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off]) & 0x7f) << 24
	bin |= uint32(sum[off+1]) << 16
	bin |= uint32(sum[off+2]) << 8
	bin |= uint32(sum[off+3])
	code := bin % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// validTOTP 判断当前时刻给出的验证码是否与密钥匹配，允许前后一个周期的时差。
func validTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	now := time.Now()
	for offset := -totpWindowDeviationSteps; offset <= totpWindowDeviationSteps; offset++ {
		candidate, err := totpCodeAt(secret, now.Add(time.Duration(offset*totpPeriod)*time.Second))
		if err != nil {
			return false
		}
		if candidate == code {
			return true
		}
	}
	return false
}

// totpSetupURI 生成用于录入 Authenticator（Google Authenticator 兼容）的 otpauth:// URI。
func totpSetupURI(secret, account string) string {
	label := url.PathEscape(totpIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", totpAlgo)
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpQRDataURL 将 otpauth:// URI 渲染为 PNG 二维码，返回 data:image/png;base64 数据 URL，
// 供用户用 Google Authenticator 等 App 扫码录入。出错时返回空字符串（前端回退为手动输入）。
func totpQRDataURL(otpauthURI string) string {
	if otpauthURI == "" {
		return ""
	}
	png, err := qrcode.Encode(otpauthURI, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// generateBackupCodes 生成 n 个一次性备份码（每个 16 位十六进制）。
func generateBackupCodes(n int) []string {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			continue
		}
		codes = append(codes, fmt.Sprintf("%x", buf))
	}
	return codes
}

// hashBackupCodes 对备份码做 bcrypt 哈希，落库前调用。
func hashBackupCodes(codes []string) []string {
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(c), bcrypt.DefaultCost)
		if err != nil {
			continue
		}
		hashes = append(hashes, string(h))
	}
	return hashes
}

// verifyAndConsumeBackupCode 校验给定备份码并返回消费后的剩余哈希列表。
func verifyAndConsumeBackupCode(code string, hashes []string) ([]string, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return hashes, false
	}
	for i, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(code)) == nil {
			remaining := append([]string(nil), hashes...)
			remaining = append(remaining[:i], remaining[i+1:]...)
			return remaining, true
		}
	}
	return hashes, false
}

// adminVerify2FA 校验动态口令或一次性备份码；返回是否通过及消费后的备份码哈希。
func adminVerify2FA(secret string, enabled bool, code string, backupHashes []string) (passed bool, consumed []string) {
	code = strings.TrimSpace(code)
	if !enabled {
		return true, backupHashes
	}
	if secret == "" {
		return false, backupHashes
	}
	if validTOTP(secret, code) {
		return true, backupHashes
	}
	consumed, ok := verifyAndConsumeBackupCode(code, backupHashes)
	return ok, consumed
}