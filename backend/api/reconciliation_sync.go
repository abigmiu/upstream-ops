package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/bejix/upstream-ops/backend/reconcile"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

// 对账子系统的 NewAPI 同步接口。站点配置读取自 cfg.Reconciliation（可在设置页编辑）。

// reconciliationSyncDeps 返回构造好的同步服务与配置；未配置站点时返回错误。
func (d *Deps) newSyncService() (*reconcile.SyncService, error) {
	cfg := d.Runtime.CurrentReconciliation()
	client := reconcile.NewNewAPIClient(reconcile.NewAPIClientConfig{
		SiteURL:     cfg.SiteURL,
		Token:       cfg.Token,
		UserID:      cfg.UserID,
		QuotaPerUSD: cfg.QuotaPerUSD,
	})
	nonCash := make(map[int64]bool, len(cfg.NonCashTopupLogIDs))
	for _, id := range cfg.NonCashTopupLogIDs {
		nonCash[id] = true
	}
	// 配置了 pgDsn 则直连 new-api 的库取渠道消费；构造失败只记日志并回退逐渠道 HTTP。
	var pg reconcile.UsageSource
	if cfg.PGDSN != "" {
		p, err := reconcile.NewPGUsageSource(cfg.PGDSN, cfg.QuotaPerUSD)
		if err != nil {
			log.Printf("[reconcile] 初始化 PG 数据源失败，回退逐渠道 HTTP: %v", err)
		} else {
			pg = p
		}
	}
	return reconcile.NewSyncService(client, d.Accounting, reconcile.SyncConfig{
		QuotaPerUSD:        cfg.QuotaPerUSD,
		NonCashTopupLogIDs: nonCash,
		PG:                 pg,
	}), nil
}

func (d *Deps) validateReconcileSite(ctx context.Context) error {
	cfg := d.Runtime.CurrentReconciliation()
	client := reconcile.NewNewAPIClient(reconcile.NewAPIClientConfig{
		SiteURL: cfg.SiteURL, Token: cfg.Token, UserID: cfg.UserID, QuotaPerUSD: cfg.QuotaPerUSD,
	})
	return client.Validate(ctx)
}

// syncChannelsHandler 同步渠道列表。
func syncChannelsHandler(c *gin.Context, d *Deps) {
	svc, err := d.newSyncService()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	n, err := svc.SyncChannels(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"channels": n}})
}

type syncRangeInput struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// syncRangeHandler 同步指定日期区间（默认昨天）。整天补数用 replace 模式。
func syncRangeHandler(c *gin.Context, d *Deps) {
	var in syncRangeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	svc, err := d.newSyncService()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer svc.Close()
	start, end, err := parseDateRange(in.StartDate, in.EndDate)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	startedAt := time.Now()
	if _, err := svc.SyncChannels(ctx); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	// 逐日 replace 补数。
	totalTopups, totalUsage := 0, 0
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		dayEnd := day.Add(24*time.Hour - time.Second)
		res, err := svc.SyncWindow(ctx, day, dayEnd, "replace")
		if err != nil {
			_, _ = d.Accounting.CreateSyncTaskLog(syncLogFromErr(d, "range-sync", "manual", startedAt, err))
			fail(c, http.StatusBadGateway, err)
			return
		}
		totalTopups += res.Topups
		totalUsage += res.UsageRows
	}
	completedAt := time.Now()
	log := syncLogFromResult("range-sync", "manual", startedAt, completedAt,
		start, end.Add(24*time.Hour-time.Second), totalTopups, totalUsage, 0, "")
	if _, err := d.Accounting.CreateSyncTaskLog(log); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"topups": totalTopups, "usage": totalUsage}})
}

// syncIncrementalHandler 增量同步：从上次成功窗口至今。
func syncIncrementalHandler(c *gin.Context, d *Deps) {
	svc, err := d.newSyncService()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	start := d.Accounting.SyncCursor()
	end := time.Now()
	// 窗口按 10 分钟对齐到分钟，避免重复累加。
	startedAt := time.Now()
	if err := svc.SyncChannelsQuiet(ctx); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	res, err := svc.SyncWindow(ctx, start, end, "accumulate")
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	completedAt := time.Now()
	d.Accounting.SetSyncCursor(end)
	log := syncLogFromResult("window-sync", "manual", startedAt, completedAt,
		start, end, res.Topups, res.UsageRows, 0, "")
	if _, err := d.Accounting.CreateSyncTaskLog(log); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"topups": res.Topups, "usage": res.UsageRows,
		"window_start": start, "window_end": end,
	}})
}

// syncUserBalanceHandler 全量拉取用户余额并落成当天快照（手动触发）。
// 余额平时由每日 cron 采样；此接口用于需要即时刷新时手动补采一次。
func syncUserBalanceHandler(c *gin.Context, d *Deps) {
	svc, err := d.newSyncService()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	startedAt := time.Now()
	n, err := svc.SyncUserBalance(ctx, startedAt)
	if err != nil {
		_, _ = d.Accounting.CreateSyncTaskLog(syncLogFromErr(d, "user-balance", "manual", startedAt, err))
		fail(c, http.StatusBadGateway, err)
		return
	}
	completedAt := time.Now()
	log := syncLogFromResult("user-balance", "manual", startedAt, completedAt,
		startedAt, completedAt, 0, 0, n, "")
	if _, err := d.Accounting.CreateSyncTaskLog(log); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"users": n}})
}

