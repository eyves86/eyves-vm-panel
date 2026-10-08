// tasklease 实现设计文档 §4.5「后台任务分布式租约」：多副本部署时，全局维护
// 循环（到期扫描、指标采样/聚合、备份计划、日志/回收站清理等）用 Redis 选主，
// 集群内只有持租约的副本执行，避免 N 副本重复扫全库。EYVESCLOUD_REDIS_ADDR
// 未设置时 maintenanceLeaseActive 恒为 true，所有循环照常运行（单副本语义，
// 回滚 = 不设该 env）。本机语义的循环（宿主机指标采样、安全组 iptables、
// 任务队列派发器）不经过租约。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"eyvescloud/internal/redisclient"
)

// maintenanceLeaseScript 原子「是我则续约，否则 NX 抢占」；返回 1 = 持有租约。
const maintenanceLeaseScript = `if redis.call('get',KEYS[1]) == ARGV[1] then redis.call('EXPIRE',KEYS[1],ARGV[2]) return 1 else if redis.call('SET',KEYS[1],ARGV[1],'NX','EX',ARGV[2]) then return 1 else return 0 end end`

const (
	maintenanceLeaseKey = "eyves:lease:maint"
	// TTL ≥ 2×续约间隔：持有者宕机后其余副本最迟 ~30s 内接管。
	maintenanceLeaseTTLSeconds = 30
	maintenanceLeaseInterval   = 10 * time.Second
)

var (
	leaseOwner = randomLeaseOwner()
	leaseHeld  atomic.Bool
)

func init() { leaseHeld.Store(true) }

func randomLeaseOwner() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartMaintenanceLease 由 server.Run 在调度器启动前调用；未配置 Redis 时空操作。
func StartMaintenanceLease() {
	c := sharedRedis()
	if c == nil {
		return
	}
	// 先抢一次再进入续约循环，避免最长一个续约间隔的全副本空窗。
	refreshMaintenanceLease(c, maintenanceLeaseKey)
	log.Printf("分布式模式：维护任务租约选主已启用（%s），仅持租约副本执行全局维护循环", maintenanceLeaseKey)
	go func() {
		ticker := time.NewTicker(maintenanceLeaseInterval)
		defer ticker.Stop()
		for range ticker.C {
			refreshMaintenanceLease(c, maintenanceLeaseKey)
		}
	}()
}

// refreshMaintenanceLease 对 key 做一轮「是我则续约，否则抢占」；key 参数化
// 是为了让集成测试用独立键验证同一脚本，不碰全局租约键。
func refreshMaintenanceLease(c *redisclient.Client, key string) {
	res, err := c.Eval(maintenanceLeaseScript, []string{key},
		leaseOwner, strconv.FormatInt(maintenanceLeaseTTLSeconds, 10))
	// Redis 出错按 fail-open（等同未配置）：宁可多副本重复执行幂等维护，
	// 也不能让 Redis 抖动把到期停机/备份/清理全部停掉。
	leaseHeld.Store(err != nil || res == 1)
}

// maintenanceLeaseActive 报告本副本当前是否应执行全局维护循环。
func maintenanceLeaseActive() bool { return leaseHeld.Load() }
