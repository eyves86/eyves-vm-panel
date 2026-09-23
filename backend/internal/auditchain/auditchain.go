// Package auditchain 实现 P8-3 审计强化：
//
//   - HashChain：每条审计记录追加 SHA-256(prev_hash || canonical_fields)，
//     校验时重算整链即可发现删改；
//   - CEF：Common Event Format 格式化（syslog/CEF 双格式），便于 SIEM
//     （Elastic/Wazuh 等）解析入库；
//   - StreamingExport：分块流式导出大文件，避免一次性 OOM。
//
// 本包只产出可审计的编码/校验原语；写入到 store 由调用方注入。
package auditchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Entry 一条审计记录的最小投影（hash 链只关心内容字段）。
type Entry struct {
	Time      string
	Action    string
	Target    string
	Detail    string
	User      string
	IP        string
	UserAgent string
	Success   bool
	Error     string
	// PrevHash 上条记录的 hash；空链首条为空字符串。
	PrevHash string
	// Hash 本条记录的 hash；由 Chain 计算并回填。
	Hash string
}

// Canonical 序列化：按字段顺序拼接 |\n；用于哈希计算。
// 注意：字段顺序必须稳定，否则校验全链会因拼接顺序变化而失败。
func (e Entry) canonical() string {
	success := "0"
	if e.Success {
		success = "1"
	}
	return strings.Join([]string{
		e.Time, e.Action, e.Target, e.Detail,
		e.User, e.IP, e.UserAgent, success, e.Error,
		e.PrevHash,
	}, "\n")
}

// ComputeHash 计算单条记录的 SHA-256 hash。
func (e Entry) ComputeHash() string {
	h := sha256.Sum256([]byte(e.canonical()))
	return hex.EncodeToString(h[:])
}

// Chain 顺序追加并维护 PrevHash / Hash 字段。
//
// 规则：第 N 条记录的 PrevHash 等于第 N-1 条的 Hash；首条 PrevHash = ""。
// 计算完成后 Hash 字段就地回填，调用方负责持久化。
func Chain(entries []Entry) error {
	for i := range entries {
		if i == 0 {
			entries[i].PrevHash = ""
		} else {
			entries[i].PrevHash = entries[i-1].Hash
		}
		entries[i].Hash = entries[i].ComputeHash()
	}
	return nil
}

// Verify 校验整链；返回首个错误条目的索引 + 错误。
// 校验规则：每条 PrevHash == 上条 Hash；每条 Hash == ComputeHash()。
func Verify(entries []Entry) error {
	if len(entries) == 0 {
		return ErrEmptyEntries
	}
	for i := range entries {
		if i == 0 {
			if entries[i].PrevHash != "" {
				return fmt.Errorf("auditchain: first entry prev_hash must be empty, got %q", entries[i].PrevHash)
			}
		} else {
			if entries[i].PrevHash != entries[i-1].Hash {
				return fmt.Errorf("auditchain: entry %d prev_hash %q != prev entry hash %q",
					i, entries[i].PrevHash, entries[i-1].Hash)
			}
		}
		if entries[i].Hash != entries[i].ComputeHash() {
			return fmt.Errorf("auditchain: entry %d hash mismatch", i)
		}
	}
	return nil
}

// CEF 构建一条 CEF 格式日志（syslog header + extension）。
// 字段：CEF:Version|Device Vendor|Device Product|Device Version|Signature ID|Name|Severity|Extension
//
// Severity 字段（0-10）由审计严重度映射而来。
func CEF(e Entry) string {
	sev := severityOf(e)
	sigID := sanitize(e.Action)
	name := sanitize(e.Action)
	if name == "" {
		name = "audit"
	}
	ext := strings.Builder{}
	writeCEFExt := func(k, v string) {
		if v == "" {
			return
		}
		if ext.Len() > 0 {
			ext.WriteString(" ")
		}
		ext.WriteString(k)
		ext.WriteString("=")
		ext.WriteString(escapeCEF(v))
	}
	writeCEFExt("src", e.IP)
	writeCEFExt("suser", e.User)
	writeCEFExt("cs1", e.Target)
	writeCEFExt("cs1Label", "target")
	writeCEFExt("cs2", e.Detail)
	writeCEFExt("cs2Label", "detail")
	writeCEFExt("cs3", e.UserAgent)
	writeCEFExt("cs3Label", "userAgent")
	writeCEFExt("cs4", e.Error)
	writeCEFExt("cs4Label", "error")
	if !e.Success {
		writeCEFExt("outcome", "failure")
	} else {
		writeCEFExt("outcome", "success")
	}
	return fmt.Sprintf("CEF:0|EyvesCloud|Audit|%s|%s|%s|%d|%s",
		time.Now().Format("2006-01-02"), sigID, name, sev, ext.String())
}

// SyslogRFC5424 构造符合 RFC 5424 的 syslog 消息；PRIORITY = 14 (info)。
func SyslogRFC5424(e Entry) string {
	msg := fmt.Sprintf("time=%s action=%s target=%q detail=%q user=%s ip=%s success=%t error=%q hash=%s",
		e.Time, e.Action, e.Target, e.Detail, e.User, e.IP, e.Success, e.Error, e.Hash)
	return fmt.Sprintf("<14>1 %s eyvescloud audit - - %s",
		time.Now().Format(time.RFC3339), escapeSyslog(msg))
}

// StreamCSV 流式写出 CSV；保证大文件不 OOM。writer 由调用方提供。
func StreamCSV(w io.Writer, entries []Entry) error {
	cw := newCountingWriter(w)
	if _, err := cw.WriteString("time,action,target,detail,user,ip,user_agent,success,error,prev_hash,hash\n"); err != nil {
		return err
	}
	for _, e := range entries {
		row := strings.Join([]string{
			csvEscape(e.Time),
			csvEscape(e.Action),
			csvEscape(e.Target),
			csvEscape(e.Detail),
			csvEscape(e.User),
			csvEscape(e.IP),
			csvEscape(e.UserAgent),
			boolStr(e.Success),
			csvEscape(e.Error),
			csvEscape(e.PrevHash),
			csvEscape(e.Hash),
		}, ",") + "\n"
		if _, err := cw.WriteString(row); err != nil {
			return err
		}
	}
	return nil
}

// severityOf 把"success/error/critical"映射到 CEF severity。
func severityOf(e Entry) int {
	if !e.Success {
		return 7
	}
	if strings.Contains(strings.ToLower(e.Action), "delete") {
		return 5
	}
	if strings.Contains(strings.ToLower(e.Action), "create") ||
		strings.Contains(strings.ToLower(e.Action), "update") {
		return 3
	}
	return 2
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "|", "_")
	s = strings.ReplaceAll(s, "=", "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func escapeCEF(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "=", "\\=")
	return s
}

func escapeSyslog(s string) string {
	// RFC 5424 section 6: SD params need escaping for ] and \
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	return s
}

func csvEscape(s string) string {
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, ",\"\r\n") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// countingWriter 把 io.Writer 包一层，未来若要加流控可在此扩展。
type countingWriter struct {
	w     io.Writer
	bytes int64
}

func newCountingWriter(w io.Writer) *countingWriter { return &countingWriter{w: w} }

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.bytes += int64(n)
	return n, err
}

func (c *countingWriter) WriteString(s string) (int, error) {
	return c.Write([]byte(s))
}

// ErrEmptyEntries 表示空链（无校验必要）。
var ErrEmptyEntries = errors.New("auditchain: entries is empty")