package api

import (
	"strings"
	"sync"
)

// containerIdempotency 记录“已成功创建”的容器名，键为客户端提供的
// Idempotency-Key。用于打通开通流程：同一幂等键重试时直接返回既有容器，
// 避免计费系统（WHMCS / 魔方）在回调超时后重复开通第二次。
//
// 说明：容器名的唯一性校验（lxc.Manager.CreateContainer 的
// “container name already exists”）仍是最终兜底，本表仅让同键重试
// 以幂等的成功结果返回，而不是以冲突错误失败。
var (
	containerIdemMu   sync.RWMutex
	containerIdemKeys = map[string]string{} // key -> container name
)

func normalizeIdempotencyKey(key string) string {
	return strings.TrimSpace(key)
}

func containerIdemLookup(key string) (string, bool) {
	containerIdemMu.RLock()
	defer containerIdemMu.RUnlock()
	name, ok := containerIdemKeys[key]
	return name, ok
}

func containerIdemStore(key, name string) {
	containerIdemMu.Lock()
	defer containerIdemMu.Unlock()
	containerIdemKeys[key] = name
}

func containerIdemRemove(key string) {
	containerIdemMu.Lock()
	defer containerIdemMu.Unlock()
	delete(containerIdemKeys, key)
}