package api

// node_sync_regression_test.go —— 节点容器同步/持久化回归（2026-09-30 生产实测修复）。
//
// 锁定以下真实 bug（生产复现链）：
//   1. 节点容器主控 ID 与本地容器撞号 → SQLite 主键冲突 → 整笔保存回滚（静默丢配置）
//   2. node_id 不落库 → 重启后节点容器"变成本机"→ orphan 检测永远失配
//   3. 心跳空容器清单时不同步 → 节点清空容器后主控无法清理 orphan
//   4. 代理删除成功后主控侧记录残留

import (
	"path/filepath"
	"testing"

	"eyvescloud/internal/config"
)

// syncAgentContainersForTest 以“必定落库”的语义调用增量同步（等价于旧
// syncAgentContainers 的可观察行为），便于回归断言落库后的状态。
func syncAgentContainersForTest(nodeID string, summaries []heartbeatContainerSummary) {
	var changes []containerStatusChange
	config.MutateGlobalSaveIf(func(cfg *config.EyvescloudConfig) bool {
		syncAgentContainersUnlocked(cfg, nodeID, summaries, &changes)
		return true
	})
}

// setupNodeSyncTest 初始化临时 DB + 基础配置（1 个本机容器 id=3）。
func setupNodeSyncTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.AppConfigMu.Lock()
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:       "root-admin",
		AdminPassHash:   "x",
		JWTSecret:       "s",
		NextContainerID: 4,
		Containers: []config.Container{
			{ID: 3, UUID: "uuid-local-3", Name: "local-3", Status: "stopped"},
		},
	}
	config.AppConfigMu.Unlock()
	if err := config.SaveConfig(); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// TestNodeSyncCollisionAssignsUniqueID 撞号场景：节点上报 id=3（与本机 3 撞）
// → 分配主控唯一 ID，NodeLocalID 保存节点本地 3；保存事务必须成功（不撞主键）。
func TestNodeSyncCollisionAssignsUniqueID(t *testing.T) {
	setupNodeSyncTest(t)
	nodeID := "node-test-1"
	summaries := []heartbeatContainerSummary{
		{ID: 3, UUID: "uuid-node-3", Name: "node-c3", Status: "stopped", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
	}
	syncAgentContainersForTest(nodeID, summaries)

	// 找到新容器
	var got *config.Container
	for i := range config.AppConfig.Containers {
		if config.AppConfig.Containers[i].UUID == "uuid-node-3" {
			got = &config.AppConfig.Containers[i]
		}
	}
	if got == nil {
		t.Fatal("节点容器未被同步入库")
	}
	if got.ID == 3 {
		t.Fatalf("主控ID与本机容器撞号（%d）——保存将因主键冲突整笔回滚", got.ID)
	}
	if got.NodeLocalID != 3 {
		t.Fatalf("NodeLocalID 应为节点本地 3，实际 %d", got.NodeLocalID)
	}
	if got.NodeID != nodeID {
		t.Fatalf("NodeID 未设置：%q", got.NodeID)
	}
	// 保存必须成功且落库（重启后 node_id/node_local_id 仍在）
	if err := config.SaveConfig(); err != nil {
		t.Fatalf("保存失败（主键冲突没有消除）：%v", err)
	}
}

// TestNodeSyncOrphanMarking 节点上报缺失的容器 → 标记 orphaned；空清单同样生效。
func TestNodeSyncOrphanMarking(t *testing.T) {
	setupNodeSyncTest(t)
	nodeID := "node-test-2"
	syncAgentContainersForTest(nodeID, []heartbeatContainerSummary{
		{ID: 1, UUID: "uuid-n1", Name: "n1", Status: "stopped"},
		{ID: 2, UUID: "uuid-n2", Name: "n2", Status: "stopped"},
	})
	// 只上报 n1 → n2 应 orphaned
	syncAgentContainersForTest(nodeID, []heartbeatContainerSummary{
		{ID: 1, UUID: "uuid-n1", Name: "n1", Status: "stopped"},
	})
	find := func(uuid string) *config.Container {
		for i := range config.AppConfig.Containers {
			if config.AppConfig.Containers[i].UUID == uuid {
				return &config.AppConfig.Containers[i]
			}
		}
		return nil
	}
	if c := find("uuid-n2"); c == nil || c.Status != "orphaned" {
		t.Fatalf("n2 应标记 orphaned，实际 %+v", c)
	}
	if c := find("uuid-n1"); c == nil || c.Status == "orphaned" {
		t.Fatal("n1 不应被 orphaned")
	}
	// 空清单：n1 也应 orphaned（此前空清单直接跳过同步）
	syncAgentContainersForTest(nodeID, []heartbeatContainerSummary{})
	if c := find("uuid-n1"); c == nil || c.Status != "orphaned" {
		t.Fatalf("空清单同步后 n1 应 orphaned，实际 %+v", c)
	}
}

// TestNodeContainerRecordRemoval 代理删除成功后的记录清理。
func TestNodeContainerRecordRemoval(t *testing.T) {
	setupNodeSyncTest(t)
	syncAgentContainersForTest("node-test-3", []heartbeatContainerSummary{
		{ID: 9, UUID: "uuid-del", Name: "del-c", Status: "stopped"},
	})
	removeNodeContainerRecord("uuid-del")
	for i := range config.AppConfig.Containers {
		if config.AppConfig.Containers[i].UUID == "uuid-del" {
			t.Fatal("记录应已被移除")
		}
	}
}

// TestNodeSyncTelemetryOnlyIsNotStructural 锁定写放大修复：
// 仅遥测字段（流量累计、IP）变化的心跳不应被判定为“需要立即落库的实质变化”。
func TestNodeSyncTelemetryOnlyIsNotStructural(t *testing.T) {
	setupNodeSyncTest(t)
	nodeID := "node-telemetry"
	first := []heartbeatContainerSummary{
		{ID: 5, UUID: "uuid-t", Name: "t", Status: "running", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
	}
	var sc1 []containerStatusChange
	structural1 := false
	config.MutateGlobalSaveIf(func(cfg *config.EyvescloudConfig) bool {
		structural1 = syncAgentContainersUnlocked(cfg, nodeID, first, &sc1)
		return structural1
	})
	if !structural1 {
		t.Fatal("首次新增容器应判定为结构性变化")
	}
	second := []heartbeatContainerSummary{
		{ID: 5, UUID: "uuid-t", Name: "t", Status: "running", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1,
			IP: "10.9.9.9", SSHPort: 22001, TrafficUsedRX: 123456, TrafficUsedTX: 654321},
	}
	var sc2 []containerStatusChange
	structural2 := false
	config.MutateGlobalSaveIf(func(cfg *config.EyvescloudConfig) bool {
		structural2 = syncAgentContainersUnlocked(cfg, nodeID, second, &sc2)
		return structural2
	})
	if structural2 {
		t.Fatal("仅遥测字段变化不应判定为结构性变化（写放大回归）")
	}
}

// TestNodeHeartbeatSaveDueThrottles 锁定心跳落库节流：首次必定到期，节流窗口内不重复落库。
func TestNodeHeartbeatSaveDueThrottles(t *testing.T) {
	nodeID := "node-due-test"
	if !nodeHeartbeatSaveDue(nodeID) {
		t.Fatal("首个心跳应到期（需要落库）")
	}
	if nodeHeartbeatSaveDue(nodeID) {
		t.Fatal("节流窗口内的第二个心跳不应到期")
	}
}
