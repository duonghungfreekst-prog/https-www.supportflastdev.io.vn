package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestIPJail_Whitelist(t *testing.T) {
	jail := NewIPJail(100)

	// Các IP Whitelist mặc định
	defaultIPs := []string{
		"127.0.0.1",
		"::1",
		"localhost",
		"0.0.0.0",
		"10.0.1.5",
		"192.168.1.100",
		"172.16.0.2",
	}

	for _, ip := range defaultIPs {
		if !jail.IsWhitelisted(ip) {
			t.Errorf("IP %s should be whitelisted by default", ip)
		}

		// Thử đưa vào Jail -> Phải bị từ chối
		jailed, _ := jail.Jail(ip, 1*time.Hour, "Test attack", SeverityHigh)
		if jailed {
			t.Errorf("Whitelisted IP %s must NEVER be jailed", ip)
		}

		isJailed, _, _ := jail.IsJailed(ip)
		if isJailed {
			t.Errorf("Whitelisted IP %s should never report as jailed", ip)
		}
	}

	// Thêm IP mới vào Whitelist
	customIP := "203.0.113.50"
	jail.AddWhitelist(customIP)
	if !jail.IsWhitelisted(customIP) {
		t.Errorf("Custom IP %s should be whitelisted after AddWhitelist", customIP)
	}

	// Gỡ khỏi Whitelist
	jail.RemoveWhitelist(customIP)
	if jail.IsWhitelisted(customIP) {
		t.Errorf("Custom IP %s should NOT be whitelisted after RemoveWhitelist", customIP)
	}
}

func TestIPJail_ManualJailAndUnjail(t *testing.T) {
	jail := NewIPJail(100)
	testIP := "198.51.100.15"

	// 1. Jail trong 15 phút (SeverityLow)
	jailed, dur := jail.Jail(testIP, JailDuration15M, "Rate limit spam", SeverityLow)
	if !jailed || dur != JailDuration15M {
		t.Fatalf("Failed to jail IP for 15 minutes")
	}

	isJailed, remaining, reason := jail.IsJailed(testIP)
	if !isJailed {
		t.Fatalf("IP %s should be jailed", testIP)
	}
	if remaining <= 0 || remaining > JailDuration15M {
		t.Fatalf("Remaining duration should be <= 15m, got: %v", remaining)
	}
	if reason != "Rate limit spam" {
		t.Errorf("Expected reason 'Rate limit spam', got: '%s'", reason)
	}

	// 2. Unjail
	unjailed := jail.Unjail(testIP)
	if !unjailed {
		t.Fatalf("Unjail should return true for existing jailed IP")
	}

	isJailedAfter, _, _ := jail.IsJailed(testIP)
	if isJailedAfter {
		t.Fatalf("IP %s should no longer be jailed after Unjail", testIP)
	}
}

func TestIPJail_AutoEscalation(t *testing.T) {
	jail := NewIPJail(100)
	attackerIP := "198.51.100.88"

	// Lần vi phạm 1: SeverityLow -> 15 phút
	_, dur1 := jail.RecordAttack(attackerIP, "First violation", SeverityLow)
	if dur1 != JailDuration15M {
		t.Errorf("1st offense expected 15m, got %v", dur1)
	}

	// Unjail để giả lập hết hạn
	jail.Unjail(attackerIP)

	// Lần vi phạm 2: Tự động leo thang lên 1 giờ
	_, dur2 := jail.RecordAttack(attackerIP, "Second violation", SeverityLow)
	if dur2 != JailDuration1H {
		t.Errorf("2nd offense expected escalation to 1h, got %v", dur2)
	}

	jail.Unjail(attackerIP)

	// Lần vi phạm 3: Tự động leo thang lên 24 giờ
	_, dur3 := jail.RecordAttack(attackerIP, "Third violation", SeverityLow)
	if dur3 != JailDuration24H {
		t.Errorf("3rd offense expected escalation to 24h, got %v", dur3)
	}

	// Vi phạm với SeverityHigh ngay từ đầu -> Trực tiếp 24 giờ
	criticalIP := "198.51.100.99"
	_, durHigh := jail.RecordAttack(criticalIP, "SQL Injection attempt", SeverityHigh)
	if durHigh != JailDuration24H {
		t.Errorf("SeverityHigh offense expected 24h immediately, got %v", durHigh)
	}
}

