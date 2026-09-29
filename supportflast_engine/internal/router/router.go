package router

import "net/http"

// AppDeps chứa tất cả dependencies cần inject vào routes
type AppDeps struct {
	// Giữ simple: chỉ là wrapper quanh các handler functions hiện tại
}

// SetupRoutes đăng ký tất cả routes vào mux
// LIỆT KÊ lại TẤT CẢ routes từ main.go, GOM NHÓM theo domain:
func SetupRoutes(mux *http.ServeMux) {
	// ────────────────────────────────────────────────────────────────────────
	// Đây là router blueprint. Trong giai đoạn REFACTOR PHẦN 1, ta chưa
	// di chuyển handler code từ main.go vào đây. Thay vào đó, ta chỉ đăng ký
	// API v1 versioned routes qua RegisterAPIv1Routes() trong routes.go.
	// ────────────────────────────────────────────────────────────────────────
	//
	// Group 1: Health & System
	//   /api/health
	//   /api/system/shutdown
	//   /api/system/status
	//   /api/system/updates
	//   /api/system/diagnostics
	//
	// Group 2: Auth & Users
	//   /api/auth/register
	//   /api/auth/login
	//   /api/auth/me
	//   /api/auth/logout
	//   /api/auth/change-password
	//   /api/auth/security-pin
	//   /api/auth/verify-pin
	//   /api/auth/public-key
	//
	// Group 3: Apps (publish, list, download)
	//   /api/apps/publish
	//   /api/apps
	//   /api/apps/download/
	//
	// Group 4: Reviews
	//   /api/reviews
	//   /api/reviews/
	//
	// Group 5: API Keys
	//   /api/keys/generate
	//   /api/keys
	//
	// Group 6: Admin & System Management
	//   /api/admin/users
	//   /api/admin/users/reset-password
	//   /api/admin/logs
	//   /api/admin/sessions
	//   /api/admin/system/update
	//   /api/admin/system/deploy-ui
	//   /api/admin/system/broadcast
	//   /api/admin/system/maintenance
	//   /api/admin/system/hot-reload
	//   /api/admin/drive
	//   /api/admin/drive/
	//   /api/admin/otp
	//   /api/admin/otp/
	//   /api/admin/update
	//   /api/admin/update/
	//
	// Group 7: AI Agents
	//   /api/agents
	//   /api/agents/chat
	//
	// Group 8: Security (Honeypot, SIEM)
	//   /api/security/cloudflare-status
	//   /api/security/ssl-status
	//   /api/security/honeypot-status
	//   /api/admin/siem/alerts
	//   /api/admin/siem/stats
	//   + Honeypot decoy routes (registered via security.RegisterHoneypotRoutes)
	//
	// Group 9: CloudPool Storage
	//   /api/files, /api/files/
	//   /api/accounts, /api/accounts/
	//   /api/stats, /api/stats/
	//   /api/shares, /api/shares/
	//   /api/sql, /api/sql/
	//   /api/settings, /api/settings/
	//   /api/remote, /api/remote/
	//   /api/tunnel, /api/tunnel/
	//   /api/public/share/
	//   /webdav, /webdav/
	//
	// Group 10: Device Sync
	//   /api/device/sync
	//
	// Group 11: Static Files
	//   / (root static file server)
	//   /tools, /tools/
	//   /storage, /storage/
	// ────────────────────────────────────────────────────────────────────────
}
