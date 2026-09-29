package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// TESTS TẦNG 2: XÁC THỰC & ĐĂNG NHẬP (RULE 8.2)
// ============================================================================

func TestLoginRateLimiter_IPLock(t *testing.T) {
	limiter := NewLoginRateLimiter(5, 15*time.Minute, 15*time.Minute)
	testIP := "203.0.113.195"
	user := "developer_alpha"

	// 4 lần đầu không bị khóa
	for i := 1; i <= 4; i++ {
		locked, remaining, left := limiter.RecordFailure(testIP, user)
		if locked {
			t.Fatalf("Attempt %d should not lock IP", i)
		}
		if remaining != 0 {
			t.Fatalf("Attempt %d should have remaining = 0", i)
		}
		expectedLeft := 5 - i
		if left != expectedLeft {
			t.Fatalf("Attempt %d expected %d attempts left, got %d", i, expectedLeft, left)
		}
	}

	// Lần thứ 5: Khóa 15 phút theo Rule 8.2
	locked, remaining, left := limiter.RecordFailure(testIP, user)
	if !locked {
		t.Fatalf("5th failed attempt MUST lock the IP")
	}
	if remaining <= 0 || remaining > 15*time.Minute {
		t.Fatalf("Expected lock duration around 15 minutes, got: %v", remaining)
	}
	if left != 0 {
		t.Fatalf("Expected 0 attempts left when locked, got %d", left)
	}

	// Kiểm tra qua IsLocked
	isLocked, rem, reason := limiter.IsLocked(testIP, "other_user")
	if !isLocked {
		t.Fatalf("IsLocked must return true for locked IP")
	}
	if rem <= 0 {
		t.Fatalf("Remaining lock time must be > 0, got %v", rem)
	}
	if !strings.Contains(reason, testIP) {
		t.Fatalf("Lock reason should contain IP, got: %s", reason)
	}
}

func TestLoginRateLimiter_UsernameLock(t *testing.T) {
	limiter := NewLoginRateLimiter(5, 15*time.Minute, 15*time.Minute)
	victimUser := "admin_targeted"

	// 5 IP khác nhau tấn công cùng 1 user (Distributed Brute-Force)
	for i := 1; i <= 4; i++ {
		ip := "198.51.100." + string(rune('0'+i))
		locked, _, _ := limiter.RecordFailure(ip, victimUser)
		if locked {
			t.Fatalf("Attempt %d should not have locked user yet", i)
		}
	}

	// Lần thứ 5 từ một IP khác
	locked, remaining, _ := limiter.RecordFailure("198.51.100.99", victimUser)
	if !locked {
		t.Fatalf("5th failed attempt on same username MUST lock the username")
	}
	if remaining <= 0 {
		t.Fatalf("Expected remaining lockout duration > 0")
	}

	// Kiểm tra username bị khóa kể cả từ một IP hoàn toàn mới chưa từng thử
	isLocked, _, reason := limiter.IsLocked("1.2.3.4", victimUser)
	if !isLocked {
		t.Fatalf("Victim user must be locked even from brand new IP")
	}
	if !strings.Contains(reason, victimUser) {
		t.Fatalf("Lock reason should mention username, got: %s", reason)
	}
}

func TestLoginRateLimiter_SuccessResets(t *testing.T) {
	limiter := NewLoginRateLimiter(5, 15*time.Minute, 15*time.Minute)
	ip := "192.0.2.55"
	user := "reset_test_user"

	// 4 lần sai
	for i := 1; i <= 4; i++ {
		limiter.RecordFailure(ip, user)
	}

	// Đăng nhập thành công -> Reset
	limiter.RecordSuccess(ip, user)

	// Lần sai tiếp theo phải bắt đầu lại từ 1 (còn 4 lần thử)
	locked, _, left := limiter.RecordFailure(ip, user)
	if locked {
		t.Fatalf("Should not be locked after reset")
	}
	if left != 4 {
		t.Fatalf("Expected 4 attempts left after reset and 1 failure, got %d", left)
	}
}

