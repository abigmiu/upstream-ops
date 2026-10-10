package storage

import (
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Shanghai 对账子系统统一使用的时区。
var Shanghai = time.FixedZone("CST", 8*3600)

// Round6 金额统一保留 6 位小数，与 Python 版 round(x, 6) 对齐。
func Round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// DayString 返回 t 在上海时区下的 YYYY-MM-DD。
func DayString(t time.Time) string {
	return t.In(Shanghai).Format("2006-01-02")
}

// Accounting 对账子系统的仓储集合。
type Accounting struct{ db *gorm.DB }

// NewAccounting 构造对账仓储。
func NewAccounting(db *gorm.DB) *Accounting { return &Accounting{db: db} }

// DB 暴露底层连接（供 dashboard 聚合等复杂查询使用）。
func (a *Accounting) DB() *gorm.DB { return a.db }

// ---------------------------------------------------------------------------
// Vendors
// ---------------------------------------------------------------------------

// ErrVendorExists 同名上游已存在。
var ErrVendorExists = errors.New("上游名称已存在")

// ListVendors 返回全部上游（含禁用），按 id 升序。
func (a *Accounting) ListVendors() ([]Vendor, error) {
	var out []Vendor
	err := a.db.Order("id ASC").Find(&out).Error
	return out, err
}

// FindVendorByID 按 id 查询上游。
func (a *Accounting) FindVendorByID(id uint) (*Vendor, error) {
	var v Vendor
	if err := a.db.First(&v, id).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// FindVendorByName 按名称查询上游。
func (a *Accounting) FindVendorByName(name string) (*Vendor, error) {
	var v Vendor
	if err := a.db.Where("name = ?", name).First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateVendor 新建上游（名称唯一）。重复名称返回 ErrVendorExists。
func (a *Accounting) CreateVendor(v *Vendor) error {
	if v.Name == "" {
		return errors.New("上游名称不能为空")
	}
	var count int64
	if err := a.db.Model(&Vendor{}).Where("name = ?", v.Name).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrVendorExists
	}
	return a.db.Create(v).Error
}

// SetVendorEnabled 启用 / 禁用上游。
func (a *Accounting) SetVendorEnabled(id uint, enabled bool) error {
	res := a.db.Model(&Vendor{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateVendor 更新上游可编辑字段（别名 / 备注 / 启用）。
func (a *Accounting) UpdateVendor(v *Vendor) error { return a.db.Save(v).Error }

// EnsureDeletedVendor 确保保留上游「已删除上游」存在并返回它。
func (a *Accounting) EnsureDeletedVendor() (*Vendor, error) {
	v, err := a.FindVendorByName(DeletedVendorName)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	v = &Vendor{Name: DeletedVendorName, Enabled: true}
	if err := a.db.Create(v).Error; err != nil {
		// 并发下可能已被其它事务创建，重查一次。
		return a.FindVendorByName(DeletedVendorName)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Vendor ledger entries（上游流水 + 余额重算）
// ---------------------------------------------------------------------------

func ledgerBalanceDelta(t LedgerEntryType, amount float64) (float64, error) {
	if !IsValidLedgerEntryType(t) {
		return 0, errors.New("上游流水类型不正确")
	}
	if LedgerIncreaseTypes[t] {
		return amount, nil
	}
	return -amount, nil
}

func (a *Accounting) getVendorBalanceBeforeEntry(tx *gorm.DB, vendorID uint, occurredAt time.Time, entryID uint) (float64, error) {
	var row VendorLedgerEntry
	err := tx.
		Where("vendor_id = ? AND (occurred_at < ? OR (occurred_at = ? AND id < ?))", vendorID, occurredAt, occurredAt, entryID).
		Order("occurred_at DESC, id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return row.BalanceAfter, nil
}

// rebuildVendorLedgerBalances 从指定流水起，重算该上游后续所有流水的 balance_before/after。
func (a *Accounting) rebuildVendorLedgerBalances(tx *gorm.DB, vendorID uint, occurredAt time.Time, entryID uint) error {
	running, err := a.getVendorBalanceBeforeEntry(tx, vendorID, occurredAt, entryID)
	if err != nil {
		return err
	}
	var rows []VendorLedgerEntry
	if err := tx.
		Where("vendor_id = ? AND (occurred_at > ? OR (occurred_at = ? AND id >= ?))", vendorID, occurredAt, occurredAt, entryID).
		Order("occurred_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		delta, err := ledgerBalanceDelta(row.EntryType, row.Amount)
		if err != nil {
			return err
		}
		before := running
		after := Round6(before + delta)
		if err := tx.Model(&VendorLedgerEntry{}).Where("id = ?", row.ID).
			Updates(map[string]any{"balance_before": before, "balance_after": after}).Error; err != nil {
			return err
		}
		running = after
	}
	return nil
}

// RebuildVendorLedgerBalancesFull 从 0 起全量重算某上游的所有流水余额（合并上游后使用）。
func (a *Accounting) RebuildVendorLedgerBalancesFull(tx *gorm.DB, vendorID uint) error {
	running := 0.0
	var rows []VendorLedgerEntry
	if err := tx.Where("vendor_id = ?", vendorID).Order("occurred_at ASC, id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		delta, err := ledgerBalanceDelta(row.EntryType, row.Amount)
		if err != nil {
			return err
		}
		before := running
		after := Round6(before + delta)
		if err := tx.Model(&VendorLedgerEntry{}).Where("id = ?", row.ID).
			Updates(map[string]any{"balance_before": before, "balance_after": after}).Error; err != nil {
			return err
		}
		running = after
	}
	return nil
}

// LedgerEntryInput 新建上游流水的入参。
type LedgerEntryInput struct {
	VendorID       uint
	EntryType      LedgerEntryType
	Amount         float64 // 到账额度，内部取绝对值
	ActualAmount   float64 // 实付现金，内部取绝对值
	OccurredAt     time.Time
	Note           string
	SourceType     string
	CorrectionOfID *uint
}

// CreateVendorLedgerEntry 新建上游流水并重算余额。
func (a *Accounting) CreateVendorLedgerEntry(in LedgerEntryInput) (*VendorLedgerEntry, error) {
	if !IsValidLedgerEntryType(in.EntryType) {
		return nil, errors.New("上游流水类型不正确")
	}
	amount := Round6(math.Abs(in.Amount))
	if amount <= 0 {
		return nil, errors.New("到账额度必须大于 0")
	}
	actual := Round6(math.Abs(in.ActualAmount))
	if actual <= 0 {
		return nil, errors.New("实付金额必须大于 0")
	}
	source := in.SourceType
	if source == "" {
		source = "manual"
	}
	entry := &VendorLedgerEntry{
		VendorID:       in.VendorID,
		EntryType:      in.EntryType,
		Amount:         amount,
		ActualAmount:   actual,
		OccurredAt:     in.OccurredAt.In(Shanghai),
		Note:           in.Note,
		SourceType:     source,
		CorrectionOfID: in.CorrectionOfID,
	}
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		return a.rebuildVendorLedgerBalances(tx, entry.VendorID, entry.OccurredAt, entry.ID)
	})
	if err != nil {
		return nil, err
	}
	var out VendorLedgerEntry
	if err := a.db.First(&out, entry.ID).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// VendorLedgerRow 流水列表行（带上游名）。
type VendorLedgerRow struct {
	VendorLedgerEntry
	VendorName string `json:"vendor_name"`
}

// ListVendorLedgerEntries 返回全部上游流水（按发生时间倒序）。
func (a *Accounting) ListVendorLedgerEntries() ([]VendorLedgerRow, error) {
	var out []VendorLedgerRow
	err := a.db.Table("vendor_ledger_entries AS vle").
		Select("vle.*, v.name AS vendor_name").
		Joins("JOIN vendors v ON v.id = vle.vendor_id").
		Order("vle.occurred_at DESC, vle.id DESC").
		Scan(&out).Error
	return out, err
}

// GetLastVendorBalances 返回每个上游的当前流水余额（balance_after 的末值）。
// 用于上游快照同步时把「上游重置累计」的场景换算成 used = 累充 - 当前余额。
func (a *Accounting) GetLastVendorBalances() (map[uint]float64, error) {
	var rows []struct {
		VendorID     uint
		BalanceAfter float64
	}
	// 取每个 vendor 最后一条流水的 balance_after。
	sub := a.db.Model(&VendorLedgerEntry{}).
		Select("vendor_id, MAX(id) AS max_id").
		Group("vendor_id")
	err := a.db.Table("vendor_ledger_entries AS vle").
		Select("vle.vendor_id AS vendor_id, vle.balance_after AS balance_after").
		Joins("JOIN (?) m ON m.vendor_id = vle.vendor_id AND m.max_id = vle.id", sub).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]float64, len(rows))
	for _, r := range rows {
		out[r.VendorID] = r.BalanceAfter
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Vendor snapshots（上游快照，每天一行）
// ---------------------------------------------------------------------------

// VendorSnapshotInput 新建 / 覆盖快照的入参。
type VendorSnapshotInput struct {
	VendorID       uint
	UsedAmount     float64
	CurrentBalance float64
	CapturedAt     time.Time
	Note           string
}

// UpsertVendorSnapshot 按 (vendor, day) 覆盖写入快照；capturedAt 更早则不覆盖。
func (a *Accounting) UpsertVendorSnapshot(in VendorSnapshotInput) (*VendorSnapshot, error) {
	captured := in.CapturedAt.In(Shanghai)
	day := DayString(captured)
	snap := &VendorSnapshot{
		VendorID:       in.VendorID,
		UsedAmount:     Round6(in.UsedAmount),
		CurrentBalance: Round6(in.CurrentBalance),
		Day:            day,
		CapturedAt:     captured,
		Note:           in.Note,
	}
	// 仅当新快照采集时间不早于已有时才覆盖。
	err := a.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "vendor_id"}, {Name: "day"}},
		DoUpdates: clause.Assignments(map[string]any{"note": gorm.Expr("excluded.note")}),
		Where:     clause.Where{Exprs: []clause.Expression{gorm.Expr("excluded.captured_at >= vendor_snapshots.captured_at")}},
	}).Create(snap).Error
	if err != nil {
		return nil, err
	}
	// OnConflict 的 DoUpdates 只更新了 note，这里显式覆盖数值字段。
	err = a.db.Model(&VendorSnapshot{}).
		Where("vendor_id = ? AND day = ? AND captured_at <= ?", in.VendorID, day, captured).
		Updates(map[string]any{
			"used_amount":     Round6(in.UsedAmount),
			"current_balance": Round6(in.CurrentBalance),
			"captured_at":     captured,
			"note":            in.Note,
		}).Error
	if err != nil {
		return nil, err
	}
	var out VendorSnapshot
	if err := a.db.Where("vendor_id = ? AND day = ?", in.VendorID, day).First(&out).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// VendorSnapshotRow 快照列表行（带上游名与上一条余额）。
type VendorSnapshotRow struct {
	VendorSnapshot
	VendorName       string     `json:"vendor_name"`
	PreviousBalance  *float64   `json:"previous_balance,omitempty"`
	PreviousCaptured *time.Time `json:"previous_captured_at,omitempty"`
}

// ListVendorSnapshots 返回全部上游快照（按天倒序），并带出同上游上一条的余额。
func (a *Accounting) ListVendorSnapshots() ([]VendorSnapshotRow, error) {
	var out []VendorSnapshotRow
	err := a.db.Table("vendor_snapshots AS s").
		Select(`s.*, v.name AS vendor_name,
			(SELECT s2.current_balance FROM vendor_snapshots s2
			   WHERE s2.vendor_id = s.vendor_id
			     AND (s2.captured_at < s.captured_at OR (s2.captured_at = s.captured_at AND s2.id < s.id))
			   ORDER BY s2.captured_at DESC, s2.id DESC LIMIT 1) AS previous_balance,
			(SELECT s3.captured_at FROM vendor_snapshots s3
			   WHERE s3.vendor_id = s.vendor_id
			     AND (s3.captured_at < s.captured_at OR (s3.captured_at = s.captured_at AND s3.id < s.id))
			   ORDER BY s3.captured_at DESC, s3.id DESC LIMIT 1) AS previous_captured_at`).
		Joins("JOIN vendors v ON v.id = s.vendor_id").
		Order("s.day DESC, s.id DESC").
		Scan(&out).Error
	return out, err
}

// LatestVendorSnapshot 返回每个上游的最新一条快照（按 captured_at）。
func (a *Accounting) LatestVendorSnapshot(vendorID uint) (*VendorSnapshot, error) {
	var s VendorSnapshot
	err := a.db.Where("vendor_id = ?", vendorID).Order("captured_at DESC, id DESC").First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ---------------------------------------------------------------------------
// Third party / operating expenses
// ---------------------------------------------------------------------------

// CreateThirdPartyEntry 新建第三方资金流水。
func (a *Accounting) CreateThirdPartyEntry(e *ThirdPartyEntry) error {
	e.Amount = Round6(math.Abs(e.Amount))
	if e.Amount <= 0 {
		return errors.New("金额必须大于 0")
	}
	e.OccurredAt = e.OccurredAt.In(Shanghai)
	return a.db.Create(e).Error
}

// ListThirdPartyEntries 返回第三方资金流水（按时间倒序）。
func (a *Accounting) ListThirdPartyEntries() ([]ThirdPartyEntry, error) {
	var out []ThirdPartyEntry
	err := a.db.Order("occurred_at DESC, id DESC").Find(&out).Error
	return out, err
}

// CreateOperatingExpense 新建运营费用。
func (a *Accounting) CreateOperatingExpense(e *OperatingExpense) error {
	e.Amount = Round6(math.Abs(e.Amount))
	if e.Amount <= 0 {
		return errors.New("金额必须大于 0")
	}
	e.OccurredAt = e.OccurredAt.In(Shanghai)
	return a.db.Create(e).Error
}

// ListOperatingExpenses 返回运营费用（按时间倒序）。
func (a *Accounting) ListOperatingExpenses() ([]OperatingExpense, error) {
	var out []OperatingExpense
	err := a.db.Order("occurred_at DESC, id DESC").Find(&out).Error
	return out, err
}

var _ = fmt.Sprintf
