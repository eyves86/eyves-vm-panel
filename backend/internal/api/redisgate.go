// redisgate 是 P4「无状态 API」半步的共享状态网关：分布式限流计数与
// TokenVersion 吊销通知。EYVESCLOUD_REDIS_ADDR 未设置时 sharedRedis()
// 返回 nil，所有调用方直接走原进程内逻辑（回滚语义见设计文档 §6 P4）。
//
// 吊销模型：真相仍是各副本内存里的配置快照；Redis 只承载「跨副本立即可见」
// 的版本信号。轮换 → SET rev:key <新版本>（TTL 24h）；删除 → SET rev:key
// 墓碑（-1，任何版本都匹配不上）；校验路径先查内存（零成本），内存判定
// 通过后用 Redis GET 复核：未命中（从未轮换）或 Redis 出错按内存判定放行
// （fail-open，单副本语义不受损）。
package api

import (
	"strconv"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/redisclient"
)

// redisShared 包级惰性客户端；测试直接赋值并经 t.Cleanup 还原。
var redisShared = redisclient.FromEnv()

func sharedRedis() *redisclient.Client { return redisShared }

const (
	// redisRevTTLSeconds 覆盖 JWT 最长生命周期：信号过期后旧令牌也已自然过期。
	redisRevTTLSeconds = 24 * 3600
	// redisRevTombstone 删除账号的墓碑值：任何合法 token_version 都不等于 -1。
	redisRevTombstone = "-1"
)

func redisRevSubKey(name string) string     { return "eyves:rev:sub:" + name }
func redisRevAdminKey(name string) string   { return "eyves:rev:adm:" + name }
func redisRevAdminGlobalKey() string        { return "eyves:rev:admver" }
func redisLoginWindowKey(key string) string { return "eyves:rl:login:" + key }
func redisAPIKeyWindowKey(id string) string { return "eyves:rl:apik:" + id }

func redisLoginWindowTTL() int64 { return int64(loginLimiter.window / time.Second) }

// notifySubUserRotated 在本副本真正轮换/新建子用户后，把（轮换后的）最新
// 版本广播到 Redis。只允许在确实发生 TokenVersion++ 或新建账号的路径调用——
// 无变更路径调用会用本副本（可能陈旧的）快照覆盖其他副本写入的新版本。
// 版本从变更后的配置内存读取，避免依赖调用方闭包捕获；v0 也写入，
// 用于同名账号重建时清除删除墓碑。
func notifySubUserRotated(name string) {
	c := sharedRedis()
	if c == nil || name == "" {
		return
	}
	config.AppConfigMu.RLock()
	ver := -1
	if i, ok := config.FindSubUserIndexByNameUnlocked(name); ok {
		ver = config.AppConfig.SubUsers[i].TokenVersion
	}
	config.AppConfigMu.RUnlock()
	if ver >= 0 {
		_ = c.SetEx(redisRevSubKey(name), strconv.Itoa(ver), redisRevTTLSeconds)
	}
}

// notifySubUserRotatedByID 同上，但按内部 ID 定位（写点只有 ID 在作用域时用）。
func notifySubUserRotatedByID(id string) {
	c := sharedRedis()
	if c == nil || id == "" {
		return
	}
	if su, ok := config.FindSubUserByID(id); ok {
		_ = c.SetEx(redisRevSubKey(su.Username), strconv.Itoa(su.TokenVersion), redisRevTTLSeconds)
	}
}

// notifySubUserDeleted 在子用户被删除后写墓碑：其他副本的陈旧快照里账号仍
// 存在且版本未变，仅靠「键不存在即放行」拦不住，必须写一个任何版本都匹配
// 不上的值，使其存量令牌在其他副本上立即失效。
func notifySubUserDeleted(name string) {
	if c := sharedRedis(); c != nil && name != "" {
		_ = c.SetEx(redisRevSubKey(name), redisRevTombstone, redisRevTTLSeconds)
	}
}

// notifyAdminRotatedByID 在额外管理员轮换/新建后广播其（最新）版本。
func notifyAdminRotatedByID(id string) {
	c := sharedRedis()
	if c == nil || id == "" {
		return
	}
	if acct, ok := config.FindAdminAccountByID(id); ok {
		_ = c.SetEx(redisRevAdminKey(acct.Username), strconv.Itoa(acct.TokenVersion), redisRevTTLSeconds)
	}
}

// notifyAdminDeleted 在额外管理员被删除后写墓碑。
func notifyAdminDeleted(name string) {
	if c := sharedRedis(); c != nil && name != "" {
		_ = c.SetEx(redisRevAdminKey(name), redisRevTombstone, redisRevTTLSeconds)
	}
}

// notifyAdminGlobalRotated 在主管理员密码/用户名轮换后广播全局版本。
func notifyAdminGlobalRotated() {
	c := sharedRedis()
	if c == nil {
		return
	}
	config.AppConfigMu.RLock()
	ver := config.AppConfig.AdminTokenVersion
	config.AppConfigMu.RUnlock()
	_ = c.SetEx(redisRevAdminGlobalKey(), strconv.Itoa(ver), redisRevTTLSeconds)
}

// redisConfirmSharedVersion 复核吊销信号：Redis 命中且版本一致才放行；
// 未命中（键不存在=从未轮换）或 Redis 出错时 true（fail-open，维持内存判定）。
// 墓碑值 -1 与任何 claim 版本都不相等，命中墓碑必拒绝。
func redisConfirmSharedVersion(c *redisclient.Client, key string, claimVersion int) bool {
	val, ok, err := c.Get(key)
	if err != nil || !ok {
		return true
	}
	shared, err := strconv.Atoi(val)
	if err != nil {
		return true
	}
	return claimVersion == shared
}
