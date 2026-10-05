package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"eyvescloud/internal/secgroup"
	"eyvescloud/internal/storage"
	"eyvescloud/internal/version"
)

// PortMapping represents a port mapping rule
type PortMapping struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	HostIP        string `json:"host_ip,omitempty"`
	Protocol      string `json:"protocol"`
	Description   string `json:"description"`
}

type FirewallRule struct {
	ID          string `json:"id"`
	Network     string `json:"network,omitempty"` // "ipv4", "ipv6", or "all"; empty defaults to "ipv4"
	Direction   string `json:"direction"`         // "in" or "out"
	Protocol    string `json:"protocol"`          // "tcp", "udp", "icmp", "all"
	Port        string `json:"port"`              // "" = all, "22", "80,443", "8000-9000"
	SourceIP    string `json:"source_ip"`         // "" = any
	Action      string `json:"action"`            // "ACCEPT" or "DROP"
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type PublicIPv4Assignment struct {
	Address   string `json:"address"`
	Interface string `json:"interface,omitempty"`
	PrefixLen int    `json:"prefix_len,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	// RDNS 用户自助设置的反向 DNS（PTR）主机名。仅记录期望值，实际 PTR 由上游
	// DNS / 机房侧按该字段生效；留空表示未设置。
	RDNS string `json:"rdns,omitempty"`
}

type IPv6Assignment struct {
	Address   string `json:"address"`
	PrefixLen int    `json:"prefix_len"`
	Interface string `json:"interface,omitempty"`
	// RDNS 同 PublicIPv4Assignment.RDNS，用于 IPv6 地址的反向解析记录。
	RDNS string `json:"rdns,omitempty"`
}

type PublicIPv6Prefix struct {
	Address   string `json:"address"`
	Prefix    string `json:"prefix,omitempty"`
	PrefixLen int    `json:"prefix_len"`
	Interface string `json:"interface,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
}

// SavedTask for persisting task queue across restarts
type SavedTask struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	CreatedAt     string `json:"created_at"`
	TemplateID    string `json:"template_id,omitempty"`
	Config        string `json:"config,omitempty"`
	User          string `json:"user,omitempty"`
	IP            string `json:"ip,omitempty"`
	UserAgent     string `json:"user_agent,omitempty"`
}

// SavedLoginLog for persisting login logs
type SavedLoginLog struct {
	Time      string `json:"time"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Success   bool   `json:"success"`
}

// AuditLog represents an operation log entry
type AuditLog struct {
	Time      string `json:"time"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	User      string `json:"user"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	Error     string `json:"error,omitempty"`
	// PrevHash / Hash 哈希链字段（P8-3）：PrevHash 等于上一条 AuditLog 的 Hash；
	// 首条 PrevHash 为空。任一记录被改写 Verify 即可定位。
	PrevHash string `json:"prev_hash,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

type VMReadinessCheck struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Container represents an LXC container configuration
type Container struct {
	ID     int    `json:"id"`
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	NodeID string `json:"node_id,omitempty"` // 所在节点 ID；空 = 主控本机（向后兼容）
	// NodeLocalID 是实例在被控节点上的本地 ID（代理调用 /api/agent/containers/{该值}/...）。
	// 与主控侧 ID（c.ID，全局唯一）分离：节点本地 ID 可能与本机容器撞号。
	NodeLocalID    int     `json:"node_local_id,omitempty"`
	Virtualization string  `json:"virtualization,omitempty"`
	LXCName        string  `json:"lxc_name,omitempty"`
	KVMName        string  `json:"kvm_name,omitempty"`
	DiskImage      string  `json:"disk_image,omitempty"`
	StoragePoolID  string  `json:"storage_pool_id,omitempty"`
	StoragePath    string  `json:"storage_path,omitempty"`
	MACAddress     string  `json:"mac_address,omitempty"`
	Template       string  `json:"template"`
	VCPU           float64 `json:"vcpu"`
	// CPUPercent 是 CPU 使用率上限（1-100，0=不额外限制；cgroup cpu.max 口径）。
	// v2 PATCH 与创建接口均暴露 cpu_percent（修复"字段被 if false{} 静默丢弃"的契约 bug）。
	CPUPercent        int     `json:"cpu_percent,omitempty"`
	RAMMB             int     `json:"ram_mb"`
	DiskGB            float64 `json:"disk_gb"`
	DataDiskGB        float64 `json:"data_disk_gb,omitempty"`
	DataDiskMountPath string  `json:"data_disk_mount_path,omitempty"`
	NetworkBWMbps     int     `json:"network_bw_mbps"`
	NetworkDownMbps   int     `json:"network_down_mbps"`
	NetworkUpMbps     int     `json:"network_up_mbps"`
	MonthlyTrafficGB  int     `json:"monthly_traffic_gb"`
	TrafficMode       string  `json:"traffic_mode"`   // "total" or "in_out"
	TrafficInGB       int     `json:"traffic_in_gb"`  // 0 = unlimited
	TrafficOutGB      int     `json:"traffic_out_gb"` // 0 = unlimited
	TrafficUsedRX     int64   `json:"traffic_used_rx"`
	TrafficUsedTX     int64   `json:"traffic_used_tx"`
	TrafficResetDate  string  `json:"traffic_reset_date"`
	IOSpeedMBps       int     `json:"io_speed_mbps"`
	IOReadMBps        int     `json:"io_read_mbps"`
	IOWriteMBps       int     `json:"io_write_mbps"`
	Status            string  `json:"status"`
	// Suspended 表示容器被财务/管理员挂起（欠费停机语义）：
	// 运行中的容器会被强制停机，且 start/restart/reinstall 与
	// WebSSH/WebVNC 访问全部被拒绝，直到 unsuspend。
	Suspended         bool                   `json:"suspended,omitempty"`
	SuspendedAt       string                 `json:"suspended_at,omitempty"`
	SuspendedReason   string                 `json:"suspended_reason,omitempty"`
	RestoreOnHostBoot bool                   `json:"restore_on_host_boot,omitempty"`
	IP                string                 `json:"ip"`
	LANIPv4Mode       string                 `json:"lan_ipv4_mode,omitempty"`
	LANInterface      string                 `json:"lan_interface,omitempty"`
	LANIPv4Address    string                 `json:"lan_ipv4_address,omitempty"`
	LANIPv4PrefixLen  int                    `json:"lan_ipv4_prefix_len,omitempty"`
	LANIPv4Gateway    string                 `json:"lan_ipv4_gateway,omitempty"`
	PublicIPv4s       []PublicIPv4Assignment `json:"public_ipv4s,omitempty"`
	IPv6              string                 `json:"ipv6"`
	IPv6PrefixLen     int                    `json:"ipv6_prefix_len"`
	IPv6Interface     string                 `json:"ipv6_interface"`
	IPv6Addresses     []IPv6Assignment       `json:"ipv6_addresses,omitempty"`
	VNCPort           int                    `json:"vnc_port"`
	VNCPassword       string                 `json:"vnc_password,omitempty"`
	SSHPort           int                    `json:"ssh_port"`
	SSHPassword       string                 `json:"ssh_password"`
	SSHHostKey        string                 `json:"ssh_host_key,omitempty"`
	// HVM 设置（KVM 容器）：启动盘顺序 / 网卡驱动 / VNC 键位 / 硬件加速 / TUN PPP
	HVMBootOrder                  string         `json:"hvm_boot_order,omitempty"`
	HVMNicDriver                  string         `json:"hvm_nic_driver,omitempty"`
	HVMVNCKeyMap                  string         `json:"hvm_vnc_keymap,omitempty"`
	HVMAcceleration               string         `json:"hvm_acceleration,omitempty"`
	HVMEnableTuntap               bool           `json:"hvm_enable_tuntap,omitempty"`
	HVMEnablePPP                  bool           `json:"hvm_enable_ppp,omitempty"`
	PortMappings                  []PortMapping  `json:"port_mappings"`
	PortMappingLimit              int            `json:"port_mapping_limit"`
	FirewallEnabled               bool           `json:"firewall_enabled"`
	FirewallDefaultAction         string         `json:"firewall_default_action"`
	FirewallRules                 []FirewallRule `json:"firewall_rules"`
	AllowedImageIDs               []string       `json:"allowed_image_ids,omitempty"`
	ImageLimitConfigured          bool           `json:"image_limit_configured,omitempty"`
	Tenant                        string         `json:"tenant,omitempty"`
	SnapshotLimit                 int            `json:"snapshot_limit"`
	CreatedAt                     string         `json:"created_at"`
	ExpiresAt                     string         `json:"expires_at"`
	SnapshotScheduleEnabled       bool           `json:"snapshot_schedule_enabled"`
	SnapshotScheduleIntervalHours int            `json:"snapshot_schedule_interval_hours"`
	SnapshotScheduleTime          string         `json:"snapshot_schedule_time"`
	SnapshotScheduleLastRun       string         `json:"snapshot_schedule_last_run"`
	SnapshotScheduleNextRun       string         `json:"snapshot_schedule_next_run"`
	SnapshotScheduleCreatedBy     string         `json:"snapshot_schedule_created_by"`
	PolicyBlocked                 bool           `json:"policy_blocked"`
	PolicyBlockedReason           string         `json:"policy_blocked_reason,omitempty"`
	PolicyBlockedAt               string         `json:"policy_blocked_at,omitempty"`
	OwnerSubUserID                string         `json:"owner_sub_user_id,omitempty"`
	// OwnerUsername 是列表响应中的派生展示字段（由 OwnerSubUserID 解析得到），
	// 供前端免拉取全量子用户即可显示属主名；不参与落库（omitempty，且仅写响应副本）。
	OwnerUsername string `json:"owner_username,omitempty"`
	// RecycledAt 非空 = 实例在回收站（软删除，同类商业面板/同类商业面板同款能力）：
	// 列表/统计默认排除，数据面不动；恢复=清标记，彻底删除（purge）才真销毁。
	RecycledAt        string `json:"recycled_at,omitempty"`
	CloudInitUserData string `json:"cloud_init_user_data,omitempty"`
	RescueEnabled     bool   `json:"rescue_enabled,omitempty"`  // 是否处于救援模式（KVM 从救援 ISO 引导）
	RescueISOID       string `json:"rescue_iso_id,omitempty"`   // 当前使用的救援 ISO 目录条目 ID
	RescueISOPath     string `json:"rescue_iso_path,omitempty"` // 救援 ISO 的本地绝对路径
	// OptionalISOID/OptionalISOPath: 通过单独 AttachISO 挂到 KVM CD-ROM (sdb) 的 ISO。
	// 与 Rescue 模式两条独立路径——Rescue 会 redefine + reboot，而 AttachISO 只是
	// 加 CD-ROM 设备，不改变启动盘顺序（Windows 安装/LiveCD 工具）。
	// DetachISO 会清空这两个字段；ExitRescue 不清空（可能同时有 rescue ISO + 可选 ISO）。
	OptionalISOID   string `json:"optional_iso_id,omitempty"`
	OptionalISOPath string `json:"optional_iso_path,omitempty"`
	// Remark 是实例备注（运维标注，供列表检索）；Locked 锁定后禁止删除/重装等
	// 破坏性操作（企业面板通用属性，主流面板/同类面板 均提供）。
	Remark string `json:"remark,omitempty"`
	Locked bool   `json:"locked,omitempty"`
	// RootVolumeID 根卷 ID（P0-1 存储抽象层）：新容器在 dir 后端池上创建的根目录卷。
	// 为空表示旧数据直连路径模式（沿用 StoragePath，读路径完全向后兼容，不做迁移）。
	RootVolumeID string `json:"root_volume_id,omitempty"`
	// DataVolumeIDs 数据卷 ID 列表（P0-1 仅落库记录，挂载流程在后续阶段接入）。
	DataVolumeIDs []string `json:"data_volume_ids,omitempty"`
	// SSHKeyIDs 绑定到该容器的 SSH 公钥 ID 列表——容器启动后公钥注入 /root/.ssh/authorized_keys
	// （Cloud-Init 或 LXC/KVM 模板预注入）。子用户也可在模板中创建容器时指定。
	SSHKeyIDs   []string `json:"ssh_key_ids,omitempty"`
	SecGroupIDs []string `json:"sec_group_ids,omitempty"`
	// Tags 资源标签（企业成本分摊 / 按标签过滤，类比 AWS EC2 Tags）。
	// key/value 均 ≤128 字符，最多 20 个；克隆时继承，删除容器时随之消亡。
	Tags map[string]string `json:"tags,omitempty"`
}

// SSHKey 是平台托管的 SSH 公钥账户（类比 GitHub SSH Key）。
// 用户创建公钥后可在创建容器时绑定，容器开机后公钥自动注入 /root/.ssh/authorized_keys。
type SSHKey struct {
	ID          string `json:"id"`          // "sk-" 前缀
	Name        string `json:"name"`        // 用户可识别的标签（如 "my-laptop"）
	PublicKey   string `json:"public_key"`  // 完整 OpenSSH 公钥行
	Fingerprint string `json:"fingerprint"` // SHA256 指纹（服务端计算，防篡改）
	Type        string `json:"type"`        // "admin" 或 subuser username
	OwnerID     string `json:"owner_id"`    // subuser ID（admin 时为空）
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
}

// Recipe 是用户预定义的 bash 脚本模板（类比 主流面板 Recipes）。
// Admin 创建的 recipe 可以标记 scope="shared"，所有 subuser 可见。
// Subuser 创建的 recipe 默认 scope="private"，仅自己可见。
type Recipe struct {
	ID          string `json:"id"`          // "recipe-" 前缀
	Name        string `json:"name"`        // 用户可识别的名称
	Description string `json:"description"` // 可选说明
	Script      string `json:"script"`      // bash 脚本正文
	OwnerID     string `json:"owner_id"`    // subuser ID 或 "admin"
	OwnerType   string `json:"owner_type"`  // "admin" 或 "subuser"
	Scope       string `json:"scope"`       // "private" 或 "shared"
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

const (
	VirtualizationLXC = "lxc"
	VirtualizationKVM = "kvm"

	LANIPv4ModeDHCP   = "dhcp"
	LANIPv4ModeStatic = "static"
)

func NormalizeVirtualization(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VirtualizationKVM:
		return VirtualizationKVM
	default:
		return VirtualizationLXC
	}
}

func (c *Container) Runtime() string {
	return NormalizeVirtualization(c.Virtualization)
}

func (c *Container) IsKVM() bool {
	return c.Runtime() == VirtualizationKVM
}

func (c *Container) UsesLANDHCP() bool {
	return strings.EqualFold(strings.TrimSpace(c.LANIPv4Mode), LANIPv4ModeDHCP)
}

func (c *Container) UsesLANStaticIPv4() bool {
	return strings.EqualFold(strings.TrimSpace(c.LANIPv4Mode), LANIPv4ModeStatic)
}

func (c *Container) UsesLANIPv4() bool {
	return c.UsesLANDHCP() || c.UsesLANStaticIPv4()
}

func normalizeStoragePools() bool {
	if AppConfig == nil {
		return false
	}
	changed := false
	result := make([]StoragePool, 0, len(AppConfig.StoragePools))
	seen := map[string]bool{}
	defaultSeen := map[string]bool{}
	for _, pool := range AppConfig.StoragePools {
		pool.ID = strings.TrimSpace(pool.ID)
		pool.Name = strings.TrimSpace(pool.Name)
		pool.Path = filepath.Clean(strings.TrimSpace(pool.Path))
		pool.MountPoint = filepath.Clean(strings.TrimSpace(pool.MountPoint))
		if pool.MountPoint == "." {
			pool.MountPoint = ""
		}
		if pool.MountPoint != "" {
			managedPath := managedStoragePoolPath(pool.MountPoint)
			if pool.Path != managedPath {
				pool.Path = managedPath
				changed = true
			}
		}
		if pool.ID == "" {
			pool.ID = storagePoolIDFromName(pool.Name, pool.Path)
			changed = true
		}
		if pool.Name == "" {
			pool.Name = pool.ID
			changed = true
		}
		if pool.Path == "." || !filepath.IsAbs(pool.Path) || seen[pool.ID] {
			changed = true
			continue
		}
		seen[pool.ID] = true
		pool.ContentTypes = normalizeStorageContentTypes(pool.ContentTypes)
		pool.DefaultContents = normalizeStorageContentTypes(pool.DefaultContents)
		allowed := map[string]bool{}
		for _, content := range pool.ContentTypes {
			allowed[content] = true
		}
		defaults := make([]string, 0, len(pool.DefaultContents))
		for _, content := range pool.DefaultContents {
			if !allowed[content] || defaultSeen[content] {
				changed = true
				continue
			}
			defaultSeen[content] = true
			defaults = append(defaults, content)
		}
		pool.DefaultContents = defaults
		if pool.ContentTypes == nil {
			pool.ContentTypes = []string{}
		}
		// P0-1：旧池数据自动补 Backend=dir 与水位线默认值（80/90）。
		if NormalizeStoragePoolDefaults(&pool) {
			changed = true
		}
		result = append(result, pool)
	}
	if len(result) != len(AppConfig.StoragePools) {
		changed = true
	}
	AppConfig.StoragePools = result
	return changed
}

func managedStoragePoolPath(mountPoint string) string {
	mountPoint = filepath.Clean(strings.TrimSpace(mountPoint))
	if mountPoint == string(os.PathSeparator) {
		return filepath.Join(string(os.PathSeparator), "var", "lib", "eyvescloud")
	}
	return filepath.Join(mountPoint, "eyvescloud")
}

func storagePoolIDFromName(name, path string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" {
		base = filepath.Base(filepath.Clean(path))
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-", "/", "-")
	base = replacer.Replace(base)
	base = strings.Trim(base, "-")
	if base == "" {
		base = "storage"
	}
	return base
}

func normalizeStorageContentTypes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	valid := map[string]bool{
		StorageContentLXC:       true,
		StorageContentKVM:       true,
		StorageContentImages:    true,
		StorageContentSnapshots: true,
		StorageContentBackups:   true,
	}
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		next := strings.ToLower(strings.TrimSpace(value))
		if !valid[next] || seen[next] {
			continue
		}
		seen[next] = true
		result = append(result, next)
	}
	return result
}

func StoragePoolsForContent(content string) []StoragePool {
	if AppConfig == nil {
		return nil
	}
	content = strings.ToLower(strings.TrimSpace(content))
	result := []StoragePool{}
	for _, pool := range AppConfig.StoragePools {
		if !pool.Enabled || !storagePoolAllows(pool, content) {
			continue
		}
		result = append(result, pool)
	}
	return result
}

func StoragePoolByID(id string) *StoragePool {
	if AppConfig == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	for i := range AppConfig.StoragePools {
		if AppConfig.StoragePools[i].ID == id {
			return &AppConfig.StoragePools[i]
		}
	}
	return nil
}

func StoragePoolAllowsContent(pool StoragePool, content string) bool {
	return storagePoolAllows(pool, strings.ToLower(strings.TrimSpace(content)))
}

func StoragePathForContent(content, fallback string) string {
	if pool := DefaultStoragePoolForContent(content); pool != nil {
		return pool.Path
	}
	return fallback
}

