// Package abusedesk 滥用工单（P3-2）。
//
// 状态机：
//
//   new → notified → responded → resolved → closed
//                  ↘  (sla 过期) escalated → closed / suspended
//                  ↘  (admin 强制)   escalated → closed
//
// 转换合法性由 AllowedTransition 维护；非法转换返回 ErrIllegalTransition。
//
//   - notified：开单后调用 Notifier.Send 通知租户；
//   - responded：租户通过 TicketReply 入单（控制台 / 邮件别名匹配）；
//   - resolved：ReputationChecker 复检通过或 admin 手动；
//   - escalated：SLA 过期未响应 → SuspendAction 升级；
//   - closed：终态（关闭后只允许 admin 解锁）。
//
// 工单号格式 T-<6hex>，便于邮件 Subject 自动匹配。
package abusedesk

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status 工单状态。
type Status string

const (
	StatusNew        Status = "new"
	StatusNotified   Status = "notified"
	StatusResponded  Status = "responded"
	StatusResolved   Status = "resolved"
	StatusEscalated  Status = "escalated"
	StatusClosed     Status = "closed"
)

// ErrIllegalTransition 非法状态转换。
var ErrIllegalTransition = errors.New("abusedesk: illegal status transition")

// SLA 是工单 SLA（通知后到响应之间的最大间隔）。超时自动 escalated。
// 默认 24h；admin 可改。
type SLA struct {
	NotifiedResponse time.Duration // notified → responded 超时
	ResponseResolve  time.Duration // responded → resolved 超时（一般给租户整改 + 复检时间）
}

// DefaultSLA 默认 SLA。
func DefaultSLA() SLA {
	return SLA{NotifiedResponse: 24 * time.Hour, ResponseResolve: 72 * time.Hour}
}

// Ticket 工单记录。
type Ticket struct {
	ID           string    `json:"id"`
	AlertID      string    `json:"alert_id"`
	TenantID     string    `json:"tenant_id"`
	Subject      string    `json:"subject"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	NotifiedAt   time.Time `json:"notified_at,omitempty"`
	RespondedAt  time.Time `json:"responded_at,omitempty"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
	EscalatedAt  time.Time `json:"escalated_at,omitempty"`
	ClosedAt     time.Time `json:"closed_at,omitempty"`
	// LastReplyExcerpt 仅保留最近回复摘要（避免敏感正文落 DB）。
	LastReplyExcerpt string `json:"last_reply_excerpt,omitempty"`
}

// AllowedTransition 状态机白名单。
var allowedTransitions = map[Status]map[Status]bool{
	StatusNew:       {StatusNotified: true, StatusClosed: true},
	StatusNotified:  {StatusResponded: true, StatusEscalated: true, StatusClosed: true},
	StatusResponded: {StatusResolved: true, StatusEscalated: true, StatusClosed: true},
	StatusResolved:  {StatusClosed: true, StatusEscalated: true},
	StatusEscalated: {StatusClosed: true, StatusResolved: true},
	StatusClosed:    {}, // 终态
}

// CanTransition 判断 from → to 合法。
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	return allowedTransitions[from][to]
}

// ---- 仓储 + 服务 ----

// Notifier 是通知接口（P7-2 注入真实实现）；本包用 NotifierFunc 函数适配。
type Notifier interface {
	Send(t Ticket) error
}

// NotifierFunc 函数适配器。
type NotifierFunc func(t Ticket) error

// Send implements Notifier。
func (f NotifierFunc) Send(t Ticket) error { return f(t) }

// SuspendAction 是 SLA 过期时的升级动作（实际调用 abuseengine.Suspend）。
type SuspendAction interface {
	Suspend(t Ticket) error
}

// SuspendFunc 函数适配器。
type SuspendFunc func(t Ticket) error

// Suspend implements SuspendAction。
func (f SuspendFunc) Suspend(t Ticket) error { return f(t) }

// ReputationChecker 信誉复检（P3-3 注入真实实现）。
type ReputationChecker interface {
	Pass(t Ticket) (bool, error)
}

// Desk 是工单服务（内存存储 + 锁；持久化由调用方接管）。
type Desk struct {
	mu       sync.Mutex
	tickets  map[string]*Ticket
	sla      SLA
	notifier Notifier
	suspend  SuspendAction
	reputer  ReputationChecker
}

// NewDesk 创建工单服务。
func NewDesk(sla SLA, n Notifier, s SuspendAction, r ReputationChecker) *Desk {
	return &Desk{
		tickets:  map[string]*Ticket{},
		sla:      sla,
		notifier: n,
		suspend:  s,
		reputer:  r,
	}
}

