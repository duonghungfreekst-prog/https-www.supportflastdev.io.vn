package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemStatusHandlerBroadcastDisabled(t *testing.T) {
	InvalidateSystemStatusCache()

	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	rec := httptest.NewRecorder()

	SystemStatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got: %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// 1. Kiểm tra trường broadcast_enabled ở cấp root của response
	if val, ok := resp["broadcast_enabled"]; !ok || val != false {
		t.Errorf("Expected root broadcast_enabled to be false, got: %v", val)
	}

	// 2. Kiểm tra trường broadcast_enabled trong object system
	systemObj, ok := resp["system"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'system' object in response")
	}

	if val, ok := systemObj["broadcast_enabled"]; !ok || val != false {
		t.Errorf("Expected system.broadcast_enabled to be false, got: %v", val)
	}
}

func TestDefaultSystemConfig(t *testing.T) {
	// Kiểm tra SystemConfig khởi tạo mặc định
	cfg := SystemConfig{
		BroadcastEnabled: false,
		BroadcastMessage: "",
	}

	if cfg.BroadcastEnabled != false {
		t.Errorf("Expected default BroadcastEnabled to be false")
	}
	if cfg.BroadcastMessage != "" {
		t.Errorf("Expected default BroadcastMessage to be empty")
	}
}

func TestGetServicePort(t *testing.T) {
	// 1. Kiểm tra biến môi trường PORT (ưu tiên hàng đầu cho PaaS: Render, Heroku, Railway, Fly.io)
	t.Setenv("PORT", "3000")
	t.Setenv("HTTP_PLATFORM_PORT", "8081")
	t.Setenv("SERVER_PORT", "8082")
	t.Setenv("APP_PORT", "8083")
	if port := GetServicePort(); port != "3000" {
		t.Errorf("Kỳ vọng PORT=3000, thực tế: %s", port)
	}

	// 2. Khi PORT rỗng, kiểm tra HTTP_PLATFORM_PORT (Windows Server IIS / Azure)
	t.Setenv("PORT", "")
	t.Setenv("HTTP_PLATFORM_PORT", "8081")
	if port := GetServicePort(); port != "8081" {
		t.Errorf("Kỳ vọng HTTP_PLATFORM_PORT=8081, thực tế: %s", port)
	}

	// 3. Khi PORT và HTTP_PLATFORM_PORT rỗng, kiểm tra SERVER_PORT
	t.Setenv("HTTP_PLATFORM_PORT", "")
	t.Setenv("SERVER_PORT", "8082")
	if port := GetServicePort(); port != "8082" {
		t.Errorf("Kỳ vọng SERVER_PORT=8082, thực tế: %s", port)
	}

	// 4. Khi chỉ có APP_PORT
	t.Setenv("SERVER_PORT", "")
	t.Setenv("APP_PORT", "8083")
	if port := GetServicePort(); port != "8083" {
		t.Errorf("Kỳ vọng APP_PORT=8083, thực tế: %s", port)
	}

	// 5. Khi toàn bộ rỗng, mặc định là "8080"
	t.Setenv("APP_PORT", "")
	if port := GetServicePort(); port != "8080" {
		t.Errorf("Kỳ vọng default=8080, thực tế: %s", port)
	}
}

func TestGetListenerHost(t *testing.T) {
	// 1. Mặc định bind vào 0.0.0.0 để nhận traffic từ bên ngoài hosting
	t.Setenv("HOST", "")
	if host := GetListenerHost(); host != "0.0.0.0" {
		t.Errorf("Kỳ vọng default host 0.0.0.0, thực tế: %s", host)
	}

	// 2. Cho phép ghi đè qua biến môi trường HOST
	t.Setenv("HOST", "127.0.0.1")
	if host := GetListenerHost(); host != "127.0.0.1" {
		t.Errorf("Kỳ vọng host 127.0.0.1, thực tế: %s", host)
	}
}

