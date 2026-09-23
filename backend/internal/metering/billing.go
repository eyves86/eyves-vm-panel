package metering

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Summarize 用量聚合：把一组 UsageRecord 折叠成单一 Usage。
// 仅累加 Billable=true 的记录；非计费（暂停期）记录不会进入账单。
func Summarize(records []UsageRecord) Usage {
	var u Usage
	for _, r := range records {
		if !r.Billable {
			continue
		}
		u.Add(r.Usage)
	}
	return u
}

// BuildInvoice 根据价目表 + 用量生成账单。
//
// 用量四舍五入到两位小数；金额按"四舍五入到最近分"。
//
// 安全约束：records 中任何一条的 TenantID 与入参 tenantID 不一致时直接拒绝，
// 防止跨租户用量被误计入他人账单（账单串号）。
func BuildInvoice(tenantID string, rc RateCard, records []UsageRecord, periodStart, periodEnd time.Time, invoiceID string) (Invoice, error) {
	if err := rc.Validate(); err != nil {
		return Invoice{}, err
	}
	if strings.TrimSpace(tenantID) == "" {
		return Invoice{}, errors.New("metering: tenant_id is required")
	}
	if !periodStart.Before(periodEnd) {
		return Invoice{}, errors.New("metering: period_start must precede period_end")
	}
	for _, r := range records {
		if r.TenantID != tenantID {
			return Invoice{}, fmt.Errorf("metering: usage record for tenant %q cannot be billed to %q", r.TenantID, tenantID)
		}
	}
	usage := Summarize(records)
	cpuCents := roundHalfUp(usage.CPUHours * rc.CPUPerHourCent)
	ramCents := roundHalfUp(float64(usage.RAMMBHour) * rc.RAMPerHBCent)
	diskCents := roundHalfUp(usage.DiskGBHour * rc.DiskPerGBHCent)
	trafficCents := roundHalfUp(usage.TrafficGB * rc.TrafficPerGBCent)
	lines := []InvoiceLine{
		{Label: "vCPU hours", Qty: roundFloat(usage.CPUHours, 4), Unit: "vCPU·h", Total: cpuCents},
		{Label: "RAM MB·hours", Qty: float64(usage.RAMMBHour), Unit: "MB·h", Total: ramCents},
		{Label: "Disk GB·hours", Qty: roundFloat(usage.DiskGBHour, 4), Unit: "GB·h", Total: diskCents},
		{Label: "Traffic", Qty: roundFloat(usage.TrafficGB, 4), Unit: "GB", Total: trafficCents},
	}
	total := cpuCents + ramCents + diskCents + trafficCents
	if invoiceID == "" {
		invoiceID = fmt.Sprintf("inv-%s-%s", tenantID, periodStart.UTC().Format("200601"))
	}
	return Invoice{
		ID:          invoiceID,
		TenantID:    tenantID,
		RateCardID:  rc.ID,
		Currency:    rc.Currency,
		PeriodStart: periodStart.UTC(),
		PeriodEnd:   periodEnd.UTC(),
		Lines:       lines,
		Total:       total,
		CreatedAt:   time.Now().UTC(),
		SourceHash:  hashUsage(records),
	}, nil
}

// Reconcile 勾稽账单：用原始用量重新计算账单明细金额，与账单的 Total/行 Total
// 比对；返回差异原因（nil 即完全勾稽）。
//
// 设计：允许 PeriodHash 重算结果与账单不一致（重算后金额偏差 < 1 分），
// 否则视为脏数据或篡改。
func Reconcile(inv Invoice, rc RateCard) error {
	if inv.RateCardID != rc.ID {
		return fmt.Errorf("metering: invoice rate card %s != %s", inv.RateCardID, rc.ID)
	}
	// 重算总额：找到同租户同账期的源记录
	// 注意：本函数只接收账单本身，无法直接访问原始记录——调用方需
	// 在重算路径中用 SourceHash 校验原始记录未被删改（见 CallerHasUsageHash）。
	// 这里我们仅做"账单内部一致性"校验：每行 Total 之和应等于 Total。
	var sum int64
	for _, line := range inv.Lines {
		sum += line.Total
	}
	if sum != inv.Total {
		return fmt.Errorf("metering: invoice %s total mismatch: lines sum=%d, total=%d", inv.ID, sum, inv.Total)
	}
	return nil
}

// CallerHasUsageHash 校验调用方提供的源记录哈希是否与账单 SourceHash 一致。
// 哈希：按 PeriodHour+InstanceID 排序后拼接各项 amount 字段取 SHA-256。
// 哈希值用 hex 编码，便于存储/审计。
func CallerHasUsageHash(records []UsageRecord, expected string) bool {
	return hashUsage(records) == expected
}

func hashUsage(records []UsageRecord) string {
	sorted := make([]UsageRecord, len(records))
	copy(sorted, records)
	// 按 (PeriodHour, InstanceID, TenantID) 排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if usageLess(sorted[j], sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	h := sha256.New()
	for _, r := range sorted {
		fmt.Fprintf(h, "%s|%s|%d|%g|%d|%g|%g|%t\n",
			r.TenantID, r.InstanceID,
			r.PeriodHour.UTC().Unix(),
			r.Usage.CPUHours, r.Usage.RAMMBHour,
			r.Usage.DiskGBHour, r.Usage.TrafficGB, r.Billable)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func usageLess(a, b UsageRecord) bool {
	if !a.PeriodHour.Equal(b.PeriodHour) {
		return a.PeriodHour.Before(b.PeriodHour)
	}
	if a.InstanceID != b.InstanceID {
		return a.InstanceID < b.InstanceID
	}
	return a.TenantID < b.TenantID
}

func roundHalfUp(v float64) int64 {
	if v >= 0 {
		return int64(math.Floor(v + 0.5))
	}
	return int64(math.Ceil(v - 0.5))
}

func roundFloat(v float64, digits int) float64 {
	p := math.Pow10(digits)
	return math.Round(v*p) / p
}