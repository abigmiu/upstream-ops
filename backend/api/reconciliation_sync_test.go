package api

import (
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// 回填区间解析：缺省全量、覆盖到现在时把 end 截到 now（否则当天 end_at 写成未来会挡住后续增量）。
func TestResolveBackfillRange(t *testing.T) {
	loc := storage.Shanghai
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, loc)

	cases := []struct {
		name           string
		startStr       string
		endStr         string
		wantStart      time.Time
		wantEnd        time.Time
		wantReachedNow bool
	}{
		{
			name: "缺省全量", startStr: "", endStr: "",
			wantStart: time.Date(2000, 1, 1, 0, 0, 0, 0, loc),
			wantEnd:   now, wantReachedNow: true,
		},
		{
			name: "结束日=今天", startStr: "", endStr: "2026-10-12",
			wantStart: time.Date(2000, 1, 1, 0, 0, 0, 0, loc),
			wantEnd:   now, wantReachedNow: true,
		},
		{
			name: "结束日=过去某天", startStr: "", endStr: "2026-08-01",
			wantStart: time.Date(2000, 1, 1, 0, 0, 0, 0, loc),
			wantEnd:   time.Date(2026, 8, 2, 0, 0, 0, 0, loc), wantReachedNow: false,
		},
		{
			name: "指定起止（都在过去）", startStr: "2026-07-01", endStr: "2026-07-31",
			wantStart: time.Date(2026, 7, 1, 0, 0, 0, 0, loc),
			wantEnd:   time.Date(2026, 8, 1, 0, 0, 0, 0, loc), wantReachedNow: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end, reached, err := resolveBackfillRange(now, c.startStr, c.endStr)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !start.Equal(c.wantStart) {
				t.Fatalf("start = %v, want %v", start, c.wantStart)
			}
			if !end.Equal(c.wantEnd) {
				t.Fatalf("end = %v, want %v", end, c.wantEnd)
			}
			if reached != c.wantReachedNow {
				t.Fatalf("reachedPresent = %v, want %v", reached, c.wantReachedNow)
			}
		})
	}
}
