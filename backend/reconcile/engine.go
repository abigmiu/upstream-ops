// Package reconcile 实现站长对账的核心计算。
//
// 直接移植自 Python 版 ai_proxy_money 的 store.build_dashboard_range /
// latest_user_balance_summary，公式与口径保持逐字一致，便于对账结果可比。
package reconcile

import (
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

// Range 对账区间。两端都为空时表示「全量（含快照）」模式。
type Range struct {
	StartDay string // YYYY-MM-DD，空表示不限
	EndDay   string
}

func (r Range) HasRange() bool {
	return r.StartDay != "" && r.EndDay != ""
}

// VendorRow 单个上游的对账结果。
type VendorRow struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Note  string `json:"note"`
	Alias string `json:"alias"`

	Recharge        float64 `json:"recharge"`
	ActualRecharge  float64 `json:"actual_recharge"`
	Refund          float64 `json:"refund"`
	ActualRefund    float64 `json:"actual_refund"`
	Deduction       float64 `json:"deduction"`
	ActualDeduction float64 `json:"actual_deduction"`

	UsedAmount     float64 `json:"used_amount"`
	CurrentBalance float64 `json:"current_balance"`
	ChannelUsage   float64 `json:"channel_usage"`

	// 派生指标
	RechargeNet        float64 `json:"rechargeNet"`
	ActualRechargeNet  float64 `json:"actualRechargeNet"`
	TheoreticalBalance float64 `json:"theoreticalBalance"`
	LedgerDrift        float64 `json:"ledgerDrift"`
	TheoreticalSpread  float64 `json:"theoreticalSpread"`
	ActualProfit       float64 `json:"actualProfit"`
}

// UserBalance 用户余额汇总。
type UserBalance struct {
	CapturedAt        *time.Time `json:"capturedAt"`
	TotalRemaining    float64    `json:"totalRemaining"`
	TotalUsed         float64    `json:"totalUsed"`
	SelfRemaining     float64    `json:"selfRemaining"`
	SelfUsed          float64    `json:"selfUsed"`
	ExternalRemaining float64    `json:"externalRemaining"`
	ExternalUsed      float64    `json:"externalUsed"`
	SelfUserCount     int        `json:"selfUserCount"`
	UserCount         int        `json:"userCount"`
}

// Summary 对账总览汇总。
type Summary struct {
	TotalTopup                float64 `json:"total_topup"`
	TotalWithdrawal           float64 `json:"total_withdrawal"`
	TotalUsage                float64 `json:"total_usage"`
	TotalOperatingExpense     float64 `json:"total_operating_expense"`
	TotalVendorBalance        float64 `json:"total_vendor_balance"`
	TotalVendorUsed           float64 `json:"total_vendor_used"`
	TotalActualRechargeNet    float64 `json:"total_actual_recharge_net"`
	TotalActualProfit         float64 `json:"total_actual_profit"`
	TotalSelfTopup            float64 `json:"total_self_topup"`
	TotalExternalTopup        float64 `json:"total_external_topup"`
	TotalUserRemaining        float64 `json:"total_user_remaining"`
	TotalUserUsed             float64 `json:"total_user_used"`
	TotalSelfRemaining        float64 `json:"total_self_remaining"`
	TotalSelfUsed             float64 `json:"total_self_used"`
	TotalExternalRemaining    float64 `json:"total_external_remaining"`
	TotalExternalUsed         float64 `json:"total_external_used"`
	EstimatedRealizedProfit   float64 `json:"estimated_realized_profit"`
	EstimatedSettlementProfit float64 `json:"estimated_settlement_profit"`
}

// Dashboard 对账总览返回。
type Dashboard struct {
	Summary     Summary     `json:"summary"`
	Vendors     []VendorRow `json:"vendors"`
	Range       RangeOut    `json:"range"`
	UserBalance UserBalance `json:"userBalance"`
}

