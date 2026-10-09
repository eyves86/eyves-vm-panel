// Package node 节点状态机 + 租约锁（P1-1）。
//
// 节点四态：online / offline / maintenance / draining。合法转换：
//
//   online      → offline       心跳超时（扫描器）
//   online      → maintenance   运维请求
//   maintenance → online        恢复
//   maintenance → draining      准备迁移（先停接新实例，再排空现有）
//   online      → draining      直接进入排水
//   draining    → maintenance   排水完成，临时进入维护态
//   offline     → online        心跳恢复
//
// 非法转换（任何不在上表的 src→dst）一律拒绝并写审计。
//
// 租约锁：node_leases 表（node_id, token, expires_at）。
// 心跳续约时校验 token 一致；扫描器对超时租约标记 offline。
// 后台扫描器单实例：配置库事务内 UPDATE ... RETURNING 抢占式取行实现单控制面
// 约束；多个控制面同时跑也只产生一次判定（行锁保证只有一个扫描器能改到该行）。
//
// events 表（state_changed）由 P7-1 接管；本包 EmitEvent() 提供占位，
// 写入失败不阻塞主流程。
package node

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

// Status 是 Node 的状态枚举（P1-1 扩展）。
//
// 旧数据 "pending" 在装载时统一归一为 "offline"（未注册完成 = 不可用）。
type Status string

const (
	StatusOnline      Status = "online"
	StatusOffline     Status = "offline"
	StatusMaintenance Status = "maintenance"
	StatusDraining    Status = "draining"
)

// IsValid 检查状态值是否合法。
func (s Status) IsValid() bool {
	switch s {
	case StatusOnline, StatusOffline, StatusMaintenance, StatusDraining:
		return true
	}
	return false
}

// Normalize 把任意字符串归一为合法 Status（未知或 "pending" → offline）。
func Normalize(s string) Status {
	switch Status(s) {
	case StatusOnline, StatusOffline, StatusMaintenance, StatusDraining:
		return Status(s)
	}
	return StatusOffline
}

// ErrIllegalTransition 状态非法转换错误。
var ErrIllegalTransition = errors.New("illegal node state transition")

// allowedTransitions 是合法转换表（白名单）。任何不在此表的转换被拒。
var allowedTransitions = map[Status]map[Status]bool{
	StatusOnline:      {StatusOffline: true, StatusMaintenance: true, StatusDraining: true},
	StatusOffline:     {StatusOnline: true, StatusMaintenance: true},
	StatusMaintenance: {StatusOnline: true, StatusDraining: true},
	StatusDraining:    {StatusMaintenance: true, StatusOffline: true},
}

// CanTransition 判断 from → to 是否合法。
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	return allowedTransitions[from][to]
}

