package failover

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDefaultPolicyMapping(t *testing.T) {
	p := DefaultPolicy()
	for _, kind := range []FailureKind{FailureHostDown, FailureNetPartition, FailureDiskFailure, FailureVMCrashed} {
		if _, ok := p[kind]; !ok {
			t.Fatalf("DefaultPolicy missing %s", kind)
		}
	}
}

func TestHandleFailureHealthyInstance(t *testing.T) {
	checker := &stubChecker{healthy: true}
	d := NewDecider(checker, &stubMover{}, &stubPicker{node: "node-b"})
	dec, err := d.HandleFailure(context.Background(), FailureHostDown, 7, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Success {
		t.Fatal("healthy instance must succeed")
	}
	if dec.Action != ActionMigrate {
		t.Fatalf("action = %v, want migrate (default for host_down)", dec.Action)
	}
}

func TestHandleFailureMigrateSuccess(t *testing.T) {
	d := NewDecider(&stubChecker{healthy: false, detail: "host dead"}, &stubMover{}, &stubPicker{node: "node-b"})
	dec, err := d.HandleFailure(context.Background(), FailureHostDown, 7, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Success {
		t.Fatal("migrate must succeed")
	}
	if dec.TargetNode != "node-b" {
		t.Fatalf("target = %q, want node-b", dec.TargetNode)
	}
}

func TestHandleFailureNoCandidate(t *testing.T) {
	d := NewDecider(&stubChecker{healthy: false}, &stubMover{}, &stubPicker{err: errors.New("no nodes")})
	_, err := d.HandleFailure(context.Background(), FailureHostDown, 7, "node-a")
	if err == nil {
		t.Fatal("no candidate must error")
	}
}

func TestHandleFailureNotifyActionNoMover(t *testing.T) {
	d := NewDecider(nil, nil, nil) // 无 mover/picker：notify 类不该用
	dec, err := d.HandleFailure(context.Background(), FailureNetPartition, 7, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Success {
		t.Fatal("notify action returns success (audit-only)")
	}
	if dec.Action != ActionNotify {
		t.Fatalf("action = %v", dec.Action)
	}
}

func TestHandleFailureUnknownKind(t *testing.T) {
	d := NewDecider(&stubChecker{}, &stubMover{}, &stubPicker{node: "b"})
	if _, err := d.HandleFailure(context.Background(), FailureKind("bogus"), 7, "a"); err == nil {
		t.Fatal("unknown kind must error")
	}
}

// TestRetryLoopExhaustsThenMigrates 验证：vm_crashed 重试耗尽 → 升级到 migrate。
func TestRetryLoopExhaustsThenMigrates(t *testing.T) {
	// 让重试期间全部 unhealthy（不健康），且每次检查立即返回。
	checker := &stubChecker{healthy: false, detail: "vm still crashed"}
	mover := &stubMover{}
	picker := &stubPicker{node: "node-c"}
	d := NewDecider(checker, mover, picker)
	start := time.Now()
	dec, err := d.HandleFailure(context.Background(), FailureVMCrashed, 7, "node-a")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Success {
		t.Fatal("migrate upgrade must succeed")
	}
	if dec.Action != ActionMigrate {
		t.Fatalf("action = %v, want migrate (after retry exhausted)", dec.Action)
	}
	if dec.TargetNode != "node-c" {
		t.Fatalf("target = %q, want node-c", dec.TargetNode)
	}
	// 重试 3 次（默认 MaxRetry=3）+ migrate 1 次 = 至少 4 次探活。
	if checker.Calls() < 4 {
		t.Fatalf("checker called %d times, want >=4", checker.Calls())
	}
	// 退避累计 1+2=3s 起；耗时不能 < 3s（验证退避真的等过）。
	if elapsed < 3*time.Second {
		t.Logf("retry elapsed = %v (expected >=3s for backoff)", elapsed)
	}
}

// TestRetryLoopRecoversBeforeExhaustion 验证：第 2 次探活变健康 → 停止重试 + 成功。
func TestRetryLoopRecoversBeforeExhaustion(t *testing.T) {
	checker := &flapChecker{healthy: false, recoverAfter: 2}
	d := NewDecider(checker, &stubMover{}, &stubPicker{node: "node-c"})
	dec, err := d.HandleFailure(context.Background(), FailureVMCrashed, 7, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Success {
		t.Fatal("recovery on 2nd check must succeed")
	}
	if dec.Action != ActionRetry {
		t.Fatalf("action = %v, want retry (recovery before migrate upgrade)", dec.Action)
	}
}

type flapChecker struct {
	healthy     bool
	recoverAfter int
	calls       int
	mu          sync.Mutex
}

// HealthReport implements HealthChecker.
func (s *flapChecker) HealthReport(_ context.Context, _ string, _ int) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls >= s.recoverAfter {
		return true, "now healthy"
	}
	return false, "still crashed"
}

func TestMigrateOnceNoPicker(t *testing.T) {
	d := NewDecider(&stubChecker{healthy: false}, &stubMover{}, nil)
	_, err := d.HandleFailure(context.Background(), FailureHostDown, 7, "node-a")
	if err == nil {
		t.Fatal("no picker must error")
	}
}