package reputation

import (
	"errors"
	"testing"
	"time"
)

type fakeProvider struct {
	name   string
	ipScore Score
	ipErr  error
	domScore Score
	domErr error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) LookupIP(string) (Score, error)       { return f.ipScore, f.ipErr }
func (f *fakeProvider) LookupDomain(string) (Score, error)    { return f.domScore, f.domErr }

func TestResultRiskLevels(t *testing.T) {
	r := Result{Score: 85}
	if !r.IsHighRisk() {
		t.Fatal("85 must be high risk")
	}
	r = Result{Score: 60}
	if r.IsHighRisk() || !r.IsMediumRisk() {
		t.Fatal("60 must be medium risk")
	}
	r = Result{Score: 20}
	if !r.IsLowRisk() {
		t.Fatal("20 must be low risk")
	}
}

func TestResultFreshAndStale(t *testing.T) {
	now := time.Now()
	fresh := Result{ExpiresAt: now.Add(time.Hour)}
	stale := Result{ExpiresAt: now.Add(-time.Hour)}
	if !fresh.IsFresh(now) {
		t.Fatal("future expiry must be fresh")
	}
	if !stale.IsStale(now) {
		t.Fatal("past expiry must be stale")
	}
}

func TestLookupIPMultiSourcePicksMax(t *testing.T) {
	providers := []Provider{
		&fakeProvider{name: "a", ipScore: 30},
		&fakeProvider{name: "b", ipScore: 85},
		&fakeProvider{name: "c", ipScore: 60},
	}
	cache := NewCache()
	r := NewRegistry(providers, NewRateLimiter(0), cache)
	results := r.LookupIP("1.2.3.4")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	max, ok := MaxScore(results)
	if !ok || max != 85 {
		t.Fatalf("max score = %d, ok=%v, want 85/true", max, ok)
	}
}

func TestLookupIPFailureFallsBackToStaleCache(t *testing.T) {
	providers := []Provider{
		&fakeProvider{name: "a", ipScore: 50, ipErr: errors.New("network down")},
	}
	cache := NewCache()
	// 预填旧缓存（已过期）
	cache.Set("a|ip|1.2.3.4", Result{Target: "1.2.3.4", Source: "a", Score: 40, ExpiresAt: time.Now().Add(-time.Hour)})
	r := NewRegistry(providers, NewRateLimiter(0), cache)
	results := r.LookupIP("1.2.3.4")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != "stale_cache" {
		t.Fatalf("failure with expired cache must return stale, got error=%q", results[0].Error)
	}
	if results[0].Score != 40 {
		t.Fatalf("stale cache score = %d, want 40", results[0].Score)
	}
}

func TestLookupIPFailureNoCacheReturnsError(t *testing.T) {
	providers := []Provider{
		&fakeProvider{name: "a", ipScore: 0, ipErr: errors.New("timeout")},
	}
	cache := NewCache()
	r := NewRegistry(providers, NewRateLimiter(0), cache)
	results := r.LookupIP("1.2.3.4")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != "timeout" {
		t.Fatalf("error = %q, want timeout", results[0].Error)
	}
	if results[0].Score != 0 {
		t.Fatalf("score = %d, want 0 (no cache, no success)", results[0].Score)
	}
}

func TestLookupIPUsesFreshCache(t *testing.T) {
	calls := 0
	counting := &countingProvider{name: "a", onLookup: func() { calls++ }, score: 70}
	cache := NewCache()
	r := NewRegistry([]Provider{counting}, NewRateLimiter(0), cache)
	r.LookupIP("1.2.3.4")
	r.LookupIP("1.2.3.4")
	if calls != 1 {
		t.Fatalf("second lookup must hit cache, provider called %d times", calls)
	}
}

func TestRateLimiterBlocksExcess(t *testing.T) {
	rl := NewRateLimiter(2)
	now := time.Now()
	if !rl.Allow("a", now) {
		t.Fatal("1st call must allow")
	}
	if !rl.Allow("a", now) {
		t.Fatal("2nd call must allow")
	}
	if rl.Allow("a", now) {
		t.Fatal("3rd call must be blocked")
	}
	// 时间推进到下一分钟
	if !rl.Allow("a", now.Add(time.Minute+time.Second)) {
		t.Fatal("next minute must reset")
	}
}

func TestRateLimiterZeroRateAllowsAll(t *testing.T) {
	rl := NewRateLimiter(0)
	for i := 0; i < 100; i++ {
		if !rl.Allow("a", time.Now()) {
			t.Fatal("rate=0 must allow all")
		}
	}
}

func TestRateLimiterProviderIsolation(t *testing.T) {
	rl := NewRateLimiter(1)
	now := time.Now()
	if !rl.Allow("a", now) {
		t.Fatal("a must allow")
	}
	if !rl.Allow("b", now) {
		t.Fatal("b must allow (independent budget)")
	}
	if rl.Allow("a", now) {
		t.Fatal("a second call same minute must block")
	}
}

type countingProvider struct {
	name string
	onLookup func()
	score Score
}

func (c *countingProvider) Name() string { return c.name }
func (c *countingProvider) LookupIP(string) (Score, error) {
	c.onLookup()
	return c.score, nil
}
func (c *countingProvider) LookupDomain(string) (Score, error) {
	return c.score, nil
}