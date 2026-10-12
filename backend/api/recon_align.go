package api

import "strconv"

// reconChannelUsage 监控渠道对应的「对账口径」指标。
type reconChannelUsage struct {
	Used    float64 // 对账口径累计消费（= 对账页「上游已用」）
	Balance float64 // 对账口径当前余额
}

// reconUsageByChannel 返回 监控渠道 id(字符串) → 对账口径指标。
//
// 监控侧采集的「累计消费」直接取供应商面板自报值，而不少面板只保证余额正确、
// 累计消费会漏报或错报，导致监控页与对账页对不上。这里通过对账的
// upstream_channel_bindings 把监控渠道映射到 vendor，再取该 vendor 最新快照的
// used_amount / current_balance——与对账引擎完全同源，从根上消除口径差。
//
// 一个渠道绑定到多个 vendor 时求和（正常为 1:1）。未绑定或对账未建快照的渠道
// 不在此映射内，调用方回退到面板自报值。
func reconUsageByChannel(d *Deps) map[string]reconChannelUsage {
	if d == nil || d.Accounting == nil {
		return nil
	}
	bindings, err := d.Accounting.ListUpstreamChannelBindings()
	if err != nil {
		return nil
	}
	if len(bindings) == 0 {
		return nil
	}
	usage, err := d.Accounting.LatestVendorUsage()
	if err != nil {
		return nil
	}
	out := make(map[string]reconChannelUsage, len(bindings))
	for _, b := range bindings {
		pt, ok := usage[b.VendorID]
		if !ok {
			continue
		}
		cur := out[b.UpstreamChannelID]
		cur.Used += pt.Used
		cur.Balance += pt.Balance
		out[b.UpstreamChannelID] = cur
	}
	return out
}

// reconUsageForChannel 按渠道 id 取对账口径指标。
func reconUsageForChannel(m map[string]reconChannelUsage, channelID uint) (reconChannelUsage, bool) {
	if m == nil {
		return reconChannelUsage{}, false
	}
	r, ok := m[strconv.FormatUint(uint64(channelID), 10)]
	return r, ok
}
