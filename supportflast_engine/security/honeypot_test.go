package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() {
	// Giảm delay về 0 trong quá trình chạy test để test chạy nhanh chóng
	SetHoneypotDelay(0)
}

func TestHoneypot_DecoyTrapsTriggerAndJail(t *testing.T) {
	GlobalIPJail.ClearJail()
	GlobalWAFAlarmManager.Reset()

	routesToTest := []struct {
		path string
		ip   string
	}{
		{"/.env", "198.51.100.1"},
		{"/wp-admin", "198.51.100.2"},
		{"/phpmyadmin", "198.51.100.3"},
		{"/api/admin/shell", "198.51.100.4"},
		{"/.git/config", "198.51.100.5"},
		{"/config.json", "198.51.100.6"},
		{"/actuator/health", "198.51.100.7"},
	}

	for _, tc := range routesToTest {
		t.Run("Decoy_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.RemoteAddr = tc.ip + ":54321"
			req.Header.Set("User-Agent", "Mozilla/5.0 Scanner-Bot/1.0")
			w := httptest.NewRecorder()

			// Gọi trực tiếp handler bẫy nhử
			HoneypotTrapHandler(w, req)

			resp := w.Result()
			// Phản hồi giả lập 404 không làm lộ danh tính Honeypot (Rule 3.5)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("Expected 404 Not Found, got %d for path %s", resp.StatusCode, tc.path)
			}

			body := w.Body.String()
			if strings.Contains(strings.ToLower(body), "honeypot") || strings.Contains(strings.ToLower(body), "trap") {
				t.Fatalf("Response body must NOT leak honeypot deception: %s", body)
			}

			// Kiểm tra IP đã bị giam giữ trong IP Jail
			isJailed, rem, reason := GlobalIPJail.IsJailed(tc.ip)
			if !isJailed {
				t.Fatalf("IP %s must be jailed immediately after hitting %s", tc.ip, tc.path)
			}
			if !strings.Contains(reason, tc.path) {
				t.Errorf("Expected reason to mention trap path %s, got: %s", tc.path, reason)
			}

			// Kiểm tra thời gian khóa phải là 24 giờ (xê xích không quá 1 phút)
			if rem < 23*time.Hour+59*time.Minute || rem > 24*time.Hour {
				t.Errorf("Lockout duration should be ~24 hours, got: %v", rem)
			}
		})
	}

	// Xác nhận cơ chế báo động WAF đã được kích hoạt cho toàn bộ các bẫy
	alerts := GlobalWAFAlarmManager.GetAlerts()
	if len(alerts) != len(routesToTest) {
		t.Fatalf("Expected %d WAF alerts, got %d", len(routesToTest), len(alerts))
	}

	for _, alert := range alerts {
		if alert.Severity != "CRITICAL" {
			t.Errorf("WAF alert severity should be CRITICAL, got %s", alert.Severity)
		}
		if alert.Action != "IP_JAILED_24H" {
			t.Errorf("WAF alert action should be IP_JAILED_24H, got %s", alert.Action)
		}
	}
}

