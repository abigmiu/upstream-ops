package runtimeconfig

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/channel"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/scheduler"
)

type fakeHTTPConfigConnector struct {
	cfg connector.HTTPConfig
}

func (f *fakeHTTPConfigConnector) SetHTTPConfig(cfg connector.HTTPConfig) {
	f.cfg = cfg
}

func TestApplyFromFileUpdatesUpstreamConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{
			BalanceCron: "",
			RateCron:    "",
			Retention:   config.RetentionConfig{Cron: ""},
		},
		Upstream: config.UpstreamConfig{
			TimeoutSeconds: 45,
			UserAgent:      "custom-agent",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	channelSvc := channel.NewService(nil, nil, nil, nil, nil, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := New(
		path,
		"",
		log,
		nil,
		channelSvc,
		nil,
		nil,
		nil,
		config.ProxyConfig{},
		config.UpstreamConfig{},
		config.GatewayConfig{},
		config.ReconciliationConfig{},
		func(scfg config.SchedulerConfig, pcfg config.ProxyConfig) *scheduler.Scheduler {
			return scheduler.New(scfg, nil, nil, nil, nil, nil, nil, nil, nil, pcfg, log)
		},
	)

	result, err := mgr.ApplyFromFile()
	if err != nil {
		t.Fatalf("ApplyFromFile: %v", err)
	}
	if len(result.AppliedSections) == 0 {
		t.Fatalf("applied sections empty")
	}
	if got := mgr.CurrentUpstream(); got.TimeoutSeconds != 45 || got.UserAgent != "custom-agent" {
		t.Fatalf("current upstream = %#v", got)
	}

	conn := &fakeHTTPConfigConnector{}
	channelSvc.ApplyHTTPConfig(conn)
	if conn.cfg.Timeout != 45*time.Second {
		t.Fatalf("timeout = %s", conn.cfg.Timeout)
	}
	if conn.cfg.UserAgent != "custom-agent" {
		t.Fatalf("user agent = %q", conn.cfg.UserAgent)
	}
}

// 启动时（New）必须把对账配置注入缓存：CurrentReconciliation 读的就是它。
// 回归历史 bug：只在 ApplyFromFile 里赋值会导致重启后对账静默停摆。
func TestNewPopulatesReconciliationConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	want := config.ReconciliationConfig{
		Enabled:     true,
		SyncCron:    "0 9 * * * *",
		SiteURL:     "https://ai.example.com",
		Token:       "tok",
		UserID:      "1",
		QuotaPerUSD: 500000,
	}
	mgr := New(
		filepath.Join(t.TempDir(), "config.yaml"),
		"",
		log,
		nil, nil, nil, nil, nil,
		config.ProxyConfig{},
		config.UpstreamConfig{},
		config.GatewayConfig{},
		want,
		nil,
	)
	if got := mgr.CurrentReconciliation(); got.SiteURL != want.SiteURL || !got.Enabled || got.Token != want.Token {
		t.Fatalf("CurrentReconciliation at boot = %#v, want %#v", got, want)
	}
}
