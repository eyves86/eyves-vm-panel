package metering

import (
	"testing"
	"time"
)

func defaultRateCard() RateCard {
	return RateCard{
		ID:               "rc-standard",
		Name:             "standard",
		Currency:         "USD",
		CPUPerHourCent:   5,   // $0.05/vCPU·h
		RAMPerHBCent:     0.1, // $0.001/MB·h
		DiskPerGBHCent:   0.2, // $0.002/GB·h
		TrafficPerGBCent: 1,   // $0.01/GB
	}
}

func sampleRecord(t *time.Time, billable bool) UsageRecord {
	return UsageRecord{
		TenantID:   "tenant-1",
		InstanceID: "i-1",
		PeriodHour: *t,
		Usage: Usage{
			CPUHours:   1.0,
			RAMMBHour:  1024,
			DiskGBHour: 10.0,
			TrafficGB:  2.0,
		},
		Billable: billable,
	}
}

func TestRateCardValidate(t *testing.T) {
	rc := defaultRateCard()
	if err := rc.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := rc
	bad.CPUPerHourCent = -1
	if err := bad.Validate(); err == nil {
		t.Fatal("negative price must error")
	}
	bad = rc
	bad.Currency = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty currency must error")
	}
	bad = rc
	bad.ID = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty id must error")
	}
}

func TestAppendUsageDedupsSameKey(t *testing.T) {
	s := NewMemoryStore()
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	r1 := sampleRecord(&t0, true)
	r1.Usage.CPUHours = 1.0
	r2 := sampleRecord(&t0, true)
	r2.Usage.CPUHours = 2.0
	if err := s.AppendUsage(r1); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendUsage(r2); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListUsage("tenant-1", t0.Add(-time.Hour), t0.Add(2*time.Hour))
	if len(got) != 1 {
		t.Fatalf("dedup failed: got %d records, want 1", len(got))
	}
	if got[0].Usage.CPUHours != 3.0 {
		t.Fatalf("CPUHours = %v, want 3.0", got[0].Usage.CPUHours)
	}
}

func TestAppendUsageRejectsBadInput(t *testing.T) {
	s := NewMemoryStore()
	if err := s.AppendUsage(UsageRecord{}); err == nil {
		t.Fatal("empty tenant must error")
	}
	zero := time.Time{}
	r := sampleRecord(&zero, true)
	if err := s.AppendUsage(r); err == nil {
		t.Fatal("zero period must error")
	}
}

func TestBuildInvoiceIgnoresNonBillable(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	recs := []UsageRecord{
		sampleRecord(&t0, true),
		sampleRecord(&t1, false),
	}
	inv, err := BuildInvoice("tenant-1", rc, recs, t0, t0.Add(2*time.Hour), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Total == 0 {
		t.Fatal("expected non-zero total")
	}
	// 非计费记录 = 1 小时 1024MB RAM 等于 1024 * 0.1 = 102.4 -> 102 cents
	// 但我们合计总额应严格按"非计费行不计入"得出：
	// CPU 1h * 5 = 5
	// RAM 1024 * 0.1 = 102.4 -> 102
	// Disk 10 * 0.2 = 2
	// Traffic 2 * 1 = 2
	// = 111 cents
	if inv.Total != 111 {
		t.Fatalf("total = %d, want 111", inv.Total)
	}
}

func TestBuildInvoiceRequiresPeriodOrder(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if _, err := BuildInvoice("t", rc, nil, t0, t0, "x"); err == nil {
		t.Fatal("zero-length period must error")
	}
	if _, err := BuildInvoice("t", rc, nil, t0.Add(time.Hour), t0, "x"); err == nil {
		t.Fatal("inverted period must error")
	}
}

// TestBuildInvoiceRejectsCrossTenantUsage 锁定计费越权修复：属于其他租户的
// 用量记录不得被计入本租户账单。
func TestBuildInvoiceRejectsCrossTenantUsage(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	hour := t0.Add(time.Hour)
	foreign := sampleRecord(&hour, true)
	foreign.TenantID = "tenant-OTHER"
	if _, err := BuildInvoice("tenant-1", rc, []UsageRecord{foreign}, t0, t0.Add(2*time.Hour), "inv-x"); err == nil {
		t.Fatal("cross-tenant usage must be rejected")
	}
	if _, err := BuildInvoice("  ", rc, nil, t0, t0.Add(time.Hour), "inv-x"); err == nil {
		t.Fatal("blank tenant id must be rejected")
	}
}

func TestSourceHashDeterministicAcrossOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	a := []UsageRecord{sampleRecord(&t0, true), sampleRecord(&t1, true)}
	b := []UsageRecord{sampleRecord(&t1, true), sampleRecord(&t0, true)}
	rc := defaultRateCard()
	end := t1.Add(time.Hour)
	invA, _ := BuildInvoice("tenant-1", rc, a, t0, end, "inv-A")
	invB, _ := BuildInvoice("tenant-1", rc, b, t0, end, "inv-B")
	if invA.SourceHash != invB.SourceHash {
		t.Fatalf("hash must be order-independent: %s vs %s", invA.SourceHash, invB.SourceHash)
	}
}

