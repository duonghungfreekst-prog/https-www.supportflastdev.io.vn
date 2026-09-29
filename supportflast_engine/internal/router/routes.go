package router

import (
	"log"
	"net/http"
	"strings"
)

// allV1Routes liệt kê TẤT CẢ API routes hiện tại cần ánh xạ sang /api/v1/
// Mỗi entry là path gốc (bắt đầu bằng /api/)
var allV1Routes = []string{
	// Group 1: Health & System
	"/api/health",
	"/api/system/shutdown",
	"/api/system/status",
	"/api/system/updates",
	"/api/system/diagnostics",

	// Group 2: Auth & Users
	"/api/auth/register",
	"/api/auth/login",
	"/api/auth/me",
	"/api/auth/logout",
	"/api/auth/change-password",
	"/api/auth/security-pin",
	"/api/auth/verify-pin",
	"/api/auth/public-key",

	// Group 3: Apps (publish, list, download)
	"/api/apps/publish",
	"/api/apps",
	"/api/apps/download/",

	// Group 4: Reviews
	"/api/reviews",
	"/api/reviews/",

	// Group 5: API Keys
	"/api/keys/generate",
	"/api/keys",

	// Group 6: Admin & System Management
	"/api/admin/users",
	"/api/admin/users/reset-password",
	"/api/admin/logs",
	"/api/admin/sessions",
	"/api/admin/system/update",
	"/api/admin/system/deploy-ui",
	"/api/admin/system/broadcast",
	"/api/admin/system/maintenance",
	"/api/admin/system/hot-reload",
	"/api/admin/drive",
	"/api/admin/drive/",
	"/api/admin/otp",
	"/api/admin/otp/",
	"/api/admin/update",
	"/api/admin/update/",

	// Group 7: AI Agents
	"/api/agents",
	"/api/agents/chat",

	// Group 8: Security (Honeypot, SIEM)
	"/api/security/cloudflare-status",
	"/api/security/ssl-status",
	"/api/security/honeypot-status",
	"/api/admin/siem/alerts",
	"/api/admin/siem/stats",

	// Group 9: CloudPool Storage
	"/api/files",
	"/api/files/",
	"/api/accounts",
	"/api/accounts/",
	"/api/stats",
	"/api/stats/",
	"/api/shares",
	"/api/shares/",
	"/api/sql",
	"/api/sql/",
	"/api/settings",
	"/api/settings/",
	"/api/remote",
	"/api/remote/",
	"/api/tunnel",
	"/api/tunnel/",
	"/api/public/share/",

	// Group 10: Device Sync
	"/api/device/sync",
}

// RegisterAPIv1Routes đăng ký các route /api/v1/* proxy sang /api/* trên cùng mux.
// Đây là bước đầu tiên của API Versioning: client có thể dùng /api/v1/health
// và request sẽ được nội bộ chuyển tiếp đến handler /api/health đã đăng ký.
func RegisterAPIv1Routes(mux *http.ServeMux) {
	registered := 0
	for _, originalPath := range allV1Routes {
		v1Path := makeV1Path(originalPath)
		mux.HandleFunc(v1Path, makeV1Proxy(originalPath, mux))
		registered++
	}
	log.Printf("[ENGINE] [ROUTER] API v1 routes registered successfully (%d routes mapped to /api/v1/*)", registered)
}

// makeV1Path chuyển đổi /api/xxx → /api/v1/xxx
// Ví dụ: /api/health → /api/v1/health
//
//	/api/apps/download/ → /api/v1/apps/download/
func makeV1Path(originalPath string) string {
	return "/api/v1" + strings.TrimPrefix(originalPath, "/api")
}

// makeV1Proxy tạo http.HandlerFunc rewrite URL path từ /api/v1/xxx → /api/xxx
// rồi forward request đến mux gốc để handler ban đầu xử lý.
func makeV1Proxy(originalPath string, mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Rewrite path: /api/v1/health → /api/health
		// Giữ nguyên phần suffix nếu route có trailing slash (wildcard)
		// Ví dụ: /api/v1/apps/download/myapp.exe → /api/apps/download/myapp.exe
		oldPath := r.URL.Path
		newPath := "/api" + strings.TrimPrefix(oldPath, "/api/v1")
		r.URL.Path = newPath
		if r.URL.RawPath != "" {
			r.URL.RawPath = "/api" + strings.TrimPrefix(r.URL.RawPath, "/api/v1")
		}

		// Forward đến mux gốc — handler ban đầu sẽ được gọi với path đã rewrite
		mux.ServeHTTP(w, r)
	}
}
