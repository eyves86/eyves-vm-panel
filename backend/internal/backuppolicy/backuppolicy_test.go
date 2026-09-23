package backuppolicy

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseCronValidExpressions(t *testing.T) {
	cases := []string{
		"0 0 * * *",       // 每天 0 点
		"*/5 * * * *",     // 每 5 分钟
		"30 2 1 * *",      // 每月 1 号 2:30
		"0 0 * * 0",       // 每周日 0 点
		"0 0 1-7 * *",     // 每月 1-7 号 0 点
	}
	for _, expr := range cases {
		if _, err := parseCron(expr); err != nil {
			t.Fatalf("parseCron(%q): %v", expr, err)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	if _, err := parseCron("0 0 *"); err == nil {
		t.Fatal("3 fields must error")
	}
	if _, err := parseCron("0 0 * * 7"); err == nil {
		t.Fatal("dow 7 (max 6) must error")
	}
	if _, err := parseCron("0 0 * * * *"); err == nil {
		t.Fatal("6 fields must error")
	}
}

func TestCronMatches(t *testing.T) {
	c, err := parseCron("0 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if !c.Matches(midnight) {
		t.Fatal("midnight must match daily@0:00")
	}
	if c.Matches(midnight.Add(1 * time.Hour)) {
		t.Fatal("01:00 must not match daily@0:00")
	}
}

func TestNextFireAfterRespectsCron(t *testing.T) {
	p := Policy{Cron: "0 0 * * *"}
	now := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	next, err := p.NextFireAfter(now)
	if err != nil {
		t.Fatal(err)
	}
	// 下一次 0:00 = 次日
	if next.Hour() != 0 || next.Minute() != 0 {
		t.Fatalf("next = %v, want 00:00", next)
	}
	if !next.After(now) {
		t.Fatalf("next must be after now")
	}
}

func TestNextFireAfterAlreadyAtFireTime(t *testing.T) {
	p := Policy{Cron: "0 12 * * *"}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	next, err := p.NextFireAfter(now)
	if err != nil {
		t.Fatal(err)
	}
	// 应推进到次日 12:00（同一时刻已被扫过）
	if next.Day() != 25 {
		t.Fatalf("next day = %d, want 25", next.Day())
	}
}

func TestKindForTime(t *testing.T) {
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC), "monthly"},
		{time.Date(2026, 9, 6, 0, 30, 0, 0, time.UTC), "weekly"}, // Sunday 2026-09-06
		{time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC), "daily"},
	}
	for _, c := range cases {
		if got := KindForTime(c.t); got != c.want {
			t.Fatalf("KindForTime(%v, weekday=%v) = %q, want %q", c.t, c.t.Weekday(), got, c.want)
		}
	}
}

func TestGFSRetain(t *testing.T) {
	p := Policy{RetainDaily: 7, RetainWeekly: 4, RetainMonthly: 12}
	if p.GFSRetain("daily") != 7 || p.GFSRetain("weekly") != 4 || p.GFSRetain("monthly") != 12 {
		t.Fatal("GFSRetain mapping wrong")
	}
}

func TestSweepNowGeneratesJobs(t *testing.T) {
	s := NewScheduler(NewNoopDriver())
	s.UpsertPolicy(Policy{ID: "p1", Scope: ScopeInstance, ScopeTarget: "ct-7", Cron: "0 0 * * *", Enabled: true, RetainDaily: 7})
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	jobs := s.SweepNow(now)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Status != JobSuccess {
		t.Fatalf("status = %v, want success", jobs[0].Status)
	}
	if jobs[0].SnapshotID == "" {
		t.Fatal("snapshot_id must be set on success")
	}
	if !jobs[0].KeepUntil.After(now) {
		t.Fatal("keep_until must be in the future")
	}
}

func TestSweepNowSkipsDisabledPolicies(t *testing.T) {
	s := NewScheduler(NewNoopDriver())
	s.UpsertPolicy(Policy{ID: "p1", Scope: ScopeInstance, ScopeTarget: "ct-7", Cron: "0 0 * * *", Enabled: false})
	if jobs := s.SweepNow(time.Now()); len(jobs) != 0 {
		t.Fatalf("disabled policy must not generate jobs, got %d", len(jobs))
	}
}

func TestSweepNowMarksFailedOnDriverError(t *testing.T) {
	d := NewNoopDriver()
	d.SnapshotErr = errors.New("disk full")
	s := NewScheduler(d)
	s.UpsertPolicy(Policy{ID: "p1", Scope: ScopeInstance, ScopeTarget: "ct-7", Cron: "0 0 * * *", Enabled: true})
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	jobs := s.SweepNow(now)
	if len(jobs) != 1 || jobs[0].Status != JobFailed {
		t.Fatalf("expected 1 failed job, got %d (%v)", len(jobs), jobs)
	}
	if !strings.Contains(jobs[0].Error, "disk full") {
		t.Fatalf("error = %q, want disk full", jobs[0].Error)
	}
}

func TestPruneExpiredRemovesOverRetain(t *testing.T) {
	d := NewNoopDriver()
	s := NewScheduler(d)
	s.UpsertPolicy(Policy{ID: "p1", Scope: ScopeInstance, ScopeTarget: "ct-7", Cron: "0 0 * * *", Enabled: true, RetainDaily: 2})
	// 注入 5 个 daily 备份
	for i := 0; i < 5; i++ {
		now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Add(time.Duration(-i) * 24 * time.Hour)
		jobs := s.SweepNow(now)
		if len(jobs) != 1 || jobs[0].Status != JobSuccess {
			t.Fatalf("iteration %d: jobs=%+v", i, jobs)
		}
	}
	if got := len(d.ListBackups(ScopeInstance, "ct-7")); got != 5 {
		t.Fatalf("snapshots after 5 sweeps = %d, want 5", got)
	}
	p := s.Policies()[0]
	deleted := s.PruneExpired(ScopeInstance, "ct-7", p, time.Now())
	if len(deleted) != 3 {
		t.Fatalf("expected 3 deleted, got %d", len(deleted))
	}
	if got := len(d.ListBackups(ScopeInstance, "ct-7")); got != 2 {
		t.Fatalf("snapshots after prune = %d, want 2", got)
	}
}

func TestComputeKeepUntil(t *testing.T) {
	p := Policy{RetainDaily: 7, RetainWeekly: 4, RetainMonthly: 12}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	daily := computeKeepUntil(now, "daily", p)
	if daily.Sub(now) != 7*24*time.Hour {
		t.Fatalf("daily keep = %v, want +7 days", daily.Sub(now))
	}
	weekly := computeKeepUntil(now, "weekly", p)
	if weekly.Sub(now) != 28*24*time.Hour {
		t.Fatalf("weekly keep = %v, want +28 days", weekly.Sub(now))
	}
	monthly := computeKeepUntil(now, "monthly", p)
	expected := now.AddDate(0, 12, 0)
	if !monthly.Equal(expected) {
		t.Fatalf("monthly keep = %v, want %v", monthly, expected)
	}
}