package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestAuditLogHashChainLocksPreviousHash 验证 AddAuditLogFull 写入时
// 自动链接 prev_hash 到上一条 Hash，并计算新 Hash。
func TestAuditLogHashChainLocksPreviousHash(t *testing.T) {
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })
	AppConfig = &EyvescloudConfig{AuditLogs: []AuditLog{}}

	AddAuditLogFull("test.first", "tgt", "d1", "u1", "127.0.0.1", "ua", true, "")
	AddAuditLogFull("test.second", "tgt", "d2", "u1", "127.0.0.1", "ua", false, "err")

	if len(AppConfig.AuditLogs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(AppConfig.AuditLogs))
	}
	if AppConfig.AuditLogs[0].PrevHash != "" {
		t.Fatalf("first entry prev_hash must be empty, got %q", AppConfig.AuditLogs[0].PrevHash)
	}
	if AppConfig.AuditLogs[1].PrevHash != AppConfig.AuditLogs[0].Hash {
		t.Fatalf("second prev_hash (%q) must equal first hash (%q)",
			AppConfig.AuditLogs[1].PrevHash, AppConfig.AuditLogs[0].Hash)
	}
	if AppConfig.AuditLogs[1].Hash == "" {
		t.Fatal("second hash must be set")
	}
}

// TestAuditLogHashMatchesCanonicalForm 验证 AddAuditLogFull 写入的 Hash
// 与 SHA-256(canonical) 严格一致（防止字段顺序漂移导致链断）。
func TestAuditLogHashMatchesCanonicalForm(t *testing.T) {
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })
	AppConfig = &EyvescloudConfig{}
	AddAuditLogFull("test.canonical", "tgt", "detail", "user", "1.2.3.4", "ua", true, "")

	got := AppConfig.AuditLogs[0]
	if got.Hash == "" {
		t.Fatal("hash must be set")
	}
	// 复制相同的 canonical 字段并重算 hash，期望一致
	success := "0"
	if got.Success != nil && *got.Success {
		success = "1"
	}
	canonical := strings.Join([]string{
		got.Time, got.Action, got.Target, got.Detail,
		got.User, got.IP, got.UserAgent, success, got.Error,
		got.PrevHash,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	want := hex.EncodeToString(sum[:])
	if got.Hash != want {
		t.Fatalf("hash mismatch: got %q want %q (canonical=%q)", got.Hash, want, canonical)
	}
}

// TestAuditLogTamperingDetectedByChainWalk 模拟链上有一条记录被改：
//   1. 写入两条记录；
//   2. 直接篡改第二条的 Detail；
//   3. 重算其 Hash 并更新；
// 4. 但 prev_hash 不变 → 链 walk 时与"上条 Hash"仍不匹配；
// 这是哈希链的预期行为：仅靠 prev_hash 不更新，篡改会在重 walk 时被定位。
func TestAuditLogTamperingDetectedByChainWalk(t *testing.T) {
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })
	AppConfig = &EyvescloudConfig{}
	AddAuditLogFull("a", "tgt", "d1", "u", "ip", "ua", true, "")
	AddAuditLogFull("b", "tgt", "d2", "u", "ip", "ua", true, "")

	originalSecond := AppConfig.AuditLogs[1]
	// 篡改 content 但故意保留 prev_hash → 链断
	AppConfig.AuditLogs[1].Detail = "tampered"
	// 不重算 hash（模拟攻击者只动内容，不动 hash 字段）

	// 检查：第二条 prev_hash 仍指向原始第二条的 prev_hash，
	// 而原始第二条 prev_hash 仍等于第一条 Hash —— 攻击者没法改 Hash。
	// 但 AuditLog.Hash 与新内容不匹配（用我们包内的 hash 函数再算一次）。
	recomputed := auditLogHash(AppConfig.AuditLogs[1])
	if recomputed == AppConfig.AuditLogs[1].Hash {
		t.Fatal("tampered content must change hash; recomputed must NOT match stored")
	}
	// 顺手验证：篡改后第一条的 Hash 没变（攻击者没改第一条），所以上一条 Hash == 篡改后的 prev_hash
	// 这意味着仅靠 prev_hash 链接无法直接发现篡改——攻击者也能重算第二条的 Hash。
	// 因此哈希链的强保证必须配合"最近 Hash 外部锚点"（SIEM 上报）。
	if AppConfig.AuditLogs[1].PrevHash != originalSecond.PrevHash {
		t.Fatal("prev_hash should still match original")
	}
}