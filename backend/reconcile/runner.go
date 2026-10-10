package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/storage"
)

// ConfigProvider 返回当前对账配置（从 runtime 热读，支持设置页改站点后即时生效）。
type ConfigProvider func() config.ReconciliationConfig

// Runner 把「读取当前配置 → 构造客户端 → 增量同步」串起来，供调度器调用。
type Runner struct {
	acc  *storage.Accounting
	prov ConfigProvider
}

// NewRunner 构造增量同步 Runner。
func NewRunner(acc *storage.Accounting, prov ConfigProvider) *Runner {
	return &Runner{acc: acc, prov: prov}
}

// RunIncremental 执行一次增量同步。未配置站点或未启用时直接返回 nil（不视为错误）。
func (r *Runner) RunIncremental(ctx context.Context) error {
	if r == nil || r.acc == nil {
		return nil
	}
	cfg := r.prov()
	if cfg.SiteURL == "" || cfg.Token == "" {
		return nil
	}
	client := NewNewAPIClient(NewAPIClientConfig{
		SiteURL:     cfg.SiteURL,
		Token:       cfg.Token,
		UserID:      cfg.UserID,
		QuotaPerUSD: cfg.QuotaPerUSD,
	})
	nonCash := make(map[int64]bool, len(cfg.NonCashTopupLogIDs))
	for _, id := range cfg.NonCashTopupLogIDs {
		nonCash[id] = true
	}
	svc := NewSyncService(client, r.acc, SyncConfig{
		QuotaPerUSD:        cfg.QuotaPerUSD,
		NonCashTopupLogIDs: nonCash,
	})

	start := r.acc.SyncCursor()
	end := time.Now()
	startedAt := time.Now()

	if err := svc.SyncChannelsQuiet(ctx); err != nil {
		_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
			JobName: "window-sync", Category: "daily", Source: "cron", Status: "failed",
			Message: fmt.Sprintf("同步渠道失败: %v", err), StartedAt: startedAt, CompletedAt: time.Now(),
		})
		return err
	}
	res, err := svc.SyncWindow(ctx, start, end, "accumulate")
	if err != nil {
		_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
			JobName: "window-sync", Category: "daily", Source: "cron", Status: "failed",
			Message: err.Error(), StartedAt: startedAt, CompletedAt: time.Now(),
		})
		return err
	}
	r.acc.SetSyncCursor(end)
	_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
		JobName: "window-sync", Category: "daily", Source: "cron", Status: "success",
		WindowStart: &start, WindowEnd: &end,
		TopupCount: res.Topups, UsageCount: res.UsageRows, UserBalanceCount: res.UserRows,
		StartedAt: startedAt, CompletedAt: time.Now(),
	})
	return nil
}