// PreferredStoragePoolForContent returns the configured default without doing
// filesystem probes. Use SelectStoragePoolForContent for new writes.
func PreferredStoragePoolForContent(content string) *StoragePool {
	if AppConfig == nil {
		return nil
	}
	content = strings.ToLower(strings.TrimSpace(content))
	for i := range AppConfig.StoragePools {
		pool := &AppConfig.StoragePools[i]
		if !pool.Enabled || !storagePoolAllows(*pool, content) {
			continue
		}
		for _, item := range pool.DefaultContents {
			if item == content {
				return pool
			}
		}
	}
	for i := range AppConfig.StoragePools {
		pool := &AppConfig.StoragePools[i]
		if pool.Enabled && storagePoolAllows(*pool, content) {
			return pool
		}
	}
	return nil
}

func DefaultStoragePoolForContent(content string) *StoragePool {
	pool, _ := SelectStoragePoolForContent(content, "", 0)
	return pool
}

const storagePoolFreeReserveBytes int64 = 256 * 1024 * 1024

type storagePoolCandidate struct {
	pool      *StoragePool
	freeBytes int64
	isDefault bool
}

// SelectStoragePoolForContent picks a writable mounted pool. The requested or
// configured default pool is preferred while it has enough space; remaining
// pools are tried by available space from largest to smallest.
func SelectStoragePoolForContent(content, requestedPoolID string, requiredBytes int64) (*StoragePool, error) {
	if AppConfig == nil {
		return nil, fmt.Errorf("storage configuration is not loaded")
	}
	content = strings.ToLower(strings.TrimSpace(content))
	requestedPoolID = strings.TrimSpace(requestedPoolID)
	if requiredBytes < 0 {
		requiredBytes = 0
	}
	requiredFree := requiredBytes + storagePoolFreeReserveBytes
	candidates := make([]storagePoolCandidate, 0, len(AppConfig.StoragePools))
	configured := 0
	for i := range AppConfig.StoragePools {
		pool := &AppConfig.StoragePools[i]
		if !pool.Enabled || !storagePoolAllows(*pool, content) {
			continue
		}
		configured++
		freeBytes, available := probeStoragePoolFreeBytes(*pool)
		if !available {
			continue
		}
		candidate := storagePoolCandidate{pool: pool, freeBytes: freeBytes}
		for _, item := range pool.DefaultContents {
			if item == content {
				candidate.isDefault = true
				break
			}
		}
		candidates = append(candidates, candidate)
	}
	if configured == 0 {
		return nil, fmt.Errorf("no storage disk is enabled for %s", storageContentLabel(content))
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("all storage disks enabled for %s are unavailable or unmounted", storageContentLabel(content))
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].freeBytes > candidates[j].freeBytes
	})
	preferred := func(match func(storagePoolCandidate) bool) *StoragePool {
		for _, candidate := range candidates {
			if match(candidate) && candidate.freeBytes >= requiredFree {
				return candidate.pool
			}
		}
		return nil
	}
	if requestedPoolID != "" {
		if pool := preferred(func(candidate storagePoolCandidate) bool { return candidate.pool.ID == requestedPoolID }); pool != nil {
			return pool, nil
		}
	}
	if pool := preferred(func(candidate storagePoolCandidate) bool { return candidate.isDefault }); pool != nil {
		return pool, nil
	}
	if pool := preferred(func(storagePoolCandidate) bool { return true }); pool != nil {
		return pool, nil
	}
	return nil, fmt.Errorf("storage disks enabled for %s do not have enough free space", storageContentLabel(content))
}

var probeStoragePoolFreeBytes = storagePoolFreeBytes

func storagePoolFreeBytes(pool StoragePool) (int64, bool) {
	if strings.TrimSpace(pool.Path) == "" {
		return 0, false
	}
	if _, err := os.Stat(pool.Path); err != nil {
		if !os.IsNotExist(err) || filepath.Clean(pool.MountPoint) != string(os.PathSeparator) {
			return 0, false
		}
		if err := os.MkdirAll(pool.Path, 0755); err != nil {
			return 0, false
		}
	}
	if mountPoint := strings.TrimSpace(pool.MountPoint); mountPoint != "" {
		out, err := exec.Command("findmnt", "-n", "-o", "TARGET", "--target", pool.Path).Output()
		if err != nil || filepath.Clean(strings.TrimSpace(string(out))) != filepath.Clean(mountPoint) {
			return 0, false
		}
	}
	out, err := exec.Command("df", "-B1", "-P", pool.Path).Output()
	if err != nil {
		return 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, false
	}
	freeBytes, err := strconv.ParseInt(fields[3], 10, 64)
	return freeBytes, err == nil
}

func storageContentLabel(content string) string {
	switch content {
	case StorageContentLXC:
		return "LXC containers"
	case StorageContentKVM:
		return "KVM disks"
	case StorageContentImages:
		return "image cache"
	case StorageContentSnapshots:
		return "snapshots"
	case StorageContentBackups:
		return "backups"
	default:
		return content
	}
}

func storagePoolAllows(pool StoragePool, content string) bool {
	for _, item := range pool.ContentTypes {
		if item == content {
			return true
		}
	}
	return false
}

func (c *Container) NormalizeNetworkAssignments() bool {
	changed := false
	lanMode := strings.ToLower(strings.TrimSpace(c.LANIPv4Mode))
	if lanMode != "" && lanMode != LANIPv4ModeDHCP && lanMode != LANIPv4ModeStatic {
		lanMode = ""
	}
	if c.LANIPv4Mode != lanMode {
		c.LANIPv4Mode = lanMode
		changed = true
	}
	lanInterface := strings.TrimSpace(c.LANInterface)
	if c.LANInterface != lanInterface {
		c.LANInterface = lanInterface
		changed = true
	}
	lanAddress := strings.TrimSpace(c.LANIPv4Address)
	if c.LANIPv4Address != lanAddress {
		c.LANIPv4Address = lanAddress
		changed = true
	}
	lanGateway := strings.TrimSpace(c.LANIPv4Gateway)
	if c.LANIPv4Gateway != lanGateway {
		c.LANIPv4Gateway = lanGateway
		changed = true
	}
	if c.LANIPv4Mode == LANIPv4ModeDHCP {
		if c.LANIPv4Address != "" {
			c.LANIPv4Address = ""
			changed = true
		}
	} else if c.LANIPv4Mode != LANIPv4ModeStatic {
		if c.LANIPv4Address != "" || c.LANIPv4PrefixLen != 0 || c.LANIPv4Gateway != "" {
			c.LANIPv4Address = ""
			c.LANIPv4PrefixLen = 0
			c.LANIPv4Gateway = ""
			changed = true
		}
	}
	seenIPv4 := map[string]bool{}
	filteredIPv4 := make([]PublicIPv4Assignment, 0, len(c.PublicIPv4s))
	for _, item := range c.PublicIPv4s {
		item.Address = strings.TrimSpace(item.Address)
		item.Interface = strings.TrimSpace(item.Interface)
		item.Gateway = strings.TrimSpace(item.Gateway)
		if item.Address == "" || seenIPv4[item.Address] {
			if item.Address != "" {
				changed = true
			}
			continue
		}
		seenIPv4[item.Address] = true
		filteredIPv4 = append(filteredIPv4, item)
	}
	if len(filteredIPv4) != len(c.PublicIPv4s) {
		changed = true
	}
	c.PublicIPv4s = filteredIPv4

	seenIPv6 := map[string]bool{}
	filteredIPv6 := make([]IPv6Assignment, 0, len(c.IPv6Addresses))
	for _, item := range c.IPv6Addresses {
		item.Address = strings.TrimSpace(item.Address)
		item.Interface = strings.TrimSpace(item.Interface)
		if item.Address == "" || seenIPv6[item.Address] {
			if item.Address != "" {
				changed = true
			}
			continue
		}
		seenIPv6[item.Address] = true
		filteredIPv6 = append(filteredIPv6, item)
	}
	if strings.TrimSpace(c.IPv6) != "" && !seenIPv6[c.IPv6] {
		filteredIPv6 = append([]IPv6Assignment{{
			Address:   c.IPv6,
			PrefixLen: c.IPv6PrefixLen,
			Interface: c.IPv6Interface,
		}}, filteredIPv6...)
		changed = true
	}
	if len(filteredIPv6) != len(c.IPv6Addresses) {
		changed = true
	}
	c.IPv6Addresses = filteredIPv6
	if len(c.IPv6Addresses) > 0 {
		first := c.IPv6Addresses[0]
		if c.IPv6 != first.Address || c.IPv6PrefixLen != first.PrefixLen || c.IPv6Interface != first.Interface {
			c.IPv6 = first.Address
			c.IPv6PrefixLen = first.PrefixLen
			c.IPv6Interface = first.Interface
			changed = true
		}
	} else if c.IPv6 != "" || c.IPv6PrefixLen != 0 || c.IPv6Interface != "" {
		c.IPv6 = ""
		c.IPv6PrefixLen = 0
		c.IPv6Interface = ""
		changed = true
	}
	return changed
}

func (c *Container) PublicIPv4Addresses() []string {
	values := make([]string, 0, len(c.PublicIPv4s))
	for _, item := range c.PublicIPv4s {
		if item.Address != "" {
			values = append(values, item.Address)
		}
	}
	return values
}

func (c *Container) PrimaryPublicIPv4() string {
	if len(c.PublicIPv4s) == 0 {
		return ""
	}
	return c.PublicIPv4s[0].Address
}

func (c *Container) IPv6AddressStrings() []string {
	values := make([]string, 0, len(c.IPv6Addresses))
	for _, item := range c.IPv6Addresses {
		if item.Address != "" {
			values = append(values, item.Address)
		}
	}
	if len(values) == 0 && c.IPv6 != "" {
		values = append(values, c.IPv6)
	}
	return values
}

// LxcName returns the internal LXC container name (ct-{id})
func (c *Container) LxcName() string {
	if c.LXCName != "" {
		return c.LXCName
	}
	return fmt.Sprintf("ct-%d", c.ID)
}

// VirshName returns the internal libvirt domain name for KVM instances.
func (c *Container) VirshName() string {
	if c.KVMName != "" {
		return c.KVMName
	}
	return fmt.Sprintf("vm-%d", c.ID)
}

// SubUser represents a sub-user with access to specific containers
type ApiKeyConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	KeyHash        string   `json:"key_hash"`
	KeyFingerprint string   `json:"key_fingerprint,omitempty"` // SHA-256 of the raw key, used for O(1) pre-screening
	Prefix         string   `json:"prefix"`
	IPWhitelist    string   `json:"ip_whitelist"`
	CreatedAt      string   `json:"created_at"`
	LastUsed       string   `json:"last_used"`
	Scopes         []string `json:"scopes,omitempty"`
	ExpiresAt      string   `json:"expires_at,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	ContainerUUIDs []string `json:"container_uuids,omitempty"`
	LastUsedIP     string   `json:"last_used_ip,omitempty"`
	// RateLimitPerMinute 单 key 每分钟请求上限（0 = 使用全局默认）
	RateLimitPerMinute int `json:"rate_limit_per_minute,omitempty"`
	// RevokedAt 撤销时间；非空即视为已撤销（与 Disabled 互为冗余，撤销是不可逆动作）
	RevokedAt string `json:"revoked_at,omitempty"`
}

// DeleteApiKey removes an API key by ID
func DeleteApiKey(id string) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	filtered := make([]ApiKeyConfig, 0, len(AppConfig.ApiKeys))
	for _, k := range AppConfig.ApiKeys {
		if k.ID != id {
			filtered = append(filtered, k)
		}
	}
	AppConfig.ApiKeys = filtered
	SaveConfigToDBLogged()
}

// DefaultAdminPath 是管理员入口路径的默认值（挂在根路径，保持历史行为）。
const DefaultAdminPath = "/"

// 管理员入口路径占用的保留前缀：这些路径属于 API、用户门户或静态资源。
var reservedAdminPathPrefixes = []string{"/api", "/user", "/assets", "/favicon", "/favicon.svg", "/index.html"}

// NormalizeAdminPath 归一化管理员入口路径并校验合法性：
//   - 空串或 "/" → "/"（默认挂在根路径）
//   - 必须以 "/" 开头，自动去掉尾部 "/"
//   - 每一段仅允许 [A-Za-z0-9._~-]，禁止空段/./..，避免路径穿越与编码歧义
//   - 不得占用 /api、/user、/assets、/favicon 等保留前缀
//
// 第二个返回值 false 表示非法，调用方应拒绝保存。
func NormalizeAdminPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return DefaultAdminPath, true
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return DefaultAdminPath, true
	}
	for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '-', r == '_', r == '.', r == '~':
			default:
				return "", false
			}
		}
	}
	lower := strings.ToLower(p)
	for _, reserved := range reservedAdminPathPrefixes {
		if lower == reserved || strings.HasPrefix(lower, reserved+"/") {
			return "", false
		}
	}
	return p, true
}

// CurrentAdminPath 返回当前管理员入口路径（已归一化，默认 "/"）。
func CurrentAdminPath() string {
	AppConfigMu.RLock()
	raw := AppConfig.AdminPath
	AppConfigMu.RUnlock()
	if p, ok := NormalizeAdminPath(raw); ok {
		return p
	}
	return DefaultAdminPath
}

// AdminPathForRequest 决定给某个前端请求注入的管理员入口路径：
//   - 管理员路径为 "/"：始终注入 "/"（默认行为）
//   - 请求落在管理员路径下：注入真实路径，管理端路由才会被挂载
//   - 其它路径：注入空串，页面里不含任何管理端路由（入口不可被枚举）
func AdminPathForRequest(requestPath string) string {
	adminPath := CurrentAdminPath()
	if adminPath == DefaultAdminPath {
		return DefaultAdminPath
	}
	if requestPath == adminPath || strings.HasPrefix(requestPath, adminPath+"/") {
		return adminPath
	}
	return ""
}

// AdminAccount 是主管理员之外的管理员账号（多管理员支持）。
//
// 主管理员仍由 AdminUser/AdminPassHash/AdminTOTP* 承载，行为完全不变（TOTP、
// 备份码、CLI、改用户名/密码都走原路径）。这里存放管理员自行创建的额外账号，
// 用于多人协作；每个账号有独立 TokenVersion，改密码只吊销该账号自己的令牌。
//
// Role 取值：admin（全权）/ operator（运维，禁平台级）/ readonly（只读）。
type AdminAccount struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PassHash     string `json:"pass_hash"`
	Role         string `json:"role"`
	TokenVersion int    `json:"token_version"`
	Disabled     bool   `json:"disabled,omitempty"`
	CreatedAt    string `json:"created_at"`
	LastLoginAt  string `json:"last_login_at,omitempty"`
}

// 管理员角色常量。
const (
	AdminRoleAdmin    = "admin"
	AdminRoleOperator = "operator"
	AdminRoleReadonly = "readonly"
)

// NormalizeAdminRole 归一化管理员角色，未知取值一律降级为 readonly（fail closed）。
func NormalizeAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case AdminRoleAdmin:
		return AdminRoleAdmin
	case AdminRoleOperator:
		return AdminRoleOperator
	case AdminRoleReadonly, "viewer", "read":
		return AdminRoleReadonly
	}
	return AdminRoleReadonly
}

// FindAdminAccount 按用户名查找额外管理员（返回副本，调用方无需持锁）。
func FindAdminAccount(username string) (AdminAccount, bool) {
	username = strings.TrimSpace(username)
	if username == "" {
		return AdminAccount{}, false
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for i := range AppConfig.Admins {
		if strings.EqualFold(AppConfig.Admins[i].Username, username) {
			return AppConfig.Admins[i], true
		}
	}
	return AdminAccount{}, false
}

// FindAdminAccountByID 按 ID 查找额外管理员（返回副本）。
func FindAdminAccountByID(id string) (AdminAccount, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AdminAccount{}, false
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for i := range AppConfig.Admins {
		if AppConfig.Admins[i].ID == id {
			return AppConfig.Admins[i], true
		}
	}
	return AdminAccount{}, false
}

// AdminUsernameTaken 判断用户名是否已被主管理员或其它管理员占用（大小写不敏感）。
func AdminUsernameTaken(username string, exceptID string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return true
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if strings.EqualFold(AppConfig.AdminUser, username) {
		return true
	}
	for i := range AppConfig.Admins {
		if AppConfig.Admins[i].ID == exceptID {
			continue
		}
		if strings.EqualFold(AppConfig.Admins[i].Username, username) {
			return true
		}
	}
	return false
}

type SubUser struct {
	ID                   string   `json:"id"`
	Username             string   `json:"username"`
	Email                string   `json:"email,omitempty"`
	Password             string   `json:"password,omitempty"`
	PassHash             string   `json:"pass_hash"`
	Role                 string   `json:"role"`             // operator(默认，可操作) / viewer(只读)
	Tenant               string   `json:"tenant,omitempty"` // 绑定租户：该子用户可访问此租户下全部容器
	ContainerNames       []string `json:"container_names"`
	ContainerUUIDs       []string `json:"container_uuids,omitempty"`
	AllowedImageIDs      []string `json:"allowed_image_ids,omitempty"`
	ImageLimitConfigured bool     `json:"image_limit_configured,omitempty"`
	Token                string   `json:"-"`
	AccessCode           string   `json:"access_code"`
	// AccessCodePassword 是「访问码登录」专用口令，与账号密码（PassHash）相互独立：
	//   - 账号密码用于用户名/邮箱登录，仅子用户本人持有；
	//   - 访问码密码用于访问码 + 口令登录（可分享给他人管理已绑定容器）。
	// 该口令必须可回显（管理员/用户端均需展示），因此以 AES-256-GCM 可逆加密落库
	// （见 store_sqlite.go 的 save/load），内存中为明文。
	AccessCodePassword string `json:"access_code_password,omitempty"`
	CreatedAt          string `json:"created_at"`
	TokenVersion       int    `json:"token_version"`
}

// subUserRoleForStorage normalizes a sub-user role for persistence.
func subUserRoleForStorage(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "viewer") {
		return "viewer"
	}
	return "operator"
}

// NormalizeEmail 规范化邮箱：trim + lowercase；空串返回空；非法邮箱返回 error。
// 只接受纯邮箱形式（name@domain.tld），不接受带 DisplayName 的 RFC 822 格式。
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", nil
	}
	// 拒绝含空格的输入，保证只能是纯 "local@domain.tld"。
	if strings.ContainsAny(email, " \t\n\r") {
		return "", fmt.Errorf("email must not contain whitespace")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("invalid email address: %w", err)
	}
	// mail.ParseAddress 接受 "Name <email>" 和纯 "email"，我们只要纯邮箱。
	if addr.Name != "" {
		return "", fmt.Errorf("email must be plain address only, no display name")
	}
	return addr.Address, nil
}

// FindSubUserByNameOrEmail 按 Username 或 Email 查找 SubUser。
// 返回找到的指针副本 + 是否存在。调用方只读即可；如需修改请用 MutateGlobal。
func FindSubUserByNameOrEmail(identifier string) (*SubUser, bool) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, false
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	lower := strings.ToLower(identifier)
	for i := range AppConfig.SubUsers {
		su := &AppConfig.SubUsers[i]
		if strings.EqualFold(su.Username, identifier) || strings.ToLower(strings.TrimSpace(su.Email)) == lower {
			cp := *su
			return &cp, true
		}
	}
	return nil, false
}

// SubUserUsernameOrEmailExists 检查 Username 或 Email 是否已存在（不区分大小写）。
// 可选排除某个 ID（编辑场景排除自己）。
func SubUserUsernameOrEmailExists(username, email, excludeID string) bool {
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for i := range AppConfig.SubUsers {
		su := &AppConfig.SubUsers[i]
		if excludeID != "" && su.ID == excludeID {
			continue
		}
		if username != "" && strings.EqualFold(su.Username, username) {
			return true
		}
		if email != "" && strings.ToLower(strings.TrimSpace(su.Email)) == email {
			return true
		}
	}
	return false
}

type Snapshot struct {
	ID            string `json:"id"`
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name"`
	LXCName       string `json:"lxc_name"`
	CreatedAt     string `json:"created_at"`
	CreatedBy     string `json:"created_by"`
	Scheduled     bool   `json:"scheduled"`
	Path          string `json:"path"`
	SizeBytes     int64  `json:"size_bytes"`
}

