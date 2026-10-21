package omnirouter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
