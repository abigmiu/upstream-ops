package reconcile

import (
	"regexp"
	"strconv"

	"github.com/bejix/upstream-ops/backend/storage"
)

// new-api「上游分组」自动生成的托管渠道：
//
//	tag   = upstream-ops-managed
//	group = uo-{上游渠道id}-{hash|分组id}   （上游渠道id == 本系统监控渠道 id，见 new-api
//	        service/upstream_group_sync.go：new-api 直接遍历本系统 /api/channels 拉取渠道）
//
// 因此 group 里的数字是精确的外键，不依赖 URL/域名。据此把托管渠道自动归属到上游。
const (
	managedChannelTag = "upstream-ops-managed"
	autoBindNote      = "自动绑定(上游分组)"
)

// upstreamChannelIdPattern 匹配 uo-{数字}-... 的前缀，取出监控渠道 id。
var upstreamChannelIdPattern = regexp.MustCompile(`^uo-(\d+)-`)

// parseUpstreamChannelId 从托管渠道的 group（uo-{上游渠道id}-…）解析上游渠道 id。
func parseUpstreamChannelId(group string) (uint, bool) {
	m := upstreamChannelIdPattern.FindStringSubmatch(group)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

// autoBindManagedChannels 为 new-api 自动生成的托管渠道建立渠道归属绑定。
//
// 链路：托管渠道.group(uo-{id}) → 上游渠道 id → upstream_channel_bindings(监控渠道id→vendor)
// → 写入 channel_bindings。精确、幂等、不覆盖手工绑定；无对应 vendor 时计入 unresolved，
// 不报错、不中断。
//
// 返回 (新建绑定数, 未解析/未匹配数)。
func (s *SyncService) autoBindManagedChannels(chans []RemoteChannel) (bound, unresolved int, err error) {
	// 仅处理托管渠道；普通站点无托管渠道时零开销。
	managed := make([]RemoteChannel, 0)
	for _, c := range chans {
		if c.Tag == managedChannelTag {
			managed = append(managed, c)
		}
	}
	if len(managed) == 0 {
		return 0, 0, nil
	}

	// 上游渠道 id（字符串）→ vendor：取最小 vendor id（与 ListUpstreamChannelBindings 的排序一致）。
	bindings, err := s.acc.ListUpstreamChannelBindings()
	if err != nil {
		return 0, 0, err
	}
	vendorFor := make(map[string]uint, len(bindings))
	for _, b := range bindings {
		if cur, ok := vendorFor[b.UpstreamChannelID]; !ok || b.VendorID < cur {
			vendorFor[b.UpstreamChannelID] = b.VendorID
		}
	}

	for _, c := range managed {
		upstreamID, ok := parseUpstreamChannelId(c.GroupName)
		if !ok {
			unresolved++
			continue
		}
		vendorID, ok := vendorFor[strconv.FormatUint(uint64(upstreamID), 10)]
		if !ok || vendorID == 0 {
			// 监控渠道 → vendor 尚未建立（如新增供应商），跳过，不报错。
			unresolved++
			continue
		}

		existing, err := s.acc.ActiveBindingFor(c.ChannelID)
		if err != nil {
			return bound, unresolved, err
		}
		if existing != nil {
			if existing.VendorID == vendorID {
				continue // 已绑定到同一上游，幂等跳过。
			}
			if existing.Note != autoBindNote {
				continue // 尊重手工绑定，绝不覆盖。
			}
			// 自动绑定但 vendor 变了 → 重新绑定（BindChannel 会关旧绑 + 记历史）。
		}

		if _, err := s.acc.BindChannel(storage.BindChannelInput{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			GroupName:   c.GroupName,
			VendorID:    vendorID,
			Note:        autoBindNote,
		}); err != nil {
			return bound, unresolved, err
		}
		bound++
	}
	return bound, unresolved, nil
}
