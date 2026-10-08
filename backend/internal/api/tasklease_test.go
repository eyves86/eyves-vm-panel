package api

import (
	"testing"

	"eyvescloud/internal/redisclient"
)

// TestMaintenanceLeaseDefaultActive 未配置 Redis（或测试未注入）时所有维护
// 循环照常运行——单副本语义是默认行为，租约不得改变它。
func TestMaintenanceLeaseDefaultActive(t *testing.T) {
	if !maintenanceLeaseActive() {
		t.Fatal("默认（无 Redis）必须放行全部维护循环")
	}
}

// TestMaintenanceLeaseSelectsSingleOwner 真 Redis 集成用例：本副本抢占成功；
// 他人占位时本副本必须让位；键空出后接管；续约保持持有；Redis 不可达时
// fail-open。需要 EYVESCLOUD_REDIS_TEST_ADDR。
func TestMaintenanceLeaseSelectsSingleOwner(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	key := uniqueKey("eyves:lease:test:")
	t.Cleanup(func() {
		_ = c.Del(key)
		leaseHeld.Store(true)
	})

	// 抢占：键不存在 → NX 成功 → 持有。
	refreshMaintenanceLease(c, key)
	if !maintenanceLeaseActive() {
		t.Fatal("空键时本副本必须抢到租约")
	}
	if v, ok, _ := c.Get(key); !ok || v != leaseOwner {
		t.Fatalf("租约键应记录本副本 owner，got %q ok=%v", v, ok)
	}

	// 排他：他人占位（模拟另一副本）→ 本副本让位。
	if err := c.SetEx(key, "other-replica", 30); err != nil {
		t.Fatalf("写入他人租约失败: %v", err)
	}
	refreshMaintenanceLease(c, key)
	if maintenanceLeaseActive() {
		t.Fatal("他人持有租约时本副本必须让位（否则 N 副本重复扫全库）")
	}

	// 接管：他人租约空出（此处删键模拟过期）→ 本副本接管。
	_ = c.Del(key)
	refreshMaintenanceLease(c, key)
	if !maintenanceLeaseActive() {
		t.Fatal("租约空出后本副本必须接管")
	}

	// 续约：持有者重复刷新仍持有。
	refreshMaintenanceLease(c, key)
	if !maintenanceLeaseActive() {
		t.Fatal("持有者续约必须保持持有")
	}

	// fail-open：Redis 不可达 → 放行（宁可重复执行幂等维护，不整体停摆）。
	dead := redisclient.Open("127.0.0.1:1", "")
	defer dead.Close()
	refreshMaintenanceLease(dead, key)
	if !maintenanceLeaseActive() {
		t.Fatal("Redis 不可达时必须 fail-open 放行维护循环")
	}
}
