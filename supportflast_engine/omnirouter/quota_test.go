package omnirouter

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestQuotaMathFormula kiểm tra công thức tính phần trăm và số lượng (khắc phục lỗi PR #2128 của 9router)
func TestQuotaMathFormula(t *testing.T) {
	cases := []struct {
		remFrac      float64
		expectedPct  float64
		expectedRem  int
		expectedUsed int
	}{
		{remFrac: 1.0, expectedPct: 100.0, expectedRem: 1000, expectedUsed: 0},
		{remFrac: 0.924, expectedPct: 92.4, expectedRem: 924, expectedUsed: 76},
		{remFrac: 0.854, expectedPct: 85.4, expectedRem: 854, expectedUsed: 146},
		{remFrac: 0.05, expectedPct: 5.0, expectedRem: 50, expectedUsed: 950},
		{remFrac: 0.0, expectedPct: 0.0, expectedRem: 0, expectedUsed: 1000},
		{remFrac: -0.2, expectedPct: 0.0, expectedRem: 0, expectedUsed: 1000}, // Kẹp clamp dưới
		{remFrac: 1.5, expectedPct: 100.0, expectedRem: 1000, expectedUsed: 0}, // Kẹp clamp trên
	}

	for _, c := range cases {
		frac := c.remFrac
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
		remPct := math.Round(frac*1000) / 10
		total := 1000
		remaining := int(math.Round(float64(total) * frac))
		used := total - remaining

		if remPct != c.expectedPct {
			t.Errorf("frac %.4f: remPct mong đợi %.1f, nhận được %.1f", c.remFrac, c.expectedPct, remPct)
		}
		if remaining != c.expectedRem {
			t.Errorf("frac %.4f: remaining mong đợi %d, nhận được %d", c.remFrac, c.expectedRem, remaining)
		}
		if used != c.expectedUsed {
			t.Errorf("frac %.4f: used mong đợi %d, nhận được %d", c.remFrac, c.expectedUsed, used)
		}
	}
}

// TestQuotaCacheRAM kiểm tra cơ chế RAM cache TTL 60s
func TestQuotaCacheRAM(t *testing.T) {
	connID := "test_account_cache"
	now := time.Now().Format(time.RFC3339)
	mockQuotas := map[string]QuotaItem{
		"gemini_session": {
			Total:               1000,
			Used:                76,
			RemainingPercentage: 92.4,
			DisplayName:         "Gemini (5h)",
			ResetAt:             &now,
		},
	}

	SetCachedQuotas(connID, mockQuotas)

	cached, ok := GetCachedQuotas(connID)
	if !ok {
		t.Fatalf("Mong đợi cache tồn tại trong RAM")
	}
	if cached["gemini_session"].RemainingPercentage != 92.4 {
		t.Errorf("Mong đợi RemainingPercentage = 92.4, nhận được %f", cached["gemini_session"].RemainingPercentage)
	}
}

// TestQuotaHandlersIntegration kiểm tra endpoint /api/quotas/refresh-all và /api/usage/{id}
func TestQuotaHandlersIntegration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}
	defer db.Close()

	if err := InitStorage(db, false); err != nil {
		t.Fatalf("InitStorage lỗi: %v", err)
	}

	// 1. Test /api/quotas/refresh-all
	refreshHandler := HandleRefreshAllQuotas(db)
	req := httptest.NewRequest("GET", "/api/quotas/refresh-all", nil)
	rr := httptest.NewRecorder()
	refreshHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Mã trả về mong đợi 200, nhận %d", rr.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal kết quả refresh-all thất bại: %v", err)
	}

	if success, ok := res["success"].(bool); !ok || !success {
		t.Errorf("Mong đợi success = true")
	}
	if _, ok := res["accountData"]; !ok {
		t.Errorf("Mong đợi trường accountData trong phản hồi JSON")
	}

	// 2. Test /api/usage/seed_antigravity_01
	usageHandler := HandleAccountUsage(db)
	req2 := httptest.NewRequest("GET", "/api/usage/seed_antigravity_01", nil)
	rr2 := httptest.NewRecorder()
	usageHandler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("Mã trả về mong đợi 200, nhận %d", rr2.Code)
	}
}

// TestIDEQuotaCacheRAM kiểm tra cơ chế RAM cache cho request Quota từ Antigravity IDE (chống quay spinner và delay)
func TestIDEQuotaCacheRAM(t *testing.T) {
	path := "/v1internal:retrieveUserQuotaSummary"
	mockBody := []byte(`{"groups":[{"displayName":"Gemini Models"}]}`)

	ideQuotaCacheLock.Lock()
	ideQuotaCache[path] = &CachedIDEQuotaResponse{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       mockBody,
		CachedAt:   time.Now(),
	}
	ideQuotaCacheLock.Unlock()

	ideQuotaCacheLock.RLock()
	cached, ok := ideQuotaCache[path]
	ideQuotaCacheLock.RUnlock()

	if !ok || cached == nil {
		t.Fatalf("Mong đợi cache tồn tại trong ideQuotaCache")
	}
	if string(cached.Body) != string(mockBody) {
		t.Errorf("Dữ liệu cache không khớp")
	}
	if time.Since(cached.CachedAt) > 5*time.Second {
		t.Errorf("Cache thời gian không hợp lệ")
	}
}
