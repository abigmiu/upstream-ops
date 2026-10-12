package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// NewAPI 站点客户端，用于对账同步。
//
// 参考 ai_proxy_money 的 app/newapi.py：
//   - 鉴权头 Authorization: <token> + New-Api-User: <userId>
//   - quota 换算：quota / quotaPerUSD = 美元
//   - 充值日志金额从 content 文本解析（在线充值 / 兑换码充值）
type NewAPIClient struct {
	http        *resty.Client
	siteURL     string
	token       string
	userID      string
	quotaPerUSD float64
}

// NewAPIClientConfig 构造参数。
type NewAPIClientConfig struct {
	SiteURL     string
	Token       string
	UserID      string
	QuotaPerUSD float64
}

// NewNewAPIClient 构造客户端。quotaPerUSD<=0 时回落为 500000。
func NewNewAPIClient(cfg NewAPIClientConfig) *NewAPIClient {
	q := cfg.QuotaPerUSD
	if q <= 0 {
		q = 500000
	}
	c := resty.New().
		SetTimeout(30*time.Second).
		SetHeader("User-Agent", "Mozilla/5.0").
		SetHeader("Accept", "application/json").
		SetHeader("Authorization", cfg.Token).
		SetHeader("New-Api-User", cfg.UserID)
	return &NewAPIClient{
		http:        c,
		siteURL:     strings.TrimRight(cfg.SiteURL, "/"),
		token:       cfg.Token,
		userID:      cfg.UserID,
		quotaPerUSD: q,
	}
}

// apiEnvelope 通用响应信封。
type apiEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// getJSON 发起 GET 并校验 success=true。
func (c *NewAPIClient) getJSON(ctx context.Context, path string) (jsonRaw, error) {
	resp, err := c.http.R().SetContext(ctx).Get(c.siteURL + path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("newapi %s: HTTP %d", path, resp.StatusCode())
	}
	return resp.Body(), nil
}

// jsonRaw 别名，避免与 encoding/json 混淆。
type jsonRaw = []byte

// Validate 校验配置是否可用（打一个最小请求）。
func (c *NewAPIClient) Validate(ctx context.Context) error {
	body, err := c.getJSON(ctx, "/api/log/?p=1&page_size=1&type=1")
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(body)
	if err != nil {
		return err
	}
	if !env.Success {
		return fmt.Errorf("newapi 校验失败: %s", env.Message)
	}
	return nil
}

