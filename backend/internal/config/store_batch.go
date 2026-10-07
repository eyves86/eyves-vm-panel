package config

import (
	"sync"
	"time"
)

// ---- P2 心跳落库批处理 ----
//
// 背景：节点心跳是 24/7 最高频的写入方（30k 节点 × 10s = 3000 次/秒）。P1 已把
// 单次保存压到 O(声明行)（30k 节点实测 329µs），但「一次心跳一个事务」本身仍是
// 墙：每次提交都要 fsync，且 saveBoundedLogs 会重写有界日志表（audit_logs ≤500 +
// login_logs ≤200 行）。心跳频率与节点数成正比时，这部分是无谓的固定写入；单写者
// SQLite 的事务延迟还会直接变成 agent 的心跳请求延迟。
//
// 本文件把心跳落库从「一请求一事务」改为「按窗口合并成一个事务」：改动仍在内存里
// **立即**生效（面板读内存，读路径不受影响），只有落库延后到下一个窗口。窗口内同一
// 行被多次声明只提交一次。
//
// 声明的是**行标识**而不是行值（见 DirtyIDs）：提交时在写锁内从内存重新读取该行的
// 当前值。这样即使声明与提交之间该行又被别的路径改过，写进去的也不会是过期快照。
//
// 可丢失性：崩溃时最多丢一个窗口（≤ deferredFlushInterval）的遥测。agent 下个心跳
// 会重发同一份数据，节点/容器状态也由下一次上报重建，故不构成数据损失；收到
// SIGTERM 时 FlushDeferredSaves 会补刷一次，正常重启不丢窗口。
//
// 不做的事：不支持「延后删除」——删除行需要旧值才能清理关联行，而旧值在延后执行时
// 可能已经取不到。需要删除的路径继续走同步落库（saveConfigToDB / MutateGlobal）。

var (
	// deferredFlushInterval 是合并窗口长度。窗口内的心跳合并成一次事务。
	deferredFlushInterval = 250 * time.Millisecond
	// deferredMaxRows 是单个窗口的脏行上限：达到即立刻提交，避免突发心跳把窗口
	// 撑成超大事务，也避免待落库集合无界增长。
	deferredMaxRows = 4000
)

// DirtyIDs 是「延后落库」的改动声明：只列哪些行被改动过，具体值在提交时于写锁内
// 从 cfg 重新读取。适用于高频且只改少数行的路径（心跳）。
//
// 契约：调用方必须把本次改动过的行**全部**列出；未列出即视为未变（与 DirtySet
// 一致）。因为它只声明标识，所以「声明后该行又被改」不需要重新声明——提交时读到
// 的就是最新值。
type DirtyIDs struct {
	Nodes      []string
	Containers []int
}

func (d DirtyIDs) empty() bool { return len(d.Nodes) == 0 && len(d.Containers) == 0 }

// deferredIDs 是待落库标识的累加器：按主键去重。
type deferredIDs struct {
	nodes map[string]struct{}
	conts map[int]struct{}
}

func newDeferredIDs() deferredIDs {
	return deferredIDs{nodes: map[string]struct{}{}, conts: map[int]struct{}{}}
}

func (d *deferredIDs) rows() int { return len(d.nodes) + len(d.conts) }

func (d *deferredIDs) addNodes(ids []string) {
	for _, id := range ids {
		if id != "" {
			d.nodes[id] = struct{}{}
		}
	}
}

func (d *deferredIDs) addContainers(ids []int) {
	for _, id := range ids {
		d.conts[id] = struct{}{}
	}
}