func TestHoneypot_IPJailMiddlewareBlocksSubsequentRequests(t *testing.T) {
	GlobalIPJail.ClearJail()
	GlobalWAFAlarmManager.Reset()

	mux := http.NewServeMux()
	RegisterHoneypotRoutes(mux)

	// Route bình thường của hệ thống
	mux.HandleFunc("/api/apps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":["app1","app2"]}`))
	})

	// Bọc bằng IPJailMiddleware
	handler := IPJailMiddleware(mux)

	attackerIP := "203.0.113.88"
	legitIP := "203.0.113.99"

	// 1. IP bình thường truy cập /api/apps -> Thành công HTTP 200
	reqLegit := httptest.NewRequest("GET", "/api/apps", nil)
	reqLegit.RemoteAddr = legitIP + ":45000"
	wLegit := httptest.NewRecorder()
	handler.ServeHTTP(wLegit, reqLegit)
	if wLegit.Code != http.StatusOK {
		t.Fatalf("Legitimate IP should get 200 OK, got %d", wLegit.Code)
	}

	// 2. Kẻ tấn công quét trúng route bẫy nhử /.env
	reqTrap := httptest.NewRequest("GET", "/.env", nil)
	reqTrap.RemoteAddr = attackerIP + ":45001"
	reqTrap.Header.Set("User-Agent", "DirBuster/0.12")
	wTrap := httptest.NewRecorder()
	handler.ServeHTTP(wTrap, reqTrap)
	// Bẫy nhử trả về 404 (hoặc bị middleware chặn nếu quét)
	if wTrap.Code != http.StatusNotFound && wTrap.Code != http.StatusForbidden {
		t.Fatalf("Trap route should return 404 or 403, got %d", wTrap.Code)
	}

	// Xác nhận IP kẻ tấn công đã bị giam
	if isJailed, _, _ := GlobalIPJail.IsJailed(attackerIP); !isJailed {
		t.Fatalf("Attacker IP %s must be jailed in Blacklist", attackerIP)
	}

	// 3. Kẻ tấn công cố tình truy cập vào route thông thường /api/apps -> Bị IPJailMiddleware chặn đứng với 403 Forbidden!
	reqBlocked := httptest.NewRequest("GET", "/api/apps", nil)
	reqBlocked.RemoteAddr = attackerIP + ":45002"
	wBlocked := httptest.NewRecorder()
	handler.ServeHTTP(wBlocked, reqBlocked)

	if wBlocked.Code != http.StatusForbidden {
		t.Fatalf("Jailed attacker MUST be blocked with 403 Forbidden, got %d", wBlocked.Code)
	}

	var jsonResp map[string]interface{}
	if err := json.NewDecoder(wBlocked.Body).Decode(&jsonResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if jsonResp["status"] != "forbidden" {
		t.Errorf("Expected status='forbidden', got %v", jsonResp["status"])
	}

	// 4. IP bình thường vẫn truy cập bình thường không bị ảnh hưởng
	wLegit2 := httptest.NewRecorder()
	handler.ServeHTTP(wLegit2, reqLegit)
	if wLegit2.Code != http.StatusOK {
		t.Fatalf("Legitimate user must continue to have access, got %d", wLegit2.Code)
	}
}

func TestHoneypot_CRLFLogSanitization(t *testing.T) {
	GlobalIPJail.ClearJail()
	GlobalWAFAlarmManager.Reset()

	maliciousUA := "Mozilla/5.0\r\n[ADMIN] Privilege Escalated to root\r\n"
	maliciousPath := "/.env\r\nHost: evil.com"
	maliciousIP := "192.0.2.1\r\nX-Injected: true"

	cleanUA := SanitizeCRLF(maliciousUA)
	if strings.Contains(cleanUA, "\r") || strings.Contains(cleanUA, "\n") {
		t.Fatalf("SanitizeCRLF failed to strip CRLF from User-Agent: %q", cleanUA)
	}
	if !strings.Contains(cleanUA, "\\r\\n") {
		t.Fatalf("Expected escaped CRLF sequence, got: %q", cleanUA)
	}

	alert := GlobalWAFAlarmManager.TriggerAlarm(maliciousIP, maliciousPath, maliciousUA, "CRLF injection test")
	if strings.Contains(alert.UserAgent, "\r") || strings.Contains(alert.UserAgent, "\n") {
		t.Fatalf("WAF alert UserAgent contains raw CRLF")
	}
	if strings.Contains(alert.Path, "\r") || strings.Contains(alert.Path, "\n") {
		t.Fatalf("WAF alert Path contains raw CRLF")
	}
}

func TestHoneypot_ReleaseAndExpiration(t *testing.T) {
	jail := NewIPJail(100)
	testIP := "192.0.2.77"

	jail.Jail(testIP, 50*time.Millisecond, "Test", SeverityLow)

	// Đang bị khóa
	isJailed, _, _ := jail.IsJailed(testIP)
	if !isJailed {
		t.Fatalf("IP should be jailed")
	}

	// Mở khóa thủ công bằng Unjail
	jail.Unjail(testIP)
	isJailed, _, _ = jail.IsJailed(testIP)
	if isJailed {
		t.Fatalf("IP should NOT be jailed after Unjail")
	}

	// Test tự hết hạn theo thời gian
	jail.Jail(testIP, 40*time.Millisecond, "Test", SeverityLow)
	time.Sleep(60 * time.Millisecond)

	isJailed, _, _ = jail.IsJailed(testIP)
	if isJailed {
		t.Fatalf("IP should NOT be jailed after duration expires")
	}
}

func TestHoneypot_IsDecoyTrapPath(t *testing.T) {
	positiveCases := []string{
		"/.env",
		"/.env.local",
		"/wp-admin",
		"/wp-admin/",
		"/wp-admin/index.php",
		"/phpmyadmin",
		"/phpmyadmin/index.php",
		"/api/admin/shell",
		"/.git/config",
		"/.git/HEAD",
		"/config.json",
		"/actuator/health",
		"/actuator/info",
	}

	for _, p := range positiveCases {
		if !IsDecoyTrapPath(p) {
			t.Errorf("Path %s should be recognized as decoy trap", p)
		}
	}

	negativeCases := []string{
		"/api/apps",
		"/api/auth/login",
		"/api/health",
		"/tools",
		"/index.html",
		"/css/style.css",
	}

	for _, p := range negativeCases {
		if IsDecoyTrapPath(p) {
			t.Errorf("Path %s should NOT be recognized as decoy trap", p)
		}
	}
}

func TestHoneypot_StatusHandler(t *testing.T) {
	GlobalIPJail.ClearJail()
	GlobalWAFAlarmManager.Reset()

	GlobalIPJail.Jail("198.51.100.11", 24*time.Hour, "Test status", SeverityHigh)

	req := httptest.NewRequest("GET", "/api/security/honeypot-status", nil)
	w := httptest.NewRecorder()

	HoneypotStatusHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode json: %v", err)
	}

	if data["status"] != "active" {
		t.Errorf("Expected status='active', got %v", data["status"])
	}
	if data["jailed_ip_count"].(float64) != 1 {
		t.Errorf("Expected 1 jailed IP, got %v", data["jailed_ip_count"])
	}
}
