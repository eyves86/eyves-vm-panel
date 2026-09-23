// Package agentproto Agent v2 协议数据结构与调度（P1-3）。
//
// 协议目标：
//   - 能力上报（注册时上报驱动 / 网络 / 容量 / 版本，控制面存 Node 上）；
//   - 指令通道（结构化指令 + 序号 + 幂等执行回执，不再 shell 拼接）；
//   - 断线重连（增量序号对齐，租约立即续期）。
//
// 本包只定义数据结构与不依赖 HTTP 的核心逻辑；HTTP 层由 internal/api/agent_api.go
// 升级接入（保留旧心跳兼容）。
package agentproto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion v2 协议版本号（旧 v1 心跳仍兼容并存）。
const ProtocolVersion = "v2"

// Capability 是 Agent 注册时上报的能力清单。
//
// Drivers：驱动可用性（如 ["dir", "zfs", "lvm", "rbd", "nfs"]）；
// 驱动未实现时由 Agent 在注册前探测二进制，跳过上报。
type Capability struct {
	Drivers   []string `json:"drivers"`
	Networks  []string `json:"networks"` // 已配置的网络名（如 ["nat4", "public-ipv4", "lan-dhcp"]）
	Arch      string   `json:"arch"`
	OSName    string   `json:"os_name"`
	OSVersion string   `json:"os_version,omitempty"`
	// CPUCount / RAMTotalMB / DiskTotalGB 冗余上报（主控已有节点容量字段，
	// 这里用于 Agent 端自报校验，避免漂移）。
	CPUCount    int     `json:"cpu_count"`
	RAMTotalMB  int64   `json:"ram_total_mb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
}

// RegistrationRequest 是 Agent 上报的注册/心跳统一载荷（v2 协议）。
type RegistrationRequest struct {
	Protocol string    `json:"protocol"` // "v2"
	NodeID   string    `json:"node_id"`
	Token    string    `json:"token"`
	Name     string    `json:"name"`
	Address  string    `json:"address"`
	Version  string    `json:"version"` // agent 二进制版本
	Capability Capability `json:"capability"`
	// LastCommandID 是 Agent 已确认执行的最后指令序号（用于重连补拉）：
	// 控制面回放 LastCommandID+1 起的所有未完成指令。
	LastCommandID int64 `json:"last_command_id"`
}

// RegistrationResponse 是控制面对注册请求的应答。
type RegistrationResponse struct {
	Accepted      bool   `json:"accepted"`
	Reason        string `json:"reason,omitempty"`
	TokenRotated  string `json:"token_rotated,omitempty"` // 控制面下发的新 token；Agent 收下后下次心跳使用
	NextHeartbeat time.Duration `json:"next_heartbeat"`
}

// CommandType 是控制面下发的指令类型（按枚举扩展）。
type CommandType string

const (
	CmdContainerStart   CommandType = "container.start"
	CmdContainerStop    CommandType = "container.stop"
	CmdContainerRestart CommandType = "container.restart"
	CmdContainerDestroy CommandType = "container.destroy"
	CmdContainerCreate  CommandType = "container.create"
	CmdNoop             CommandType = "noop" // 用于序号对齐的占位指令
)

// Command 是控制面下发的单条指令。
type Command struct {
	ID      int64       `json:"id"`
	Type    CommandType `json:"type"`
	Target  string      `json:"target,omitempty"` // 容器 ID 等
	Payload json.RawMessage `json:"payload,omitempty"`
	// IdempotencyKey 由 Agent 计算（hash(id+type+target+payload)），Agent 据此
	// 去重：同一 IdempotencyKey 已完成的不再执行。
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// CommandReceipt 是 Agent 对单条指令的回执。
type CommandReceipt struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`           // "completed" / "failed" / "in_progress"
	Progress int   `json:"progress"`          // 0-100（in_progress 时有意义）
	Error    string `json:"error,omitempty"` // failed 时非空
	ErrorCode int   `json:"error_code,omitempty"`
}

// PollCommandsRequest 是 Agent 拉取指令的请求（拉模式）。
type PollCommandsRequest struct {
	NodeID string `json:"node_id"`
	AfterCommandID int64 `json:"after_command_id"`
}

// PollCommandsResponse 是拉模式的应答（Agent 拿到 after 之后的所有 pending 命令）。
type PollCommandsResponse struct {
	Commands []Command `json:"commands"`
	// LatestID：Agent 已知的最新序号；用于 Agent 本地 cursor 推进。
	LatestID int64 `json:"latest_id"`
}

