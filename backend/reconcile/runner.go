package reconcile

import (
	"context"
	"fmt"
	"log"
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

// newSyncService 按当前配置构造同步服务；未配置站点/Token 时返回 ok=false（调用方视为 no-op）。
func (r *Runner) newSyncService() (*SyncService, bool) {
	if r == nil || r.acc == nil {
		return nil, false
	}
	cfg := r.prov()
	if cfg.SiteURL == "" || cfg.Token == "" {
		return nil, false
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
	// 配置了 pgDsn 则直连 new-api 的库取渠道消费；构造失败（DSN 非法）只记日志并回退 HTTP。
	var pg UsageSource
	if cfg.PGDSN != "" {
		p, err := NewPGUsageSource(cfg.PGDSN, cfg.QuotaPerUSD)
		if err != nil {
			log.Printf("[reconcile] 初始化 PG 数据源失败，回退逐渠道 HTTP: %v", err)
		} else {
			pg = p
		}
	}
	return NewSyncService(client, r.acc, SyncConfig{
		QuotaPerUSD:        cfg.QuotaPerUSD,
		NonCashTopupLogIDs: nonCash,
		PG:                 pg,
	}), true
}

// RunIncremental 执行一次增量同步。未配置站点或未启用时直接返回 nil（不视为错误）。
func (r *Runner) RunIncremental(ctx context.Context) error {
	svc, ok := r.newSyncService()
	if !ok {
		return nil
	}
	defer svc.Close()

	// 从游标所在自然日 0 点起、以 replace 绝对重算受影响的天：
	// 反复执行结果一致，且窗口重叠（重试 / 并发）也不会重复累加。
	start := DayFloor(r.acc.SyncCursor())
	end := time.Now()
	startedAt := time.Now()

	if err := svc.SyncChannelsQuiet(ctx); err != nil {
		_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
			JobName: "window-sync", Category: "daily", Source: "cron", Status: "failed",
			Message: fmt.Sprintf("同步渠道失败: %v", err), StartedAt: startedAt, CompletedAt: time.Now(),
		})
		return err
	}
	res, err := svc.SyncWindow(ctx, start, end, "replace")
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
		TopupCount: res.Topups, UsageCount: res.UsageRows,
		StartedAt: startedAt, CompletedAt: time.Now(),
	})
	return nil
}

// RunUserBalance 全量拉取用户余额并落成当天快照。
// 独立每日任务：余额只需低频采集，与高频窗口同步解耦，避免每次窗口都全量拉一遍用户。
func (r *Runner) RunUserBalance(ctx context.Context) error {
	svc, ok := r.newSyncService()
	if !ok {
		return nil
	}
	defer svc.Close()
	startedAt := time.Now()
	n, err := svc.SyncUserBalance(ctx, startedAt)
	if err != nil {
		_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
			JobName: "user-balance", Category: "daily", Source: "cron", Status: "failed",
			Message: err.Error(), StartedAt: startedAt, CompletedAt: time.Now(),
		})
		return err
	}
	_, _ = r.acc.CreateSyncTaskLog(storage.SyncTaskLogInput{
		JobName: "user-balance", Category: "daily", Source: "cron", Status: "success",
		UserBalanceCount: n, StartedAt: startedAt, CompletedAt: time.Now(),
	})
	return nil
}
