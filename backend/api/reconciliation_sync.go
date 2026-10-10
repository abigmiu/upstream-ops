package api

import (
	"context"
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
	return reconcile.NewSyncService(client, d.Accounting, reconcile.SyncConfig{
		QuotaPerUSD:        cfg.QuotaPerUSD,
		NonCashTopupLogIDs: nonCash,
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
		start, end, res.Topups, res.UsageRows, res.UserRows, "")
	if _, err := d.Accounting.CreateSyncTaskLog(log); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"topups": res.Topups, "usage": res.UsageRows, "users": res.UserRows,
		"window_start": start, "window_end": end,
	}})
}

// testNewAPIHandler 校验站点配置。
func testNewAPIHandler(c *gin.Context, d *Deps) {
	if err := d.validateReconcileSite(c.Request.Context()); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
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
