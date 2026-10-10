package storage

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------------------------------------------------------------------------
// App settings（对账子系统 KV）
// ---------------------------------------------------------------------------

// GetSetting 读取 KV 配置，不存在时返回 def。
func (a *Accounting) GetSetting(key, def string) (string, error) {
	var s AppSetting
	err := a.db.First(&s, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	return s.Value, nil
}

// SetSetting 写入 KV 配置。
func (a *Accounting) SetSetting(key, value string) error {
	s := AppSetting{Key: key, Value: value, UpdatedAt: time.Now().In(Shanghai)}
	return a.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": value, "updated_at": s.UpdatedAt}),
	}).Create(&s).Error
}

// ---------------------------------------------------------------------------
// Topup logs（站内充值日志）
// ---------------------------------------------------------------------------

// TopupLogInput 站内充值日志入参（来自 NewAPI 同步）。
type TopupLogInput struct {
	ID         uint
	Username   string
	LogType    string
	Amount     float64
	OccurredAt time.Time
	Content    string
}

// SaveTopupLogs 批量 upsert 充值日志。已人工标记的 included_in_cash 会被保留。
// 写完后重建 daily_topups。返回 (写入数, 保留人工标记数)。
func (a *Accounting) SaveTopupLogs(logs []TopupLogInput) (int, int, error) {
	inserted := 0
	preserved := 0
	now := time.Now().In(Shanghai)
	err := a.db.Transaction(func(tx *gorm.DB) error {
		for _, item := range logs {
			var existing TopupLog
			err := tx.First(&existing, item.ID).Error
			inCash := true
			manual := false
			note := ""
			switch {
			case err == nil && existing.IncludedInCashManual:
				inCash = existing.IncludedInCash
				manual = true
				note = existing.Note
				preserved++
			case errors.Is(err, gorm.ErrRecordNotFound):
				inserted++
			case err != nil:
				return err
			}
			row := TopupLog{
				ID: item.ID, Username: item.Username, LogType: item.LogType,
				Amount: Round6(item.Amount), OccurredAt: item.OccurredAt.In(Shanghai),
				IncludedInCash: inCash, IncludedInCashManual: manual, Note: note,
				Content: item.Content, CreatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"username":    row.Username,
					"log_type":    row.LogType,
					"amount":      row.Amount,
					"occurred_at": row.OccurredAt,
					"content":     row.Content,
				}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return a.rebuildDailyTopups(tx, now)
	})
	return inserted, preserved, err
}

// rebuildDailyTopups 全量重建 daily_topups（DELETE-all 后按天汇总现金充值）。
func (a *Accounting) rebuildDailyTopups(tx *gorm.DB, now time.Time) error {
	if err := tx.Exec("DELETE FROM daily_topups").Error; err != nil {
		return err
	}
	var rows []struct {
		Day    string
		Amount float64
	}
	if err := tx.Model(&TopupLog{}).
		Select("substr(occurred_at, 1, 10) AS day, ROUND(SUM(amount), 6) AS amount").
		Where("included_in_cash = ?", true).
		Group("substr(occurred_at, 1, 10)").
		Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		d := DailyTopup{Day: r.Day, Amount: r.Amount, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&d).Error; err != nil {
			return err
		}
	}
	return nil
}

// SetTopupIncludedInCash 人工切换某充值日志的现金口径，并重建 daily_topups。
func (a *Accounting) SetTopupIncludedInCash(logID uint, inCash bool) error {
	now := time.Now().In(Shanghai)
	return a.db.Transaction(func(tx *gorm.DB) error {
		var row TopupLog
		if err := tx.First(&row, logID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("充值日志不存在")
			}
			return err
		}
		note := ""
		if !inCash {
			note = "手动标记为非现金充值"
		}
		if err := tx.Model(&TopupLog{}).Where("id = ?", logID).
			Updates(map[string]any{"included_in_cash": inCash, "included_in_cash_manual": true, "note": note}).Error; err != nil {
			return err
		}
		return a.rebuildDailyTopups(tx, now)
	})
}

// ListTopupLogs 返回充值日志（按发生时间倒序）。
func (a *Accounting) ListTopupLogs() ([]TopupLog, error) {
	var out []TopupLog
	err := a.db.Order("occurred_at DESC, id DESC").Find(&out).Error
	return out, err
}

