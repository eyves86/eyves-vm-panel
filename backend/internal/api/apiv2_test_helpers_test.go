package api

// 测试辅助常量：与 apiv2_auth.go 中的限流桶键保持同步（用于 F-4 回归检测）。
// 若实现把用户名加回键名，请连同修改这里的常量说明。
const (
	v2LoginRateKeyAdmin  = `ip + "|v2-admin"`
	v2LoginRateKeyClient = `ip + "|v2-client"`
)

// apiKeyHashForTest 为回归测试生成 API Key 哈希（复用生产哈希算法）。
func apiKeyHashForTest(key string) string {
	h, err := hashAPIKey(key)
	if err != nil {
		// 测试环境哈希失败几乎不可能发生；回退 legacy 哈希保持测试可运行。
		return legacyHashKey(key)
	}
	return h
}
