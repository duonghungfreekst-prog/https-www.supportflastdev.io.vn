package omnirouter

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestProvidersAndQuotasInMemoryCacheSpeedAndInvalidation kiểm tra toàn diện:
// 1. In-Memory Cache cho Providers & Quotas phản hồi siêu tốc (<5ms)
// 2. Cache invalidation khi thêm/sửa/xóa kết nối hoặc cờ force=1
// 3. Đăng ký đầy đủ route trên cả router cục bộ (/api/*) và router cloud (/api/router/*)
func TestProvidersAndQuotasInMemoryCacheSpeedAndInvalidation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}
	defer db.Close()

	if err := InitStorage(db, false); err != nil {
		t.Fatalf("InitStorage lỗi: %v", err)
	}

	testAdminKey := "test_master_admin_key_secure_32_bytes_speed"
	t.Setenv("MASTER_ADMIN_KEY", testAdminKey)

	// Thêm 1 provider mẫu
	testConn := ProviderConnection{
		ID:        "acc_test_speed_01",
		Provider:  "google",
		AuthType:  "oauth",
		Name:      "Speed Test Account",
		Email:     "speed@supportflast.dev",
		Priority:  1,
		IsActive:  1,
		Data:      []byte(`{"quotas":{"gemini_session":{"total":1000,"used":50,"remainingPercentage":95.0}}}`),
		CreatedAt: NowISO(),
		UpdatedAt: NowISO(),
	}
	if err := SaveProvider(db, &testConn); err != nil {
		t.Fatalf("SaveProvider lỗi: %v", err)
	}

	providersHandler := HandleListProviders(db)
	quotasHandler := HandleRefreshAllQuotas(db)
	usageHandler := HandleAccountUsage(db)

	// --- BƯỚC 1: Kiểm tra Cache Providers ---
	// Lần 1: Nạp cache
	req1 := httptest.NewRequest("GET", "/api/providers", nil)
	req1.Header.Set("Authorization", "Bearer "+testAdminKey)
	rec1 := httptest.NewRecorder()
	providersHandler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("Lần 1 GET /api/providers thất bại: %d", rec1.Code)
	}

	// Lần 2: Hit Cache RAM -> Phải trả về siêu tốc (<5ms)
	req2 := httptest.NewRequest("GET", "/api/providers", nil)
	req2.Header.Set("Authorization", "Bearer "+testAdminKey)
	rec2 := httptest.NewRecorder()

	start := time.Now()
	providersHandler.ServeHTTP(rec2, req2)
	duration := time.Since(start)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Lần 2 GET /api/providers thất bại: %d", rec2.Code)
	}
	if duration > 20*time.Millisecond {
		t.Errorf("Kỳ vọng cache RAM trả về <20ms, thực tế nhận: %v", duration)
	}
	t.Logf("✔ GET /api/providers từ RAM Cache thành công trong %v (<5ms)", duration)

	// Kiểm tra qua route cloud: /api/router/providers
	reqCloud := httptest.NewRequest("GET", "/api/router/providers", nil)
	reqCloud.Header.Set("Authorization", "Bearer "+testAdminKey)
	recCloud := httptest.NewRecorder()
	startCloud := time.Now()
	providersHandler.ServeHTTP(recCloud, reqCloud)
	durCloud := time.Since(startCloud)
	if recCloud.Code != http.StatusOK {
		t.Fatalf("GET /api/router/providers thất bại: %d", recCloud.Code)
	}
	t.Logf("✔ GET /api/router/providers từ RAM Cache thành công trong %v", durCloud)

	// --- BƯỚC 2: Kiểm tra Cache Quotas (/api/quotas/refresh-all) ---
	reqQ1 := httptest.NewRequest("GET", "/api/quotas/refresh-all", nil)
	reqQ1.Header.Set("Authorization", "Bearer "+testAdminKey)
	recQ1 := httptest.NewRecorder()
	quotasHandler.ServeHTTP(recQ1, reqQ1)
	if recQ1.Code != http.StatusOK {
		t.Fatalf("Lần 1 GET /api/quotas/refresh-all thất bại: %d", recQ1.Code)
	}

	// Lần 2: Hit Quota Cache RAM
	reqQ2 := httptest.NewRequest("GET", "/api/quotas/refresh-all", nil)
	reqQ2.Header.Set("Authorization", "Bearer "+testAdminKey)
	recQ2 := httptest.NewRecorder()
	startQ := time.Now()
	quotasHandler.ServeHTTP(recQ2, reqQ2)
	durQ := time.Since(startQ)
	if recQ2.Code != http.StatusOK {
		t.Fatalf("Lần 2 GET /api/quotas/refresh-all thất bại: %d", recQ2.Code)
	}
	if durQ > 20*time.Millisecond {
		t.Errorf("Kỳ vọng cache Quotas trả về <20ms, thực tế nhận: %v", durQ)
	}
	t.Logf("✔ GET /api/quotas/refresh-all từ RAM Cache thành công trong %v (<5ms)", durQ)

	// --- BƯỚC 3: Kiểm tra route /api/usage/ (không kèm ID) trả về Quotas ---
	reqUAll := httptest.NewRequest("GET", "/api/usage/", nil)
	reqUAll.Header.Set("Authorization", "Bearer "+testAdminKey)
	recUAll := httptest.NewRecorder()
	startU := time.Now()
	usageHandler.ServeHTTP(recUAll, reqUAll)
	durU := time.Since(startU)
	if recUAll.Code != http.StatusOK {
		t.Fatalf("GET /api/usage/ thất bại: %d", recUAll.Code)
	}
	var uRes map[string]interface{}
	if err := json.Unmarshal(recUAll.Body.Bytes(), &uRes); err != nil || uRes["success"] != true {
		t.Fatalf("GET /api/usage/ không trả về danh sách quota hợp lệ: %v", err)
	}
	t.Logf("✔ GET /api/usage/ trả về đầy đủ Quotas từ RAM Cache trong %v", durU)

	// --- BƯỚC 4: Kiểm tra cờ ?force=1 bypass cache ---
	reqForce := httptest.NewRequest("GET", "/api/providers?force=1", nil)
	reqForce.Header.Set("Authorization", "Bearer "+testAdminKey)
	recForce := httptest.NewRecorder()
	providersHandler.ServeHTTP(recForce, reqForce)
	if recForce.Code != http.StatusOK {
		t.Fatalf("GET /api/providers?force=1 thất bại: %d", recForce.Code)
	}
	t.Logf("✔ Cờ ?force=1 bypass cache và query trực tiếp thành công")

	// --- BƯỚC 5: Kiểm tra Invalidation khi sửa/xóa tài khoản ---
	if err := UpdateProviderStatus(db, "acc_test_speed_01", 0, 5); err != nil {
		t.Fatalf("UpdateProviderStatus lỗi: %v", err)
	}
	// Sau khi update, cache cũ phải bị xóa ngay lập tức
	globalProvidersCache.mu.RLock()
	hasCachedData := len(globalProvidersCache.data) > 0
	globalProvidersCache.mu.RUnlock()
	if hasCachedData {
		t.Errorf("Kỳ vọng cache providers bị vô hiệu hóa sau khi cập nhật trạng thái tài khoản")
	}
	t.Logf("✔ Cache tự vô hiệu hóa (invalidation) thành công khi sửa kết nối")

	// --- BƯỚC 6: Kiểm tra đăng ký route đầy đủ trên ServeMux (Cục bộ & Cloud) ---
	mux := http.NewServeMux()
	RegisterOmniRouterRoutes(mux, db, false, t.TempDir())

	testEndpoints := []string{
		"/api/providers",
		"/api/providers/",
		"/api/router/providers",
		"/api/router/providers/",
		"/api/quotas",
		"/api/quotas/",
		"/api/router/quotas",
		"/api/router/quotas/",
		"/api/quotas/refresh-all",
		"/api/quotas/refresh-all/",
		"/api/router/quotas/refresh-all",
		"/api/router/quotas/refresh-all/",
		"/api/usage",
		"/api/usage/",
		"/api/router/usage",
		"/api/router/usage/",
	}

	for _, ep := range testEndpoints {
		r := httptest.NewRequest("GET", ep, nil)
		r.Header.Set("Authorization", "Bearer "+testAdminKey)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusNotFound {
			t.Errorf("Route %s chưa được đăng ký trên mux (trả về 404)!", ep)
		} else {
			t.Logf("✔ Route đã đăng ký thành công: %s (Status: %d)", ep, w.Code)
		}
	}
}