// ListDailyTopups 返回按天充值汇总。
func (a *Accounting) ListDailyTopups() ([]DailyTopup, error) {
	var out []DailyTopup
	err := a.db.Order("day DESC").Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Channel usage stats（站内归属消费，每天一行）
// ---------------------------------------------------------------------------

// ChannelUsageInput 一条渠道消费入参。
type ChannelUsageInput struct {
	ChannelID   uint
	ChannelName string
	Amount      float64
	StartAt     time.Time
	EndAt       time.Time
}

// SaveChannelUsageStats 写入站内归属消费。
//   - mode="replace"：整天补数，先删当天再整批覆盖。
//   - mode="accumulate"：窗口同步，同一窗口重试幂等（end_at 相等不累加）。
//
// 无绑定的渠道会被跳过。返回写入数。
func (a *Accounting) SaveChannelUsageStats(rows []ChannelUsageInput, mode string) (int, error) {
	now := time.Now().In(Shanghai)
	written := 0
	replacedDays := map[string]bool{}
	err := a.db.Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			if item.ChannelID == 0 {
				continue
			}
			binding, err := currentBindingTx(tx, item.ChannelID)
			if err != nil {
				return err
			}
			if binding == nil {
				continue
			}
			day := DayString(item.StartAt)
			if mode == "replace" && !replacedDays[day] {
				if err := tx.Where("day = ?", day).Delete(&ChannelUsageStat{}).Error; err != nil {
					return err
				}
				replacedDays[day] = true
			}
			row := ChannelUsageStat{
				ChannelID: item.ChannelID, ChannelName: item.ChannelName, VendorID: binding.VendorID,
				Day: day, Amount: Round6(item.Amount),
				StartAt: item.StartAt.In(Shanghai), EndAt: item.EndAt.In(Shanghai), CreatedAt: now,
			}
			if mode == "replace" {
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "channel_id"}, {Name: "day"}},
					DoUpdates: clause.Assignments(map[string]any{
						"channel_name": row.ChannelName,
						"vendor_id":    row.VendorID,
						"amount":       row.Amount,
						"start_at":     row.StartAt,
						"end_at":       row.EndAt,
					}),
				}).Create(&row).Error; err != nil {
					return err
				}
			} else {
				// 累加模式：仅当新窗口 end_at 更大时累加（幂等重试保护）。
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "channel_id"}, {Name: "day"}},
					DoUpdates: clause.Assignments(map[string]any{
						"channel_name": row.ChannelName,
						"vendor_id":    row.VendorID,
						"amount":       gorm.Expr("channel_usage_stats.amount + ?", row.Amount),
						"start_at":     gorm.Expr("CASE WHEN ? < channel_usage_stats.start_at THEN ? ELSE channel_usage_stats.start_at END", row.StartAt, row.StartAt),
						"end_at":       gorm.Expr("CASE WHEN ? > channel_usage_stats.end_at THEN ? ELSE channel_usage_stats.end_at END", row.EndAt, row.EndAt),
					}),
					Where: clause.Where{Exprs: []clause.Expression{gorm.Expr("? > channel_usage_stats.end_at", row.EndAt)}},
				}).Create(&row).Error; err != nil {
					return err
				}
			}
			written++
		}
		return nil
	})
	return written, err
}

func currentBindingTx(tx *gorm.DB, channelID uint) (*ChannelBinding, error) {
	var b ChannelBinding
	err := tx.Where("channel_id = ?", channelID).Order("id DESC").First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ChannelUsageRow 消费列表行（带上游名）。
type ChannelUsageRow struct {
	ChannelUsageStat
	VendorName string `json:"vendor_name"`
}

// ListChannelUsageStats 返回站内归属消费（按天倒序）。
func (a *Accounting) ListChannelUsageStats() ([]ChannelUsageRow, error) {
	var out []ChannelUsageRow
	err := a.db.Table("channel_usage_stats AS c").
		Select("c.*, v.name AS vendor_name").
		Joins("JOIN vendors v ON v.id = c.vendor_id").
		Order("c.day DESC, c.vendor_id ASC, c.channel_id ASC").
		Scan(&out).Error
	return out, err
}

// EnsureChannelForUsage 确保某渠道在渠道缓存中存在（用于承载已删除渠道的消费归属）。
// 已存在则不覆盖名称。
func (a *Accounting) EnsureChannelForUsage(channelID uint, channelName string, seenAt time.Time) error {
	if channelID == 0 {
		return nil
	}
	now := seenAt.In(Shanghai)
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ReconciliationChannel{
		ChannelID: channelID, ChannelName: channelName, IsActive: true,
		FirstSeenAt: now, LastSyncedAt: now,
	}).Error
}

// ---------------------------------------------------------------------------
// User balance snapshots（用户余额快照，每天一行）
// ---------------------------------------------------------------------------

// UserBalanceInput 一条用户余额入参。
type UserBalanceInput struct {
	Username       string
	RemainingAmount float64
	UsedAmount     float64
	RequestCount   int
}

