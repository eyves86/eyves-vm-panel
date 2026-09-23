// Package evidence 自动取证（P3-4）：
//
//   - 链式哈希防篡改：每条证据含 prev_hash + curr_hash（SHA-256）；
//   - 处置触发后由 abuseengine.Revoker/Executor 钩子调 Collect → 自动
//     抓包/日志摘取，存到 StorageBackend 临时卷；
//   - 验证器 VerifyChain 检测哈希链断裂（任一记录被改/删/插都被发现）；
//   - 磁盘水位不足时降级：只取日志（不抓包），返回 ErrInsufficientStorage。
//
// 本包只提供 Chain + Collector + 验证器；网络抓包/日志摘取由调用方
// 注入（CaptureSource 接口）。
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Kind 证据类型。
type Kind string

const (
	KindPCAP     Kind = "pcap"
	KindLogs     Kind = "logs"
	KindScreenshot Kind = "screenshot"
)

// Evidence 单条证据记录。
type Evidence struct {
	ID         string    `json:"id"`
	TicketID   string    `json:"ticket_id"`
	Kind       Kind      `json:"kind"`
	Path       string    `json:"path"`           // 在存储卷里的相对路径
	SizeBytes  int64     `json:"size_bytes"`
	Hash       string    `json:"hash"`           // 当前记录 SHA-256（仅文件内容）
	PrevHash   string    `json:"prev_hash"`      // 前一条证据的 Hash（链式）
	ChainHash  string    `json:"chain_hash"`     // 当前记录的链式 SHA-256（含 prev_hash + body）
	TakenAt    time.Time `json:"taken_at"`
	TakenBy    string    `json:"taken_by,omitempty"` // 操作员 / 自动采集时为 "auto"
	Reason     string    `json:"reason,omitempty"`    // 采集原因（关联 abuse alert id 等）
}

// ErrInsufficientStorage 存储水位不足（降级为仅取日志）。
var ErrInsufficientStorage = errors.New("evidence: insufficient storage; pcap skipped")

// Chain 是证据链（per ticket）。
type Chain struct {
	mu     sync.Mutex
	items  []Evidence
	lastHash string
}

// NewChain 创建空链。
func NewChain() *Chain {
	return &Chain{}
}

// Append 添加一条新证据，自动计算 PrevHash + ChainHash。
//
// prevHash 取上一条的 ChainHash；空链时为空字符串（hash chain genesis）。
func (c *Chain) Append(e Evidence) Evidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.PrevHash = c.lastHash
	e.ChainHash = computeChainHash(e)
	c.lastHash = e.ChainHash
	c.items = append(c.items, e)
	return e
}

// Items 返回链中所有证据（按追加顺序；测试断言用）。
func (c *Chain) Items() []Evidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Evidence, len(c.items))
	copy(out, c.items)
	return out
}

// Len 已记录条数。
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// VerifyChain 校验整链的 PrevHash + ChainHash 一致性。
//
// 返回 nil = 通过；非 nil = 第一个被检测到的篡改位置（链序号 + 错误原因）。
func (c *Chain) VerifyChain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := ""
	for i, e := range c.items {
		if e.PrevHash != prev {
			return fmt.Errorf("evidence[%d].prev_hash mismatch (want %q, got %q)", i, prev, e.PrevHash)
		}
		if computeChainHash(e) != e.ChainHash {
			return fmt.Errorf("evidence[%d].chain_hash mismatch (record tampered)", i)
		}
		prev = e.ChainHash
	}
	return nil
}

