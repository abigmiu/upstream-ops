package reconcile

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// fakeUsageSource 是 UsageSource 的测试替身：无需真实 PostgreSQL。
type fakeUsageSource struct {
	byChannel map[uint]float64
	byDay     []ChannelUsageDay
	err       error
	closed    bool
}

func (f *fakeUsageSource) UsageByChannel(context.Context, time.Time, time.Time) (map[uint]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byChannel, nil
}

func (f *fakeUsageSource) UsageByChannelDay(context.Context, time.Time, time.Time) ([]ChannelUsageDay, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byDay, nil
}

func (f *fakeUsageSource) Close() error { f.closed = true; return nil }

// bindVendorFor 建一个上游并把渠道绑上去（消费只有绑定后才会计入）。
func bindVendorFor(t *testing.T, acc *storage.Accounting, channelID uint, name string) *storage.Vendor {
	t.Helper()
	v := &storage.Vendor{Name: "上游X", Enabled: true}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if _, err := acc.BindChannel(storage.BindChannelInput{
		ChannelID: channelID, ChannelName: name, VendorID: v.ID,
	}); err != nil {
		t.Fatalf("bind channel: %v", err)
	}
	return v
}

// 配置了 PG 源时，SyncWindow 的渠道消费走直连库，而不是逐渠道 HTTP。
func TestSyncWindowPrefersPGSource(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	srv := newFakeNewAPI(t) // 其 /api/log/stat 不返回 quota → HTTP 路径会得到 0
	bindVendorFor(t, acc, 1, "渠道5-【GPT】 Pro")

	client := NewNewAPIClient(NewAPIClientConfig{SiteURL: srv.URL, Token: "tok", UserID: "1", QuotaPerUSD: 500000})
	pg := &fakeUsageSource{byChannel: map[uint]float64{1: 100}}
	svc := NewSyncService(client, acc, SyncConfig{QuotaPerUSD: 500000, PG: pg})

	if _, err := svc.SyncWindow(context.Background(), time.Now().Add(-time.Hour), time.Now(), "accumulate"); err != nil {
		t.Fatalf("sync window: %v", err)
	}
	rows, err := acc.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(rows))
	}
	if math.Abs(rows[0].Amount-100) > 1e-6 {
		t.Fatalf("amount = %v, want 100 (来自 PG，而非 HTTP 的 0)", rows[0].Amount)
	}
}

// PG 源报错时回退逐渠道 HTTP，整体不失败。
func TestSyncWindowFallsBackToHTTPOnPGError(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	srv := newFakeNewAPI(t)
	bindVendorFor(t, acc, 1, "渠道5-【GPT】 Pro")
	// HTTP 回退路径按渠道缓存逐个拉取，故需先有渠道缓存。
	if err := acc.SaveReconciliationChannels([]storage.ReconciliationChannel{
		{ChannelID: 1, ChannelName: "渠道5-【GPT】 Pro", IsActive: true},
	}); err != nil {
		t.Fatalf("save channels: %v", err)
	}

	client := NewNewAPIClient(NewAPIClientConfig{SiteURL: srv.URL, Token: "tok", UserID: "1", QuotaPerUSD: 500000})
	pg := &fakeUsageSource{err: errors.New("pg down")}
	svc := NewSyncService(client, acc, SyncConfig{QuotaPerUSD: 500000, PG: pg})

	if _, err := svc.SyncWindow(context.Background(), time.Now().Add(-time.Hour), time.Now(), "accumulate"); err != nil {
		t.Fatalf("sync window should not fail on PG error: %v", err)
	}
	rows, err := acc.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	// 回退到 HTTP：fake 的 stat 无 quota → 0，但仍写入一行（证明走了 HTTP 路径）。
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want 1 (HTTP fallback)", len(rows))
	}
}

// BackfillUsageFromPG 按天 replace 写入历史。
func TestBackfillUsageFromPG(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	bindVendorFor(t, acc, 7, "ch7")

	pg := &fakeUsageSource{byDay: []ChannelUsageDay{
		{ChannelID: 7, Day: "2026-07-01", Amount: 12.5},
		{ChannelID: 7, Day: "2026-07-02", Amount: 3.25},
	}}
	svc := NewSyncService(nil, acc, SyncConfig{QuotaPerUSD: 500000, PG: pg})

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, storage.Shanghai)
	end := time.Date(2026, 7, 3, 0, 0, 0, 0, storage.Shanghai)
	n, err := svc.BackfillUsageFromPG(context.Background(), start, end)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 2 {
		t.Fatalf("written = %d, want 2", n)
	}
	rows, err := acc.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("usage rows = %d, want 2", len(rows))
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Day] = r.Amount
	}
	if math.Abs(got["2026-07-01"]-12.5) > 1e-6 || math.Abs(got["2026-07-02"]-3.25) > 1e-6 {
		t.Fatalf("backfilled days = %#v", got)
	}

	// 重复执行应幂等（replace：同一天先删后写，不累加）。
	if _, err := svc.BackfillUsageFromPG(context.Background(), start, end); err != nil {
		t.Fatalf("backfill rerun: %v", err)
	}
	rows2, _ := acc.ListChannelUsageStats()
	if len(rows2) != 2 {
		t.Fatalf("rows after rerun = %d, want 2 (idempotent)", len(rows2))
	}
}

// 未配置 PG 时回填报错。
func TestBackfillUsageWithoutPGErrors(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	svc := NewSyncService(nil, acc, SyncConfig{QuotaPerUSD: 500000})
	if _, err := svc.BackfillUsageFromPG(context.Background(), time.Now().Add(-time.Hour), time.Now()); err == nil {
		t.Fatalf("expected error when PG not configured")
	}
}

// Close 释放 PG 源。
func TestSyncServiceCloseClosesPG(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	pg := &fakeUsageSource{}
	svc := NewSyncService(nil, acc, SyncConfig{PG: pg})
	svc.Close()
	if !pg.closed {
		t.Fatalf("PG source not closed")
	}
}
