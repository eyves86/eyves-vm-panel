package abusedesk

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStateMachineLegalTransitions(t *testing.T) {
	// 完整合法路径：new → notified → responded → resolved → closed。
	legal := []struct{ from, to Status }{
		{StatusNew, StatusNotified},
		{StatusNew, StatusClosed},
		{StatusNotified, StatusResponded},
		{StatusNotified, StatusEscalated},
		{StatusNotified, StatusClosed},
		{StatusResponded, StatusResolved},
		{StatusResponded, StatusEscalated},
		{StatusResponded, StatusClosed},
		{StatusResolved, StatusClosed},
		{StatusEscalated, StatusClosed},
		{StatusEscalated, StatusResolved},
	}
	for _, c := range legal {
		if !CanTransition(c.from, c.to) {
			t.Fatalf("expected legal: %s -> %s", c.from, c.to)
		}
	}
}

func TestStateMachineIllegalTransitions(t *testing.T) {
	illegal := []struct{ from, to Status }{
		{StatusClosed, StatusNew},
		{StatusClosed, StatusNotified},
		{StatusResolved, StatusNew},
		{StatusNew, StatusResponded},
		{StatusNew, StatusEscalated},
	}
	for _, c := range illegal {
		if CanTransition(c.from, c.to) {
			t.Fatalf("expected illegal: %s -> %s", c.from, c.to)
		}
	}
}

func TestOpenNotifyReplyResolveClose(t *testing.T) {
	notified := 0
	suspended := 0
	d := NewDesk(DefaultSLA(),
		NotifierFunc(func(t Ticket) error { notified++; return nil }),
		SuspendFunc(func(t Ticket) error { suspended++; return nil }),
		nil,
	)
	tk, err := d.OpenTicket("alert-1", "tenant-A", "[T-test] abuse/spam")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusNew {
		t.Fatalf("status = %v, want new", tk.Status)
	}
	if !strings.HasPrefix(tk.ID, "T-") {
		t.Fatalf("ticket id = %q, want T- prefix", tk.ID)
	}
	if err := d.Notify(tk.ID); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Fatalf("notifier called %d times, want 1", notified)
	}
	if err := d.Reply(tk.ID, "已修复，请复检"); err != nil {
		t.Fatal(err)
	}
	if err := d.Resolve(tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Get(tk.ID)
	if got.Status != StatusClosed {
		t.Fatalf("status = %v, want closed", got.Status)
	}
	if got.LastReplyExcerpt != "已修复，请复检" {
		t.Fatalf("excerpt = %q, want 完整中文", got.LastReplyExcerpt)
	}
}

func TestReplyBeforeNotifyIllegal(t *testing.T) {
	d := NewDesk(DefaultSLA(), nil, nil, nil)
	tk, _ := d.OpenTicket("a", "t", "x")
	if err := d.Reply(tk.ID, "r"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("reply before notify must error, got %v", err)
	}
}

func TestCloseFromAnyState(t *testing.T) {
	d := NewDesk(DefaultSLA(), nil, nil, nil)
	tk, _ := d.OpenTicket("a", "t", "x")
	if err := d.Close(tk.ID); err != nil {
		t.Fatal(err)
	}
}

// TestSLASweepEscalatesExpiredTickets 验证 SLA 过期升级。
func TestSLASweepEscalatesExpiredTickets(t *testing.T) {
	suspend := 0
	var mu sync.Mutex
	d := NewDesk(SLA{NotifiedResponse: 1 * time.Millisecond, ResponseResolve: time.Hour},
		NotifierFunc(func(t Ticket) error { return nil }),
		SuspendFunc(func(t Ticket) error { mu.Lock(); suspend++; mu.Unlock(); return nil }),
		nil,
	)
	tk, _ := d.OpenTicket("a", "t", "x")
	if err := d.Notify(tk.ID); err != nil {
		t.Fatal(err)
	}
	// 等到 NotifiedAt + SLA
	time.Sleep(20 * time.Millisecond)
	expired := d.SweepSLAExpirations()
	if len(expired) != 1 || expired[0] != tk.ID {
		t.Fatalf("expired = %v, want [%s]", expired, tk.ID)
	}
	got, _ := d.Get(tk.ID)
	if got.Status != StatusEscalated {
		t.Fatalf("status = %v, want escalated", got.Status)
	}
	mu.Lock()
	if suspend != 1 {
		t.Fatalf("suspend action called %d, want 1", suspend)
	}
	mu.Unlock()
}

// TestSLASweepIgnoresFreshTickets 验证未过期工单不被升级。
func TestSLASweepIgnoresFreshTickets(t *testing.T) {
	d := NewDesk(SLA{NotifiedResponse: 1 * time.Hour},
		NotifierFunc(func(t Ticket) error { return nil }),
		nil, nil)
	tk, _ := d.OpenTicket("a", "t", "x")
	d.Notify(tk.ID)
	if expired := d.SweepSLAExpirations(); len(expired) != 0 {
		t.Fatalf("fresh ticket must not be expired, got %v", expired)
	}
}

func TestParseSubjectTicketID(t *testing.T) {
	cases := []struct {
		subject string
		want    string
	}{
		{"[T-abc123] abuse/ddos", "[T-abc123]"},
		{"[T-deadbe] prefix [T-aaa] middle", "[T-deadbe]"},
		{"no ticket here", ""},
		{"[T-abc no closing bracket", ""},
	}
	for _, c := range cases {
		if got := ParseSubjectTicketID(c.subject); got != c.want {
			t.Fatalf("ParseSubjectTicketID(%q) = %q, want %q", c.subject, got, c.want)
		}
	}
}

func TestReplyTruncatesExcerpt(t *testing.T) {
	d := NewDesk(DefaultSLA(), NotifierFunc(func(t Ticket) error { return nil }), nil, nil)
	tk, _ := d.OpenTicket("a", "t", "x")
	d.Notify(tk.ID)
	longReply := strings.Repeat("x", 500)
	d.Reply(tk.ID, longReply)
	got, _ := d.Get(tk.ID)
	if len(got.LastReplyExcerpt) != 200 {
		t.Fatalf("excerpt len = %d, want 200", len(got.LastReplyExcerpt))
	}
}

func TestNotifyMissingTicket(t *testing.T) {
	d := NewDesk(DefaultSLA(), nil, nil, nil)
	if err := d.Notify("T-nonexist"); err == nil {
		t.Fatal("missing ticket must error")
	}
}