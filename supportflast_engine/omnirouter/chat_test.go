package omnirouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestChatCompletionsTask9Validation kiểm tra các yêu cầu của Task 9:
// 1. Giới hạn body 10MB bằng http.MaxBytesReader
// 2. Bắt lỗi JSON không hợp lệ -> trả 400 {"error":"Dữ liệu JSON không hợp lệ"}
// 3. Thiếu messages -> trả 400
// 4. Toàn bộ tài khoản cooldown -> trả 503 Service Unavailable kèm Retry-After, KHÔNG reset bừa bãi
// 5. Kiểm tra tính năng gom chunks cho non-streaming request
func TestChatCompletionsTask9Validation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	handler := HandleChatCompletions(db)

	validKey, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Task9-Key", "supportflastdev.io.vn", 100, 24)
	if err != nil {
		t.Fatalf("Tạo valid key lỗi: %v", err)
	}

	// 1. Kiểm tra request JSON không hợp lệ -> Bắt buộc trả 400 với {"error":"Dữ liệu JSON không hợp lệ"}
	t.Run("Invalid JSON Syntax -> 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model": "gemini-3.8-flash-high", "messages":`))
		req.Header.Set("Authorization", "Bearer "+validKey.Key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Kỳ vọng 400 khi JSON sai cú pháp, nhận được: %d", rec.Code)
		}
		var errResp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("Không thể parse response lỗi: %v", err)
		}
		errMsg, _ := errResp["error"].(string)
		if errMsg != "Dữ liệu JSON không hợp lệ" {
			t.Errorf("Kỳ vọng error 'Dữ liệu JSON không hợp lệ', nhận: %v", errResp["error"])
		}
	})

	// 2. Kiểm tra giới hạn kích thước Body > 10MB bằng http.MaxBytesReader
	t.Run("Payload Exceeding 10MB -> 400", func(t *testing.T) {
		bigBody := strings.NewReader(`{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"` + strings.Repeat("A", 11*1024*1024) + `"}]}`)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bigBody)
		req.Header.Set("Authorization", "Bearer "+validKey.Key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Kỳ vọng 400 khi body vượt quá 10MB, nhận được: %d", rec.Code)
		}
	})

	// 3. Kiểm tra missing messages -> 400
	t.Run("Missing Messages -> 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.8-flash-high","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+validKey.Key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Kỳ vọng 400 khi messages rỗng, nhận được: %d", rec.Code)
		}
	})

	// 4. Kiểm tra Cooldown: Khi toàn bộ tài khoản bị cooldown -> 503 Service Unavailable kèm Retry-After
	t.Run("All Accounts in Cooldown -> 503", func(t *testing.T) {
		providerCacheMu.Lock()
		originalCached := providerCache
		dummyAccounts := []ProviderConnection{
			{ID: "acc_cool_1", Name: "Cool Account 1", Provider: "antigravity", IsActive: 1},
			{ID: "acc_cool_2", Name: "Cool Account 2", Provider: "antigravity", IsActive: 1},
		}
		providerCache = dummyAccounts
		providerCacheMu.Unlock()

		defer func() {
			providerCacheMu.Lock()
			providerCache = originalCached
			providerCacheMu.Unlock()
		}()

		coolDuration := 30 * time.Second
		accountCooldownMap.Store("acc_cool_1", time.Now().Add(coolDuration))
		accountCooldownMap.Store("acc_cool_2", time.Now().Add(coolDuration))
		defer func() {
			accountCooldownMap.Delete("acc_cool_1")
			accountCooldownMap.Delete("acc_cool_2")
		}()

		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"test"}]}`))
		req.Header.Set("Authorization", "Bearer "+validKey.Key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Kỳ vọng 503 Service Unavailable khi tất cả tài khoản trong cooldown, nhận: %d", rec.Code)
		}
		retryAfter := rec.Header().Get("Retry-After")
		if retryAfter == "" || retryAfter == "0" {
			t.Errorf("Kỳ vọng Retry-After header hợp lệ, nhận: %s", retryAfter)
		}

		var respObj map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &respObj)
		errMap, ok := respObj["error"].(map[string]interface{})
		if !ok || errMap["code"] != "all_accounts_in_cooldown" {
			t.Errorf("Kỳ vọng error.code = 'all_accounts_in_cooldown', nhận: %v", respObj)
		}
	})

	// 5. Kiểm tra tính năng gom chunks cho non-streaming request
	t.Run("ParseNonStreamingResponse Aggregates Chunks", func(t *testing.T) {
		sseChunks := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Xin \"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2}}}\n\n" +
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"chào!\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5}}}\n\n" +
			"data: [DONE]\n\n"

		fullText, pTok, cTok, err := ParseNonStreamingResponse("antigravity", []byte(sseChunks))
		if err != nil {
			t.Fatalf("ParseNonStreamingResponse lỗi: %v", err)
		}
		if fullText != "Xin chào!" {
			t.Errorf("Kỳ vọng 'Xin chào!', nhận: %q", fullText)
		}
		if pTok != 10 || cTok != 5 {
			t.Errorf("Kỳ vọng prompt=10, completion=5, nhận: prompt=%d, completion=%d", pTok, cTok)
		}
	})
}

