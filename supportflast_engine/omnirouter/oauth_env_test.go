package omnirouter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestOAuthEnvHelpers kiểm tra việc nạp Client ID và Client Secret từ biến môi trường
func TestOAuthEnvHelpers(t *testing.T) {
	// 1. Kiểm tra ưu tiên OAUTH_CLIENT_ID
	t.Run("Ưu tiên OAUTH_CLIENT_ID khi cả hai cùng có", func(t *testing.T) {
		t.Setenv("OAUTH_CLIENT_ID", "client-primary-id.apps.googleusercontent.com")
		t.Setenv("ANTIGRAVITY_CLIENT_ID", "client-secondary-id.apps.googleusercontent.com")

		cid := getOAuthClientID()
		if cid != "client-primary-id.apps.googleusercontent.com" {
			t.Fatalf("Kỳ vọng OAUTH_CLIENT_ID ưu tiên, nhận được: %s", cid)
		}
		if GetOAuthClientID() != cid {
			t.Fatalf("GetOAuthClientID() không khớp với getOAuthClientID()")
		}
	})

	// 2. Kiểm tra fallback sang ANTIGRAVITY_CLIENT_ID khi OAUTH_CLIENT_ID rỗng
	t.Run("Fallback sang ANTIGRAVITY_CLIENT_ID khi OAUTH_CLIENT_ID rỗng", func(t *testing.T) {
		t.Setenv("OAUTH_CLIENT_ID", "")
		t.Setenv("ANTIGRAVITY_CLIENT_ID", "antigravity-fallback-id.apps.googleusercontent.com")

		cid := getOAuthClientID()
		if cid != "antigravity-fallback-id.apps.googleusercontent.com" {
			t.Fatalf("Kỳ vọng fallback sang ANTIGRAVITY_CLIENT_ID, nhận được: %s", cid)
		}
	})

	// 3. Kiểm tra ưu tiên OAUTH_CLIENT_SECRET
	t.Run("Ưu tiên OAUTH_CLIENT_SECRET khi cả hai cùng có", func(t *testing.T) {
		t.Setenv("OAUTH_CLIENT_SECRET", "mock-primary-secret-val")
		t.Setenv("ANTIGRAVITY_CLIENT_SECRET", "mock-secondary-secret-val")

		sec := getOAuthClientSecret()
		if sec != "mock-primary-secret-val" {
			t.Fatalf("Kỳ vọng OAUTH_CLIENT_SECRET ưu tiên, nhận được: %s", sec)
		}
		if GetOAuthClientSecret() != sec {
			t.Fatalf("GetOAuthClientSecret() không khớp với getOAuthClientSecret()")
		}
	})

	// 4. Kiểm tra fallback sang ANTIGRAVITY_CLIENT_SECRET khi OAUTH_CLIENT_SECRET rỗng
	t.Run("Fallback sang ANTIGRAVITY_CLIENT_SECRET khi OAUTH_CLIENT_SECRET rỗng", func(t *testing.T) {
		t.Setenv("OAUTH_CLIENT_SECRET", "")
		t.Setenv("ANTIGRAVITY_CLIENT_SECRET", "antigravity-fallback-secret-val")

		sec := getOAuthClientSecret()
		if sec != "antigravity-fallback-secret-val" {
			t.Fatalf("Kỳ vọng fallback sang ANTIGRAVITY_CLIENT_SECRET, nhận được: %s", sec)
		}
	})

	// 5. Kiểm tra trường hợp cả hai đều rỗng (không văng panic, ghi nhận cảnh báo)
	t.Run("Cả hai biến môi trường rỗng không gây panic", func(t *testing.T) {
		t.Setenv("OAUTH_CLIENT_ID", "")
		t.Setenv("ANTIGRAVITY_CLIENT_ID", "")
		t.Setenv("OAUTH_CLIENT_SECRET", "")
		t.Setenv("ANTIGRAVITY_CLIENT_SECRET", "")

		// Đặt biến toàn cục về rỗng để kiểm thử nhánh rỗng
		AntigravityClientID = ""
		AntigravityClientSecret = ""

		cid := getOAuthClientID()
		sec := getOAuthClientSecret()

		if cid != "" {
			t.Errorf("Kỳ vọng Client ID rỗng khi không có env, nhận được: %s", cid)
		}
		if sec != "" {
			t.Errorf("Kỳ vọng Client Secret rỗng khi không có env, nhận được: %s", sec)
		}
	})
}

// TestOAuthAuthorizeWithDynamicEnv kiểm tra luồng Authorize sinh URL chứa Client ID từ biến môi trường
func TestOAuthAuthorizeWithDynamicEnv(t *testing.T) {
	testClientID := "dynamic-test-client-999.apps.googleusercontent.com"
	t.Setenv("OAUTH_CLIENT_ID", testClientID)
	t.Setenv("OAUTH_CLIENT_SECRET", "test-secret-dynamically-loaded")

	req := httptest.NewRequest("GET", "/api/oauth/antigravity/authorize?redirect_uri=http://localhost:20128/callback", nil)
	rec := httptest.NewRecorder()

	HandleOAuthAuthorize(nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng mã HTTP 200, nhận được: %d", rec.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Phân tích phản hồi JSON thất bại: %v", err)
	}

	authURL, ok := res["authUrl"].(string)
	if !ok || authURL == "" {
		t.Fatalf("authUrl không tồn tại hoặc bị rỗng")
	}

	if !strings.Contains(authURL, testClientID) {
		t.Fatalf("authUrl không chứa dynamic client ID '%s'. URL thực tế: %s", testClientID, authURL)
	}
}

// TestNoHardcodedSecretsInOAuthSource kiểm tra đảm bảo file source oauth.go không chứa chuỗi GOCSPX hardcoded
func TestNoHardcodedSecretsInOAuthSource(t *testing.T) {
	content, err := os.ReadFile("oauth.go")
	if err != nil {
		t.Fatalf("Không thể đọc file oauth.go: %v", err)
	}

	sourceStr := string(content)
	if strings.Contains(sourceStr, "GOCSPX-") {
		t.Fatalf("Phát hiện chuỗi client secret 'GOCSPX-' vẫn bị hardcode trong oauth.go! Vi phạm quy tắc bảo mật.")
	}
}