func TestGetRequestScheme(t *testing.T) {
	// 1. Header X-Forwarded-Proto = https
	req1 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req1.Header.Set("X-Forwarded-Proto", "https")
	if s := GetRequestScheme(req1); s != "https" {
		t.Errorf("Kỳ vọng https từ X-Forwarded-Proto, thực tế: %s", s)
	}

	// 2. Header X-Forwarded-Proto có nhiều proxy (comma separated)
	req2 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req2.Header.Set("X-Forwarded-Proto", "https, http")
	if s := GetRequestScheme(req2); s != "https" {
		t.Errorf("Kỳ vọng https từ multi-proxy X-Forwarded-Proto, thực tế: %s", s)
	}

	// 3. Header CF-Visitor của Cloudflare
	req3 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req3.Header.Set("CF-Visitor", `{"scheme":"https"}`)
	if s := GetRequestScheme(req3); s != "https" {
		t.Errorf("Kỳ vọng https từ CF-Visitor, thực tế: %s", s)
	}

	// 4. Header X-Forwarded-Ssl = on
	req4 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req4.Header.Set("X-Forwarded-Ssl", "on")
	if s := GetRequestScheme(req4); s != "https" {
		t.Errorf("Kỳ vọng https từ X-Forwarded-Ssl, thực tế: %s", s)
	}

	// 5. Origin https
	req5 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req5.Header.Set("Origin", "https://supportflastdev.io.vn")
	if s := GetRequestScheme(req5); s != "https" {
		t.Errorf("Kỳ vọng https từ Origin, thực tế: %s", s)
	}

	// 6. Mặc định là http
	req6 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	if s := GetRequestScheme(req6); s != "http" {
		t.Errorf("Kỳ vọng default http, thực tế: %s", s)
	}

	// 7. Request nil an toàn
	if s := GetRequestScheme(nil); s != "http" {
		t.Errorf("Kỳ vọng http cho nil request, thực tế: %s", s)
	}
}

func TestGetRequestHost(t *testing.T) {
	// 1. Header X-Forwarded-Host
	req1 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req1.Header.Set("X-Forwarded-Host", "myapp.onrender.com")
	if h := GetRequestHost(req1); h != "myapp.onrender.com" {
		t.Errorf("Kỳ vọng myapp.onrender.com từ X-Forwarded-Host, thực tế: %s", h)
	}

	// 2. Header X-Forwarded-Host có nhiều proxy (comma separated)
	req2 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req2.Header.Set("X-Forwarded-Host", "koyeb-app.koyeb.app, internal-proxy:8080")
	if h := GetRequestHost(req2); h != "koyeb-app.koyeb.app" {
		t.Errorf("Kỳ vọng koyeb-app.koyeb.app từ multi-proxy X-Forwarded-Host, thực tế: %s", h)
	}

	// 3. Fallback sang r.Host
	req3 := httptest.NewRequest(http.MethodGet, "http://supportflastdev.io.vn/api/test", nil)
	req3.Host = "supportflastdev.io.vn"
	if h := GetRequestHost(req3); h != "supportflastdev.io.vn" {
		t.Errorf("Kỳ vọng supportflastdev.io.vn từ r.Host, thực tế: %s", h)
	}

	// 4. Sanitize CRLF injection trong Host
	req4 := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req4.Header.Set("X-Forwarded-Host", "malicious.com\r\nEvil-Header: 1")
	if h := GetRequestHost(req4); h != "malicious.comEvil-Header: 1" {
		t.Errorf("Kỳ vọng host đã được sanitize CRLF, thực tế: %s", h)
	}
}

func TestGetRequestBaseURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/test", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "app.railway.app")

	expected := "https://app.railway.app"
	if base := GetRequestBaseURL(req); base != expected {
		t.Errorf("Kỳ vọng %s, thực tế: %s", expected, base)
	}
}

