package reconcile

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: filepath.Join(t.TempDir(), "recon.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func day(s string) time.Time {
	tm, _ := time.ParseInLocation("2006-01-02", s, storage.Shanghai)
	return tm
}

// 复刻 Python test_dashboard_range_does_not_mix_interval_entries_with_latest_snapshot。
func TestRangeDoesNotMixSnapshot(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	v := &storage.Vendor{Name: "上游A", Enabled: true}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	// 两笔充值：06-01 100、06-10 50
	if _, err := acc.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: v.ID, EntryType: storage.LedgerEntryRecharge, Amount: 100, ActualAmount: 100, OccurredAt: day("2026-06-01")}); err != nil {
		t.Fatalf("e1: %v", err)
	}
	if _, err := acc.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: v.ID, EntryType: storage.LedgerEntryRecharge, Amount: 50, ActualAmount: 50, OccurredAt: day("2026-06-10")}); err != nil {
		t.Fatalf("e2: %v", err)
	}
	// 快照 06-20：已耗 120 余额 30
	if _, err := acc.UpsertVendorSnapshot(storage.VendorSnapshotInput{
		VendorID: v.ID, UsedAmount: 120, CurrentBalance: 30, CapturedAt: day("2026-06-20")}); err != nil {
		t.Fatalf("snap: %v", err)
	}
	// 消费 06-10：40（先绑定渠道 1）
	if _, err := acc.BindChannel(storage.BindChannelInput{ChannelID: 1, ChannelName: "c1", VendorID: v.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := acc.SaveChannelUsageStats([]storage.ChannelUsageInput{
		{ChannelID: 1, ChannelName: "c1", Amount: 40, StartAt: day("2026-06-10"), EndAt: day("2026-06-10").Add(23*time.Hour + 59*time.Minute)},
	}, "replace"); err != nil {
		t.Fatalf("usage: %v", err)
	}

	dash, err := Build(db, Range{StartDay: "2026-06-10", EndDay: "2026-06-10"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(dash.Vendors) != 1 {
		t.Fatalf("vendors = %d, want 1", len(dash.Vendors))
	}
	vrow := dash.Vendors[0]
	if vrow.RechargeNet != 50 {
		t.Errorf("rechargeNet = %v, want 50", vrow.RechargeNet)
	}
	if vrow.UsedAmount != 0 {
		t.Errorf("used_amount = %v, want 0 (range 模式不带快照)", vrow.UsedAmount)
	}
	if vrow.CurrentBalance != 0 {
		t.Errorf("current_balance = %v, want 0", vrow.CurrentBalance)
	}
	if vrow.TheoreticalBalance != 50 {
		t.Errorf("theoreticalBalance = %v, want 50", vrow.TheoreticalBalance)
	}
	if vrow.LedgerDrift != -50 {
		t.Errorf("ledgerDrift = %v, want -50", vrow.LedgerDrift)
	}
	if vrow.ChannelUsage != 40 {
		t.Errorf("channel_usage = %v, want 40", vrow.ChannelUsage)
	}
	if vrow.TheoreticalSpread != 40 {
		t.Errorf("theoreticalSpread = %v, want 40", vrow.TheoreticalSpread)
	}
}

// 无区间时应带快照，且全量口径正确。
func TestFullDashboardUsesSnapshot(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	v := &storage.Vendor{Name: "上游B", Enabled: true}
	acc.CreateVendor(v)
	acc.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: v.ID, EntryType: storage.LedgerEntryRecharge, Amount: 200, ActualAmount: 150, OccurredAt: day("2026-06-01")})
	acc.UpsertVendorSnapshot(storage.VendorSnapshotInput{
		VendorID: v.ID, UsedAmount: 120, CurrentBalance: 80, CapturedAt: day("2026-06-20")})

	dash, err := Build(db, Range{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	vrow := dash.Vendors[0]
	// 理论余额 = 充值净额 200 - 已耗 120 = 80；与当前余额 80 一致 → drift 0
	if vrow.TheoreticalBalance != 80 {
		t.Errorf("theoreticalBalance = %v, want 80", vrow.TheoreticalBalance)
	}
	if vrow.LedgerDrift != 0 {
		t.Errorf("ledgerDrift = %v, want 0", vrow.LedgerDrift)
	}
	if dash.Summary.TotalVendorBalance != 80 {
		t.Errorf("total_vendor_balance = %v, want 80", dash.Summary.TotalVendorBalance)
	}
	if dash.Summary.TotalActualRechargeNet != 150 {
		t.Errorf("total_actual_recharge_net = %v, want 150", dash.Summary.TotalActualRechargeNet)
	}
}

// 利润公式：到手已消费利润 = 提现净额 - 上游实付净充值 - 运营费用。
func TestProfitFormulas(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	v := &storage.Vendor{Name: "上游C", Enabled: true}
	acc.CreateVendor(v)
	acc.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: v.ID, EntryType: storage.LedgerEntryRecharge, Amount: 100, ActualAmount: 80, OccurredAt: day("2026-06-01")})
	// 提现 500，扣减 50 → 净 450
	acc.CreateThirdPartyEntry(&storage.ThirdPartyEntry{PlatformName: "p", EntryType: storage.ThirdPartyWithdrawal, Amount: 500, OccurredAt: day("2026-06-02")})
	acc.CreateThirdPartyEntry(&storage.ThirdPartyEntry{PlatformName: "p", EntryType: storage.ThirdPartyDeduction, Amount: 50, OccurredAt: day("2026-06-02")})
	// 运营费用 30
	acc.CreateOperatingExpense(&storage.OperatingExpense{Category: "server", Amount: 30, OccurredAt: day("2026-06-03")})

	dash, err := Build(db, Range{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if dash.Summary.TotalWithdrawal != 450 {
		t.Errorf("withdrawal = %v, want 450", dash.Summary.TotalWithdrawal)
	}
	// realized = 450 - 80 - 30 = 340
	if dash.Summary.EstimatedRealizedProfit != 340 {
		t.Errorf("realized = %v, want 340", dash.Summary.EstimatedRealizedProfit)
	}
}

// 自用充值应从总充值中剔除。
func TestSelfTopupSplit(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	acc.SaveTopupLogs([]storage.TopupLogInput{
		{ID: 1, Username: "me", LogType: "在线充值", Amount: 30, OccurredAt: day("2026-06-01")},
		{ID: 2, Username: "user", LogType: "在线充值", Amount: 70, OccurredAt: day("2026-06-01")},
	})
	acc.SetSelfUsernames([]string{"me"})

	dash, err := Build(db, Range{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if dash.Summary.TotalTopup != 100 {
		t.Errorf("total_topup = %v, want 100", dash.Summary.TotalTopup)
	}
	if dash.Summary.TotalSelfTopup != 30 {
		t.Errorf("self_topup = %v, want 30", dash.Summary.TotalSelfTopup)
	}
	if dash.Summary.TotalExternalTopup != 70 {
		t.Errorf("external_topup = %v, want 70", dash.Summary.TotalExternalTopup)
	}
}