// RangeOut 返回给前端的区间表示。
type RangeOut struct {
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`
}

// Build 计算对账总览。连接与仓储由调用方注入。
func Build(db *gorm.DB, rng Range) (*Dashboard, error) {
	hasRange := rng.HasRange()

	summary := Summary{}
	// total_topup = SUM(daily_topups.amount)（已按现金口径）
	if err := sumInto(db, &summary.TotalTopup, "SELECT COALESCE(SUM(amount),0) FROM daily_topups"+dayFilter(hasRange, "day"), rng, false); err != nil {
		return nil, err
	}
	// 平台提现净额 = 提现 - 扣减（充值不计）
	if err := sumInto(db, &summary.TotalWithdrawal, thirdPartySQL(hasRange), rng, false); err != nil {
		return nil, err
	}
	// 站内归属消费
	if err := sumInto(db, &summary.TotalUsage, "SELECT COALESCE(SUM(amount),0) FROM channel_usage_stats"+dayFilter(hasRange, "day"), rng, false); err != nil {
		return nil, err
	}
	// 运营费用
	if err := sumInto(db, &summary.TotalOperatingExpense, "SELECT COALESCE(SUM(amount),0) FROM operating_expenses"+dayFilter(hasRange, "substr(occurred_at,1,10)"), rng, false); err != nil {
		return nil, err
	}

	vendors, err := buildVendors(db, rng, hasRange)
	if err != nil {
		return nil, err
	}

	var totalActualRechargeNet, totalVendorBalance, totalVendorUsed float64
	for i := range vendors {
		totalActualRechargeNet += vendors[i].ActualRechargeNet
		totalVendorBalance += vendors[i].CurrentBalance
		totalVendorUsed += vendors[i].UsedAmount
	}

	ub, err := LatestUserBalance(db)
	if err != nil {
		return nil, err
	}

	selfTopup, err := selfTopupTotal(db, rng, hasRange)
	if err != nil {
		return nil, err
	}
	externalTopup := round6(summary.TotalTopup - selfTopup)
	withdrawal := summary.TotalWithdrawal
	expense := summary.TotalOperatingExpense
	realized := round6(withdrawal - totalActualRechargeNet - expense)
	settlement := round6(withdrawal - ub.ExternalRemaining - totalActualRechargeNet - expense)

	summary.TotalVendorBalance = round6(totalVendorBalance)
	summary.TotalVendorUsed = round6(totalVendorUsed)
	summary.TotalActualRechargeNet = round6(totalActualRechargeNet)
	summary.TotalOperatingExpense = round6(expense)
	summary.TotalActualProfit = realized
	summary.TotalSelfTopup = round6(selfTopup)
	summary.TotalExternalTopup = externalTopup
	summary.TotalUserRemaining = ub.TotalRemaining
	summary.TotalUserUsed = ub.TotalUsed
	summary.TotalSelfRemaining = ub.SelfRemaining
	summary.TotalSelfUsed = ub.SelfUsed
	summary.TotalExternalRemaining = ub.ExternalRemaining
	summary.TotalExternalUsed = ub.ExternalUsed
	summary.EstimatedRealizedProfit = realized
	summary.EstimatedSettlementProfit = settlement

	out := &Dashboard{Summary: summary, Vendors: vendors, UserBalance: ub}
	if hasRange {
		out.Range.StartDate = &rng.StartDay
		out.Range.EndDate = &rng.EndDay
	}
	return out, nil
}

func buildVendors(db *gorm.DB, rng Range, hasRange bool) ([]VendorRow, error) {
	// 用原生 SQL 生成每个上游的聚合量，保持与 Python 版一致的 LEFT JOIN 语义。
	entryFilter := ""
	if hasRange {
		entryFilter = " AND substr(occurred_at,1,10) >= ? AND substr(occurred_at,1,10) <= ?"
	}
	usageFilter := ""
	if hasRange {
		usageFilter = " WHERE day >= ? AND day <= ?"
	}

	snapshotUsed := "0"
	snapshotBalance := "0"
	if !hasRange {
		snapshotUsed = "(SELECT vs.used_amount FROM vendor_snapshots vs WHERE vs.vendor_id = v.id ORDER BY vs.captured_at DESC, vs.id DESC LIMIT 1)"
		snapshotBalance = "(SELECT vs.current_balance FROM vendor_snapshots vs WHERE vs.vendor_id = v.id ORDER BY vs.captured_at DESC, vs.id DESC LIMIT 1)"
	}

	sql := `
		SELECT
		  v.id, v.name, v.alias, v.note,
		  COALESCE(topup.recharge,0) AS recharge,
		  COALESCE(topup.actual_recharge,0) AS actual_recharge,
		  COALESCE(refund.refund,0) AS refund,
		  COALESCE(refund.actual_refund,0) AS actual_refund,
		  COALESCE(adjust.deduction,0) AS deduction,
		  COALESCE(adjust.actual_deduction,0) AS actual_deduction,
		  COALESCE(` + snapshotUsed + `,0) AS used_amount,
		  COALESCE(` + snapshotBalance + `,0) AS current_balance,
		  COALESCE(usage.amount,0) AS channel_usage
		FROM vendors v
		LEFT JOIN (
		  SELECT vendor_id, SUM(amount) AS recharge, SUM(actual_amount) AS actual_recharge
		  FROM vendor_ledger_entries WHERE entry_type IN ('充值','补款')` + entryFilter + `
		  GROUP BY vendor_id
		) topup ON topup.vendor_id = v.id
		LEFT JOIN (
		  SELECT vendor_id, SUM(amount) AS refund, SUM(actual_amount) AS actual_refund
		  FROM vendor_ledger_entries WHERE entry_type = '退款'` + entryFilter + `
		  GROUP BY vendor_id
		) refund ON refund.vendor_id = v.id
		LEFT JOIN (
		  SELECT vendor_id, SUM(amount) AS deduction, SUM(actual_amount) AS actual_deduction
		  FROM vendor_ledger_entries WHERE entry_type = '扣减'` + entryFilter + `
		  GROUP BY vendor_id
		) adjust ON adjust.vendor_id = v.id
		LEFT JOIN (
		  SELECT vendor_id, SUM(amount) AS amount FROM channel_usage_stats` + usageFilter + `
		  GROUP BY vendor_id
		) usage ON usage.vendor_id = v.id
		ORDER BY v.id ASC`

	var params []any
	if hasRange {
		for i := 0; i < 3; i++ {
			params = append(params, rng.StartDay, rng.EndDay)
		}
		params = append(params, rng.StartDay, rng.EndDay)
	}

	type rawRow struct {
		ID              uint
		Name            string
		Alias           string
		Note            string
		Recharge        float64
		ActualRecharge  float64
		Refund          float64
		ActualRefund    float64
		Deduction       float64
		ActualDeduction float64
		UsedAmount      float64
		CurrentBalance  float64
		ChannelUsage    float64
	}
	var rows []rawRow
	if err := db.Raw(sql, params...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]VendorRow, 0, len(rows))
	for _, r := range rows {
		rechargeNet := round6(r.Recharge - r.Refund - r.Deduction)
		actualRechargeNet := round6(r.ActualRecharge - r.ActualRefund - r.ActualDeduction)
		theoreticalBalance := round6(rechargeNet - r.UsedAmount)
		ledgerDrift := round6(r.CurrentBalance - theoreticalBalance)
		theoreticalSpread := round6(r.ChannelUsage - r.UsedAmount)
		actualProfit := round6(r.ChannelUsage - actualRechargeNet)
		out = append(out, VendorRow{
			ID: r.ID, Name: r.Name, Alias: r.Alias, Note: r.Note,
			Recharge: r.Recharge, ActualRecharge: r.ActualRecharge,
			Refund: r.Refund, ActualRefund: r.ActualRefund,
			Deduction: r.Deduction, ActualDeduction: r.ActualDeduction,
			UsedAmount: r.UsedAmount, CurrentBalance: r.CurrentBalance, ChannelUsage: r.ChannelUsage,
			RechargeNet: rechargeNet, ActualRechargeNet: actualRechargeNet,
			TheoreticalBalance: theoreticalBalance, LedgerDrift: ledgerDrift,
			TheoreticalSpread: theoreticalSpread, ActualProfit: actualProfit,
		})
	}
	return out, nil
}

// LatestUserBalance 返回最新一批用户余额快照的汇总。
func LatestUserBalance(db *gorm.DB) (UserBalance, error) {
	var out UserBalance
	var latest storage.UserBalanceSnapshot
	err := db.Order("captured_at DESC, id DESC").First(&latest).Error
	if err == gorm.ErrRecordNotFound {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var rows []storage.UserBalanceSnapshot
	if err := db.Where("captured_at = ?", latest.CapturedAt).Find(&rows).Error; err != nil {
		return out, err
	}
	var selfRemaining, selfUsed float64
	selfCount := 0
	for _, row := range rows {
		out.TotalRemaining += row.RemainingAmount
		out.TotalUsed += row.UsedAmount
		if row.IsSelf {
			selfRemaining += row.RemainingAmount
			selfUsed += row.UsedAmount
			selfCount++
		}
	}
	out.TotalRemaining = round6(out.TotalRemaining)
	out.TotalUsed = round6(out.TotalUsed)
	out.SelfRemaining = round6(selfRemaining)
	out.SelfUsed = round6(selfUsed)
	out.ExternalRemaining = round6(out.TotalRemaining - out.SelfRemaining)
	out.ExternalUsed = round6(out.TotalUsed - out.SelfUsed)
	out.SelfUserCount = selfCount
	out.UserCount = len(rows)
	out.CapturedAt = &latest.CapturedAt
	return out, nil
}

func selfTopupTotal(db *gorm.DB, rng Range, hasRange bool) (float64, error) {
	set, err := storage.NewAccounting(db).GetSelfUsernames()
	if err != nil {
		return 0, err
	}
	if len(set) == 0 {
		return 0, nil
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sql := "SELECT COALESCE(SUM(amount),0) FROM topup_logs WHERE included_in_cash = 1 AND username IN ?"
	args := []any{names}
	if hasRange {
		sql += " AND substr(occurred_at,1,10) >= ? AND substr(occurred_at,1,10) <= ?"
		args = append(args, rng.StartDay, rng.EndDay)
	}
	var total float64
	if err := db.Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func thirdPartySQL(hasRange bool) string {
	filter := ""
	if hasRange {
		filter = "WHERE substr(occurred_at,1,10) >= ? AND substr(occurred_at,1,10) <= ?"
	}
	return `SELECT COALESCE(SUM(CASE WHEN entry_type='提现' THEN amount WHEN entry_type='扣减' THEN -amount ELSE 0 END),0)
		FROM third_party_entries ` + filter
}

func dayFilter(hasRange bool, col string) string {
	if !hasRange {
		return ""
	}
	return " WHERE " + col + " >= ? AND " + col + " <= ?"
}

// sumInto 执行一条标量 SQL 到目标指针。仅当区间生效时才带上区间参数。
func sumInto(db *gorm.DB, dest *float64, sql string, rng Range, _ bool) error {
	args := []any{}
	if rng.HasRange() {
		args = append(args, rng.StartDay, rng.EndDay)
	}
	return db.Raw(sql, args...).Scan(dest).Error
}

func round6(v float64) float64 { return storage.Round6(v) }
