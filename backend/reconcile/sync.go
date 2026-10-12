package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// shanghaiZone 返回对账统一时区。
func shanghaiZone() *time.Location { return storage.Shanghai }

// DayFloor 返回 t 所在自然日（上海时区）的 0 点。
func DayFloor(t time.Time) time.Time {
	d := t.In(shanghaiZone())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, shanghaiZone())
}

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
	// PG 非空时，渠道消费走直连 PostgreSQL（一条 SQL 汇总全部渠道）；
	// 为空则回退逐渠道 HTTP（旧行为）。所有权归调用方，由 SyncService.Close 释放。
	PG UsageSource
}

// SyncResult 一次同步的结果摘要。
// 注意：用户余额快照不再随窗口同步产生（另有每日任务），故不在此统计。
type SyncResult struct {
	Channels    int
	Topups      int
	UsageRows   int
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

// Close 释放持有的资源（当前仅 PG 连接池）。可安全重复调用。
func (s *SyncService) Close() {
	if s != nil && s.cfg.PG != nil {
		_ = s.cfg.PG.Close()
	}
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
	// 为 new-api 自动生成的托管渠道（上游分组）按 group 解析上游渠道 id 自动归属，
	// 必须在 SyncWindow 之前：未绑定的渠道消费不会被拉取（SyncWindow 只处理已绑定渠道）。
	bound, unresolved, err := s.autoBindManagedChannels(chans)
	if err != nil {
		return 0, err
	}
	if bound > 0 || unresolved > 0 {
		log.Printf("[reconcile] 托管渠道自动绑定: 新建 %d, 未解析 %d", bound, unresolved)
	}
	return len(models), nil
}

// SyncChannelsQuiet 同步渠道列表，出错不阻断（仅返回错误供日志记录）。
func (s *SyncService) SyncChannelsQuiet(ctx context.Context) error {
	_, err := s.SyncChannels(ctx)
	return err
}

// SyncWindow 同步一个时间窗口：充值日志 + 各渠道消费。
//
// usageMode:
//   - "replace"     按自然日绝对覆盖（增量同步与补数都走这个）
//   - "accumulate"  窗口增量累加（保留给「单日内细分窗口」语义；当前调用方已不再使用）
//
// 为什么增量同步用 replace 而不是 accumulate —— 游标与写入不是一个事务，累加语义会重复计入：
//   - 游标 SetSyncCursor 写失败会被静默吞掉（存储层是 `_ = SetSetting`），cron 又没有
//     SkipIfStillRunning 防重入，于是「同一游标被两次读取」和「行写成功但游标没推进」都可能发生；
//     后者的下一次窗口是上一次的**超集**。
//   - accumulate 的幂等守卫只认「end_at 完全相同」的重试（见 storage.SaveChannelUsageStats），
//     超集会被当成新窗口把整段再加一次 → 重叠区间金额翻倍。
//   - 改成按自然日 replace 写「绝对值」后，结果与游标是否推进、窗口是否重叠、是否并发无关：
//     重复/重试/并发都不会重复累加，游标停滞时还能顺带自愈缺口。
//
// 因此增量同步固定「从游标所在自然日 0 点起、mode=replace」
// （见 reconcile.Runner.RunIncremental 与 api.syncIncrementalHandler）。
//
// 用户余额快照已从中剥离（见 SyncUserBalance）：余额只需每天采一次，
// 跟随高频窗口同步会既浪费请求、又把「存量余额」混进窗口区间语义里。
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

	// 2) 渠道消费
	n, err := s.syncUsage(ctx, start, end, usageMode)
	if err != nil {
		return nil, err
	}
	res.UsageRows = n

	return res, nil
}

// syncUsage 写入 [start, end) 的渠道消费。
// 配置了 PG 时走直连库（一条 SQL），失败则回退逐渠道 HTTP，保证 PG 抖动不影响同步。
func (s *SyncService) syncUsage(ctx context.Context, start, end time.Time, mode string) (int, error) {
	if s.cfg.PG != nil {
		n, err := s.syncUsageFromPG(ctx, start, end, mode)
		if err == nil {
			return n, nil
		}
		log.Printf("[reconcile] PG 取渠道消费失败，回退逐渠道 HTTP: %v", err)
	}
	return s.syncUsageFromHTTP(ctx, start, end, mode)
}