// TestQuotaReleaseOnUpstreamFailure kiểm tra P0: Quota reservation phải được hoàn trả nếu request thất bại trước khi upstream xử lý
func TestQuotaReleaseOnUpstreamFailure(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	handler := HandleChatCompletions(db)

	key, err := CreateNewClientKeyWithQuotaAndExpiry(db, "Test-Release-Quota", "supportflastdev.io.vn", 10, 24)
	if err != nil {
		t.Fatalf("Tạo key lỗi: %v", err)
	}

	// 1. Kiểm tra ban đầu: used_requests = 0
	var initialUsed int64
	_ = db.QueryRow("SELECT used_requests FROM omni_client_keys WHERE id = ?", key.ID).Scan(&initialUsed)
	if initialUsed != 0 {
		t.Fatalf("Kỳ vọng ban đầu used_requests = 0, thực tế: %d", initialUsed)
	}

	// 2. Không có tài khoản nào khả dụng (providerCache rỗng) -> Handler trả 503
	providerCacheMu.Lock()
	origCache := providerCache
	providerCache = []ProviderConnection{}
	providerCacheMu.Unlock()

	defer func() {
		providerCacheMu.Lock()
		providerCache = origCache
		providerCacheMu.Unlock()
	}()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Kỳ vọng 503, nhận được: %d", rec.Code)
	}

	// 3. Sau khi bị 503 do không có tài khoản, quota BẮT BUỘC phải được hoàn trả -> used_requests = 0
	var usedAfterNoAccount int64
	_ = db.QueryRow("SELECT used_requests FROM omni_client_keys WHERE id = ?", key.ID).Scan(&usedAfterNoAccount)
	if usedAfterNoAccount != 0 {
		t.Errorf("P0 VIOLATION: Quota reservation bị trừ oan (%d) dù không có tài khoản nào khả dụng!", usedAfterNoAccount)
	}

	// 4. Kiểm tra trong RAM Cache cũng phải được hoàn trả -> UsedRequests = 0
	keyCacheMu.RLock()
	memItem := keyCache[key.Key]
	keyCacheMu.RUnlock()
	if memItem != nil && memItem.UsedRequests != 0 {
		t.Errorf("P0 VIOLATION: RAM Cache UsedRequests bị trừ oan (%d) dù request 503!", memItem.UsedRequests)
	}
}

