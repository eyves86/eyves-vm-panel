package api

import (
	"net/http"
	"sync"
	"time"
)

// loginRateLimiter throttles failed login attempts per identity
// (client IP + username / access code) to mitigate brute-force attacks.
// A sliding window keeps only failures within the window; successful
// logins reset the counter for that identity.
type loginRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	maxFail int
	maxKeys int
	fails   map[string][]time.Time
}

var loginLimiter = &loginRateLimiter{
	window:  10 * time.Minute,
	maxFail: 5,
	maxKeys: 10000,
	fails:   map[string][]time.Time{},
}

// allow reports whether a login attempt for key may proceed.
func (l *loginRateLimiter) allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.fails[key] = kept
	return len(kept) < l.maxFail
}

// recordFail registers a failed login attempt for key.
func (l *loginRateLimiter) recordFail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.fails[key], time.Now())
	l.enforceCapLocked()
}

// reset clears all failed attempts for key after a successful login.
func (l *loginRateLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// enforceCapLocked bounds the number of tracked identities to prevent an
// unauthenticated attacker from growing the map without limit (memory DoS).
// Called with l.mu held. When over budget it prunes entries whose failures
// have expired; if still over budget it drops the oldest entries wholesale.
func (l *loginRateLimiter) enforceCapLocked() {
	if l.maxKeys <= 0 || len(l.fails) <= l.maxKeys {
		return
	}
	now := time.Now()
	cutoff := now.Add(-l.window)
	// 1) drop empty/expired entries to reclaim space
	for key, hits := range l.fails {
		kept := hits[:0]
		for _, t := range hits {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.fails, key)
		} else if len(kept) < len(hits) {
			l.fails[key] = kept
		}
	}
	// 2) if still over budget, evict from the map to protect memory
	if len(l.fails) <= l.maxKeys {
		return
	}
	overflow := len(l.fails) - l.maxKeys
	deleted := 0
	for key := range l.fails {
		if deleted >= overflow {
			break
		}
		delete(l.fails, key)
		deleted++
	}
}

// loginRateLimited is a tiny wrapper that applies the limiter and writes a
// 429 response when the identity is currently throttled.
func loginRateLimited(w http.ResponseWriter, key string) bool {
	if !loginLimiter.allow(key) {
		jsonResponse(w, http.StatusTooManyRequests, APIResponse{Success: false, Message: "登录尝试过于频繁，请稍后再试"})
		return true
	}
	return false
}
