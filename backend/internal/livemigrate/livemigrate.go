// Package livemigrate 实时迁移（P6-1）：
//
//   - 输入：实例 ID + 目标节点 + 存储类型（共享/本地）；
//   - 准备：fence 源节点 IO（暂停容器）+ 检查目标节点资源；
//   - 传输：共享存储（shared）→ 跳过数据迁移；本地存储（local）→
//     drbd/qemu-disks 双写 + 最终切换；调用方注入 TransferDriver；
//   - 切换：源节点 stop + 目标节点 start，迁移成功；
//   - 失败回滚：源节点继续运行 + 解 fence。
//
// 本包产出编排状态机；真实迁移原语由 storage.Backend + libvirt
// Driver 注入。
package livemigrate

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// StorageClass 存储类（决定是否需要传输磁盘）。
type StorageClass string

const (
	StorageShared StorageClass = "shared" // 共享存储（NFS / CephFS / RBD），无需迁移磁盘
	StorageLocal  StorageClass = "local"  // 本地存储（dir / ZFS / LVM），需要迁移磁盘
)

// Status 实时迁移状态。
type Status string

const (
	StatusPending    Status = "pending"
	StatusFencingSrc Status = "fencing_src"
	StatusTransfer   Status = "transfer"
	StatusSwitching  Status = "switching"
	StatusDone       Status = "done"
	StatusRolledBack Status = "rolled_back"
	StatusFailed     Status = "failed"
)

// Migration 单次迁移记录。
type Migration struct {
	ID            string    `json:"id"`
	InstanceID    int       `json:"instance_id"`
	SourceNode    string    `json:"source_node"`
	TargetNode    string    `json:"target_node"`
	Storage       StorageClass `json:"storage"`
	Status        Status    `json:"status"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	BytesMoved    int64     `json:"bytes_moved,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// TransferDriver 真实磁盘传输抽象（drbd/qemu-disks/NFS export 等）。
type TransferDriver interface {
	// Transfer 共享存储无需传输；本地存储返回迁移字节数。
	// mode=shared: 跳过；mode=local: drbd 同步/逐块拷贝。
	Transfer(instanceID int, sourceNode, targetNode string, storage StorageClass) (bytesMoved int64, err error)
}

// NodeController 节点启停抽象（libvirt / LXC lxc-*）。
type NodeController interface {
	FenceSource(node string, instanceID int) error
	UnfenceSource(node string, instanceID int) error
	StopOnSource(node string, instanceID int) error
	StartOnTarget(node string, instanceID int) error
}

// Engine 实时迁移编排器。
type Engine struct {
	mu        sync.Mutex
	driver    TransferDriver
	controller NodeController
	migs      map[string]*Migration
}

// NewEngine 创建引擎。
func NewEngine(driver TransferDriver, controller NodeController) *Engine {
	return &Engine{driver: driver, migs: map[string]*Migration{}, controller: controller}
}

// StartMigration 启动迁移；按状态机顺序推进，任一步失败回滚。
//
// 返回 Migration 永远非 nil（即便整体失败）；调用方持久化并写审计。
func (e *Engine) StartMigration(instanceID int, sourceNode, targetNode string, storage StorageClass) (*Migration, error) {
	if e.driver == nil || e.controller == nil {
		return nil, errors.New("livemigrate: nil driver/controller")
	}
	if instanceID <= 0 || sourceNode == "" || targetNode == "" {
		return nil, errors.New("livemigrate: instance_id/source/target required")
	}
	if sourceNode == targetNode {
		return nil, errors.New("livemigrate: source == target")
	}
	m := &Migration{
		ID:         fmt.Sprintf("mig-%d-%d", instanceID, time.Now().UnixNano()),
		InstanceID: instanceID,
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Storage:    storage,
		Status:     StatusPending,
		StartedAt:  time.Now(),
	}
	e.record(m)

	// 1. Fence 源节点 IO
	m.Status = StatusFencingSrc
	e.recordUpdate(m)
	if err := e.controller.FenceSource(sourceNode, instanceID); err != nil {
		m.Error = err.Error()
		m.Status = StatusFailed
		e.recordUpdate(m)
		return m, err
	}

	// 2. 传输（仅本地存储）
	m.Status = StatusTransfer
	e.recordUpdate(m)
	bytes, err := e.driver.Transfer(instanceID, sourceNode, targetNode, storage)
	if err != nil {
		m.Error = err.Error()
		m.Status = StatusRolledBack
		_ = e.controller.UnfenceSource(sourceNode, instanceID)
		m.Status = StatusFailed
		e.recordUpdate(m)
		return m, err
	}
	m.BytesMoved = bytes

	// 3. 切换：源节点 stop + 目标节点 start
	m.Status = StatusSwitching
	e.recordUpdate(m)
	if err := e.controller.StopOnSource(sourceNode, instanceID); err != nil {
		m.Error = "stop source: " + err.Error()
		_ = e.controller.UnfenceSource(sourceNode, instanceID)
		m.Status = StatusFailed
		e.recordUpdate(m)
		return m, err
	}
	if err := e.controller.StartOnTarget(targetNode, instanceID); err != nil {
		m.Error = "start target: " + err.Error()
		// 回滚：源节点重新启动 + 解 fence（最差情况：实例双停）
		_ = e.controller.UnfenceSource(sourceNode, instanceID)
		m.Status = StatusFailed
		e.recordUpdate(m)
		return m, err
	}

	// 4. 解 fence 源节点（实例已在目标运行，源节点停止）
	if err := e.controller.UnfenceSource(sourceNode, instanceID); err != nil {
		// 警告但不视为致命：实例已经在 target 启动。
		m.Error = "unfence: " + err.Error()
	}
	m.Status = StatusDone
	m.FinishedAt = time.Now()
	e.recordUpdate(m)
	return m, nil
}

// record / recordUpdate 写入 migs map。
func (e *Engine) record(m *Migration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.migs[m.ID] = m
}

func (e *Engine) recordUpdate(m *Migration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.migs[m.ID] = m
}

// Get 查询迁移记录。
func (e *Engine) Get(id string) (Migration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.migs[id]
	if !ok {
		return Migration{}, false
	}
	return *m, true
}

// ---- 测试桩 ----

// NoopTransferDriver 测试桩：Transfer 返回 0 字节 + nil。
type NoopTransferDriver struct {
	TransferErr error
}

// Transfer implements TransferDriver。
func (n *NoopTransferDriver) Transfer(_ int, _, _ string, _ StorageClass) (int64, error) {
	if n.TransferErr != nil {
		return 0, n.TransferErr
	}
	return 0, nil
}

// NoopNodeController 测试桩：所有节点操作 nil。
type NoopNodeController struct {
	FenceErr      error
	StopSourceErr error
	StartTargetErr error
}

// FenceSource implements NodeController。
func (n *NoopNodeController) FenceSource(string, int) error { return n.FenceErr }

// UnfenceSource implements NodeController。
func (n *NoopNodeController) UnfenceSource(string, int) error { return nil }

// StopOnSource implements NodeController。
func (n *NoopNodeController) StopOnSource(string, int) error { return n.StopSourceErr }

// StartOnTarget implements NodeController。
func (n *NoopNodeController) StartOnTarget(string, int) error { return n.StartTargetErr }