package storage

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------------------------------------------------------------------------
// Reconciliation channels（对账用 NewAPI 渠道缓存）
// ---------------------------------------------------------------------------

// SaveReconciliationChannels 批量 upsert 渠道缓存（channel_id 为自然键）。
// channel_id=0 的聚合哨兵会被忽略。
func (a *Accounting) SaveReconciliationChannels(list []ReconciliationChannel) error {
	if len(list) == 0 {
		return nil
	}
	now := time.Now().In(Shanghai)
	filtered := make([]ReconciliationChannel, 0, len(list))
	for _, c := range list {
		if c.ChannelID == 0 {
			continue
		}
		if c.FirstSeenAt.IsZero() {
			c.FirstSeenAt = now
		}
		c.LastSyncedAt = now
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		return nil
	}
	return a.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"channel_name":   gorm.Expr("excluded.channel_name"),
			"group_name":     gorm.Expr("excluded.group_name"),
			"is_active":      gorm.Expr("excluded.is_active"),
			"last_synced_at": gorm.Expr("excluded.last_synced_at"),
		}),
	}).Create(&filtered).Error
}

// ListReconciliationChannels 返回渠道缓存（排除聚合哨兵，按 id 升序）。
func (a *Accounting) ListReconciliationChannels() ([]ReconciliationChannel, error) {
	var out []ReconciliationChannel
	err := a.db.Where("channel_id <> 0").Order("channel_id ASC").Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Channel bindings（渠道 → 上游 归属）
// ---------------------------------------------------------------------------

// ActiveBindingFor 返回某渠道当前生效（effective_to IS NULL）的绑定，取最新一条。
func (a *Accounting) ActiveBindingFor(channelID uint) (*ChannelBinding, error) {
	var b ChannelBinding
	err := a.db.Where("channel_id = ? AND effective_to IS NULL", channelID).
		Order("id DESC").First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// CurrentBinding 返回某渠道的当前绑定（历史日期也按最新绑定归属，与 Python 版一致）。
func (a *Accounting) CurrentBinding(channelID uint) (*ChannelBinding, error) {
	var b ChannelBinding
	err := a.db.Where("channel_id = ?", channelID).Order("id DESC").First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ChannelBindingRow 绑定列表行（带上游名）。
type ChannelBindingRow struct {
	ChannelBinding
	VendorName string `json:"vendor_name"`
}

// ListChannelBindings 返回所有当前生效的绑定（effective_to IS NULL），带上游名。
func (a *Accounting) ListChannelBindings() ([]ChannelBindingRow, error) {
	var out []ChannelBindingRow
	err := a.db.Table("channel_bindings AS b").
		Select("b.*, v.name AS vendor_name").
		Joins("JOIN vendors v ON v.id = b.vendor_id").
		Where("b.effective_to IS NULL").
		Order("b.channel_id ASC, b.id DESC").
		Scan(&out).Error
	return out, err
}

// BindChannelInput 绑定入参。
type BindChannelInput struct {
	ChannelID     uint
	ChannelName   string
	GroupName     string
	VendorID      uint
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Note          string
}

// BindChannel 把渠道归属到上游：关闭旧的有效绑定并写历史，新增一条有效绑定。
func (a *Accounting) BindChannel(in BindChannelInput) (*ChannelBinding, error) {
	now := time.Now().In(Shanghai)
	from := in.EffectiveFrom
	if from.IsZero() {
		from = now
	}
	var created ChannelBinding
	err := a.db.Transaction(func(tx *gorm.DB) error {
		// 关闭现存有效绑定并记录 close 历史。
		var olds []ChannelBinding
		if err := tx.Where("channel_id = ? AND effective_to IS NULL", in.ChannelID).Find(&olds).Error; err != nil {
			return err
		}
		for _, old := range olds {
			closedAt := from
			if err := tx.Model(&ChannelBinding{}).Where("id = ?", old.ID).
				Update("effective_to", closedAt).Error; err != nil {
				return err
			}
			h := ChannelBindingHistory{
				ChannelID: old.ChannelID, ChannelName: old.ChannelName, GroupName: old.GroupName,
				VendorID: old.VendorID, EffectiveFrom: old.EffectiveFrom, EffectiveTo: &closedAt,
				Note: old.Note, ChangeType: BindingChangeClose, ChangedAt: now,
			}
			if err := tx.Create(&h).Error; err != nil {
				return err
			}
		}
		created = ChannelBinding{
			ChannelID: in.ChannelID, ChannelName: in.ChannelName, GroupName: in.GroupName,
			VendorID: in.VendorID, EffectiveFrom: from, EffectiveTo: in.EffectiveTo,
			Note: in.Note,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		h := ChannelBindingHistory{
			ChannelID: created.ChannelID, ChannelName: created.ChannelName, GroupName: created.GroupName,
			VendorID: created.VendorID, EffectiveFrom: created.EffectiveFrom, EffectiveTo: created.EffectiveTo,
			Note: created.Note, ChangeType: BindingChangeBind, ChangedAt: now,
		}
		return tx.Create(&h).Error
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// ChannelBindingHistoryRow 绑定历史行（带上游名）。
type ChannelBindingHistoryRow struct {
	ChannelBindingHistory
	VendorName string `json:"vendor_name"`
}

// ListChannelBindingHistory 返回绑定历史（按变更时间倒序）。
func (a *Accounting) ListChannelBindingHistory() ([]ChannelBindingHistoryRow, error) {
	var out []ChannelBindingHistoryRow
	err := a.db.Table("channel_binding_history AS h").
		Select("h.*, v.name AS vendor_name").
		Joins("JOIN vendors v ON v.id = h.vendor_id").
		Order("h.changed_at DESC, h.id DESC").
		Scan(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Upstream hub channel bindings（本系统监控渠道 id → vendor）
// ---------------------------------------------------------------------------

// UpstreamBindingRow 上游渠道绑定行（带上游名）。
type UpstreamBindingRow struct {
	UpstreamChannelBinding
	VendorName string `json:"vendor_name"`
}

// ListUpstreamChannelBindings 返回上游渠道绑定。
func (a *Accounting) ListUpstreamChannelBindings() ([]UpstreamBindingRow, error) {
	var out []UpstreamBindingRow
	err := a.db.Table("upstream_channel_bindings AS b").
		Select("b.*, v.name AS vendor_name").
		Joins("JOIN vendors v ON v.id = b.vendor_id").
		Order("b.vendor_id ASC, b.upstream_channel_id ASC, b.id ASC").
		Scan(&out).Error
	return out, err
}

// BindUpstreamChannel 绑定上游渠道到 vendor（按 (vendor, channel) upsert）。
func (a *Accounting) BindUpstreamChannel(vendorID uint, upstreamChannelID, name, note string) error {
	if upstreamChannelID == "" {
		return errors.New("upstream-hub 渠道 ID 不能为空")
	}
	now := time.Now().In(Shanghai)
	b := UpstreamChannelBinding{
		VendorID: vendorID, UpstreamChannelID: upstreamChannelID,
		UpstreamChannelName: name, Note: note,
	}
	return a.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "vendor_id"}, {Name: "upstream_channel_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"upstream_channel_name": gorm.Expr("excluded.upstream_channel_name"),
			"note":                  gorm.Expr("excluded.note"),
			"updated_at":            now,
		}),
	}).Create(&b).Error
}

// DeleteUpstreamChannelBinding 删除一条上游渠道绑定。
func (a *Accounting) DeleteUpstreamChannelBinding(id uint) error {
	res := a.db.Delete(&UpstreamChannelBinding{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// VendorIDsForUpstreamChannel 返回某上游渠道 id 映射到的所有 vendor。
func (a *Accounting) VendorIDsForUpstreamChannel(upstreamChannelID string) ([]uint, error) {
	var ids []uint
	err := a.db.Model(&UpstreamChannelBinding{}).
		Where("upstream_channel_id = ?", upstreamChannelID).
		Pluck("vendor_id", &ids).Error
	return ids, err
}
