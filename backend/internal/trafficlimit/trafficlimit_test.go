package trafficlimit

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNormalizePolicyDefaults(t *testing.T) {
	p := NormalizePolicy(Policy{MonthlyQuotaGB: 100})
	if p.SoftLimitPct != 80 || p.HardLimitPct != 100 {
		t.Fatalf("defaults wrong: %+v", p)
	}
	if p.SoftAction != string(ActionThrottle) || p.HardAction != string(ActionSuspend) {
		t.Fatalf("actions wrong: %+v", p)
	}
	if p.CooldownHours != 24 {
		t.Fatalf("cooldown wrong: %d", p.CooldownHours)
	}
}

func TestNormalizePolicyClamps(t *testing.T) {
	p := NormalizePolicy(Policy{SoftLimitPct: 0, HardLimitPct: 50})
	// hard 50 <= soft 80 → 强制 hard=100
	if p.HardLimitPct != 100 {
		t.Fatalf("hard must clamp to 100, got %d", p.HardLimitPct)
	}
}

func TestEvalUnconfiguredPolicy(t *testing.T) {
	d := Eval(Policy{}, 1024*1024*1024, mustParse(t, "2026-09-15T10:00:00Z"))
	if d.Level != "none" {
		t.Fatalf("unconfigured must be none, got %v", d)
	}
	if d.Action != ActionNotify {
		t.Fatalf("action = %v, want notify", d.Action)
	}
}

func TestEvalBelowSoft(t *testing.T) {
	p := Policy{MonthlyQuotaGB: 10, SoftLimitPct: 80, HardLimitPct: 100}
	// 已用 5GB = 50%
	d := Eval(p, 5*1024*1024*1024, mustParse(t, "2026-09-15T10:00:00Z"))
	if d.Level != "none" {
		t.Fatalf("50%% must be none, got %v", d)
	}
}

func TestEvalAtSoftTriggersThrottle(t *testing.T) {
	p := NormalizePolicy(Policy{MonthlyQuotaGB: 10})
	d := Eval(p, 8*1024*1024*1024, mustParse(t, "2026-09-15T10:00:00Z"))
	if d.Level != "soft" || d.Action != ActionThrottle {
		t.Fatalf("80%% must trigger soft/throttle, got %v", d)
	}
	if d.NextAction == nil || *d.NextAction != ActionSuspend {
		t.Fatalf("soft must set NextAction=suspend, got %v", d.NextAction)
	}
}

func TestEvalAtHardTriggersSuspend(t *testing.T) {
	p := NormalizePolicy(Policy{MonthlyQuotaGB: 10})
	d := Eval(p, 11*1024*1024*1024, mustParse(t, "2026-09-15T10:00:00Z"))
	if d.Level != "hard" || d.Action != ActionSuspend {
		t.Fatalf("110%% must trigger hard/suspend, got %v", d)
	}
}

func TestEvalReportsPeriod(t *testing.T) {
	p := Policy{MonthlyQuotaGB: 100}
	d := Eval(p, 1<<30, mustParse(t, "2026-12-01T00:00:00Z"))
	if d.Period != "2026-12" {
		t.Fatalf("period = %q, want 2026-12", d.Period)
	}
}

func TestIsValidAction(t *testing.T) {
	for _, a := range []Action{ActionThrottle, ActionSuspend, ActionShutdown, ActionNotify} {
		if !IsValidAction(a) {
			t.Fatalf("%v must be valid", a)
		}
	}
	if IsValidAction("invalid") {
		t.Fatal("invalid must be rejected")
	}
}

// ---- Counters / 增量累计 ----

func TestApplyDeltaFirstReport(t *testing.T) {
	c := Counters{InstanceID: 1}
	now := mustParse(t, "2026-09-15T10:00:00Z")
	nc, archive := ApplyDelta(c, 100, 200, now)
	if archive != nil {
		t.Fatalf("first report must not archive, got %+v", archive)
	}
	if nc.Period != "2026-09" || nc.BytesIn != 100 || nc.BytesOut != 200 {
		t.Fatalf("unexpected state: %+v", nc)
	}
	if !nc.LastReported.Equal(now) {
		t.Fatalf("last_reported not set")
	}
}

func TestApplyDeltaSamePeriodAccumulate(t *testing.T) {
	c := Counters{InstanceID: 1, Period: "2026-09"}
	now := mustParse(t, "2026-09-15T10:00:00Z")
	nc, _ := ApplyDelta(c, 500, 600, now)
	if nc.BytesIn != 500 || nc.BytesOut != 600 {
		t.Fatalf("accumulate wrong: %+v", nc)
	}
	// 第二次上报：同账期累加。
	nc2, _ := ApplyDelta(nc, 100, 200, now)
	if nc2.BytesIn != 600 || nc2.BytesOut != 800 {
		t.Fatalf("accumulate wrong: %+v", nc2)
	}
}

func TestApplyDeltaRolloverArchives(t *testing.T) {
	c := Counters{InstanceID: 1, Period: "2026-08", BytesIn: 1000, BytesOut: 2000}
	now := mustParse(t, "2026-09-01T00:00:00Z")
	nc, archive := ApplyDelta(c, 100, 200, now)
	if archive == nil {
		t.Fatal("rollover must produce archive")
	}
	if archive.Period != "2026-08" || archive.BytesIn != 1000 || archive.BytesOut != 2000 {
		t.Fatalf("archive wrong: %+v", archive)
	}
	if nc.Period != "2026-09" || nc.BytesIn != 100 || nc.BytesOut != 200 {
		t.Fatalf("new period wrong: %+v", nc)
	}
}

func TestIsSamePeriod(t *testing.T) {
	if !IsSamePeriod("2026-09", "2026-09") {
		t.Fatal("same must be true")
	}
	if IsSamePeriod("2026-09", "2026-10") {
		t.Fatal("different must be false")
	}
	if !IsSamePeriod("2026-09", "  2026-09  ") {
		t.Fatal("trim must allow match")
	}
}

// 防 import 折叠。
var _ = strings.TrimSpace