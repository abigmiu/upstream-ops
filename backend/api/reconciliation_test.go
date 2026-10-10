package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func newReconciliationRouter(t *testing.T) (*gin.Engine, *storage.Accounting) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	acc := storage.NewAccounting(db)
	if _, err := acc.EnsureDeletedVendor(); err != nil {
		t.Fatalf("ensure deleted vendor: %v", err)
	}
	d := &Deps{DB: db, Accounting: acc}
	r := gin.New()
	g := r.Group("/api")
	registerReconciliation(g, d)
	return r, acc
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestVendorCRUD(t *testing.T) {
	r, _ := newReconciliationRouter(t)

	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游X", "alias": "X"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data storage.Vendor `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Data.ID == 0 || created.Data.Name != "上游X" {
		t.Fatalf("vendor = %+v", created.Data)
	}

	// 重复名称 → 409
	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游X"})
	if w.Code != http.StatusConflict {
		t.Fatalf("dup status = %d, want 409", w.Code)
	}

	// 列表
	w = doJSON(t, r, http.MethodGet, "/api/reconciliation/vendors", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}

	// 禁用
	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors/1/enabled", map[string]any{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("disable status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestBindChannelAndUsage(t *testing.T) {
	r, acc := newReconciliationRouter(t)

	// 建上游
	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游Y"})
	var created struct {
		Data storage.Vendor `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	vid := created.Data.ID

	// 绑定渠道 5
	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/channel-bindings", map[string]any{
		"channel_id": 5, "channel_name": "ch5", "vendor_id": vid,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("bind status = %d body=%s", w.Code, w.Body.String())
	}

	// 消费写入后应能按绑定归属
	rows, err := acc.ListChannelBindings()
	if err != nil || len(rows) != 1 {
		t.Fatalf("bindings = %v err=%v", rows, err)
	}
	if rows[0].VendorID != vid {
		t.Fatalf("binding vendor = %d, want %d", rows[0].VendorID, vid)
	}
}

func TestVendorEntryAPIRecomputesBalances(t *testing.T) {
	r, _ := newReconciliationRouter(t)
	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游Z"})
	var created struct {
		Data storage.Vendor `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)

	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/vendor-entries", map[string]any{
		"vendor_id": created.Data.ID, "entry_type": "充值", "amount": 100, "actual_amount": 90,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("entry status = %d body=%s", w.Code, w.Body.String())
	}
	var entry struct {
		Data storage.VendorLedgerEntry `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &entry)
	if entry.Data.BalanceAfter != 100 {
		t.Fatalf("balance_after = %v, want 100", entry.Data.BalanceAfter)
	}

	// 非法类型 → 400
	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/vendor-entries", map[string]any{
		"vendor_id": created.Data.ID, "entry_type": "乱写", "amount": 10,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad type status = %d, want 400", w.Code)
	}
}

func TestSelfUsersRoundTrip(t *testing.T) {
	r, _ := newReconciliationRouter(t)
	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/settings/self-users", map[string]any{
		"usernames": []string{"me", "admin", "me"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d", w.Code)
	}
	w = doJSON(t, r, http.MethodGet, "/api/reconciliation/settings/self-users", nil)
	var out struct {
		Data []string `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data) != 2 {
		t.Fatalf("self users = %v, want 2 unique", out.Data)
	}
}

func TestSnapshotAPI(t *testing.T) {
	r, _ := newReconciliationRouter(t)
	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游S"})
	var created struct {
		Data storage.Vendor `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)

	w = doJSON(t, r, http.MethodPost, "/api/reconciliation/vendor-snapshots", map[string]any{
		"vendor_id": created.Data.ID, "used_amount": 30, "current_balance": 70,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("snapshot status = %d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(t, r, http.MethodGet, "/api/reconciliation/vendor-snapshots", nil)
	var out struct {
		Data []storage.VendorSnapshotRow `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data) != 1 || out.Data[0].UsedAmount != 30 {
		t.Fatalf("snapshots = %+v", out.Data)
	}
}

func TestDashboardEndpoint(t *testing.T) {
	r, _ := newReconciliationRouter(t)
	w := doJSON(t, r, http.MethodPost, "/api/reconciliation/vendors", map[string]any{"name": "上游D"})
	var created struct {
		Data storage.Vendor `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	doJSON(t, r, http.MethodPost, "/api/reconciliation/vendor-entries", map[string]any{
		"vendor_id": created.Data.ID, "entry_type": "充值", "amount": 100, "actual_amount": 100,
	})
	w = doJSON(t, r, http.MethodGet, "/api/reconciliation/dashboard", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			Summary map[string]any `json:"summary"`
			Vendors []any          `json:"vendors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Data.Vendors) == 0 {
		t.Fatalf("expected vendors in dashboard")
	}
}
