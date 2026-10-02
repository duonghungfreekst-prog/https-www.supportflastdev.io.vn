package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecurityHeaders_Default kiểm tra việc áp dụng đầy đủ security headers mặc định khi downstream không set
func TestSecurityHeaders_Default(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Apache/2.4")
		w.Header().Set("X-Powered-By", "Express")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/test", nil)

	handler := SecurityHeaders(dummyHandler)
	handler.ServeHTTP(rec, req)

	// 1. Kiểm tra các header bắt buộc
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options kỳ vọng 'nosniff', nhận '%s'", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options kỳ vọng 'DENY', nhận '%s'", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-XSS-Protection") != "1; mode=block" {
		t.Errorf("X-XSS-Protection kỳ vọng '1; mode=block', nhận '%s'", rec.Header().Get("X-XSS-Protection"))
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Errorf("Strict-Transport-Security không được rỗng")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("Content-Security-Policy không được rỗng")
	}

	// 2. Chống lộ thông tin máy chủ
	if rec.Header().Get("Server") != "" {
		t.Errorf("Server header phải bị triệt tiêu, nhận '%s'", rec.Header().Get("Server"))
	}
	if rec.Header().Get("X-Powered-By") != "" {
		t.Errorf("X-Powered-By header phải bị triệt tiêu, nhận '%s'", rec.Header().Get("X-Powered-By"))
	}

	// 3. Cache control mặc định
	if rec.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate, max-age=0" {
		t.Errorf("Cache-Control kỳ vọng no-cache, nhận '%s'", rec.Header().Get("Cache-Control"))
	}
}

// TestSecurityHeaders_PreserveCustomHeaders kiểm tra không ghi đè khi handler con đã chủ động thiết lập
func TestSecurityHeaders_PreserveCustomHeaders(t *testing.T) {
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Giả lập route stream media hoặc preview file cho phép iframe từ cùng origin
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// Giả lập route tĩnh thiết lập cache trình duyệt 7 ngày
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		// Giả lập CSP tùy chỉnh cho streaming sandbox
		w.Header().Set("Content-Security-Policy", "default-src 'self'; media-src *")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("custom stream content"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stream/123", nil)

	handler := SecurityHeaders(customHandler)
	handler.ServeHTTP(rec, req)

	// Header tùy biến PHẢI được bảo toàn, KHÔNG được ghi đè
	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options bị ghi đè, kỳ vọng 'SAMEORIGIN', nhận '%s'", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=604800, immutable" {
		t.Errorf("Cache-Control bị ghi đè, kỳ vọng 'public, max-age=604800, immutable', nhận '%s'", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Security-Policy") != "default-src 'self'; media-src *" {
		t.Errorf("Content-Security-Policy bị ghi đè, nhận '%s'", rec.Header().Get("Content-Security-Policy"))
	}

	// Các header bảo mật khác không bị ảnh hưởng vẫn được set đầy đủ
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options kỳ vọng 'nosniff', nhận '%s'", rec.Header().Get("X-Content-Type-Options"))
	}
}
