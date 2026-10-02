package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/cloudpool/webdav"
	"supportflast_engine/internal/middleware"
	"supportflast_engine/registry"
)


type Server struct {
	db      *storage.DB
	gd      *gdrive.Manager
	vfs     *vfs.VFS
	port    int
	uiDir   string
	baseDir string
	server  *http.Server
}



func NewServer(db *storage.DB, gd *gdrive.Manager, vfs *vfs.VFS, port int, uiDir string, baseDir string) *Server {
	return &Server{
		db:      db,
		gd:      gd,
		vfs:     vfs,
		port:    port,
		uiDir:   uiDir,
		baseDir: baseDir,
	}
}

// ChunkedUploadSession holds state for a multi-chunk upload in progress.


func (s *Server) Start() error {
	if err := InitJWTKeys(s.baseDir); err != nil {
		fmt.Printf("[ENGINE] [FATAL] Failed to initialize JWT keys: %v\n", err)
		return err
	}

	// Start background goroutine to clean up expired chunked upload sessions
	StartSessionCleanupWorker(s.baseDir)

	mux := http.NewServeMux()

	// 1. WebDAV Endpoint
	davHandler := webdav.CreateWebDAVHandler(s.vfs, s.db)
	mux.Handle("/webdav/", davHandler)
	mux.Handle("/webdav", davHandler)

	// 2. API Endpoints
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/accounts", s.handleAccounts)
	mux.HandleFunc("/api/accounts/oauth/url", s.handleOAuthURL)
	mux.HandleFunc("/api/accounts/oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc("/api/accounts/service-account", s.handleAddServiceAccount)
	mux.HandleFunc("/api/accounts/delete", s.handleDeleteAccount)
	mux.HandleFunc("/api/accounts/refresh", s.handleRefreshAccount)
	mux.HandleFunc("/api/accounts/toggle", s.handleToggleAccount)

	mux.HandleFunc("/api/files", s.handleListFiles)
	mux.HandleFunc("/api/files/mkdir", s.handleMkdir)
	mux.HandleFunc("/api/files/rename", s.handleRenameFile)
	mux.HandleFunc("/api/files/delete", s.handleDeleteFile)
	mux.HandleFunc("/api/files/bulk-delete", s.handleBulkDeleteFiles)
	mux.HandleFunc("/api/files/upload", s.handleUploadFile)
	mux.HandleFunc("/api/files/upload-chunked", s.handleChunkedUpload)
	mux.HandleFunc("/api/files/upload-status", s.handleChunkedUploadStatus)
	mux.HandleFunc("/api/files/download", s.handleDownloadFile)
	mux.HandleFunc("/api/files/stream", s.handleStreamFile)
	mux.HandleFunc("/api/files/chunks", s.handleFileChunks)
	mux.HandleFunc("/api/files/zip", s.handleDownloadZip)
	mux.HandleFunc("/api/files/download-zip", s.handleDownloadZip)

	// Recycle Bin (Trash) Endpoints
	mux.HandleFunc("/api/files/trash", s.handleListTrashFiles)
	mux.HandleFunc("/api/files/trash/restore", s.handleRestoreTrashFile)
	mux.HandleFunc("/api/files/trash/delete-forever", s.handlePurgeTrashFile)
	mux.HandleFunc("/api/files/trash/empty", s.handleEmptyTrash)

	// Public Share Links Endpoints
	mux.HandleFunc("/api/shares/create", s.handleCreatePublicShare)
	mux.HandleFunc("/api/shares/list", s.handleListPublicShares)
	mux.HandleFunc("/api/shares/revoke", s.handleRevokePublicShare)
	mux.HandleFunc("/api/public/share/info", s.handlePublicShareInfo)
	mux.HandleFunc("/api/public/share/folder/browse", s.handlePublicShareBrowseFolder)
	mux.HandleFunc("/api/public/share/stream", s.handlePublicShareStream)
	mux.HandleFunc("/api/public/share/download", s.handlePublicShareDownload)

	// Storage Breakdown & Analytics
	mux.HandleFunc("/api/stats/breakdown", s.handleStorageBreakdown)

	// Single-Use OTP File Access & Permission Endpoints
	mux.HandleFunc("/api/files/otp/verify", s.handleVerifyFileOTP)
	mux.HandleFunc("/api/files/otp/request", s.handleRequestFileOTP)
	mux.HandleFunc("/api/files/otp/my-requests", s.handleListMyFileRequests)
	mux.HandleFunc("/api/admin/otp/generate", s.handleGenerateFileOTP)
	mux.HandleFunc("/api/admin/otp/list", s.handleListFileOTPs)
	mux.HandleFunc("/api/admin/otp/revoke", s.handleRevokeFileOTP)
	mux.HandleFunc("/api/admin/otp/requests", s.handleListAdminAccessRequests)
	mux.HandleFunc("/api/admin/otp/approve-request", s.handleApproveAccessRequest)
	mux.HandleFunc("/api/admin/otp/reject-request", s.handleRejectAccessRequest)
	mux.HandleFunc("/api/admin/otp/pending-count", s.handleOTPPendingCount) // Polling badge thông báo


	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/remote/info", s.handleRemoteInfo)
	mux.HandleFunc("/api/tunnel/start", s.handleTunnelStart)
	mux.HandleFunc("/api/tunnel/stop", s.handleTunnelStop)
	mux.HandleFunc("/api/tunnel/status", s.handleTunnelStatus)
	mux.HandleFunc("/api/admin/drive/import", s.handleImportDriveFiles)
	mux.HandleFunc("/api/admin/drive/files", s.handleListDriveFiles)

	// User Authentication & Management Endpoints
	mux.HandleFunc("/api/auth/register", s.handleAuthRegister)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/turnstile-config", s.handleTurnstileConfig)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/verify-admin-pass", s.handleVerifyAdminPass)
	mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/auth/change-password", s.handleAuthChangePassword)
	mux.HandleFunc("/api/auth/security-pin", s.handleAuthSecurityPin)
	mux.HandleFunc("/api/auth/verify-pin", s.handleAuthVerifyPin)
	mux.HandleFunc("/api/admin/users", s.handleListUsers)
	mux.HandleFunc("/api/admin/users/quota", s.handleUpdateUserQuota)
	mux.HandleFunc("/api/admin/users/delete", s.handleDeleteUser)
	mux.HandleFunc("/api/admin/users/reset-password", s.handleAdminResetPassword)
	mux.HandleFunc("/api/admin/logs", s.handleListLogs)
	mux.HandleFunc("/api/admin/sessions", s.handleListSessions)

	// SQL Studio Endpoints
	mux.HandleFunc("/api/sql/tables", s.handleSQLTables)
	mux.HandleFunc("/api/sql/query", s.handleSQLQuery)
	mux.HandleFunc("/api/sql/download", s.handleSQLDownloadDB)
	mux.HandleFunc("/api/sql/optimize", s.handleSQLOptimize)
	mux.HandleFunc("/api/sql/check", s.handleSQLCheck)
	mux.HandleFunc("/api/sql/backup", s.handleSQLBackup)

	// In-App Software Update & Hot-Patching Endpoints
	mux.HandleFunc("/api/admin/update/info", s.handleUpdateInfo)
	mux.HandleFunc("/api/admin/update/upload", s.handleUpdateUpload)
	mux.HandleFunc("/api/admin/update/rollback", s.handleUpdateRollback)
	mux.HandleFunc("/api/admin/update/restart", s.handleRestartEngine)

	// GitHub Auto-Sync Endpoints
	mux.HandleFunc("/api/admin/git/sync", registry.GitSyncHandler)
	mux.HandleFunc("/api/admin/git/status", registry.GitStatusHandler)
	mux.HandleFunc("/api/git/sync", registry.GitSyncHandler)
	mux.HandleFunc("/api/git/status", registry.GitStatusHandler)

	// 3. Static Web UI files
	fileServer := http.FileServer(http.Dir(s.uiDir))
	mux.Handle("/", fileServer)

	// Wrap with Security Middleware (Rule PHAN 3.4) & Gzip Middleware (>1KB)
	handler := s.securityMiddleware(middleware.Gzip(mux))

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      handler,
		ReadHeaderTimeout: 20 * time.Second,
		ReadTimeout:       60 * time.Minute,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    2 << 20,
	}

	return s.server.ListenAndServe()
}



