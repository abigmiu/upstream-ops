package storage

import (
	"testing"
	"time"
)

func dayAt(y, m, d, h int) time.Time {
	return time.Date(y, time.Month(m), d, h, 0, 0, 0, Shanghai)
}

// 同一天的多次同步应合并成一行：计数求和、失败优先、区间取并集、消息拼接。
func TestSyncTaskLogDayMerge(t *testing.T) {
	a := newTestAccounting(t)
	d := dayAt(2026, 6, 1, 10)

	okStart := d
	okEnd := d.Add(5 * time.Minute)
	first, err := a.CreateSyncTaskLog(SyncTaskLogInput{
		JobName: "reconcile", Category: "reconcile", Source: "cron", Status: "success",
		WindowStart: &okStart, WindowEnd: &okEnd,
		TopupCount: 2, UsageCount: 3, UserBalanceCount: 10,
		Message: "窗口1完成", StartedAt: d, CompletedAt: d,
	})
	if err != nil {
		t.Fatalf("first log: %v", err)
	}
	if first.TopupCount != 2 || first.Status != "success" {
		t.Fatalf("first = %+v", first)
	}

	// 第二个窗口，时间靠后、且失败。
	laterStart := d.Add(10 * time.Minute)
	laterEnd := d.Add(15 * time.Minute)
	second, err := a.CreateSyncTaskLog(SyncTaskLogInput{
		JobName: "reconcile", Category: "reconcile", Source: "cron", Status: "failed",
		WindowStart: &laterStart, WindowEnd: &laterEnd,
		TopupCount: 1, UsageCount: 4, UserBalanceCount: 20,
		Message: "窗口2失败", StartedAt: d, CompletedAt: d.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("second log: %v", err)
	}
	if second.Status != "failed" {
		t.Fatalf("status = %q, want failed (fail wins)", second.Status)
	}
	if second.TopupCount != 3 || second.UsageCount != 7 || second.UserBalanceCount != 30 {
		t.Fatalf("counts = %d/%d/%d, want 3/7/30", second.TopupCount, second.UsageCount, second.UserBalanceCount)
	}

	// 日志仍是一行。
	logs, err := a.ListSyncTaskLogs(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	got := logs[0]
	if got.WindowStart == nil || !got.WindowStart.Equal(okStart) {
		t.Fatalf("window_start = %v, want %v (min)", got.WindowStart, okStart)
	}
	if got.WindowEnd == nil || !got.WindowEnd.Equal(laterEnd) {
		t.Fatalf("window_end = %v, want %v (max)", got.WindowEnd, laterEnd)
	}
	if got.Message != "窗口1完成; 窗口2失败" {
		t.Fatalf("message = %q, want concatenated", got.Message)
	}
	if !got.CompletedAt.Equal(d.Add(time.Minute)) {
		t.Fatalf("completed_at = %v, want latest", got.CompletedAt)
	}
}

// 重复写入同一条消息不应重复拼接（幂等）。
func TestSyncTaskLogMessageIdempotent(t *testing.T) {
	a := newTestAccounting(t)
	d := dayAt(2026, 6, 2, 10)
	in := SyncTaskLogInput{
		JobName: "reconcile", Category: "reconcile", Source: "cron", Status: "success",
		Message: "同一消息", StartedAt: d, CompletedAt: d,
	}
	if _, err := a.CreateSyncTaskLog(in); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := a.CreateSyncTaskLog(in); err != nil {
		t.Fatalf("second: %v", err)
	}
	logs, _ := a.ListSyncTaskLogs(10)
	if len(logs) != 1 || logs[0].Message != "同一消息" {
		t.Fatalf("message = %q, want single", logs[0].Message)
	}
}

// accumulate 模式下更早（或相等）窗口的 end_at 不应再次累加金额。
func TestChannelUsageAccumulateIgnoresOlderWindow(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游窗口")
	if _, err := a.BindChannel(BindChannelInput{ChannelID: 12, ChannelName: "ch12", VendorID: v.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	day := dayAt(2026, 6, 1, 9)

	// 先写一个较晚的窗口 9:10-9:15，金额 10。
	if _, err := a.SaveChannelUsageStats([]ChannelUsageInput{
		{ChannelID: 12, ChannelName: "ch12", Amount: 10, StartAt: day.Add(10 * time.Minute), EndAt: day.Add(15 * time.Minute)},
	}, "accumulate"); err != nil {
		t.Fatalf("late window: %v", err)
	}
	// 再补一个更早的窗口 9:00-9:05，金额 5；end_at 更小 → 不应累加。
	if _, err := a.SaveChannelUsageStats([]ChannelUsageInput{
		{ChannelID: 12, ChannelName: "ch12", Amount: 5, StartAt: day, EndAt: day.Add(5 * time.Minute)},
	}, "accumulate"); err != nil {
		t.Fatalf("early window: %v", err)
	}

	rows, err := a.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Amount != 10 {
		t.Fatalf("amount = %v, want 10 (older window ignored)", rows[0].Amount)
	}
}

// replace 模式：整天补数，重放应覆盖而非累加。
func TestChannelUsageReplaceOverwrites(t *testing.T) {
	a := newTestAccounting(t)
	v := mustVendor(t, a, "上游替换")
	if _, err := a.BindChannel(BindChannelInput{ChannelID: 13, ChannelName: "ch13", VendorID: v.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	day := dayAt(2026, 6, 1, 9)
	in := []ChannelUsageInput{
		{ChannelID: 13, ChannelName: "ch13", Amount: 42, StartAt: day, EndAt: day.Add(time.Hour)},
	}
	if _, err := a.SaveChannelUsageStats(in, "replace"); err != nil {
		t.Fatalf("replace1: %v", err)
	}
	if _, err := a.SaveChannelUsageStats(in, "replace"); err != nil {
		t.Fatalf("replace2: %v", err)
	}
	rows, err := a.ListChannelUsageStats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Amount != 42 {
		t.Fatalf("rows = %+v, want single 42", rows)
	}
}

// 更早采集时间的用户余额快照不应覆盖当天较新的记录。
func TestUserBalanceSnapshotKeepsNewerCapture(t *testing.T) {
	a := newTestAccounting(t)
	newer := dayAt(2026, 6, 1, 12)
	older := dayAt(2026, 6, 1, 9)

	if _, err := a.SaveUserBalanceSnapshots([]UserBalanceInput{
		{Username: "alice", RemainingAmount: 100, UsedAmount: 5, RequestCount: 1},
	}, newer, map[string]bool{}); err != nil {
		t.Fatalf("newer: %v", err)
	}
	// 更早的采集不应覆盖。
	if _, err := a.SaveUserBalanceSnapshots([]UserBalanceInput{
		{Username: "alice", RemainingAmount: 1, UsedAmount: 99, RequestCount: 9},
	}, older, map[string]bool{}); err != nil {
		t.Fatalf("older: %v", err)
	}
	captured, err := a.LatestUserBalanceSnapshotAt()
	if err != nil || captured == nil {
		t.Fatalf("latest: %v", err)
	}
	rows, err := a.ListUserBalanceSnapshotsAt(*captured)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].RemainingAmount != 100 {
		t.Fatalf("rows = %+v, want remaining 100 (newer kept)", rows)
	}
}