func TestLoginRateLimitMiddleware_BlocksWhenLocked(t *testing.T) {
	GlobalLoginRateLimiter.ResetAll()
	defer GlobalLoginRateLimiter.ResetAll()

	testIP := "198.51.100.77"
	testUser := "locked_account"

	// Giả lập 5 lần sai để khóa
	for i := 0; i < 5; i++ {
		GlobalLoginRateLimiter.RecordFailure(testIP, testUser)
	}

	nextCalled := false
	handler := LoginRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	reqBody := `{"username_or_email":"locked_account","password":"secret"}`
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(reqBody))
	req.RemoteAddr = testIP + ":45678"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Fatalf("Next handler should NOT be called when locked")
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Expected HTTP 429 Too Many Requests, got: %d", resp.StatusCode)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" || retryAfter == "0" {
		t.Fatalf("Expected non-zero Retry-After header, got: %s", retryAfter)
	}

	var jsonResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}

	if jsonResp["status"] != "locked" {
		t.Errorf("Expected status='locked', got: %v", jsonResp["status"])
	}
	if jsonResp["code"] != "ERR_ACCOUNT_LOCKED" {
		t.Errorf("Expected code='ERR_ACCOUNT_LOCKED', got: %v", jsonResp["code"])
	}
}

// ============================================================================
// TESTS TẦNG 1: GIỚI HẠN TOÀN CỤC (120 REQUESTS/PHÚT)
// ============================================================================

func TestSlidingWindowLimiter_Tier1Global(t *testing.T) {
	limiter := NewSlidingWindowLimiter(120, time.Minute)
	testIP := "203.0.113.10"

	// 120 requests đầu tiên phải thành công
	for i := 1; i <= 120; i++ {
		allowed, remaining, retryAfter := limiter.Allow(testIP)
		if !allowed {
			t.Fatalf("Request %d must be allowed, but was blocked", i)
		}
		expectedRemaining := 120 - i
		if remaining != expectedRemaining {
			t.Fatalf("Request %d expected %d remaining, got %d", i, expectedRemaining, remaining)
		}
		if retryAfter != 0 {
			t.Fatalf("Request %d expected retryAfter=0, got %v", i, retryAfter)
		}
	}

	// Request thứ 121 phải bị từ chối
	allowed, remaining, retryAfter := limiter.Allow(testIP)
	if allowed {
		t.Fatalf("Request 121 MUST be rejected by Tier 1 rate limiter")
	}
	if remaining != 0 {
		t.Fatalf("Expected remaining=0 when blocked, got %d", remaining)
	}
	if retryAfter <= 0 {
		t.Fatalf("Expected retryAfter > 0 when blocked, got %v", retryAfter)
	}

	// Reset lại IP -> Request tiếp theo phải thành công
	limiter.Reset(testIP)
	allowedAfterReset, remAfterReset, _ := limiter.Allow(testIP)
	if !allowedAfterReset || remAfterReset != 119 {
		t.Fatalf("Expected request to succeed after reset, allowed=%v, remaining=%d", allowedAfterReset, remAfterReset)
	}
}

// ============================================================================
// TESTS TẦNG 3: GIỚI HẠN QUẢN TRỊ & THAO TÁC NGUY HIỂM (30 REQUESTS/PHÚT)
// ============================================================================

func TestSlidingWindowLimiter_Tier3Admin(t *testing.T) {
	limiter := NewSlidingWindowLimiter(30, time.Minute)
	testIP := "198.51.100.22"

	// 30 requests đầu tiên phải thành công
	for i := 1; i <= 30; i++ {
		allowed, remaining, retryAfter := limiter.Allow(testIP)
		if !allowed {
			t.Fatalf("Admin request %d must be allowed, got blocked", i)
		}
		expectedRemaining := 30 - i
		if remaining != expectedRemaining {
			t.Fatalf("Admin request %d expected %d remaining, got %d", i, expectedRemaining, remaining)
		}
		if retryAfter != 0 {
			t.Fatalf("Admin request %d expected retryAfter=0, got %v", i, retryAfter)
		}
	}

	// Request thứ 31 phải bị chặn
	allowed, remaining, retryAfter := limiter.Allow(testIP)
	if allowed {
		t.Fatalf("Admin request 31 MUST be rejected by Tier 3 rate limiter")
	}
	if remaining != 0 {
		t.Fatalf("Expected remaining=0 when blocked, got %d", remaining)
	}
	if retryAfter <= 0 {
		t.Fatalf("Expected retryAfter > 0 when blocked, got %v", retryAfter)
	}
}