// SaveUserBalanceSnapshots 按 (username, day) 覆盖写入；capturedAt 更早不覆盖。
// selfUsernames 中的用户标记 is_self。返回写入数。
func (a *Accounting) SaveUserBalanceSnapshots(rows []UserBalanceInput, capturedAt time.Time, selfUsernames map[string]bool) (int, error) {
	captured := capturedAt.In(Shanghai)
	day := DayString(captured)
	now := time.Now().In(Shanghai)
	written := 0
	err := a.db.Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			username := strings.TrimSpace(item.Username)
			if username == "" {
				continue
			}
			row := UserBalanceSnapshot{
				Username: username, RemainingAmount: Round6(item.RemainingAmount),
				UsedAmount: Round6(item.UsedAmount), RequestCount: item.RequestCount,
				IsSelf: selfUsernames[username], Day: day, CapturedAt: captured, CreatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "username"}, {Name: "day"}},
				DoUpdates: clause.Assignments(map[string]any{
					"remaining_amount": row.RemainingAmount,
					"used_amount":      row.UsedAmount,
					"request_count":    row.RequestCount,
					"is_self":          row.IsSelf,
					"captured_at":      row.CapturedAt,
				}),
				Where: clause.Where{Exprs: []clause.Expression{gorm.Expr("? >= user_balance_snapshots.captured_at", captured)}},
			}).Create(&row).Error; err != nil {
				return err
			}
			written++
		}
		return nil
	})
	return written, err
}

// LatestUserBalanceSnapshotAt 返回最新一批用户余额快照的采集时间。
func (a *Accounting) LatestUserBalanceSnapshotAt() (*time.Time, error) {
	var row UserBalanceSnapshot
	err := a.db.Order("captured_at DESC, id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row.CapturedAt, nil
}

// ListUserBalanceSnapshotsAt 返回指定采集时间的全部用户余额快照。
func (a *Accounting) ListUserBalanceSnapshotsAt(capturedAt time.Time) ([]UserBalanceSnapshot, error) {
	var out []UserBalanceSnapshot
	err := a.db.Where("captured_at = ?", capturedAt).Order("username ASC").Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Sync task logs（同步执行日志，每天一行）
// ---------------------------------------------------------------------------

// SyncTaskLogInput 同步日志入参。
type SyncTaskLogInput struct {
	JobName          string
	Category         string
	Source           string
	Status           string
	WindowStart      *time.Time
	WindowEnd        *time.Time
	TopupCount       int
	UsageCount       int
	UserBalanceCount int
	Message          string
	StartedAt        time.Time
	CompletedAt      time.Time
}

// CreateSyncTaskLog 按天合并写入同步日志（失败优先、计数求和、消息拼接）。
func (a *Accounting) CreateSyncTaskLog(in SyncTaskLogInput) (*SyncTaskLog, error) {
	day := DayString(in.CompletedAt)
	category := in.Category
	if category == "" {
		category = in.JobName
	}
	row := SyncTaskLog{
		Day: day, JobName: in.JobName, Category: category, Source: in.Source, Status: in.Status,
		WindowStart: in.WindowStart, WindowEnd: in.WindowEnd,
		TopupCount: in.TopupCount, UsageCount: in.UsageCount, UserBalanceCount: in.UserBalanceCount,
		Message: in.Message, StartedAt: in.StartedAt.In(Shanghai), CompletedAt: in.CompletedAt.In(Shanghai),
	}
	err := a.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status": gorm.Expr("CASE WHEN sync_task_logs.status = 'failed' OR excluded.status = 'failed' THEN 'failed' ELSE 'success' END"),
			"window_start": gorm.Expr(`CASE
				WHEN sync_task_logs.window_start IS NULL THEN excluded.window_start
				WHEN excluded.window_start IS NULL THEN sync_task_logs.window_start
				WHEN excluded.window_start < sync_task_logs.window_start THEN excluded.window_start
				ELSE sync_task_logs.window_start END`),
			"window_end": gorm.Expr(`CASE
				WHEN sync_task_logs.window_end IS NULL THEN excluded.window_end
				WHEN excluded.window_end IS NULL THEN sync_task_logs.window_end
				WHEN excluded.window_end > sync_task_logs.window_end THEN excluded.window_end
				ELSE sync_task_logs.window_end END`),
			"topup_count":        gorm.Expr("sync_task_logs.topup_count + excluded.topup_count"),
			"usage_count":        gorm.Expr("sync_task_logs.usage_count + excluded.usage_count"),
			"user_balance_count": gorm.Expr("sync_task_logs.user_balance_count + excluded.user_balance_count"),
			"message": gorm.Expr(`CASE
				WHEN excluded.message = '' THEN sync_task_logs.message
				WHEN sync_task_logs.message = '' THEN excluded.message
				WHEN instr(sync_task_logs.message, excluded.message) > 0 THEN sync_task_logs.message
				ELSE sync_task_logs.message || '; ' || excluded.message END`),
			"completed_at": gorm.Expr("CASE WHEN excluded.completed_at > sync_task_logs.completed_at THEN excluded.completed_at ELSE sync_task_logs.completed_at END"),
		}),
	}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	var out SyncTaskLog
	if err := a.db.Where("day = ?", day).First(&out).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSyncTaskLogs 返回同步日志（按完成时间倒序）。
func (a *Accounting) ListSyncTaskLogs(limit int) ([]SyncTaskLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []SyncTaskLog
	err := a.db.Order("completed_at DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}
