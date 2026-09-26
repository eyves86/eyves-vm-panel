package instantclone

import (
	"errors"
	"strings"
	"testing"
)

func TestStartCloneHappyPath(t *testing.T) {
	e := NewEngine(&NoopBackend{}, UpstreamQuota{MaxConcurrentClones: 5})
	job, err := e.StartClone("vol-src", "full", "vol-dst-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusReady {
		t.Fatalf("status = %v, want ready", job.Status)
	}
	if job.NewID == "" {
		t.Fatal("new_id must be set on success")
	}
	if job.StartedAt.IsZero() || job.FinishedAt.IsZero() {
		t.Fatal("timestamps must be set")
	}
}

func TestStartCloneRequiresFields(t *testing.T) {
	e := NewEngine(&NoopBackend{}, UpstreamQuota{})
	if _, err := e.StartClone("", "full", "x"); err == nil {
		t.Fatal("empty upstream must error")
	}
	if _, err := e.StartClone("src", "full", ""); err == nil {
		t.Fatal("empty new_id must error")
	}
}

func TestStartCloneQuotaEnforced(t *testing.T) {
	// 模拟第一次占用：手工填 inflight 计数（绕过实际 backend 调用）。
	e := NewEngine(&NoopBackend{}, UpstreamQuota{MaxConcurrentClones: 1})
	e.mu.Lock()
	e.perUpstreamInflight["src"] = 1
	e.mu.Unlock()
	if _, err := e.StartClone("src", "full", "vol-2"); err == nil {
		t.Fatal("quota must block 2nd concurrent clone")
	}
}

func TestStartCloneFailureTriggersCleanup(t *testing.T) {
	n := &NoopBackend{CloneUpstreamErr: errors.New("disk full")}
	e := NewEngine(n, UpstreamQuota{MaxConcurrentClones: 5})
	job, err := e.StartClone("src", "full", "vol-fail")
	if err == nil {
		t.Fatal("error must surface")
	}
	if job.Status != StatusCleaned {
		t.Fatalf("status = %v, want cleaned (after rollback)", job.Status)
	}
	if !strings.Contains(job.Error, "disk full") {
		t.Fatalf("error = %q", job.Error)
	}
}

func TestStartCloneInflightReleasedOnFailure(t *testing.T) {
	n := &NoopBackend{CloneUpstreamErr: errors.New("boom")}
	e := NewEngine(n, UpstreamQuota{MaxConcurrentClones: 1})
	_, _ = e.StartClone("src", "full", "vol-1")
	if e.perUpstreamInflight["src"] != 0 {
		t.Fatalf("inflight = %d, want 0 after failure", e.perUpstreamInflight["src"])
	}
	// 配额应释放，第二次能进入 cloning 阶段（即使最终失败）
	if _, err := e.StartClone("src", "full", "vol-2"); err == nil {
		t.Fatal("2nd call must error from backend, not quota")
	} else if strings.Contains(err.Error(), "quota") {
		t.Fatalf("quota must be released: %v", err)
	}
}

func TestValidTransitions(t *testing.T) {
	legal := []struct{ from, to Status }{
		{StatusQueued, StatusCloning},
		{StatusQueued, StatusFailed},
		{StatusCloning, StatusReady},
		{StatusCloning, StatusFailed},
		{StatusFailed, StatusCleaningUp},
		{StatusCleaningUp, StatusCleaned},
	}
	for _, c := range legal {
		if !ValidTransition(c.from, c.to) {
			t.Fatalf("expected legal: %v -> %v", c.from, c.to)
		}
	}
	if ValidTransition(StatusReady, StatusFailed) {
		t.Fatal("ready is terminal")
	}
	if ValidTransition(StatusCleaned, StatusFailed) {
		t.Fatal("cleaned is terminal")
	}
}

func TestIsTerminal(t *testing.T) {
	for _, s := range []Status{StatusReady, StatusFailed, StatusCleaned} {
		if !s.IsTerminal() {
			t.Fatalf("%v must be terminal", s)
		}
	}
	for _, s := range []Status{StatusQueued, StatusCloning, StatusCleaningUp} {
		if s.IsTerminal() {
			t.Fatalf("%v must not be terminal", s)
		}
	}
}

func TestNilBackendErrors(t *testing.T) {
	e := NewEngine(nil, UpstreamQuota{})
	if _, err := e.StartClone("src", "full", "dst"); err == nil {
		t.Fatal("nil backend must error")
	}
}

func TestJobsAndGet(t *testing.T) {
	e := NewEngine(&NoopBackend{}, UpstreamQuota{MaxConcurrentClones: 5})
	e.StartClone("src", "full", "vol-1")
	e.StartClone("src", "full", "vol-2")
	if got := len(e.Jobs()); got != 2 {
		t.Fatalf("jobs = %d, want 2", got)
	}
	if _, ok := e.Get("vol-1"); !ok {
		t.Fatal("Get vol-1 must succeed")
	}
	if _, ok := e.Get("missing"); ok {
		t.Fatal("Get missing must fail")
	}
}

type slowBackend struct {
	blocking chan struct{}
}

func (s *slowBackend) CloneUpstream(_, _ string) (string, error) {
	<-s.blocking
	return "vol-slow", nil
}

func (s *slowBackend) DeleteNewUpstream(_ string) error { return nil }

var _ = slowBackend{}