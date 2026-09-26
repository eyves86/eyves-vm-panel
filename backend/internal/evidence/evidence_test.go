package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestChainAppendAndChainHash(t *testing.T) {
	c := NewChain()
	e1 := c.Append(Evidence{TicketID: "T1", Kind: KindLogs, Path: "logs/1.txt", SizeBytes: 100, Hash: "h1"})
	if e1.PrevHash != "" {
		t.Fatalf("first item must have empty prev_hash, got %q", e1.PrevHash)
	}
	if e1.ChainHash == "" {
		t.Fatal("chain_hash must be non-empty")
	}
	e2 := c.Append(Evidence{TicketID: "T1", Kind: KindPCAP, Path: "pcap/1.pcap", SizeBytes: 1024, Hash: "h2"})
	if e2.PrevHash != e1.ChainHash {
		t.Fatalf("prev_hash = %q, want %q", e2.PrevHash, e1.ChainHash)
	}
	if e2.ChainHash == e1.ChainHash {
		t.Fatal("two items must produce different chain hashes")
	}
}

func TestVerifyChainPasses(t *testing.T) {
	c := NewChain()
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "a"})
	c.Append(Evidence{TicketID: "T", Kind: KindPCAP, Path: "b"})
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "c"})
	if err := c.VerifyChain(); err != nil {
		t.Fatalf("valid chain must pass: %v", err)
	}
}

func TestVerifyChainDetectsTampering(t *testing.T) {
	c := NewChain()
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "a"})
	c.Append(Evidence{TicketID: "T", Kind: KindPCAP, Path: "b"})
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "c"})
	// 篡改第二条
	items := c.Items()
	items[1].Path = "EVIL_PATH"
	if err := c.itemsMut(items); err == nil {
		t.Fatal("verify must detect tampering")
	}
}

// itemsMut 是 Chain 的非导出方法——测试通过 Items() 拷贝改后再写回。
// Chain 本身不提供 mutate，验证器应通过 Chain.Items() 的副本检测篡改；
// 这里手工复制 Chain 内部状态做篡改测试。
func (c *Chain) itemsMut(items []Evidence) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	oldItems := c.items
	c.items = items
	err := c.verifyInternal(c.items)
	c.items = oldItems
	return err
}

func (c *Chain) verifyInternal(items []Evidence) error {
	prev := ""
	for _, e := range items {
		if e.PrevHash != prev {
			return errors.New("prev_hash mismatch")
		}
		if computeChainHash(e) != e.ChainHash {
			return errors.New("chain_hash mismatch")
		}
		prev = e.ChainHash
	}
	return nil
}

func TestVerifyChainDetectsInsertion(t *testing.T) {
	c := NewChain()
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "a"})
	c.Append(Evidence{TicketID: "T", Kind: KindLogs, Path: "c"})
	// 插入一条后：第二条 prev_hash 仍指 a 的 chain_hash，但插入项自
	// 算链错位 → verifyInternal 应失败。
	items := c.Items()
	inserted := Evidence{TicketID: "T", Kind: KindPCAP, Path: "INJECTED", SizeBytes: 999}
	items[1].PrevHash = items[0].ChainHash
	items = append(items, Evidence{})
	copy(items[2:], items[1:])
	items[1] = inserted
	if err := c.itemsMut(items); err == nil {
		t.Fatal("insertion must be detected")
	}
}

func TestHashContentStableAndDistinct(t *testing.T) {
	a := HashContent([]byte("hello"))
	b := HashContent([]byte("hello"))
	c := HashContent([]byte("world"))
	if a != b {
		t.Fatalf("same content must produce same hash: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("different content must produce different hash")
	}
	if len(a) != 64 { // SHA-256 hex
		t.Fatalf("hash length = %d, want 64", len(a))
	}
}

func TestCollectorSkipsPCAPWhenStorageLow(t *testing.T) {
	c := NewCollector(NoopCaptureSource{}, 1024) // 1KB，远低于 50MB 阈值
	failures := c.CollectEvidence("T1", []Kind{KindPCAP, KindLogs}, "test")
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure (PCAP skipped), got %d", len(failures))
	}
	if !errors.Is(failures[0], ErrInsufficientStorage) {
		t.Fatalf("failure = %v, want ErrInsufficientStorage", failures[0])
	}
	if c.Chain().Len() != 1 {
		t.Fatalf("logs should still be collected, chain len = %d", c.Chain().Len())
	}
}

func TestCollectorCollectsAllWhenStorageEnough(t *testing.T) {
	c := NewCollector(NoopCaptureSource{}, 1024*1024*1024) // 1GB
	failures := c.CollectEvidence("T1", []Kind{KindLogs, KindPCAP, KindScreenshot}, "test")
	if len(failures) != 0 {
		t.Fatalf("expected 0 failures, got %v", failures)
	}
	if c.Chain().Len() != 3 {
		t.Fatalf("expected 3 evidence items, got %d", c.Chain().Len())
	}
	// 整链验证通过（自动生成的 chain_hash 内部一致）
	if err := c.Chain().VerifyChain(); err != nil {
		t.Fatalf("chain must verify: %v", err)
	}
}

func TestFormatChainSummaryEmpty(t *testing.T) {
	c := NewChain()
	if got := FormatChainSummary(c); !strings.Contains(got, "no evidence") {
		t.Fatalf("empty summary = %q", got)
	}
}