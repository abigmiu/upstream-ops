package storage

import (
	"testing"
	"time"
)

// LatestVendorUsage 应返回每个上游 captured_at 最新的一条快照（used/balance），
// 这是监控页与对账页对齐「累计消费」的口径来源。
func TestLatestVendorUsagePicksNewestPerVendor(t *testing.T) {
	a := newTestAccounting(t)
	v1 := mustVendor(t, a, "usage-上游A")
	v2 := mustVendor(t, a, "usage-上游B")

	day := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v1.ID, UsedAmount: 10, CurrentBalance: 90, CapturedAt: day,
	}); err != nil {
		t.Fatalf("v1 old snapshot: %v", err)
	}
	// 同一天更晚的一条，应覆盖前一条。
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v1.ID, UsedAmount: 30, CurrentBalance: 70, CapturedAt: day.Add(3 * time.Hour),
	}); err != nil {
		t.Fatalf("v1 new snapshot: %v", err)
	}
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v2.ID, UsedAmount: 5, CurrentBalance: 95, CapturedAt: day,
	}); err != nil {
		t.Fatalf("v2 snapshot: %v", err)
	}

	got, err := a.LatestVendorUsage()
	if err != nil {
		t.Fatalf("LatestVendorUsage: %v", err)
	}
	if p := got[v1.ID]; p.Used != 30 || p.Balance != 70 {
		t.Fatalf("v1 = %+v, want used=30 balance=70", p)
	}
	if p := got[v2.ID]; p.Used != 5 || p.Balance != 95 {
		t.Fatalf("v2 = %+v, want used=5 balance=95", p)
	}
}

// 没有任何快照的上游不应出现在结果里（调用方据此回退到面板自报值）。
func TestLatestVendorUsageSkipsVendorsWithoutSnapshot(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "usage-无快照")

	got, err := a.LatestVendorUsage()
	if err != nil {
		t.Fatalf("LatestVendorUsage: %v", err)
	}
	if _, ok := got[v.ID]; ok {
		t.Fatalf("vendor %d without snapshot should be absent, got %+v", v.ID, got[v.ID])
	}
}

// NetRechargeByVendor = Σ(充值/补款) − Σ(退款/扣减)，是「累计已用」自动推算的输入。
func TestNetRechargeByVendor(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "net-上游")
	at := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)
	entries := []LedgerEntryInput{
		{VendorID: v.ID, EntryType: LedgerEntryRecharge, Amount: 100, ActualAmount: 100, OccurredAt: at},
		{VendorID: v.ID, EntryType: LedgerEntryTopup, Amount: 20, ActualAmount: 20, OccurredAt: at},
		{VendorID: v.ID, EntryType: LedgerEntryRefund, Amount: 5, ActualAmount: 5, OccurredAt: at},
		{VendorID: v.ID, EntryType: LedgerEntryDeduction, Amount: 3, ActualAmount: 3, OccurredAt: at},
	}
	for _, e := range entries {
		if _, err := a.CreateVendorLedgerEntry(e); err != nil {
			t.Fatalf("ledger entry: %v", err)
		}
	}
	got, err := a.NetRechargeByVendor()
	if err != nil {
		t.Fatalf("NetRechargeByVendor: %v", err)
	}
	// 100 + 20 − 5 − 3 = 112
	if got[v.ID] != 112 {
		t.Fatalf("net = %v, want 112", got[v.ID])
	}
}
