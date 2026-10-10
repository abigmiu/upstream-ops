package reconcile

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// shanghaiZone 返回对账统一时区。
func shanghaiZone() *time.Location { return storage.Shanghai }

// jsonUnmarshal 薄封装，便于测试替换。
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// jsonInt 兼容 NewAPI 返回的 number 或 string 型整数。
type jsonInt int64

func (j *jsonInt) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" || s == "" {
		*j = 0
		return nil
	}
	if s[0] == '"' {
		unq, err := strconv.Unquote(s)
		if err != nil {
			return err
		}
		s = unq
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// 兼容浮点（如 199.0）
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return err
		}
		v = int64(f)
	}
	*j = jsonInt(v)
	return nil
}

// SyncConfig 同步参数。
type SyncConfig struct {
	QuotaPerUSD        float64
	NonCashTopupLogIDs map[int64]bool
}

// SyncResult 一次同步的结果摘要。
type SyncResult struct {
	Channels    int
	Topups      int
	UsageRows   int
	UserRows    int
	WindowStart time.Time
	WindowEnd   time.Time
}

// SyncService 编排从 NewAPI 拉取数据并写入对账库。
type SyncService struct {
	client *NewAPIClient
	acc    *storage.Accounting
	cfg    SyncConfig
}

// NewSyncService 构造同步服务。
func NewSyncService(client *NewAPIClient, acc *storage.Accounting, cfg SyncConfig) *SyncService {
	return &SyncService{client: client, acc: acc, cfg: cfg}
}

// SyncChannels 同步渠道列表到对账渠道缓存。
func (s *SyncService) SyncChannels(ctx context.Context) (int, error) {
	chans, err := s.client.fetchChannels(ctx)
	if err != nil {
		return 0, err
	}
	models := make([]storage.ReconciliationChannel, 0, len(chans))
	for _, c := range chans {
		models = append(models, storage.ReconciliationChannel{
			ChannelID: c.ChannelID, ChannelName: c.ChannelName, GroupName: c.GroupName, IsActive: true,
		})
	}
	if err := s.acc.SaveReconciliationChannels(models); err != nil {
		return 0, err
	}
	return len(models), nil
}

// SyncChannelsQuiet 同步渠道列表，出错不阻断（仅返回错误供日志记录）。
func (s *SyncService) SyncChannelsQuiet(ctx context.Context) error {
	_, err := s.SyncChannels(ctx)
	return err
}

// SyncWindow 同步一个时间窗口：充值日志 + 各渠道消费 + 用户余额快照。
// usageMode 决定消费写入方式（accumulate / replace）。
func (s *SyncService) SyncWindow(ctx context.Context, start, end time.Time, usageMode string) (*SyncResult, error) {
	res := &SyncResult{WindowStart: start, WindowEnd: end}

	// 1) 充值日志
	topups, err := s.client.fetchTopupLogs(ctx, start, end)
	if err != nil {
		return nil, err
	}
	inputs := make([]storage.TopupLogInput, 0, len(topups))
	for _, t := range topups {
		inputs = append(inputs, storage.TopupLogInput{
			ID: t.ID, Username: t.Username, LogType: t.LogType,
			Amount: t.Amount, OccurredAt: t.OccurredAt, Content: t.Content,
		})
	}
	_, _, err = s.acc.SaveTopupLogs(inputs)
	if err != nil {
		return nil, err
	}
	res.Topups = len(inputs)

	// 2) 渠道消费（仅对已绑定的渠道拉取）
	chans, err := s.acc.ListReconciliationChannels()
	if err != nil {
		return nil, err
	}
	usageInputs := make([]storage.ChannelUsageInput, 0, len(chans))
	for _, c := range chans {
		binding, err := s.acc.CurrentBinding(c.ChannelID)
		if err != nil {
			return nil, err
		}
		if binding == nil {
			continue
		}
		u, err := s.client.fetchChannelUsage(ctx, c.ChannelID, c.ChannelName, start, end)
		if err != nil {
			// 单渠道失败不阻断整体；跳过。
			continue
		}
		usageInputs = append(usageInputs, storage.ChannelUsageInput{
			ChannelID: u.ChannelID, ChannelName: u.ChannelName,
			Amount: u.Amount, StartAt: u.StartAt, EndAt: u.EndAt,
		})
	}
	if len(usageInputs) > 0 {
		n, err := s.acc.SaveChannelUsageStats(usageInputs, usageMode)
		if err != nil {
			return nil, err
		}
		res.UsageRows = n
	}

	// 3) 用户余额快照（以窗口结束时间为采集点）
	users, err := s.client.fetchUserBalances(ctx)
	if err != nil {
		return nil, err
	}
	selfSet, err := s.acc.GetSelfUsernames()
	if err != nil {
		return nil, err
	}
	ubInputs := make([]storage.UserBalanceInput, 0, len(users))
	for _, u := range users {
		ubInputs = append(ubInputs, storage.UserBalanceInput{
			Username: u.Username, RemainingAmount: u.RemainingAmount,
			UsedAmount: u.UsedAmount, RequestCount: u.RequestCount,
		})
	}
	if n, err := s.acc.SaveUserBalanceSnapshots(ubInputs, end, selfSet); err == nil {
		res.UserRows = n
	}

	return res, nil
}

// ResolveChannelNames 返回渠道 id → 名称映射，用于消费归属。
func (s *SyncService) ResolveChannelNames(ctx context.Context) (map[uint]string, error) {
	chans, err := s.client.fetchChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(chans))
	for _, c := range chans {
		out[c.ChannelID] = c.ChannelName
	}
	return out, nil
}