func TestIPJail_DetectVulnerabilityScan(t *testing.T) {
	scanPaths := []string{
		"/.env",
		"/.git/config",
		"/wp-admin/index.php",
		"/phpmyadmin/",
		"/etc/passwd",
		"/api/../../etc/shadow",
		"/shell.php",
		"/actuator/health",
	}

	for _, p := range scanPaths {
		isScan, reason := DetectVulnerabilityScan(p)
		if !isScan {
			t.Errorf("Path '%s' should be detected as vulnerability scanning", p)
		}
		if reason == "" {
			t.Errorf("Path '%s' should return a detection reason", p)
		}
	}

	cleanPaths := []string{
		"/",
		"/api/health",
		"/api/apps",
		"/api/apps/publish",
		"/api/auth/login",
		"/tools",
		"/storage",
	}

	for _, p := range cleanPaths {
		isScan, _ := DetectVulnerabilityScan(p)
		if isScan {
			t.Errorf("Normal path '%s' should NOT be detected as scanning", p)
		}
	}
}

func TestIPJail_DetectMaliciousPayload(t *testing.T) {
	// 1. Request có chứa SQL Injection trong query
	req1 := httptest.NewRequest("GET", "/api/apps?search=1%20union%20select%20*%20from%20users", nil)
	isAttack, reason, severity := DetectMaliciousRequest(req1)
	if !isAttack {
		t.Errorf("SQLi query should be detected as attack")
	}
	if severity != SeverityHigh {
		t.Errorf("SQLi should have SeverityHigh, got: %v", severity)
	}
	if reason == "" {
		t.Errorf("Expected reason for SQLi attack")
	}

	// 2. Request có chứa XSS trong query
	req2 := httptest.NewRequest("GET", "/api/apps?q=<script>alert(1)</script>", nil)
	isAttack2, _, _ := DetectMaliciousRequest(req2)
	if !isAttack2 {
		t.Errorf("XSS query should be detected as attack")
	}

	// 3. Request bình thường
	req3 := httptest.NewRequest("GET", "/api/apps?search=calculator&page=1", nil)
	isAttack3, _, _ := DetectMaliciousRequest(req3)
	if isAttack3 {
		t.Errorf("Clean query should NOT be detected as attack")
	}
}

func TestIPJailMiddleware_BlocksJailedIP(t *testing.T) {
	GlobalIPJail.ClearJail()
	defer GlobalIPJail.ClearJail()

	jailedIP := "203.0.113.77"
	GlobalIPJail.Jail(jailedIP, 15*time.Minute, "Manual test ban", SeverityLow)

	nextCalled := false
	handler := IPJailMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/apps", nil)
	req.RemoteAddr = jailedIP + ":34567"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatalf("Handler should NOT be called for jailed IP")
	}

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403 Forbidden, got: %d", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" || retryAfter == "0" {
		t.Errorf("Expected Retry-After header for jailed IP, got: '%s'", retryAfter)
	}

	var jsonResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&jsonResp); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if jsonResp["code"] != "ERR_IP_BANNED" {
		t.Errorf("Expected code='ERR_IP_BANNED', got: %v", jsonResp["code"])
	}
}

func TestIPJailMiddleware_AutoJailOnVulnerabilityScan(t *testing.T) {
	GlobalIPJail.ClearJail()
	defer GlobalIPJail.ClearJail()

	scannerIP := "198.51.100.66"

	handler := IPJailMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Gửi request cố tình quét file nhạy cảm /.env
	req := httptest.NewRequest("GET", "/.env", nil)
	req.RemoteAddr = scannerIP + ":54321"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// 1. Phải bị chặn với HTTP 403 Forbidden ngay lập tức
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403 Forbidden for scanning /.env, got: %d", rec.Code)
	}

	// 2. IP phải tự động bị đưa vào danh sách Blacklist của IP Jail
	isJailed, _, _ := GlobalIPJail.IsJailed(scannerIP)
	if !isJailed {
		t.Fatalf("IP %s must be automatically jailed after scanning /.env", scannerIP)
	}

	// 3. Request tiếp theo kể cả vào trang hợp lệ /api/health cũng phải bị chặn 403
	req2 := httptest.NewRequest("GET", "/api/health", nil)
	req2.RemoteAddr = scannerIP + ":54321"
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("Subsequent requests from jailed IP must be blocked with 403 Forbidden, got: %d", rec2.Code)
	}
}

func TestIPJail_Concurrency(t *testing.T) {
	jail := NewIPJail(1000)
	var wg sync.WaitGroup

	// 20 goroutines thực hiện đồng thời các tác vụ đọc/ghi
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ip := "192.0.2." + string(rune('1'+(workerID%9)))
			jail.RecordAttack(ip, "Concurrent test", SeverityLow)
			jail.IsJailed(ip)
			jail.GetJailList()
			if workerID%2 == 0 {
				jail.Unjail(ip)
			}
		}(i)
	}

	wg.Wait()
}
