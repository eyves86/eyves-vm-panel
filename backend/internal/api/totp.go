package api

import (
	"crypto/subtle"
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
// 兼容包装：仅做校验、不做重放登记（供纯校验场景使用）。
func validTOTP(secret, code string) bool {
	_, ok := verifyTOTPWithCounter(secret, code, 0, time.Now())
	return ok
}

// verifyTOTPWithCounter 校验验证码，并返回该验证码对应的时间步（counter）。
//
// 审计 H-4 修复：TOTP 的有效窗口是 ±1 个周期（最长约 90 秒），同一验证码在窗口内
// 可被重复使用（例如被肩窥、录屏或日志泄露后重放）。调用方应把自身记录的
// lastUsedCounter 传入：
//   - 返回的 counter <= lastUsedCounter 时直接拒绝（同一或更早的时间步已消费）；
//   - 校验通过后把新 counter 持久化，保证每个时间步只能用一次。
// 比较使用常量时间，避免逐字节提前返回形成侧信道。
func verifyTOTPWithCounter(secret, code string, lastUsedCounter uint64, now time.Time) (uint64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	current := uint64(now.Unix() / totpPeriod)
	matched := false
	var matchedCounter uint64
	for offset := -totpWindowDeviationSteps; offset <= totpWindowDeviationSteps; offset++ {
		step := int64(current) + int64(offset)
		if step < 0 {
			continue
		}
		candidate, err := totpCodeAtStep(secret, uint64(step))
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			matched = true
			matchedCounter = uint64(step)
		}
	}
	if !matched {
		return 0, false
	}
	if matchedCounter <= lastUsedCounter {
		// 时间步已被消费过（重放）。
		return matchedCounter, false
	}
	return matchedCounter, true
}

// totpCodeAtStep 计算指定时间步的 TOTP 6 位码。
func totpCodeAtStep(secret string, counter uint64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("invalid TOTP secret")
	}
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

// TOTPNow 返回当前时间步（供调用方读取"已消费到哪一步"）。
func TOTPNow() uint64 {
	return uint64(time.Now().Unix() / totpPeriod)
}

// totpSetupURI 生成用于录入 TOTP 验证器的 otpauth:// URI（标准协议，兼容任意验证器 App）。
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
// 供用户用任意 TOTP 验证器 App 扫码录入。出错时返回空字符串（前端回退为手动输入）。
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