func decodeEnvelope(body []byte) (*apiEnvelope, error) {
	var env apiEnvelope
	if err := jsonUnmarshal(body, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// ---------------------------------------------------------------------------
// 渠道列表
// ---------------------------------------------------------------------------

// RemoteChannel 站内渠道。
type RemoteChannel struct {
	ChannelID   uint
	ChannelName string
	GroupName   string
	Tag         string
}

func (c *NewAPIClient) fetchChannels(ctx context.Context) ([]RemoteChannel, error) {
	const pageSize = 100
	var out []RemoteChannel
	for page := 1; page <= 100; page++ {
		path := fmt.Sprintf("/api/channel/?p=%d&page_size=%d", page, pageSize)
		body, err := c.getJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Total int `json:"total"`
				Items []struct {
					ID    jsonInt `json:"id"`
					Name  string  `json:"name"`
					Group string  `json:"group"`
					Tag   string  `json:"tag"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := jsonUnmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Data.Items {
			out = append(out, RemoteChannel{
				ChannelID:   uint(it.ID),
				ChannelName: it.Name,
				GroupName:   it.Group,
				Tag:         it.Tag,
			})
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 充值日志
// ---------------------------------------------------------------------------

// RemoteTopup 站内充值日志。
type RemoteTopup struct {
	ID         uint
	Username   string
	LogType    string
	Amount     float64
	OccurredAt time.Time
	Content    string
}

var amountPatterns = []*regexp.Regexp{
	regexp.MustCompile(`充值金额:\s*[＄$]([0-9]+(?:\.[0-9]+)?)`),
	regexp.MustCompile(`通过兑换码充值\s*[＄$]([0-9]+(?:\.[0-9]+)?)`),
}

func parseTopupAmount(content string) float64 {
	for _, re := range amountPatterns {
		if m := re.FindStringSubmatch(content); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				return v
			}
		}
	}
	return 0
}

func detectTopupType(content string) string {
	switch {
	case strings.Contains(content, "使用在线充值成功"):
		return "在线充值"
	case strings.Contains(content, "通过兑换码充值"):
		return "兑换码充值"
	case strings.Contains(content, "管理员补单成功"):
		return "管理员补单"
	default:
		return "其他充值"
	}
}

// fetchTopupLogs 拉取 [start, end] 区间的充值日志（按秒级时间戳过滤）。
func (c *NewAPIClient) fetchTopupLogs(ctx context.Context, start, end time.Time) ([]RemoteTopup, error) {
	const pageSize = 100
	startTs := start.Unix()
	endTs := end.Unix()
	var out []RemoteTopup
	for page := 1; page <= 1000; page++ {
		path := fmt.Sprintf("/api/log/?p=%d&page_size=%d&type=1&start_timestamp=%d&end_timestamp=%d",
			page, pageSize, startTs, endTs)
		body, err := c.getJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data struct {
				Total int `json:"total"`
				Items []struct {
					ID        jsonInt `json:"id"`
					Username  string  `json:"username"`
					CreatedAt jsonInt `json:"created_at"`
					Content   string  `json:"content"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := jsonUnmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Data.Items {
			content := it.Content
			occurred := time.Unix(int64(it.CreatedAt), 0).In(shanghaiZone())
			out = append(out, RemoteTopup{
				ID:         uint(it.ID),
				Username:   it.Username,
				LogType:    detectTopupType(content),
				Amount:     parseTopupAmount(content),
				OccurredAt: occurred,
				Content:    content,
			})
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 渠道消费
// ---------------------------------------------------------------------------

// RemoteUsage 单渠道消费。
type RemoteUsage struct {
	ChannelID   uint
	ChannelName string
	Amount      float64
	StartAt     time.Time
	EndAt       time.Time
}

// fetchChannelUsage 拉取某渠道在区间内的消费额度。
func (c *NewAPIClient) fetchChannelUsage(ctx context.Context, channelID uint, name string, start, end time.Time) (*RemoteUsage, error) {
	path := fmt.Sprintf("/api/log/stat?p=1&page_size=1&type=0&channel=%d&start_timestamp=%d&end_timestamp=%d",
		channelID, start.Unix(), end.Unix())
	body, err := c.getJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Quota float64 `json:"quota"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &RemoteUsage{
		ChannelID:   channelID,
		ChannelName: name,
		Amount:      resp.Data.Quota / c.quotaPerUSD,
		StartAt:     start,
		EndAt:       end,
	}, nil
}

// ---------------------------------------------------------------------------
// 用户余额
// ---------------------------------------------------------------------------

// RemoteUserBalance 用户余额。
type RemoteUserBalance struct {
	Username        string
	RemainingAmount float64
	UsedAmount      float64
	RequestCount    int
}

func (c *NewAPIClient) fetchUserBalances(ctx context.Context) ([]RemoteUserBalance, error) {
	const pageSize = 100
	var out []RemoteUserBalance
	for page := 1; page <= 1000; page++ {
		path := fmt.Sprintf("/api/user/?p=%d&page_size=%d", page, pageSize)
		body, err := c.getJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data struct {
				Items []struct {
					Username     string  `json:"username"`
					Quota        float64 `json:"quota"`
					UsedQuota    float64 `json:"used_quota"`
					RequestCount int     `json:"request_count"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := jsonUnmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Data.Items {
			if strings.TrimSpace(it.Username) == "" {
				continue
			}
			out = append(out, RemoteUserBalance{
				Username:        it.Username,
				RemainingAmount: round6(it.Quota / c.quotaPerUSD),
				UsedAmount:      round6(it.UsedQuota / c.quotaPerUSD),
				RequestCount:    it.RequestCount,
			})
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}
	return out, nil
}