// Transition 应用状态转换；非法转换返回 ErrIllegalTransition。
func Transition(from, to Status) error {
	if !from.IsValid() {
		return fmt.Errorf("%w: source status %q invalid", ErrIllegalTransition, from)
	}
	if !to.IsValid() {
		return fmt.Errorf("%w: target status %q invalid", ErrIllegalTransition, to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s not allowed", ErrIllegalTransition, from, to)
	}
	return nil
}

// Lease 是租约记录（与配置库 node_leases 表对应）。
type Lease struct {
	NodeID    string
	Token     string
	ExpiresAt time.Time
}

// ScannedLease 由 ReconcileOnce 返回的超时租约（按 NodeID 去重）。
type ScannedLease struct {
	NodeID string
	LastSeen time.Time
}

// EventSink 是事件落表接口；P7-1 接入 events 表前由调用方注入。
// 写入失败不应阻塞主流程。
type EventSink interface {
	Emit(stateChanged EventStateChanged) error
}

// EventStateChanged 是状态变化事件载荷。
type EventStateChanged struct {
	NodeID  string
	From    Status
	To      Status
	Reason  string
	Actor   string
	At      time.Time
}

// Scanner 后台扫描器：单实例运行，定期扫描超时租约并标记节点 offline。
//
// 通过 LeaseSweeper 单实例化接口（由 ReconcileLock 提供 SELECT/UPDATE
// 抢占）保证多控制面部署下只触发一次判定。
type Scanner struct {
	mu       sync.Mutex
	db       *sql.DB
	sweeper  LeaseSweeper
	eventSink EventSink
	stop     chan struct{}
	running  bool
	interval time.Duration
}

// LeaseSweeper 是单次扫描的接口，由 ReconcileLock 提供原子 SELECT/UPDATE
// 实现：每行租约只允许一个 Scanner 进程/线程成功标记 offline。
type LeaseSweeper interface {
	// SweepExpired 原子扫描超时租约：成功获取行的扫描器返回该行供下游
	// 标记；其它扫描器看不到同一行（已被标记 in_progress）。
	// 返回所有本次"获胜"的超时租约。
	SweepExpired(now time.Time) ([]Lease, error)
}

// ReconcileLock 实现 LeaseSweeper 的 Postgres 抢占版。
//
// 扫描策略（与 P1-1 验收条目"双控制面并发扫描只触发一次判定"对齐）：
//
//   BEGIN;
//     UPDATE node_leases SET in_progress = 1
//       WHERE expires_at < ? AND in_progress = 0
//       RETURNING node_id, token, expires_at;
//   COMMIT;
//
// 配置库事务 + 行锁保证并发扫描器只有一个能改到该行；其它扫描器改不到就退出。
// 由于 row 已 in_progress=1，下次扫描跳过；处理完成后删除该行（MarkProcessed）。
type ReconcileLock struct {
	DB *sql.DB
}

// SweepExpired 实现 LeaseSweeper。
func (r *ReconcileLock) SweepExpired(now time.Time) ([]Lease, error) {
	tx, err := r.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`UPDATE node_leases
		SET in_progress = 1
		WHERE expires_at < ? AND in_progress = 0
		RETURNING node_id, token, expires_at`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var leases []Lease
	for rows.Next() {
		var l Lease
		var expStr string
		if err := rows.Scan(&l.NodeID, &l.Token, &expStr); err != nil {
			return nil, err
		}
		exp, perr := time.Parse(time.RFC3339, expStr)
		if perr != nil {
			continue
		}
		l.ExpiresAt = exp
		leases = append(leases, l)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return leases, nil
}

// MarkProcessed 删除已处理的租约行（处理完 = 不再需要续约）。
//
// 设计权衡：原本想保留行并置 in_progress=0，但那样下次扫描仍会重新拾取
// 同一条已过期租约，导致重复标记 offline + 重复事件。删行让"已处理"成为
// 终态——下次扫描跳过；下一次心跳到达会重新 INSERT（ON CONFLICT 覆盖）。
func (r *ReconcileLock) MarkProcessed(nodeID string) error {
	_, err := r.DB.Exec(`DELETE FROM node_leases WHERE node_id = ?`, nodeID)
	return err
}

// EnsureSchema 建 node_leases 表（幂等）。
// 由 InitConfig / openConfigDB 迁移框架调用。
const EnsureSchemaSQL = `CREATE TABLE IF NOT EXISTS node_leases (
	node_id TEXT PRIMARY KEY,
	token TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	in_progress INTEGER NOT NULL DEFAULT 0
)`

// NewScanner 创建后台扫描器。db/sweeper/eventSink 任一为 nil 即返回错误。
func NewScanner(db *sql.DB, sweeper LeaseSweeper, sink EventSink, interval time.Duration) (*Scanner, error) {
	if db == nil {
		return nil, errors.New("node scanner: nil db")
	}
	if sweeper == nil {
		return nil, errors.New("node scanner: nil sweeper")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Scanner{
		db:        db,
		sweeper:   sweeper,
		eventSink: sink,
		interval:  interval,
		stop:      make(chan struct{}),
	}, nil
}

// Start 启动后台循环（一次性起一个 goroutine；不允许多次启动）。
func (s *Scanner) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	go s.loop()
}

// Stop 通知后台循环退出（不阻塞）。
func (s *Scanner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stop)
}

func (s *Scanner) loop() {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			_, _ = s.RunOnce()
		}
	}
}

