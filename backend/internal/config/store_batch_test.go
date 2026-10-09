package config

import (
	"path/filepath"
	"testing"
	"time"
)

// ---- P2 心跳落库批处理单测 ----
//
// 契约（与 store_batch.go 头部注释一一对应）：
//   - 窗口内同一行的多次声明只提交一次（合并成单事务）；
//   - 锁定的是行**标识**，提交时按标识重新读值 —— 声明后该行又被改，写进去的是最新值；
//   - 内存改动立即可见（面板读内存），磁盘延后到窗口提交；
//   - 不支持延后删除：声明一个已消失的行必须被静默跳过，绝不能误删库里的行；
//   - 提交失败不丢改动（mergeBack），下次提交补上；
//   - 待落库集合达上限立即唤醒后台（背压）。

// resetDeferredForTest 清空待落库集合与唤醒信号，并复位窗口参数。
func resetDeferredForTest(t *testing.T) {
	t.Helper()
	drainDeferred := func() {
		deferredSaves.take()
		select {
		case <-deferredSaves.wake:
		default:
		}
	}
	drainDeferred()
	oldInterval, oldMax := deferredFlushInterval, deferredMaxRows
	t.Cleanup(func() {
		deferredFlushInterval, deferredMaxRows = oldInterval, oldMax
		drainDeferred()
	})
}

// enqueueDeferred 以「持 AppConfigMu」的契约入队一批声明。
func enqueueDeferred(ids DirtyIDs) {
	AppConfigMu.Lock()
	deferredSaves.add(ids)
	AppConfigMu.Unlock()
}

// initDeferredStore 建立临时库并把 AppConfig 指向一个干净配置。
func initDeferredStore(t *testing.T) {
	t.Helper()
	resetConfigStoreForTest(t)
	resetDeferredForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
}

// TestDeferredSavesCoalesceSameRow 验证窗口内同一行的多次声明只写一次：
// 50 次心跳声明同一容器 → 恰好 1 次行写，且写入的是最后一次的值。
func TestDeferredSavesCoalesceSameRow(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	const beats = 50
	for i := 0; i < beats; i++ {
		AppConfigMu.Lock()
		AppConfig.Containers[0].TrafficUsedRX = int64(i + 1) // 每跳刷新遥测
		AppConfigMu.Unlock()
		enqueueDeferred(DirtyIDs{Containers: []int{1}})
	}
	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}

	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("50 次心跳声明产生了 %d 次 containers 行写，want 1（窗口内必须合并）", got)
	}
	var rx int64
	if err := db.QueryRow(`SELECT traffic_used_rx FROM containers WHERE id = 1`).Scan(&rx); err != nil {
		t.Fatal(err)
	}
	if rx != beats {
		t.Fatalf("合并提交未写入最终值：rx=%d, want %d", rx, beats)
	}
}

// TestDeferredUsesLatestValueNotDeclaredSnapshot 是 P2「按标识而非按值声明」设计的核心回归：
// 声明之后该行又被别的写路径改过，提交时必须写**最新值**，否则会静默回滚并发改动。
func TestDeferredUsesLatestValueNotDeclaredSnapshot(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Nodes = []Node{{ID: "node-1", Name: "n1", Token: "t", MaintenanceMode: false}}
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	// 心跳声明「本节点/本容器有改动」（例如刷新 LastSeen）。
	enqueueDeferred(DirtyIDs{Nodes: []string{"node-1"}, Containers: []int{1}})

	// 声明之后，管理端并发地把节点切进维护模式、把容器停掉 ——
	// 若提交时用的是「声明时刻的快照」，这两处改动会被静默回滚。
	AppConfigMu.Lock()
	AppConfig.Nodes[0].MaintenanceMode = true
	AppConfig.Containers[0].Status = "stopped"
	AppConfigMu.Unlock()

	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}

	nodes, err := loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || !nodes[0].MaintenanceMode {
		t.Fatalf("延后提交回滚了并发的维护模式改动（用了过期快照）：%+v", nodes)
	}
	var diskStatus string
	if err := db.QueryRow(`SELECT status FROM containers WHERE id = 1`).Scan(&diskStatus); err != nil {
		t.Fatal(err)
	}
	if diskStatus != "stopped" {
		t.Fatalf("容器状态被旧快照覆盖：库中 = %q, want stopped", diskStatus)
	}
}

