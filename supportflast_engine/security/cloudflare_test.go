package security

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsCloudflareOrLocalIP(t *testing.T) {
	// Localhost
	if !IsCloudflareOrLocalIP("127.0.0.1:8080") {
		t.Errorf("Expected 127.0.0.1 to be trusted local IP")
	}
	if !IsCloudflareOrLocalIP("::1") {
		t.Errorf("Expected ::1 to be trusted local IP")
	}

	// Cloudflare official IPv4
	if !IsCloudflareOrLocalIP("173.245.48.10:443") {
		t.Errorf("Expected Cloudflare IP 173.245.48.10 to be recognized")
	}
	if !IsCloudflareOrLocalIP("104.16.123.45") {
		t.Errorf("Expected Cloudflare IP 104.16.123.45 to be recognized")
	}

	// Non-Cloudflare public IP
	if IsCloudflareOrLocalIP("8.8.8.8:53") {
		t.Errorf("Google DNS 8.8.8.8 should not be identified as Cloudflare")
	}
}

func TestGetRealClientIP(t *testing.T) {
	// Request từ Cloudflare IP có header CF-Connecting-IP
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "104.16.1.1:12345"
	req.Header.Set("CF-Connecting-IP", "203.113.15.20")

	ip := GetRealClientIP(req)
	if ip != "203.113.15.20" {
		t.Errorf("Expected real IP 203.113.15.20, got %s", ip)
	}

	// Request từ IP lạ cố tình giả mạo header CF-Connecting-IP
	fakeReq := httptest.NewRequest("GET", "/api/test", nil)
	fakeReq.RemoteAddr = "1.2.3.4:56789" // Không thuộc Cloudflare cũng không phải local
	fakeReq.Header.Set("CF-Connecting-IP", "99.99.99.99")

	fakeIP := GetRealClientIP(fakeReq)
	if fakeIP == "99.99.99.99" {
		t.Errorf("Spoofed CF-Connecting-IP should not be trusted from untrusted IP")
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := &ipRateLimiter{
		requests: make(map[string][]time.Time),
	}

	testIP := "192.168.1.100"
	// Cho phép 3 requests
	for i := 0; i < 3; i++ {
		if !limiter.Allow(testIP, 3, time.Second) {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	// Request thứ 4 phải bị block
	if limiter.Allow(testIP, 3, time.Second) {
		t.Errorf("Request 4 should be blocked by rate limiter")
	}
}

func TestVerifyTurnstileToken(t *testing.T) {
	// 1. Empty token should fail
	valid, err := VerifyTurnstileToken("secret", "", "127.0.0.1")
	if valid || err == nil {
		t.Errorf("Expected failure for empty token, got valid=%v, err=%v", valid, err)
	}

	// 2. Cloudflare official always-pass test secret key
	valid, err = VerifyTurnstileToken("1x0000000000000000000000000000000AA", "any-token", "127.0.0.1")
	if !valid || err != nil {
		t.Errorf("Expected success for Cloudflare test secret key, got valid=%v, err=%v", valid, err)
	}

	// 3. Dummy token bypass
	valid, err = VerifyTurnstileToken("secret", "XXXX.DUMMY.TOKEN.XXXX", "127.0.0.1")
	if !valid || err != nil {
		t.Errorf("Expected success for dummy token, got valid=%v, err=%v", valid, err)
	}
}