// syncUsageFromPG 直连 PostgreSQL 汇总消费，按 (渠道, 上海日) 逐天落行。
//
// 无论 accumulate 还是 replace 都按天写入：窗口可能跨天（增量追赶 / 首次同步），
// 逐日归类才不会把多天用量堆到窗口起始那一天。
func (s *SyncService) syncUsageFromPG(ctx context.Context, start, end time.Time, mode string) (int, error) {
	days, err := s.cfg.PG.UsageByChannelDay(ctx, start, end)
	if err != nil {
		return 0, err
	}
	if len(days) == 0 {
		return 0, nil
	}
	names, err := s.channelNameMap()
	if err != nil {
		return 0, err
	}
	byDay := make(map[string][]storage.ChannelUsageInput)
	for _, d := range days {
		dayStart, err := time.ParseInLocation("2006-01-02", d.Day, shanghaiZone())
		if err != nil {
			continue
		}
		// 与查询区间求交：跨天窗口里每一天只落自己那一段。
		segStart := dayStart
		if segStart.Before(start) {
			segStart = start
		}
		segEnd := dayStart.Add(24*time.Hour - time.Second)
		if segEnd.After(end) {
			segEnd = end
		}
		byDay[d.Day] = append(byDay[d.Day], storage.ChannelUsageInput{
			ChannelID: d.ChannelID, ChannelName: names[d.ChannelID],
			Amount: d.Amount, StartAt: segStart, EndAt: segEnd,
		})
	}
	written := 0
	for _, inputs := range byDay {
		n, err := s.acc.SaveChannelUsageStats(inputs, mode)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

// syncUsageFromHTTP 逐渠道 HTTP 拉取消费（旧路径；无 PG 时的回退）。
// new-api 无 group-by-channel 接口，只能对每个已绑定渠道各发一次请求。
//
// 按天切分：跨天窗口逐天拉取，replace 语义才正确、accumulate 也不会把多天用量堆到一天。
func (s *SyncService) syncUsageFromHTTP(ctx context.Context, start, end time.Time, mode string) (int, error) {
	chans, err := s.acc.ListReconciliationChannels()
	if err != nil {
		return 0, err
	}
	// 预先取出已绑定渠道，避免每天都重查一遍绑定。
	bound := make([]storage.ReconciliationChannel, 0, len(chans))
	for _, c := range chans {
		binding, err := s.acc.CurrentBinding(c.ChannelID)
		if err != nil {
			return 0, err
		}
		if binding != nil {
			bound = append(bound, c)
		}
	}
	if len(bound) == 0 {
		return 0, nil
	}
	written := 0
	for day := DayFloor(start); day.Before(end); day = day.AddDate(0, 0, 1) {
		segStart := day
		if segStart.Before(start) {
			segStart = start
		}
		segEnd := day.Add(24*time.Hour - time.Second)
		if segEnd.After(end) {
			segEnd = end
		}
		inputs := make([]storage.ChannelUsageInput, 0, len(bound))
		for _, c := range bound {
			u, err := s.client.fetchChannelUsage(ctx, c.ChannelID, c.ChannelName, segStart, segEnd)
			if err != nil {
				// 单渠道失败不阻断整体；跳过。
				continue
			}
			inputs = append(inputs, storage.ChannelUsageInput{
				ChannelID: u.ChannelID, ChannelName: u.ChannelName,
				Amount: u.Amount, StartAt: u.StartAt, EndAt: u.EndAt,
			})
		}
		if len(inputs) == 0 {
			continue
		}
		n, err := s.acc.SaveChannelUsageStats(inputs, mode)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

// channelNameMap 返回渠道 id → 名称映射（来自本地渠道缓存）。
func (s *SyncService) channelNameMap() (map[uint]string, error) {
	chans, err := s.acc.ListReconciliationChannels()
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(chans))
	for _, c := range chans {
		out[c.ChannelID] = c.ChannelName
	}
	return out, nil
}

// BackfillUsageFromPG 用直连 PG 按天重建 [start, end) 的历史渠道消费（replace）。
//
// 逐日 replace：先删当天再整批覆盖，因此可安全重复执行。与逐日 HTTP 补数等价，
// 但只需一条 SQL，不受请求数 / 超时限制。
//
// 注意：调用方应把 end 截到「现在」，且回填覆盖到当天时应把同步游标推进到当下，
// 否则当天稍后的增量窗口会被当天已写入的 end_at 挡住（见 syncBackfillUsageHandler）。
func (s *SyncService) BackfillUsageFromPG(ctx context.Context, start, end time.Time) (int, error) {
	if s.cfg.PG == nil {
		return 0, fmt.Errorf("未配置 pgDsn，无法从 PostgreSQL 回填消费")
	}
	return s.syncUsageFromPG(ctx, start, end, "replace")
}

// SyncUserBalance 全量拉取站点用户余额，在 capturedAt 采集点落一份快照。
// 独立于窗口同步：一天采一次即可（快照按 (username, day) 覆盖，同一天重复采集取更晚的）。
func (s *SyncService) SyncUserBalance(ctx context.Context, capturedAt time.Time) (int, error) {
	users, err := s.client.fetchUserBalances(ctx)
	if err != nil {
		return 0, err
	}
	selfSet, err := s.acc.GetSelfUsernames()
	if err != nil {
		return 0, err
	}
	ubInputs := make([]storage.UserBalanceInput, 0, len(users))
	for _, u := range users {
		ubInputs = append(ubInputs, storage.UserBalanceInput{
			Username: u.Username, RemainingAmount: u.RemainingAmount,
			UsedAmount: u.UsedAmount, RequestCount: u.RequestCount,
		})
	}
	return s.acc.SaveUserBalanceSnapshots(ubInputs, capturedAt, selfSet)
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