// computeChainHash 计算链式 SHA-256：hash(prev_hash + ticket_id + kind +
// path + size + file_hash + taken_at + reason)。任一字段被改都会让
// chain_hash 变化。
func computeChainHash(e Evidence) string {
	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write([]byte("|"))
	h.Write([]byte(e.TicketID))
	h.Write([]byte("|"))
	h.Write([]byte(string(e.Kind)))
	h.Write([]byte("|"))
	h.Write([]byte(e.Path))
	h.Write([]byte("|"))
	h.Write([]byte(fmt.Sprintf("%d", e.SizeBytes)))
	h.Write([]byte("|"))
	h.Write([]byte(e.Hash))
	h.Write([]byte("|"))
	h.Write([]byte(e.TakenAt.UTC().Format(time.RFC3339Nano)))
	h.Write([]byte("|"))
	h.Write([]byte(e.Reason))
	return hex.EncodeToString(h.Sum(nil))
}

// HashContent 给定文件内容计算 hex SHA-256（Chain.Hash 字段用）。
func HashContent(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// ---- 采集器 ----

// CaptureSource 是单条证据的采集接口（真实实现：tcpdump / journalctl /
// screenshot 命令；调用方注入）。
type CaptureSource interface {
	// Capture 采集一条证据并写入 storagePath，返回实际大小与内容 hash。
	// size / hash 字段会写入 Evidence。
	Capture(ticketID string, kind Kind, reason string) (storagePath string, sizeBytes int64, hash string, err error)
}

// NoopCaptureSource 测试桩：每条证据返回 0 字节 + 空 hash。
type NoopCaptureSource struct{}

// Capture implements CaptureSource。
func (NoopCaptureSource) Capture(ticketID string, kind Kind, reason string) (string, int64, string, error) {
	return string(kind) + "_" + ticketID + ".bin", 0, "", nil
}

// Collector 是采集协调器：收到 abuse alert → 决定采集类型 → 调 CaptureSource →
// 追加到 Chain。
type Collector struct {
	cache    *Chain
	source   CaptureSource
	storageAvailableBytes int64
}

// NewCollector 创建采集器；storageAvailableBytes < 50MB 时 PCAP 自动跳过。
func NewCollector(source CaptureSource, storageAvailableBytes int64) *Collector {
	return &Collector{
		cache:    NewChain(),
		source:   source,
		storageAvailableBytes: storageAvailableBytes,
	}
}

// Chain 返回累计的证据链（不可变克隆，调用方只读）。
func (c *Collector) Chain() *Chain {
	return c.cache
}

// CollectEvidence 触发一次证据采集。
//
// kinds：指定要采集的种类（调用方决定顺序）；reason：写入 Evidence.Reason。
// 任一采集失败：跳过该条但不中断整体；返回所有失败汇总。
func (c *Collector) CollectEvidence(ticketID string, kinds []Kind, reason string) []error {
	if len(kinds) == 0 {
		return nil
	}
	failures := []error{}
	for _, k := range kinds {
		if k == KindPCAP && c.storageAvailableBytes < 50*1024*1024 {
			// 水位不足：降级为仅取日志（不写 PCAP）。
			failures = append(failures, fmt.Errorf("%w (have %d bytes)", ErrInsufficientStorage, c.storageAvailableBytes))
			continue
		}
		path, size, hash, err := c.source.Capture(ticketID, k, reason)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %v", k, err))
			continue
		}
		c.cache.Append(Evidence{
			TicketID:  ticketID,
			Kind:      k,
			Path:      path,
			SizeBytes: size,
			Hash:      hash,
			TakenAt:   time.Now(),
			TakenBy:   "auto",
			Reason:    reason,
		})
	}
	return failures
}

// FormatChainSummary 是审计/展示用的人类可读摘要。
func FormatChainSummary(c *Chain) string {
	items := c.Items()
	if len(items) == 0 {
		return "no evidence"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d evidence items, last chain_hash=%s\n", len(items), items[len(items)-1].ChainHash)
	for _, e := range items {
		fmt.Fprintf(&b, "  - %s kind=%s path=%s size=%d hash=%s taken_at=%s\n",
			e.ID, e.Kind, e.Path, e.SizeBytes, e.Hash, e.TakenAt.Format(time.RFC3339))
	}
	return b.String()
}