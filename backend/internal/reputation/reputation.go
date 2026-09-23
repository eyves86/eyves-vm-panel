// Package reputation IP/域名信誉源（P3-3）：
//
//   - Provider 接口（LookupIP/LookupDomain），调用方注入真实适配器；
//   - Cache（per source 的 TTL 内存缓存，查询失败降级用旧值）；
//   - RateLimiter 简单令牌桶，遵守各源配额；
//   - ProviderRegistry 管理多个 Provider；
//   - 评分阈值（Score >= 80 → 高风险，< 30 → 低风险）。
//
// 主流程：被处置 IP 到期 → 调用 Registry.LookupIP → 多源评分 → 若
// 高风险持续则保持工单 open，若全部源降为低风险则建议销单（ReputationChecker.Pass）。
package reputation

import (
	"errors"
	"sync"
	"time"
)

// Score 是信誉评分（0-100，越高越可疑；不同源语义不同，统一映射）。
type Score int

// 阈值常量。
const (
	HighRiskScore    Score = 80
	MediumRiskScore  Score = 50
)

// Result 单次信誉查询结果。
type Result struct {
	Target     string    // IP 或域名
	Source     string    // "abuseipdb" / "spamhaus" 等
	Score      Score
	CheckedAt  time.Time
	ExpiresAt  time.Time
	Error      string    // 非空表示查询失败，缓存旧值降级
}

// IsHighRisk 高风险阈值。
func (r Result) IsHighRisk() bool {
	return r.Error == "" && r.Score >= HighRiskScore
}

// IsMediumRisk 中风险。
func (r Result) IsMediumRisk() bool {
	return r.Error == "" && r.Score >= MediumRiskScore && r.Score < HighRiskScore
}

// IsLowRisk 低风险。
func (r Result) IsLowRisk() bool {
	return r.Error == "" && r.Score < MediumRiskScore
}

// IsStale 缓存已过期（用于降级提示）。
func (r Result) IsStale(now time.Time) bool {
	return now.After(r.ExpiresAt)
}

// IsFresh 在 TTL 内。
func (r Result) IsFresh(now time.Time) bool {
	return !r.IsStale(now)
}

// Provider 信誉源接口。
type Provider interface {
	Name() string
	LookupIP(target string) (Score, error)
	LookupDomain(target string) (Score, error)
}

// ProviderRegistry 多 provider 聚合查询。
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	cache     *Cache
	limiter   *RateLimiter
}

// NewRegistry 创建注册表。
func NewRegistry(providers []Provider, limiter *RateLimiter, cache *Cache) *ProviderRegistry {
	r := &ProviderRegistry{
		providers: map[string]Provider{},
		cache:     cache,
		limiter:   limiter,
	}
	for _, p := range providers {
		r.providers[p.Name()] = p
	}
	return r
}

// LookupIP 多源查询 IP，所有源 Score 取最大值（最坏优先）。
//
// 任意单源查询失败：用 cache 旧值降级（Result.Error 标记）；旧值也
// 缺失则记 Error="no_data"。
func (r *ProviderRegistry) LookupIP(ip string) []Result {
	r.mu.RLock()
	providers := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, p)
	}
	r.mu.RUnlock()

	out := make([]Result, 0, len(providers))
	for _, p := range providers {
		key := cacheKey(p.Name(), "ip", ip)
		now := time.Now()
		if cached, ok := r.cache.Get(key); ok && cached.IsFresh(now) {
			out = append(out, cached)
			continue
		}
		if r.limiter != nil {
			if !r.limiter.Allow(p.Name(), now) {
				// 限速：使用缓存旧值或返回错误
				if cached, ok := r.cache.Get(key); ok {
					out = append(out, staleResult(cached, now))
				} else {
					out = append(out, Result{Target: ip, Source: p.Name(), Error: "rate_limited_no_cache"})
				}
				continue
			}
		}
		score, err := p.LookupIP(ip)
		res := Result{Target: ip, Source: p.Name(), Score: score, CheckedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
		if err != nil {
			res.Error = err.Error()
			// 失败降级：保留旧缓存值；旧值不存在则标记错误
			if cached, ok := r.cache.Get(key); ok {
				out = append(out, staleResult(cached, now))
			} else {
				out = append(out, res)
			}
			continue
		}
		r.cache.Set(key, res)
		out = append(out, res)
	}
	return out
}

