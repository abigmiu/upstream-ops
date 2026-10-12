package reconcile

import (
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestParseUpstreamChannelId(t *testing.T) {
	cases := []struct {
		group string
		want  uint
		ok    bool
	}{
		{"uo-5-17", 5, true},
		{"uo-4-bc1e35c3acd7", 4, true},
		{"uo-13-27", 13, true},
		{"uo-11-83", 11, true},
		{"uo-12-7799834d31f5", 12, true},
		{"默认", 0, false},
		{"gpt-0.1倍率", 0, false},
		{"uo-abc", 0, false},
		{"uo--17", 0, false},
		{"uo-0-x", 0, false},
		{"uo-5", 0, false}, // 缺尾部 '-'
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseUpstreamChannelId(tc.group)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseUpstreamChannelId(%q) = (%d,%v), want (%d,%v)", tc.group, got, ok, tc.want, tc.ok)
		}
	}
}

// seedVendorAndUpstream 建一个 vendor，并建立「监控渠道id → vendor」映射。
func seedVendorAndUpstream(t *testing.T, acc *storage.Accounting, vendorName, upstreamChannelID string) uint {
	t.Helper()
	v := &storage.Vendor{Name: vendorName, Enabled: true}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("create vendor %s: %v", vendorName, err)
	}
	if err := acc.BindUpstreamChannel(v.ID, upstreamChannelID, vendorName, ""); err != nil {
		t.Fatalf("bind upstream %s: %v", upstreamChannelID, err)
	}
	return v.ID
}

func newAutobindService(acc *storage.Accounting) *SyncService {
	return NewSyncService(nil, acc, SyncConfig{})
}

// 托管渠道 uo-5-… + 已有 vendor 绑定 → 自动建一条 channel_bindings。
func TestAutoBindManagedChannels(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	v5 := seedVendorAndUpstream(t, acc, "100hub", "5")

	chans := []RemoteChannel{
		{ChannelID: 50, ChannelName: "渠道5-【GPT】 Pro 纯享", GroupName: "uo-5-17", Tag: managedChannelTag},
		{ChannelID: 2, ChannelName: "普通渠道", GroupName: "默认"},
		{ChannelID: 51, ChannelName: "渠道9-无映射", GroupName: "uo-9-x", Tag: managedChannelTag},
	}
	bound, unresolved, err := newAutobindService(acc).autoBindManagedChannels(chans)
	if err != nil {
		t.Fatalf("autoBind: %v", err)
	}
	if bound != 1 || unresolved != 1 {
		t.Fatalf("(bound,unresolved) = (%d,%d), want (1,1)", bound, unresolved)
	}

	rows, err := acc.ListChannelBindings()
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active bindings = %d, want 1", len(rows))
	}
	if rows[0].ChannelID != 50 || rows[0].VendorID != v5 {
		t.Fatalf("binding = (chan %d, vendor %d), want (50, %d)", rows[0].ChannelID, rows[0].VendorID, v5)
	}
	if rows[0].Note != autoBindNote {
		t.Fatalf("note = %q, want %q", rows[0].Note, autoBindNote)
	}
}

// 幂等：连跑两次，active 数不变、历史不增长。
func TestAutoBindIdempotent(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	seedVendorAndUpstream(t, acc, "100hub", "5")

	chans := []RemoteChannel{
		{ChannelID: 50, ChannelName: "渠道5-A", GroupName: "uo-5-17", Tag: managedChannelTag},
		{ChannelID: 51, ChannelName: "渠道5-B", GroupName: "uo-5-8", Tag: managedChannelTag},
	}
	svc := newAutobindService(acc)
	if bound, _, err := svc.autoBindManagedChannels(chans); err != nil || bound != 2 {
		t.Fatalf("first run: bound=%d err=%v, want bound=2", bound, err)
	}
	activeAfter1, _ := acc.ListChannelBindings()
	histAfter1, _ := acc.ListChannelBindingHistory()

	bound, unresolved, err := svc.autoBindManagedChannels(chans)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if bound != 0 || unresolved != 0 {
		t.Fatalf("second run (bound,unresolved) = (%d,%d), want (0,0)", bound, unresolved)
	}
	activeAfter2, _ := acc.ListChannelBindings()
	histAfter2, _ := acc.ListChannelBindingHistory()
	if len(activeAfter2) != len(activeAfter1) {
		t.Fatalf("active bindings changed: %d -> %d", len(activeAfter1), len(activeAfter2))
	}
	if len(histAfter2) != len(histAfter1) {
		t.Fatalf("binding history grew: %d -> %d", len(histAfter1), len(histAfter2))
	}
}

