package api

import (
	"strings"
	"sync"
	"time"
)

// containerIdempotency 记录“已成功创建”的容器名，键为客户端提供的
// Idempotency-Key。用于打通开通流程：同一幂等键重试时直接返回既有容器，
// 避免计费系统（WHMCS 等）在回调超时后重复开通第二次。
//
// 说明：容器名的唯一性校验（lxc.Manager.CreateContainer 的
// “container name already exists”）仍是最终兜底，本表仅让同键重试
// 以幂等的成功结果返回，而不是以冲突错误失败。
var (
	containerIdemMu   sync.RWMutex
	containerIdemKeys = map[string]idemEntry{} // key -> {container name, stored at}
)

// idemEntry 记录幂等键映射与写入时间，用于 TTL 回收，避免客户端用无限多条
// 唯一 Idempotency-Key 撑爆进程内存（万级规模下的软性 DoS）。
type idemEntry struct {
	name string
	at   time.Time
}

const (
	containerIdemTTL        = 24 * time.Hour
	containerIdemMaxEntries = 10000
)

func normalizeIdempotencyKey(key string) string {
	return strings.TrimSpace(key)
}

func containerIdemLookup(key string) (string, bool) {
	containerIdemMu.RLock()
	defer containerIdemMu.RUnlock()
	e, ok := containerIdemKeys[key]
	if !ok || time.Since(e.at) > containerIdemTTL {
		return "", false
	}
	return e.name, true
}

func containerIdemStore(key, name string) {
	containerIdemMu.Lock()
	defer containerIdemMu.Unlock()
	now := time.Now()
	if len(containerIdemKeys) >= containerIdemMaxEntries {
		for k, e := range containerIdemKeys {
			if now.Sub(e.at) > containerIdemTTL {
				delete(containerIdemKeys, k)
			}
		}
		if len(containerIdemKeys) >= containerIdemMaxEntries {
			// 极端情况下仍超限：整体清空，保证内存有界（幂等键本就短期有效）。
			containerIdemKeys = map[string]idemEntry{}
		}
	}
	containerIdemKeys[key] = idemEntry{name: name, at: now}
}

func containerIdemRemove(key string) {
	containerIdemMu.Lock()
	defer containerIdemMu.Unlock()
	delete(containerIdemKeys, key)
}