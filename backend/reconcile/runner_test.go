package reconcile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/storage"
)

// newFakeNewAPI 返回一个最小可用的 NewAPI 假服务：
// 渠道列表 2 个、充值日志 1 条、用户余额 2 个、渠道消费统计 1 条。
func newFakeNewAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	mux.HandleFunc("/api/channel/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"success":true,"data":{"total":2,"items":[{"id":1,"name":"渠道5-【GPT】 Pro","group":"uo-5-17","tag":"upstream-ops-managed"},{"id":2,"name":"渠道B","group":"默认","tag":""}]}}`)
	})
	mux.HandleFunc("/api/log/stat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"success":true,"data":{"total":0,"items":[]}}`)
	})
	mux.HandleFunc("/api/log/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"success":true,"data":{"total":1,"items":[{"id":101,"username":"alice","type":1,"created_at":1700000000,"content":"使用在线充值成功，充值金额: ＄10.000000"}]}}`)
	})
	mux.HandleFunc("/api/user/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"success":true,"data":{"total":2,"items":[{"id":1,"username":"alice","quota":500000,"used_quota":100000,"request_count":3},{"id":2,"username":"bob","quota":0,"used_quota":0,"request_count":0}]}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunnerNoopWithoutConfig(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	r := NewRunner(acc, func() config.ReconciliationConfig { return config.ReconciliationConfig{} })
	if err := r.RunIncremental(context.Background()); err != nil {
		t.Fatalf("noop runner returned err: %v", err)
	}
	// 未配置站点时不应写入任何同步日志。
	logs, err := acc.ListSyncTaskLogs(10)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected no sync logs, got %d", len(logs))
	}
}

func TestRunnerIncrementalSyncsAgainstFakeSite(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	srv := newFakeNewAPI(t)

	cfg := config.ReconciliationConfig{
		Enabled: true, SiteURL: srv.URL, Token: "tok", UserID: "1", QuotaPerUSD: 500000,
	}
	r := NewRunner(acc, func() config.ReconciliationConfig { return cfg })

	if err := r.RunIncremental(context.Background()); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	// 渠道缓存应写入 2 个。
	chans, err := acc.ListReconciliationChannels()
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	if len(chans) != 2 {
		t.Fatalf("channels = %d, want 2", len(chans))
	}
	// 充值日志应写入 1 条，金额 $10。
	topups, err := acc.ListTopupLogs()
	if err != nil {
		t.Fatalf("topups: %v", err)
	}
	if len(topups) != 1 {
		t.Fatalf("topups = %d, want 1", len(topups))
	}
	if topups[0].Amount < 9.99 || topups[0].Amount > 10.01 {
		t.Fatalf("topup amount = %v, want ~10", topups[0].Amount)
	}
	// 用户余额快照不再随窗口同步产生：增量同步后不应有余额快照。
	if captured, err := acc.LatestUserBalanceSnapshotAt(); err != nil || captured != nil {
		t.Fatalf("window sync wrote user balance snapshot: captured=%v err=%v", captured, err)
	}
	// 同步日志应写入 1 条 success。
	logs, err := acc.ListSyncTaskLogs(10)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Status != "success" {
		t.Fatalf("sync logs = %#v, want 1 success", logs)
	}
	if logs[0].Source != "cron" {
		t.Fatalf("sync log source = %q, want cron", logs[0].Source)
	}

	// 游标应推进到接近当前时间。
	if cur := acc.SyncCursor(); cur.IsZero() {
		t.Fatalf("sync cursor not advanced")
	}
}

// 用户余额快照为独立任务：单独调用 RunUserBalance 才写入 2 条。
func TestRunnerUserBalanceSnapshot(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	srv := newFakeNewAPI(t)

	cfg := config.ReconciliationConfig{
		Enabled: true, SiteURL: srv.URL, Token: "tok", UserID: "1", QuotaPerUSD: 500000,
	}
	r := NewRunner(acc, func() config.ReconciliationConfig { return cfg })

	if err := r.RunUserBalance(context.Background()); err != nil {
		t.Fatalf("run user balance: %v", err)
	}

	captured, err := acc.LatestUserBalanceSnapshotAt()
	if err != nil || captured == nil {
		t.Fatalf("latest user balance: %v", err)
	}
	users, err := acc.ListUserBalanceSnapshotsAt(*captured)
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("user snapshots = %d, want 2", len(users))
	}
	logs, err := acc.ListSyncTaskLogs(10)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Status != "success" || logs[0].UserBalanceCount != 2 {
		t.Fatalf("sync logs = %#v, want 1 success with user_balance_count=2", logs)
	}
}

// 未配置站点时 RunUserBalance 是 no-op。
func TestRunnerUserBalanceNoopWithoutConfig(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	r := NewRunner(acc, func() config.ReconciliationConfig { return config.ReconciliationConfig{} })
	if err := r.RunUserBalance(context.Background()); err != nil {
		t.Fatalf("noop user balance returned err: %v", err)
	}
	if captured, _ := acc.LatestUserBalanceSnapshotAt(); captured != nil {
		t.Fatalf("noop wrote a snapshot: %v", captured)
	}
}

// 增量同步会为托管渠道自动建立归属（端到端：拉渠道 → 解析 group → 绑定）。
func TestRunnerAutoBindsManagedChannels(t *testing.T) {
	db := newDB(t)
	acc := storage.NewAccounting(db)
	srv := newFakeNewAPI(t)

	// 预先建立「监控渠道5 → vendor」映射（手工维护的那一步）。
	v := &storage.Vendor{Name: "100hub", Enabled: true}
	if err := acc.CreateVendor(v); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if err := acc.BindUpstreamChannel(v.ID, "5", "100hub", ""); err != nil {
		t.Fatalf("bind upstream: %v", err)
	}

	cfg := config.ReconciliationConfig{
		Enabled: true, SiteURL: srv.URL, Token: "tok", UserID: "1", QuotaPerUSD: 500000,
	}
	r := NewRunner(acc, func() config.ReconciliationConfig { return cfg })
	if err := r.RunIncremental(context.Background()); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	// 托管渠道 1（group uo-5-17）应被自动绑到 vendor。
	b, err := acc.ActiveBindingFor(1)
	if err != nil {
		t.Fatalf("active binding: %v", err)
	}
	if b == nil {
		t.Fatalf("managed channel 1 not auto-bound")
	}
	if b.VendorID != v.ID || b.Note != autoBindNote {
		t.Fatalf("binding = (vendor %d, note %q), want (%d, %q)", b.VendorID, b.Note, v.ID, autoBindNote)
	}
	// 非托管渠道 2 不应被自动绑定。
	if b2, _ := acc.ActiveBindingFor(2); b2 != nil {
		t.Fatalf("non-managed channel 2 got bound: %#v", b2)
	}
}