// TestDeferredMemoryImmediateDiskLags 钉死「内存立即可见、磁盘延后」的语义：
// 面板读内存不受影响，落库被推迟到窗口提交。
func TestDeferredMemoryImmediateDiskLags(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	AppConfigMu.Lock()
	AppConfig.Containers[0].Status = "stopped"
	deferredSaves.add(DirtyIDs{Containers: []int{1}})
	AppConfigMu.Unlock()

	// 内存：立即是新值。
	AppConfigMu.RLock()
	memStatus := AppConfig.Containers[0].Status
	AppConfigMu.RUnlock()
	if memStatus != "stopped" {
		t.Fatalf("内存状态 = %q, want stopped（内存必须立即生效）", memStatus)
	}

	// 磁盘：仍是旧值，且提交前不得产生行写。
	if got := probeRead(t)["containers_INSERT"] - base["containers_INSERT"]; got != 0 {
		t.Fatalf("窗口提交前就写了 %d 行，want 0（落库应延后）", got)
	}
	var diskStatus string
	if err := db.QueryRow(`SELECT status FROM containers WHERE id = 1`).Scan(&diskStatus); err != nil {
		t.Fatal(err)
	}
	if diskStatus != "running" {
		t.Fatalf("提交前磁盘 = %q, want running（磁盘允许滞后）", diskStatus)
	}

	// 提交后一致。
	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM containers WHERE id = 1`).Scan(&diskStatus); err != nil {
		t.Fatal(err)
	}
	if diskStatus != "stopped" {
		t.Fatalf("提交后磁盘 = %q, want stopped", diskStatus)
	}
}

// TestDeferredFlushEmptyIsNoop 空刷必须是纯 no-op（含 AppConfig 未初始化）。
func TestDeferredFlushEmptyIsNoop(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		if d := after["containers_"+op] - base["containers_"+op]; d != 0 {
			t.Fatalf("空刷写了 containers.%s %d 次，want 0", op, d)
		}
	}

	// AppConfig 未初始化时也必须是安全的 no-op。
	resetConfigStoreForTest(t)
	if err := FlushDeferredSaves(); err != nil {
		t.Fatalf("AppConfig 未初始化时空刷报错：%v", err)
	}
}

// TestDeferredSkipsVanishedRowWithoutDeleting 验证「不支持延后删除」：
// 声明一个已在内存消失的行，提交时静默跳过，绝不能顺手删掉库里的行。
func TestDeferredSkipsVanishedRowWithoutDeleting(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	AppConfigMu.Lock()
	deferredSaves.add(DirtyIDs{Containers: []int{1}})
	AppConfig.Containers = nil // 声明之后该行在内存里消失
	AppConfigMu.Unlock()

	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if d := after["containers_DELETE"] - base["containers_DELETE"]; d != 0 {
		t.Fatalf("延后提交误删了 %d 行（不支持延后删除）", d)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM containers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("容器行数 = %d, want 1（延后机制绝不能删行）", n)
	}
}

// TestDeferredPersistsNodeRows 验证节点行也走批处理落库（心跳只声明本节点的标识）。
func TestDeferredPersistsNodeRows(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Nodes = []Node{{ID: "node-1", Name: "n1", Token: "t", LastSeen: "old"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "nodes")
	base := probeRead(t)

	AppConfigMu.Lock()
	AppConfig.Nodes[0].LastSeen = "2026-10-06 05:00:00"
	deferredSaves.add(DirtyIDs{Nodes: []string{"node-1"}})
	AppConfigMu.Unlock()

	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	if d := probeRead(t)["nodes_INSERT"] - base["nodes_INSERT"]; d != 1 {
		t.Fatalf("节点行写 = %d, want 1", d)
	}
	nodes, err := loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].LastSeen != "2026-10-06 05:00:00" {
		t.Fatalf("节点 LastSeen 未落库：%+v", nodes)
	}
}

// TestDeferredBackpressureSignalsAtRowLimit 验证达到行上限即唤醒后台提交（背压），
// 避免突发心跳把窗口撑成超大事务、或让待落库集合无界增长。
func TestDeferredBackpressureSignalsAtRowLimit(t *testing.T) {
	initDeferredStore(t)
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
		{ID: 3, UUID: "uuid-3", Name: "ct-3", Status: "running"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	deferredMaxRows = 3 // 声明 3 行即达上限

	// 未达上限：不唤醒（避免每跳都唤醒后台空转）。
	enqueueDeferred(DirtyIDs{Containers: []int{1}})
	select {
	case <-deferredSaves.wake:
		t.Fatal("未达上限却投递了唤醒信号")
	default:
	}

	// 达到上限：立即唤醒后台提交（背压）。
	enqueueDeferred(DirtyIDs{Containers: []int{2, 3}})
	select {
	case <-deferredSaves.wake:
		// 期望：达到上限后立即投递唤醒信号。
	case <-time.After(2 * time.Second):
		t.Fatal("达到行上限后未唤醒后台提交（背压失效：窗口可能被撑成超大事务）")
	}
}

// TestDeferredFlushFailureMergesBack 验证提交失败不丢改动：失败的批被并回待落库集合，
// 下一次提交必须补上（否则这批心跳改动会永久丢失）。
func TestDeferredFlushFailureMergesBack(t *testing.T) {
	initDeferredStore(t)
	t.Cleanup(func() { forceFullScanNextSave = false })
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	// 注入失败：让容器行的写入中止。批处理走 newExactDirtySet，目录类集合被声明为
	// 未改动（零扫描），故注入点必须落在容器表上才能触发失败。
	installFailTrigger(t, "fail_container_insert", "containers", "INSERT")
	t.Cleanup(func() { dropFailTrigger(t, "fail_container_insert", "containers") })

	AppConfigMu.Lock()
	AppConfig.Containers[0].Status = "stopped"
	deferredSaves.add(DirtyIDs{Containers: []int{1}})
	AppConfigMu.Unlock()

	if err := FlushDeferredSaves(); err == nil {
		t.Fatal("注入的触发器未让提交失败：用例前提不成立")
	}

	// 失败的批必须被并回待落库集合。
	deferredSaves.mu.Lock()
	_, kept := deferredSaves.pending.conts[1]
	deferredSaves.mu.Unlock()
	if !kept {
		t.Fatal("提交失败后待落库标识被丢弃（这批心跳改动会永久丢失）")
	}

	// 移除注入后再次提交必须补上。
	dropFailTrigger(t, "fail_container_insert", "containers")
	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	var diskStatus string
	if err := db.QueryRow(`SELECT status FROM containers WHERE id = 1`).Scan(&diskStatus); err != nil {
		t.Fatal(err)
	}
	if diskStatus != "stopped" {
		t.Fatalf("补救提交未写入改动：库中 = %q, want stopped", diskStatus)
	}
}

// TestDeferredCoalescingAtScale 量化批处理对事务数的压缩：同样是 K 次心跳，
// 「逐跳落库」产生 K 次行写，而「窗口合并」只产生 1 次行写。断言只依赖写量
// （与机器性能无关）；耗时仅作日志证据。
func TestDeferredCoalescingAtScale(t *testing.T) {
	k := 500
	if testing.Short() {
		k = 50
	}
	initDeferredStore(t)
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	toggle := func() {
		if AppConfig.Containers[0].Status == "running" {
			AppConfig.Containers[0].Status = "stopped"
		} else {
			AppConfig.Containers[0].Status = "running"
		}
	}

	// A) 逐跳落库（等价旧行为）：每次声明后立即提交 → 每跳一次行写。
	t0 := time.Now()
	for i := 0; i < k; i++ {
		AppConfigMu.Lock()
		toggle()
		AppConfigMu.Unlock()
		enqueueDeferred(DirtyIDs{Containers: []int{1}})
		if err := FlushDeferredSaves(); err != nil {
			t.Fatal(err)
		}
	}
	perBeat := time.Since(t0)
	afterA := probeRead(t)
	rowsA := afterA["containers_INSERT"] - base["containers_INSERT"]
	if rowsA != k {
		t.Fatalf("逐跳落库写 %d 行，want %d", rowsA, k)
	}

	// B) 窗口合并（新行为）：K 次声明累积到窗口末尾，只提交一次。
	t1 := time.Now()
	for i := 0; i < k; i++ {
		AppConfigMu.Lock()
		AppConfig.Containers[0].TrafficUsedRX = int64(i + 1) // 每跳都有新值，终值必然与落库值不同
		AppConfigMu.Unlock()
		enqueueDeferred(DirtyIDs{Containers: []int{1}})
	}
	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	coalesced := time.Since(t1)
	rowsB := probeRead(t)["containers_INSERT"] - afterA["containers_INSERT"]
	if rowsB != 1 {
		t.Fatalf("窗口合并写 %d 行，want 1（窗口内必须合并成单事务）", rowsB)
	}

	t.Logf("K=%d 逐跳落库=%d 行写耗时 %v；窗口合并=1 行写耗时 %v（事务数 %d→1）",
		k, rowsA, perBeat.Round(time.Millisecond), coalesced.Round(time.Millisecond), k)
}