const (
	SSLModeDisabled    = "disabled"
	SSLModeLetsEncrypt = "letsencrypt"
	SSLModeSelfSigned  = "self_signed"
	SSLModeUploaded    = "uploaded"
)

type SSLConfig struct {
	Enabled      bool   `json:"enabled"`
	Mode         string `json:"mode"`
	Target       string `json:"target"`
	Email        string `json:"email,omitempty"`
	CertPath     string `json:"cert_path,omitempty"`
	KeyPath      string `json:"key_path,omitempty"`
	LastIssuedAt string `json:"last_issued_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	// HTTPRedirectPort：启用 TLS 后额外监听一个 HTTP 端口做 301 跳转到 HTTPS。
	//
	// 存在的理由：面板是单端口服务，启用 TLS 后原 HTTP 入口直接消失——用户的
	// 既有书签、监控探针、计费系统回调会立刻连接失败，表现为"开了证书反而打不开"。
	// 设一个跳转端口（如 8998）可把"访问断裂"变成透明升级。0 = 不启用。
	HTTPRedirectPort int `json:"http_redirect_port,omitempty"`
}

const (
	StorageContentLXC       = "lxc"
	StorageContentKVM       = "kvm"
	StorageContentImages    = "images"
	StorageContentSnapshots = "snapshots"
	StorageContentBackups   = "backups"
)

type StoragePool struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	MountPoint      string   `json:"mount_point,omitempty"`
	ContentTypes    []string `json:"content_types"`
	DefaultContents []string `json:"default_contents,omitempty"`
	Enabled         bool     `json:"enabled"`
	// Backend 存储后端类型：dir|zfs|lvm|rbd|cephfs|nfs（P0-1 仅实现 dir；
	// 旧数据该字段为空字符串，加载时自动补 dir）。
	Backend string `json:"backend,omitempty"`
	// Shared 是否为多节点共享存储池（如 NFS/CephFS）。P0-1 仅落库，暂不参与调度。
	Shared bool `json:"shared,omitempty"`
	// WatermarkWarn 使用率告警水位线（百分比），默认 80：超过后产生告警事件。
	WatermarkWarn int `json:"watermark_warn,omitempty"`
	// WatermarkCritical 使用率禁止写入水位线（百分比），默认 90：超过后拒绝新建/扩容。
	WatermarkCritical int `json:"watermark_critical,omitempty"`
	// Config 后端私有配置（K/V，键名空间由后端约定）。dir 池忽略；
	// zfs 池示例：{"parent":"tank/eyvescloud","compression":"lz4","refquota":"1G"}。
	// P0-2 不下放接口（前端不可见），仅在池配置同步/启动时供后端读取。
	Config map[string]string `json:"config,omitempty"`
}

// 存储池水位线默认值（P0-1 约定 80/90）。
const (
	DefaultStorageWatermarkWarn     = 80
	DefaultStorageWatermarkCritical = 90
)

// NormalizeStoragePoolDefaults 补齐存储池 P0-1 新增字段的默认值并返回是否发生修改：
//   - Backend 为空（旧数据）或为未知后端时回退 dir（当前唯一可服务的后端实现）；
//   - 水位线缺省补 80/90，并保证 Warn <= Critical。
//
// 该函数同时被 config 加载归一化路径与 /api/storage 的 PUT 归一化路径使用，
// 保证两条写路径行为一致。
func NormalizeStoragePoolDefaults(pool *StoragePool) bool {
	changed := false
	if backend := storage.NormalizeBackendKind(pool.Backend); pool.Backend != backend {
		pool.Backend = backend
		changed = true
	}
	if pool.Config == nil {
		pool.Config = map[string]string{}
		changed = true
	}
	warn := pool.WatermarkWarn
	if warn <= 0 {
		warn = DefaultStorageWatermarkWarn
	}
	critical := pool.WatermarkCritical
	if critical < warn {
		critical = DefaultStorageWatermarkCritical
	}
	if critical < warn {
		critical = warn
	}
	if pool.WatermarkWarn != warn {
		pool.WatermarkWarn = warn
		changed = true
	}
	if pool.WatermarkCritical != critical {
		pool.WatermarkCritical = critical
		changed = true
	}
	return changed
}

func defaultPrimaryStoragePool() StoragePool {
	contents := []string{
		StorageContentLXC,
		StorageContentKVM,
		StorageContentImages,
		StorageContentSnapshots,
		StorageContentBackups,
	}
	return StoragePool{
		ID:                "disk-root",
		Name:              "system (/)",
		Path:              "/var/lib/eyvescloud",
		MountPoint:        "/",
		ContentTypes:      append([]string(nil), contents...),
		DefaultContents:   append([]string(nil), contents...),
		Enabled:           true,
		Backend:           storage.BackendDir,
		WatermarkWarn:     DefaultStorageWatermarkWarn,
		WatermarkCritical: DefaultStorageWatermarkCritical,
	}
}

// AuditRetentionDefault is the default number of days audit/login logs are kept (0 = keep all).
const AuditRetentionDefault = 90

// MetricRetentionDefault is the default number of days hourly metric rollups are
// kept (0 = keep all). Raw per-sample metrics are kept for a short window only.
const MetricRetentionDefault = 90

// BackupRecord represents an on-disk configuration backup snapshot.
type BackupRecord struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	Kind      string `json:"kind"` // "config"
	CreatedAt string `json:"created_at"`
}

// InstanceBackup is a portable, keep-N archivable full-disk backup of a
// container instance. Backups are created from a consistent snapshot and
// archived into the instance-backup store so they survive container deletion,
// reinstall, or even node loss (they can be restored onto a fresh instance).
type InstanceBackup struct {
	ID            string `json:"id"`
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name"`
	Kind          string `json:"kind"` // "lxc" | "kvm"
	CreatedAt     string `json:"created_at"`
	CreatedBy     string `json:"created_by"`
	Scheduled     bool   `json:"scheduled,omitempty"`
	Path          string `json:"path"`       // archive file on disk (.tar)
	SizeBytes     int64  `json:"size_bytes"` // archive file size
	// RemoteUploaded / RemoteError 记录该备份是否已成功上传到异地目标（见 RemoteBackupSettings）。
	// 上传失败不影响本地备份的有效性，仅在 UI 中提示异地副本缺失。
	RemoteUploaded bool   `json:"remote_uploaded,omitempty"`
	RemoteError    string `json:"remote_error,omitempty"`
}

// BackupSettings controls automatic configuration backups.
type BackupSettings struct {
	Enabled        bool   `json:"enabled"`
	IntervalHours  int    `json:"interval_hours"`
	Keep           int    `json:"keep"`
	Directory      string `json:"directory,omitempty"`
	LastBackupAt   string `json:"last_backup_at,omitempty"`
	LastBackupFile string `json:"last_backup_file,omitempty"`
}

// InstanceBackupSettings controls automatic instance (disk) backups.
// 开启后按 IntervalHours 周期为所有运行中的容器创建磁盘备份，并保留最新 Keep 份。
type InstanceBackupSettings struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"interval_hours"`
	Keep          int    `json:"keep"`
	LastRunAt     string `json:"last_run_at,omitempty"`
}

// RemoteBackupSettings 异地备份目标：把实例备份归档额外复制到一台用户自备的
// 备份服务器（通过 SSH/SCP），用于应对本机磁盘损坏、整机丢失等灾难场景。
//
// 安全约束：
//   - 仅支持 SSH/SCP 免密登录，私钥只允许放在面板数据目录内（KeyPath 可为空，
//     默认使用 DataDir/ssh/backup_ed25519，由面板用 ssh-keygen 生成）；
//   - 远端目录与文件名在拼接命令前做白名单校验，杜绝命令注入。
type RemoteBackupSettings struct {
	Enabled   bool   `json:"enabled"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	RemoteDir string `json:"remote_dir"` // 远端目录（绝对路径，如 /data/eyvescloud-backups）
	KeyPath   string `json:"key_path,omitempty"`
	// LastResult / LastError / LastRunAt 记录最近一次异地同步的结果，供管理页展示。
	LastResult string `json:"last_result,omitempty"` // success / failed
	LastError  string `json:"last_error,omitempty"`
	LastRunAt  string `json:"last_run_at,omitempty"`
}

// APIRateLimitConfig controls per-client rate limiting on the versioned API.
type APIRateLimitConfig struct {
	Enabled   bool `json:"enabled"`
	PerMinute int  `json:"per_minute"`
}

// SMTPSettings 邮件发送（SMTP）配置：用于子用户账号通知
// （挂起/复机/到期提醒等）。密码只存于此处并随 config.db 0600 权限保护，
// API 读取时Password 字段不回显（见 api 层 smtp 设置端点）。
type SMTPSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
	// TLSMode: "starttls"（默认，587）或 "smtps"（465 隐式 TLS）
	TLSMode string `json:"tls_mode,omitempty"`
}

// Tenant represents a tenant group with resource quotas.
type Tenant struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	ContainerQuota int    `json:"container_quota"` // 0 = unlimited
	VCPUQuota      int    `json:"vcpu_quota"`
	RAMQuotaMB     int64  `json:"ram_quota_mb"`
	DiskQuotaGB    int64  `json:"disk_quota_gb"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
}

// NotificationConfig controls external alert push (webhook / SMTP).
type NotificationConfig struct {
	SecurityAlertsEnabled bool   `json:"security_alerts_enabled"`
	MinSeverity           string `json:"min_severity"` // low / medium / high / critical
	WebhookURL            string `json:"webhook_url,omitempty"`
	SMTPEnabled           bool   `json:"smtp_enabled"`
	SMTPServer            string `json:"smtp_server,omitempty"`
	SMTPPort              int    `json:"smtp_port"`
	SMTPUser              string `json:"smtp_user,omitempty"`
	SMTPPassword          string `json:"smtp_password,omitempty"`
	SMTPFrom              string `json:"smtp_from,omitempty"`
	SMTPTo                string `json:"smtp_to,omitempty"`
}

// Node represents a managed worker (被控节点) registered to this controller.
// The controller generates InstallKey (used once by the agent install script)
// and Token (used for heartbeat and controller->agent API calls).
type Node struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address,omitempty"` // 被控自身面板地址 http(s)://host:port
	// PublicHost 是该节点上容器对外提供服务的接入地址（公网 IP 或域名）。
	// 留空时由 Address 的 host 推导。用于生成容器的 SSH/RDP 接入端点
	// （NAT 端口映射挂在节点上，端点必须指向节点而不是主控）。
	PublicHost          string   `json:"public_host,omitempty"`
	Token               string   `json:"token,omitempty"`
	InstallKey          string   `json:"install_key,omitempty"`
	InstallKeyCreatedAt string   `json:"install_key_created_at,omitempty"`
	InstallKeyIP        string   `json:"install_key_ip,omitempty"`
	Status              string   `json:"status"` // online / offline / pending
	LastSeen            string   `json:"last_seen,omitempty"`
	Version             string   `json:"version,omitempty"`
	OSName              string   `json:"os_name,omitempty"`
	CPUCount            int      `json:"cpu_count,omitempty"`
	RAMTotalMB          int64    `json:"ram_total_mb,omitempty"`
	RAMUsedMB           int64    `json:"ram_used_mb,omitempty"`
	DiskTotalGB         float64  `json:"disk_total_gb,omitempty"`
	DiskUsedGB          float64  `json:"disk_used_gb,omitempty"`
	ContainerCount      int      `json:"container_count,omitempty"`
	RegionID            string   `json:"region_id,omitempty"`     // 所属区域，见 Regions
	NodeGroupID         string   `json:"node_group_id,omitempty"` // 所属节点分组，见 NodeGroups（迁移池/策略池）
	ClusterID           string   `json:"cluster_id,omitempty"`    // 所属集群，见 Clusters（跨分组 HA/迁移域）
	VirtTypes           []string `json:"virt_types,omitempty"`    // 节点支持的虚拟化类型: "lxc"/"kvm"/["lxc","kvm"]
	CreatedAt           string   `json:"created_at,omitempty"`
	// MaintenanceMode 维护模式：调度器不再把新容器放到该节点（升级/维修前开启）。
	// 已有容器不受影响，配合 drain 列表手动迁移。
	MaintenanceMode  bool   `json:"maintenance_mode,omitempty"`
	MaintenanceSince string `json:"maintenance_since,omitempty"`
	// TLSSkipVerify 允许主控→被控方向跳过 TLS 证书校验（被控自签证书场景）。
	// 默认 false = 严格校验。开启会降低中间人防护，创建时落审计。
	TLSSkipVerify bool `json:"tls_skip_verify,omitempty"`
	// AllowPrivateAddr 记录该节点地址被显式豁免 SSRF 私网/环回拦截（内网部署场景）。
	// 链路本地（169.254.0.0/16 含云元数据、fe80::/10）永远拒绝，无豁免。
	AllowPrivateAddr bool `json:"allow_private_addr,omitempty"`
}

// NodeGroup 是一个逻辑节点分组（迁移池 / 策略池）：主流面板 叫 Server Group，
// 同类面板 叫 Node Group。调度器可按 NodeGroup 过滤；同一 NodeGroup 内的节点
// 共享迁移目标范围与资源策略。
type NodeGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	RegionID    string `json:"region_id,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// Cluster 是一个跨 NodeGroup 的高可用 / 迁移域：主流面板 叫 Cluster。
// Cluster 内可包含多个 NodeGroup，调度器在 Cluster 范围内挑选目标节点；
// 容器显式迁移时，默认只允许在同一 Cluster 内跨 NodeGroup 移动。
type Cluster struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	RegionIDs   []string `json:"region_ids,omitempty"` // Cluster 覆盖的区域（可选）
	CreatedAt   string   `json:"created_at,omitempty"`
}

// Region 是一个逻辑区域，用于把节点/存储/容器按地域分组管理。
type Region struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Location  string `json:"location,omitempty"` // 展示用地域（城市/机房）
	CreatedAt string `json:"created_at,omitempty"`
	// 区域级配额（0 = 不限制）：统计口径为「归属该区域的节点上的实例总量」。
	// 创建实例时按目标节点所属区域校验，避免把同一区域的资源超卖。
	MaxInstances int     `json:"max_instances,omitempty"`
	MaxRAMMB     int64   `json:"max_ram_mb,omitempty"`
	MaxDiskGB    float64 `json:"max_disk_gb,omitempty"`
}

// RegionUsage 统计区域当前用量：实例数、内存（MB）、磁盘（GB，含数据盘）。
// 口径：先按 region_id 找出区域下的节点，再累加这些节点上的实例。
func RegionUsage(regionID string) (instances int, ramMB int64, diskGB float64) {
	if strings.TrimSpace(regionID) == "" {
		return 0, 0, 0
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return 0, 0, 0
	}
	nodeIDs := make(map[string]bool)
	for _, n := range AppConfig.Nodes {
		if n.RegionID == regionID {
			nodeIDs[n.ID] = true
		}
	}
	if len(nodeIDs) == 0 {
		return 0, 0, 0
	}
	for _, c := range AppConfig.Containers {
		if c.NodeID == "" || !nodeIDs[c.NodeID] {
			continue
		}
		instances++
		ramMB += int64(c.RAMMB)
		diskGB += c.DiskGB + c.DataDiskGB
	}
	return instances, ramMB, diskGB
}

// FindRegion 按 ID 取区域快照。
func FindRegion(id string) (Region, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return Region{}, false
	}
	for _, region := range AppConfig.Regions {
		if region.ID == id {
			return region, true
		}
	}
	return Region{}, false
}

// IPGroup 定义一组可故障切换（failover）的公网 IP。组内 IP 与跨容器移动由面板管理。
type IPGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	FaultOpen string   `json:"fault_open,omitempty"` // 当前故障保持的 IP（enable）
	Enable    []string `json:"enable,omitempty"`     // 当前生效 IP
	Standby   []string `json:"standby,omitempty"`    // 备用 IP（未生效）
	CreatedAt string   `json:"created_at,omitempty"`
}

// ISOFile 是一份可挂载到 KVM 虚拟机的 ISO 镜像（安装/驱动盘）。
type ISOFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	OS        string `json:"os,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// EyvescloudConfig is the main configuration structure
// UpdateSource 描述面板自动更新的 Git Release 源。
// 统一适配 GitHub / Codeberg / Gitee / GitLab 四大平台的 Releases API：
//   - platform: github | codeberg | gitee | gitlab
//   - owner/repo: 仓库 owner 与 repo 名
//   - branch: 可选，当 latest release 未找到时回退到此分支最新 tag；默认 main
//   - token: 可选，私有仓库需要
//   - asset_prefix: 可选，release 产物前缀；默认 eyvescloud
//
// 留空时 initConfig 会写入官方仓库（Codeberg）默认值。
type UpdateSource struct {
	Platform    string `json:"platform"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch,omitempty"`
	Token       string `json:"token,omitempty"`
	AssetPrefix string `json:"asset_prefix,omitempty"`
}

// defaultUpdateSourceComponents 从 version.Repo（"platform:owner/name"）解析
// 官方默认更新源，保证「面板默认更新地址」与二进制内置仓库单一来源、不会漂移。
func defaultUpdateSourceComponents() (platform, owner, repo string) {
	platform, owner, repo = "codeberg", "fenhaolost", "eyves-vm-panel"
	raw := strings.TrimSpace(version.Repo)
	if idx := strings.Index(raw, ":"); idx > 0 && !strings.HasPrefix(raw, "http") {
		if p := strings.ToLower(strings.TrimSpace(raw[:idx])); p != "" {
			platform = p
		}
		raw = strings.TrimSpace(raw[idx+1:])
	}
	if parts := strings.SplitN(raw, "/", 2); len(parts) == 2 {
		if o := strings.TrimSpace(parts[0]); o != "" {
			owner = o
		}
		if r := strings.TrimSpace(parts[1]); r != "" {
			repo = r
		}
	}
	return platform, owner, repo
}

// NormalizeUpdateSource 补齐默认值：留空 platform/owner/repo → 官方仓库（Codeberg）；
// 留空 branch → main；留空 asset_prefix → eyvescloud。
func NormalizeUpdateSource(u UpdateSource) UpdateSource {
	defPlatform, defOwner, defRepo := defaultUpdateSourceComponents()
	u.Platform = strings.ToLower(strings.TrimSpace(u.Platform))
	switch u.Platform {
	case "":
		u.Platform = defPlatform
	case "github", "codeberg", "gitee", "gitlab":
	default:
		u.Platform = defPlatform
	}
	if strings.TrimSpace(u.Owner) == "" {
		u.Owner = defOwner
	}
	if strings.TrimSpace(u.Repo) == "" {
		u.Repo = defRepo
	}
	if strings.TrimSpace(u.Branch) == "" {
		u.Branch = "main"
	}
	if strings.TrimSpace(u.AssetPrefix) == "" {
		u.AssetPrefix = "eyvescloud"
	}
	return u
}

