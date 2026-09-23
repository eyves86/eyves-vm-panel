package notify

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionShouldDeliver(t *testing.T) {
	sub := Subscription{
		Channel:    ChannelEmail,
		EventTypes: []string{"abuse_alert", "storage_watermark"},
		MinSeverity: SeverityWarning,
		Enabled:    true,
	}
	if !sub.ShouldDeliver("abuse_alert", SeverityError) {
		t.Fatal("matching event type + higher severity must deliver")
	}
	if sub.ShouldDeliver("abuse_alert", SeverityInfo) {
		t.Fatal("info below warning must not deliver")
	}
	if sub.ShouldDeliver("backup_completed", SeverityError) {
		t.Fatal("non-matching event type must not deliver")
	}
	disabled := sub
	disabled.Enabled = false
	if disabled.ShouldDeliver("abuse_alert", SeverityCritical) {
		t.Fatal("disabled subscription must never deliver")
	}
}

func TestSubscriptionMinSeverityBoundaries(t *testing.T) {
	sub := Subscription{Enabled: true, MinSeverity: SeverityWarning}
	for _, c := range []struct {
		sev    Severity
		expect bool
	}{
		{SeverityInfo, false},
		{SeverityWarning, true},
		{SeverityError, true},
		{SeverityCritical, true},
	} {
		if got := sub.ShouldDeliver("anything", c.sev); got != c.expect {
			t.Fatalf("sev %v: got %v, want %v", c.sev, got, c.expect)
		}
	}
}

func TestRenderEmailBodyRejectsHTML(t *testing.T) {
	if _, err := RenderEmailBody(Message{Body: "<script>alert(1)</script>", Severity: SeverityInfo}); err == nil {
		t.Fatal("HTML tags must be rejected")
	}
	if _, err := RenderEmailBody(Message{Body: "plain text body", Severity: SeverityInfo}); err != nil {
		t.Fatalf("plain text must pass: %v", err)
	}
}

func TestRenderEmailBodySubstitution(t *testing.T) {
	msg := Message{Subject: "alert", Body: "ip 1.2.3.4 banned", Severity: SeverityCritical, EventType: "abuse_alert"}
	out, err := RenderEmailBody(msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Severity: critical", "Event:   abuse_alert", "ip 1.2.3.4 banned"} {
		if !strings.Contains(out, want) {
			t.Fatalf("email body missing %q: %s", want, out)
		}
	}
}

func TestDispatchRoutesByChannelAndSeverity(t *testing.T) {
	d := NewDispatcher()
	email := &stubSender{kind: ChannelEmail}
	webhook := &stubSender{kind: ChannelWebhook}
	d.RegisterSender(email)
	d.RegisterSender(webhook)
	d.Subscribe(Subscription{ID: "s1", Channel: ChannelEmail, EventTypes: []string{"abuse_alert"}, MinSeverity: SeverityCritical, Enabled: true})
	d.Subscribe(Subscription{ID: "s2", Channel: ChannelWebhook, EventTypes: []string{"backup_failed"}, MinSeverity: SeverityError, Enabled: true})
	d.Subscribe(Subscription{ID: "s3", Channel: ChannelEmail, MinSeverity: SeverityInfo, Enabled: true})

	failed := d.Dispatch(Message{Subject: "x", Body: "y", Severity: SeverityCritical, EventType: "abuse_alert"}, "abuse_alert")
	if len(failed) != 0 {
		t.Fatalf("unexpected failures: %v", failed)
	}
	// s1 收到 + s3 收到(都 email,critical 满足);s2 不匹配 event_type。
	if email.Calls() != 2 {
		t.Fatalf("email sender called %d, want 2", email.Calls())
	}
	if webhook.Calls() != 0 {
		t.Fatalf("webhook sender called %d, want 0", webhook.Calls())
	}
}

func TestDispatchDeadLetterAfterRetries(t *testing.T) {
	d := NewDispatcher()
	d.Backoff = func(int) time.Duration { return 0 }
	sender := &stubSender{kind: ChannelEmail, err: errors.New("smtp down")}
	d.RegisterSender(sender)
	d.Subscribe(Subscription{ID: "s1", Channel: ChannelEmail, MinSeverity: SeverityInfo, Enabled: true})

	failed := d.Dispatch(Message{Subject: "x", Body: "y", Severity: SeverityInfo, EventType: "x"}, "x")
	if len(failed) != 1 {
		t.Fatalf("expected 1 dead-letter, got %d", len(failed))
	}
	if sender.Calls() != MaxAttempts {
		t.Fatalf("attempts = %d, want %d", sender.Calls(), MaxAttempts)
	}
}

func TestDispatchMissingSenderReportsError(t *testing.T) {
	d := NewDispatcher()
	d.Subscribe(Subscription{ID: "s", Channel: ChannelWebhook, MinSeverity: SeverityInfo, Enabled: true})
	failed := d.Dispatch(Message{Subject: "x", Severity: SeverityInfo}, "x")
	if len(failed) != 1 {
		t.Fatalf("expected 1 failure (no sender), got %d", len(failed))
	}
}

func TestIsValidChannel(t *testing.T) {
	for _, c := range []ChannelKind{ChannelEmail, ChannelWebhook, ChannelInApp} {
		if !IsValidChannel(c) {
			t.Fatalf("%v must be valid", c)
		}
	}
	if IsValidChannel("bogus") {
		t.Fatal("bogus must be invalid")
	}
}