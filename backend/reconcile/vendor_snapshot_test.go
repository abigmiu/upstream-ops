package reconcile

import (
	"strconv"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

// 自动采集：上游余额取自监控渠道的最新余额，累计已用 = 累计净到账 − 余额。
func TestVendorSnapshotServiceSyncsFromMonitoring(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	channels := storage.NewChannels(db)

	// 上游 + 台账净到账 200。
	v := &storage.Vendor{Name: "快照上游"}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if _, err := acc.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: v.ID, EntryType: storage.LedgerEntryRecharge, Amount: 200, ActualAmount: 200,
		OccurredAt: day("2026-06-01"),
	}); err != nil {
		t.Fatalf("ledger: %v", err)
	}

	// 监控渠道，最新余额 80。
	bal := 80.0
	ch := &storage.Channel{
		Name: "快照渠道", Type: storage.ChannelTypeNewAPI,
		SiteURL: "https://x.example.com", Username: "u", LastBalance: &bal,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("channel: %v", err)
	}
	if err := acc.BindUpstreamChannel(v.ID, strconv.FormatUint(uint64(ch.ID), 10), ch.Name, ""); err != nil {
		t.Fatalf("bind: %v", err)
	}

	svc := NewVendorSnapshotService(acc, channels)
	n, err := svc.SyncFromMonitoring()
	if err != nil {
		t.Fatalf("SyncFromMonitoring: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}

	usage, err := acc.LatestVendorUsage()
	if err != nil {
		t.Fatalf("LatestVendorUsage: %v", err)
	}
	p := usage[v.ID]
	// 累计已用 = 200 − 80 = 120；余额 = 80。
	if p.Balance != 80 || p.Used != 120 {
		t.Fatalf("usage = %+v, want used=120 balance=80", p)
	}
}

// 未绑定监控渠道的上游不产生自动快照。
func TestVendorSnapshotServiceSkipsUnbound(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	channels := storage.NewChannels(db)

	v := &storage.Vendor{Name: "无绑定上游"}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	bal := 10.0
	if err := channels.Create(&storage.Channel{
		Name: "游离渠道", Type: storage.ChannelTypeNewAPI,
		SiteURL: "https://y.example.com", Username: "u", LastBalance: &bal,
	}); err != nil {
		t.Fatalf("channel: %v", err)
	}

	svc := NewVendorSnapshotService(acc, channels)
	if n, err := svc.SyncFromMonitoring(); err != nil || n != 0 {
		t.Fatalf("written = %d err = %v, want 0, nil", n, err)
	}
}