type EyvescloudConfig struct {
	AdminUser         string   `json:"admin_user"`
	AdminPassHash     string   `json:"admin_pass_hash"`
	AdminTokenVersion int      `json:"admin_token_version"`
	AdminTOTPSecret   string   `json:"admin_totp_secret,omitempty"`
	AdminTOTPEnabled  bool     `json:"admin_totp_enabled"`
	AdminBackupCodes  []string `json:"admin_backup_codes,omitempty"`
	// AdminPath 是管理员入口路径（可自定义，默认 "/"）。用户门户固定为 /user。
	AdminPath string `json:"admin_path,omitempty"`
	// PanelDomain 是面板对外绑定的域名（如 https://panel.example.com）。
	// 用于生成对外 URL（节点安装命令、agent 注册地址、邮件链接等），避免在
	// 反代/多入口环境下拿 r.Host 拼出内网或错误地址。留空 = 按请求 Host 推导。
	PanelDomain string `json:"panel_domain,omitempty"`
	// TurnstileSiteKey / TurnstileSecretKey 是 Cloudflare Turnstile 人机验证密钥。
	// SiteKey 公开（前端渲染 widget），SecretKey 仅服务端调用 siteverify（密文落库）。
	TurnstileSiteKey   string `json:"turnstile_site_key,omitempty"`
	TurnstileSecretKey string `json:"turnstile_secret_key,omitempty"`
	// TurnstileAdminLogin / TurnstileUserLogin 分别控制管理员登录页与用户登录页
	// 是否强制 Turnstile 人机验证（密钥均配置后才生效）。
	TurnstileAdminLogin  bool                   `json:"turnstile_admin_login,omitempty"`
	TurnstileUserLogin   bool                   `json:"turnstile_user_login,omitempty"`
	JWTSecret            string                 `json:"jwt_secret"`
	Port                 int                    `json:"port"`
	DataDir              string                 `json:"data_dir"`
	Containers           []Container            `json:"containers"`
	NextContainerID      int                    `json:"next_container_id"`
	NextVNCPort          int                    `json:"next_vnc_port"`
	NextSSHPort          int                    `json:"next_ssh_port"`
	NATPortStart         int                    `json:"nat_port_start"`
	NATPortEnd           int                    `json:"nat_port_end"`
	LXCNATSubnet         string                 `json:"lxc_nat_subnet"`
	KVMNATSubnet         string                 `json:"kvm_nat_subnet"`
	SetupComplete        bool                   `json:"setup_complete"`
	SubUsers             []SubUser              `json:"sub_users"`
	Admins               []AdminAccount         `json:"admins,omitempty"`
	ApiKeys              []ApiKeyConfig         `json:"api_keys"`
	AuditLogs            []AuditLog             `json:"audit_logs"`
	Tasks                []SavedTask            `json:"tasks"`
	LoginLogs            []SavedLoginLog        `json:"login_logs"`
	EnabledImages        []string               `json:"enabled_images"`
	CustomKVMImages      []CustomKVMImage       `json:"custom_kvm_images"`
	CustomLXCImages      []CustomLXCImage       `json:"custom_lxc_images"`
	Snapshots            []Snapshot             `json:"snapshots"`
	PublicIPv4Pool       []PublicIPv4Assignment `json:"public_ipv4_pool"`
	PublicIPv6Prefixes   []PublicIPv6Prefix     `json:"public_ipv6_prefixes"`
	WebSSHAllowedOrigins []string               `json:"webssh_allowed_origins"`
	PanelAccessPolicy    PanelAccessPolicy      `json:"panel_access_policy"`
	SecurityAutoShutdown bool                   `json:"security_auto_shutdown"`
	ARPProtectionEnabled bool                   `json:"arp_protection_enabled"`
	// IPAntiSpoofEnabled 开启后，平台会把分配给容器的公网 IPv4 与其 MAC 绑定，
	// 阻止容器盗用其它 IP（IP 防盗 / 防 ARP 冒充）。默认关闭。
	IPAntiSpoofEnabled bool `json:"ip_anti_spoof_enabled"`
	// AbuseDetectionEnabled 控制是否启用滥用行为检测（挖矿、BT/PT、VPN/代理/Tor、
	// 25 端口垃圾邮件、DDoS/CC、爆破、端口扫描、后门/远控监听、内网横向移动、
	// 疑似被入侵等）。仅产生告警，不直接处置容器；默认开启。
	AbuseDetectionEnabled bool                  `json:"abuse_detection_enabled"`
	Notifications         NotificationConfig    `json:"notifications"`
	TaskConcurrency       int                   `json:"task_concurrency"`
	Language              string                `json:"language"`
	SSL                   SSLConfig             `json:"ssl"`
	SSLCertificates       map[string]SSLConfig  `json:"ssl_certificates"`
	StoragePools          []StoragePool         `json:"storage_pools"`
	SSHKeys               []SSHKey              `json:"ssh_keys"`
	PolicyRules           []PolicyRule          `json:"policy_rules"`
	PolicyHistory         []PolicyTriggerRecord `json:"policy_history"`
	Nodes                 []Node                `json:"nodes,omitempty"`
	// AgentPairingKey 是本面板作为被控时的「对接密钥」：一次性、24h 有效，
	// 由管理员在「节点管理 → 节点接入」生成，填到目标主控的「对接已有面板」
	// 表单中，主控凭它调用本面板完成注册对接（方向与 install key 相反）。
	AgentPairingKey       string `json:"agent_pairing_key,omitempty"`
	AgentPairingKeyExpiry string `json:"agent_pairing_key_expiry,omitempty"`
	// UpdateSource 面板自动更新源。
	// 支持 GitHub / Codeberg / Gitee / GitLab 四大平台，填入 owner/repo + 可选 token
	// 即可从各平台的 Releases 拉取最新版本，统一产物命名：
	//   eyvescloud-linux-amd64.tar.gz / eyvescloud-linux-arm64.tar.gz
	// 留空时默认从官方仓库（codeberg.org/fenhaolost/eyves-vm-panel）检查。
	UpdateSource  UpdateSource     `json:"update_source"`
	Regions       []Region         `json:"regions,omitempty"`
	NodeGroups    []NodeGroup      `json:"node_groups,omitempty"`
	Clusters      []Cluster        `json:"clusters,omitempty"`
	IPGroups      []IPGroup        `json:"ip_groups,omitempty"`
	ISOFiles      []ISOFile        `json:"iso_files,omitempty"`
	SecGroups     []secgroup.Group `json:"sec_groups,omitempty"`
	SecGroupRules []secgroup.Rule  `json:"sec_group_rules,omitempty"`
	// SecurityGroupEnforced 控制安全组规则是否真正下发到防火墙。
	//
	// 默认 false：规则会被保存但不生效。默认策略是 drop，某个容器的放行规则
	// 配得不全时一旦启用就会直接断网——这个风险必须由管理员显式承担，不能由
	// 升级动作替他决定。开启后由 api.StartSecurityGroupEnforcer 周期同步。
	SecurityGroupEnforced  bool                   `json:"security_group_enforced,omitempty"`
	MetricRetentionDays    int                    `json:"metric_retention_days"`
	AuditRetentionDays     int                    `json:"audit_retention_days"`
	BackupSettings         BackupSettings         `json:"backup_settings"`
	InstanceBackupSettings InstanceBackupSettings `json:"instance_backup_settings"`
	// RemoteBackupSettings 异地（远程）备份目标，见 RemoteBackupSettings。
	RemoteBackupSettings RemoteBackupSettings `json:"remote_backup_settings"`
	Backups              []BackupRecord       `json:"backups,omitempty"`
	InstanceBackups      []InstanceBackup     `json:"instance_backups,omitempty"`
	// BackupPlans 定时备份计划（每计划独立 cron / 目标 / 保留份数）。
	BackupPlans             []BackupPlan       `json:"backup_plans,omitempty"`
	APIRateLimit            APIRateLimitConfig `json:"api_rate_limit"`
	SMTPSettings            SMTPSettings       `json:"smtp_settings"`
	Tenants                 []Tenant           `json:"tenants,omitempty"`
	MemoryOvercommitRatio   float64            `json:"memory_overcommit_ratio"`   // 内存超售比：可分配内存 = 物理内存 × 该值（1.0=不变，2.0=2倍）
	MemoryOvercommitEnabled bool               `json:"memory_overcommit_enabled"` // 是否启用内存超售（默认关闭，保守）
	KSMTuning               KSMTuningConfig    `json:"ksm_tuning"`
	// NATSubnetOversubscription 允许容器数量超过 NAT 子网 DHCP 地址池容量。
	// 企业超售几千台时需配合更大的 NAT 网段（如 /22 ~ /16）；开启后不再硬性拦截
	// 地址耗尽，新容器可能拿不到正常内网 IP，故默认关闭（保守，不易出问题）。
	NATSubnetOversubscription bool `json:"nat_subnet_oversubscription"`
	// DiskOvercommitRatio 磁盘超售比：磁盘累计配额上限 = 宿主磁盘总量 × 该值（1.0=不超售）。
	// 用于企业大批量开通时放宽磁盘配额校验，默认 1.0 不超售。
	DiskOvercommitRatio float64 `json:"disk_overcommit_ratio"`
	// Recipes 用户自定义 bash 脚本模板（类比 主流面板 Recipes）。
	// 支持 admin 和 subuser 创建；subuser 仅能看到自己的 + admin 共享的。
	Recipes []Recipe `json:"recipes,omitempty"`
	// Webhooks 事件订阅端点（企业集成：容器状态变更回调，类比 AWS EventBridge / GitHub Webhooks）。
	// 每次容器状态变化（running/stopped）会向订阅 URL POST 签名 JSON 载荷。
	Webhooks []WebhookSubscription `json:"webhooks,omitempty"`

	// ScheduledActions 容器级定时启停任务（对齐主流面板语义）。
	// 由主控定时巡检：ExecuteAt 到达且 Enabled=true 时调用容器启/停/重启/硬关机。
	// 每容器最多 10 条，由创建者在请求接口按 container:power scope 写入。
	ScheduledActions []ScheduledAction `json:"scheduled_actions,omitempty"`

	// LoginFooterText 登录页底部版权栏的自定义文字；留空时前端显示默认版权
	// （© <年份> EyvesCloud. All rights reserved.）。
	LoginFooterText string `json:"login_footer_text,omitempty"`
	// LoginFooterHidden 为 true 时登录页底部版权栏完全不渲染。
	LoginFooterHidden bool `json:"login_footer_hidden,omitempty"`
	// 白标品牌（面向"授权给其它公司运营"场景）：全部留空 = EyvesCloud 默认。
	// BrandName 出现在登录页/侧边栏/document.title/邮件抬头/页脚；
	// BrandLogo 为 data URL（PNG/SVG 的 base64），空 = 默认图标。
	// BrandPoweredHidden = true 时页脚不再显示 "Powered by EyvesCloud"（付费授权）。
	BrandName          string `json:"brand_name,omitempty"`
	BrandLogo          string `json:"brand_logo,omitempty"`
	BrandFavicon       string `json:"brand_favicon,omitempty"`
	BrandLoginTitle    string `json:"brand_login_title,omitempty"`
	BrandPoweredHidden bool   `json:"brand_powered_hidden,omitempty"`
}

// ScheduledAction 容器级定时任务（与 主流面板 act=self_shutdown 对齐）。
type ScheduledAction struct {
	ID            string `json:"id"`
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name,omitempty"`
	// Type 任务类型：start/stop/restart/poweroff
	Type   string `json:"type"`
	Repeat string `json:"repeat,omitempty"` // none/daily/weekly/monthly
	// ExecuteAt 下次执行时间（RFC3339）。Repeat=weekly 时仅用于首次；
	// repeat=daily/weekly/monthly 时由主控 cron 计算下次。
	ExecuteAt string `json:"execute_at"`
	// LastRunAt 最近一次执行时间，omitempty
	LastRunAt string `json:"last_run_at,omitempty"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// WebhookSubscription 是一个事件订阅端点。
// 安全模型：
//   - URL 仅允许 http/https 且长度受限；
//   - Secret 用于 HMAC-SHA256 签名（X-EyvesCloud-Signature 头），接收方验签防伪造；
//   - 连续失败 10 次自动停用（AutoDisabledReason 记录原因），防止雪崩重试；
//   - 重试：3 次指数退避（1s/5s/25s），全部失败计入 ConsecutiveFailures。
type WebhookSubscription struct {
	ID                  string   `json:"id"` // wh-xxx
	Name                string   `json:"name"`
	URL                 string   `json:"url"`                   // 回调端点（http/https）
	Secret              string   `json:"secret,omitempty"`      // HMAC 签名密钥（创建时生成，仅回显一次）
	EventTypes          []string `json:"event_types,omitempty"` // 订阅的事件类型；空 = 全部
	Enabled             bool     `json:"enabled"`
	ConsecutiveFailures int      `json:"consecutive_failures,omitempty"`
	LastDeliveryAt      string   `json:"last_delivery_at,omitempty"`     // RFC3339
	LastDeliveryStatus  string   `json:"last_delivery_status,omitempty"` // ok / error: xxx
	AutoDisabledReason  string   `json:"auto_disabled_reason,omitempty"`
	CreatedAt           string   `json:"created_at"`
	// OwnerSubject 标记该订阅由哪个主体创建：admin 登录创建时为空（全局可见，
	// 仅 admin 可改/删），sub-user 或受限 API Key 创建时填入 Actor 标识，仅
	// 创建者本人或 admin 可读/改/删，防止越权修改他人的回调订阅。
	OwnerSubject string `json:"owner_subject,omitempty"`
	// OwnerType 标记创建者的认证类型：admin / sub-user / api-key，便于审计。
	OwnerType string `json:"owner_type,omitempty"`
}

// KSMTuningConfig 控制 Linux KSM（Kernel Samepage Merging）调优，用于在内存超售
// 场景下合并重复内存页、降低实际占用。写入 /sys/kernel/mm/ksm/*。
type KSMTuningConfig struct {
	Enabled        bool `json:"enabled"`
	PagesToScan    int  `json:"pages_to_scan"`   // ksm/pages_to_scan
	SleepMillisecs int  `json:"sleep_millisecs"` // ksm/sleep_millisecs
	UseTuneKSM     bool `json:"use_tune_ksm"`    // 若系统有 tuneksm 则优先使用
}

const (
	KVMProvisionerLinuxCloudInit = "linux-cloud-init"
	KVMProvisionerWindows10      = "windows-10"
	KVMProvisionerWindows11      = "windows-11"
)

// CustomKVMImage is an administrator-defined KVM image source.
type CustomKVMImage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Distro      string `json:"distro"`
	Release     string `json:"release"`
	Arch        string `json:"arch"`
	URL         string `json:"url"`
	Provisioner string `json:"provisioner"`
	SHA256      string `json:"sha256,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// CustomLXCImage is an administrator-defined LXC rootfs archive source.
type CustomLXCImage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Distro      string `json:"distro"`
	Release     string `json:"release"`
	Arch        string `json:"arch"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256,omitempty"`
	CreatedAt   string `json:"created_at"`
}

var configPath string
var AppConfig *EyvescloudConfig
var allocationMu sync.Mutex

// agentToken is the node token issued by the controller. In agent mode it is
// used to authenticate controller->agent API calls (/api/agent/*).
var agentToken string

// SetAgentToken stores the agent token for this node.
func SetAgentToken(token string) {
	agentToken = token
}

// AgentToken returns the configured agent token ("" when not in agent mode).
func AgentToken() string {
	return agentToken
}

// AppConfigMu guards concurrent access to the in-memory AppConfig graph.
// Background goroutines (policy engine, metric sampler, security scanner,
// expiry scanner) and HTTP handlers mutate the same slices, so every read
// snapshot and every mutation must hold this lock. Lock order convention:
// always acquire AppConfigMu before dbMu (SaveConfig path), never the reverse.
var AppConfigMu sync.RWMutex

const DefaultSnapshotLimit = 3

// FirstBootCredsFile 是首次启动时写入 DataDir 下的临时凭据文件名。
// 主管理员首次成功登录后应由 HandleLogin 删除，避免密钥长期留盘。
const FirstBootCredsFile = "initial-admin-credentials.txt"

const (
	DefaultTaskConcurrency = 2
	MaxTaskConcurrency     = 16
)

const (
	DefaultNATPortStart = 20000
	DefaultNATPortEnd   = 65535
)

