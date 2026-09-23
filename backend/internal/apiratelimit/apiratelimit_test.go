package apiratelimit

import (
	"testing"
	"time"
)

func TestAllowReturnsFalseWhenOverLimit(t *testing.T) {
	l := New()
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4", 3) {
			t.Fatalf("request %d must be allowed", i+1)
		}
	}
	if l.Allow("1.2.3.4", 3) {
		t.Fatal("4th request must be denied")
	}
}

func TestAllowResetsAfterWindow(t *testing.T) {
	l := New()
	fixed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	l.Now = func() time.Time { return fixed }
	if !l.Allow("ip", 1) {
		t.Fatal("first request must be allowed")
	}
	if l.Allow("ip", 1) {
		t.Fatal("second request in same window must be denied")
	}
	fixed = fixed.Add(61 * time.Second)
	if !l.Allow("ip", 1) {
		t.Fatal("after window expiry request must be allowed")
	}
}

func TestAllowKeyIsolatesBuckets(t *testing.T) {
	l := New()
	if !l.AllowKey("k1", 1) {
		t.Fatal("k1 first request must pass")
	}
	if l.AllowKey("k1", 1) {
		t.Fatal("k1 second request must fail")
	}
	if !l.AllowKey("k2", 1) {
		t.Fatal("k2 first request must pass independently")
	}
}

func TestAllowZeroIsUnlimited(t *testing.T) {
	l := New()
	for i := 0; i < 100; i++ {
		if !l.Allow("ip", 0) {
			t.Fatalf("zero perMinute must allow all (failed at %d)", i)
		}
	}
}

func TestAllowAndKeyAreIndependent(t *testing.T) {
	l := New()
	for i := 0; i < 5; i++ {
		if !l.Allow("ip", 5) {
			t.Fatal("ip request must pass")
		}
	}
	if l.Allow("ip", 5) {
		t.Fatal("ip exhausted")
	}
	// IP 已满，但 key 维度独立
	if !l.AllowKey("key1", 1) {
		t.Fatal("key dimension must be independent of ip dimension")
	}
}

func TestRetryAfterReportsWindowRemaining(t *testing.T) {
	l := New()
	fixed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	l.Now = func() time.Time { return fixed }
	l.Allow("ip", 1)
	fixed = fixed.Add(20 * time.Second)
	wait := l.RetryAfter("ip:ip")
	if wait <= 0 || wait > 41*time.Second {
		t.Fatalf("retry after = %v, want ~40s", wait)
	}
}