// Package eventcenter 统一事件中心（P7-1）：
//
//   - EventType 事件类型（node_state_changed / abuse_alert / backup_completed
//     / storage_watermark / volume_event / task_lifecycle 等）；
//   - Severity 严重度（info / warning / error / critical）；
//   - EventSink 写入 + 订阅接口（in-memory + 持久化由调用方注入）；
//   - Query 多条件查询（severity/type/time-range）；
//   - 路由：按类型/严重度分发到不同处理通道（P7-2 通知 / P3-1 处置 等）；
//   - 全量审计 + 持久化（SQLite events 表预留，本包只定义模型）。
package eventcenter

import (
	"errors"
	"sync"
	"time"
)

// EventType 事件类型枚举。
type EventType string

const (
	EventNodeStateChanged EventType = "node_state_changed"
	EventAbuseAlert        EventType = "abuse_alert"
	EventBackupCompleted   EventType = "backup_completed"
	EventBackupFailed      EventType = "backup_failed"
	EventStorageWatermark  EventType = "storage_watermark"
	EventVolumeEvent        EventType = "volume_event"
	EventTaskLifecycle     EventType = "task_lifecycle"
	EventRestoreDrill      EventType = "restore_drill"
	EventFailoverDecision  EventType = "failover_decision"
	EventDrainCompleted    EventType = "drain_completed"
)

// Severity 事件严重度。
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// IsValidSeverity 检查值合法。
func IsValidSeverity(s Severity) bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityError, SeverityCritical:
		return true
	}
	return false
}

// Event 事件记录。
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Severity  Severity  `json:"severity"`
	Subject   string    `json:"subject"`        // 受影响对象 ID（节点/IP/容器/卷 等）
	Message   string    `json:"message"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Validate 检查必填字段。
func (e Event) Validate() error {
	if e.Type == "" {
		return errors.New("eventcenter: type required")
	}
	if !IsValidSeverity(e.Severity) {
		return errors.New("eventcenter: invalid severity")
	}
	if e.Subject == "" {
		return errors.New("eventcenter: subject required")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("eventcenter: occurred_at required")
	}
	return nil
}

// ---- 仓储 / 查询 ----

// Sink 是事件落地抽象（持久化 + 路由分发）。
// 实现可注入 SQLite（按事件类型路由）/WebHook/邮件。
type Sink interface {
	Emit(e Event) error
}

// InMemorySink 内存 Sink（测试用 + 不持久化场景）。
type InMemorySink struct {
	mu     sync.Mutex
	events []Event
}

// Emit implements Sink。
func (s *InMemorySink) Emit(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// Events 返回已记录的事件副本。
func (s *InMemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// Filter 事件查询条件。
type Filter struct {
	Types     []EventType // 任一匹配
	Severities []Severity
	Subject   string     // 精确匹配
	Since     time.Time
	Until     time.Time
	Limit     int
}

// Query 从 sink 中筛选事件（按时间倒序）。
func Query(sink Sink, f Filter) ([]Event, error) {
	// 本包仅支持 InMemorySink 查询；持久化 sink 走 SQL。
	mem, ok := sink.(*InMemorySink)
	if !ok {
		return nil, errors.New("eventcenter: Query only supports InMemorySink")
	}
	all := mem.Events()
	out := make([]Event, 0, len(all))
	for _, e := range all {
		if len(f.Types) > 0 && !containsType(f.Types, e.Type) {
			continue
		}
		if len(f.Severities) > 0 && !containsSev(f.Severities, e.Severity) {
			continue
		}
		if f.Subject != "" && e.Subject != f.Subject {
			continue
		}
		if !f.Since.IsZero() && e.OccurredAt.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && e.OccurredAt.After(f.Until) {
			continue
		}
		out = append(out, e)
	}
	// 时间倒序（最新在前）
	for i := 0; i < len(out)/2; i++ {
		out[i], out[len(out)-1-i] = out[len(out)-1-i], out[i]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func containsType(types []EventType, t EventType) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

func containsSev(ss []Severity, s Severity) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ---- 路由 ----

// Router 按事件类型 / 严重度分发到不同的处理通道（订阅者）。
type Router struct {
	mu       sync.Mutex
	handlers map[string][]Handler // key: event_type or "all"
	sink     Sink
}

// Handler 是事件处理回调。
type Handler func(e Event) error

// NewRouter 创建路由器。
func NewRouter(sink Sink) *Router {
	return &Router{handlers: map[string][]Handler{}, sink: sink}
}

// Subscribe 订阅特定事件类型（type == "" 订阅所有）。
func (r *Router) Subscribe(eventType EventType, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(eventType)
	r.handlers[key] = append(r.handlers[key], h)
}

// Emit 写入事件 + 路由给订阅者；订阅者错误不影响后续派发。
func (r *Router) Emit(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if r.sink != nil {
		if err := r.sink.Emit(e); err != nil {
			return err
		}
	}
	r.mu.Lock()
	typedHandlers := r.handlers[string(e.Type)]
	allHandlers := r.handlers[""]
	handlers := append([]Handler{}, typedHandlers...)
	handlers = append(handlers, allHandlers...)
	r.mu.Unlock()
	for _, h := range handlers {
		_ = h(e) // 订阅错误不应阻塞其他订阅者
	}
	return nil
}