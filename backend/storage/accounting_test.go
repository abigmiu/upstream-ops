package storage

import (
	"testing"
	"time"
)

func newTestAccounting(t *testing.T) *Accounting {
	t.Helper()
	db, err := Open(DBConfig{Driver: DBDriverSQLite, Path: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewAccounting(db)
}

func mustVendor(t *testing.T, a *Accounting, name string) *Vendor {
	t.Helper()
	v := &Vendor{Name: name, Enabled: true}
	if err := a.CreateVendor(v); err != nil {
		t.Fatalf("create vendor %s: %v", name, err)
	}
	return v
}

func TestLedgerRebuildBalances(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游A")
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, Shanghai)

	// 充值 100
	e1, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: v.ID, EntryType: LedgerEntryRecharge, Amount: 100, ActualAmount: 80, OccurredAt: base,
	})
	if err != nil {
		t.Fatalf("entry1: %v", err)
	}
	if e1.BalanceBefore != 0 || e1.BalanceAfter != 100 {
		t.Fatalf("entry1 balances = %v/%v, want 0/100", e1.BalanceBefore, e1.BalanceAfter)
	}

	// 补款 50 → 150
	e2, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: v.ID, EntryType: LedgerEntryTopup, Amount: 50, ActualAmount: 50, OccurredAt: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("entry2: %v", err)
	}
	if e2.BalanceAfter != 150 {
		t.Fatalf("entry2 after = %v, want 150", e2.BalanceAfter)
	}

	// 退款 30 → 120
	e3, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: v.ID, EntryType: LedgerEntryRefund, Amount: 30, ActualAmount: 30, OccurredAt: base.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("entry3: %v", err)
	}
	if e3.BalanceAfter != 120 {
		t.Fatalf("entry3 after = %v, want 120", e3.BalanceAfter)
	}

	// 在最早时间点插一条 → 后续余额应整体重算。
	e0, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: v.ID, EntryType: LedgerEntryRecharge, Amount: 10, ActualAmount: 10, OccurredAt: base.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("entry0: %v", err)
	}
	if e0.BalanceAfter != 10 {
		t.Fatalf("entry0 after = %v, want 10", e0.BalanceAfter)
	}

	rows, err := a.ListVendorLedgerEntries()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 期望按时间：e0(10) e1(110) e2(160) e3(130)
	want := []float64{10, 110, 160, 130}
	got := map[uint]float64{}
	for _, r := range rows {
		got[r.ID] = r.BalanceAfter
	}
	for i, id := range []uint{e0.ID, e1.ID, e2.ID, e3.ID} {
		if got[id] != want[i] {
			t.Fatalf("entry %d balance = %v, want %v", id, got[id], want[i])
		}
	}
}

func TestLedgerRejectsNonPositive(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游B")
	_, err := a.CreateVendorLedgerEntry(LedgerEntryInput{
		VendorID: v.ID, EntryType: LedgerEntryRecharge, Amount: 0, ActualAmount: 10, OccurredAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestVendorSnapshotDayUpsert(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游C")
	day1 := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)

	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v.ID, UsedAmount: 10, CurrentBalance: 90, CapturedAt: day1,
	}); err != nil {
		t.Fatalf("snap1: %v", err)
	}
	// 同一天的更晚采集应覆盖。
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v.ID, UsedAmount: 20, CurrentBalance: 80, CapturedAt: day1.Add(6 * time.Hour),
	}); err != nil {
		t.Fatalf("snap2: %v", err)
	}
	// 同一天但更早的采集不应覆盖。
	if _, err := a.UpsertVendorSnapshot(VendorSnapshotInput{
		VendorID: v.ID, UsedAmount: 5, CurrentBalance: 95, CapturedAt: day1.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("snap3: %v", err)
	}

	snaps, err := a.ListVendorSnapshots()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps))
	}
	if snaps[0].UsedAmount != 20 || snaps[0].CurrentBalance != 80 {
		t.Fatalf("snapshot = %+v, want used 20 bal 80", snaps[0])
	}
}

