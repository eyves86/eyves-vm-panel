package api

// node_sync_regression_test.go —— 节点容器同步/持久化回归（2026-09-30 生产实测修复）。
//
// 锁定以下真实 bug（生产复现链）：
//   1. 节点容器主控 ID 与本地容器撞号 → SQLite 主键冲突 → 整笔保存回滚（静默丢配置）
//   2. node_id 不落库 → 重启后节点容器"变成本机"→ orphan 检测永远失配
//   3. 心跳空容器清单时不同步 → 节点清空容器后主控无法清理 orphan
//   4. 代理删除成功后主控侧记录残留

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// syncAgentContainersForTest 以“必定落库”的语义调用增量同步（等价于旧
// syncAgentContainers 的可观察行为），便于回归断言落库后的状态。
func syncAgentContainersForTest(nodeID string, summaries []heartbeatContainerSummary) {
	var changes []containerStatusChange
	config.MutateGlobalSaveIf(func(cfg *config.EyvescloudConfig) bool {
		syncAgentContainersUnlocked(cfg, nodeID, summaries, &changes, nil)
		return true
	})
}

// resetNodeReportedForTest 清掉「上次上报集合」的进程级缓存：它在生产里就是进程级
// 状态，但测试之间会替换整个 AppConfig，留着上一轮的集合只会干扰 orphan 判定。
func resetNodeReportedForTest() {
	config.AppConfigMu.Lock()
	defer config.AppConfigMu.Unlock()
	nodeReported = nil
	nodeReportedSeeded = false
}

