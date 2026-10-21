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
		{remFrac: -0.2, expectedPct: 0.0, expectedRem: 0, expectedUsed: 1000},  // Kẹp clamp dưới
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

	// 1. Test /api/quotas/refresh-all (Bắt buộc xác thực Admin theo chuẩn P0)
	testAdminKey := "test_master_admin_key_secure_32_bytes_123"
	t.Setenv("MASTER_ADMIN_KEY", testAdminKey)
	refreshHandler := HandleRefreshAllQuotas(db)
	req := httptest.NewRequest("GET", "/api/quotas/refresh-all", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
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

	// 2. Test /api/usage/seed_antigravity_01 (yêu cầu Admin auth)
	usageHandler := HandleAccountUsage(db)
	req2 := httptest.NewRequest("GET", "/api/usage/seed_antigravity_01", nil)
	req2.Header.Set("Authorization", "Bearer "+testAdminKey)
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

// TestVirtualAggregatedQuotaPooling kiểm tra ảo hóa hạn mức chuẩn 9Router (chống 0% Claude & Notice cam)
func TestVirtualAggregatedQuotaPooling(t *testing.T) {
	rawInput := []byte(`{
		"groups": [
			{
				"displayName": "Gemini Models",
				"buckets": [
					{
						"bucketId": "gemini_weekly",
						"displayName": "Weekly Limit",
						"window": "WEEKLY",
						"remainingFraction": 0.93,
						"disabled": false
					}
				]
			},
			{
				"displayName": "Claude and GPT models",
				"buckets": [
					{
						"bucketId": "claude_weekly",
						"displayName": "Weekly Limit",
						"window": "WEEKLY",
						"remainingFraction": 0.0,
						"disabled": true
					}
				]
			}
		]
	}`)

	patched := buildVirtualAggregatedQuota(rawInput)

	var res struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				RemainingFraction float64 `json:"remainingFraction"`
				Disabled          bool    `json:"disabled"`
			} `json:"buckets"`
		} `json:"groups"`
	}

	if err := json.Unmarshal(patched, &res); err != nil {
		t.Fatalf("Không thể parse JSON sau ảo hóa: %v", err)
	}

	if len(res.Groups) != 2 {
		t.Fatalf("Mong đợi 2 nhóm, nhận được %d", len(res.Groups))
	}

	// Kiểm tra Claude group
	claudeGroup := res.Groups[1]
	if claudeGroup.Buckets[0].RemainingFraction != 1.0 {
		t.Errorf("Mong đợi Claude remainingFraction = 1.0, nhận %f", claudeGroup.Buckets[0].RemainingFraction)
	}
	if claudeGroup.Buckets[0].Disabled != false {
		t.Errorf("Mong đợi Claude disabled = false, nhận %v", claudeGroup.Buckets[0].Disabled)
	}

	// Kiểm tra Gemini group
	geminiGroup := res.Groups[0]
	if geminiGroup.Buckets[0].RemainingFraction != 1.0 {
		t.Errorf("Mong đợi Gemini remainingFraction = 1.0, nhận %f", geminiGroup.Buckets[0].RemainingFraction)
	}
	if geminiGroup.Buckets[0].Disabled != false {
		t.Errorf("Mong đợi Gemini disabled = false, nhận %v", geminiGroup.Buckets[0].Disabled)
	}
}

// TestModelAwareAccountOrdering kiểm tra định tuyến bám dính và ưu tiên tài khoản còn quota Claude
func TestModelAwareAccountOrdering(t *testing.T) {
	accExhausted := ProviderConnection{ID: "acc_cld_exhausted", Name: "Hết Claude"}
	accReady := ProviderConnection{ID: "acc_cld_ready", Name: "Còn Claude"}

	// Thiết lập cache Quota: Acc1 hết Claude (0%), Acc2 còn Claude (95%)
	SetCachedQuotas("acc_cld_exhausted", map[string]QuotaItem{
		"claude_gpt_weekly": {RemainingPercentage: 0.0},
	})
	SetCachedQuotas("acc_cld_ready", map[string]QuotaItem{
		"claude_gpt_weekly": {RemainingPercentage: 95.0},
	})

	accounts := []ProviderConnection{accExhausted, accReady}

	// Khi gọi Claude: Router phải ưu tiên đưa accReady lên đầu
	ordered := orderAccountsForModel(accounts, "claude-sonnet-4-6", 0)
	if len(ordered) != 2 {
		t.Fatalf("Mong đợi 2 accounts, nhận %d", len(ordered))
	}
	if ordered[0].ID != "acc_cld_ready" {
		t.Errorf("Mong đợi acc_cld_ready đứng đầu danh sách, nhận được: %s", ordered[0].ID)
	}

	// Khi gọi request không chỉ định model: giữ nguyên Session Affinity bắt đầu từ startIdx=0
	orderedGeneral := orderAccountsForModel(accounts, "", 0)
	if orderedGeneral[0].ID != "acc_cld_exhausted" {
		t.Errorf("Mong đợi Session Affinity giữ nguyên startIdx=0, nhận được: %s", orderedGeneral[0].ID)
	}
}