// ============================================================================
// TESTS MULTI-TIER RATE LIMITER TÍCH HỢP & MIDDLEWARE
// ============================================================================

func TestMultiTierRateLimiter_Routes(t *testing.T) {
	adminRoutes := []string{
		"/api/admin/users",
		"/api/admin/system/update",
		"/api/keys/generate",
		"/api/apps/publish",
		"/api/system/maintenance",
		"/api/device/sync",
	}
	for _, r := range adminRoutes {
		if !IsAdminSensitiveRoute(r) {
			t.Errorf("Route '%s' should be detected as Admin/Sensitive", r)
		}
	}

	normalRoutes := []string{
		"/api/health",
		"/api/apps",
		"/api/apps/download/123",
		"/api/reviews",
		"/api/agents",
	}
	for _, r := range normalRoutes {
		if IsAdminSensitiveRoute(r) {
			t.Errorf("Route '%s' should NOT be detected as Admin/Sensitive", r)
		}
	}
}

func TestMultiTierRateLimitMiddleware_Tier1GlobalLimit(t *testing.T) {
	GlobalMultiTierLimiter.ResetAll()
	defer GlobalMultiTierLimiter.ResetAll()

	oldMax := GlobalMultiTierLimiter.tier1Global.maxRequests
	GlobalMultiTierLimiter.tier1Global.maxRequests = 120
	defer func() { GlobalMultiTierLimiter.tier1Global.maxRequests = oldMax }()

	testIP := "203.0.113.88"
	handler := MultiTierRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// Gửi 120 requests tới /api/apps thành công
	for i := 0; i < 120; i++ {
		req := httptest.NewRequest("GET", "/api/apps", nil)
		req.RemoteAddr = testIP + ":12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Request %d expected 200 OK, got: %d", i+1, rec.Code)
		}
	}

	// Request thứ 121 tới /api/apps phải nhận HTTP 429
	req := httptest.NewRequest("GET", "/api/apps", nil)
	req.RemoteAddr = testIP + ":12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("Request 121 expected 429 Too Many Requests, got: %d", rec.Code)
	}

	tier := rec.Header().Get("X-RateLimit-Tier")
	if tier != "1-Global" {
		t.Errorf("Expected X-RateLimit-Tier='1-Global', got: %s", tier)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if body["code"] != "ERR_GLOBAL_RATE_LIMITED" {
		t.Errorf("Expected code='ERR_GLOBAL_RATE_LIMITED', got: %v", body["code"])
	}
}

func TestMultiTierRateLimitMiddleware_Tier3AdminLimit(t *testing.T) {
	GlobalMultiTierLimiter.ResetAll()
	defer GlobalMultiTierLimiter.ResetAll()

	testIP := "203.0.113.99"
	handler := MultiTierRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admin ok"))
	}))

	// Gửi 30 requests tới /api/admin/users thành công
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.RemoteAddr = testIP + ":23456"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Admin request %d expected 200 OK, got: %d", i+1, rec.Code)
		}
	}

	// Request thứ 31 tới /api/admin/users phải nhận HTTP 429 Tier 3
	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.RemoteAddr = testIP + ":23456"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("Admin request 31 expected 429 Too Many Requests, got: %d", rec.Code)
	}

	tier := rec.Header().Get("X-RateLimit-Tier")
	if tier != "3-Admin" {
		t.Errorf("Expected X-RateLimit-Tier='3-Admin', got: %s", tier)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if body["code"] != "ERR_ADMIN_RATE_LIMITED" {
		t.Errorf("Expected code='ERR_ADMIN_RATE_LIMITED', got: %v", body["code"])
	}
}

func TestMultiTierRateLimitMiddleware_WhitelistBypass(t *testing.T) {
	GlobalMultiTierLimiter.ResetAll()
	defer GlobalMultiTierLimiter.ResetAll()

	trustedIP := "127.0.0.1" // Thuộc Whitelist mặc định
	handler := MultiTierRateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Gửi 150 requests (vượt 120 của Tier 1 và 30 của Tier 3)
	for i := 0; i < 150; i++ {
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.RemoteAddr = trustedIP + ":1111"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Whitelisted IP request %d was blocked with status %d", i+1, rec.Code)
		}
	}
}
