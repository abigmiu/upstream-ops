package storage

import "time"

// 本文件是「站长对账」(reconciliation) 子系统的领域模型。
//
// 设计来源：Python 版 ai_proxy_money（app/db.py 的 SCHEMA_SQL）。
// 记账主体是「上游 vendor」，而不是渠道 channel。
// 渠道只通过 channel_bindings 归属到某个 vendor，用来把站内消费聚合到上游名下。
//
// 时间口径：所有时间按 Asia/Shanghai 存储（与 Python 版一致），便于按天聚合。
// 金额口径：一律保留 6 位小数（写入前 round6）。
//
// 注意：其中 4 张明细表（channel_usage_stats / user_balance_snapshots /
// vendor_snapshots / sync_task_logs）已折叠成「每天一行」（day + UNIQUE），
// 这是为了避免按窗口时间戳无限追加导致 SQLite 膨胀。Go 端沿用折叠后的结构。

// DeletedVendorName 是保留的上游名，用于承接「已删除渠道 / 历史渠道」的归属。
const DeletedVendorName = "已删除上游"

// LedgerEntryType 上游流水类型。
type LedgerEntryType string

const (
	LedgerEntryRecharge  LedgerEntryType = "充值"
	LedgerEntryTopup     LedgerEntryType = "补款"
	LedgerEntryRefund    LedgerEntryType = "退款"
	LedgerEntryDeduction LedgerEntryType = "扣减"
)

// LedgerIncreaseTypes 计正的上游流水类型。
var LedgerIncreaseTypes = map[LedgerEntryType]bool{
	LedgerEntryRecharge: true,
	LedgerEntryTopup:    true,
}

// LedgerDecreaseTypes 计负的上游流水类型。
var LedgerDecreaseTypes = map[LedgerEntryType]bool{
	LedgerEntryRefund:    true,
	LedgerEntryDeduction: true,
}

// IsValidLedgerEntryType 判断是否为合法流水类型。
func IsValidLedgerEntryType(t LedgerEntryType) bool {
	return LedgerIncreaseTypes[t] || LedgerDecreaseTypes[t]
}

// ThirdPartyEntryType 第三方平台资金流水类型。
type ThirdPartyEntryType string

const (
	ThirdPartyWithdrawal ThirdPartyEntryType = "提现"
	ThirdPartyDeduction  ThirdPartyEntryType = "扣减"
	ThirdPartyTopup      ThirdPartyEntryType = "充值"
)

// BindingChangeType 渠道绑定历史变更类型。
type BindingChangeType string

const (
	BindingChangeImport       BindingChangeType = "import"
	BindingChangeUpdate       BindingChangeType = "update"
	BindingChangeClose        BindingChangeType = "close"
	BindingChangeBind         BindingChangeType = "bind"
	BindingChangeBindDeleted  BindingChangeType = "bind_deleted"
	BindingChangeProbeDeleted BindingChangeType = "probe_deleted"
)

// Vendor 上游主体。每条记录代表一个上游，而不是一个渠道。
type Vendor struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:256;not null;uniqueIndex" json:"name"`
	Alias     string    `gorm:"size:256;not null;default:''" json:"alias"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
	Note      string    `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Vendor) TableName() string { return "vendors" }

