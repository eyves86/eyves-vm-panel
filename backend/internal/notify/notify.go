// Package notify 通知渠道（P7-2）：
//
//   - 渠道：email / webhook / 内部 in-app；调用方注入 Sender；
//   - 模板渲染：Go text/template 安全替换（{{.Subject}} + 简单邮件模板）；
//   - 退避：失败重试指数退避 + 死信上限；
//   - 订阅模型：per event_type + severity 阈值订阅，路由到多个渠道；
//   - 渲染注入防 XSS（邮件 body 仅 text/plain，禁止 HTML）。
//
// 本包只产出编排逻辑；真实邮件/Webhook 发送由调用方注入 Sender。
package notify

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ChannelKind 渠道类型。
type ChannelKind string

const (
	ChannelEmail   ChannelKind = "email"
	ChannelWebhook ChannelKind = "webhook"
	ChannelInApp   ChannelKind = "in_app"
)

// IsValidChannel 检查值合法。
func IsValidChannel(c ChannelKind) bool {
	switch c {
	case ChannelEmail, ChannelWebhook, ChannelInApp:
		return true
	}
	return false
}

// Severity 严重度（与 eventcenter.Severity 对齐；这里定义常量便于解耦）。
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Message 待发送的通知载荷。
type Message struct {
	Subject  string
	Body     string
	Severity Severity
	// EventType 来源事件类型（用于路由过滤）。
	EventType string
	// Recipient 收件人/URL/user id（按渠道类型而定）。
	Recipient string
	Metadata  map[string]string
}

// Subscription 订阅规则。
type Subscription struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`     // 收件用户
	Channel    ChannelKind    `json:"channel"`
	EventTypes []string       `json:"event_types"` // 任一匹配
	// MinSeverity 阈值订阅：>= MinSeverity 的事件才投递。
	MinSeverity Severity `json:"min_severity"`
	Enabled    bool      `json:"enabled"`
}

// ShouldDeliver 事件类型 + 严重度是否匹配订阅规则。
func (s Subscription) ShouldDeliver(eventType string, severity Severity) bool {
	if !s.Enabled {
		return false
	}
	if s.EventTypes != nil && len(s.EventTypes) > 0 {
		matched := false
		for _, t := range s.EventTypes {
			if t == eventType {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return severityRank(severity) >= severityRank(s.MinSeverity)
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityError:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

// Sender 渠道发送抽象。
type Sender interface {
	Kind() ChannelKind
	Send(msg Message) error
}

// ---- 模板 ----

// emailTemplate 是最小邮件正文模板（text/plain,禁止 HTML 注入）。
const emailTemplate = `Severity: {{.Severity}}
Event:   {{.EventType}}

{{.Body}}

---
Sent by eyvescloud notification center.
`

// RenderEmailBody 用简单占位符渲染邮件正文（无 HTML；XSS 安全）。
func RenderEmailBody(msg Message) (string, error) {
	if strings.ContainsAny(msg.Body, "<>") {
		return "", errors.New("notify: email body must be plain text, no HTML tags")
	}
	tmpl := emailTemplate
	rendered := tmpl
	rendered = replaceAll(rendered, "{{.Severity}}", string(msg.Severity))
	rendered = replaceAll(rendered, "{{.EventType}}", msg.EventType)
	rendered = replaceAll(rendered, "{{.Body}}", msg.Body)
	return rendered, nil
}

// replaceAll 简易全量替换（不引入 strings.ReplaceAll 以保证代码自包含）。
func replaceAll(s, old, new string) string {
	for {
		i := strings.Index(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

// ---- 重试 + 死信 ----

// MaxAttempts 失败投递最大尝试次数（死信上限）。
const MaxAttempts = 5

// Dispatcher 编排订阅匹配 + 渲染 + 发送 + 重试。
type Dispatcher struct {
	mu            sync.Mutex
	subscriptions []Subscription
	senders       map[ChannelKind]Sender
	// Backoff 自定义退避函数（默认 2^attempt 秒,封顶 1 分钟；测试可注入 0 加速）。
	Backoff func(attempt int) time.Duration
}

// NewDispatcher 创建调度器。
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		senders: map[ChannelKind]Sender{},
		Backoff: defaultBackoff,
	}
}

func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<attempt) * time.Second
	if d > time.Minute {
		d = time.Minute
	}
	return d
}

// RegisterSender 注册渠道 sender。
func (d *Dispatcher) RegisterSender(s Sender) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.senders[s.Kind()] = s
}

// Subscribe 注册订阅。
func (d *Dispatcher) Subscribe(s Subscription) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscriptions = append(d.subscriptions, s)
}

// Dispatch 把消息投递给所有匹配订阅；单次失败按退避重试 + 死信。
//
// 返回尝试失败的订阅列表（errors.New 形式）；调用方写审计。
func (d *Dispatcher) Dispatch(msg Message, eventType string) (failed []error) {
	d.mu.Lock()
	subs := make([]Subscription, len(d.subscriptions))
	copy(subs, d.subscriptions)
	senders := make(map[ChannelKind]Sender, len(d.senders))
	for k, v := range d.senders {
		senders[k] = v
	}
	d.mu.Unlock()

	for _, s := range subs {
		if !s.ShouldDeliver(eventType, msg.Severity) {
			continue
		}
		sender, ok := senders[s.Channel]
		if !ok {
			failed = append(failed, fmt.Errorf("notify: no sender for channel %q", s.Channel))
			continue
		}
		// 渲染正文（email 走模板，其它 channel 原样）
		payload := msg
		if sender.Kind() == ChannelEmail {
			body, err := RenderEmailBody(msg)
			if err != nil {
				failed = append(failed, fmt.Errorf("notify: render email: %w", err))
				continue
			}
			payload.Body = body
		}
		// 重试循环
		var lastErr error
		for attempt := 1; attempt <= MaxAttempts; attempt++ {
			if err := sender.Send(payload); err == nil {
				lastErr = nil
				break
			} else {
				lastErr = err
				if attempt < MaxAttempts {
					time.Sleep(d.Backoff(attempt))
				}
			}
		}
		if lastErr != nil {
			failed = append(failed, fmt.Errorf("notify: subscription %s dead-letter after %d attempts: %w", s.ID, MaxAttempts, lastErr))
		}
	}
	return
}

// ---- 测试桩 ----

type stubSender struct {
	kind  ChannelKind
	err   error
	calls int
	mu    sync.Mutex
}

// Kind implements Sender。
func (s *stubSender) Kind() ChannelKind { return s.kind }

// Send implements Sender。
func (s *stubSender) Send(Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

// Calls returns invocation count.
func (s *stubSender) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}