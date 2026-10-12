package reconcile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

// sliceDBPath 指向从线上裁剪迁移出来的测试库（由 _migrate/migrate.py 生成）。
// 不存在时跳过：CI 环境没有这份数据也不应失败。
const sliceDBPath = "/Users/ikaros/Documents/code/_migrate/target.db"

// 对真实裁剪库跑一遍完整对账，验证：
//  1. 迁移后的时间戳能被驱动解析（T→空格回归保护）；
//  2. 用户余额批次能按 captured_at 精确匹配（userCount > 0）；
//  3. 各汇总口径不为空且自洽。
func TestDashboardAgainstMigratedSlice(t *testing.T) {
	if _, err := os.Stat(sliceDBPath); err != nil {
		t.Skipf("slice db not present at %s: %v", sliceDBPath, err)
	}
	// 复制一份，避免测试写入污染迁移产物。
	tmp := filepath.Join(t.TempDir(), "slice.db")
	if err := copyFile(sliceDBPath, tmp); err != nil {
		t.Fatalf("copy slice: %v", err)
	}
	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: tmp})
	if err != nil {
		t.Fatalf("open slice: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	dash, err := Build(db, Range{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// 迁移库里有 15 个上游，快照口径下应全部出现。
	if len(dash.Vendors) == 0 {
		t.Fatalf("no vendors in migrated slice")
	}

	// 时间戳回归保护：批次匹配必须命中，用户数应远大于 0。
	if dash.UserBalance.UserCount == 0 {
		t.Fatalf("userCount = 0 — 时间戳格式可能不匹配（T 分隔符残留）")
	}
	if dash.UserBalance.CapturedAt == nil {
		t.Fatalf("capturedAt is nil")
	}
	// 自助 / 外部拆分应守恒：self + external = total。
	if !approxEq(dash.UserBalance.SelfRemaining+dash.UserBalance.ExternalRemaining, dash.UserBalance.TotalRemaining) {
		t.Errorf("remaining split mismatch: self=%v external=%v total=%v",
			dash.UserBalance.SelfRemaining, dash.UserBalance.ExternalRemaining, dash.UserBalance.TotalRemaining)
	}
	if !approxEq(dash.UserBalance.SelfUsed+dash.UserBalance.ExternalUsed, dash.UserBalance.TotalUsed) {
		t.Errorf("used split mismatch: self=%v external=%v total=%v",
			dash.UserBalance.SelfUsed, dash.UserBalance.ExternalUsed, dash.UserBalance.TotalUsed)
	}
	// 上游实付净充值汇总应等于各上游之和。
	var sum float64
	for _, v := range dash.Vendors {
		sum += v.ActualRechargeNet
	}
	if !approxEq(sum, dash.Summary.TotalActualRechargeNet) {
		t.Errorf("total_actual_recharge_net = %v, per-vendor sum = %v", dash.Summary.TotalActualRechargeNet, sum)
	}
	t.Logf("slice: vendors=%d users=%d topup=%.2f vendorBal=%.2f userBal=%.2f realized=%.2f",
		len(dash.Vendors), dash.UserBalance.UserCount,
		dash.Summary.TotalTopup, dash.Summary.TotalVendorBalance,
		dash.Summary.TotalUserRemaining, dash.Summary.EstimatedRealizedProfit)
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-4
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}