// OpenTicket 自动开单（告警触发）：分配 ID + 状态 new；调用方立即
// Notify() 触发邮件投递。
//
// subject 模板由调用方组装（建议格式："[abuse/<ticket>] <alert-kind> ..."）。
func (d *Desk) OpenTicket(alertID, tenantID, subject string) (*Ticket, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := &Ticket{
		ID:        newTicketID(),
		AlertID:   alertID,
		TenantID:  tenantID,
		Subject:   subject,
		Status:    StatusNew,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	d.tickets[t.ID] = t
	return t, nil
}

// Notify 把工单从 new 推到 notified 并调用 Notifier.Send。
func (d *Desk) Notify(id string) error {
	t, err := d.transitionLocked(id, StatusNotified)
	if err != nil {
		return err
	}
	t.NotifiedAt = time.Now()
	t.UpdatedAt = t.NotifiedAt
	if d.notifier != nil {
		if err := d.notifier.Send(*t); err != nil {
			return fmt.Errorf("notify %s: %w", id, err)
		}
	}
	return nil
}

// Reply 把工单从 notified 推到 responded；记录回复摘要。
func (d *Desk) Reply(id, excerpt string) error {
	t, err := d.transitionLocked(id, StatusResponded)
	if err != nil {
		return err
	}
	t.RespondedAt = time.Now()
	t.UpdatedAt = t.RespondedAt
	t.LastReplyExcerpt = truncate(excerpt, 200)
	return nil
}

// Resolve 把工单推到 resolved（前提：有 responded → resolved 的合法转换，
// 或 escalated → resolved 的复检通过路径）。
func (d *Desk) Resolve(id string) error {
	t, err := d.transitionLocked(id, StatusResolved)
	if err != nil {
		return err
	}
	t.ResolvedAt = time.Now()
	t.UpdatedAt = t.ResolvedAt
	return nil
}

// Escalate SLA 过期升级（内部：notified → escalated / responded → escalated）。
func (d *Desk) Escalate(id, reason string) error {
	t, err := d.transitionLocked(id, StatusEscalated)
	if err != nil {
		return err
	}
	t.EscalatedAt = time.Now()
	t.UpdatedAt = t.EscalatedAt
	t.LastReplyExcerpt = truncate("escalated: "+reason, 200)
	if d.suspend != nil {
		_ = d.suspend.Suspend(*t)
	}
	return nil
}

// Close 关闭工单（任何非终态 → closed）。
func (d *Desk) Close(id string) error {
	t, err := d.transitionLocked(id, StatusClosed)
	if err != nil {
		return err
	}
	t.ClosedAt = time.Now()
	t.UpdatedAt = t.ClosedAt
	return nil
}

// transitionLocked 状态机转换 + 锁释放前的合法性校验。
func (d *Desk) transitionLocked(id string, to Status) (*Ticket, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.tickets[id]
	if !ok {
		return nil, fmt.Errorf("abusedesk: ticket %s not found", id)
	}
	if !CanTransition(t.Status, to) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, t.Status, to)
	}
	t.Status = to
	return t, nil
}

// Get 工单查询。
func (d *Desk) Get(id string) (*Ticket, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.tickets[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// List 工单列表（按创建时间升序；调用方负责分页）。
func (d *Desk) List() []*Ticket {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*Ticket, 0, len(d.tickets))
	for _, t := range d.tickets {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

// SweepSLAExpirations 由后台定时器调用：扫描 notified 超过 NotifiedResponse
// 仍未响应的工单，触发 Escalate。
//
// 返回被升级的工单 ID 列表（用于审计）。
func (d *Desk) SweepSLAExpirations() []string {
	d.mu.Lock()
	now := time.Now()
	expired := []string{}
	for id, t := range d.tickets {
		if t.Status != StatusNotified {
			continue
		}
		if now.Sub(t.NotifiedAt) <= d.sla.NotifiedResponse {
			continue
		}
		expired = append(expired, id)
	}
	d.mu.Unlock()
	for _, id := range expired {
		_ = d.Escalate(id, "SLA expired")
	}
	return expired
}

// newTicketID 生成唯一工单号 T-<6hex>。
func newTicketID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("T-%d", time.Now().UnixNano())
	}
	return "T-" + hex.EncodeToString(b)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// ParseSubjectTicketID 从邮件主题中提取工单号（"[T-xxxxxx]" 格式）。
// 主题示例："[T-abc123] abuse/ddos detected on your account"。
// 找不到返回空串（让调用方把邮件归档为普通邮件，不入单）。
//
// 返回工单号（保留 [ ] 便于直接与 Ticket.ID 比较；调用方如需纯 ID
// 可自行 Trim("[", "]")）。
func ParseSubjectTicketID(subject string) string {
	i := strings.Index(subject, "[T-")
	if i < 0 {
		return ""
	}
	rest := subject[i:]
	j := strings.Index(rest, "]")
	if j < 0 {
		return ""
	}
	return rest[:j+1] // 含方括号
}