func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Security Middleware (Rule PHAN 3.4 & 3.5)


func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security Headers (Rule PHAN 3.4)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		
		// [BUG FIX] Stored XSS Mitigation: 
		// If streaming a raw user-uploaded file inline, we MUST sandbox it to prevent it from executing malicious JS
		// in the context of the CloudPool domain.
		if strings.HasPrefix(r.URL.Path, "/api/files/stream") || strings.HasPrefix(r.URL.Path, "/api/public/share/stream") {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; media-src 'self' https: data: blob:; img-src 'self' https: data: blob:; style-src 'unsafe-inline';")
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval' https: data: blob:; script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com; connect-src 'self' ws: wss: https://challenges.cloudflare.com; frame-src 'self' https://challenges.cloudflare.com https: blob: data:; object-src 'self' https: blob: data:; media-src 'self' https: blob: data:;")
		}
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Del("Server")
		w.Header().Del("X-Powered-By")
		w.Header().Del("X-AspNet-Version")
		w.Header().Del("server")
		w.Header().Del("x-powered-by")
		w.Header().Del("x-aspnet-version")
		w.Header().Set("ngrok-skip-browser-warning", "true")

		// [BUG FIX] CORS Security (Rule PHAN 3.4)
		// Prevent mirroring arbitrary Origin headers which leads to full CSRF and data exfiltration.
		origin := r.Header.Get("Origin")
		if origin != "" {
			// Parse the origin to safely check hostname
			importURL, err := url.Parse(origin)
			if err == nil && (importURL.Hostname() == "localhost" || importURL.Hostname() == "127.0.0.1" || importURL.Host == r.Host) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Range, X-User-ID, X-Requested-With, ngrok-skip-browser-warning")
				w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges, ngrok-skip-browser-warning")
			}
		}

		// Disable browser caching for UI scripts and styles to support live updates instantly
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// JSON Helper


