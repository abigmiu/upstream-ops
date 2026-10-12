package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/monitor"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

type fakeGatewayResort struct {
	called int
}

func (f *fakeGatewayResort) ResortRoutesOnRateScan(_ context.Context) {
	f.called++
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "scheduler-test.db"),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestRunRetentionDeletesAnnouncements(t *testing.T) {
	db := openTestDB(t)
	announcements := storage.NewUpstreamAnnouncements(db)
	notifies := storage.NewNotifications(db)
	monLogs := storage.NewMonitorLogs(db)
	rates := storage.NewRates(db)

	oldTime := time.Now().AddDate(0, 0, -10)
	if _, err := announcements.Sync(1, []storage.UpstreamAnnouncement{{
		ChannelID:   1,
		SourceKey:   "old",
		Content:     "old",
		FirstSeenAt: oldTime,
	}}); err != nil {
		t.Fatalf("sync announcement: %v", err)
	}

	s := New(
		config.SchedulerConfig{
			Retention: config.RetentionConfig{
				AnnouncementsDays: 1,
			},
		},
		&monitor.Service{},
		monLogs,
		rates,
		notifies,
		announcements,
		nil,
		nil,
		nil,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	s.runRetention()

	list, total, err := announcements.ListPage(1, 10)
	if err != nil {
		t.Fatalf("list announcements: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("announcements not cleaned: total=%d list=%#v", total, list)
	}
}

func TestRunRatesTriggersGatewayResort(t *testing.T) {
	gatewayResort := &fakeGatewayResort{}
	s := New(
		config.SchedulerConfig{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		gatewayResort,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	s.runRates()

	if gatewayResort.called != 1 {
		t.Fatalf("gateway resort calls = %d, want 1", gatewayResort.called)
	}
}

type fakeReconcileSync struct {
	calls int
	err   error
}

func (f *fakeReconcileSync) RunIncremental(_ context.Context) error {
	f.calls++
	return f.err
}

func TestRunReconcileInvokesHook(t *testing.T) {
	fake := &fakeReconcileSync{}
	s := New(
		config.SchedulerConfig{},
		nil, nil, nil, nil, nil, nil, nil, nil,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	s.SetReconcileSync(fake)

	s.runReconcile()

	if fake.calls != 1 {
		t.Fatalf("reconcile sync calls = %d, want 1", fake.calls)
	}
}

func TestRunReconcileNoHookIsSafe(t *testing.T) {
	s := New(
		config.SchedulerConfig{},
		nil, nil, nil, nil, nil, nil, nil, nil,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	// 未注入 hook 时不应 panic。
	s.runReconcile()
}

type fakeVendorSnapshot struct {
	calls int
}

func (f *fakeVendorSnapshot) SyncFromMonitoring() (int, error) {
	f.calls++
	return 1, nil
}

// 只有注入了服务时，对账 / 余额快照两个 cron 才会被注册。
// 这是 main.go 与 runtime 重建 scheduler 时必须重新注入服务的契约：
// 漏注入不会报错，但对应的定时任务会静默消失（历史 bug）。
func TestStartRegistersCronsOnlyWhenServiceInjected(t *testing.T) {
	cfg := config.SchedulerConfig{
		ReconcileCron:      "0 9 * * * *",
		VendorSnapshotCron: "50 */15 * * * *",
	}
	newSch := func() *Scheduler {
		return New(cfg, nil, nil, nil, nil, nil, nil, nil, nil,
			config.ProxyConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}

	// 未注入服务：两个 cron 都不注册。
	bare := newSch()
	if err := bare.Start(); err != nil {
		t.Fatalf("start bare: %v", err)
	}
	if n := len(bare.cron.Entries()); n != 0 {
		t.Fatalf("entries without services = %d, want 0", n)
	}
	bare.Stop()

	// 注入服务后：两个 cron 都注册。
	bound := newSch()
	bound.SetReconcileSync(&fakeReconcileSync{})
	bound.SetVendorSnapshotService(&fakeVendorSnapshot{})
	if err := bound.Start(); err != nil {
		t.Fatalf("start bound: %v", err)
	}
	if n := len(bound.cron.Entries()); n != 2 {
		t.Fatalf("entries with services = %d, want 2", n)
	}
	bound.Stop()
}
