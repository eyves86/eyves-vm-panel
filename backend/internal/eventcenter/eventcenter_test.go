package eventcenter

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validEvent() Event {
	return Event{
		Type:       EventNodeStateChanged,
		Severity:   SeverityInfo,
		Subject:    "node-1",
		Message:    "transitioned to online",
		OccurredAt: time.Now(),
	}
}

func TestEventValidate(t *testing.T) {
	e := validEvent()
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := e
	bad.Type = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty type must error")
	}
	bad = e
	bad.Severity = "weird"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid severity must error")
	}
	bad = e
	bad.Subject = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty subject must error")
	}
	bad = e
	bad.OccurredAt = time.Time{}
	if err := bad.Validate(); err == nil {
		t.Fatal("zero occurred_at must error")
	}
}

// NewInMemorySink 是 InMemorySink 的工厂函数（与类型同名函数冲突规避）。
func NewInMemorySink() *InMemorySink { return &InMemorySink{} }

func TestRouterEmitPersistsAndRoutes(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	var received []Event
	r.Subscribe(EventNodeStateChanged, func(e Event) error {
		received = append(received, e)
		return nil
	})
	if err := r.Emit(validEvent()); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 {
		t.Fatalf("handler called %d times, want 1", len(received))
	}
	if len(sink.Events()) != 1 {
		t.Fatalf("sink events = %d, want 1", len(sink.Events()))
	}
}

func TestRouterSubscribeAll(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	var all []Event
	r.Subscribe("", func(e Event) error {
		all = append(all, e)
		return nil
	})
	r.Emit(validEvent())
	r.Emit(validEvent())
	if len(all) != 2 {
		t.Fatalf("all handler called %d, want 2", len(all))
	}
}

func TestRouterEmitValidation(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	if err := r.Emit(Event{}); err == nil {
		t.Fatal("empty event must error")
	}
}

func TestRouterEmitInvalidatesWhenHandlerFails(t *testing.T) {
	// 订阅者错误不影响其他订阅者,也不回滚 sink。
	sink := NewInMemorySink()
	r := NewRouter(sink)
	r.Subscribe(EventNodeStateChanged, func(e Event) error {
		return errors.New("subscriber boom")
	})
	r.Subscribe("", func(e Event) error { return nil })
	if err := r.Emit(validEvent()); err != nil {
		t.Fatalf("Emit must not propagate subscriber error: %v", err)
	}
	if len(sink.Events()) != 1 {
		t.Fatal("sink must still receive event")
	}
}

func TestQueryByTypeAndSeverity(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	now := time.Now()
	_ = r.Emit(Event{Type: EventAbuseAlert, Severity: SeverityCritical, Subject: "ip-1", OccurredAt: now})
	_ = r.Emit(Event{Type: EventBackupCompleted, Severity: SeverityInfo, Subject: "ct-1", OccurredAt: now.Add(-time.Hour)})
	_ = r.Emit(Event{Type: EventAbuseAlert, Severity: SeverityWarning, Subject: "ip-2", OccurredAt: now.Add(-2 * time.Hour)})

	got, err := Query(sink, Filter{Types: []EventType{EventAbuseAlert}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("type filter = %d, want 2", len(got))
	}

	got, _ = Query(sink, Filter{Severities: []Severity{SeverityCritical}})
	if len(got) != 1 {
		t.Fatalf("severity filter = %d, want 1", len(got))
	}

	got, _ = Query(sink, Filter{Subject: "ip-1"})
	if len(got) != 1 || got[0].Subject != "ip-1" {
		t.Fatalf("subject filter = %v", got)
	}
}

func TestQueryTimeRange(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	now := time.Now()
	_ = r.Emit(Event{Type: EventAbuseAlert, Severity: SeverityInfo, Subject: "x", OccurredAt: now.Add(-2 * time.Hour)})
	_ = r.Emit(Event{Type: EventAbuseAlert, Severity: SeverityInfo, Subject: "y", OccurredAt: now})

	got, _ := Query(sink, Filter{Since: now.Add(-time.Hour)})
	if len(got) != 1 {
		t.Fatalf("since filter = %d, want 1 (only 'y')", len(got))
	}
}

func TestQueryLimit(t *testing.T) {
	sink := NewInMemorySink()
	r := NewRouter(sink)
	for i := 0; i < 5; i++ {
		_ = r.Emit(validEvent())
	}
	got, _ := Query(sink, Filter{Limit: 2})
	if len(got) != 2 {
		t.Fatalf("limit = %d, want 2", len(got))
	}
}

func TestQueryOnlyInMemorySink(t *testing.T) {
	otherSink := &mockOtherSink{}
	_, err := Query(otherSink, Filter{})
	if err == nil {
		t.Fatal("Query must reject non-InMemorySink")
	}
	if !strings.Contains(err.Error(), "InMemorySink") {
		t.Fatalf("error must mention InMemorySink: %v", err)
	}
}

type mockOtherSink struct{}

func (m *mockOtherSink) Emit(Event) error { return nil }