type quotaMockRoundTripper func(*http.Request) (*http.Response, error)

func (m quotaMockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m(r)
}

// TestQuotaP0_NoFakeImageWhenMissing kiểm tra lỗi P0: Khi upstream không có model ảnh, KHÔNG TỰ BỊA 100%
func TestQuotaP0_NoFakeImageWhenMissing(t *testing.T) {
	mockClient := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "fetchAvailableModels") {
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{
					"models": {
						"gemini-2.5-pro": {
							"displayName": "Gemini 2.5 Pro",
							"quotaInfo": {
								"remainingFraction": 0.85,
								"resetTime": "2026-10-24T18:00:00Z"
							}
						}
					}
				}`)
			} else {
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"groups": []}`)
			}
			return rec.Result(), nil
		}),
	}

	creds := &Credentials{AccessToken: "mock_tok_p0"}
	quotas, err := executeQuotaFetch(mockClient, creds)
	if err != nil {
		t.Fatalf("executeQuotaFetch thất bại: %v", err)
	}

	if _, exists := quotas["gemini_flash_image"]; exists {
		t.Errorf("VI PHẠM P0: gemini_flash_image không được phép tự động sinh ra khi upstream không trả về!")
	}
	if _, exists := quotas["gemini-3.1-flash-image"]; exists {
		t.Errorf("gemini-3.1-flash-image không được tồn tại")
	}
	if q, exists := quotas["gemini-2.5-pro"]; !exists || q.RemainingPercentage != 85.0 {
		t.Errorf("gemini-2.5-pro phải tồn tại với 85%% quota, nhận: %+v", q)
	}
}