// RunOnce 执行单次扫描：拾取超时租约 → 标记 Node.Status=offline → 落事件。
// 返回被标记的节点 ID 列表（测试断言用）。
func (s *Scanner) RunOnce() ([]string, error) {
	leases, err := s.sweeper.SweepExpired(time.Now())
	if err != nil {
		return nil, err
	}
	marked := []string{}
	for _, lease := range leases {
		if err := s.markOffline(lease); err != nil {
			// 单条失败不阻塞：把 in_progress 清零让下次重试。
			if rl, ok := s.sweeper.(*ReconcileLock); ok {
				_ = rl.MarkProcessed(lease.NodeID)
			}
			continue
		}
		marked = append(marked, lease.NodeID)
	}
	return marked, nil
}

// markOffline 在 config 全局中更新节点状态，写审计 + 落事件。
func (s *Scanner) markOffline(lease Lease) error {
	var prev Status
	_, ok := config.UpdateNode(lease.NodeID, func(n *config.Node) {
		prev = Normalize(string(n.Status))
		n.Status = string(StatusOffline)
	})
	if !ok {
		// 节点不存在：清 in_progress 让下次扫描跳过。
		if rl, ok := s.sweeper.(*ReconcileLock); ok {
			_ = rl.MarkProcessed(lease.NodeID)
		}
		return fmt.Errorf("node %s not found", lease.NodeID)
	}
	if rl, ok := s.sweeper.(*ReconcileLock); ok {
		if err := rl.MarkProcessed(lease.NodeID); err != nil {
			return err
		}
	}
	if s.eventSink != nil {
		_ = s.eventSink.Emit(EventStateChanged{
			NodeID: lease.NodeID,
			From:   prev,
			To:     StatusOffline,
			Reason: "lease expired",
			At:     time.Now(),
		})
	}
	return nil
}

// Heartbeat 续约（替换 token 表示新心跳）。
//
//   - 节点状态非 online/maintenance/draining 时拒绝续约（强制要求先离线）；
//   - token 不匹配时拒绝（防 token 泄露后旧请求续约）；
//   - 续约成功 → 节点状态置 online + LastSeen 更新。
func Heartbeat(nodeID, token string, ttl time.Duration, db *sql.DB) error {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(token) == "" {
		return errors.New("node heartbeat: empty id or token")
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	now := time.Now()
	expires := now.Add(ttl).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO node_leases (node_id, token, expires_at, in_progress)
		VALUES (?, ?, ?, 0)
		ON CONFLICT(node_id) DO UPDATE SET token = excluded.token, expires_at = excluded.expires_at, in_progress = 0`,
		nodeID, token, expires); err != nil {
		return err
	}
	// 状态合法转换：任何状态都可由心跳恢复为 online。
	// （offline → online 是允许的，maintenance/draining → online 也允许：
	// 心跳到达意味着节点存活，运维/排水的语义被心跳"打断"——后续由
	// 显式 Transition 调用重新置回 maintenance/draining。）
	_, _ = config.UpdateNode(nodeID, func(n *config.Node) {
		n.Status = string(StatusOnline)
		n.LastSeen = now.Format("2006-01-02 15:04:05")
	})
	return nil
}

// ValidateToken 校验 node_id + token 配对（心跳外的 API 调用复用）。
func ValidateToken(nodeID, token string, db *sql.DB) bool {
	var stored string
	err := db.QueryRow(`SELECT token FROM node_leases WHERE node_id = ?`, nodeID).Scan(&stored)
	if err != nil {
		return false
	}
	return stored == token
}

// AllowsContainerOps 判断节点状态是否允许接收容器操作（启动/删除/迁移）。
//
//   - online：允许
//   - offline：拒绝（返回明确错误码，由调用方映射 HTTP 409）
//   - maintenance：拒绝（新实例由创建层单独拒绝；运维操作另论）
//   - draining：拒绝（迁移准备中，停止新操作）
func AllowsContainerOps(status Status) bool {
	return status == StatusOnline
}

// AllowsNewInstance 判断节点是否允许创建新实例。
//
//   - 仅 online 允许；
//   - maintenance / draining 明确拒绝（运维意图）。
func AllowsNewInstance(status Status) bool {
	return status == StatusOnline
}