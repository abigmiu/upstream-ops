package reconcile

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveNewAPI 只在设置了 RECON_LIVE_NEWAPI=1 时运行，用于联调真实站点。
// 只读：仅拉取列表 / 统计，不修改任何远端数据。
//
//	RECON_LIVE_NEWAPI=1 RECON_SITE=https://ai.wewont.top RECON_TOKEN=xxx RECON_USER=1 go test ./backend/reconcile -run TestLiveNewAPI -v
func TestLiveNewAPI(t *testing.T) {
	if os.Getenv("RECON_LIVE_NEWAPI") != "1" {
		t.Skip("set RECON_LIVE_NEWAPI=1 to run live NewAPI test")
	}
	site := os.Getenv("RECON_SITE")
	token := os.Getenv("RECON_TOKEN")
	user := os.Getenv("RECON_USER")
	if site == "" || token == "" || user == "" {
		t.Fatal("RECON_SITE / RECON_TOKEN / RECON_USER required")
	}
	c := NewNewAPIClient(NewAPIClientConfig{SiteURL: site, Token: token, UserID: user, QuotaPerUSD: 500000})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := c.Validate(ctx); err != nil {
		t.Fatalf("validate: %v", err)
	}
	t.Log("validate OK")

	names, err := c.fetchChannels(ctx)
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	t.Logf("channels: %d", len(names))

	end := time.Now()
	start := end.Add(-24 * time.Hour)
	topups, err := c.fetchTopupLogs(ctx, start, end)
	if err != nil {
		t.Fatalf("topups: %v", err)
	}
	t.Logf("topups(24h): %d", len(topups))
	if len(topups) > 0 {
		s := topups[0]
		t.Logf("sample topup: id=%d user=%s type=%s amount=%.2f at=%s content=%q",
			s.ID, s.Username, s.LogType, s.Amount, s.OccurredAt.Format("2006-01-02 15:04"), s.Content)
	}

	if len(names) > 0 {
		u, err := c.fetchChannelUsage(ctx, names[0].ChannelID, names[0].ChannelName, start, end)
		if err != nil {
			t.Logf("channel usage err (channel may have no logs): %v", err)
		} else {
			t.Logf("channel %d usage(24h): %.4f", u.ChannelID, u.Amount)
		}
	}

	users, err := c.fetchUserBalances(ctx)
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	t.Logf("users: %d", len(users))
}

func TestParseTopupAmount(t *testing.T) {
	cases := map[string]float64{
		"通过兑换码充值 ＄20.000000 额度，兑换码ID 676": 20,
		"通过兑换码充值 $5.5 额度":                  5.5,
		"充值金额: ＄100.00":                     100,
		"管理员补单成功":                            0,
	}
	for in, want := range cases {
		if got := parseTopupAmount(in); got != want {
			t.Errorf("parseTopupAmount(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDetectTopupType(t *testing.T) {
	if got := detectTopupType("通过兑换码充值 ＄20 额度"); got != "兑换码充值" {
		t.Errorf("type = %q, want 兑换码充值", got)
	}
	if got := detectTopupType("使用在线充值成功"); got != "在线充值" {
		t.Errorf("type = %q, want 在线充值", got)
	}
	if got := detectTopupType("管理员补单成功"); got != "管理员补单" {
		t.Errorf("type = %q, want 管理员补单", got)
	}
	if got := detectTopupType("其它"); got != "其他充值" {
		t.Errorf("type = %q, want 其他充值", got)
	}
}

func TestJSONIntCompatibility(t *testing.T) {
	var v struct {
		A jsonInt `json:"a"`
		B jsonInt `json:"b"`
		C jsonInt `json:"c"`
	}
	if err := jsonUnmarshal([]byte(`{"a":199716,"b":"12345","c":199.0}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.A != 199716 || v.B != 12345 || v.C != 199 {
		t.Fatalf("got %d/%d/%d", v.A, v.B, v.C)
	}
}
