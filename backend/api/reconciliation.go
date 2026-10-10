package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)


// registerReconciliation 注册站长对账子系统的管理端接口。
// 全部挂在 /api 鉴权组下（由 Register 统一加 AuthMiddleware）。
func registerReconciliation(g *gin.RouterGroup, d *Deps) {
	if d.Accounting == nil {
		return
	}
	rg := g.Group("/reconciliation")

	// 上游 vendor
	rg.GET("/vendors", func(c *gin.Context) { listVendors(c, d) })
	rg.POST("/vendors", func(c *gin.Context) { createVendor(c, d) })
	rg.PUT("/vendors/:id", func(c *gin.Context) { updateVendor(c, d) })
	rg.POST("/vendors/:id/enabled", func(c *gin.Context) { setVendorEnabled(c, d) })
	rg.POST("/vendors/:id/merge-deleted", func(c *gin.Context) { mergeVendorIntoDeleted(c, d) })

	// 渠道 → 上游 绑定
	rg.GET("/channel-bindings", func(c *gin.Context) { listChannelBindings(c, d) })
	rg.POST("/channel-bindings", func(c *gin.Context) { bindChannel(c, d) })
	rg.GET("/channel-binding-history", func(c *gin.Context) { listChannelBindingHistory(c, d) })

	// 上游渠道（本系统监控渠道）→ 上游 绑定
	rg.GET("/upstream-channel-bindings", func(c *gin.Context) { listUpstreamChannelBindings(c, d) })
	rg.POST("/upstream-channel-bindings", func(c *gin.Context) { bindUpstreamChannel(c, d) })
	rg.POST("/upstream-channel-bindings/:id/delete", func(c *gin.Context) { deleteUpstreamChannelBinding(c, d) })

	// 对账渠道缓存
	rg.GET("/channels", func(c *gin.Context) { listReconciliationChannels(c, d) })

	// 上游流水 / 快照 / 第三方 / 运营费用
	rg.GET("/vendor-entries", func(c *gin.Context) { listVendorEntries(c, d) })
	rg.POST("/vendor-entries", func(c *gin.Context) { createVendorEntry(c, d) })
	rg.GET("/vendor-snapshots", func(c *gin.Context) { listVendorSnapshots(c, d) })
	rg.POST("/vendor-snapshots", func(c *gin.Context) { createVendorSnapshot(c, d) })
	rg.GET("/third-party-entries", func(c *gin.Context) { listThirdPartyEntries(c, d) })
	rg.POST("/third-party-entries", func(c *gin.Context) { createThirdPartyEntry(c, d) })
	rg.GET("/operating-expenses", func(c *gin.Context) { listOperatingExpenses(c, d) })
	rg.POST("/operating-expenses", func(c *gin.Context) { createOperatingExpense(c, d) })

	// 站内充值 / 消费 / 用户余额 / 同步日志
	rg.GET("/topup-logs", func(c *gin.Context) { listTopupLogs(c, d) })
	rg.POST("/topup-logs/:id/cash", func(c *gin.Context) { setTopupCash(c, d) })
	rg.GET("/daily-topups", func(c *gin.Context) { listDailyTopups(c, d) })
	rg.GET("/channel-usage-stats", func(c *gin.Context) { listChannelUsageStats(c, d) })
	rg.GET("/user-balance-snapshots", func(c *gin.Context) { listUserBalanceSnapshots(c, d) })
	rg.GET("/sync-task-logs", func(c *gin.Context) { listSyncTaskLogs(c, d) })

	// 对账总览
	rg.GET("/dashboard", func(c *gin.Context) { reconciliationDashboard(c, d) })

	// 自助用户标记
	rg.POST("/settings/self-users", func(c *gin.Context) { saveSelfUsers(c, d) })
	rg.GET("/settings/self-users", func(c *gin.Context) { getSelfUsers(c, d) })

	// NewAPI 同步
	rg.POST("/sync/channels", func(c *gin.Context) { syncChannelsHandler(c, d) })
	rg.POST("/sync/range", func(c *gin.Context) { syncRangeHandler(c, d) })
	rg.POST("/sync/incremental", func(c *gin.Context) { syncIncrementalHandler(c, d) })
	rg.POST("/sync/test", func(c *gin.Context) { testNewAPIHandler(c, d) })
}