func TestReconcilePassesForValidInvoice(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	hour := t0.Add(time.Hour)
	recs := []UsageRecord{sampleRecord(&hour, true)}
	inv, err := BuildInvoice("tenant-1", rc, recs, t0, t1, "inv-x")
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(inv, rc); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
}

func TestReconcileDetectsTamperedLine(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hour := t0.Add(time.Hour)
	recs := []UsageRecord{sampleRecord(&hour, true)}
	inv, _ := BuildInvoice("tenant-1", rc, recs, t0, t0.Add(2*time.Hour), "inv-y")
	// 仅修改 Total 而不同步行明细 → sum != total 触发告警
	inv.Total += 100
	if err := Reconcile(inv, rc); err == nil {
		t.Fatal("tampered total without line update must error")
	}
}

func TestCallerHasUsageHashVerifiesReconciliation(t *testing.T) {
	rc := defaultRateCard()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hour := t0.Add(time.Hour)
	recs := []UsageRecord{sampleRecord(&hour, true)}
	inv, _ := BuildInvoice("tenant-1", rc, recs, t0, t0.Add(2*time.Hour), "inv-z")
	if !CallerHasUsageHash(recs, inv.SourceHash) {
		t.Fatal("CallerHasUsageHash must verify identical records")
	}
	tampered := append([]UsageRecord(nil), recs...)
	tampered[0].Usage.CPUHours += 1
	if CallerHasUsageHash(tampered, inv.SourceHash) {
		t.Fatal("tampered records must not match original hash")
	}
}

func TestUpsertAndGetRateCard(t *testing.T) {
	s := NewMemoryStore()
	rc := defaultRateCard()
	if err := s.UpsertRateCard(rc); err != nil {
		t.Fatal(err)
	}
	got, ok := s.GetRateCard(rc.ID)
	if !ok {
		t.Fatal("rate card not found")
	}
	if got.CPUPerHourCent != rc.CPUPerHourCent {
		t.Fatal("roundtrip mismatch")
	}
}

func TestUpsertRateCardValidates(t *testing.T) {
	s := NewMemoryStore()
	if err := s.UpsertRateCard(RateCard{ID: "x"}); err == nil {
		t.Fatal("invalid rate card must error")
	}
}

func TestListInvoicesSortsByPeriod(t *testing.T) {
	s := NewMemoryStore()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_ = s.AppendInvoice(Invoice{ID: "old", TenantID: "t", PeriodStart: t0})
	_ = s.AppendInvoice(Invoice{ID: "new", TenantID: "t", PeriodStart: t0.Add(720 * time.Hour)})
	got, _ := s.ListInvoices("t")
	if len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("expected newest first, got %v", got)
	}
}

func TestListUsageTimeRange(t *testing.T) {
	s := NewMemoryStore()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		hour := t0.Add(time.Duration(i) * time.Hour)
		_ = s.AppendUsage(sampleRecord(&hour, true))
	}
	got, _ := s.ListUsage("tenant-1", t0.Add(time.Hour), t0.Add(3*time.Hour))
	if len(got) != 2 {
		t.Fatalf("expected 2 records in range, got %d", len(got))
	}
}

func TestListUsageTenantIsolation(t *testing.T) {
	s := NewMemoryStore()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_ = s.AppendUsage(sampleRecord(&t0, true))
	got, _ := s.ListUsage("other-tenant", t0, t0.Add(time.Hour))
	if len(got) != 0 {
		t.Fatal("must filter by tenant")
	}
}

func TestAppendInvoiceIdempotent(t *testing.T) {
	s := NewMemoryStore()
	inv := Invoice{ID: "i", TenantID: "t", Total: 100}
	if err := s.AppendInvoice(inv); err != nil {
		t.Fatal(err)
	}
	inv.Total = 200
	if err := s.AppendInvoice(inv); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListInvoices("t")
	if len(got) != 1 {
		t.Fatalf("expected 1 invoice, got %d", len(got))
	}
	if got[0].Total != 200 {
		t.Fatalf("expected upsert to win, got total=%d", got[0].Total)
	}
}

func TestAppendInvoiceRejectsEmptyID(t *testing.T) {
	s := NewMemoryStore()
	if err := s.AppendInvoice(Invoice{}); err == nil {
		t.Fatal("empty invoice id must error")
	}
}