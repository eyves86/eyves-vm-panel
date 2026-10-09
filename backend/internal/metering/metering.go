// Package metering 提供 P7-3 计量计费核心原语：
//
//   - UsageRecord：按小时聚合的资源用量（cpu/mem/disk/traffic），租户/实例/账期三个维度；
//   - RateCard：套餐价目表，按资源维度配置单价；
//   - Invoice：账期账单 + 明细行；
//   - Reconciliation：账单与原始用量勾稽校验。
//
// 采集器（caller）按小时调用 Record() 写入用量；账期任务调用 Summarize() 生
// 成账单；勾稽任务调用 Reconcile() 校验一致性。所有金额以最小货币单位（分）
// 表示，避免浮点累计误差。
package metering

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Usage 一次用量记录的字段（单位见字段注释）。
type Usage struct {
	CPUHours   float64 `json:"cpu_hours"`   // vCPU 小时数
	RAMMBHour  int64   `json:"ram_mb_hour"` // MB 小时数
	DiskGBHour float64 `json:"disk_gb_hour"`
	TrafficGB  float64 `json:"traffic_gb"` // 出入总流量
}

// Add 累加另一条用量的分量（用于分区补采）。
func (u *Usage) Add(other Usage) {
	u.CPUHours += other.CPUHours
	u.RAMMBHour += other.RAMMBHour
	u.DiskGBHour += other.DiskGBHour
	u.TrafficGB += other.TrafficGB
}

// UsageRecord 一条按小时聚合的用量记录。
type UsageRecord struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	InstanceID string    `json:"instance_id,omitempty"`
	PeriodHour time.Time `json:"period_hour"` // UTC 整点（秒为 0）
	Usage      Usage     `json:"usage"`
	// Billable 当实例被暂停/关机时，CPU/RAM 不计费，但磁盘仍计费，
	// 由采集器写入时已按策略过滤过；这里保存的是"已计费"用量。
	Billable bool `json:"billable"`
}

// PeriodKey 返回记录的归一化小时键（truncate 到整点）。
func (r UsageRecord) PeriodKey() time.Time {
	return r.PeriodHour.UTC().Truncate(time.Hour)
}

// RateCard 套餐价目表。所有金额为最小货币单位（分）。
type RateCard struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Currency       string  `json:"currency"`
	CPUPerHourCent float64 `json:"cpu_per_hour_cent"`  // 每 vCPU·小时
	RAMPerHBCent   float64 `json:"ram_per_mb_hour_cent"`
	DiskPerGBHCent float64 `json:"disk_per_gb_hour_cent"`
	TrafficPerGBCent float64 `json:"traffic_per_gb_cent"`
}

// Validate 检查价目表字段合法（任意单价非负）。
func (rc RateCard) Validate() error {
	if rc.ID == "" {
		return errors.New("metering: rate card id is required")
	}
	if rc.CPUPerHourCent < 0 || rc.RAMPerHBCent < 0 || rc.DiskPerGBHCent < 0 || rc.TrafficPerGBCent < 0 {
		return errors.New("metering: rate card prices must be non-negative")
	}
	if rc.Currency == "" {
		return errors.New("metering: rate card currency is required")
	}
	return nil
}

// InvoiceLine 账单明细行。
type InvoiceLine struct {
	Label string  `json:"label"` // e.g. "vCPU hours"
	Qty   float64 `json:"qty"`
	Unit  string  `json:"unit"`
	Total int64   `json:"total_cent"`
}

// Invoice 账期账单。
type Invoice struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenant_id"`
	RateCardID  string        `json:"rate_card_id"`
	Currency    string        `json:"currency"`
	PeriodStart time.Time     `json:"period_start"`
	PeriodEnd   time.Time     `json:"period_end"`
	Lines       []InvoiceLine `json:"lines"`
	Total       int64         `json:"total_cent"`
	CreatedAt   time.Time     `json:"created_at"`
	// SourceHash 勾稽凭证：源用量记录之和的 SHA256 摘要。
	SourceHash string `json:"source_hash"`
}

// Store 用量/账单内存仓库（生产环境接配置库）。
//
// 单测使用 NewMemoryStore；外部包可实现接口接持久化层。
type Store interface {
	AppendUsage(UsageRecord) error
	ListUsage(tenantID string, since, until time.Time) ([]UsageRecord, error)
	UpsertRateCard(RateCard) error
	GetRateCard(id string) (RateCard, bool)
	AppendInvoice(Invoice) error
	ListInvoices(tenantID string) ([]Invoice, error)
}

// MemoryStore 线程安全的内存仓库实现。
type MemoryStore struct {
	mu         sync.Mutex
	usage      []UsageRecord
	rateCards  map[string]RateCard
	invoices   []Invoice
	invoiceIDs map[string]bool
}

// NewMemoryStore 构造空仓库。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rateCards:  map[string]RateCard{},
		invoiceIDs: map[string]bool{},
	}
}

// AppendUsage 追加用量记录；同 (tenant, instance, period) 重复则累加。
func (s *MemoryStore) AppendUsage(r UsageRecord) error {
	if r.TenantID == "" {
		return errors.New("metering: tenant_id required")
	}
	if r.PeriodHour.IsZero() {
		return errors.New("metering: period_hour required")
	}
	r.PeriodHour = r.PeriodHour.UTC().Truncate(time.Hour)
	if r.ID == "" {
		r.ID = fmt.Sprintf("u-%d", time.Now().UnixNano())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.usage {
		if s.usage[i].TenantID == r.TenantID &&
			s.usage[i].InstanceID == r.InstanceID &&
			s.usage[i].PeriodHour.Equal(r.PeriodHour) {
			s.usage[i].Usage.Add(r.Usage)
			if r.Billable {
				s.usage[i].Billable = true
			}
			return nil
		}
	}
	s.usage = append(s.usage, r)
	return nil
}

// ListUsage 列出某租户在 [since, until) 区间内的所有用量记录。
func (s *MemoryStore) ListUsage(tenantID string, since, until time.Time) ([]UsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UsageRecord, 0)
	for _, r := range s.usage {
		if r.TenantID != tenantID {
			continue
		}
		p := r.PeriodHour
		if !p.Before(since) && p.Before(until) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PeriodHour.Equal(out[j].PeriodHour) {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].PeriodHour.Before(out[j].PeriodHour)
	})
	return out, nil
}

// UpsertRateCard 写入或更新价目表。
func (s *MemoryStore) UpsertRateCard(rc RateCard) error {
	if err := rc.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateCards[rc.ID] = rc
	return nil
}

// GetRateCard 按 ID 取价目表。
func (s *MemoryStore) GetRateCard(id string) (RateCard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, ok := s.rateCards[id]
	return rc, ok
}

// AppendInvoice 写入账单（重复 ID 覆盖）。
func (s *MemoryStore) AppendInvoice(inv Invoice) error {
	if inv.ID == "" {
		return errors.New("metering: invoice id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invoiceIDs[inv.ID] {
		for i := range s.invoices {
			if s.invoices[i].ID == inv.ID {
				s.invoices[i] = inv
				return nil
			}
		}
	}
	s.invoices = append(s.invoices, inv)
	s.invoiceIDs[inv.ID] = true
	return nil
}

// ListInvoices 列出某租户的全部账单（按账期倒序）。
func (s *MemoryStore) ListInvoices(tenantID string) ([]Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Invoice, 0)
	for _, inv := range s.invoices {
		if inv.TenantID == tenantID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PeriodStart.After(out[j].PeriodStart)
	})
	return out, nil
}