// ---------------------------------------------------------------------------
// Vendors
// ---------------------------------------------------------------------------

func listVendors(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListVendors()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type vendorInput struct {
	Name    string `json:"name" binding:"required"`
	Alias   string `json:"alias"`
	Note    string `json:"note"`
	Enabled *bool  `json:"enabled"`
}

func createVendor(c *gin.Context, d *Deps) {
	var in vendorInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	v := &storage.Vendor{Name: in.Name, Alias: in.Alias, Note: in.Note, Enabled: enabled}
	if err := d.Accounting.CreateVendor(v); err != nil {
		if errors.Is(err, storage.ErrVendorExists) {
			fail(c, http.StatusConflict, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": v})
}

func updateVendor(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in vendorInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	v, err := d.Accounting.FindVendorByID(id)
	if err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	if in.Name != "" && in.Name != v.Name {
		if _, err := d.Accounting.FindVendorByName(in.Name); err == nil {
			fail(c, http.StatusConflict, storage.ErrVendorExists)
			return
		}
		v.Name = in.Name
	}
	v.Alias = in.Alias
	v.Note = in.Note
	if in.Enabled != nil {
		v.Enabled = *in.Enabled
	}
	if err := d.Accounting.UpdateVendor(v); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": v})
}

type enabledInput struct {
	Enabled bool `json:"enabled"`
}

func setVendorEnabled(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in enabledInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Accounting.SetVendorEnabled(id, in.Enabled); err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func mergeVendorIntoDeleted(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Accounting.MergeVendorIntoDeleted(id); err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Channel bindings
// ---------------------------------------------------------------------------

func listChannelBindings(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListChannelBindings()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type bindChannelInput struct {
	ChannelID     uint       `json:"channel_id" binding:"required"`
	ChannelName   string     `json:"channel_name"`
	GroupName     string     `json:"group_name"`
	VendorID      uint       `json:"vendor_id" binding:"required"`
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	Note          string     `json:"note"`
}

func bindChannel(c *gin.Context, d *Deps) {
	var in bindChannelInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.Accounting.FindVendorByID(in.VendorID); err != nil {
		fail(c, statusForErr(err), errors.New("上游不存在"))
		return
	}
	var from time.Time
	if in.EffectiveFrom != nil {
		from = *in.EffectiveFrom
	}
	b, err := d.Accounting.BindChannel(storage.BindChannelInput{
		ChannelID: in.ChannelID, ChannelName: in.ChannelName, GroupName: in.GroupName,
		VendorID: in.VendorID, EffectiveFrom: from, EffectiveTo: in.EffectiveTo, Note: in.Note,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": b})
}

func listChannelBindingHistory(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListChannelBindingHistory()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func listUpstreamChannelBindings(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListUpstreamChannelBindings()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

type upstreamBindInput struct {
	VendorID            uint   `json:"vendor_id" binding:"required"`
	UpstreamChannelID   string `json:"upstream_channel_id" binding:"required"`
	UpstreamChannelName string `json:"upstream_channel_name"`
	Note                string `json:"note"`
}

func bindUpstreamChannel(c *gin.Context, d *Deps) {
	var in upstreamBindInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.Accounting.FindVendorByID(in.VendorID); err != nil {
		fail(c, statusForErr(err), errors.New("上游不存在"))
		return
	}
	if err := d.Accounting.BindUpstreamChannel(in.VendorID, in.UpstreamChannelID, in.UpstreamChannelName, in.Note); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

func deleteUpstreamChannelBinding(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Accounting.DeleteUpstreamChannelBinding(id); err != nil {
		fail(c, statusForErr(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func listReconciliationChannels(c *gin.Context, d *Deps) {
	list, err := d.Accounting.ListReconciliationChannels()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// statusForErr 把 gorm.ErrRecordNotFound 映射成 404。
func statusForErr(err error) int {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