func getConfigPath() string {
	if configPath != "" {
		return configPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return filepath.Join(home, ".eyvescloud", "config.json")
}

func SetConfigPath(path string) {
	configPath = path
}

func getDataDir() string {
	// EYVESCLOUD_DATA_DIR 允许运维与测试显式重定向数据目录（与
	// EYVESCLOUD_LXC_SUBNET 等环境变量风格一致）；未设置时保持原默认。
	if dir := strings.TrimSpace(os.Getenv("EYVESCLOUD_DATA_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return filepath.Join(home, ".eyvescloud")
}

func generateRandomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

// randomAlnum 生成 length 位随机小写字母+数字（字母表 36 字符，比 hex 更难枚举）。
func randomAlnum(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见；退回 hex 保证可用性。
		return hex.EncodeToString(b)[:length]
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// GenerateRandomAdminPath 返回随机化管理员入口路径（/admin- + 12 位字母数字）。
// 默认不占用 /、/admin、/login 等可猜测路径，管理入口无法被枚举发现。
func GenerateRandomAdminPath() string {
	return "/admin-" + randomAlnum(12)
}

// randomShortID 返回一个 6 字符的小写十六进制短 ID，用于 NodeGroup / Cluster 等内部标识。
func randomShortID() string {
	return generateRandomString(6)
}

func generateUUIDString() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return generateRandomString(32)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewContainerUUID returns a UUID that is unique within the current config.
func NewContainerUUID() string {
	for {
		uuid := generateUUIDString()
		if FindContainerByUUID(uuid) == nil {
			return uuid
		}
	}
}

// newContainerUUIDUnlocked returns a unique UUID. The caller must already hold
// AppConfigMu (write lock). It must not call FindContainerByUUID, which would
// attempt to re-acquire the lock and deadlock the writer.
func newContainerUUIDUnlocked() string {
	for {
		uuid := generateUUIDString()
		found := false
		for i := range AppConfig.Containers {
			if AppConfig.Containers[i].UUID == uuid {
				found = true
				break
			}
		}
		if !found {
			return uuid
		}
	}
}

// NewVolumeID 生成全局唯一的卷 ID（vol-<uuid>），用于 P0-1 存储抽象层。
// 卷 ID 同时是 dir 后端下卷目录的名字，因此不含路径分隔符。
func NewVolumeID() string {
	for {
		id := "vol-" + generateUUIDString()
		if _, exists := GetVolume(id); !exists {
			return id
		}
	}
}

// InitConfig initializes or loads the configuration
func InitConfig() (*EyvescloudConfig, error) {
	cfgPath := getConfigPath()
	dataDir := getDataDir()

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}
	if err := openConfigDB(); err != nil {
		return nil, err
	}

	cfg, ok, err := loadConfigFromDB()
	if err != nil {
		return nil, err
	}
	if ok {
		AppConfig = cfg
		changed := normalizeConfigDefaults(dataDir)
		if migrateLoadedConfig() {
			changed = true
		}
		if changed {
			if err := SaveConfig(); err != nil {
				return nil, err
			}
		}
		return AppConfig, nil
	}

	legacy, ok, err := loadLegacyJSONConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	if ok {
		AppConfig = legacy
		normalizeConfigDefaults(dataDir)
		migrateLoadedConfig()
		// Always save legacy JSON data into SQLite.
		if err := SaveConfig(); err != nil {
			return nil, err
		}
		return AppConfig, nil
	}

	adminUser := "admin"
	adminPass := generateRandomString(16)
	jwtSecret := generateRandomString(32)
	// 管理员入口默认随机化（/admin- + 12 位字母数字），避免被枚举。
	adminPath := GenerateRandomAdminPath()
	// 首装即自带「节点对接密钥」（一次性、24h 有效）：主控+被控开箱模式下，
	// 安装脚本可直接把地址+密钥展示给运维，免去登录面板手动生成一步。
	pairingKey := generateRandomString(64)
	pairingKeyExpiry := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %v", err)
	}

	AppConfig = &EyvescloudConfig{
		AdminUser:            adminUser,
		AdminPassHash:        string(hash),
		JWTSecret:            jwtSecret,
		AdminPath:            adminPath,
		Port:                 8999,
		DataDir:              dataDir,
		Containers:           []Container{},
		NextContainerID:      1,
		NextVNCPort:          5900,
		NextSSHPort:          22000,
		NATPortStart:         DefaultNATPortStart,
		NATPortEnd:           DefaultNATPortEnd,
		LXCNATSubnet:         configuredSubnetValue("", "EYVESCLOUD_LXC_SUBNET", DefaultLXCNATSubnet),
		KVMNATSubnet:         configuredSubnetValue("", "EYVESCLOUD_KVM_SUBNET", DefaultKVMNATSubnet),
		SetupComplete:        false,
		SubUsers:             []SubUser{},
		AuditLogs:            []AuditLog{},
		Tasks:                []SavedTask{},
		LoginLogs:            []SavedLoginLog{},
		Snapshots:            []Snapshot{},
		PublicIPv4Pool:       []PublicIPv4Assignment{},
		PublicIPv6Prefixes:   []PublicIPv6Prefix{},
		WebSSHAllowedOrigins: []string{},
		PanelAccessPolicy: PanelAccessPolicy{
			AllowedSources: []string{},
			TrustedProxies: []string{},
		},
		TaskConcurrency:     DefaultTaskConcurrency,
		StoragePools:        []StoragePool{defaultPrimaryStoragePool()},
		MetricRetentionDays: MetricRetentionDefault,
		AuditRetentionDays:  AuditRetentionDefault,
		BackupSettings: BackupSettings{
			Enabled:       false,
			IntervalHours: 24,
			Keep:          14,
			Directory:     filepath.Join(dataDir, "backups"),
		},
		APIRateLimit: APIRateLimitConfig{
			Enabled:   false,
			PerMinute: 120,
		},
		Tenants:                 []Tenant{},
		MemoryOvercommitEnabled: false,
		MemoryOvercommitRatio:   1.0,
		KSMTuning: KSMTuningConfig{
			Enabled:        false,
			PagesToScan:    100,
			SleepMillisecs: 20,
			UseTuneKSM:     true,
		},
		NATSubnetOversubscription: false,
		DiskOvercommitRatio:       1.0,
		AgentPairingKey:           pairingKey,
		AgentPairingKeyExpiry:     pairingKeyExpiry,
		UpdateSource:              NormalizeUpdateSource(UpdateSource{}),
	}

	if err := SaveConfig(); err != nil {
		return nil, err
	}

	// 安全加固：首次启动生成的随机口令不应走 stdout（会被 systemd/journald / Docker
	// 日志 捕获）。改为写入 DataDir 下 0600 权限的凭据文件，由安装脚本 / 运维人员
	// 手动查看并立即删除。
	firstBootCreds := filepath.Join(dataDir, FirstBootCredsFile)
	if err := os.WriteFile(firstBootCreds, []byte(fmt.Sprintf(
		"# EyvesCloud initial admin credentials - DELETE after first login\nUsername: %s\nPassword: %s\nAdmin login path: %s\nChangedAt: \n\n# Node pairing key (adopt this panel as an agent node; one-time, valid 24h)\nNode pairing key: %s\nNode pairing key expiry: %s\n",
		adminUser, adminPass, adminPath, pairingKey, pairingKeyExpiry)), 0600); err == nil {
		// 目录已经是 0700；额外 chmod 一道以防 umask 意外放开。
		_ = os.Chmod(firstBootCreds, 0600)
		fmt.Println("\n========================================")
		fmt.Println("  EyvesCloud - First Boot Credentials")
		fmt.Println("========================================")
		fmt.Printf("  Credentials file: %s (mode 0600)\n", firstBootCreds)
		fmt.Println("  Read it once, then delete the file.")
		fmt.Printf("  Admin login path: %s (randomized, see credentials file)\n", adminPath)
		fmt.Println("  User portal: http://0.0.0.0:8999/user/login")
		fmt.Println("  AND change the password / enable 2FA immediately.")
		fmt.Println("========================================")
		fmt.Println()
	} else {
		// 凭据文件落盘失败时，退回到 stdout 但用醒目红字提示风险。
		fmt.Println("\n========================================")
		fmt.Println("  EyvesCloud - FIRST BOOT (SECURITY WARNING)")
		fmt.Println("========================================")
		fmt.Println("  Credentials file could not be written securely.")
		// 绝不把明文密码写进非交互式 stdout（systemd 会落盘到 journald，造成长期
		// 泄露）：仅当 stdout 是交互式终端（字符设备）时才打印密码，否则只给出
		// 告警与恢复指引。
		if isInteractiveStdout() {
			fmt.Println("  The generated password is shown below — save it NOW")
			fmt.Println("  and change it on first login.")
			fmt.Printf("  Username: %s\n", adminUser)
			fmt.Printf("  Password: %s\n", adminPass)
		} else {
			fmt.Println("  Refusing to print the password to a non-interactive stream")
			fmt.Println("  (it would be persisted to logs).")
			fmt.Printf("  Username: %s\n", adminUser)
			fmt.Println("  Reset it now with: eyvescloud account reset")
		}
		fmt.Printf("  Admin login path: %s\n", adminPath)
		fmt.Println("========================================")
		fmt.Println()
	}

	return AppConfig, nil
}

// isInteractiveStdout 报告 stdout 是否为交互式终端（字符设备）。
// 用于避免把一次性密码写入 journald 等非交互式日志流。
func isInteractiveStdout() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func normalizeConfigDefaults(dataDir string) bool {
	changed := false
	// 存量部署升级：admin_path 为空或为旧默认值 "/"（挂在根路径）时自动随机化，
	// 并把新入口写入凭据文件 + 醒目启动日志，避免管理员被锁在门外。
	// 管理员如需自定义入口，登录后在「设置」里改为其它路径即可。
	if p := strings.TrimSpace(AppConfig.AdminPath); p == "" || p == "/" {
		AppConfig.AdminPath = GenerateRandomAdminPath()
		changed = true
		noticePath := filepath.Join(dataDir, FirstBootCredsFile)
		if dataDir != "" {
			if err := os.WriteFile(noticePath, []byte(fmt.Sprintf(
				"# EyvesCloud admin login path (upgraded) - DELETE after reading\n"+
					"Admin login path: %s\n"+
					"Admin username:   %s\n"+
					"(password unchanged from your old install)\n"+
					"User portal:      /user/login\n"+
					"Quick view: run `vm` or `eyvescloud account show`\n",
				AppConfig.AdminPath, AppConfig.AdminUser)), 0600); err == nil {
				_ = os.Chmod(noticePath, 0600)
			}
		}
		// 无论凭据文件写成功与否，都在启动日志里显眼打印——老用户升级后
		// 访问原根路径会被导到用户入口，必须能在 stdout 一眼找到新入口。
		fmt.Fprintln(os.Stderr, "========================================")
		fmt.Fprintln(os.Stderr, "  [UPGRADE] Admin login path randomized")
		if AppConfig.PanelDomain != "" {
			fmt.Fprintf(os.Stderr, "  New admin URL:   %s%s\n", AppConfig.PanelDomain, AppConfig.AdminPath)
		} else {
			fmt.Fprintf(os.Stderr, "  New admin path:  %s  (visit http(s)://<server-ip>:%d%s)\n", AppConfig.AdminPath, AppConfig.Port, AppConfig.AdminPath)
		}
		fmt.Fprintf(os.Stderr, "  Admin username:  %s\n", AppConfig.AdminUser)
		fmt.Fprintln(os.Stderr, "  Run `vm` or `eyvescloud account show` anytime to re-view")
		fmt.Fprintln(os.Stderr, "========================================")
	}
	if AppConfig.Port == 0 {
		AppConfig.Port = 8999
		changed = true
	}
	if AppConfig.NextVNCPort == 0 {
		AppConfig.NextVNCPort = 5900
		changed = true
	}
	if AppConfig.NextSSHPort == 0 {
		AppConfig.NextSSHPort = 22000
		changed = true
	}
	if normalizeNATPortRangeDefaults() {
		changed = true
	}
	if normalizeNATNetworkDefaults() {
		changed = true
	}
	if AppConfig.NextContainerID == 0 {
		AppConfig.NextContainerID = 1
		changed = true
	}
	if normalized := NormalizeTaskConcurrency(AppConfig.TaskConcurrency); AppConfig.TaskConcurrency != normalized {
		AppConfig.TaskConcurrency = normalized
		changed = true
	}
	if AppConfig.DataDir == "" {
		AppConfig.DataDir = dataDir
		changed = true
	}
	if AppConfig.Containers == nil {
		AppConfig.Containers = make([]Container, 0)
		changed = true
	}
	if AppConfig.Snapshots == nil {
		AppConfig.Snapshots = make([]Snapshot, 0)
		changed = true
	}
	if AppConfig.PublicIPv4Pool == nil {
		AppConfig.PublicIPv4Pool = make([]PublicIPv4Assignment, 0)
		changed = true
	}
	if AppConfig.PublicIPv6Prefixes == nil {
		AppConfig.PublicIPv6Prefixes = make([]PublicIPv6Prefix, 0)
		changed = true
	}
	if AppConfig.WebSSHAllowedOrigins == nil {
		AppConfig.WebSSHAllowedOrigins = make([]string, 0)
		changed = true
	} else if normalized, err := NormalizeAllowedOrigins(AppConfig.WebSSHAllowedOrigins); err == nil && strings.Join(normalized, "\n") != strings.Join(AppConfig.WebSSHAllowedOrigins, "\n") {
		AppConfig.WebSSHAllowedOrigins = normalized
		changed = true
	}
	if normalized, err := NormalizePanelAccessPolicy(AppConfig.PanelAccessPolicy); err == nil {
		if !panelAccessPoliciesEqual(AppConfig.PanelAccessPolicy, normalized) {
			AppConfig.PanelAccessPolicy = normalized
			changed = true
		}
	} else {
		AppConfig.PanelAccessPolicy = PanelAccessPolicy{
			AllowedSources: []string{},
			TrustedProxies: []string{},
		}
		changed = true
	}
	if len(AppConfig.StoragePools) == 0 {
		AppConfig.StoragePools = []StoragePool{defaultPrimaryStoragePool()}
		changed = true
	}
	if normalizeStoragePools() {
		changed = true
	}
	if AppConfig.SubUsers == nil {
		AppConfig.SubUsers = make([]SubUser, 0)
		changed = true
	}
	if AppConfig.Admins == nil {
		AppConfig.Admins = make([]AdminAccount, 0)
		changed = true
	} else {
		for i := range AppConfig.Admins {
			if normalized := NormalizeAdminRole(AppConfig.Admins[i].Role); normalized != AppConfig.Admins[i].Role {
				AppConfig.Admins[i].Role = normalized
				changed = true
			}
		}
	}
	if normalized, ok := NormalizeAdminPath(AppConfig.AdminPath); !ok {
		// 非法值（含历史脏数据）一律回落默认路径，避免面板入口不可达。
		if AppConfig.AdminPath != DefaultAdminPath {
			AppConfig.AdminPath = DefaultAdminPath
			changed = true
		}
	} else if normalized != AppConfig.AdminPath {
		AppConfig.AdminPath = normalized
		changed = true
	}
	if AppConfig.ApiKeys == nil {
		AppConfig.ApiKeys = make([]ApiKeyConfig, 0)
		changed = true
	} else {
		for i := range AppConfig.ApiKeys {
			// 空 scope 的 Key 保持为空（无任何权限），绝不默认升级为全权限 "*"。
			if AppConfig.ApiKeys[i].Scopes == nil {
				AppConfig.ApiKeys[i].Scopes = []string{}
				changed = true
			}
		}
	}
	if AppConfig.AuditLogs == nil {
		AppConfig.AuditLogs = make([]AuditLog, 0)
		changed = true
	}
	if AppConfig.Tasks == nil {
		AppConfig.Tasks = make([]SavedTask, 0)
		changed = true
	}
	if AppConfig.LoginLogs == nil {
		AppConfig.LoginLogs = make([]SavedLoginLog, 0)
		changed = true
	}
	if AppConfig.Nodes == nil {
		AppConfig.Nodes = make([]Node, 0)
		changed = true
	}
	if AppConfig.EnabledImages == nil {
		AppConfig.EnabledImages = make([]string, 0)
		changed = true
	}
	if AppConfig.CustomKVMImages == nil {
		AppConfig.CustomKVMImages = make([]CustomKVMImage, 0)
		changed = true
	}
	if AppConfig.CustomLXCImages == nil {
		AppConfig.CustomLXCImages = make([]CustomLXCImage, 0)
		changed = true
	}
	if AppConfig.Regions == nil {
		AppConfig.Regions = make([]Region, 0)
		changed = true
	}
	if AppConfig.NodeGroups == nil {
		AppConfig.NodeGroups = make([]NodeGroup, 0)
		changed = true
	} else {
		for i := range AppConfig.NodeGroups {
			if AppConfig.NodeGroups[i].ID == "" {
				AppConfig.NodeGroups[i].ID = "ng-" + randomShortID()
				changed = true
			}
		}
	}
	if AppConfig.Clusters == nil {
		AppConfig.Clusters = make([]Cluster, 0)
		changed = true
	} else {
		for i := range AppConfig.Clusters {
			if AppConfig.Clusters[i].ID == "" {
				AppConfig.Clusters[i].ID = "cl-" + randomShortID()
				changed = true
			}
		}
	}
	// Node 引用完整性：清空不存在的 NodeGroupID / ClusterID；推断空 VirtTypes。
	nodeGroupIDs := map[string]bool{}
	for _, ng := range AppConfig.NodeGroups {
		nodeGroupIDs[ng.ID] = true
	}
	clusterIDs := map[string]bool{}
	for _, cl := range AppConfig.Clusters {
		clusterIDs[cl.ID] = true
	}
	for i := range AppConfig.Nodes {
		if AppConfig.Nodes[i].NodeGroupID != "" && !nodeGroupIDs[AppConfig.Nodes[i].NodeGroupID] {
			AppConfig.Nodes[i].NodeGroupID = ""
			changed = true
		}
		if AppConfig.Nodes[i].ClusterID != "" && !clusterIDs[AppConfig.Nodes[i].ClusterID] {
			AppConfig.Nodes[i].ClusterID = ""
			changed = true
		}
		if len(AppConfig.Nodes[i].VirtTypes) == 0 {
			AppConfig.Nodes[i].VirtTypes = []string{"lxc", "kvm"}
			changed = true
		}
	}
	if AppConfig.IPGroups == nil {
		AppConfig.IPGroups = make([]IPGroup, 0)
		changed = true
	}
	if AppConfig.ISOFiles == nil {
		AppConfig.ISOFiles = make([]ISOFile, 0)
		changed = true
	}
	if AppConfig.MetricRetentionDays < 0 {
		AppConfig.MetricRetentionDays = MetricRetentionDefault
		changed = true
	}
	if AppConfig.Language == "" {
		AppConfig.Language = "zh"
		changed = true
	}
	if AppConfig.Language != "zh" && AppConfig.Language != "en" {
		AppConfig.Language = "zh"
		changed = true
	}
	if AppConfig.AuditRetentionDays == 0 {
		AppConfig.AuditRetentionDays = AuditRetentionDefault
		changed = true
	}
	if AppConfig.BackupSettings.IntervalHours <= 0 {
		AppConfig.BackupSettings.IntervalHours = 24
		changed = true
	}
	if AppConfig.BackupSettings.Keep <= 0 {
		AppConfig.BackupSettings.Keep = 14
		changed = true
	}
	if AppConfig.BackupSettings.Directory == "" {
		AppConfig.BackupSettings.Directory = filepath.Join(AppConfig.DataDir, "backups")
		changed = true
	}
	if AppConfig.APIRateLimit.PerMinute <= 0 {
		AppConfig.APIRateLimit.PerMinute = 120
		changed = true
	}
	if AppConfig.Tenants == nil {
		AppConfig.Tenants = make([]Tenant, 0)
		changed = true
	}
	if AppConfig.MemoryOvercommitRatio <= 0 {
		AppConfig.MemoryOvercommitRatio = 1.0
		changed = true
	}
	if !AppConfig.KSMTuning.Enabled && AppConfig.KSMTuning.PagesToScan == 0 {
		AppConfig.KSMTuning.PagesToScan = 100
		AppConfig.KSMTuning.SleepMillisecs = 20
		changed = true
	}
	if AppConfig.Backups == nil {
		AppConfig.Backups = make([]BackupRecord, 0)
		changed = true
	}
	if AppConfig.InstanceBackups == nil {
		AppConfig.InstanceBackups = make([]InstanceBackup, 0)
		changed = true
	}
	if normalizeSSLDefaults() {
		changed = true
	}
	return changed
}

func NormalizeTaskConcurrency(value int) int {
	if value <= 0 {
		return DefaultTaskConcurrency
	}
	if value > MaxTaskConcurrency {
		return MaxTaskConcurrency
	}
	return value
}

func NormalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "en-us", "en_us", "english":
		return "en"
	default:
		return "zh"
	}
}

func normalizeSSLDefaults() bool {
	changed := false
	previousMode := AppConfig.SSL.Mode
	AppConfig.SSL.Mode = NormalizeSSLMode(AppConfig.SSL.Mode)
	if AppConfig.SSL.Mode != previousMode {
		changed = true
	}
	if AppConfig.SSL.Mode == SSLModeDisabled {
		if AppConfig.SSL.Enabled {
			changed = true
		}
		AppConfig.SSL.Enabled = false
	}
	if AppConfig.SSLCertificates == nil {
		AppConfig.SSLCertificates = map[string]SSLConfig{}
		changed = true
	}
	for mode, cert := range AppConfig.SSLCertificates {
		cert.Mode = NormalizeSSLMode(cert.Mode)
		if cert.Mode == SSLModeDisabled {
			delete(AppConfig.SSLCertificates, mode)
			changed = true
			continue
		}
		if AppConfig.SSLCertificates[cert.Mode] != cert {
			changed = true
		}
		AppConfig.SSLCertificates[cert.Mode] = cert
		if mode != cert.Mode {
			delete(AppConfig.SSLCertificates, mode)
			changed = true
		}
	}
	if AppConfig.SSL.Mode != SSLModeDisabled && AppConfig.SSL.CertPath != "" && AppConfig.SSL.KeyPath != "" {
		cert := AppConfig.SSL
		cert.Enabled = false
		if AppConfig.SSLCertificates[cert.Mode] != cert {
			changed = true
		}
		AppConfig.SSLCertificates[cert.Mode] = cert
	}
	return changed
}

func NormalizeSSLMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SSLModeLetsEncrypt:
		return SSLModeLetsEncrypt
	case SSLModeSelfSigned:
		return SSLModeSelfSigned
	case SSLModeUploaded:
		return SSLModeUploaded
	default:
		return SSLModeDisabled
	}
}

func migrateLoadedConfig() bool {
	changed := ensureContainerUUIDs()
	if ensureContainerVirtualization() {
		changed = true
	}
	if ensureContainerPortMappingLimits() {
		changed = true
	}
	if ensureContainerSnapshotLimits() {
		changed = true
	}
	if ensureContainerNetworkAssignments() {
		changed = true
	}
	if ensureContainerResourceAliases() {
		changed = true
	}
	if ensureContainerSnapshotScheduleDefaults() {
		changed = true
	}
	if migrateSubUsers() {
		changed = true
	}
	if migrateContainerOwnerSubUserIDs() {
		changed = true
	}
	if removeLegacyVNCMappings() {
		changed = true
	}
	return changed
}

func ensureContainerVirtualization() bool {
	changed := false
	for i := range AppConfig.Containers {
		next := NormalizeVirtualization(AppConfig.Containers[i].Virtualization)
		if AppConfig.Containers[i].Virtualization != next {
			AppConfig.Containers[i].Virtualization = next
			changed = true
		}
	}
	return changed
}

func ensureContainerSnapshotScheduleDefaults() bool {
	changed := false
	for i := range AppConfig.Containers {
		if AppConfig.Containers[i].SnapshotScheduleEnabled && AppConfig.Containers[i].SnapshotScheduleIntervalHours < 24 {
			AppConfig.Containers[i].SnapshotScheduleIntervalHours = 24
			changed = true
		}
		if AppConfig.Containers[i].SnapshotScheduleEnabled && AppConfig.Containers[i].SnapshotScheduleTime == "" {
			AppConfig.Containers[i].SnapshotScheduleTime = "03:00"
			changed = true
		}
	}
	return changed
}

func ensureContainerUUIDs() bool {
	changed := false
	used := make(map[string]bool)
	for i := range AppConfig.Containers {
		uuid := AppConfig.Containers[i].UUID
		if uuid == "" || used[uuid] {
			for {
				uuid = generateUUIDString()
				if !used[uuid] {
					break
				}
			}
			AppConfig.Containers[i].UUID = uuid
			changed = true
		}
		used[uuid] = true
	}
	return changed
}

func ensureContainerPortMappingLimits() bool {
	changed := false
	for i := range AppConfig.Containers {
		if AppConfig.Containers[i].PortMappingLimit < 0 {
			limit := len(AppConfig.Containers[i].PortMappings)
			if limit < 2 {
				limit = 2
			}
			AppConfig.Containers[i].PortMappingLimit = limit
			changed = true
		} else if AppConfig.Containers[i].PortMappingLimit == 0 && len(AppConfig.Containers[i].PortMappings) > 0 {
			AppConfig.Containers[i].PortMappingLimit = len(AppConfig.Containers[i].PortMappings)
			changed = true
		}
	}
	return changed
}

func ensureContainerSnapshotLimits() bool {
	changed := false
	for i := range AppConfig.Containers {
		if AppConfig.Containers[i].SnapshotLimit <= 0 {
			AppConfig.Containers[i].SnapshotLimit = DefaultSnapshotLimit
			changed = true
		}
	}
	return changed
}

func ensureContainerNetworkAssignments() bool {
	changed := false
	for i := range AppConfig.Containers {
		if AppConfig.Containers[i].NormalizeNetworkAssignments() {
			changed = true
		}
	}
	return changed
}

func ensureContainerResourceAliases() bool {
	changed := false
	for i := range AppConfig.Containers {
		if NormalizeContainerResourceAliases(&AppConfig.Containers[i]) {
			changed = true
		}
	}
	return changed
}

func NormalizeContainerResourceAliases(c *Container) bool {
	if c == nil {
		return false
	}
	changed := false
	if c.NetworkBWMbps < 0 {
		c.NetworkBWMbps = 0
		changed = true
	}
	if c.NetworkDownMbps < 0 {
		c.NetworkDownMbps = 0
		changed = true
	}
	if c.NetworkUpMbps < 0 {
		c.NetworkUpMbps = 0
		changed = true
	}
	if c.NetworkDownMbps == 0 && c.NetworkUpMbps == 0 && c.NetworkBWMbps > 0 {
		c.NetworkDownMbps = c.NetworkBWMbps
		c.NetworkUpMbps = c.NetworkBWMbps
		changed = true
	}
	nextNetworkBW := LegacySymmetricLimit(c.NetworkDownMbps, c.NetworkUpMbps)
	if c.NetworkBWMbps != nextNetworkBW {
		c.NetworkBWMbps = nextNetworkBW
		changed = true
	}

	if c.IOSpeedMBps < 0 {
		c.IOSpeedMBps = 0
		changed = true
	}
	if c.IOReadMBps < 0 {
		c.IOReadMBps = 0
		changed = true
	}
	if c.IOWriteMBps < 0 {
		c.IOWriteMBps = 0
		changed = true
	}
	if c.IOReadMBps == 0 && c.IOWriteMBps == 0 && c.IOSpeedMBps > 0 {
		c.IOReadMBps = c.IOSpeedMBps
		c.IOWriteMBps = c.IOSpeedMBps
		changed = true
	}
	nextIO := LegacySymmetricLimit(c.IOReadMBps, c.IOWriteMBps)
	if c.IOSpeedMBps != nextIO {
		c.IOSpeedMBps = nextIO
		changed = true
	}
	return changed
}

func LegacySymmetricLimit(a, b int) int {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a == b {
		return a
	}
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func migrateSubUsers() bool {
	changed := false
	for i := range AppConfig.SubUsers {
		su := &AppConfig.SubUsers[i]
		if su.PassHash == "" && su.Password != "" {
			if hash, err := bcrypt.GenerateFromPassword([]byte(su.Password), bcrypt.DefaultCost); err == nil {
				su.PassHash = string(hash)
				changed = true
			}
		}
		if su.Token != "" {
			su.Token = ""
			changed = true
		}
		if len(su.ContainerUUIDs) == 0 && len(su.ContainerNames) > 0 {
			for _, name := range su.ContainerNames {
				// 使用不加锁的查找：migrateLoadedConfig 也可能在 ReconcileConfig 持有写锁时被调用，
				// 若此处再取 AppConfigMu.RLock 会构成递归锁并死锁。
				if c := findContainerByNameUnlocked(name); c != nil && c.UUID != "" {
					su.ContainerUUIDs = appendUniqueString(su.ContainerUUIDs, c.UUID)
				}
			}
			if len(su.ContainerUUIDs) > 0 {
				changed = true
			}
		}
	}
	return changed
}

// migrateContainerOwnerSubUserIDs 从 SubUser.ContainerUUIDs 反推 Container.OwnerSubUserID。
// 只给那些只被**一个** SubUser 绑的 Container 自动回填 owner；若被多个 SubUser 共享则跳过。
// 不加锁——migrateLoadedConfig 调用时已持有写锁。
func migrateContainerOwnerSubUserIDs() bool {
	changed := false
	// 先统计每个 container UUID 被哪些 subuser 引用
	type ownerVote struct {
		subUserIDs []string
	}
	votes := map[string]*ownerVote{}
	for si := range AppConfig.SubUsers {
		su := &AppConfig.SubUsers[si]
		for _, uuid := range su.ContainerUUIDs {
			if uuid == "" {
				continue
			}
			v, ok := votes[uuid]
			if !ok {
				v = &ownerVote{}
				votes[uuid] = v
			}
			v.subUserIDs = append(v.subUserIDs, su.ID)
		}
	}
	// 回填 Container.OwnerSubUserID
	for ci := range AppConfig.Containers {
		c := &AppConfig.Containers[ci]
		if c.OwnerSubUserID != "" {
			continue
		}
		v, ok := votes[c.UUID]
		if !ok || len(v.subUserIDs) != 1 {
			continue
		}
		c.OwnerSubUserID = v.subUserIDs[0]
		changed = true
	}
	return changed
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func NormalizeSnapshotLimit(limit int) int {
	if limit <= 0 {
		return DefaultSnapshotLimit
	}
	return limit
}

func ContainerSnapshotLimit(c *Container) int {
	if c == nil {
		return DefaultSnapshotLimit
	}
	return NormalizeSnapshotLimit(c.SnapshotLimit)
}

func removeLegacyVNCMappings() bool {
	changed := false
	for i := range AppConfig.Containers {
		mappings := AppConfig.Containers[i].PortMappings
		if len(mappings) == 0 {
			continue
		}

		filtered := mappings[:0]
		for _, pm := range mappings {
			isLegacyVNC := strings.EqualFold(pm.Description, "VNC") || pm.ContainerPort == 5901
			if isLegacyVNC {
				changed = true
				continue
			}
			filtered = append(filtered, pm)
		}
		AppConfig.Containers[i].PortMappings = filtered
	}
	return changed
}

// SaveConfig saves configuration to disk. It holds AppConfigMu for the whole
// serialization so background readers observe a consistent snapshot.
func SaveConfig() error {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	// 测试 teardown 会把 AppConfig 还原为 nil；后台任务队列 goroutine
	// 此刻仍可能触发落库，直接返回错误而不是 panic。
	if AppConfig == nil {
		return fmt.Errorf("config is not initialized")
	}
	return saveConfigToDB()
}

// CloseConfigDB 关闭 SQLite 连接（用于服务优雅停机与测试中重放启动迁移）。
// 之后的再次调用会重新打开数据库并重跑 ensureSchema 迁移（幂等）。
func CloseConfigDB() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		_ = db.Close()
		db = nil
	}
}

func ListCustomKVMImages() []CustomKVMImage {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	return append([]CustomKVMImage(nil), AppConfig.CustomKVMImages...)
}

// FindCustomKVMImage returns a copy of a custom KVM image by ID, or nil.
func FindCustomKVMImage(id string) *CustomKVMImage {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	for i := range AppConfig.CustomKVMImages {
		if AppConfig.CustomKVMImages[i].ID == id {
			img := AppConfig.CustomKVMImages[i]
			return &img
		}
	}
	return nil
}

func AddCustomKVMImage(image CustomKVMImage) error {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	for _, existing := range AppConfig.CustomKVMImages {
		if existing.ID == image.ID {
			return fmt.Errorf("custom KVM image %q already exists", image.ID)
		}
	}
	AppConfig.CustomKVMImages = append(AppConfig.CustomKVMImages, image)
	if err := SaveConfig(); err != nil {
		AppConfig.CustomKVMImages = AppConfig.CustomKVMImages[:len(AppConfig.CustomKVMImages)-1]
		return err
	}
	return nil
}

func RemoveCustomKVMImage(id string) (bool, error) {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	filtered := make([]CustomKVMImage, 0, len(AppConfig.CustomKVMImages))
	found := false
	for _, image := range AppConfig.CustomKVMImages {
		if image.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, image)
	}
	if !found {
		return false, nil
	}
	previous := AppConfig.CustomKVMImages
	AppConfig.CustomKVMImages = filtered
	if err := SaveConfig(); err != nil {
		AppConfig.CustomKVMImages = previous
		return false, err
	}
	return true, nil
}

func ListCustomLXCImages() []CustomLXCImage {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	return append([]CustomLXCImage(nil), AppConfig.CustomLXCImages...)
}

// FindCustomLXCImage returns a copy of a custom LXC image by ID, or nil.
func FindCustomLXCImage(id string) *CustomLXCImage {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	for i := range AppConfig.CustomLXCImages {
		if AppConfig.CustomLXCImages[i].ID == id {
			img := AppConfig.CustomLXCImages[i]
			return &img
		}
	}
	return nil
}

func AddCustomLXCImage(image CustomLXCImage) error {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	for _, existing := range AppConfig.CustomLXCImages {
		if existing.ID == image.ID {
			return fmt.Errorf("custom LXC image %q already exists", image.ID)
		}
	}
	AppConfig.CustomLXCImages = append(AppConfig.CustomLXCImages, image)
	if err := SaveConfig(); err != nil {
		AppConfig.CustomLXCImages = AppConfig.CustomLXCImages[:len(AppConfig.CustomLXCImages)-1]
		return err
	}
	return nil
}

func RemoveCustomLXCImage(id string) (bool, error) {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	filtered := make([]CustomLXCImage, 0, len(AppConfig.CustomLXCImages))
	found := false
	for _, image := range AppConfig.CustomLXCImages {
		if image.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, image)
	}
	if !found {
		return false, nil
	}
	previous := AppConfig.CustomLXCImages
	AppConfig.CustomLXCImages = filtered
	if err := SaveConfig(); err != nil {
		AppConfig.CustomLXCImages = previous
		return false, err
	}
	return true, nil
}

// AddContainer adds a container to the config
func AddContainer(c Container) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	if c.UUID == "" {
		c.UUID = newContainerUUIDUnlocked()
	}
	c.Virtualization = NormalizeVirtualization(c.Virtualization)
	NormalizeContainerResourceAliases(&c)
	AppConfig.Containers = append(AppConfig.Containers, c)
	SaveConfigToDBLogged()
}

// MutateGlobal applies fn to the live configuration under the write lock and
// persists the result. It is the safe way for handlers to change top-level
// config fields (admin credentials, language, concurrency, ...) concurrently
// with the background scanners and metric samplers.
func MutateGlobal(fn func(*EyvescloudConfig)) error {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	fn(AppConfig)
	return saveConfigToDB()
}

// UpdateContainer 整体替换容器配置（按 ID 匹配），写回 DB。
// 用于 HVM 设置等元数据持久化。
func UpdateContainer(c *Container) error {
	if c == nil {
		return fmt.Errorf("container is nil")
	}
	return MutateGlobal(func(cfg *EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].ID == c.ID {
				cfg.Containers[i] = *c
				return
			}
		}
	})
}

// ListScheduledActions 列出某容器的定时任务。
func ListScheduledActions(containerID int) []ScheduledAction {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	out := make([]ScheduledAction, 0)
	for _, a := range AppConfig.ScheduledActions {
		if a.ContainerID == containerID {
			out = append(out, a)
		}
	}
	return out
}

// ListAllScheduledActions 列出全部定时任务（不区分容器），用于后台调度器扫描。
func ListAllScheduledActions() []ScheduledAction {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	out := make([]ScheduledAction, len(AppConfig.ScheduledActions))
	copy(out, AppConfig.ScheduledActions)
	return out
}

// SaveScheduledAction 保存（upsert）定时任务。
func SaveScheduledAction(a ScheduledAction) error {
	return MutateGlobal(func(cfg *EyvescloudConfig) {
		found := false
		for i := range cfg.ScheduledActions {
			if cfg.ScheduledActions[i].ID == a.ID {
				cfg.ScheduledActions[i] = a
				found = true
				break
			}
		}
		if !found {
			cfg.ScheduledActions = append(cfg.ScheduledActions, a)
		}
	})
}

// DeleteScheduledAction 删除某容器的某条定时任务。
func DeleteScheduledAction(containerID int, actionID string) error {
	return MutateGlobal(func(cfg *EyvescloudConfig) {
		out := cfg.ScheduledActions[:0]
		for _, a := range cfg.ScheduledActions {
			if a.ContainerID == containerID && a.ID == actionID {
				continue
			}
			out = append(out, a)
		}
		cfg.ScheduledActions = out
	})
}

// BackupDirectory returns the fixed, safe backup directory under the data dir.
// The backup directory is intentionally not user-configurable: allowing an
// arbitrary path here would let the backup download/restore handlers read or
// delete files anywhere on the host.
//
// 每次访问都会确保目录存在且 mode 0700，防止 umask / 历史遗留文件导致泄漏。
func BackupDirectory() string {
	var dir string
	if AppConfig != nil && AppConfig.DataDir != "" {
		dir = filepath.Join(AppConfig.DataDir, "backups")
	} else {
		dir = filepath.Join(getDataDir(), "backups")
	}
	if err := os.MkdirAll(dir, 0700); err == nil {
		_ = os.Chmod(dir, 0700)
	}
	return dir
}

// GetContainers returns a snapshot (deep copy) of the active container list.
// Background goroutines (expiry scanner, usage monitor, policy engine, metric
// sampler, snapshot scheduler, ...) MUST call this instead of slicing
// AppConfig.Containers directly, so reads never race with concurrent writers.
func GetContainers() []Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return append([]Container(nil), AppConfig.Containers...)
}

// SubUserUsernameByID 返回 subuser ID → username 的只读映射（持锁快照），
// 供列表接口派生 owner_username 展示字段。
func SubUserUsernameByID() map[string]string {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return nil
	}
	m := make(map[string]string, len(AppConfig.SubUsers))
	for i := range AppConfig.SubUsers {
		m[AppConfig.SubUsers[i].ID] = AppConfig.SubUsers[i].Username
	}
	return m
}

// ---------------------------------------------------------------------------
// 测试辅助：持锁访问全局配置
//
// 直接写 `config.AppConfig = ...` 是无锁写，与后台协程（任务队列 dispatcher、
// 到期扫描器等，均由包级 init 启动）的持锁读构成数据竞态——`go test -race`
// 实测会连带把产品代码路径标红，掩盖真正的缺陷。测试一律走下面三个入口。
// ---------------------------------------------------------------------------

// GetSecurityGroupEnforced 持锁读取安全组强制执行开关。
func GetSecurityGroupEnforced() bool {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig != nil && AppConfig.SecurityGroupEnforced
}

// SetSecurityGroupEnforced 持锁写入安全组强制执行开关并落库。
func SetSecurityGroupEnforced(enabled bool) error {
	AppConfigMu.Lock()
	if AppConfig == nil {
		AppConfigMu.Unlock()
		return fmt.Errorf("config is not initialized")
	}
	AppConfig.SecurityGroupEnforced = enabled
	AppConfigMu.Unlock()
	return SaveConfig()
}

// SetTestConfig 持锁替换全局配置（测试专用）。
func SetTestConfig(cfg *EyvescloudConfig) {
	AppConfigMu.Lock()
	AppConfig = cfg
	AppConfigMu.Unlock()
}

// GetTestConfig 持锁读取当前全局配置指针（测试专用）。
func GetTestConfig() *EyvescloudConfig {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig
}

// RestoreTestConfig 持锁恢复先前保存的全局配置指针（测试专用）。
func RestoreTestConfig(previous *EyvescloudConfig) {
	AppConfigMu.Lock()
	AppConfig = previous
	AppConfigMu.Unlock()
}

// GetAPIRateLimit returns a snapshot of the versioned-API rate-limit config.
func GetAPIRateLimit() APIRateLimitConfig {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig.APIRateLimit
}

