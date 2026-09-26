package auditchain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sampleEntries() []Entry {
	return []Entry{
		{Time: "2026-09-24 10:00:00", Action: "container.create", Target: "c-1", Detail: "ok", User: "admin", IP: "1.2.3.4", UserAgent: "ua", Success: true},
		{Time: "2026-09-24 10:01:00", Action: "container.power", Target: "c-1", Detail: "start", User: "admin", IP: "1.2.3.4", UserAgent: "ua", Success: true},
		{Time: "2026-09-24 10:02:00", Action: "container.delete", Target: "c-1", Detail: "ok", User: "admin", IP: "1.2.3.4", UserAgent: "ua", Success: false, Error: "not empty"},
	}
}

func TestChainAndVerify(t *testing.T) {
	entries := sampleEntries()
	if err := Chain(entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].PrevHash != "" {
		t.Fatal("first entry prev_hash must be empty")
	}
	if entries[1].PrevHash != entries[0].Hash {
		t.Fatal("second entry prev_hash must equal first hash")
	}
	if entries[2].PrevHash != entries[1].Hash {
		t.Fatal("third entry prev_hash must equal second hash")
	}
	if err := Verify(entries); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	// 篡改第二条的 Detail
	entries[1].Detail = "tampered"
	if err := Verify(entries); err == nil {
		t.Fatal("tampering must be detected")
	}
}

func TestVerifyDetectsReordering(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	// 交换两条记录
	entries[0], entries[2] = entries[2], entries[0]
	if err := Verify(entries); err == nil {
		t.Fatal("reordering must be detected")
	}
}

func TestVerifyDetectsDeletionHonest(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	// 删除中间条：仅依赖 prev_hash 的链无法可靠检测纯删除
	// （需外部锚点/发布最新 hash 到第三方；本包不实现）。
	// 这里只验证"长度变化后整链仍通过"，说明哈希链本身是有局限的。
	trimmed := append([]Entry(nil), entries[:2]...)
	if err := Verify(trimmed); err != nil {
		t.Fatalf("trimmed chain still verifies (delete not detected without external anchor): %v", err)
	}
	// 但对原文做"删一条 + 把后一条改 prev_hash"，必须检测到伪造：
	tampered := []Entry{entries[0], entries[2]}
	if err := Verify(tampered); err == nil {
		t.Fatal("tampered prev_hash after delete must be detected")
	}
}

func TestVerifyDetectsInjectedEntry(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	// 注入一条新记录
	injected := append(append([]Entry(nil), entries[:2]...), Entry{Time: "2026-09-24 10:01:30", Action: "injected", Target: "x", User: "admin", Success: true})
	injected = append(injected, entries[2])
	if err := Verify(injected); err == nil {
		t.Fatal("injection must be detected")
	}
}

func TestCEFFormat(t *testing.T) {
	entry := Entry{Action: "container.create", Target: "c-1", User: "admin", IP: "1.2.3.4", Success: true}
	out := CEF(entry)
	if !strings.HasPrefix(out, "CEF:0|") {
		t.Fatalf("CEF prefix missing: %s", out)
	}
	if !strings.Contains(out, "src=1.2.3.4") {
		t.Fatalf("src extension missing: %s", out)
	}
	if !strings.Contains(out, "outcome=success") {
		t.Fatalf("outcome extension missing: %s", out)
	}
}

func TestCEFEscapesPipesAndEquals(t *testing.T) {
	entry := Entry{Action: "evil|action=inject", Detail: "with=pipe|or", Success: true}
	out := CEF(entry)
	if strings.Contains(strings.SplitN(out, "|", 7)[6], "|evil|action=inject|") {
		t.Fatal("unescaped pipe in extension")
	}
}

func TestSyslogRFC5424(t *testing.T) {
	entry := Entry{Time: time.Now().Format("2006-01-02 15:04:05"), Action: "test", User: "admin", Success: true, Hash: "abc"}
	out := SyslogRFC5424(entry)
	if !strings.HasPrefix(out, "<14>1 ") {
		t.Fatalf("syslog priority/format wrong: %s", out)
	}
	if !strings.Contains(out, "hash=abc") {
		t.Fatalf("hash field missing: %s", out)
	}
}

func TestStreamCSV(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	var buf bytes.Buffer
	if err := StreamCSV(&buf, entries); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != 4 { // header + 3 entries
		t.Fatalf("expected 4 lines, got %d", lines)
	}
	if !bytes.Contains(buf.Bytes(), []byte(entries[2].Hash)) {
		t.Fatal("hash column must be exported")
	}
}

func TestCSVEscape(t *testing.T) {
	if csvEscape("plain") != "plain" {
		t.Fatal("plain must not be quoted")
	}
	if csvEscape("a,b") != `"a,b"` {
		t.Fatal("comma must be quoted")
	}
	if csvEscape(`a"b`) != `"a""b"` {
		t.Fatal("quote must be doubled")
	}
}

// TestCSVEscapeNeutralizesFormulaInjection 锁定 CSV 公式注入修复。
func TestCSVEscapeNeutralizesFormulaInjection(t *testing.T) {
	for _, in := range []string{`=cmd|'/C calc'!A0`, "+1+1", "-2+3", "@SUM(A1)"} {
		got := csvEscape(in)
		if got == in {
			t.Fatalf("formula-leading field %q must be neutralized", in)
		}
		if !strings.HasPrefix(got, "'") {
			t.Fatalf("neutralized field %q must be prefixed with a single quote", got)
		}
	}
	// 普通数值/文本不应被改写
	if csvEscape("2026-09-24 10:00:00") != "2026-09-24 10:00:00" {
		t.Fatal("normal timestamp must not be altered")
	}
}

// TestVerifyAcceptsTruncatedChainAnchor 锁定审计链截断修复：有界环形缓冲
// 截断后首条 PrevHash 指向已清理记录（非空锚点），Verify 必须仍能通过，
// 否则 chain=verify 端点会恒失败、篡改检测彻底失效。
func TestVerifyAcceptsTruncatedChainAnchor(t *testing.T) {
	entries := sampleEntries()
	Chain(entries)
	// 模拟仅保留最后两条（首条 PrevHash 指向被清理的前一条，非空）
	truncated := append([]Entry(nil), entries[1:]...)
	if truncated[0].PrevHash == "" {
		t.Fatal("precondition: truncated first entry must carry a non-empty anchor")
	}
	if err := Verify(truncated); err != nil {
		t.Fatalf("truncated chain must verify via anchor: %v", err)
	}
	// 截断链内篡改仍必须被发现
	truncated[1].Detail = "tampered"
	if err := Verify(truncated); err == nil {
		t.Fatal("tampering inside truncated chain must be detected")
	}
}

func TestChainEmpty(t *testing.T) {
	if err := Chain(nil); err != nil {
		t.Fatal(err)
	}
	if err := Verify(nil); err == nil {
		t.Fatal("verify empty must report ErrEmptyEntries to force callers to handle zero-length explicitly")
	}
}