// TestWeightedStartIndexDistribution kiểm tra phân phối xác suất của Proportional Quota-Weighted Selection
func TestWeightedStartIndexDistribution(t *testing.T) {
	// 3 tài khoản: Quota lần lượt 90%, 60%, 30%
	accs := []ProviderConnection{
		{ID: "acc_90", Name: "Quota 90%"},
		{ID: "acc_60", Name: "Quota 60%"},
		{ID: "acc_30", Name: "Quota 30%"},
	}
	SetCachedQuotas("acc_90", map[string]QuotaItem{
		"gemini_weekly":  {RemainingPercentage: 90.0},
		"gemini_session": {RemainingPercentage: 90.0},
	})
	SetCachedQuotas("acc_60", map[string]QuotaItem{
		"gemini_weekly":  {RemainingPercentage: 60.0},
		"gemini_session": {RemainingPercentage: 60.0},
	})
	SetCachedQuotas("acc_30", map[string]QuotaItem{
		"gemini_weekly":  {RemainingPercentage: 30.0},
		"gemini_session": {RemainingPercentage: 30.0},
	})

	// Chạy 1200 lần và thống kê phân phối
	counts := make(map[string]int)
	trials := 1200
	for i := 0; i < trials; i++ {
		idx := selectWeightedStartIndex(accs, "gemini-2.5-pro")
		counts[accs[idx].ID]++
	}

	// Tổng trọng số: 90+60+30 = 180
	// Tỷ lệ kỳ vọng: acc_90 = 50%, acc_60 = 33.3%, acc_30 = 16.7%
	// Cho phép sai số ±10% (±120 lần)
	pct90 := float64(counts["acc_90"]) / float64(trials) * 100
	pct60 := float64(counts["acc_60"]) / float64(trials) * 100
	pct30 := float64(counts["acc_30"]) / float64(trials) * 100

	t.Logf("Phân phối WRS sau %d lần: acc_90=%.1f%% (kỳ vọng~50%%), acc_60=%.1f%% (kỳ vọng~33%%), acc_30=%.1f%% (kỳ vọng~17%%)",
		trials, pct90, pct60, pct30)

	if pct90 < 35 || pct90 > 65 {
		t.Errorf("acc_90 nằm ngoài khoảng kỳ vọng [35,65]%%: %.1f%%", pct90)
	}
	if pct60 < 22 || pct60 > 44 {
		t.Errorf("acc_60 nằm ngoài khoảng kỳ vọng [22,44]%%: %.1f%%", pct60)
	}
	if pct30 < 8 || pct30 > 28 {
		t.Errorf("acc_30 nằm ngoài khoảng kỳ vọng [8,28]%%: %.1f%%", pct30)
	}
}

// TestWeightedSelectionQuotaEqualization kiểm tra rằng tài khoản cạn quota (0%) không bao giờ được chọn bởi WRS
func TestWeightedSelectionQuotaEqualization(t *testing.T) {
	accs := []ProviderConnection{
		{ID: "acc_full", Name: "Đầy"},
		{ID: "acc_empty", Name: "Hết"},
	}
	SetCachedQuotas("acc_full", map[string]QuotaItem{
		"claude_gpt_weekly":  {RemainingPercentage: 80.0},
		"claude_gpt_session": {RemainingPercentage: 80.0},
	})
	SetCachedQuotas("acc_empty", map[string]QuotaItem{
		"claude_gpt_weekly":  {RemainingPercentage: 0.0},
		"claude_gpt_session": {RemainingPercentage: 0.0},
	})

	// Chạy 200 lần: acc_empty (weight=0) không bao giờ nên được chọn
	for i := 0; i < 200; i++ {
		idx := selectWeightedStartIndex(accs, "claude-sonnet-4-6")
		if accs[idx].ID == "acc_empty" {
			t.Errorf("Lần thứ %d: WRS đã chọn tài khoản cạn quota (acc_empty), trọng số phải là 0!", i+1)
			return
		}
	}
}