// resolve 把标识解析成当前值。**必须在持有 AppConfigMu（读锁或写锁）时调用**：
// 否则读到的 cfg 与调用方认为的快照可能不一致。
//
// 已消失的行直接跳过：删除必须由删除方同步落库（本机制不支持延后删除），因此这里
// 取不到值时不对库做任何动作是正确的。
func (d *deferredIDs) resolve() DirtySet {
	var set DirtySet
	for id := range d.nodes {
		if i, ok := FindNodeIndexUnlocked(id); ok {
			set.Nodes = append(set.Nodes, AppConfig.Nodes[i])
		}
	}
	for id := range d.conts {
		if i, ok := findContainerIndexByIDUnlocked(id); ok {
			set.Containers = append(set.Containers, AppConfig.Containers[i])
		}
	}
	return set
}

func (set DirtySet) empty() bool {
	return len(set.Nodes) == 0 && len(set.RemovedNodes) == 0 &&
		len(set.Containers) == 0 && len(set.RemovedContainers) == 0
}

// deferredSaver 收集待落库标识，由后台 goroutine 按窗口提交。
type deferredSaver struct {
	mu      sync.Mutex
	pending deferredIDs
	started bool
	wake    chan struct{}
}

var deferredSaves = &deferredSaver{pending: newDeferredIDs(), wake: make(chan struct{}, 1)}

// add 合并一批声明。达到 deferredMaxRows 时唤醒后台立刻提交（背压）。
// 调用方必须持有 AppConfigMu：加锁顺序固定为 AppConfigMu → deferredSaves.mu。
func (d *deferredSaver) add(ids DirtyIDs) {
	if ids.empty() {
		return
	}
	d.mu.Lock()
	d.pending.addNodes(ids.Nodes)
	d.pending.addContainers(ids.Containers)
	full := d.pending.rows() >= deferredMaxRows
	d.mu.Unlock()
	if full {
		d.signal()
	}
}

func (d *deferredSaver) signal() {
	select {
	case d.wake <- struct{}{}:
	default: // 已有待处理的唤醒信号，不重复投递
	}
}

// take 取走当前待落库集合。调用方必须持有 AppConfigMu。
func (d *deferredSaver) take() deferredIDs {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.pending
	d.pending = newDeferredIDs()
	return cur
}

// mergeBack 在提交失败后把这一批并回待落库集合，保证不丢改动。
// 已经存在的新声明（更新）优先：同一行只保留一个标识，值在提交时重新读取，故
// 「谁赢」不影响最终结果。
func (d *deferredSaver) mergeBack(cur deferredIDs) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range cur.nodes {
		if _, ok := d.pending.nodes[id]; !ok {
			d.pending.nodes[id] = struct{}{}
		}
	}
	for id := range cur.conts {
		if _, ok := d.pending.conts[id]; !ok {
			d.pending.conts[id] = struct{}{}
		}
	}
}

// FlushDeferredSaves 立刻提交待落库改动；无待落库内容时不做任何事。
// 供后台窗口与进程退出前调用。
func FlushDeferredSaves() error {
	AppConfigMu.Lock()
	if AppConfig == nil {
		AppConfigMu.Unlock()
		return nil
	}
	batch := deferredSaves.take()
	if batch.rows() == 0 {
		AppConfigMu.Unlock()
		return nil
	}
	set := batch.resolve()
	var err error
	if !set.empty() {
		err = saveConfigToDBHinted(newExactDirtySet(set))
	}
	AppConfigMu.Unlock()
	if err != nil {
		deferredSaves.mergeBack(batch)
	}
	return err
}

// StartDeferredSaver 启动后台窗口：定期把累积的心跳改动合并成一个事务落库。
// 幂等，重复调用不产生第二个 goroutine。
func StartDeferredSaver() {
	deferredSaves.mu.Lock()
	if deferredSaves.started {
		deferredSaves.mu.Unlock()
		return
	}
	deferredSaves.started = true
	deferredSaves.mu.Unlock()

	go func() {
		ticker := time.NewTicker(deferredFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-deferredSaves.wake:
			}
			if err := FlushDeferredSaves(); err != nil {
				logSaveFailure(err, 1)
			}
		}
	}()
}
