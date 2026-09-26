package abuseengine

import (
	"strings"
	"testing"
)

// TestPlanLookup 验证默认策略表覆盖所有 AbuseKind。
func TestPlanLookup(t *testing.T) {
	for _, kind := range []AbuseKind{AbuseDDoS, AbuseSpam, AbuseAbuse, AbuseSevere} {
		plan, err := LookupPlan(kind)
		if err != nil {
			t.Fatalf("LookupPlan(%s): %v", kind, err)
		}
		if len(plan.Actions) == 0 {
			t.Fatalf("plan %s has no actions", kind)
		}
	}
	if _, err := LookupPlan("bogus"); err == nil {
		t.Fatal("unknown kind must error")
	}
}

// TestActionIdempotency 验证同一 kind+target+param 重复追加只生效一次。
func TestActionIdempotency(t *testing.T) {
	a := NewAlertActionsTaken("a1")
	r := ActionRecord{Kind: ActionBlockIP, Target: "1.2.3.4", Param: "30"}
	if !a.Append(r) {
		t.Fatal("first Append must return true")
	}
	if a.Append(r) {
		t.Fatal("duplicate Append must return false")
	}
	if a.Len() != 1 {
		t.Fatalf("expected 1 record, got %d", a.Len())
	}
}

// TestApplyAlertRunsAllActions 验证 ApplyAlert 顺序执行所有策略动作。
func TestApplyAlertRunsAllActions(t *testing.T) {
	exec := &NoopExecutor{}
	e := NewEngine(exec)
	a := NewAlertActionsTaken("a-severe")
	plan, err := e.ApplyAlert(AbuseSevere, "5.6.7.8", a)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbuseSevere || len(plan.Actions) != 3 {
		t.Fatalf("plan = %+v, want 3 actions for severe", plan)
	}
	if len(exec.Executed()) != 3 {
		t.Fatalf("executor called %d times, want 3", len(exec.Executed()))
	}
	// Target 必须注入到所有动作
	for _, rec := range a.Records() {
		if rec.Target != "5.6.7.8" {
			t.Fatalf("target = %q, want 5.6.7.8", rec.Target)
		}
	}
}

// TestApplyAlertIdempotentSecondCall 验证二次 ApplyAlert 同告警不重复执行。
func TestApplyAlertIdempotentSecondCall(t *testing.T) {
	exec := &NoopExecutor{}
	e := NewEngine(exec)
	a := NewAlertActionsTaken("a-ddos")
	_, _ = e.ApplyAlert(AbuseDDoS, "9.9.9.9", a)
	_, _ = e.ApplyAlert(AbuseDDoS, "9.9.9.9", a)
	if len(exec.Executed()) != 1 {
		t.Fatalf("second call must be deduped, got %d", len(exec.Executed()))
	}
}

func TestApplyAlertUnknownKind(t *testing.T) {
	e := NewEngine(&NoopExecutor{})
	a := NewAlertActionsTaken("a")
	if _, err := e.ApplyAlert("bogus", "x", a); err == nil {
		t.Fatal("unknown kind must error")
	}
}

// TestRevokeAllLIFO 验证撤销按 LIFO 顺序且跳过不可逆动作。
func TestRevokeAllLIFO(t *testing.T) {
	exec := &NoopExecutor{}
	e := NewEngine(exec)
	a := NewAlertActionsTaken("a-rev")
	_, _ = e.ApplyAlert(AbuseSevere, "x", a)
	// 顺序应为 suspend → block_ip → notify
	// 可逆：suspend / throttle；不可逆：block_ip(自动到期) / notify(无副作用)
	rev := &NoopRevoker{}
	if err := RevokeAll(a, rev); err != nil {
		t.Fatal(err)
	}
	if len(rev.Revoked()) != 1 {
		t.Fatalf("only suspend is reversible in severe plan, got %d revokes", len(rev.Revoked()))
	}
	recs := rev.Revoked()
	if recs[0].Kind != ActionSuspend {
		t.Fatalf("revoked[0] = %v, want suspend (LIFO first)", recs[0].Kind)
	}
}

// TestRevokeAllNilSafety 验证 RevokeAll 接受 nil 时的安全行为。
func TestRevokeAllNilSafety(t *testing.T) {
	if err := RevokeAll(nil, &NoopRevoker{}); err == nil {
		t.Fatal("nil actions must error")
	}
}

func TestFormatReportIncludesAllFields(t *testing.T) {
	exec := &NoopExecutor{}
	e := NewEngine(exec)
	a := NewAlertActionsTaken("rep")
	_, _ = e.ApplyAlert(AbuseSpam, "5.5.5.5", a)
	report := FormatReport("rep", AbuseSpam, a)
	for _, want := range []string{"alert_id=rep", "kind=spam", "throttle", "5.5.5.5"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q: %s", want, report)
		}
	}
}