// ReconciliationChannel 对账用的 NewAPI 渠道缓存（来自 /api/channel）。
// channel_id 是自然主键（来自 NewAPI）；channel_id=0 是聚合哨兵，始终排除在列表外。
// 注意：与 upstream-ops 已有的 Channel（上游账号）不同，这张表记录的是「你站的渠道」。
type ReconciliationChannel struct {
	ChannelID    uint      `gorm:"primaryKey" json:"channel_id"`
	ChannelName  string    `gorm:"size:256;not null" json:"channel_name"`
	GroupName    string    `gorm:"size:256;not null;default:''" json:"group_name"`
	IsActive     bool      `gorm:"not null;default:true" json:"is_active"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSyncedAt time.Time `json:"last_synced_at"`
}

func (ReconciliationChannel) TableName() string { return "reconciliation_channels" }

// ChannelBinding 渠道→上游 的归属映射。同一渠道同一时刻只允许一条 active 绑定
// （effective_to IS NULL）。重新绑定会关闭旧记录并写入历史。
type ChannelBinding struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	ChannelID     uint       `gorm:"not null;index" json:"channel_id"`
	ChannelName   string     `gorm:"size:256;not null" json:"channel_name"`
	GroupName     string     `gorm:"size:256;not null;default:''" json:"group_name"`
	VendorID      uint       `gorm:"not null;index" json:"vendor_id"`
	EffectiveFrom time.Time  `gorm:"not null" json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	Note          string     `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (ChannelBinding) TableName() string { return "channel_bindings" }

// ChannelBindingHistory 渠道绑定审计历史。
type ChannelBindingHistory struct {
	ID            uint              `gorm:"primaryKey" json:"id"`
	ChannelID     uint              `gorm:"not null;index" json:"channel_id"`
	ChannelName   string            `gorm:"size:256;not null" json:"channel_name"`
	GroupName     string            `gorm:"size:256;not null;default:''" json:"group_name"`
	VendorID      uint              `gorm:"not null;index" json:"vendor_id"`
	EffectiveFrom time.Time         `gorm:"not null" json:"effective_from"`
	EffectiveTo   *time.Time        `json:"effective_to,omitempty"`
	Note          string            `gorm:"type:text;not null;default:''" json:"note"`
	ChangeType    BindingChangeType `gorm:"size:32;not null" json:"change_type"`
	ChangedAt     time.Time         `gorm:"not null;index" json:"changed_at"`
}

func (ChannelBindingHistory) TableName() string { return "channel_binding_history" }

// VendorLedgerEntry 上游流水（append-only 事件流）。
// amount = 到账额度；actual_amount = 实付现金。balance_before/after 为派生值，
// 每次写入后由 rebuild 逻辑重算，绝不原地编辑余额。
type VendorLedgerEntry struct {
	ID             uint            `gorm:"primaryKey" json:"id"`
	VendorID       uint            `gorm:"not null;index" json:"vendor_id"`
	EntryType      LedgerEntryType `gorm:"size:16;not null" json:"entry_type"`
	Amount         float64         `gorm:"not null" json:"amount"`
	ActualAmount   float64         `gorm:"not null;default:0" json:"actual_amount"`
	BalanceBefore  float64         `gorm:"not null" json:"balance_before"`
	BalanceAfter   float64         `gorm:"not null" json:"balance_after"`
	OccurredAt     time.Time       `gorm:"not null;index" json:"occurred_at"`
	Note           string          `gorm:"type:text;not null;default:''" json:"note"`
	SourceType     string          `gorm:"size:32;not null;default:'manual'" json:"source_type"`
	CorrectionOfID *uint           `json:"correction_of_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (VendorLedgerEntry) TableName() string { return "vendor_ledger_entries" }

// ThirdPartyEntry 第三方平台资金流水（提现/扣减/充值）。
type ThirdPartyEntry struct {
	ID           uint                `gorm:"primaryKey" json:"id"`
	PlatformName string              `gorm:"size:128;not null" json:"platform_name"`
	EntryType    ThirdPartyEntryType `gorm:"size:16;not null" json:"entry_type"`
	Amount       float64             `gorm:"not null" json:"amount"`
	OccurredAt   time.Time           `gorm:"not null;index" json:"occurred_at"`
	Note         string              `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt    time.Time           `json:"created_at"`
}

func (ThirdPartyEntry) TableName() string { return "third_party_entries" }

// OperatingExpense 运营费用。
type OperatingExpense struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Category   string    `gorm:"size:128;not null" json:"category"`
	Amount     float64   `gorm:"not null" json:"amount"`
	OccurredAt time.Time `gorm:"not null;index" json:"occurred_at"`
	Note       string    `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

func (OperatingExpense) TableName() string { return "operating_expenses" }

// VendorSnapshot 上游快照（点值语义，每天一行）。
// used_amount = 截止当时累计已耗；current_balance = 当前余额。
type VendorSnapshot struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	VendorID       uint      `gorm:"not null;uniqueIndex:idx_vendor_snap_vendor_day" json:"vendor_id"`
	UsedAmount     float64   `gorm:"not null" json:"used_amount"`
	CurrentBalance float64   `gorm:"not null" json:"current_balance"`
	Day            string    `gorm:"size:10;not null;uniqueIndex:idx_vendor_snap_vendor_day" json:"day"`
	CapturedAt     time.Time `gorm:"not null;index" json:"captured_at"`
	Note           string    `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

func (VendorSnapshot) TableName() string { return "vendor_snapshots" }

// UpstreamChannelBinding 把 upstream-hub（本系统的监控渠道 id，字符串）映射到 vendor。
type UpstreamChannelBinding struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	VendorID            uint      `gorm:"not null;uniqueIndex:idx_upstream_bind_vendor_channel" json:"vendor_id"`
	UpstreamChannelID   string    `gorm:"size:64;not null;uniqueIndex:idx_upstream_bind_vendor_channel" json:"upstream_channel_id"`
	UpstreamChannelName string    `gorm:"size:256;not null;default:''" json:"upstream_channel_name"`
	Note                string    `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (UpstreamChannelBinding) TableName() string { return "upstream_channel_bindings" }

// TopupLog 站内充值日志。id 来自 NewAPI 日志 id（自然键）。
// included_in_cash = 1 的才计入现金收入；included_in_cash_manual 标记被人工改过。
type TopupLog struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	Username             string    `gorm:"size:256;not null;default:''" json:"username"`
	LogType              string    `gorm:"size:64;not null" json:"log_type"`
	Amount               float64   `gorm:"not null" json:"amount"`
	OccurredAt           time.Time `gorm:"not null;index" json:"occurred_at"`
	IncludedInCash       bool      `gorm:"not null;default:true" json:"included_in_cash"`
	IncludedInCashManual bool      `gorm:"not null;default:false" json:"included_in_cash_manual"`
	Note                 string    `gorm:"type:text;not null;default:''" json:"note"`
	Content              string    `gorm:"type:text;not null;default:''" json:"content"`
	CreatedAt            time.Time `json:"created_at"`
}

// TopupLog 的表名。为了避免与既有表冲突，使用 topup_logs。
func (TopupLog) TableName() string { return "topup_logs" }

// DailyTopup 按天充值汇总（由 topup_logs 派生，DELETE-all 后重建）。
type DailyTopup struct {
	Day       string    `gorm:"primaryKey;size:10" json:"day"`
	Amount    float64   `gorm:"not null" json:"amount"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (DailyTopup) TableName() string { return "daily_topups" }

// ChannelUsageStat 站内归属消费（每天一行，UNIQUE(channel_id, day)）。
type ChannelUsageStat struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ChannelID   uint      `gorm:"not null;uniqueIndex:idx_channel_usage_channel_day" json:"channel_id"`
	ChannelName string    `gorm:"size:256;not null" json:"channel_name"`
	VendorID    uint      `gorm:"not null;index:idx_channel_usage_day" json:"vendor_id"`
	Day         string    `gorm:"size:10;not null;uniqueIndex:idx_channel_usage_channel_day;index:idx_channel_usage_day" json:"day"`
	Amount      float64   `gorm:"not null" json:"amount"`
	StartAt     time.Time `gorm:"not null" json:"start_at"`
	EndAt       time.Time `gorm:"not null" json:"end_at"`
	CreatedAt   time.Time `json:"created_at"`
}

func (ChannelUsageStat) TableName() string { return "channel_usage_stats" }

// UserBalanceSnapshot 用户余额快照（点值语义，UNIQUE(username, day)）。
// is_self 标记运营者自己的账号。
type UserBalanceSnapshot struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Username        string    `gorm:"size:256;not null;uniqueIndex:idx_user_balance_user_day" json:"username"`
	RemainingAmount float64   `gorm:"not null" json:"remaining_amount"`
	UsedAmount      float64   `gorm:"not null" json:"used_amount"`
	RequestCount    int       `gorm:"not null;default:0" json:"request_count"`
	IsSelf          bool      `gorm:"not null;default:false" json:"is_self"`
	Day             string    `gorm:"size:10;not null;uniqueIndex:idx_user_balance_user_day" json:"day"`
	CapturedAt      time.Time `gorm:"not null;index" json:"captured_at"`
	CreatedAt       time.Time `json:"created_at"`
}

func (UserBalanceSnapshot) TableName() string { return "user_balance_snapshots" }

// SyncTaskLog 同步执行日志（每天合并成一行，UNIQUE(day)）。
type SyncTaskLog struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Day              string     `gorm:"size:10;not null;uniqueIndex" json:"day"`
	JobName          string     `gorm:"size:64;not null" json:"job_name"`
	Category         string     `gorm:"size:32;not null;default:''" json:"category"`
	Source           string     `gorm:"size:32;not null" json:"source"`
	Status           string     `gorm:"size:16;not null" json:"status"`
	WindowStart      *time.Time `json:"window_start,omitempty"`
	WindowEnd        *time.Time `json:"window_end,omitempty"`
	TopupCount       int        `gorm:"not null;default:0" json:"topup_count"`
	UsageCount       int        `gorm:"not null;default:0" json:"usage_count"`
	UserBalanceCount int        `gorm:"not null;default:0" json:"user_balance_count"`
	Message          string     `gorm:"type:text;not null;default:''" json:"message"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      time.Time  `json:"completed_at"`
}

func (SyncTaskLog) TableName() string { return "sync_task_logs" }

// AppSetting 通用键值配置（对账子系统使用，独立于 upstream-ops 的 YAML 配置）。
type AppSetting struct {
	Key       string    `gorm:"primaryKey;size:128" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AppSetting) TableName() string { return "app_settings" }