// syncBackfillUsageHandler 从 new-api 的 PostgreSQL 直连回填历史渠道消费（按天 replace，可重复执行）。
// 请求体 {start_date,end_date} 可选：缺省 start 取 2000-01-01、end 取今天，即回填全部历史。
// 仅当配置了 reconciliation.pgDsn 时可用；否则报错（回退逐日 HTTP 补数请用 /sync/range）。
func syncBackfillUsageHandler(c *gin.Context, d *Deps) {
	var in syncRangeInput
	_ = c.ShouldBindJSON(&in) // 允许空 body
	svc, err := d.newSyncService()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer svc.Close()

	now := time.Now().In(storage.Shanghai)
	start, end, reachedPresent, err := resolveBackfillRange(now, in.StartDate, in.EndDate)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	startedAt := time.Now()
	n, err := svc.BackfillUsageFromPG(ctx, start, end)
	if err != nil {
		_, _ = d.Accounting.CreateSyncTaskLog(syncLogFromErr(d, "backfill-usage", "manual", startedAt, err))
		fail(c, http.StatusBadGateway, err)
		return
	}
	completedAt := time.Now()
	// 回填已覆盖到此刻：把游标推进到现在，避免后续增量把已回填的区间再加一遍。
	if reachedPresent {
		d.Accounting.SetSyncCursor(completedAt)
	}
	entry := syncLogFromResult("backfill-usage", "manual", startedAt, completedAt, start, end, 0, n, 0, "")
	if _, err := d.Accounting.CreateSyncTaskLog(entry); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"usage": n, "start": start, "end": end}})
}

// testNewAPIHandler 校验站点配置。
func testNewAPIHandler(c *gin.Context, d *Deps) {
	if err := d.validateReconcileSite(c.Request.Context()); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// resolveBackfillRange 解析回填区间：start 缺省 2000-01-01，end 缺省今天，均为上海时区整天。
// 返回右开区间 [start, end)，以及该区间是否覆盖到 now（用来决定是否推进同步游标）。
//
// 覆盖到 now 时把 end 截到 now：若写成当天 23:59:59（未来），之后每次增量窗口的 end_at
// 都比它小，会被 SaveChannelUsageStats 的幂等保护挡住，导致当天后续用量一直漏掉。
func resolveBackfillRange(now time.Time, startStr, endStr string) (start, end time.Time, reachedPresent bool, err error) {
	loc := now.Location()
	start = time.Date(2000, 1, 1, 0, 0, 0, 0, loc)
	if startStr != "" {
		if start, err = time.ParseInLocation("2006-01-02", startStr, loc); err != nil {
			return
		}
	}
	endDay := now
	if endStr != "" {
		if endDay, err = time.ParseInLocation("2006-01-02", endStr, loc); err != nil {
			return
		}
	}
	end = endDay.Add(24 * time.Hour) // 右开区间，含 end 当天
	if reachedPresent = end.After(now); reachedPresent {
		end = now
	}
	return
}

// parseDateRange 解析 [start, end] 日期（YYYY-MM-DD，上海时区）。
// 都为空时默认「昨天」。start 起点为当天 0 点，end 为当天 0 点。
func parseDateRange(startStr, endStr string) (time.Time, time.Time, error) {
	loc := storage.Shanghai
	if startStr == "" && endStr == "" {
		y := time.Now().In(loc).AddDate(0, 0, -1)
		d := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, loc)
		return d, d, nil
	}
	start, err := time.ParseInLocation("2006-01-02", startStr, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end := start
	if endStr != "" {
		end, err = time.ParseInLocation("2006-01-02", endStr, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if end.Before(start) {
		start, end = end, start
	}
	return start, end, nil
}

// syncLogFromResult 组装一条同步日志输入。
func syncLogFromResult(job, source string, startedAt, completedAt, winStart, winEnd time.Time, topups, usage, users int, message string) storage.SyncTaskLogInput {
	return storage.SyncTaskLogInput{
		JobName: job, Category: "daily", Source: source, Status: "success",
		WindowStart: &winStart, WindowEnd: &winEnd,
		TopupCount: topups, UsageCount: usage, UserBalanceCount: users,
		Message: message, StartedAt: startedAt, CompletedAt: completedAt,
	}
}

// syncLogFromErr 组装一条失败同步日志输入。
func syncLogFromErr(_ *Deps, job, source string, startedAt time.Time, err error) storage.SyncTaskLogInput {
	now := time.Now()
	return storage.SyncTaskLogInput{
		JobName: job, Category: "daily", Source: source, Status: "failed",
		Message: err.Error(), StartedAt: startedAt, CompletedAt: now,
	}
}
