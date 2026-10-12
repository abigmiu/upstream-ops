package reconcile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

// TestAutoBindLiveDataDryRun 用线上真实库的只读副本试跑 autoBindManagedChannels：
// 断言「只新增 17 条绑定、不动任何现有行、无覆盖、幂等」。默认跳过，
// 设置 UO_LIVE_DB=<线上 sqlite 路径> 后运行。
func TestAutoBindLiveDataDryRun(t *testing.T) {
	live := os.Getenv("UO_LIVE_DB")
	if live == "" {
		t.Skip("set UO_LIVE_DB to the live sqlite path to run this dry run")
	}

	// 复制到临时目录（含 WAL/SHM），保证原库零写入。
	dir := t.TempDir()
	dst := filepath.Join(dir, "live.db")
	if err := copyFile(live, dst); err != nil {
		t.Fatalf("copy live db: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(live + suffix); err == nil {
			if err := copyFile(live+suffix, dst+suffix); err != nil {
				t.Fatalf("copy %s: %v", suffix, err)
			}
		}
	}

	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: dst})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	acc := storage.NewAccounting(db)

	// 真实托管渠道（new-api 生成，group = uo-…）。
	var rc []storage.ReconciliationChannel
	if err := db.Where("group_name LIKE ?", "uo-%").Order("channel_id ASC").Find(&rc).Error; err != nil {
		t.Fatalf("read reconciliation_channels: %v", err)
	}
	chans := make([]RemoteChannel, 0, len(rc))
	for _, c := range rc {
		chans = append(chans, RemoteChannel{
			ChannelID: c.ChannelID, ChannelName: c.ChannelName, GroupName: c.GroupName, Tag: managedChannelTag,
		})
	}
	if len(chans) != 17 {
		t.Fatalf("live managed channels = %d, want 17", len(chans))
	}

	before, err := acc.ListChannelBindings()
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	beforeHist, err := acc.ListChannelBindingHistory()
	if err != nil {
		t.Fatalf("list history: %v", err)
	}

	bound, unresolved, err := newAutobindService(acc).autoBindManagedChannels(chans)
	if err != nil {
		t.Fatalf("autoBind: %v", err)
	}
	t.Logf("试跑结果: bound=%d unresolved=%d (现有 active=%d hist=%d)", bound, unresolved, len(before), len(beforeHist))
	if bound != 17 || unresolved != 0 {
		t.Fatalf("(bound,unresolved) = (%d,%d), want (17,0)", bound, unresolved)
	}

	after, _ := acc.ListChannelBindings()
	afterHist, _ := acc.ListChannelBindingHistory()
	if len(after) != len(before)+17 {
		t.Fatalf("active bindings %d -> %d, want +17", len(before), len(after))
	}
	if len(afterHist) != len(beforeHist)+17 {
		t.Fatalf("history %d -> %d, want +17", len(beforeHist), len(afterHist))
	}

	// 现有行必须一行不动：同 id 的字段完全一致。
	afterByID := make(map[uint]storage.ChannelBindingRow, len(after))
	for _, r := range after {
		afterByID[r.ID] = r
	}
	for _, b := range before {
		a, ok := afterByID[b.ID]
		if !ok {
			t.Fatalf("pre-existing binding id=%d disappeared", b.ID)
		}
		if a.ChannelID != b.ChannelID || a.VendorID != b.VendorID || a.Note != b.Note ||
			a.EffectiveTo != b.EffectiveTo || !a.EffectiveFrom.Equal(b.EffectiveFrom) {
			t.Fatalf("pre-existing binding id=%d changed: before=%#v after=%#v", b.ID, b, a)
		}
	}

	// 新增的 17 条必须落在 49–65，且 vendor 与 group 解析一致。
	wantVendor := map[uint]uint{
		49: 8, 50: 2, 51: 2, 52: 2, 53: 2, 54: 2, 55: 2, 56: 5,
		57: 14, 58: 14, 59: 14, 60: 15, 61: 16, 62: 16, 63: 2, 64: 14, 65: 14,
	}
	seenNew := 0
	for _, r := range after {
		if _, isNew := wantVendor[r.ChannelID]; !isNew {
			continue
		}
		seenNew++
		if r.VendorID != wantVendor[r.ChannelID] {
			t.Fatalf("channel %d bound to vendor %d, want %d", r.ChannelID, r.VendorID, wantVendor[r.ChannelID])
		}
		if r.Note != autoBindNote {
			t.Fatalf("channel %d note=%q, want %q", r.ChannelID, r.Note, autoBindNote)
		}
	}
	if seenNew != 17 {
		t.Fatalf("new managed bindings = %d, want 17", seenNew)
	}

	// 幂等：再跑一次不新增、不报错。
	if b2, u2, err := newAutobindService(acc).autoBindManagedChannels(chans); err != nil || b2 != 0 || u2 != 0 {
		t.Fatalf("second run: bound=%d unresolved=%d err=%v, want 0/0/nil", b2, u2, err)
	}
}