func TestChannelUsageAccumulateIdempotent(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游D")
	// 绑定渠道 7 → vendor
	if _, err := a.BindChannel(BindChannelInput{ChannelID: 7, ChannelName: "ch7", VendorID: v.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	day := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)

	// 第一个窗口 9:00-9:05
	w1 := ChannelUsageInput{ChannelID: 7, ChannelName: "ch7", Amount: 1, StartAt: day, EndAt: day.Add(5 * time.Minute)}
	if _, err := a.SaveChannelUsageStats([]ChannelUsageInput{w1}, "accumulate"); err != nil {
		t.Fatalf("save w1: %v", err)
	}
	// 重试同一窗口 → 不累加
	if _, err := a.SaveChannelUsageStats([]ChannelUsageInput{w1}, "accumulate"); err != nil {
		t.Fatalf("retry w1: %v", err)
	}
	// 第二个窗口 → 累加
	w2 := ChannelUsageInput{ChannelID: 7, ChannelName: "ch7", Amount: 2, StartAt: day.Add(5 * time.Minute), EndAt: day.Add(10 * time.Minute)}
	if _, err := a.SaveChannelUsageStats([]ChannelUsageInput{w2}, "accumulate"); err != nil {
		t.Fatalf("save w2: %v", err)
	}

	rows, err := a.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(rows))
	}
	if rows[0].Amount != 3 {
		t.Fatalf("accumulated amount = %v, want 3", rows[0].Amount)
	}
}

func TestChannelUsageSkipsUnbound(t *testing.T) {
	a := newTestAccounting(t)
	day := time.Date(2026, 6, 1, 9, 0, 0, 0, Shanghai)
	n, err := a.SaveChannelUsageStats([]ChannelUsageInput{
		{ChannelID: 99, ChannelName: "no-bind", Amount: 5, StartAt: day, EndAt: day.Add(time.Minute)},
	}, "accumulate")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if n != 0 {
		t.Fatalf("written = %d, want 0 (unbound skipped)", n)
	}
}

func TestTopupDailyRebuildAndToggle(t *testing.T) {
	a := newTestAccounting(t)
	base := time.Date(2026, 6, 1, 8, 0, 0, 0, Shanghai)
	logs := []TopupLogInput{
		{ID: 1, Username: "u1", LogType: "在线充值", Amount: 10, OccurredAt: base, Content: "c1"},
		{ID: 2, Username: "u2", LogType: "在线充值", Amount: 20, OccurredAt: base.Add(time.Hour), Content: "c2"},
	}
	if _, _, err := a.SaveTopupLogs(logs); err != nil {
		t.Fatalf("save: %v", err)
	}
	daily, err := a.ListDailyTopups()
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(daily) != 1 || daily[0].Amount != 30 {
		t.Fatalf("daily = %+v, want one day 30", daily)
	}
	// 标记 log 2 为非现金 → 汇总变 10
	if err := a.SetTopupIncludedInCash(2, false); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	daily, _ = a.ListDailyTopups()
	if daily[0].Amount != 10 {
		t.Fatalf("daily after toggle = %v, want 10", daily[0].Amount)
	}
	// 再次同步同一条日志，人工标记应被保留（仍为 10）。
	if _, _, err := a.SaveTopupLogs(logs); err != nil {
		t.Fatalf("resave: %v", err)
	}
	daily, _ = a.ListDailyTopups()
	if daily[0].Amount != 10 {
		t.Fatalf("daily after resave = %v, want 10 (manual flag preserved)", daily[0].Amount)
	}
}

func TestEnsureDeletedVendor(t *testing.T) {
	a := newTestAccounting(t)
	v1, err := a.EnsureDeletedVendor()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	v2, err := a.EnsureDeletedVendor()
	if err != nil {
		t.Fatalf("ensure2: %v", err)
	}
	if v1.ID != v2.ID {
		t.Fatalf("deleted vendor ids differ: %d vs %d", v1.ID, v2.ID)
	}
	if v1.Name != DeletedVendorName {
		t.Fatalf("name = %s, want %s", v1.Name, DeletedVendorName)
	}
}

func TestCreateVendorDuplicate(t *testing.T) {
	a := newTestAccounting(t)
	mustVendor(t, a, "dup")
	err := a.CreateVendor(&Vendor{Name: "dup", Enabled: true})
	if err != ErrVendorExists {
		t.Fatalf("err = %v, want ErrVendorExists", err)
	}
}
