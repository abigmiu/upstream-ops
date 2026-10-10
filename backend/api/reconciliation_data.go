package api

import (
	"net/http"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

// 本文件承载对账子系统的数据录入 / 查询接口（流水、快照、第三方、费用、
// 站内充值、消费、用户余额、同步日志、自用用户标记）。

// ---------------------------------------------------------------------------
// 上游流水
// ---------------------------------------------------------------------------

func listVendorEntries(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListVendorLedgerEntries()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type vendorEntryInput struct {
	VendorID       uint                    `json:"vendor_id" binding:"required"`
	EntryType      storage.LedgerEntryType `json:"entry_type" binding:"required"`
	Amount         float64                 `json:"amount" binding:"required"`
	ActualAmount   float64                 `json:"actual_amount"`
	OccurredAt     *time.Time              `json:"occurred_at"`
	Note           string                  `json:"note"`
	SourceType     string                  `json:"source_type"`
	CorrectionOfID *uint                   `json:"correction_of_id"`
}

func createVendorEntry(c *gin.Context, d *Deps) {
	var in vendorEntryInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.Accounting.FindVendorByID(in.VendorID); err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	actual := in.ActualAmount
	if actual == 0 {
		actual = in.Amount // 未填实付时默认等于到账额度。
	}
	entry, err := d.Accounting.CreateVendorLedgerEntry(storage.LedgerEntryInput{
		VendorID: in.VendorID, EntryType: in.EntryType, Amount: in.Amount,
		ActualAmount: actual, OccurredAt: occurred, Note: in.Note,
		SourceType: in.SourceType, CorrectionOfID: in.CorrectionOfID,
	})
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": entry})
}

// ---------------------------------------------------------------------------
// 上游快照
// ---------------------------------------------------------------------------

func listVendorSnapshots(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListVendorSnapshots()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type vendorSnapshotInput struct {
	VendorID       uint       `json:"vendor_id" binding:"required"`
	UsedAmount     float64    `json:"used_amount"`
	CurrentBalance float64    `json:"current_balance" binding:"required"`
	CapturedAt     *time.Time `json:"captured_at"`
	Note           string     `json:"note"`
}

func createVendorSnapshot(c *gin.Context, d *Deps) {
	var in vendorSnapshotInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.Accounting.FindVendorByID(in.VendorID); err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	captured := time.Now()
	if in.CapturedAt != nil {
		captured = *in.CapturedAt
	}
	snap, err := d.Accounting.UpsertVendorSnapshot(storage.VendorSnapshotInput{
		VendorID: in.VendorID, UsedAmount: in.UsedAmount,
		CurrentBalance: in.CurrentBalance, CapturedAt: captured, Note: in.Note,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": snap})
}

// ---------------------------------------------------------------------------
// 第三方资金流水
// ---------------------------------------------------------------------------

func listThirdPartyEntries(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListThirdPartyEntries()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type thirdPartyInput struct {
	PlatformName string                      `json:"platform_name"`
	EntryType    storage.ThirdPartyEntryType `json:"entry_type" binding:"required"`
	Amount       float64                     `json:"amount" binding:"required"`
	OccurredAt   *time.Time                  `json:"occurred_at"`
	Note         string                      `json:"note"`
}

func createThirdPartyEntry(c *gin.Context, d *Deps) {
	var in thirdPartyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	e := &storage.ThirdPartyEntry{
		PlatformName: in.PlatformName, EntryType: in.EntryType, Amount: in.Amount,
		OccurredAt: occurred, Note: in.Note,
	}
	if err := d.Accounting.CreateThirdPartyEntry(e); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": e})
}

// ---------------------------------------------------------------------------
// 运营费用
// ---------------------------------------------------------------------------

func listOperatingExpenses(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListOperatingExpenses()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type operatingExpenseInput struct {
	Category   string     `json:"category" binding:"required"`
	Amount     float64    `json:"amount" binding:"required"`
	OccurredAt *time.Time `json:"occurred_at"`
	Note       string     `json:"note"`
}

func createOperatingExpense(c *gin.Context, d *Deps) {
	var in operatingExpenseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	e := &storage.OperatingExpense{
		Category: in.Category, Amount: in.Amount, OccurredAt: occurred, Note: in.Note,
	}
	if err := d.Accounting.CreateOperatingExpense(e); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": e})
}

// ---------------------------------------------------------------------------
// 站内充值
// ---------------------------------------------------------------------------

func listTopupLogs(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListTopupLogs()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type topupCashInput struct {
	IncludedInCash bool `json:"included_in_cash"`
}

func setTopupCash(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in topupCashInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Accounting.SetTopupIncludedInCash(id, in.IncludedInCash); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func listDailyTopups(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListDailyTopups()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// ---------------------------------------------------------------------------
// 站内消费 / 用户余额 / 同步日志
// ---------------------------------------------------------------------------

func listChannelUsageStats(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListChannelUsageStats()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func listUserBalanceSnapshots(c *gin.Context, d *Deps) {
	at, err := d.Accounting.LatestUserBalanceSnapshotAt()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if at == nil {
		c.JSON(http.StatusOK, gin.H{"data": []storage.UserBalanceSnapshot{}})
		return
	}
	list, err := d.Accounting.ListUserBalanceSnapshotsAt(*at)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func listSyncTaskLogs(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListSyncTaskLogs(50)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// ---------------------------------------------------------------------------
// 自用用户
// ---------------------------------------------------------------------------

type selfUsersInput struct {
	Usernames []string `json:"usernames"`
}

func saveSelfUsers(c *gin.Context, d *Deps) {
	var in selfUsersInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Accounting.SetSelfUsernames(in.Usernames); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func getSelfUsers(c *gin.Context, d *Deps) {
	set, err := d.Accounting.GetSelfUsernames()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// ---------------------------------------------------------------------------
// 对账总览（S4 实现完整公式；此处先提供占位，返回基础汇总）
// ---------------------------------------------------------------------------

func reconciliationDashboard(c *gin.Context, d *Deps) {
	fail(c, http.StatusNotImplemented, errDashboardPending)
}