// 跳过：无 vendor 绑定 / group 非 uo- 前缀 / 已有手工绑定（不同 vendor）。
func TestAutoBindSkips(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	seedVendorAndUpstream(t, acc, "100hub", "5")
	vOther := seedVendorAndUpstream(t, acc, "别的家", "99")

	// 已存在手工绑定（Note 非自动标记），指向 vOther。
	if _, err := acc.BindChannel(storage.BindChannelInput{
		ChannelID: 60, ChannelName: "渠道9-手工", VendorID: vOther, Note: "手工绑的",
	}); err != nil {
		t.Fatalf("manual bind: %v", err)
	}

	chans := []RemoteChannel{
		{ChannelID: 70, ChannelName: "无映射", GroupName: "uo-7-x", Tag: managedChannelTag},     // 监控渠道7 无 vendor
		{ChannelID: 71, ChannelName: "非托管组", GroupName: "默认", Tag: managedChannelTag},        // group 非 uo-
		{ChannelID: 60, ChannelName: "渠道5-手工", GroupName: "uo-5-17", Tag: managedChannelTag}, // 手工绑定的，不覆盖
	}
	bound, unresolved, err := newAutobindService(acc).autoBindManagedChannels(chans)
	if err != nil {
		t.Fatalf("autoBind: %v", err)
	}
	if bound != 0 || unresolved != 2 {
		t.Fatalf("(bound,unresolved) = (%d,%d), want (0,2)", bound, unresolved)
	}
	// 手工绑定必须原样保留。
	b, err := acc.ActiveBindingFor(60)
	if err != nil || b == nil {
		t.Fatalf("manual binding lost: %v", err)
	}
	if b.VendorID != vOther || b.Note != "手工绑的" {
		t.Fatalf("manual binding overwritten: %#v", b)
	}
}

// 自动标记的旧绑定在 vendor 变化时重新绑定；手工绑定不受影响。
func TestAutoBindRebindsAutoNote(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	vA := seedVendorAndUpstream(t, acc, "A家", "5")

	chans := []RemoteChannel{{ChannelID: 50, ChannelName: "渠道5-A", GroupName: "uo-5-17", Tag: managedChannelTag}}
	svc := newAutobindService(acc)
	if _, _, err := svc.autoBindManagedChannels(chans); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if b, _ := acc.ActiveBindingFor(50); b == nil || b.VendorID != vA {
		t.Fatalf("want bound to vA(%d), got %#v", vA, b)
	}

	// 监控渠道5 改归属：删掉 vA 的映射、改建到 vB。
	rows, err := acc.ListUpstreamChannelBindings()
	if err != nil {
		t.Fatalf("list upstream: %v", err)
	}
	for _, r := range rows {
		if r.UpstreamChannelID == "5" {
			if err := acc.DeleteUpstreamChannelBinding(r.ID); err != nil {
				t.Fatalf("delete upstream binding: %v", err)
			}
		}
	}
	vB := seedVendorAndUpstream(t, acc, "B家", "5")

	if _, _, err := svc.autoBindManagedChannels(chans); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	b, _ := acc.ActiveBindingFor(50)
	if b == nil || b.VendorID != vB {
		t.Fatalf("want rebound to vB(%d), got %#v", vB, b)
	}
	if b.Note != autoBindNote {
		t.Fatalf("note = %q, want %q", b.Note, autoBindNote)
	}
}