// GetJWTSecret returns a snapshot of the JWT signing secret.
func GetJWTSecret() string {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig.JWTSecret
}

// GetMetricRetentionDays returns the configured metric rollup retention in days.
func GetMetricRetentionDays() int {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil || AppConfig.MetricRetentionDays <= 0 {
		return MetricRetentionDefault
	}
	return AppConfig.MetricRetentionDays
}

// GetMemoryOvercommit returns whether memory oversubscription is enabled and the
// configured ratio (physical memory × ratio = allocatable memory ceiling).
func GetMemoryOvercommit() (enabled bool, ratio float64) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return false, 1.0
	}
	if AppConfig.MemoryOvercommitRatio <= 0 {
		return AppConfig.MemoryOvercommitEnabled, 1.0
	}
	return AppConfig.MemoryOvercommitEnabled, AppConfig.MemoryOvercommitRatio
}

// GetNATSubnetOversubscription 返回是否允许 NAT 子网地址超售（容器数量超过 DHCP 池容量）。
func GetNATSubnetOversubscription() bool {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig != nil && AppConfig.NATSubnetOversubscription
}

// GetDiskOvercommitRatio 返回磁盘超售比（>=1，1 表示不超售）。
func GetDiskOvercommitRatio() float64 {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil || AppConfig.DiskOvercommitRatio < 1.0 {
		return 1.0
	}
	return AppConfig.DiskOvercommitRatio
}

// GetKSMTuning returns a snapshot of the KSM tuning configuration.
func GetKSMTuning() KSMTuningConfig {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return KSMTuningConfig{PagesToScan: 100, SleepMillisecs: 20, UseTuneKSM: true}
	}
	return AppConfig.KSMTuning
}

// GetBackupSettings returns a snapshot of the automatic-backup settings.
func GetBackupSettings() BackupSettings {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return AppConfig.BackupSettings
}

// GetInstanceBackupSettings returns a snapshot of the automatic instance-backup settings.
func GetInstanceBackupSettings() InstanceBackupSettings {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return InstanceBackupSettings{}
	}
	return AppConfig.InstanceBackupSettings
}

// UpdateInstanceBackupSettings replaces the automatic instance-backup settings.
func UpdateInstanceBackupSettings(settings InstanceBackupSettings) {
	MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.InstanceBackupSettings = settings
	})
}

// UpdateInstanceBackupLastRun records the last scheduled instance-backup run time.
func UpdateInstanceBackupLastRun(at string) {
	MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.InstanceBackupSettings.LastRunAt = at
	})
}

// GetRemoteBackupSettings returns a snapshot of the off-site backup target settings.
func GetRemoteBackupSettings() RemoteBackupSettings {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return RemoteBackupSettings{}
	}
	return AppConfig.RemoteBackupSettings
}

// UpdateRemoteBackupSettings replaces the off-site backup target settings.
func UpdateRemoteBackupSettings(settings RemoteBackupSettings) {
	MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.RemoteBackupSettings = settings
	})
}

// RecordRemoteBackupResult records the outcome of the last off-site sync attempt.
func RecordRemoteBackupResult(ok bool, errMsg string) {
	MutateGlobal(func(cfg *EyvescloudConfig) {
		if ok {
			cfg.RemoteBackupSettings.LastResult = "success"
			cfg.RemoteBackupSettings.LastError = ""
		} else {
			cfg.RemoteBackupSettings.LastResult = "failed"
			cfg.RemoteBackupSettings.LastError = errMsg
		}
		cfg.RemoteBackupSettings.LastRunAt = time.Now().Format("2006-01-02 15:04:05")
	})
}

// SetInstanceBackupRemoteStatus updates the off-site upload status of one backup record.
func SetInstanceBackupRemoteStatus(id string, uploaded bool, errMsg string) {
	MutateGlobal(func(cfg *EyvescloudConfig) {
		for i := range cfg.InstanceBackups {
			if cfg.InstanceBackups[i].ID == id {
				cfg.InstanceBackups[i].RemoteUploaded = uploaded
				cfg.InstanceBackups[i].RemoteError = errMsg
				return
			}
		}
	})
}

// GetAuditLogCount returns the current number of retained audit logs.
func GetAuditLogCount() int {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return len(AppConfig.AuditLogs)
}

// GetBackupCount returns the current number of registered config backups.
func GetBackupCount() int {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return len(AppConfig.Backups)
}

// IsBackupFileKnown reports whether filename is a currently registered backup
// snapshot. Used to restrict download/restore to real backups, preventing
// arbitrary file reads even if the base directory were ever misconfigured.
func IsBackupFileKnown(filename string) bool {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, b := range AppConfig.Backups {
		if b.Filename == filename {
			return true
		}
	}
	return false
}

// ReconcileConfig re-applies default normalization and migrations to the live
// config (used after restoring a snapshot from an older version) and persists.
func ReconcileConfig() error {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	normalizeConfigDefaults(AppConfig.DataDir)
	migrateLoadedConfig()
	return saveConfigToDB()
}

// FindNode returns a snapshot copy of a managed node by ID.
func FindNode(id string) (Node, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, n := range AppConfig.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// FindNodeByInstallKey returns a snapshot copy of a node by its install key.
func FindNodeByInstallKey(key string) (Node, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, n := range AppConfig.Nodes {
		if n.InstallKey != "" && n.InstallKey == key {
			return n, true
		}
	}
	return Node{}, false
}

// AddNode persists a new managed node. Node IDs must be unique.
func AddNode(n Node) error {
	return MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.Nodes = append(cfg.Nodes, n)
	})
}

// UpdateNode applies fn to a node under the write lock and returns the updated
// snapshot. It reports whether the node existed.
func UpdateNode(id string, fn func(*Node)) (Node, bool) {
	var updated Node
	ok := false
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		for i := range cfg.Nodes {
			if cfg.Nodes[i].ID != id {
				continue
			}
			fn(&cfg.Nodes[i])
			updated = cfg.Nodes[i]
			ok = true
			return
		}
	})
	return updated, ok
}

// RemoveNode removes a managed node by ID.
func RemoveNode(id string) bool {
	removed := false
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		filtered := make([]Node, 0, len(cfg.Nodes))
		for _, n := range cfg.Nodes {
			if n.ID == id {
				removed = true
				continue
			}
			filtered = append(filtered, n)
		}
		cfg.Nodes = filtered
	})
	return removed
}

// -------- NodeGroup helpers --------

func FindNodeGroup(id string) (NodeGroup, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, ng := range AppConfig.NodeGroups {
		if ng.ID == id {
			return ng, true
		}
	}
	return NodeGroup{}, false
}

func AddNodeGroup(ng NodeGroup) (NodeGroup, error) {
	if ng.ID == "" {
		ng.ID = "ng-" + randomShortID()
	}
	if ng.CreatedAt == "" {
		ng.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	err := MutateGlobal(func(cfg *EyvescloudConfig) {
		if cfg.NodeGroups == nil {
			cfg.NodeGroups = make([]NodeGroup, 0)
		}
		cfg.NodeGroups = append(cfg.NodeGroups, ng)
	})
	return ng, err
}

func UpdateNodeGroup(id string, fn func(*NodeGroup)) (NodeGroup, bool) {
	var found bool
	var result NodeGroup
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		for i := range cfg.NodeGroups {
			if cfg.NodeGroups[i].ID == id {
				fn(&cfg.NodeGroups[i])
				found = true
				result = cfg.NodeGroups[i]
				return
			}
		}
	})
	return result, found
}

func RemoveNodeGroup(id string) bool {
	removed := false
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		filtered := make([]NodeGroup, 0, len(cfg.NodeGroups))
		for _, ng := range cfg.NodeGroups {
			if ng.ID == id {
				removed = true
				continue
			}
			filtered = append(filtered, ng)
		}
		cfg.NodeGroups = filtered
		// 同步清空 Node 上的 NodeGroupID 引用
		for i := range cfg.Nodes {
			if cfg.Nodes[i].NodeGroupID == id {
				cfg.Nodes[i].NodeGroupID = ""
			}
		}
	})
	return removed
}

// -------- Cluster helpers --------

func FindCluster(id string) (Cluster, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, cl := range AppConfig.Clusters {
		if cl.ID == id {
			return cl, true
		}
	}
	return Cluster{}, false
}

func AddCluster(cl Cluster) (Cluster, error) {
	if cl.ID == "" {
		cl.ID = "cl-" + randomShortID()
	}
	if cl.CreatedAt == "" {
		cl.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	err := MutateGlobal(func(cfg *EyvescloudConfig) {
		if cfg.Clusters == nil {
			cfg.Clusters = make([]Cluster, 0)
		}
		cfg.Clusters = append(cfg.Clusters, cl)
	})
	return cl, err
}

func UpdateCluster(id string, fn func(*Cluster)) (Cluster, bool) {
	var found bool
	var result Cluster
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		for i := range cfg.Clusters {
			if cfg.Clusters[i].ID == id {
				fn(&cfg.Clusters[i])
				found = true
				result = cfg.Clusters[i]
				return
			}
		}
	})
	return result, found
}

func RemoveCluster(id string) bool {
	removed := false
	_ = MutateGlobal(func(cfg *EyvescloudConfig) {
		filtered := make([]Cluster, 0, len(cfg.Clusters))
		for _, cl := range cfg.Clusters {
			if cl.ID == id {
				removed = true
				continue
			}
			filtered = append(filtered, cl)
		}
		cfg.Clusters = filtered
		for i := range cfg.Nodes {
			if cfg.Nodes[i].ClusterID == id {
				cfg.Nodes[i].ClusterID = ""
			}
		}
	})
	return removed
}

// ListNodeGroupNodes 返回属于指定 NodeGroup 的节点 ID 列表。
func ListNodeGroupNodes(groupID string) []string {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	var ids []string
	for _, n := range AppConfig.Nodes {
		if n.NodeGroupID == groupID {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

// ListClusterNodes 返回属于指定 Cluster 的节点 ID 列表（通过 Cluster.RegionIDs + Node.ClusterID 双路径）。
func ListClusterNodes(clusterID string) []string {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	var ids []string
	cluster, ok := FindCluster(clusterID)
	if !ok {
		return ids
	}
	regionSet := map[string]bool{}
	for _, r := range cluster.RegionIDs {
		regionSet[r] = true
	}
	for _, n := range AppConfig.Nodes {
		if n.ClusterID == clusterID || (regionSet[n.RegionID] && len(cluster.RegionIDs) > 0) {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

// NodeSupportsVirt 报告节点是否支持指定虚拟化类型（lxc / kvm）。
func NodeSupportsVirt(n Node, virtType string) bool {
	v := strings.ToLower(strings.TrimSpace(virtType))
	if v == "" {
		return true
	}
	for _, t := range n.VirtTypes {
		if strings.ToLower(t) == v {
			return true
		}
	}
	return false
}

// AllocateContainerID allocates a new container ID
func AllocateContainerID() int {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	id := AppConfig.NextContainerID
	AppConfig.NextContainerID++
	SaveConfig()
	return id
}

// RemoveContainer removes a container from config by ID
func RemoveContainer(id int) bool {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	for i, c := range AppConfig.Containers {
		if c.ID == id {
			removeSubUserContainerAccess(c.Name, c.UUID)
			removeContainerSnapshotMetadata(id)
			// Clear snapshot schedule for this container
			clearContainerSnapshotSchedule(&AppConfig.Containers[i])
			AppConfig.Containers = append(AppConfig.Containers[:i], AppConfig.Containers[i+1:]...)
			SaveConfigToDBLogged()
			return true
		}
	}
	return false
}

func clearContainerSnapshotSchedule(c *Container) {
	c.SnapshotScheduleEnabled = false
	c.SnapshotScheduleIntervalHours = 0
	c.SnapshotScheduleTime = ""
	c.SnapshotScheduleLastRun = ""
	c.SnapshotScheduleNextRun = ""
	c.SnapshotScheduleCreatedBy = ""
}

func AddSnapshot(snapshot Snapshot) {
	AppConfigMu.Lock()
	AppConfig.Snapshots = append(AppConfig.Snapshots, snapshot)
	AppConfigMu.Unlock()
	SaveConfig()
}

func FindSnapshot(id string) *Snapshot {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for i := range AppConfig.Snapshots {
		if AppConfig.Snapshots[i].ID == id {
			return &AppConfig.Snapshots[i]
		}
	}
	return nil
}

func RemoveSnapshot(id string) bool {
	AppConfigMu.Lock()
	found := false
	for i := range AppConfig.Snapshots {
		if AppConfig.Snapshots[i].ID == id {
			AppConfig.Snapshots = append(AppConfig.Snapshots[:i], AppConfig.Snapshots[i+1:]...)
			found = true
			break
		}
	}
	AppConfigMu.Unlock()
	if found {
		SaveConfig()
	}
	return found
}

func ContainerSnapshots(containerID int) []Snapshot {
	AppConfigMu.RLock()
	result := make([]Snapshot, 0)
	for _, snapshot := range AppConfig.Snapshots {
		if snapshot.ContainerID == containerID {
			result = append(result, snapshot)
		}
	}
	AppConfigMu.RUnlock()
	return result
}

// ListInstanceBackups returns a shallow copy of all instance backups.
func ListInstanceBackups() []InstanceBackup {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return nil
	}
	return append([]InstanceBackup(nil), AppConfig.InstanceBackups...)
}

// ContainerInstanceBackups returns the backups belonging to a container, newest first.
func ContainerInstanceBackups(containerID int) []InstanceBackup {
	result := make([]InstanceBackup, 0)
	for _, b := range ListInstanceBackups() {
		if b.ContainerID == containerID {
			result = append(result, b)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		ti, _ := time.ParseInLocation("2006-01-02 15:04:05", result[i].CreatedAt, time.Local)
		tj, _ := time.ParseInLocation("2006-01-02 15:04:05", result[j].CreatedAt, time.Local)
		return ti.After(tj)
	})
	return result
}

// FindInstanceBackup returns a copy of an instance backup by ID, or nil.
func FindInstanceBackup(id string) *InstanceBackup {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return nil
	}
	for i := range AppConfig.InstanceBackups {
		if AppConfig.InstanceBackups[i].ID == id {
			b := AppConfig.InstanceBackups[i]
			return &b
		}
	}
	return nil
}

// AddInstanceBackup appends a backup record and persists it.
func AddInstanceBackup(b InstanceBackup) error {
	AppConfigMu.Lock()
	AppConfig.InstanceBackups = append(AppConfig.InstanceBackups, b)
	AppConfigMu.Unlock()
	return SaveConfig()
}

// RemoveInstanceBackup removes a backup record by ID and persists. Returns whether found.
func RemoveInstanceBackup(id string) bool {
	AppConfigMu.Lock()
	found := false
	filtered := make([]InstanceBackup, 0, len(AppConfig.InstanceBackups))
	for _, b := range AppConfig.InstanceBackups {
		if b.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, b)
	}
	AppConfig.InstanceBackups = filtered
	AppConfigMu.Unlock()
	if found {
		SaveConfig()
	}
	return found
}

func removeContainerSnapshotMetadata(containerID int) {
	filtered := make([]Snapshot, 0, len(AppConfig.Snapshots))
	for _, snapshot := range AppConfig.Snapshots {
		if snapshot.ContainerID != containerID {
			filtered = append(filtered, snapshot)
		}
	}
	AppConfig.Snapshots = filtered
}

func RemoveSubUserContainerAccess(containerName string, containerUUID string) {
	removeSubUserContainerAccess(containerName, containerUUID)
	SaveConfig()
}

func removeSubUserContainerAccess(containerName string, containerUUID string) {
	if containerName == "" && containerUUID == "" || len(AppConfig.SubUsers) == 0 {
		return
	}
	filteredUsers := make([]SubUser, 0, len(AppConfig.SubUsers))
	for _, su := range AppConfig.SubUsers {
		filteredNames := make([]string, 0, len(su.ContainerNames))
		for _, name := range su.ContainerNames {
			if name != containerName {
				filteredNames = append(filteredNames, name)
			}
		}
		filteredUUIDs := make([]string, 0, len(su.ContainerUUIDs))
		for _, uuid := range su.ContainerUUIDs {
			if uuid != containerUUID {
				filteredUUIDs = append(filteredUUIDs, uuid)
			}
		}
		if len(filteredNames) == 0 && len(filteredUUIDs) == 0 {
			continue
		}
		su.ContainerNames = filteredNames
		su.ContainerUUIDs = filteredUUIDs
		filteredUsers = append(filteredUsers, su)
	}
	AppConfig.SubUsers = filteredUsers
}

// findContainerUnlocked finds a container by ID. Caller must hold AppConfigMu
// (read or write) when calling this from a locked context.
func findContainerUnlocked(id int) *Container {
	// AppConfig 可能在测试 teardown 中被还原为 nil；后台任务队列 goroutine
	// 此刻仍可能调用 FindContainer，nil 防护让它返回 nil 而不是 panic。
	if AppConfig == nil {
		return nil
	}
	for i, c := range AppConfig.Containers {
		if c.ID == id {
			return &AppConfig.Containers[i]
		}
	}
	return nil
}

// findContainerByNameUnlocked looks up a container by name without taking the
// config lock. Callers MUST already hold AppConfigMu (read or write). It exists
// so migration helpers that run under the write lock (ReconcileConfig) do not
// re-acquire AppConfigMu.RLock, which would deadlock the RWMutex.
func findContainerByNameUnlocked(name string) *Container {
	if AppConfig == nil {
		return nil
	}
	for i, c := range AppConfig.Containers {
		if c.Name == name {
			return &AppConfig.Containers[i]
		}
	}
	return nil
}

// FindContainerInConfigUnlocked 在给定 cfg 副本里按 UUID 查找容器（不加锁，供 MutateGlobal 内闭包使用）。
func FindContainerInConfigUnlocked(cfg *EyvescloudConfig, uuid string) *Container {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Containers {
		if cfg.Containers[i].UUID == uuid {
			return &cfg.Containers[i]
		}
	}
	return nil
}

// FindContainer finds a container by ID
func FindContainer(id int) *Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return findContainerUnlocked(id)
}

// GetContainerSnapshot 返回容器的**值拷贝**（含易被就地修改的切片字段的深拷贝）。
//
// 使用场景：调用方需要"持锁读一次、之后在锁外长期使用"时，必须用本函数。
// FindContainer 返回的是全局切片元素的内部指针，锁一释放就不再受保护，
// 锁外再解引用即与并发写构成数据竞态（`go test -race` 实测：DestroyContainer
// 读 c.LxcName()/c.IPv6Addresses 与任务队列写状态相撞）。
func GetContainerSnapshot(id int) (Container, bool) {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	c := findContainerUnlocked(id)
	if c == nil {
		return Container{}, false
	}
	snap := *c
	// 深拷贝会被就地修改的切片，避免快照上的写入污染全局（也避免反向污染）。
	if c.IPv6Addresses != nil {
		snap.IPv6Addresses = append([]IPv6Assignment(nil), c.IPv6Addresses...)
	}
	if c.PublicIPv4s != nil {
		snap.PublicIPv4s = append([]PublicIPv4Assignment(nil), c.PublicIPv4s...)
	}
	if c.PortMappings != nil {
		snap.PortMappings = append([]PortMapping(nil), c.PortMappings...)
	}
	return snap, true
}

// FindContainerByUUID finds a container by UUID.
func FindContainerByUUID(uuid string) *Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for i, c := range AppConfig.Containers {
		if c.UUID == uuid {
			return &AppConfig.Containers[i]
		}
	}
	return nil
}

// FindContainerByName finds a container by name
func FindContainerByName(name string) *Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	return findContainerByNameUnlocked(name)
}