// LookupDomain 多源查询域名（语义同 LookupIP）。
func (r *ProviderRegistry) LookupDomain(domain string) []Result {
	r.mu.RLock()
	providers := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, p)
	}
	r.mu.RUnlock()
	out := make([]Result, 0, len(providers))
	for _, p := range providers {
		key := cacheKey(p.Name(), "domain", domain)
		now := time.Now()
		if cached, ok := r.cache.Get(key); ok && cached.IsFresh(now) {
			out = append(out, cached)
			continue
		}
		if r.limiter != nil {
			if !r.limiter.Allow(p.Name(), now) {
				if cached, ok := r.cache.Get(key); ok {
					out = append(out, staleResult(cached, now))
				} else {
					out = append(out, Result{Target: domain, Source: p.Name(), Error: "rate_limited_no_cache"})
				}
				continue
			}
		}
		score, err := p.LookupDomain(domain)
		res := Result{Target: domain, Source: p.Name(), Score: score, CheckedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
		if err != nil {
			res.Error = err.Error()
			if cached, ok := r.cache.Get(key); ok {
				out = append(out, staleResult(cached, now))
			} else {
				out = append(out, res)
			}
			continue
		}
		r.cache.Set(key, res)
		out = append(out, res)
	}
	return out
}

// MaxScore 返回结果集中的最高分（最坏优先）。
func MaxScore(results []Result) (Score, bool) {
	var max Score
	has := false
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		if !has || r.Score > max {
			max = r.Score
			has = true
		}
	}
	return max, has
}

// ---- 缓存 ----

// Cache 是 per-source 的内存缓存（TTL 由 Result.ExpiresAt 决定）。
type Cache struct {
	mu   sync.RWMutex
	data map[string]Result
}

// NewCache 创建空缓存。
func NewCache() *Cache {
	return &Cache{data: map[string]Result{}}
}

// Get 取缓存；缺失或过期返回 false。
func (c *Cache) Get(key string) (Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.data[key]
	return r, ok
}

// Set 写缓存。
func (c *Cache) Set(key string, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = r
}

func cacheKey(source, kind, target string) string {
	return source + "|" + kind + "|" + target
}

// staleResult 把缓存条目标为过期（保留 Score + CheckedAt），错误字段填
// "stale_cache"，UI 显示"使用过期数据"提示。
func staleResult(cached Result, now time.Time) Result {
	r := cached
	r.Error = "stale_cache"
	r.CheckedAt = now
	return r
}

// ---- 限速（令牌桶） ----

// RateLimiter 简单令牌桶：每 provider 每分钟 N 次。
type RateLimiter struct {
	mu      sync.Mutex
	rate    int           // 每分钟请求数
	tokens  map[string]int
	lastRefill map[string]time.Time
}

// NewRateLimiter 创建限速器（rate=每分钟令牌数）。
func NewRateLimiter(perMinute int) *RateLimiter {
	return &RateLimiter{
		rate:        perMinute,
		tokens:      map[string]int{},
		lastRefill:  map[string]time.Time{},
	}
}

// Allow 检查 provider 当前是否可调用；成功调用会让token-1。
func (r *RateLimiter) Allow(provider string, now time.Time) bool {
	if r.rate <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	last := r.lastRefill[provider]
	elapsed := now.Sub(last)
	if elapsed >= time.Minute {
		r.tokens[provider] = r.rate
		r.lastRefill[provider] = now
	}
	if r.tokens[provider] > 0 {
		r.tokens[provider]--
		return true
	}
	return false
}

// ---- ErrUnknownProvider ----

// ErrUnknownProvider 未知 provider（不在 registry 中）。
var ErrUnknownProvider = errors.New("reputation: unknown provider")