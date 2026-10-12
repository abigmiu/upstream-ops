package storage

import (
	"testing"
	"time"
)

// 合并上游时，源上游与「已删除上游」若在同一天都有快照，
// 重新映射 vendor_id 会撞上 UNIQUE(vendor_id, day)。
// 期望：合并不报错，且该天只保留一条快照。
func TestMergeVendorIntoDeletedSnapshotConflict(t *testing.T) {
	a := newTestAccounting(t)
	src := mustVendor(t, a, "上游合并-源")
	target, err := a.EnsureDeletedVendor()
	if err != nil {
		t.Fatalf("ensure deleted: %v", err)
	}
	day := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)

	// 目标（已删除上游）当天一条快照。
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: target.ID, UsedAmount: 100, CurrentBalance: 900, CapturedAt: day,
	}); err != nil {
		t.Fatalf("target snapshot: %v", err)
	}
	// 源上游同一天另一条快照。
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: src.ID, UsedAmount: 50, CurrentBalance: 950, CapturedAt: day.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("src snapshot: %v", err)
	}

	if err := a.MergeVendorIntoDeleted(src.ID); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// 源上游应被删除。
	if _, err := a.FindVendorByID(src.ID); err == nil {
		t.Fatalf("source vendor still exists after merge")
	}
	// 该天在合并后只能剩一条快照。
	snaps, err := a.ListVendorSnapshots()
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	for _, s := range snaps {
		if s.VendorID != target.ID {
			t.Fatalf("snapshot vendor = %d, want %d", s.VendorID, target.ID)
		}
		if s.Day == "2026-06-01" {
			count := 0
			for _, x := range snaps {
				if x.Day == "2026-06-01" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("2026-06-01 snapshots = %d, want 1", count)
			}
		}
	}
}

// 合并上游时，源与目标绑定了同一个上游渠道 id，
// 重新映射 vendor_id 会撞上 UNIQUE(vendor_id, upstream_channel_id)。
// 期望：合并不报错，且同一上游渠道只保留一条绑定。
func TestMergeVendorIntoDeletedUpstreamBindingConflict(t *testing.T) {
	a := newTestAccounting(t)
	src := mustVendor(t, a, "上游合并-源2")
	target, err := a.EnsureDeletedVendor()
	if err != nil {
		t.Fatalf("ensure deleted: %v", err)
	}

	if err := a.BindUpstreamChannel(target.ID, "777", "共享渠道", ""); err != nil {
		t.Fatalf("bind target: %v", err)
	}
	if err := a.BindUpstreamChannel(src.ID, "777", "共享渠道", ""); err != nil {
		t.Fatalf("bind src: %v", err)
	}

	if err := a.MergeVendorIntoDeleted(src.ID); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	rows, err := a.ListUpstreamChannelBindings()
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	count := 0
	for _, r := range rows {
		if r.UpstreamChannelID == "777" {
			count++
			if r.VendorID != target.ID {
				t.Fatalf("binding vendor = %d, want %d", r.VendorID, target.ID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("bindings for 777 = %d, want 1", count)
	}
}

// 无冲突时，合并应重映射流水并按合并后的时间线重算余额。
func TestMergeVendorIntoDeletedRemapsLedger(t *testing.T) {
	a := newTestAccounting(t)
	src := mustVendor(t, a, "上游合并-源3")
	target, err := a.EnsureDeletedVendor()
	if err != nil {
		t.Fatalf("ensure deleted: %v", err)
	}
	base := time.Date(2026, 6, 1, 8, 0, 0, 0, Shanghai)

	// 目标：充值 100 @ 8:00
	if _, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: target.ID, EntryType: LedgerEntryRecharge, Amount: 100, ActualAmount: 100, OccurredAt: base,
	}); err != nil {
		t.Fatalf("target entry: %v", err)
	}
	// 源：充值 50 @ 9:00
	if _, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: src.ID, EntryType: LedgerEntryRecharge, Amount: 50, ActualAmount: 50, OccurredAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("src entry: %v", err)
	}

	if err := a.MergeVendorIntoDeleted(src.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	rows, err := a.ListVendorLedgerEntries()
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	// 按时间：8:00 → after 100；9:00 → after 150。
	// ListVendorLedgerEntries 按 occurred_at DESC 排序。
	if rows[0].VendorID != target.ID || rows[1].VendorID != target.ID {
		t.Fatalf("ledger not remapped to target: %+v", rows)
	}
	if rows[1].BalanceAfter != 100 {
		t.Fatalf("first entry after = %v, want 100", rows[1].BalanceAfter)
	}
	if rows[0].BalanceAfter != 150 {
		t.Fatalf("second entry after = %v, want 150", rows[0].BalanceAfter)
	}
}