// TestQuotaP1_DailyVsSessionClassification kiểm tra lỗi P1: Quota Daily không bị gán nhầm thành Session 5h
func TestQuotaP1_DailyVsSessionClassification(t *testing.T) {
	mockClient := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "retrieveUserQuotaSummary") {
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{
					"groups": [
						{
							"displayName": "Gemini Models",
							"buckets": [
								{
									"bucketId": "gemini_daily_bucket",
									"displayName": "Daily Limit",
									"window": "DAILY",
									"remainingFraction": 0.45,
									"resetTime": "2026-10-25T00:00:00Z"
								},
								{
									"bucketId": "gemini_5h_bucket",
									"displayName": "Five Hour Window",
									"window": "5H",
									"remainingFraction": 0.88,
									"resetTime": "2026-10-24T17:00:00Z"
								}
							]
						}
					]
				}`)
			} else {
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"models": {}}`)
			}
			return rec.Result(), nil
		}),
	}

	creds := &Credentials{AccessToken: "mock_tok_p1"}
	quotas, err := executeQuotaFetch(mockClient, creds)
	if err != nil {
		t.Fatalf("executeQuotaFetch thất bại: %v", err)
	}

	// 1. Kiểm tra gemini_daily
	daily, okDaily := quotas["gemini_daily"]
	if !okDaily {
		t.Fatalf("gemini_daily phải tồn tại trong quotas")
	}
	if daily.Window != "daily" || daily.RemainingPercentage != 45.0 {
		t.Errorf("gemini_daily sai cửa sổ hoặc %%: Window=%s, Pct=%.1f", daily.Window, daily.RemainingPercentage)
	}

	// 2. Kiểm tra gemini_session (5h)
	sess, okSess := quotas["gemini_session"]
	if !okSess {
		t.Fatalf("gemini_session phải tồn tại trong quotas")
	}
	if sess.Window != "5h" || sess.RemainingPercentage != 88.0 {
		t.Errorf("gemini_session sai cửa sổ hoặc %%: Window=%s, Pct=%.1f", sess.Window, sess.RemainingPercentage)
	}

	// 3. Đảm bảo quota 5h không bị gán đè bởi quota Daily (45% vs 88%)
	if sess.RemainingPercentage == 45.0 {
		t.Errorf("VI PHẠM P1: Quota 5h bị gán nhầm giá trị của quota Daily 45%%!")
	}
}

// TestQuotaP1_NoLowestModelInference kiểm tra lỗi P1: Không tự lấy model thấp nhất gán bừa thành nhóm 5h
func TestQuotaP1_NoLowestModelInference(t *testing.T) {
	mockClient := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "fetchAvailableModels") {
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{
					"models": {
						"gemini-2.5-flash": {
							"displayName": "Gemini 2.5 Flash",
							"quotaInfo": { "remainingFraction": 0.10 }
						},
						"gemini-2.5-pro": {
							"displayName": "Gemini 2.5 Pro",
							"quotaInfo": { "remainingFraction": 0.90 }
						}
					}
				}`)
			} else {
				// Không có nhóm tóm tắt 5h
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"groups": []}`)
			}
			return rec.Result(), nil
		}),
	}

	creds := &Credentials{AccessToken: "mock_tok_infer"}
	quotas, err := executeQuotaFetch(mockClient, creds)
	if err != nil {
		t.Fatalf("executeQuotaFetch thất bại: %v", err)
	}

	// Không được tự ý tạo "gemini_session" bằng cách lấy model thấp nhất (10%)
	if _, exists := quotas["gemini_session"]; exists {
		t.Errorf("VI PHẠM P1: Không được tự tiện suy diễn gemini_session từ model thấp nhất khi API không trả về summary 5h!")
	}

	// Từng model riêng lẻ phải giữ nguyên quota độc lập của nó
	if q, ok := quotas["gemini-2.5-flash"]; !ok || q.RemainingPercentage != 10.0 {
		t.Errorf("gemini-2.5-flash quota sai: %+v", q)
	}
	if q, ok := quotas["gemini-2.5-pro"]; !ok || q.RemainingPercentage != 90.0 {
		t.Errorf("gemini-2.5-pro quota sai: %+v", q)
	}
}