// TestHandleCacheStats_RealMetrics kiểm tra P1: HandleCacheStats trả về metrics thật 100% thay vì số liệu mẫu cố định
func TestHandleCacheStats_RealMetrics(t *testing.T) {
	// Reset metrics về trạng thái ban đầu
	cacheEntriesCount.Store(0)
	cacheHitCount.Store(0)
	cacheMissCount.Store(0)
	cacheTokensSaved.Store(0)

	req := httptest.NewRequest("GET", "/api/cache/stats", nil)
	rec := httptest.NewRecorder()
	HandleCacheStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng 200, nhận: %d", rec.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Parse JSON lỗi: %v", err)
	}

	// Xác nhận không còn các giá trị hardcoded cũ (128, 342, 680, 154200)
	entries := int64(stats["entries"].(float64))
	hits := int64(stats["hitCount"].(float64))
	tokens := int64(stats["tokensSaved"].(float64))

	if entries == 128 && hits == 342 && tokens == 154200 {
		t.Errorf("P1 VIOLATION: HandleCacheStats vẫn trả về số liệu mẫu cứng giả lập!")
	}

	// Ghi nhận sự kiện thật và kiểm tra cập nhật động
	RecordCacheHit(1500)
	RecordCacheMiss()
	UpdateCacheEntries(42)

	rec2 := httptest.NewRecorder()
	HandleCacheStats(rec2, req)
	var stats2 map[string]interface{}
	_ = json.Unmarshal(rec2.Body.Bytes(), &stats2)

	if int64(stats2["hitCount"].(float64)) != 1 {
		t.Errorf("Kỳ vọng hitCount = 1, nhận: %v", stats2["hitCount"])
	}
	if int64(stats2["tokensSaved"].(float64)) != 1500 {
		t.Errorf("Kỳ vọng tokensSaved = 1500, nhận: %v", stats2["tokensSaved"])
	}
	if int64(stats2["entries"].(float64)) != 42 {
		t.Errorf("Kỳ vọng entries = 42, nhận: %v", stats2["entries"])
	}
}

// TestDetermineProvider_FourFreeProviders kiểm tra nhận diện chính xác 4 provider mới (OpenRouter, Groq, Mistral, Cloudflare)
func TestDetermineProvider_FourFreeProviders(t *testing.T) {
	tests := []struct {
		accProvider  string
		model        string
		wantProvider string
	}{
		// Theo Connection Provider
		{"openrouter", "openrouter/free", "openrouter"},
		{"groq", "llama-3.3-70b-versatile", "groq"},
		{"mistral", "mistral-small-latest", "mistral"},
		{"cloudflare", "@cf/meta/llama-3.1-8b-instruct", "cloudflare"},
		{"cloudflare-ai", "cf-model", "cloudflare"},
		{"workers-ai", "cf-model", "cloudflare"},

		// Fallback theo Model Prefix
		{"", "openrouter/free", "openrouter"},
		{"", "groq/llama-3.3-70b", "groq"},
		{"", "mistral/mistral-large", "mistral"},
		{"", "mistralai/mistral-7b", "mistral"},
		{"", "cloudflare/@cf/meta/llama-3.1-8b-instruct", "cloudflare"},
		{"", "@cf/meta/llama-3.1-8b-instruct", "cloudflare"},
	}

	for _, tt := range tests {
		var acc *ProviderConnection
		if tt.accProvider != "" {
			acc = &ProviderConnection{Provider: tt.accProvider}
		}
		got := DetermineProvider(acc, tt.model)
		if got != tt.wantProvider {
			t.Errorf("DetermineProvider(acc=%s, model=%s) = %s, kỳ vọng: %s", tt.accProvider, tt.model, got, tt.wantProvider)
		}
	}
}