// setupNodeSyncTest 初始化临时 DB + 基础配置（1 个本机容器 id=3）。
func setupNodeSyncTest(t *testing.T) {
	t.Helper()
	resetNodeReportedForTest()
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

// TestNodeSyncBulkNewContainersGetDistinctIDs 一次 sync 上报多个新容器时必须拿到互不
// 相同、且不与既有容器撞号的主控 ID（分配器在整段 sync 内复用，本地自增）。
func TestNodeSyncBulkNewContainersGetDistinctIDs(t *testing.T) {
	// 分配器本身不得「按次分配」——旧实现每次调用都为全部容器建一张 map（30w 容器
	// ≈ 数十 MB）。本断言与机器性能无关，直接锁住修复动机。
	probeCfg := &config.EyvescloudConfig{NextContainerID: 1, Containers: make([]config.Container, 1000)}
	alloc := newContainerIDAllocator(probeCfg)
	if got := testing.AllocsPerRun(100, func() { _ = alloc() }); got != 0 {
		t.Fatalf("容器 ID 分配器每次调用应零分配，实测 %.1f 次", got)
	}

	setupNodeSyncTest(t) // 既有本机容器 id=3，NextContainerID=4
	nodeID := "node-bulk-1"
	summaries := []heartbeatContainerSummary{
		{ID: 1, UUID: "uuid-bulk-1", Name: "b1", Status: "stopped", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
		{ID: 2, UUID: "uuid-bulk-2", Name: "b2", Status: "stopped", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
		{ID: 3, UUID: "uuid-bulk-3", Name: "b3", Status: "stopped", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
	}
	syncAgentContainersForTest(nodeID, summaries)

	seen := map[int]string{}
	for i := range config.AppConfig.Containers {
		c := config.AppConfig.Containers[i]
		if prev, dup := seen[c.ID]; dup {
			t.Fatalf("主控 ID %d 被重复分配（%s 与 %s）——落库将撞主键", c.ID, prev, c.UUID)
		}
		seen[c.ID] = c.UUID
	}
	for _, uuid := range []string{"uuid-bulk-1", "uuid-bulk-2", "uuid-bulk-3"} {
		got := -1
		for i := range config.AppConfig.Containers {
			if config.AppConfig.Containers[i].UUID == uuid {
				got = config.AppConfig.Containers[i].ID
			}
		}
		if got <= 3 {
			t.Fatalf("%s 分配到的 ID=%d 未避开既有容器号段（应 > 3）", uuid, got)
		}
	}
}

// TestNodeSyncIDAllocatorHandlesCounterDrift 计数器落后于实际最大 ID 时必须从
// maxID+1 起分配（这是保留 O(n) 最大值扫描的原因；旧实现的 used 集合对此是死代码）。
func TestNodeSyncIDAllocatorHandlesCounterDrift(t *testing.T) {
	setupNodeSyncTest(t)
	config.AppConfigMu.Lock()
	config.AppConfig.Containers = []config.Container{
		{ID: 5, UUID: "uuid-keep-5", Name: "keep-5", Status: "stopped"},
	}
	config.AppConfig.NextContainerID = 2 // 故意落后于 maxID=5
	config.AppConfigMu.Unlock()

	syncAgentContainersForTest("node-drift-1", []heartbeatContainerSummary{
		{ID: 9, UUID: "uuid-drift-1", Name: "d1", Status: "stopped", Virtualization: "lxc", VCPU: 1, RAMMB: 128, DiskGB: 1},
	})

	var got int
	for i := range config.AppConfig.Containers {
		if config.AppConfig.Containers[i].UUID == "uuid-drift-1" {
			got = config.AppConfig.Containers[i].ID
		}
	}
	if got != 6 {
		t.Fatalf("计数器落后时应从 maxID+1=6 起分配，实际 %d", got)
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
		structural1 = syncAgentContainersUnlocked(cfg, nodeID, first, &sc1, nil)
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
		structural2 = syncAgentContainersUnlocked(cfg, nodeID, second, &sc2, nil)
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

// TestNodeHeartbeatHandlerPersistsExactDirtySet 是「心跳 → 精确落库」的端到端回归。
//
// 精确模式把落库从 O(全部容器) 压到 O(声明容器)，代价是未声明即视为未变。本用例
// 从 handler 入口驱动，落库后用「关连接 + 重开加载」验证磁盘状态，覆盖两个关键场景：
//  1. 结构性变化（新增节点容器）必须落库，且 node_id 归属正确（重启后不变成本机容器）；
//  2. 节流窗口到期后的「纯遥测」心跳（无结构变化）也必须把流量累计落库 ——
//     这是精确模式最容易漏声明的一类改动。
func TestNodeHeartbeatHandlerPersistsExactDirtySet(t *testing.T) {
	setupNodeSyncTest(t)
	nodeID := "node-e2e-1"
	token := "e2e-node-token"

	config.AppConfigMu.Lock()
	config.AppConfig.Nodes = []config.Node{{ID: nodeID, Name: "e2e", Token: token}}
	config.AppConfigMu.Unlock()
	if err := config.SaveConfig(); err != nil {
		t.Fatal(err)
	}

	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/heartbeat", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handleNodeHeartbeat(rec, req, nodeID)
		return rec.Code
	}

	// 让该节点的节流窗口立即到期（等价于距上次落库已满 nodeHeartbeatPersistEvery）。
	expireThrottle := func() {
		nodeHeartbeatSaveMu.Lock()
		delete(nodeHeartbeatSavedAt, nodeID)
		nodeHeartbeatSaveMu.Unlock()
	}

	// 场景 1：首跳报一个新容器（结构性变化）。
	body := `{"version":"2.2.50","containers":[{"id":3,"uuid":"uuid-e2e","name":"e2e-c","status":"running",` +
		`"virtualization":"lxc","vcpu":1,"ram_mb":128,"disk_gb":1,"traffic_used_rx":777,"traffic_used_tx":888}]}`
	if code := post(body); code != http.StatusOK {
		t.Fatalf("心跳应返回 200，实际 %d", code)
	}

	// 场景 2：纯遥测心跳（结构未变，只有流量累计）。先让节流窗口到期，否则不会落库。
	expireThrottle()
	telemetry := `{"version":"2.2.50","containers":[{"id":3,"uuid":"uuid-e2e","name":"e2e-c","status":"running",` +
		`"virtualization":"lxc","vcpu":1,"ram_mb":128,"disk_gb":1,"traffic_used_rx":999111,"traffic_used_tx":222333}]}`
	if code := post(telemetry); code != http.StatusOK {
		t.Fatalf("遥测心跳应返回 200，实际 %d", code)
	}

	// 落库验证：心跳现在是「延后合并落库」（P2 批处理），所以要先补刷一次窗口 ——
	// 这正是生产里的路径：agent 心跳返回 200 后不久，后台窗口把这一批写进库。
	if err := config.FlushDeferredSaves(); err != nil {
		t.Fatalf("补刷心跳批处理失败：%v", err)
	}
	// 关连接重开（等价于面板重启），从磁盘读回。
	config.CloseConfigDB()
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("重载配置失败：%v", err)
	}
	var got *config.Container
	config.AppConfigMu.RLock()
	for i := range config.AppConfig.Containers {
		if config.AppConfig.Containers[i].UUID == "uuid-e2e" {
			c := config.AppConfig.Containers[i]
			got = &c
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if got == nil {
		t.Fatal("心跳上报的节点容器未落库（重启后丢失）")
	}
	if got.NodeID != nodeID {
		t.Fatalf("node_id 未落库：%q（重启后会被误判为本机容器）", got.NodeID)
	}
	if got.NodeLocalID != 3 {
		t.Fatalf("node_local_id 未落库：%d, want 3", got.NodeLocalID)
	}
	if got.Status != "running" {
		t.Fatalf("status 未落库：%q, want running", got.Status)
	}
	if got.TrafficUsedRX != 999111 || got.TrafficUsedTX != 222333 {
		t.Fatalf("遥测未落库：rx=%d tx=%d, want 999111/222333（纯遥测漏声明会被静默丢弃）",
			got.TrafficUsedRX, got.TrafficUsedTX)
	}
}

// TestRemoveNodeCascadesContainers 锁定实机发现的缺陷：删除节点后其容器记录必须一并
// 移除。否则会留下悬空 node_id 的「幽灵实例」——列表里照常显示，但节点已不存在，
// 既不会被该节点心跳刷新，也无法被 orphan 对账修复（节点没了就没有对账机会）。
// 同时断言本机容器不受影响。
func TestRemoveNodeCascadesContainers(t *testing.T) {
	setupNodeSyncTest(t)
	nodeID := "node-cascade"
	config.AppConfigMu.Lock()
	config.AppConfig.Nodes = []config.Node{{ID: nodeID, Name: "cascade", Token: "t"}}
	config.AppConfigMu.Unlock()

	// 该节点上报 2 个容器 → 主控建 2 条节点容器记录。
	syncAgentContainersForTest(nodeID, []heartbeatContainerSummary{
		{ID: 1, UUID: "uuid-c1", Name: "c1", Status: "running"},
		{ID: 2, UUID: "uuid-c2", Name: "c2", Status: "running"},
	})

	count := func() (nodes, nodeConts, localConts int) {
		config.AppConfigMu.RLock()
		defer config.AppConfigMu.RUnlock()
		for i := range config.AppConfig.Nodes {
			if config.AppConfig.Nodes[i].ID == nodeID {
				nodes++
			}
		}
		for i := range config.AppConfig.Containers {
			if config.AppConfig.Containers[i].NodeID == nodeID {
				nodeConts++
			} else {
				localConts++
			}
		}
		return
	}
	if n, nc, _ := count(); n != 1 || nc != 2 {
		t.Fatalf("前置状态不符：节点=%d 节点容器=%d，want 1/2", n, nc)
	}

	removed, conts := config.RemoveNode(nodeID)
	if !removed {
		t.Fatal("RemoveNode 未报告移除成功")
	}
	if conts != 2 {
		t.Fatalf("级联移除容器数 = %d, want 2", conts)
	}
	if n, nc, local := count(); n != 0 || nc != 0 || local != 1 {
		t.Fatalf("级联结果：节点=%d 节点容器=%d 本机容器=%d，want 0/0/1（本机容器不得受影响）",
			n, nc, local)
	}

	// 幂等 / 不存在：返回 (false, 0)。
	if removed, conts := config.RemoveNode(nodeID); removed || conts != 0 {
		t.Fatalf("重复删除应返回 (false,0)，实际 (%v,%d)", removed, conts)
	}
}
