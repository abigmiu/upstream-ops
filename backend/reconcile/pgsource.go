package reconcile

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// UsageSource 汇总渠道消费的数据源。生产实现是直连 new-api 的 PGUsageSource；
// 抽成接口便于测试注入替身（无需真实 PG）。
type UsageSource interface {
	// UsageByChannel 返回 [start, end) 内各渠道的消费（USD）。
	UsageByChannel(ctx context.Context, start, end time.Time) (map[uint]float64, error)
	// UsageByChannelDay 返回 [start, end) 内按 (渠道, 上海日) 汇总的消费（USD），用于历史回填。
	UsageByChannelDay(ctx context.Context, start, end time.Time) ([]ChannelUsageDay, error)
	Close() error
}

// PGUsageSource 直连 new-api 的 PostgreSQL，按渠道汇总消费。
//
// 为什么需要它：new-api 的 HTTP 接口只暴露 /api/log/stat，而该接口永远只返回
// 单个 SUM（type 被硬编码为 LogTypeConsume），没有 group-by-channel 的出口。
// 于是「按渠道取消费」只能对每个渠道各发一次请求（N 次 HTTP）。
// new-api 库里有 logs 明细表，一条 GROUP BY channel_id 即可覆盖全部渠道，
// 且能一次性回填任意历史区间（HTTP 窗口机制只能往前滚、补不了历史）。
//
// 只读：仅执行 SELECT。
type PGUsageSource struct {
	db          *sql.DB
	quotaPerUSD float64
}

// consumeType 是 new-api 的消费日志类型（model.LogTypeConsume = 2）。
const consumeType = 2

// ChannelUsageDay 某渠道在某天（上海时区）的消费。
type ChannelUsageDay struct {
	ChannelID uint
	Day       string // YYYY-MM-DD（上海时区）
	Amount    float64
}

// NewPGUsageSource 打开一个 PG 连接池（惰性，首次查询才真正连接）。
// quotaPerUSD<=0 时回落为 500000。
func NewPGUsageSource(dsn string, quotaPerUSD float64) (*PGUsageSource, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	q := quotaPerUSD
	if q <= 0 {
		q = 500000
	}
	return &PGUsageSource{db: db, quotaPerUSD: q}, nil
}

// Close 关闭连接池。
func (p *PGUsageSource) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

// UsageByChannel 返回 [start, end) 内各渠道的消费（USD）。一条 SQL 覆盖全部渠道。
func (p *PGUsageSource) UsageByChannel(ctx context.Context, start, end time.Time) (map[uint]float64, error) {
	const q = `SELECT channel_id, SUM(quota) FROM logs
	           WHERE type = $1 AND created_at >= $2 AND created_at < $3
	           GROUP BY channel_id`
	rows, err := p.db.QueryContext(ctx, q, consumeType, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询渠道消费: %w", err)
	}
	defer rows.Close()
	out := make(map[uint]float64)
	for rows.Next() {
		var ch, quota int64
		if err := rows.Scan(&ch, &quota); err != nil {
			return nil, err
		}
		if ch <= 0 {
			continue
		}
		out[uint(ch)] = float64(quota) / p.quotaPerUSD
	}
	return out, rows.Err()
}

// UsageByChannelDay 返回 [start, end) 内按 (渠道, 上海日) 汇总的消费（USD），用于历史回填。
func (p *PGUsageSource) UsageByChannelDay(ctx context.Context, start, end time.Time) ([]ChannelUsageDay, error) {
	const q = `SELECT channel_id,
	                  to_char(to_timestamp(created_at) AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day,
	                  SUM(quota)
	           FROM logs
	           WHERE type = $1 AND created_at >= $2 AND created_at < $3
	           GROUP BY channel_id, day`
	rows, err := p.db.QueryContext(ctx, q, consumeType, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询渠道消费(按天): %w", err)
	}
	defer rows.Close()
	var out []ChannelUsageDay
	for rows.Next() {
		var ch, quota int64
		var day string
		if err := rows.Scan(&ch, &day, &quota); err != nil {
			return nil, err
		}
		if ch <= 0 {
			continue
		}
		out = append(out, ChannelUsageDay{
			ChannelID: uint(ch), Day: day, Amount: float64(quota) / p.quotaPerUSD,
		})
	}
	return out, rows.Err()
}
