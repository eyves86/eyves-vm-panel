// Package apiratelimit 提供 P7-4 公开 API 限流原语：
//
//   - 双层限流：按客户端 IP 走全局配额 + 按 API Key 走单 key 配额；
//   - 滑动窗口：1 分钟内请求计数，超过返回 false；
//   - 内存实现：单进程限速；如需分布式可换 Redis/Memcache 后端，接口稳定。
//
// 包设计为独立可测；调用方通过 Allow() / AllowKey() 检查配额并返回 429。
package apiratelimit

import (
	"sync"
	"time"
)

// windowSeconds 是固定窗口长度。
const windowSeconds = 60

// Limiter 滑动窗口限流器；线程安全。
type Limiter struct {
	mu   sync.Mutex
	win  map[string][]time.Time
	// Now 用于测试注入虚拟时钟。
	Now func() time.Time
}

// New 构造限流器。
func New() *Limiter {
	return &Limiter{
		win: map[string][]time.Time{},
		Now: time.Now,
	}
}

// Allow 检查全局（按 IP）配额。perMinute<=0 时不限制。
func (l *Limiter) Allow(clientKey string, perMinute int) bool {
	return l.allow("ip:"+clientKey, perMinute)
}

// AllowKey 检查 API Key 配额。perMinute<=0 时不限制。
func (l *Limiter) AllowKey(keyID string, perMinute int) bool {
	return l.allow("key:"+keyID, perMinute)
}

// RetryAfter 返回在限流生效时距离下次放行的等待时长。
func (l *Limiter) RetryAfter(bucketKey string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := l.win[bucketKey]
	if len(ts) == 0 {
		return 0
	}
	now := l.Now()
	cutoff := now.Add(-windowSeconds * time.Second)
	oldest := ts[0]
	if oldest.After(cutoff) {
		return oldest.Add(windowSeconds * time.Second).Sub(now)
	}
	return 0
}

func (l *Limiter) allow(bucketKey string, perMinute int) bool {
	if perMinute <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	cutoff := now.Add(-windowSeconds * time.Second)
	kept := l.win[bucketKey][:0]
	for _, t := range l.win[bucketKey] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.win[bucketKey] = kept
	if len(kept) >= perMinute {
		return false
	}
	l.win[bucketKey] = append(l.win[bucketKey], now)
	if len(l.win) > 5000 {
		l.gcLocked(cutoff)
	}
	return true
}

func (l *Limiter) gcLocked(cutoff time.Time) {
	for k, times := range l.win {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(l.win, k)
		}
	}
}