// TestBuildOutboundRequest_FourFreeProviders kiểm tra dựng outbound request chuẩn OpenAI-compatible cho 4 provider
func TestBuildOutboundRequest_FourFreeProviders(t *testing.T) {
	ctx := context.Background()
	req := ChatCompletionRequest{
		Model: "test-model",
		Messages: []map[string]interface{}{
			{"role": "user", "content": "Xin chào SupportFlast"},
		},
	}

	// 1. OpenRouter
	{
		creds := &Credentials{AccessToken: "sk-or-v1-testkey123"}
		fReq, err := BuildOutboundRequest(ctx, "openrouter", "openrouter/free", creds, req, false)
		if err != nil {
			t.Fatalf("BuildOutboundRequest openrouter thất bại: %v", err)
		}
		if fReq.URL.String() != "https://openrouter.ai/api/v1/chat/completions" {
			t.Errorf("OpenRouter URL không đúng: %s", fReq.URL.String())
		}
		if fReq.Header.Get("Authorization") != "Bearer sk-or-v1-testkey123" {
			t.Errorf("OpenRouter Auth Header sai: %s", fReq.Header.Get("Authorization"))
		}
		if fReq.Header.Get("HTTP-Referer") != "https://supportflastdev.io.vn" {
			t.Errorf("OpenRouter HTTP-Referer thiếu")
		}
	}

	// 2. Groq
	{
		creds := &Credentials{AccessToken: "gsk_testgroqkey456"}
		fReq, err := BuildOutboundRequest(ctx, "groq", "groq/llama-3.3-70b-versatile", creds, req, true)
		if err != nil {
			t.Fatalf("BuildOutboundRequest groq thất bại: %v", err)
		}
		if fReq.URL.String() != "https://api.groq.com/openai/v1/chat/completions" {
			t.Errorf("Groq URL không đúng: %s", fReq.URL.String())
		}
		if fReq.Header.Get("Authorization") != "Bearer gsk_testgroqkey456" {
			t.Errorf("Groq Auth Header sai: %s", fReq.Header.Get("Authorization"))
		}
	}

	// 3. Mistral
	{
		creds := &Credentials{AccessToken: "mis_testkey789"}
		fReq, err := BuildOutboundRequest(ctx, "mistral", "mistral/mistral-small-latest", creds, req, false)
		if err != nil {
			t.Fatalf("BuildOutboundRequest mistral thất bại: %v", err)
		}
		if fReq.URL.String() != "https://api.mistral.ai/v1/chat/completions" {
			t.Errorf("Mistral URL không đúng: %s", fReq.URL.String())
		}
		if fReq.Header.Get("Authorization") != "Bearer mis_testkey789" {
			t.Errorf("Mistral Auth Header sai: %s", fReq.Header.Get("Authorization"))
		}
	}

	// 4. Cloudflare
	{
		creds := &Credentials{AccessToken: "cf_token_secret", ProjectId: "acc_cf_12345"}
		fReq, err := BuildOutboundRequest(ctx, "cloudflare", "cloudflare/@cf/meta/llama-3.1-8b-instruct", creds, req, false)
		if err != nil {
			t.Fatalf("BuildOutboundRequest cloudflare thất bại: %v", err)
		}
		expectedURL := "https://api.cloudflare.com/client/v4/accounts/acc_cf_12345/ai/v1/chat/completions"
		if fReq.URL.String() != expectedURL {
			t.Errorf("Cloudflare URL không đúng: nhận %s, kỳ vọng %s", fReq.URL.String(), expectedURL)
		}
		if fReq.Header.Get("Authorization") != "Bearer cf_token_secret" {
			t.Errorf("Cloudflare Auth Header sai: %s", fReq.Header.Get("Authorization"))
		}
	}
}

// TestSeedEnvProviders kiểm tra tự động nạp cấu hình Provider từ biến môi trường
func TestSeedEnvProviders(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	os.Setenv("OPENROUTER_API_KEY", "sk-or-test-mock-key")
	os.Setenv("GROQ_API_KEY", "gsk-test-mock-key")
	defer os.Unsetenv("OPENROUTER_API_KEY")
	defer os.Unsetenv("GROQ_API_KEY")

	SeedEnvProviders(db)

	var orCount, groqCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM omni_providers WHERE id = 'prov_openrouter_env'").Scan(&orCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM omni_providers WHERE id = 'prov_groq_env'").Scan(&groqCount)

	if orCount != 1 {
		t.Errorf("Kỳ vọng 1 provider OpenRouter được nạp từ env, nhận: %d", orCount)
	}
	if groqCount != 1 {
		t.Errorf("Kỳ vọng 1 provider Groq được nạp từ env, nhận: %d", groqCount)
	}
}