func TestGetOAuthRedirectURL(t *testing.T) {
	// 1. Request từ đám mây (Render, Railway, Fly.io, Cloudflare) với cấu hình mặc định localhost
	reqCloud := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/accounts/oauth/url", nil)
	reqCloud.Header.Set("X-Forwarded-Proto", "https")
	reqCloud.Header.Set("X-Forwarded-Host", "supportflastdev.io.vn")

	redirectCloud := GetOAuthRedirectURL(reqCloud, "http://localhost:8080/api/accounts/oauth/callback")
	expectedCloud := "https://supportflastdev.io.vn/api/accounts/oauth/callback"
	if redirectCloud != expectedCloud {
		t.Errorf("Kỳ vọng cloud redirect URL: %s, thực tế: %s", expectedCloud, redirectCloud)
	}

	// 2. Request với cấu hình rỗng
	redirectEmpty := GetOAuthRedirectURL(reqCloud, "")
	if redirectEmpty != expectedCloud {
		t.Errorf("Kỳ vọng cloud redirect URL khi cấu hình rỗng: %s, thực tế: %s", expectedCloud, redirectEmpty)
	}

	// 3. Request từ local development (không qua proxy)
	reqLocal := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/accounts/oauth/url", nil)
	reqLocal.Host = "localhost:8080"
	redirectLocal := GetOAuthRedirectURL(reqLocal, "http://localhost:8080/api/accounts/oauth/callback")
	expectedLocal := "http://localhost:8080/api/accounts/oauth/callback"
	if redirectLocal != expectedLocal {
		t.Errorf("Kỳ vọng local redirect URL: %s, thực tế: %s", expectedLocal, redirectLocal)
	}

	// 4. Request với custom domain cụ thể đã được cấu hình thủ công (không phải localhost)
	customURL := "https://auth.custom-domain.com/oauth/google/callback"
	redirectCustom := GetOAuthRedirectURL(reqCloud, customURL)
	if redirectCustom != customURL {
		t.Errorf("Kỳ vọng tôn trọng cấu hình custom URL: %s, thực tế: %s", customURL, redirectCustom)
	}

	// 5. Request qua subdomain www. (www.supportflastdev.io.vn) tự động chuẩn hóa về Canonical Host
	reqWWW := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/accounts/oauth/url", nil)
	reqWWW.Header.Set("X-Forwarded-Proto", "https")
	reqWWW.Header.Set("X-Forwarded-Host", "www.supportflastdev.io.vn")
	redirectWWW := GetOAuthRedirectURL(reqWWW, "http://localhost:8080/api/accounts/oauth/callback")
	if redirectWWW != expectedCloud {
		t.Errorf("Kỳ vọng www redirect tự chuẩn hóa về Canonical URL %s, thực tế: %s", expectedCloud, redirectWWW)
	}
}

func TestSystemStatusHandlerDynamicHostAndScheme(t *testing.T) {
	InvalidateSystemStatusCache()

	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "cloud-deploy.onrender.com")
	rec := httptest.NewRecorder()

	SystemStatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got: %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if resp["scheme"] != "https" {
		t.Errorf("Expected scheme='https', got: %v", resp["scheme"])
	}
	if resp["host"] != "cloud-deploy.onrender.com" {
		t.Errorf("Expected host='cloud-deploy.onrender.com', got: %v", resp["host"])
	}
	if resp["domain"] != "cloud-deploy.onrender.com" {
		t.Errorf("Expected domain='cloud-deploy.onrender.com', got: %v", resp["domain"])
	}
	if resp["base_url"] != "https://cloud-deploy.onrender.com" {
		t.Errorf("Expected base_url='https://cloud-deploy.onrender.com', got: %v", resp["base_url"])
	}
	if resp["port"] == nil || resp["port"] == "" {
		t.Errorf("Expected port to be non-empty, got: %v", resp["port"])
	}
}