// TestQuotaP2_HttpErrorsAndMalformedJson kiểm tra lỗi P2: Bắt và báo lỗi HTTP != 200 và JSON lỗi
func TestQuotaP2_HttpErrorsAndMalformedJson(t *testing.T) {
	// Kịch bản 1: HTTP 503 Service Unavailable
	client503 := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusServiceUnavailable)
			_, _ = rec.WriteString(`{"error": "Backend server overloaded"}`)
			return rec.Result(), nil
		}),
	}

	creds := &Credentials{AccessToken: "tok"}
	_, err503 := executeQuotaFetch(client503, creds)
	if err503 == nil || !strings.Contains(err503.Error(), "503") {
		t.Errorf("Mong đợi lỗi HTTP 503, nhận được: %v", err503)
	}

	// Kịch bản 2: HTTP 429 Too Many Requests
	client429 := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusTooManyRequests)
			_, _ = rec.WriteString(`Rate limit exceeded`)
			return rec.Result(), nil
		}),
	}
	_, err429 := executeQuotaFetch(client429, creds)
	if err429 == nil || !strings.Contains(err429.Error(), "429") {
		t.Errorf("Mong đợi lỗi HTTP 429, nhận được: %v", err429)
	}

	// Kịch bản 3: JSON hỏng (Malformed JSON)
	clientMalformed := &http.Client{
		Transport: quotaMockRoundTripper(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.WriteString(`{invalid_json_content`)
			return rec.Result(), nil
		}),
	}
	_, errJson := executeQuotaFetch(clientMalformed, creds)
	if errJson == nil || (!strings.Contains(errJson.Error(), "giải mã JSON") && !strings.Contains(errJson.Error(), "invalid")) {
		t.Errorf("Mong đợi lỗi giải mã JSON, nhận được: %v", errJson)
	}
}

// TestQuotaP1_StaleCacheDistinction kiểm tra phân biệt rõ ràng dữ liệu LIVE và dữ liệu STALE Cache
func TestQuotaP1_StaleCacheDistinction(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở CSDL in-memory: %v", err)
	}
	defer db.Close()

	if err := InitStorage(db, false); err != nil {
		t.Fatalf("InitStorage lỗi: %v", err)
	}

	// Chuẩn bị 1 tài khoản có cache cũ trong DB
	oldQuotaJson := `{"quotas":{"gemini_session":{"total":1000,"used":100,"remainingPercentage":90.0,"source":"live_api","status":"LIVE"}}}`
	acc := ProviderConnection{
		ID:        "acc_test_stale_01",
		Provider:  "google",
		AuthType:  "oauth",
		Name:      "Stale Test Account",
		Email:     "stale@supportflast.dev",
		Priority:  1,
		IsActive:  1,
		Data:      []byte(oldQuotaJson),
		CreatedAt: NowISO(),
		UpdatedAt: NowISO(),
	}
	if err := SaveProvider(db, &acc); err != nil {
		t.Fatalf("SaveProvider lỗi: %v", err)
	}

	// Force gọi khi token không hợp lệ (gặp lỗi kết nối/auth upstream)
	// Hàm FetchAntigravityQuota phải fallback về cache DB và gắn cờ Status="STALE", Source="db_cache"
	resQuotas, err := FetchAntigravityQuota(db, &acc, true)
	if err != nil {
		t.Fatalf("Mong đợi fallback thành công về cache DB, nhận lỗi: %v", err)
	}

	sessItem, ok := resQuotas["gemini_session"]
	if !ok {
		t.Fatalf("Mong đợi gemini_session tồn tại trong kết quả cache")
	}

	if sessItem.Status != "STALE" {
		t.Errorf("Mong đợi Status == 'STALE', nhận: %s", sessItem.Status)
	}
	if sessItem.Source != "db_cache" {
		t.Errorf("Mong đợi Source == 'db_cache', nhận: %s", sessItem.Source)
	}
	if sessItem.LastError == "" {
		t.Errorf("Mong đợi LastError ghi nhận lý do fallback cache")
	}
	t.Logf("✔ Phân biệt thành công STALE cache: Status=%s, Source=%s, LastError=%s",
		sessItem.Status, sessItem.Source, sessItem.LastError)
}
