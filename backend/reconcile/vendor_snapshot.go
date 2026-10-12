package reconcile

import (
	"strconv"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// VendorSnapshotService 把监控采集到的上游余额，自动落成对账的每日余额快照。
//
// 背景：上游面板会清空充值/消费记录，但余额始终正确；而监控本就每隔十几分钟
// 采一次每个渠道的余额。这里把「已绑定监控渠道」的余额汇总成 vendor_snapshots
// （一天一行，Upsert 取当天最后一条），并顺带由
//
//	累计已用 = 累计净到账(流水台账) − 当前余额
//
// 推算出 used_amount。于是上游余额与累计已用都不再需要人工手抄。
type VendorSnapshotService struct {
	acc      *storage.Accounting
	channels *storage.Channels
}

// NewVendorSnapshotService 构造自动快照服务。
func NewVendorSnapshotService(acc *storage.Accounting, channels *storage.Channels) *VendorSnapshotService {
	return &VendorSnapshotService{acc: acc, channels: channels}
}

// SyncFromMonitoring 采集一次：对每个「已绑定且监控有余额」的上游，
// 写入/更新当天的余额快照（余额取监控最新值，已用由台账反推）。
// 返回写入的上游数。没有绑定或没有任何余额时返回 0，不算错误。
func (s *VendorSnapshotService) SyncFromMonitoring() (int, error) {
	if s == nil || s.acc == nil || s.channels == nil {
		return 0, nil
	}
	bindings, err := s.acc.ListUpstreamChannelBindings()
	if err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		return 0, nil
	}
	chans, err := s.channels.List()
	if err != nil {
		return 0, err
	}
	// 监控渠道 id（字符串）→ 最新余额。只认有余额的渠道。
	balanceByChannel := make(map[string]float64, len(chans))
	for _, c := range chans {
		if c.LastBalance != nil {
			balanceByChannel[strconv.FormatUint(uint64(c.ID), 10)] = *c.LastBalance
		}
	}
	net, err := s.acc.NetRechargeByVendor()
	if err != nil {
		return 0, err
	}

	now := time.Now().In(storage.Shanghai)
	written := 0
	for _, b := range bindings {
		bal, ok := balanceByChannel[b.UpstreamChannelID]
		if !ok {
			continue // 该渠道尚未采到余额，跳过
		}
		used := round6(net[b.VendorID] - bal)
		if _, err := s.acc.UpsertVendorSnapshot(storage.VendorSnapshotInput{
			VendorID:       b.VendorID,
			UsedAmount:     used,
			CurrentBalance: bal,
			CapturedAt:     now,
			Note:           "自动采集",
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
