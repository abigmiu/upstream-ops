package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// 绑定上游的渠道：累计消费应改用对账口径（vendor 最新快照的 used_amount），
// 面板自报值保留在 panel_total_cost——这是让监控页与对账页「对得上」的核心行为。
func TestChannelOutputUsesReconUsageWhenBound(t *testing.T) {
	db := openTestDB(t)
	channels := storage.NewChannels(db)
	acc := storage.NewAccounting(db)

	panelTotal := 186.0524
	panelBalance := -0.0524
	ch := &storage.Channel{
		Name:        "咕咕鸟",
		Type:        storage.ChannelTypeNewAPI,
		SiteURL:     "https://g.example.com",
		Username:    "u",
		LastBalance: &panelBalance,
		TotalCost:   &panelTotal,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	vendor := &storage.Vendor{Name: "咕咕鸟"}
	if err := acc.CreateVendor(vendor); err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	// 对账快照口径：used=206.2324（= 到账 206.18 − 余额 −0.0524），与面板自报 186.05 不同。
	if _, err := acc.UpsertVendorSnapshot(storage.VendorSnapshotInput{
		VendorID: vendor.ID, UsedAmount: 206.2324, CurrentBalance: -0.0524,
		CapturedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, storage.Shanghai),
	}); err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	if err := acc.BindUpstreamChannel(vendor.ID, strconv.FormatUint(uint64(ch.ID), 10), ch.Name, ""); err != nil {
		t.Fatalf("bind: %v", err)
	}

	out := channelOutputs(&Deps{Channels: channels, Accounting: acc}, []storage.Channel{*ch})
	if len(out) != 1 {
		t.Fatalf("got %d outputs", len(out))
	}
	got := out[0]
	if got.TotalCost == nil || *got.TotalCost != 206.2324 {
		t.Fatalf("total_cost = %v, want 206.2324 (对账口径)", got.TotalCost)
	}
	if got.PanelTotalCost == nil || *got.PanelTotalCost != 186.0524 {
		t.Fatalf("panel_total_cost = %v, want 186.0524", got.PanelTotalCost)
	}
	if got.ReconBalance == nil || *got.ReconBalance != -0.0524 {
		t.Fatalf("recon_balance = %v, want -0.0524", got.ReconBalance)
	}
}

// 未绑定上游的渠道：累计消费保持面板自报值，不注入对账口径。
func TestChannelOutputKeepsPanelCostWhenUnbound(t *testing.T) {
	db := openTestDB(t)
	channels := storage.NewChannels(db)
	acc := storage.NewAccounting(db)

	panelTotal := 17.9716
	ch := &storage.Channel{
		Name:      "未绑定",
		Type:      storage.ChannelTypeNewAPI,
		SiteURL:   "https://x.example.com",
		Username:  "u",
		TotalCost: &panelTotal,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	out := channelOutputs(&Deps{Channels: channels, Accounting: acc}, []storage.Channel{*ch})
	got := out[0]
	if got.TotalCost == nil || *got.TotalCost != 17.9716 {
		t.Fatalf("total_cost = %v, want 17.9716 (面板自报)", got.TotalCost)
	}
	if got.ReconUsed != nil || got.PanelTotalCost != nil {
		t.Fatalf("unbound channel should not carry recon fields: %+v", got)
	}
}