// ErrUnsupportedProtocol v1 协议已被识别但本代码路径只支持 v2。
var ErrUnsupportedProtocol = errors.New("agentproto: only v2 protocol accepted")

// ComputeIdempotencyKey 计算幂等键：稳定 hash，相同 id+type+target+payload 必相同。
// Agent 收到指令后用此 key 去重（同 key 已 completed 不再执行）。
func ComputeIdempotencyKey(cmd Command) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d|%s|%s|", cmd.ID, cmd.Type, cmd.Target)))
	h.Write([]byte(cmd.Payload))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Channel 是控制面→Agent 的指令通道（拉模式）。
//
// 控制面与 Agent 共享 Channel 实例：Enqueue 写入待发指令，Poll 返回 after ID 之后
// 的所有 pending 指令；Agent 提交回执后 MarkCompleted 清理。
//
// 持久化由 SQLite 层完成（agent_commands 表），本结构只提供 in-memory 抽象，
// 接口签名稳定便于后续接 SQLite。
type Channel struct {
	mu       sync.Mutex
	nextID   int64
	pending  map[int64]Command
	complete map[string]bool // idempotency key -> done
}

// NewChannel 创建空通道。
func NewChannel() *Channel {
	return &Channel{
		nextID:   1,
		pending:  make(map[int64]Command),
		complete: make(map[string]bool),
	}
}

// Enqueue 写入一条新指令，返回分配的指令 ID（单调递增）。
func (c *Channel) Enqueue(typ CommandType, target string, payload []byte) Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	cmd := Command{
		ID:      id,
		Type:    typ,
		Target:  target,
		Payload: payload,
		IdempotencyKey: ComputeIdempotencyKey(Command{
			ID:      id,
			Type:    typ,
			Target:  target,
			Payload: payload,
		}),
	}
	c.pending[id] = cmd
	return cmd
}

// Poll 返回 afterID 之后的所有 pending 指令（按 ID 升序）。
//
// 已 completed 的指令不再返回（即使 ID 更大），由 Agent 根据 LastCommandID
// 单调推进 cursor；这是 P1-3 验收"重连补拉无丢失无重复"的依据。
func (c *Channel) Poll(afterID int64) PollCommandsResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]int64, 0, len(c.pending))
	for id := range c.pending {
		if id > afterID {
			ids = append(ids, id)
		}
	}
	// 按 ID 升序：保证调用方按顺序处理；map 迭代顺序 Go 不保证。
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	out := PollCommandsResponse{}
	for _, id := range ids {
		out.Commands = append(out.Commands, c.pending[id])
		if id > out.LatestID {
			out.LatestID = id
		}
	}
	if out.LatestID == 0 {
		out.LatestID = afterID
	}
	return out
}

// MarkCompleted 标记指令执行完成；后续 Poll 不再返回（Agent 本地按 ID cursor 推进）。
func (c *Channel) MarkCompleted(id int64, idempotencyKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
	if idempotencyKey != "" {
		c.complete[idempotencyKey] = true
	}
}

// IsCompleted 检查幂等键是否已被执行（用于 Agent 重发去重）。
func (c *Channel) IsCompleted(idempotencyKey string) bool {
	if idempotencyKey == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.complete[idempotencyKey]
}

// NextID 返回下一条将分配的指令 ID（不消耗）。
func (c *Channel) NextID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextID
}

// ParseRegistrationRequest 反序列化并校验载荷。
func ParseRegistrationRequest(raw []byte) (RegistrationRequest, error) {
	var r RegistrationRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if strings.TrimSpace(r.NodeID) == "" {
		return r, errors.New("agentproto: missing node_id")
	}
	if strings.TrimSpace(r.Token) == "" {
		return r, errors.New("agentproto: missing token")
	}
	if r.Protocol != "" && r.Protocol != ProtocolVersion {
		return r, ErrUnsupportedProtocol
	}
	return r, nil
}

// FormatRegistrationResponse 序列化注册应答。
func FormatRegistrationResponse(r RegistrationResponse) ([]byte, error) {
	return json.Marshal(r)
}

// FormatCommandReceipt 序列化单条指令回执（多回执由调用方打包数组）。
func FormatCommandReceipt(r CommandReceipt) ([]byte, error) {
	return json.Marshal(r)
}

// Now 抽象时间源（测试可注入）。
var Now = func() time.Time { return time.Now() }