func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// Error Helper (Rule PHAN 3.5 - Mask internal error from client, log server side)


func writeError(w http.ResponseWriter, status int, clientMsg string, serverErr error) {
	if serverErr != nil {
		// Sanitize log against CRLF (Rule PHAN 3.6)
		cleanErr := strings.ReplaceAll(strings.ReplaceAll(serverErr.Error(), "\n", "\\n"), "\r", "")
		fmt.Printf("[ENGINE] [ERROR] %s: %s\n", clientMsg, cleanErr)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, status, map[string]string{
		"error": clientMsg,
	})
}

// setAuthCookie sets the JWT cookie with correct Secure + SameSite flags.
// When accessed via HTTPS (Ngrok, Cloudflare, etc.), must use Secure=true + SameSite=None
// so the cookie is sent in cross-origin/subdomain requests.
// When accessed via HTTP (localhost), use SameSite=Lax (Secure not needed).


func setAuthCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	isHTTPS := registry.GetRequestScheme(r) == "https" ||
		r.TLS != nil ||
		strings.Contains(r.Header.Get("Origin"), "https://") ||
		strings.Contains(r.Host, "ngrok") ||
		strings.Contains(r.Host, "ngrok-free") ||
		strings.Contains(r.Host, "cloudflare") ||
		strings.Contains(r.Host, "trycloudflare") ||
		strings.Contains(r.Header.Get("X-Forwarded-Host"), "ngrok")

	sameSite := http.SameSiteLaxMode
	secure := false
	if isHTTPS {
		// [BUG FIX] CSRF Security Mitigation
		// Never use SameSiteNoneMode because UI and API are on the exact same domain.
		// Using SameSiteNoneMode allows cross-site requests to send cookies, enabling CSRF.
		sameSite = http.SameSiteLaxMode 
		secure = true
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cloudpool_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "sf_auth_token",
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   maxAge,
	})
}

// -------------------------------------------------------------
// API Handlers
// -------------------------------------------------------------