// FindContainerByIdentifier finds a container by ID, UUID, or name.
func FindContainerByIdentifier(identifier string) *Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if id, err := strconv.Atoi(identifier); err == nil {
		if c := findContainerUnlocked(id); c != nil {
			return c
		}
	}
	for i, c := range AppConfig.Containers {
		if c.UUID == identifier {
			return &AppConfig.Containers[i]
		}
	}
	for i, c := range AppConfig.Containers {
		if c.Name == identifier {
			return &AppConfig.Containers[i]
		}
	}
	return nil
}

// RecycleRetentionDays 默认回收站保留天数（超过后自动彻底删除）。
// 可用 app_meta 键 recycle_retention_days 覆盖；0/负值按默认处理。
const RecycleRetentionDays = 7

// RecycleContainer 把实例移入回收站（软删除）：打标记并停机（运行中时）。
// 返回 (实例名, 是否在运行)。数据面不动，恢复=清标记。
func RecycleContainer(id int, reason string) (string, bool, error) {
	c := FindContainer(id)
	if c == nil {
		return "", false, fmt.Errorf("container not found: %d", id)
	}
	if c.RecycledAt != "" {
		return c.Name, false, nil // 已在回收站，幂等
	}
	wasRunning := c.Status == "running"
	ok, _ := MutateContainerByID(id, func(target *Container) {
		target.RecycledAt = time.Now().Format("2006-01-02 15:04:05")
	})
	if !ok {
		return "", false, fmt.Errorf("recycle container %d failed", id)
	}
	return c.Name, wasRunning, nil
}

// RestoreContainer 从回收站恢复实例（清标记）。
// 同名活跃实例已存在时拒绝（避免命名冲突），要求先处理冲突。
func RestoreContainer(id int) error {
	c := FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found: %d", id)
	}
	if c.RecycledAt == "" {
		return nil // 不在回收站，幂等
	}
	if dup := findContainerByNameUnlocked(c.Name); dup != nil && dup.ID != c.ID && dup.RecycledAt == "" {
		return fmt.Errorf("存在同名活跃实例 %s（ID %d），无法恢复；请先重命名其中之一", c.Name, dup.ID)
	}
	ok, _ := MutateContainerByID(id, func(target *Container) {
		target.RecycledAt = ""
	})
	if !ok {
		return fmt.Errorf("restore container %d failed", id)
	}
	return nil
}

// RecycledContainers 返回回收站中的实例快照。
func RecycledContainers() []Container {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	out := []Container{}
	for _, c := range AppConfig.Containers {
		if c.RecycledAt != "" {
			out = append(out, c)
		}
	}
	return out
}

// RecyclePurgeDue 返回超过保留期、应被彻底删除的回收站实例 ID 列表。
func RecyclePurgeDue(retentionDays int) []int {
	if retentionDays <= 0 {
		retentionDays = RecycleRetentionDays
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	out := []int{}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, c := range AppConfig.Containers {
		if c.RecycledAt == "" {
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", c.RecycledAt, time.Local); err == nil && t.Before(cutoff) {
			out = append(out, c.ID)
		}
	}
	return out
}

// ContainerStatusHook 是容器状态变更回调（Webhook 事件订阅用）。
// 由 api 包在启动时注入，避免 config → api 的反向 import。
// 回调在锁外异步触发语义：实现方不得假定同步完成，也不得长时间阻塞。
var ContainerStatusHook func(containerID int, name string, oldStatus, newStatus string)

// UpdateContainerStatus updates container status by ID
func UpdateContainerStatus(id int, status string) {
	var name, oldStatus string
	func() {
		// 闭包 + defer：panic 时仍能安全释放锁（HTTP 层有 recover 中间件兜底）。
		AppConfigMu.Lock()
		defer AppConfigMu.Unlock()
		if c := findContainerUnlocked(id); c != nil {
			name, oldStatus = c.Name, c.Status
			c.Status = status
			SaveConfigToDBLogged()
		}
	}()
	// 锁外触发钩子（Webhook 投递可能耗时，不能占住全局配置锁）。
	if ContainerStatusHook != nil && name != "" && oldStatus != status {
		ContainerStatusHook(id, name, oldStatus, status)
	}
}

// FireContainerStatusHook 在锁外显式触发容器状态变更钩子。
// 供 api 层在锁内完成状态变更后、锁外补发事件使用——webhook 投递会
// 重新获取配置读锁，绝不能在持有 AppConfigMu 写锁时调用，否则死锁。
func FireContainerStatusHook(id int, name, oldStatus, newStatus string) {
	if ContainerStatusHook != nil && name != "" && oldStatus != newStatus {
		ContainerStatusHook(id, name, oldStatus, newStatus)
	}
}

// SetContainerStatusAndNotify 供运行时层（lxc/kvm）在已持有容器活指针时
// 更新状态并触发钩子。不落盘——调用方按原有节奏决定 SaveConfig 时机。
// 状态未变化时为 no-op（不触发钩子，避免心跳抖动产生事件噪音）。
func SetContainerStatusAndNotify(c *Container, newStatus string) {
	if c == nil || c.Status == newStatus {
		return
	}
	old := c.Status
	c.Status = newStatus
	FireContainerStatusHook(c.ID, c.Name, old, newStatus)
}

// UpdateContainerStatusNotify 持锁按 ID 更新状态，并在状态确有变化时于锁外触发
// 状态钩子（与 UpdateContainerStatus 同款语义，但不落库——状态由其它路径持久化）。
//
// 存在的理由：SetContainerStatusAndNotify 直接改调用方传入的指针，要求该指针指向
// 全局切片元素且调用方自己持锁；运行时探测路径拿到的是「快照副本」，不能再用它
// 写回内存态，否则要么改不到全局、要么构成数据竞态（go test -race 实测）。
func UpdateContainerStatusNotify(id int, newStatus string) {
	var name, oldStatus string
	func() {
		AppConfigMu.Lock()
		defer AppConfigMu.Unlock()
		c := findContainerUnlocked(id)
		if c == nil || c.Status == newStatus {
			return
		}
		name, oldStatus = c.Name, c.Status
		c.Status = newStatus
	}()
	if ContainerStatusHook != nil && name != "" && oldStatus != newStatus {
		ContainerStatusHook(id, name, oldStatus, newStatus)
	}
}

func UpdateContainerStatusAndRestore(id int, status string, restoreOnHostBoot bool) {
	var name, oldStatus string
	func() {
		AppConfigMu.Lock()
		defer AppConfigMu.Unlock()
		if c := findContainerUnlocked(id); c != nil {
			name, oldStatus = c.Name, c.Status
			c.Status = status
			c.RestoreOnHostBoot = restoreOnHostBoot
			SaveConfigToDBLogged()
		}
	}()
	if ContainerStatusHook != nil && name != "" && oldStatus != status {
		ContainerStatusHook(id, name, oldStatus, status)
	}
}

func SetContainerRestoreOnHostBoot(id int, restore bool) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	c := findContainerUnlocked(id)
	if c != nil {
		c.RestoreOnHostBoot = restore
		SaveConfigToDBLogged()
	}
}

func SetContainerPolicyBlock(id int, blocked bool, reason string) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	c := findContainerUnlocked(id)
	if c == nil {
		return
	}
	c.PolicyBlocked = blocked
	if blocked {
		c.PolicyBlockedReason = reason
		c.PolicyBlockedAt = time.Now().Format("2006-01-02 15:04:05")
	} else {
		c.PolicyBlockedReason = ""
		c.PolicyBlockedAt = ""
	}
	SaveConfigToDBLogged()
}

// SetContainerTenant assigns a container to a tenant group.
func SetContainerTenant(id int, tenant string) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	c := findContainerUnlocked(id)
	if c == nil {
		return
	}
	c.Tenant = strings.TrimSpace(tenant)
	SaveConfigToDBLogged()
}

// UpdateVNC refreshes all container statuses
func UpdateVNC(containers []Container) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	AppConfig.Containers = containers
	SaveConfigToDBLogged()
}

// MutateContainerByID applies fn to the live container under the write lock
// and persists the change. It reports whether the container existed and the
// mutation was saved, and returns the container so the caller can apply the
// change to the runtime afterwards.
func MutateContainerByID(id int, fn func(*Container)) (bool, *Container) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	c := findContainerUnlocked(id)
	if c == nil {
		return false, nil
	}
	fn(c)
	if err := saveConfigToDB(); err != nil {
		return false, c
	}
	return true, c
}

// MutateContainerNoSave applies fn to the live container under the write lock
// without persisting. Returns false if the container no longer exists. The
// caller is expected to persist with SaveConfig once after a batch of
// mutations. This keeps multi-field updates (e.g. traffic counters) atomic
// against readers and other writers.
func MutateContainerNoSave(id int, fn func(*Container)) bool {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	c := findContainerUnlocked(id)
	if c == nil {
		return false
	}
	fn(c)
	return true
}

// UpdatePolicyRuleMeta persists the trigger counters for a policy rule.
func UpdatePolicyRuleMeta(id string, triggeredCount int, lastTriggered string) {
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	for i := range AppConfig.PolicyRules {
		if AppConfig.PolicyRules[i].ID == id {
			AppConfig.PolicyRules[i].TriggeredCount = triggeredCount
			AppConfig.PolicyRules[i].LastTriggered = lastTriggered
			return
		}
	}
}

func NormalizeNATPortRange(start, end int) (int, int, error) {
	if start == 0 && end == 0 {
		return DefaultNATPortStart, DefaultNATPortEnd, nil
	}
	if start == 0 {
		start = DefaultNATPortStart
	}
	if end == 0 {
		end = DefaultNATPortEnd
	}
	if start < 1 || start > 65535 {
		return 0, 0, fmt.Errorf("NAT port start must be 1-65535")
	}
	if end < 1 || end > 65535 {
		return 0, 0, fmt.Errorf("NAT port end must be 1-65535")
	}
	if start > end {
		return 0, 0, fmt.Errorf("NAT port start cannot be greater than end")
	}
	return start, end, nil
}

func NATPortRange() (int, int) {
	if AppConfig == nil {
		return DefaultNATPortStart, DefaultNATPortEnd
	}
	start, end, err := NormalizeNATPortRange(AppConfig.NATPortStart, AppConfig.NATPortEnd)
	if err != nil {
		return DefaultNATPortStart, DefaultNATPortEnd
	}
	return start, end
}

func NATPortCapacity() int {
	start, end := NATPortRange()
	return end - start + 1
}

func NATPortInRange(port int) bool {
	start, end := NATPortRange()
	return port >= start && port <= end
}

func SetNATPortRange(start, end int) error {
	start, end, err := NormalizeNATPortRange(start, end)
	if err != nil {
		return err
	}
	AppConfig.NATPortStart = start
	AppConfig.NATPortEnd = end
	if AppConfig.NextSSHPort < start || AppConfig.NextSSHPort > end {
		AppConfig.NextSSHPort = start
	}
	return SaveConfig()
}

func normalizeNATPortRangeDefaults() bool {
	if AppConfig == nil {
		return false
	}
	start, end, err := NormalizeNATPortRange(AppConfig.NATPortStart, AppConfig.NATPortEnd)
	if err != nil {
		start, end = DefaultNATPortStart, DefaultNATPortEnd
	}
	changed := AppConfig.NATPortStart != start || AppConfig.NATPortEnd != end
	AppConfig.NATPortStart = start
	AppConfig.NATPortEnd = end
	if AppConfig.NextSSHPort < start || AppConfig.NextSSHPort > end {
		AppConfig.NextSSHPort = start
		changed = true
	}
	return changed
}

// AllocateSSHPort allocates a new SSH port, skipping ports already used by any container
func AllocateSSHPort() (int, error) {
	return AllocateSSHPortExcluding(nil)
}

// AllocateSSHPortExcluding allocates a management port while reserving
// user-requested NAT host ports for the container being created.
func AllocateSSHPortExcluding(excluded []int) (int, error) {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	candidate, err := previewSSHPortExcluding(excluded)
	if err != nil {
		return 0, err
	}
	start, end := NATPortRange()
	AppConfig.NextSSHPort = candidate + 1
	if AppConfig.NextSSHPort > end {
		AppConfig.NextSSHPort = start
	}
	SaveConfig()
	return candidate, nil
}

// PreviewSSHPortExcluding returns the management port that the allocator would
// choose without advancing or persisting the allocation cursor.
func PreviewSSHPortExcluding(excluded []int) (int, error) {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	return previewSSHPortExcluding(excluded)
}

func previewSSHPortExcluding(excluded []int) (int, error) {
	used := collectAllHostPorts()
	for _, port := range excluded {
		if port > 0 {
			used[port] = true
		}
	}
	start, end := NATPortRange()
	port := AppConfig.NextSSHPort
	if port < start || port > end {
		port = start
	}
	capacity := end - start + 1
	for i := 0; i < capacity; i++ {
		candidate := start + ((port - start + i) % capacity)
		if used[candidate] {
			continue
		}
		return candidate, nil
	}
	return 0, fmt.Errorf("no free NAT4 host port in configured range %d-%d", start, end)
}

// collectAllHostPorts collects all host ports used by any container (LXC + KVM)
func collectAllHostPorts() map[int]bool {
	used := map[int]bool{}
	for _, c := range AppConfig.Containers {
		for _, pm := range c.PortMappings {
			used[pm.HostPort] = true
		}
	}
	return used
}

// IsValidContainerName checks if container name is valid (no duplicate check needed, ID is primary key)
func IsValidContainerName(name string) bool {
	return IsValidContainerNameSyntax(name)
}

// IsValidContainerNameSyntax checks only the container name format.
func IsValidContainerNameSyntax(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	// Only allow alphanumeric, hyphens, underscores
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// AddAuditLog adds an audit log entry
func AddAuditLog(action, target, detail, user string) {
	if AppConfig == nil {
		return
	}
	log := AuditLog{
		Time:   time.Now().Format("2006-01-02 15:04:05"),
		Action: action,
		Target: target,
		Detail: detail,
		User:   user,
	}
	AppConfigMu.Lock()
	AppConfig.AuditLogs = append(AppConfig.AuditLogs, log)
	if len(AppConfig.AuditLogs) > 500 {
		AppConfig.AuditLogs = AppConfig.AuditLogs[len(AppConfig.AuditLogs)-500:]
	}
	AppConfigMu.Unlock()
	SaveConfig()
}

func AddAuditLogFull(action, target, detail, user, ip, userAgent string, success bool, errMsg string) {
	if AppConfig == nil {
		return
	}
	s := success
	log := AuditLog{
		Time:      time.Now().Format("2006-01-02 15:04:05"),
		Action:    action,
		Target:    target,
		Detail:    detail,
		User:      user,
		IP:        ip,
		UserAgent: userAgent,
		Success:   &s,
		Error:     errMsg,
	}
	AppConfigMu.Lock()
	if n := len(AppConfig.AuditLogs); n > 0 {
		log.PrevHash = AppConfig.AuditLogs[n-1].Hash
	}
	log.Hash = auditLogHash(log)
	AppConfig.AuditLogs = append(AppConfig.AuditLogs, log)
	if len(AppConfig.AuditLogs) > 500 {
		AppConfig.AuditLogs = AppConfig.AuditLogs[len(AppConfig.AuditLogs)-500:]
	}
	AppConfigMu.Unlock()
	SaveConfig()
}

// auditLogHash 用 P8-3 auditchain 包相同的 canonical 字段顺序计算 SHA-256。
// 在 config 包内嵌一份实现以避免循环依赖；与 auditchain.Entry.canonical 字段
// 顺序必须保持一致。
func auditLogHash(log AuditLog) string {
	success := "0"
	if log.Success != nil && *log.Success {
		success = "1"
	}
	canonical := strings.Join([]string{
		log.Time, log.Action, log.Target, log.Detail,
		log.User, log.IP, log.UserAgent, success, log.Error,
		log.PrevHash,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// SaveTasks persists the task queue to config
func SaveTasks(tasks []SavedTask) {
	// nil 检查必须在锁内：锁外读 AppConfig 与持锁替换（启动/测试夹具）
	// 构成数据竞态（go test -race 实测）。
	AppConfigMu.Lock()
	if AppConfig == nil {
		AppConfigMu.Unlock()
		return
	}
	AppConfig.Tasks = tasks
	AppConfigMu.Unlock()
	SaveConfig()
}

// AddLoginLog persists a login log entry
func AddLoginLog(username, ip, userAgent string, success bool) {
	log := SavedLoginLog{
		Time:      time.Now().Format("2006-01-02 15:04:05 MST"),
		Username:  username,
		IP:        ip,
		UserAgent: userAgent,
		Success:   success,
	}
	AppConfigMu.Lock()
	AppConfig.LoginLogs = append(AppConfig.LoginLogs, log)
	if len(AppConfig.LoginLogs) > 200 {
		AppConfig.LoginLogs = AppConfig.LoginLogs[len(AppConfig.LoginLogs)-200:]
	}
	AppConfigMu.Unlock()
	SaveConfig()
}

// ResetAdminPassword resets the admin password from CLI
func ResetAdminPassword(newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	AppConfig.AdminPassHash = string(hash)
	return SaveConfig()
}

// CleanStaleContainers removes containers from config if their LXC directory doesn't exist
func CleanStaleContainers() {
	valid := make([]Container, 0)
	changed := false
	for _, c := range AppConfig.Containers {
		// 节点容器：主控本地文件系统没有它的 LXC 目录/KVM 镜像，存在性由
		// 被控心跳维护（syncAgentContainers 的 orphan 机制）。此前未跳过：
		// 每次主控重启都会把节点容器当"过期记录"误删（生产实测，v2.2.36）。
		if c.NodeID != "" {
			valid = append(valid, c)
			continue
		}
		if c.IsKVM() {
			if c.DiskImage == "" {
				valid = append(valid, c)
				continue
			}
			if _, err := os.Stat(c.DiskImage); os.IsNotExist(err) {
				fmt.Printf("Cleaning stale KVM config: %s (disk image not found)\n", c.VirshName())
				changed = true
				continue
			}
			valid = append(valid, c)
			continue
		}
		lxcDir := "/var/lib/lxc/" + c.LxcName()
		if _, err := os.Stat(lxcDir); os.IsNotExist(err) {
			fmt.Printf("Cleaning stale container config: %s (LXC dir not found)\n", c.LxcName())
			changed = true
			continue
		}
		valid = append(valid, c)
	}
	if changed {
		AppConfig.Containers = valid
		SaveConfig()
	}
}

// DeleteFirstBootCredentialsIfExists 尝试删除 DataDir 下的首启凭据文件。
// 文件不存在或删除失败都静默忽略——API 层在主管理员首次登录成功后调用它，
// 即使删除失败也不应该阻断登录流程。
func DeleteFirstBootCredentialsIfExists() {
	if AppConfig == nil || strings.TrimSpace(AppConfig.DataDir) == "" {
		return
	}
	target := filepath.Join(AppConfig.DataDir, FirstBootCredsFile)
	_ = os.Remove(target)
}
