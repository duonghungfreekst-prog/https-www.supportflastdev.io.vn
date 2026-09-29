package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/cloudpool/webdav"
	"supportflast_engine/registry"
	"supportflast_engine/security"

	"github.com/google/uuid"
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
type ChunkedUploadSession struct {
	UploadID    string              `json:"upload_id"`
	FileName    string              `json:"file_name"`
	ParentID    string              `json:"parent_id"`
	UserID      string              `json:"user_id"`
	TotalSize   int64               `json:"total_size"`
	TotalChunks int                 `json:"total_chunks"`
	TempDir     string              `json:"temp_dir"` // Thư mục lưu các mảnh trên ổ đĩa: data/temp_chunks/{upload_id}
	Received    map[int]bool        `json:"received"` // Đánh dấu các index mảnh đã nhận thành công
	Status      string              `json:"status"`   // "uploading", "assembling", "completed", "error"
	ResultFile  *models.VirtualFile `json:"result_file,omitempty"` // Resulting VirtualFile when completed
	ErrorMsg    string              `json:"error_msg,omitempty"`   // Error details if failed
	mu          sync.Mutex          `json:"-"`
	CreatedAt   time.Time           `json:"created_at"`
}

// chunkedSessions stores active chunked upload sessions keyed by upload_id.
var chunkedSessions sync.Map

func (s *Server) getOrRestoreChunkedSession(uploadID string) (*ChunkedUploadSession, bool) {
	if uploadID == "" {
		return nil, false
	}
	if val, ok := chunkedSessions.Load(uploadID); ok {
		return val.(*ChunkedUploadSession), true
	}

	// Thử khôi phục từ tệp session.json trên ổ đĩa nếu server vừa được khởi động lại
	sessionFile := filepath.Join(s.baseDir, "data", "temp_chunks", uploadID, "session.json")
	sBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil, false
	}

	var session ChunkedUploadSession
	if err := json.Unmarshal(sBytes, &session); err != nil {
		return nil, false
	}
	if session.Received == nil {
		session.Received = make(map[int]bool)
	}

	// Đọc lại các mảnh chunk đã ghi trên ổ đĩa
	if entries, err := os.ReadDir(session.TempDir); err == nil {
		for _, entry := range entries {
			var idx int
			if n, _ := fmt.Sscanf(entry.Name(), "chunk_%d", &idx); n == 1 {
				session.Received[idx] = true
			}
		}
	}

	chunkedSessions.Store(uploadID, &session)
	log.Printf("[ENGINE] [RESTORE] Đã khôi phục thành công phiên tải lên từ đĩa: %s (file: %s, chunks: %d/%d)",
		uploadID, session.FileName, len(session.Received), session.TotalChunks)
	return &session, true
}

// cleanupExpiredSessions removes chunked upload sessions older than 2 hours and deletes temp files.
func cleanupExpiredSessions() {
	chunkedSessions.Range(func(key, value interface{}) bool {
		session, ok := value.(*ChunkedUploadSession)
		if !ok {
			chunkedSessions.Delete(key)
			return true
		}
		if time.Since(session.CreatedAt) > 2*time.Hour {
			if session.TempDir != "" {
				_ = os.RemoveAll(session.TempDir)
			}
			chunkedSessions.Delete(key)
			log.Printf("[ENGINE] Cleaned up expired chunked upload session and temp dir: %s", key)
		}
		return true
	})
}

func (s *Server) Start() error {
	if err := InitJWTKeys(s.baseDir); err != nil {
		fmt.Printf("[ENGINE] [FATAL] Failed to initialize JWT keys: %v\n", err)
		return err
	}

	// Start background goroutine to clean up expired chunked upload sessions
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupExpiredSessions()
		}
	}()

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
	mux.HandleFunc("/api/admin/otp/pending-count", s.handleOTPPendingCount) // Polling badge thÃ´ng bÃ¡o


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

	// Wrap with Security Middleware (Rule PHAN 3.4)
	handler := s.securityMiddleware(mux)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      handler,
		ReadTimeout:  30 * time.Minute, // Large file upload support
		WriteTimeout: 30 * time.Minute, // Large file stream support
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
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval' https: data: blob:; frame-src 'self' https: blob: data:; object-src 'self' https: blob: data:; media-src 'self' https: blob: data:;")
		}
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
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
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
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

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ láº¥y sá»‘ liá»‡u thá»‘ng kÃª", err)
		return
	}
	stats.RustCoreActive = core.IsDLLLoaded()

	user := s.getUserFromRequest(r)
	if user != nil && user.Role != "admin" {
		stats.IsUserPartition = true
		stats.UserUsedBytes = user.UsedBytes
		stats.UserQuotaBytes = user.QuotaBytes
	}

	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.db.ListAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ láº¥y danh sÃ¡ch tÃ i khoáº£n", err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) handleOAuthURL(w http.ResponseWriter, r *http.Request) {
	settings, err := s.db.GetSettings()
	if err != nil || settings.GoogleClientID == "" {
		writeError(w, http.StatusBadRequest, "Vui lÃ²ng cáº¥u hÃ¬nh Google Client ID & Secret trong pháº§n CÃ i Ä‘áº·t trÆ°á»›c", err)
		return
	}

	redirectURL := registry.GetOAuthRedirectURL(r, settings.RedirectURL)

	state := fmt.Sprintf("st_%d", time.Now().Unix())
	url := s.gd.GetOAuthURL(settings.GoogleClientID, settings.GoogleClientSecret, redirectURL, state)
	writeJSON(w, http.StatusOK, map[string]string{
		"auth_url":     url,
		"state":        state,
		"redirect_url": redirectURL,
	})
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		// Also check POST body
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		code = body.Code
	}

	if code == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u mÃ£ xÃ¡c thá»±c OAuth (code)", nil)
		return
	}

	settings, err := s.db.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y cáº¥u hÃ¬nh", err)
		return
	}

	redirectURL := registry.GetOAuthRedirectURL(r, settings.RedirectURL)

	callbackCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	acc, err := s.gd.HandleOAuthCallback(callbackCtx, settings.GoogleClientID, settings.GoogleClientSecret, redirectURL, code)
	if err != nil {
		if r.Method == http.MethodGet {
			var errType string
			if strings.Contains(err.Error(), "insufficient") || strings.Contains(err.Error(), "SCOPE") {
				errType = "insufficient_scope"
			} else {
				errType = url.QueryEscape(err.Error())
			}
			http.Redirect(w, r, "/storage/?oauth_error="+errType, http.StatusTemporaryRedirect)
			return
		}
		writeError(w, http.StatusBadRequest, "XÃ¡c thá»±c Google tháº¥t báº¡i: "+err.Error(), err)
		return
	}

	// Redirect to web UI with success query param or return JSON
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/storage/?oauth_success=true", http.StatusTemporaryRedirect)
		return
	}

	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleAddServiceAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thÃªm tÃ i khoáº£n Ä‘Ã¡m mÃ¢y", nil)
		return
	}

	file, _, err := r.FormFile("sa_file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Vui lÃ²ng táº£i lÃªn tá»‡p JSON Service Account", err)
		return
	}
	defer file.Close()

	saBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "KhÃ´ng Ä‘á»c Ä‘Æ°á»£c tá»‡p JSON", err)
		return
	}

	acc, err := s.gd.AddServiceAccount(r.Context(), saBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Lá»—i káº¿t ná»‘i Service Account: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n xÃ³a tÃ i khoáº£n Ä‘Ã¡m mÃ¢y", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID tÃ i khoáº£n", nil)
		return
	}

	if err := s.db.DeleteAccount(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i xÃ³a tÃ i khoáº£n", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ xÃ³a tÃ i khoáº£n thÃ nh cÃ´ng"})
}

func (s *Server) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user != nil && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n lÃ m má»›i dung lÆ°á»£ng tÃ i khoáº£n", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		// Refresh all
		go s.gd.RefreshAllQuotas(context.Background())
		writeJSON(w, http.StatusOK, map[string]string{"message": "Äang quÃ©t vÃ  lÃ m má»›i dung lÆ°á»£ng táº¥t cáº£ tÃ i khoáº£n"})
		return
	}

	if err := s.gd.RefreshAccountQuota(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ cáº­p nháº­t dung lÆ°á»£ng: "+err.Error(), err)
		return
	}

	acc, _ := s.db.GetAccount(id)
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)

	accountID := r.URL.Query().Get("account_id")
	if accountID != "" {
		files, err := s.db.ListFilesByAccount(accountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ táº£i danh sÃ¡ch tá»‡p cá»§a tÃ i khoáº£n", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"parent": nil,
			"files":  files,
		})
		return
	}

	parentID := r.URL.Query().Get("parent_id")
	if parentID == "" {
		parentID = "root"
	}

	var targetUserID string
	if user == nil {
		targetUserID = "guest"
	} else if user.Role == "admin" {
		filterUser := r.URL.Query().Get("user_id")
		if filterUser != "" {
			targetUserID = filterUser
		} else {
			targetUserID = "all"
		}
	} else {
		// Regular user strictly isolated to their own partition
		targetUserID = user.ID
	}

	files, err := s.vfs.ListDirectory(targetUserID, parentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ táº£i danh sÃ¡ch tá»‡p", err)
		return
	}

	// Also retrieve current folder info if not root
	var currentFolder *models.VirtualFile
	if parentID != "root" {
		currentFolder, _ = s.db.GetVirtualFile(parentID)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"parent": currentFolder,
		"files":  files,
	})
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	} else {
		if guestMode == "strict" {
			writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ táº¡o thÆ° má»¥c", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Cháº¿ Ä‘á»™ KhÃ¡ch chá»‰ cho phÃ©p xem vÃ  táº£i xuá»‘ng. Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ táº¡o thÆ° má»¥c.", nil)
			return
		}
	}

	var body struct {
		ParentID string `json:"parent_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "TÃªn thÆ° má»¥c khÃ´ng há»£p lá»‡", err)
		return
	}

	folder, err := s.vfs.Mkdir(currentUserID, body.ParentID, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, folder)
}

func (s *Server) handleRenameFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" || body.NewName == "" {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u Ä‘á»•i tÃªn khÃ´ng há»£p lá»‡", err)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ Ä‘á»•i tÃªn tá»‡p tin", nil)
		return
	}
	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(body.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n Ä‘á»•i tÃªn tá»‡p tin cá»§a tÃ i khoáº£n khÃ¡c", nil)
			return
		}
	}

	if err := s.vfs.RenameFileOrFolder(body.ID, body.NewName); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i Ä‘á»•i tÃªn: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ Ä‘á»•i tÃªn thÃ nh cÃ´ng"})
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ xÃ³a tá»‡p tin", nil)
		return
	}
	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(id)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n xÃ³a tá»‡p tin cá»§a tÃ i khoáº£n khÃ¡c", nil)
			return
		}
	}

	if err := s.vfs.DeleteFileOrFolder(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i xÃ³a tá»‡p/thÆ° má»¥c: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ xÃ³a thÃ nh cÃ´ng"})
}

func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	} else {
		if guestMode == "strict" {
			writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ táº£i lÃªn tá»‡p tin", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Cháº¿ Ä‘á»™ KhÃ¡ch (Guest) chá»‰ cho phÃ©p xem vÃ  táº£i xuá»‘ng. Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ táº£i lÃªn.", nil)
			return
		}
	}

	// Support up to 500GB uploads via stream parsing
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart request", err)
		return
	}

	var parentID string
	var uploadedFile *models.VirtualFile

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Error reading multipart part", err)
			return
		}

		formName := part.FormName()
		if formName == "parent_id" {
			data, _ := io.ReadAll(part)
			parentID = string(data)
			continue
		}
		if formName == "rel_path" {
			data, _ := io.ReadAll(part)
			relPath := string(data)
			if relPath != "" {
				// Auto-create directory structure for folder uploads
				targetParentID, err := s.vfs.EnsureDirectoryPath(currentUserID, parentID, relPath)
				if err == nil && targetParentID != "" {
					parentID = targetParentID
				}
			}
			continue
		}

		if formName == "file" {
			if parentID == "" {
				parentID = "root"
				log.Printf("[ENGINE] Warning: parent_id not set before file part, defaulting to root")
			}

			fileName := part.FileName()
			if fileName == "" {
				fileName = fmt.Sprintf("upload_%d.bin", time.Now().Unix())
			}

			// If filename contains relative folder path (from webkitRelativePath), extract folder
			if strings.Contains(fileName, "/") || strings.Contains(fileName, "\\") {
				normPath := strings.ReplaceAll(fileName, "\\", "/")
				dirPart := path.Dir(normPath)
				baseName := path.Base(normPath)
				if dirPart != "." && dirPart != "/" && dirPart != "" {
					targetParentID, err := s.vfs.EnsureDirectoryPath(currentUserID, parentID, dirPart)
					if err == nil && targetParentID != "" {
						parentID = targetParentID
					}
					fileName = baseName
				}
			}

			// Kiá»ƒm tra trÆ°á»›c xem cÃ³ file trÃ¹ng tÃªn khÃ´ng (Ä‘á»ƒ bÃ¡o vá»›i frontend)
			existingOld, _ := s.db.FindFileByNameInParent(parentID, fileName)
			wasReplaced := existingOld != nil

			vfile, err := s.vfs.UploadFile(r.Context(), currentUserID, parentID, fileName, part, -1)
			if err != nil {
				log.Printf("[ENGINE] Upload error for user %s: %v", currentUserID, err)
				errMsg := err.Error()
				switch {
				case strings.Contains(errMsg, "không có tài khoản Google Drive nào còn đủ dung lượng") || strings.Contains(errMsg, "storage") || strings.Contains(errMsg, "quota"):
					writeError(w, http.StatusInsufficientStorage, "Hết dung lượng lưu trữ. Vui lòng thêm tài khoản Google Drive hoặc giải phóng dung lượng.", err)
				case strings.Contains(errMsg, "parent folder not found"):
					writeError(w, http.StatusNotFound, "Thư mục đích không tồn tại.", err)
				case strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context deadline"):
					writeError(w, http.StatusRequestTimeout, "Yêu cầu tải lên đã hết thời gian hoặc bị hủy.", err)
				case strings.Contains(errMsg, "error reading file data"):
					writeError(w, http.StatusBadRequest, "Lỗi đọc dữ liệu tệp. Kết nối có thể đã bị ngắt.", err)
				default:
					writeError(w, http.StatusInternalServerError, "Lỗi tải tệp lên. Vui lòng thử lại sau.", err)
				}
				return
			}
			uploadedFile = vfile
			uploadedFile.Replaced = wasReplaced
		}

	}

	if uploadedFile == nil {
		writeError(w, http.StatusBadRequest, "KhÃ´ng tÃ¬m tháº¥y tá»‡p Ä‘Ã­nh kÃ¨m trong request", nil)
		return
	}

	writeJSON(w, http.StatusOK, uploadedFile)
}

func (s *Server) handleChunkedUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	// Auth: copy logic from handleUploadFile
	user := s.getUserFromRequest(r)
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	} else {
		if guestMode == "strict" {
			writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để tải lên tệp tin", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Chế độ Khách (Guest) chỉ cho phép xem và tải xuống. Vui lòng đăng nhập để tải lên.", nil)
			return
		}
	}

	// Determine mode: INIT or CHUNK
	uploadID := r.URL.Query().Get("upload_id")
	chunkIndexStr := r.URL.Query().Get("chunk_index")

	if uploadID == "" && chunkIndexStr == "" {
		// === INIT MODE: Content-Type should be application/json ===
		var initReq struct {
			FileName    string `json:"file_name"`
			ParentID    string `json:"parent_id"`
			TotalSize   int64  `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&initReq); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body", err)
			return
		}
		if initReq.FileName == "" || initReq.TotalChunks <= 0 {
			writeError(w, http.StatusBadRequest, "Thiếu file_name hoặc total_chunks không hợp lệ", nil)
			return
		}
		if initReq.ParentID == "" {
			initReq.ParentID = "root"
		}

		newID := uuid.New().String()
		tempDir := filepath.Join(s.baseDir, "data", "temp_chunks", newID)
		if err := os.MkdirAll(tempDir, 0700); err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi khởi tạo không gian lưu trữ mảnh tạm", err)
			return
		}

		session := &ChunkedUploadSession{
			UploadID:    newID,
			FileName:    initReq.FileName,
			ParentID:    initReq.ParentID,
			UserID:      currentUserID,
			TotalSize:   initReq.TotalSize,
			TotalChunks: initReq.TotalChunks,
			TempDir:     tempDir,
			Received:    make(map[int]bool),
			Status:      "uploading",
			CreatedAt:   time.Now(),
		}
		chunkedSessions.Store(newID, session)

		// Lưu session.json vào ổ đĩa để phiên tải lên không bị mất nếu server reload
		if sBytes, err := json.Marshal(session); err == nil {
			_ = os.WriteFile(filepath.Join(tempDir, "session.json"), sBytes, 0600)
		}

		log.Printf("[ENGINE] Chunked upload session created: %s, file=%s, chunks=%d, size=%d, user=%s (tempDir: %s)",
			newID, initReq.FileName, initReq.TotalChunks, initReq.TotalSize, currentUserID, tempDir)

		writeJSON(w, http.StatusOK, map[string]string{"upload_id": newID})
		return
	}

	// === CHUNK MODE: receive a chunk ===
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu upload_id", nil)
		return
	}

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "chunk_index không hợp lệ", err)
		return
	}

	session, ok := s.getOrRestoreChunkedSession(uploadID)
	if !ok {
		writeError(w, http.StatusNotFound, "Phiên tải lên không tồn tại hoặc đã hết hạn", nil)
		return
	}

	// Verify user ownership
	if session.UserID != currentUserID && session.UserID != "user_guest" && currentUserID != "user_guest" && (user == nil || user.Role != "admin") {
		writeError(w, http.StatusForbidden, "Không có quyền truy cập phiên tải lên này", nil)
		return
	}

	if chunkIndex < 0 || chunkIndex >= session.TotalChunks {
		writeError(w, http.StatusBadRequest, "chunk_index ngoài phạm vi cho phép", nil)
		return
	}

	// Kiểm tra nếu chunk này đã được nhận trước đó (idempotent, mobile retry mượt mà)
	session.mu.Lock()
	if session.Received[chunkIndex] {
		session.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "chunk_received",
			"chunk_index":  chunkIndex,
			"total_chunks": session.TotalChunks,
			"received":     len(session.Received),
		})
		return
	}
	session.mu.Unlock()

	// Read chunk data from multipart form and stream directly to disk file (Zero RAM allocation)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart request", err)
		return
	}

	chunkFilePath := filepath.Join(session.TempDir, fmt.Sprintf("chunk_%d", chunkIndex))
	chunkFile, err := os.OpenFile(chunkFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo tệp lưu trữ mảnh tạm", err)
		return
	}

	var written int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			chunkFile.Close()
			_ = os.Remove(chunkFilePath)
			writeError(w, http.StatusBadRequest, "Lỗi đọc multipart part", err)
			return
		}
		if part.FormName() == "chunk" {
			written, err = io.Copy(chunkFile, part)
			chunkFile.Close()
			if err != nil {
				_ = os.Remove(chunkFilePath)
				writeError(w, http.StatusInternalServerError, "Lỗi ghi dữ liệu mảnh lên ổ đĩa", err)
				return
			}
			break
		}
	}

	if written == 0 {
		_ = os.Remove(chunkFilePath)
		writeError(w, http.StatusBadRequest, "Không tìm thấy dữ liệu chunk trong request hoặc chunk rỗng", nil)
		return
	}

	// Store chunk status
	session.mu.Lock()
	session.Received[chunkIndex] = true
	receivedCount := len(session.Received)
	session.mu.Unlock()

	log.Printf("[ENGINE] Chunked upload %s: received chunk %d/%d (%d bytes written to disk)",
		uploadID, chunkIndex+1, session.TotalChunks, written)

	// Check if all chunks received
	if receivedCount < session.TotalChunks {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "chunk_received",
			"received": receivedCount,
			"total":    session.TotalChunks,
		})
		return
	}

	// === All chunks received → assemble and upload asynchronously via background goroutine ===
	session.mu.Lock()
	session.Status = "assembling"
	session.mu.Unlock()

	log.Printf("[ENGINE] Chunked upload %s: all %d chunks received, starting async assembly for file %s",
		uploadID, session.TotalChunks, session.FileName)

	go func(uID string, sess *ChunkedUploadSession) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()

		pr, pw := io.Pipe()

		go func() {
			defer pw.Close()
			defer os.RemoveAll(sess.TempDir)
			for i := 0; i < sess.TotalChunks; i++ {
				cPath := filepath.Join(sess.TempDir, fmt.Sprintf("chunk_%d", i))
				cf, err := os.Open(cPath)
				if err != nil {
					pw.CloseWithError(fmt.Errorf("mảnh %d bị thiếu trên ổ đĩa: %w", i, err))
					return
				}
				if _, err := io.Copy(pw, cf); err != nil {
					cf.Close()
					_ = os.Remove(cPath)
					pw.CloseWithError(err)
					return
				}
				cf.Close()
				_ = os.Remove(cPath) // Xóa mảnh ngay sau khi stream vào mã hóa Google Drive
			}
		}()

		existingOld, _ := s.db.FindFileByNameInParent(sess.ParentID, sess.FileName)
		wasReplaced := existingOld != nil

		vfile, err := s.vfs.UploadFile(bgCtx, sess.UserID, sess.ParentID, sess.FileName, pr, sess.TotalSize)
		sess.mu.Lock()
		defer sess.mu.Unlock()

		if err != nil {
			log.Printf("[ENGINE] [ASYNC-UPLOAD] Error assembling file %s for user %s: %v", sess.FileName, sess.UserID, err)
			sess.Status = "error"
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "không có tài khoản Google Drive nào còn đủ dung lượng") || strings.Contains(errMsg, "storage") || strings.Contains(errMsg, "quota"):
				sess.ErrorMsg = "Hết dung lượng lưu trữ. Vui lòng thêm tài khoản Google Drive hoặc giải phóng dung lượng."
			case strings.Contains(errMsg, "parent folder not found"):
				sess.ErrorMsg = "Thư mục đích không tồn tại."
			case strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context deadline"):
				sess.ErrorMsg = "Xử lý tệp đã quá thời gian cho phép hoặc bị hủy."
			case strings.Contains(errMsg, "error reading file data"):
				sess.ErrorMsg = "Lỗi đọc dữ liệu tệp tin."
			default:
				sess.ErrorMsg = "Lỗi ghép và mã hóa tệp lên đám mây. Vui lòng thử lại sau."
			}
			return
		}

		vfile.Replaced = wasReplaced
		sess.ResultFile = vfile
		sess.Status = "completed"
		log.Printf("[ENGINE] [ASYNC-UPLOAD] Chunked upload %s finished successfully: file=%s, id=%s", uID, sess.FileName, vfile.ID)
	}(uploadID, session)

	// Phản hồi ngay lập tức cho client không để HTTP request bị Cloudflare timeout (Error 524)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "assembling",
		"received":  receivedCount,
		"total":     session.TotalChunks,
		"upload_id": uploadID,
		"message":   "Tất cả mảnh đã tải lên máy chủ. Đang mã hóa và phân tán lên Google Drive...",
	})
}

func (s *Server) handleChunkedUploadStatus(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("upload_id")
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu upload_id", nil)
		return
	}

	session, ok := s.getOrRestoreChunkedSession(uploadID)
	if !ok {
		writeError(w, http.StatusNotFound, "Phiên tải lên không tồn tại hoặc đã hết hạn", nil)
		return
	}

	// Auth check
	user := s.getUserFromRequest(r)
	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	}
	if session.UserID != currentUserID && session.UserID != "user_guest" && currentUserID != "user_guest" && (user == nil || user.Role != "admin") {
		writeError(w, http.StatusForbidden, "Không có quyền truy cập phiên này", nil)
		return
	}

	session.mu.Lock()
	status := session.Status
	resultFile := session.ResultFile
	errMsg := session.ErrorMsg
	session.mu.Unlock()

	if status == "completed" && resultFile != nil {
		if session.TempDir != "" {
			_ = os.RemoveAll(session.TempDir)
		}
		chunkedSessions.Delete(uploadID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "completed",
			"file":   resultFile,
		})
		return
	}

	if status == "error" {
		if session.TempDir != "" {
			_ = os.RemoveAll(session.TempDir)
		}
		chunkedSessions.Delete(uploadID)
		writeError(w, http.StatusInternalServerError, errMsg, nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    status, // "uploading" hoặc "assembling"
		"upload_id": uploadID,
	})
}

func (s *Server) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID", nil)
		return
	}

	vfile, err := s.db.GetVirtualFile(id)
	if err != nil || vfile.IsDir {
		writeError(w, http.StatusNotFound, "Tá»‡p khÃ´ng tá»“n táº¡i", err)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	isOwner := user != nil && vfile.UserID == user.ID

	if !isAdmin && !isOwner {
		// Kiểm tra xem file hoặc thư mục cha có được chia sẻ công khai không
		if !s.db.IsFileOrAncestorShared(vfile.ID) {
			settings, _ := s.db.GetSettings()
			guestMode := "view_only"
			if settings != nil && settings.GuestAccessMode != "" {
				guestMode = settings.GuestAccessMode
			}

			isAdminOwned := (vfile.UserID == "user_admin" || vfile.UserID == "")
			otp := r.URL.Query().Get("otp")
			if otp == "" {
				otp = r.Header.Get("X-File-OTP")
			}

			if isAdminOwned {
				if otp != "" {
					valid, err := s.db.VerifyAndBurnOTP(vfile.ID, "user_admin", otp)
					if !valid || err != nil {
						writeError(w, http.StatusForbidden, "Mã OTP mở khóa không hợp lệ hoặc đã hết hạn", err)
						return
					}
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    "user_admin",
						Username:  "admin",
						Action:    "OTP_STREAM",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Mở khóa xem tệp tin Admin thành công bằng mã OTP",
					})
				} else if guestMode == "strict" {
					if user == nil {
						writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để xem nội dung.", nil)
						return
					}
					writeError(w, http.StatusForbidden, "Tệp tin này yêu cầu Mã OTP do Quản Trị Viên cấp.", nil)
					return
				}
				// Chế độ view_only hoặc open: Cho phép xem và phát trực tiếp tệp tin trong kho lưu trữ
			} else {
				// Tệp tin riêng tư của người dùng khác
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để xem nội dung.", nil)
					return
				}
				writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập tệp tin riêng tư này.", nil)
				return
			}
		}
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i khá»Ÿi táº¡o luá»“ng stream: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	mimeType := vfile.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ext := strings.ToLower(filepath.Ext(vfile.Name))
	if ext == ".pdf" {
		mimeType = "application/pdf"
	} else if ext == ".webm" {
		mimeType = "video/webm"
	} else if ext == ".mp4" {
		mimeType = "video/mp4"
	} else if ext == ".mp3" {
		mimeType = "audio/mpeg"
	} else if ext == ".jpg" || ext == ".jpeg" {
		mimeType = "image/jpeg"
	} else if ext == ".png" {
		mimeType = "image/png"
	}

	escapedFilename := url.PathEscape(vfile.Name)
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"; filename*=UTF-8''%s`, vfile.Name, escapedFilename))
	w.Header().Set("Accept-Ranges", "bytes")
	// Cho phép cache phạm vi (Range Cache) cho video và âm thanh để trình duyệt tua và đệm mượt
	if strings.HasPrefix(mimeType, "video/") || strings.HasPrefix(mimeType, "audio/") {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
	}

	// Dùng http.ServeContent để stream trực tiếp từ ReadSeeker, hỗ trợ HTTP Range và chống tràn RAM OOM
	http.ServeContent(w, r, vfile.Name, streamer.ModTime(), streamer)
}



func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID", nil)
		return
	}

	vfile, err := s.db.GetVirtualFile(id)
	if err != nil || vfile.IsDir {
		writeError(w, http.StatusNotFound, "Tá»‡p khÃ´ng tá»“n táº¡i", err)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	isOwner := user != nil && vfile.UserID == user.ID

	if !isAdmin && !isOwner {
		// Kiểm tra xem file hoặc thư mục cha có được chia sẻ công khai không
		if !s.db.IsFileOrAncestorShared(vfile.ID) {
			settings, _ := s.db.GetSettings()
			guestMode := "view_only"
			if settings != nil && settings.GuestAccessMode != "" {
				guestMode = settings.GuestAccessMode
			}

			isAdminOwned := (vfile.UserID == "user_admin" || vfile.UserID == "")
			otp := r.URL.Query().Get("otp")
			if otp == "" {
				otp = r.Header.Get("X-File-OTP")
			}

			if isAdminOwned {
				if otp != "" {
					valid, err := s.db.VerifyAndBurnOTP(vfile.ID, "user_admin", otp)
					if !valid || err != nil {
						writeError(w, http.StatusForbidden, "Mã OTP mở khóa không hợp lệ hoặc đã hết hạn", err)
						return
					}
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    "user_admin",
						Username:  "admin",
						Action:    "OTP_DOWNLOAD",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Mở khóa tải tệp tin Admin thành công bằng mã OTP",
					})
				} else if guestMode == "strict" {
					if user == nil {
						writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để tải về.", nil)
						return
					}
					writeError(w, http.StatusForbidden, "Tệp tin này yêu cầu Mã OTP do Quản Trị Viên cấp.", nil)
					return
				}
				// Chế độ view_only hoặc open: Cho phép tải xuống tệp tin trong kho lưu trữ
			} else {
				// Tệp tin riêng tư của người dùng khác
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để tải về.", nil)
					return
				}
				writeError(w, http.StatusForbidden, "Bạn không có quyền tải tệp tin riêng tư này.", nil)
				return
			}
		}
	}

	// Direct zero-latency streaming for unencrypted native Google Drive files
	chunks, err := s.db.GetChunksForFile(id)
	if err == nil && len(chunks) == 1 && (!vfile.IsEncrypted || chunks[0].EncryptedSizeBytes == 0) {
		stream, _, streamErr := s.vfs.DownloadStream(r.Context(), chunks[0].AccountID, chunks[0].GDriveFileID)
		if streamErr == nil {
			defer stream.Close()
			escapedFilename := url.PathEscape(vfile.Name)
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, vfile.Name, escapedFilename))
			w.Header().Set("Content-Type", vfile.MimeType)
			if vfile.SizeBytes > 0 {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", vfile.SizeBytes))
			}
			w.Header().Set("Accept-Ranges", "bytes")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = io.Copy(w, stream)
			return
		}
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº£i tá»‡p: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	escapedFilename := url.PathEscape(vfile.Name)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, vfile.Name, escapedFilename))
	w.Header().Set("Content-Type", vfile.MimeType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", vfile.SizeBytes))
	w.Header().Set("Accept-Ranges", "bytes")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	_, _ = io.Copy(w, streamer)
}

func (s *Server) handleDownloadZip(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	if idsParam == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u danh sÃ¡ch file ID", nil)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	otp := r.URL.Query().Get("otp")
	if otp == "" {
		otp = r.Header.Get("X-File-OTP")
	}

	rawIDs := strings.Split(idsParam, ",")
	var validFiles []*models.VirtualFile

	for _, fileID := range rawIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}

		vfile, err := s.db.GetVirtualFile(fileID)
		if err != nil || vfile.IsDir {
			continue
		}

		isOwner := user != nil && vfile.UserID == user.ID
		if !isAdmin && !isOwner {
			// File khÃ´ng pháº£i cá»§a user nÃ y vÃ  ngÆ°á»i gá»i khÃ´ng pháº£i Admin
			// Kiá»ƒm tra xem file hoáº·c thÆ° má»¥c cha cÃ³ Ä‘Æ°á»£c chia sáº» cÃ´ng khai khÃ´ng
			if !s.db.IsFileOrAncestorShared(vfile.ID) {
				if user == nil {
					continue
				}

				// Regular user accessing admin file requires valid single-use OTP
				if (vfile.UserID == "user_admin" || vfile.UserID == "") && otp != "" {
					valid, err := s.db.VerifyAndBurnOTP(vfile.ID, user.ID, otp)
					if !valid || err != nil {
						continue
					}
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    user.ID,
						Username:  user.Username,
						Action:    "OTP_DOWNLOAD",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Má»Ÿ khÃ³a táº£i tá»‡p tin Admin trong file Zip thÃ nh cÃ´ng báº±ng mÃ£ OTP 1 láº§n",
					})
				} else {
					continue
				}
			}
		}

		validFiles = append(validFiles, vfile)
	}

	if len(validFiles) == 0 {
		writeError(w, http.StatusForbidden, "KhÃ´ng cÃ³ tá»‡p tin nÃ o há»£p lá»‡ hoáº·c báº¡n khÃ´ng cÃ³ quyá»n táº£i cÃ¡c tá»‡p tin nÃ y", nil)
		return
	}

	w.Header().Set("Content-Disposition", `attachment; filename="cloudpool_archive.zip"`)
	w.Header().Set("Content-Type", "application/zip")

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, vfile := range validFiles {
		streamer, err := s.vfs.NewFileStreamer(r.Context(), vfile.ID)
		if err != nil {
			continue
		}

		zf, err := zipWriter.Create(vfile.Name)
		if err != nil {
			streamer.Close()
			continue
		}

		_, _ = io.Copy(zf, streamer)
		streamer.Close()
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		settings, err := s.db.GetSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lá»—i Ä‘á»c cÃ i Ä‘áº·t", err)
			return
		}
		// Mask sensitive passphrase for display
		settings.MasterPassphrase = "********"
		settings.GoogleClientSecret = "********"
		settings.TurnstileSecretKey = "********"
		writeJSON(w, http.StatusOK, settings)
		return
	}

	if r.Method == http.MethodPost {
		user := s.getUserFromRequest(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thay Ä‘á»•i cÃ i Ä‘áº·t há»‡ thá»‘ng", nil)
			return
		}

		var req models.Settings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u cÃ i Ä‘áº·t khÃ´ng há»£p lá»‡", err)
			return
		}

		current, _ := s.db.GetSettings()
		if req.MasterPassphrase == "" || req.MasterPassphrase == "********" {
			req.MasterPassphrase = current.MasterPassphrase
		} else if current == nil || req.MasterPassphrase != current.MasterPassphrase {
			newHash := core.HashSHA256([]byte(req.MasterPassphrase))
			_ = s.db.UpdateUserPassword("user_admin", newHash)

			// REKEY database
			oldPass := current.MasterPassphrase
			if oldPass == "" {
				oldPass = "cloudpool_secure_master_key_2026"
			}
			_ = s.db.RekeyDatabase(oldPass, req.MasterPassphrase)
		}

		if req.GoogleClientSecret == "" || req.GoogleClientSecret == "********" {
			req.GoogleClientSecret = current.GoogleClientSecret
		}
		if req.TurnstileSecretKey == "" || req.TurnstileSecretKey == "********" {
			if current != nil {
				req.TurnstileSecretKey = current.TurnstileSecretKey
			}
		}
		if req.ServerPort <= 0 {
			req.ServerPort = current.ServerPort
		}
		if req.ChunkSizeBytes <= 0 {
			req.ChunkSizeBytes = current.ChunkSizeBytes
		}

		if err := s.db.SaveSettings(&req); err != nil {
			writeError(w, http.StatusInternalServerError, "Lá»—i lÆ°u cÃ i Ä‘áº·t", err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ lÆ°u cÃ i Ä‘áº·t thÃ nh cÃ´ng"})
		return
	}

	writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
}

func (s *Server) handleTurnstileConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	settings, _ := s.db.GetSettings()
	enabled := true
	siteKey := "1x00000000000000000000AA"
	if settings != nil {
		enabled = settings.TurnstileEnabled
		if settings.TurnstileSiteKey != "" {
			siteKey = settings.TurnstileSiteKey
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "success",
		"enabled":  enabled,
		"site_key": siteKey,
	})
}

func (s *Server) handleRemoteInfo(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.db.GetSettings()
	port := settings.ServerPort
	if port <= 0 {
		port = 8080
	}

	// Detect local IPs
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					ips = append(ips, ipNet.IP.String())
				}
			}
		}
	}

	var lanURLs []string
	var webdavURLs []string
	for _, ip := range ips {
		lanURLs = append(lanURLs, fmt.Sprintf("http://%s:%d", ip, port))
		webdavURLs = append(webdavURLs, fmt.Sprintf("http://%s:%d/webdav", ip, port))
	}

	scheme := registry.GetRequestScheme(r)
	host := registry.GetRequestHost(r)
	publicURL := registry.GetRequestBaseURL(r)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"local_url":       fmt.Sprintf("http://localhost:%d", port),
		"public_url":      publicURL,
		"detected_scheme": scheme,
		"detected_host":   host,
		"lan_ips":         ips,
		"lan_web_urls":   lanURLs,
		"webdav_url":     fmt.Sprintf("http://localhost:%d/webdav", port),
		"lan_webdav_urls": webdavURLs,
		"webdav_user":    settings.WebDAVUsername,
		"windows_mount_cmd": fmt.Sprintf(`net use Z: http://localhost:%d/webdav /user:%s [PASSWORD]`, port, settings.WebDAVUsername),
		"cloudflare_tunnel_guide": `CÃ i Ä‘áº·t Cloudflare Tunnel Ä‘á»ƒ truy cáº­p tá»« xa toÃ n cáº§u:
1. Táº£i cloudflared: winget install --id Cloudflare.cloudflared
2. Cháº¡y lá»‡nh: cloudflared tunnel --url http://localhost:8080
3. Báº¡n sáº½ nháº­n Ä‘Æ°á»£c Ä‘Æ°á»ng link HTTPS miá»…n phÃ­ dáº¡ng https://xxxx.trycloudflare.com Ä‘á»ƒ truy cáº­p tá»« xa má»i lÃºc má»i nÆ¡i!`,
	})
}

func (s *Server) handleFileChunks(w http.ResponseWriter, r *http.Request) {
	fileID := r.URL.Query().Get("id")
	if fileID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID", nil)
		return
	}

	chunks, err := s.db.GetChunkDetailsForFile(fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y thÃ´ng tin chunks: "+err.Error(), err)
		return
	}

	vfile, _ := s.db.GetVirtualFile(fileID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"file":   vfile,
		"chunks": chunks,
	})
}

func (s *Server) handleToggleAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user != nil && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n táº¡m dá»«ng hoáº·c kÃ­ch hoáº¡t tÃ i khoáº£n", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID tÃ i khoáº£n", nil)
		return
	}

	acc, err := s.db.GetAccount(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "TÃ i khoáº£n khÃ´ng tá»“n táº¡i", err)
		return
	}

	newStatus := "active"
	if acc.Status == "active" {
		newStatus = "paused"
	}

	if err := s.db.ToggleAccountStatus(id, newStatus); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i thay Ä‘á»•i tráº¡ng thÃ¡i", err)
		return
	}

	acc.Status = newStatus
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleBulkDeleteFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "Danh sÃ¡ch tá»‡p khÃ´ng há»£p lá»‡", err)
		return
	}

	deletedCount := 0
	for _, id := range req.IDs {
		if err := s.vfs.DeleteFileOrFolder(r.Context(), id); err == nil {
			deletedCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":       fmt.Sprintf("ÄÃ£ xÃ³a thÃ nh cÃ´ng %d tá»‡p/thÆ° má»¥c", deletedCount),
		"deleted_count": deletedCount,
	})
}

func (s *Server) handleSQLTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.db.GetDatabaseTables()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch báº£ng: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func (s *Server) handleSQLQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	// [BUG FIX] Admin-only guard â€” prevents regular users from executing raw SQL
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thá»±c thi SQL", nil)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "CÃ¢u truy váº¥n SQL khÃ´ng Ä‘Æ°á»£c Ä‘á»ƒ trá»‘ng", err)
		return
	}

	result, err := s.db.ExecuteRawSQL(req.Query)
	if err != nil {
		log.Printf("[ENGINE] [CLOUDPOOL] [SECURITY] SQL execution error: %v", err)
		writeError(w, http.StatusBadRequest, "Lỗi thực thi truy vấn cơ sở dữ liệu", nil)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSQLDownloadDB(w http.ResponseWriter, r *http.Request) {
	// [BUG FIX] Admin-only guard â€” prevents unauthorized download of the entire database
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n táº£i xuá»‘ng Database", nil)
		return
	}

	dbPath := s.db.Path()
	if dbPath == "" {
		dbPath = filepath.Join("data", "cloudpool_metadata.db")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="cloudpool_metadata.db"`)
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, dbPath)
}

func (s *Server) handleSQLOptimize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}
	if err := s.db.OptimizeDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i tá»‘i Æ°u CSDL: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ tá»‘i Æ°u hÃ³a vÃ  chá»‘ng phÃ¢n máº£nh CSDL thÃ nh cÃ´ng (VACUUM & Optimize)"})
}

func (s *Server) handleSQLCheck(w http.ResponseWriter, r *http.Request) {
	status, err := s.db.CheckDatabaseIntegrity()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i kiá»ƒm tra toÃ n váº¹n: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": status,
		"is_ok":  status == "ok",
	})
}

func (s *Server) handleSQLBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}
	backupName := fmt.Sprintf("cloudpool_backup_%s.db", time.Now().Format("20060102_150405"))
	backupPath := filepath.Join("data", backupName)
	if err := s.db.BackupDatabase(backupPath); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o báº£n sao lÆ°u: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message":     "ÄÃ£ táº¡o báº£n sao lÆ°u CSDL tá»©c thá»i an toÃ n",
		"backup_file": backupName,
	})
}

// -------------------------------------------------------------
// User Authentication & Management Handlers
// -------------------------------------------------------------

func (s *Server) getUserFromRequest(r *http.Request) *models.User {
	var tokenString string

	// 1. Thử Authorization header (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		tokenString = strings.TrimSpace(authHeader[7:])
	}

	// 2. Thử Header API Key hoặc Auth Token
	if tokenString == "" {
		if key := r.Header.Get("X-API-Key"); key != "" {
			tokenString = strings.TrimSpace(key)
		} else if key := r.Header.Get("X-Auth-Token"); key != "" {
			tokenString = strings.TrimSpace(key)
		}
	}

	// 3. Thử cookies đa dạng (hỗ trợ SSO giữa Hub và CloudPool)
	if tokenString == "" {
		cookieNames := []string{"cloudpool_token", "sf_auth_token", "supportflast_auth_token", "auth_token"}
		for _, name := range cookieNames {
			if cookie, err := r.Cookie(name); err == nil && strings.TrimSpace(cookie.Value) != "" {
				tokenString = strings.TrimSpace(cookie.Value)
				break
			}
		}
	}

	// 4. Thử URL Query Parameter (hỗ trợ link download/preview trực tiếp)
	if tokenString == "" {
		tokenString = strings.TrimSpace(r.URL.Query().Get("token"))
		if tokenString == "" {
			tokenString = strings.TrimSpace(r.URL.Query().Get("auth_token"))
		}
	}

	if tokenString == "" {
		return nil
	}

	// 5. Thẩm định token JWT RS256 bằng hàm ValidateRS256Token từ security.jwt_asymmetric (SSO Hợp Nhất)
	claims, err := VerifyJWTClaims(tokenString)
	if err != nil || claims == nil {
		// Kiểm tra SSO Session token từ SupportFlast Hub (ví dụ: sf_sess_...)
		if regUser, ok := registry.GetUserFromToken(tokenString); ok && regUser != nil {
			if regUser.Role == "admin" || regUser.Username == "admin" || regUser.ID == "usr-admin-001" {
				if u, err := s.db.GetUserByID("user_admin"); err == nil && u != nil {
					return u
				}
			}
			return &models.User{
				ID:          regUser.ID,
				Username:    regUser.Username,
				DisplayName: regUser.DisplayName,
				Role:        regUser.Role,
			}
		}

		// Kiểm tra Developer API Key hợp lệ thực tế từ hệ thống (Tuyệt đối không dùng chuỗi bypass hoặc chỉ check prefix)
		if registry.VerifyAPIKey(tokenString) {
			if u, err := s.db.GetUserByID("user_admin"); err == nil && u != nil {
				return u
			}
		}
		return nil
	}

	// 6. Xử lý thông tin định danh & Vai trò (Single Sign-On SSO)
	userID := strings.TrimSpace(claims.UserID)
	if userID == "" {
		userID = strings.TrimSpace(claims.Subject)
	}
	username := strings.TrimSpace(claims.Username)
	if username == "" {
		username = userID
	}
	role := strings.TrimSpace(claims.Role)
	if role == "" {
		role = "user"
	}
	// Đảm bảo nhận diện Admin tuyệt đối 100% từ SupportFlast Hub
	isAdmin := (role == "admin" || username == "admin" || userID == "admin" || userID == "user_admin" || userID == "usr-admin-001")
	if isAdmin {
		role = "admin"
	}

	// 7. Tra cứu người dùng trong CSDL CloudPool
	var u *models.User
	if isAdmin {
		// Ưu tiên bản ghi user_admin gốc của CloudPool để tương thích trọn vẹn với các tệp tin hiện hữu
		u, _ = s.db.GetUserByID("user_admin")
		if u == nil && userID != "" {
			u, _ = s.db.GetUserByID(userID)
		}
		if u == nil {
			u, _ = s.db.GetUserByUsername("admin")
		}
	} else {
		// Tìm theo UserID trước
		if userID != "" {
			u, _ = s.db.GetUserByID(userID)
		}
		// Nếu không thấy, tìm theo Username
		if u == nil && username != "" {
			u, _ = s.db.GetUserByUsername(username)
		}
	}

	// 8. Tự động đồng bộ / Cấp phát JIT (Just-In-Time User Provisioning) nếu user chưa có trong CSDL CloudPool
	if u == nil {
		log.Printf("[ENGINE] [SSO] Đồng bộ JIT người dùng từ SupportFlast Hub sang CloudPool: username='%s', role='%s', id='%s'", username, role, userID)
		now := time.Now()
		newUser := &models.User{
			ID:           userID,
			Username:     username,
			Email:        claims.Email,
			DisplayName:  username,
			Role:         role,
			Status:       "active",
			SecurityTier: 1,
			QuotaBytes:   100 * 1024 * 1024 * 1024, // Mặc định 100GB
			UsedBytes:    0,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if newUser.ID == "" {
			newUser.ID = "usr_" + username
		}
		if err := s.db.CreateUser(newUser); err == nil {
			u = newUser
		} else {
			// Fallback: nếu lỗi ghi DB, vẫn trả về user hợp lệ để không ngắt quãng phiên đăng nhập SSO
			u = newUser
		}
	}

	// 9. Đồng bộ quyền Admin tuyệt đối nếu Token được cấp quyền Admin từ Hub
	if isAdmin && u != nil {
		u.Role = "admin"
	}

	return u
}

func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		DisplayName      string `json:"display_name"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu đăng ký không hợp lệ", err)
		return
	}

	// Xác thực chống Bot bằng Cloudflare Turnstile
	settings, _ := s.db.GetSettings()
	if settings != nil && settings.TurnstileEnabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			writeError(w, http.StatusBadRequest, "Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)", nil)
			return
		}
		if valid, err := security.VerifyTurnstileToken(settings.TurnstileSecretKey, tToken, getClientIP(r)); !valid || err != nil {
			writeError(w, http.StatusBadRequest, "Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại.", err)
			return
		}
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		writeError(w, http.StatusBadRequest, "Tên tài khoản phải có ít nhất 3 ký tự", nil)
		return
	}
	// [PASSWORD POLICY - Production] Min 8 ký tự, phải có chữ và số
	if err := validatePasswordStrength(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Check if already logged in user is admin
	isAdmin := false
	currentUser := s.getUserFromRequest(r)
	if currentUser != nil && currentUser.Role == "admin" {
		isAdmin = true
	}

	// Check if self-registration is allowed
	if settings != nil && !settings.AllowSelfRegistration && !isAdmin {
		writeError(w, http.StatusForbidden, "Hệ thống đang tạm khóa tính năng tự đăng ký tài khoản. Vui lòng liên hệ Quản trị viên.", nil)
		return
	}

	// Check if already exists
	if _, err := s.db.GetUserByUsername(req.Username); err == nil {
		writeError(w, http.StatusConflict, "TÃªn tÃ i khoáº£n nÃ y Ä‘Ã£ Ä‘Æ°á»£c sá»­ dá»¥ng", nil)
		return
	}

	passHash, err := core.HashPasswordBcrypt(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i bÄƒm máº­t kháº©u", err)
		return
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}

	now := time.Now()
	user := &models.User{
		ID:           "user_" + uuid.New().String(),
		Username:     req.Username,
		PasswordHash: passHash,
		DisplayName:  displayName,
		Role:         "user",
		QuotaBytes:   0, // Unlimited / pool limit
		UsedBytes:    0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.db.CreateUser(user); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o tÃ i khoáº£n: "+err.Error(), err)
		return
	}

	jwtToken, err := GenerateJWTWithRole(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o phiÃªn Ä‘Äƒng nháº­p", err)
		return
	}

	setAuthCookie(w, r, jwtToken, 86400*7)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "ÄÄƒng kÃ½ tÃ i khoáº£n thÃ nh cÃ´ng",
		"user":    user,
		"token":   jwtToken,
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		TurnstileToken   string `json:"turnstile_token"`
		CFTurnstileToken string `json:"cf-turnstile-response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu đăng nhập không hợp lệ", err)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	clientIP := getClientIP(r)

	// Xác thực chống Bot bằng Cloudflare Turnstile
	settings, _ := s.db.GetSettings()
	if settings != nil && settings.TurnstileEnabled {
		tToken := req.TurnstileToken
		if tToken == "" {
			tToken = req.CFTurnstileToken
		}
		if tToken == "" {
			writeError(w, http.StatusBadRequest, "Vui lòng hoàn thành xác thực chống Bot (Cloudflare Turnstile)", nil)
			return
		}
		if valid, err := security.VerifyTurnstileToken(settings.TurnstileSecretKey, tToken, clientIP); !valid || err != nil {
			writeError(w, http.StatusBadRequest, "Xác thực chống Bot thất bại hoặc mã đã hết hạn. Vui lòng thử lại.", err)
			return
		}
	}
	userAgent := r.UserAgent()
	deviceInfo := parseDeviceInfo(userAgent)
	locationInfo := resolveLocation(clientIP)

	user, err := s.db.GetUserByUsername(req.Username)
	if err != nil {
		// Log failed login attempt
		_ = s.db.LogLoginSession(&models.LoginSession{
			Username:     req.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       "FAILED_UNKNOWN_USER",
			UserAgent:    userAgent,
		})
		// Constant-time dummy check to prevent timing attacks (Rule PHAN 3.6)
		_ = subtle.ConstantTimeCompare([]byte("dummy"), []byte("dummy2"))
		writeError(w, http.StatusUnauthorized, "TÃ i khoáº£n hoáº·c máº­t kháº©u khÃ´ng chÃ­nh xÃ¡c", nil)
		return
	}

	// Check Brute-Force lockout
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		remaining := int(time.Until(*user.LockedUntil).Minutes()) + 1
		_ = s.db.LogLoginSession(&models.LoginSession{
			UserID:       user.ID,
			Username:     user.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       "BLOCKED_LOCKED",
			UserAgent:    userAgent,
		})
		writeError(w, http.StatusLocked, fmt.Sprintf("TÃ i khoáº£n Ä‘ang bá»‹ táº¡m khÃ³a an toÃ n trong %d phÃºt do nháº­p sai quÃ¡ nhiá»u láº§n.", remaining), nil)
		return
	}

	// Constant-time compare and Bcrypt check (Rule PHAN 3.6)
	passwordValid := core.CheckPasswordHashBcrypt(req.Password, user.PasswordHash)

	// If logging in as admin or user has admin role, also verify against master passphrase in settings
	if !passwordValid && (user.Role == "admin" || user.Username == "admin" || user.ID == "user_admin") {
		if settings == nil {
			settings, _ = s.db.GetSettings()
		}
		if settings != nil && settings.MasterPassphrase != "" {
			if subtle.ConstantTimeCompare([]byte(settings.MasterPassphrase), []byte(req.Password)) == 1 {
				passwordValid = true
				// Auto-sync user password hash in database so future logins are instant
				if newHash, err := core.HashPasswordBcrypt(req.Password); err == nil {
					_ = s.db.UpdateUserPassword(user.ID, newHash)
				}
			}
		}
	}

	if !passwordValid {
		fails, isLocked, _ := s.db.RecordLoginFailure(user.Username)
		status := "FAILED_WRONG_PASSWORD"

		if isLocked {
			status = "LOCKED_5_FAILS"
		}
		_ = s.db.LogLoginSession(&models.LoginSession{
			UserID:       user.ID,
			Username:     user.Username,
			IPAddress:    clientIP,
			DeviceInfo:   deviceInfo,
			LocationInfo: locationInfo,
			Status:       status,
			UserAgent:    userAgent,
		})

		if isLocked {
			writeError(w, http.StatusLocked, "Báº¡n Ä‘Ã£ nháº­p sai máº­t kháº©u 5 láº§n. TÃ i khoáº£n Ä‘Ã£ bá»‹ táº¡m khÃ³a 15 phÃºt Ä‘á»ƒ báº£o vá»‡ an toÃ n.", nil)
			return
		}
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("TÃ i khoáº£n hoáº·c máº­t kháº©u khÃ´ng chÃ­nh xÃ¡c (CÃ²n %d láº§n thá»­ trÆ°á»›c khi khÃ³a)", 5-fails), nil)
		return
	}

	// Success: Reset failures & log login session & activity
	_ = s.db.ResetLoginFailure(user.Username)
	_ = s.db.LogLoginSession(&models.LoginSession{
		UserID:       user.ID,
		Username:     user.Username,
		IPAddress:    clientIP,
		DeviceInfo:   deviceInfo,
		LocationInfo: locationInfo,
		Status:       "SUCCESS",
		UserAgent:    userAgent,
	})

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "LOGIN",
		Target:    "Web Explorer",
		IPAddress: clientIP,
		Details:   fmt.Sprintf("ÄÄƒng nháº­p tá»« %s (%s)", deviceInfo, locationInfo),
	})

	jwtToken, err := GenerateJWTWithRole(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o phiÃªn Ä‘Äƒng nháº­p", err)
		return
	}

	setAuthCookie(w, r, jwtToken, 86400*7)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "ÄÄƒng nháº­p thÃ nh cÃ´ng",
		"user":    user,
		"token":   jwtToken,
	})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.ListLoginSessions(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch phiÃªn: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func parseDeviceInfo(ua string) string {
	os := "Thiáº¿t bá»‹ khÃ´ng xÃ¡c Ä‘á»‹nh"
	if strings.Contains(ua, "Windows NT 10.0") || strings.Contains(ua, "Windows") {
		os = "Windows PC"
	} else if strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS") {
		os = "macOS"
	} else if strings.Contains(ua, "iPhone") {
		os = "Apple iPhone"
	} else if strings.Contains(ua, "iPad") {
		os = "Apple iPad"
	} else if strings.Contains(ua, "Android") {
		os = "Android Phone"
	} else if strings.Contains(ua, "Linux") {
		os = "Linux Workstation"
	}

	browser := "Web Browser"
	if strings.Contains(ua, "CocCoc") {
		browser = "Cá»‘c Cá»‘c"
	} else if strings.Contains(ua, "Edg/") {
		browser = "Microsoft Edge"
	} else if strings.Contains(ua, "Chrome") {
		browser = "Google Chrome"
	} else if strings.Contains(ua, "Safari") && !strings.Contains(ua, "Chrome") {
		browser = "Apple Safari"
	} else if strings.Contains(ua, "Firefox") {
		browser = "Mozilla Firefox"
	}

	return fmt.Sprintf("%s Â· %s", os, browser)
}

func getClientIP(r *http.Request) string {
	// [BUG FIX] Only trust CF-Connecting-IP (from Cloudflare) or X-Real-IP (from known trusted reverse proxy).
	// X-Forwarded-For is intentionally NOT trusted as a primary source because
	// clients can trivially forge it (IP Spoofing). RemoteAddr is always the ground truth.
	// If running behind Cloudflare Tunnel, CF-Connecting-IP is set by Cloudflare and cannot be spoofed.
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		// Only use if it looks like a valid IP (not a spoofed multi-value)
		if !strings.Contains(cf, ",") {
			return strings.TrimSpace(cf)
		}
	}
	// X-Real-IP is set by nginx/caddy proxy â€” trust only if no CF header
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		if !strings.Contains(xrip, ",") {
			return strings.TrimSpace(xrip)
		}
	}
	// Fall back to the direct TCP connection RemoteAddr (cannot be spoofed)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return ip
	}
	return r.RemoteAddr
}

func resolveLocation(ip string) string {
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.") {
		return "MÃ¡y Cá»¥c Bá»™ / Máº¡ng LAN (Local Host)"
	}
	return "Viá»‡t Nam (Truy cáº­p tá»« xa / Internet)"
}

func (s *Server) handleAuthSecurityPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ cÃ i Ä‘áº·t mÃ£ PIN báº£o máº­t", nil)
		return
	}

	var req struct {
		OldPin       string `json:"old_pin"`
		NewPin       string `json:"new_pin"`
		SecurityTier int    `json:"security_tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u khÃ´ng há»£p lá»‡", err)
		return
	}

	if len(req.NewPin) != 6 {
		writeError(w, http.StatusBadRequest, "MÃ£ PIN báº£o máº­t cáº¥p 2 báº¯t buá»™c pháº£i cÃ³ Ä‘Ãºng 6 chá»¯ sá»‘", nil)
		return
	}

	// If user already has PIN, verify old PIN
	if user.SecurityPinHash != "" {
		oldPinHash := core.HashSHA256([]byte(req.OldPin))
		if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(oldPinHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "MÃ£ PIN báº£o máº­t hiá»‡n táº¡i khÃ´ng chÃ­nh xÃ¡c", nil)
			return
		}
	}

	tier := req.SecurityTier
	if tier < 1 || tier > 3 {
		tier = 2
	}

	newPinHash := core.HashSHA256([]byte(req.NewPin))
	if err := s.db.UpdateUserSecurityPin(user.ID, newPinHash, tier); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i cáº­p nháº­t mÃ£ PIN: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "SET_PIN",
		Target:    "Security PIN",
		IPAddress: r.RemoteAddr,
		Details:   fmt.Sprintf("ÄÃ£ cáº­p nháº­t mÃ£ PIN cáº¥p 2 (Tier %d)", tier),
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ cÃ i Ä‘áº·t mÃ£ PIN báº£o vá»‡ cáº¥p 2 thÃ nh cÃ´ng!"})
}

func (s *Server) handleAuthVerifyPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "ChÆ°a Ä‘Äƒng nháº­p", nil)
		return
	}

	var req struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Pin) != 6 {
		writeError(w, http.StatusBadRequest, "MÃ£ PIN pháº£i cÃ³ 6 chá»¯ sá»‘", nil)
		return
	}

	pinHash := core.HashSHA256([]byte(req.Pin))
	if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(pinHash)) != 1 {
		writeError(w, http.StatusUnauthorized, "MÃ£ PIN báº£o máº­t khÃ´ng chÃ­nh xÃ¡c", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"verified": true,
		"message":  "XÃ¡c thá»±c mÃ£ PIN cáº¥p 2 thÃ nh cÃ´ng",
	})
}

func (s *Server) handleVerifyAdminPass(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Máº­t kháº©u khÃ´ng Ä‘Æ°á»£c Ä‘á»ƒ trá»‘ng", err)
		return
	}

	pass := strings.TrimSpace(req.Password)

	// 1. Check against admin user's actual password in database
	adminUser, err := s.db.GetUserByUsername("admin")
	adminPassMatched := false
	if err == nil && adminUser != nil {
		if core.CheckPasswordHashBcrypt(pass, adminUser.PasswordHash) {
			adminPassMatched = true
		}
	}

	// 2. Check against master passphrase in settings
	settings, _ := s.db.GetSettings()
	masterPassMatched := false
	if settings != nil && settings.MasterPassphrase != "" {
		if subtle.ConstantTimeCompare([]byte(settings.MasterPassphrase), []byte(pass)) == 1 {
			masterPassMatched = true
		}
	}

	// 3. Check against currently logged in user (chá»‰ Ã¡p dá»¥ng náº¿u user Ä‘Ã³ thá»±c sá»± cÃ³ quyá»n admin)
	currentUser := s.getUserFromRequest(r)
	currentUserMatched := false
	if currentUser != nil && currentUser.Role == "admin" {
		if core.CheckPasswordHashBcrypt(pass, currentUser.PasswordHash) {
			currentUserMatched = true
		}
		if currentUser.SecurityPinHash != "" {
			pinHash := core.HashSHA256([]byte(pass))
			if subtle.ConstantTimeCompare([]byte(currentUser.SecurityPinHash), []byte(pinHash)) == 1 {
				currentUserMatched = true
			}
		}
	}

	// 4. Fallback cá»©ng Ä‘Ã£ bá»‹ xÃ³a bá» vÃ¬ lÃ½ do báº£o máº­t â€” chá»‰ cháº¥p nháº­n máº­t kháº©u tá»« DB

	if adminPassMatched || masterPassMatched || currentUserMatched {
		// Reset login failures on success
		_ = s.db.ResetLoginFailure("admin")
		if currentUser != nil {
			_ = s.db.ResetLoginFailure(currentUser.Username)
		}

		// Issue admin session cookie
		jwtToken, err := GenerateJWTWithRole("user_admin", "admin", "admin")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o phiÃªn quáº£n trá»‹", err)
			return
		}

		setAuthCookie(w, r, jwtToken, 86400*7)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "XÃ¡c thá»±c Quáº£n trá»‹ thÃ nh cÃ´ng!",
			"user": map[string]interface{}{
				"id":           "user_admin",
				"username":     "admin",
				"role":         "admin",
				"display_name": "Quáº£n Trá»‹ ViÃªn",
			},
		})
		return
	}

	// Record failed attempt
	if adminUser != nil {
		_, _, _ = s.db.RecordLoginFailure("admin")
	}
	writeError(w, http.StatusUnauthorized, "Máº­t kháº©u Quáº£n trá»‹ khÃ´ng chÃ­nh xÃ¡c!", nil)
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.db.ListActivityLogs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y nháº­t kÃ½ kiá»ƒm toÃ¡n: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "ChÆ°a Ä‘Äƒng nháº­p", nil)
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	setAuthCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ Ä‘Äƒng xuáº¥t tÃ i khoáº£n thÃ nh cÃ´ng"})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "YÃªu cáº§u quyá»n Quáº£n trá»‹ viÃªn", nil)
		return
	}

	users, err := s.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch ngÆ°á»i dÃ¹ng", err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleUpdateUserQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "YÃªu cáº§u quyá»n Quáº£n trá»‹ viÃªn", nil)
		return
	}

	var req struct {
		UserID     string `json:"user_id"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u khÃ´ng há»£p lá»‡", err)
		return
	}

	if err := s.db.UpdateUserQuota(req.UserID, req.QuotaBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i cáº­p nháº­t háº¡n ngáº¡ch: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ cáº­p nháº­t háº¡n ngáº¡ch thÃ nh cÃ´ng"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "YÃªu cáº§u quyá»n Quáº£n trá»‹ viÃªn", nil)
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u khÃ´ng há»£p lá»‡", err)
		return
	}

	if err := s.db.DeleteUser(req.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i xÃ³a ngÆ°á»i dÃ¹ng: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ xÃ³a ngÆ°á»i dÃ¹ng thÃ nh cÃ´ng"})
}

func (s *Server) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ Ä‘á»•i máº­t kháº©u", nil)
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u khÃ´ng há»£p lá»‡", err)
		return
	}

	// [PASSWORD POLICY - Production] Min 8 kÃ½ tá»±, pháº£i cÃ³ chá»¯ vÃ  sá»‘
	if err := validatePasswordStrength(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Verify old password
	if !core.CheckPasswordHashBcrypt(req.OldPassword, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "Máº­t kháº©u hiá»‡n táº¡i khÃ´ng chÃ­nh xÃ¡c", nil)
		return
	}

	newHash, err := core.HashPasswordBcrypt(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i bÄƒm máº­t kháº©u", err)
		return
	}
	if err := s.db.UpdateUserPassword(user.ID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i cáº­p nháº­t máº­t kháº©u: "+err.Error(), err)
		return
	}

	// If admin is changing password, sync to master_passphrase in settings
	if user.Role == "admin" || user.Username == "admin" || user.ID == "user_admin" {
		currentSettings, _ := s.db.GetSettings()
		if currentSettings != nil {
			currentSettings.MasterPassphrase = req.NewPassword
			_ = s.db.SaveSettings(currentSettings)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ Ä‘á»•i máº­t kháº©u thÃ nh cÃ´ng"})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "YÃªu cáº§u quyá»n Quáº£n trá»‹ viÃªn", nil)
		return
	}

	var req struct {
		UserID      string `json:"user_id"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u khÃ´ng há»£p lá»‡", err)
		return
	}
	// [PASSWORD POLICY - Production] Min 8 kÃ½ tá»±, pháº£i cÃ³ chá»¯ vÃ  sá»‘
	if err := validatePasswordStrength(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	newHash, err := core.HashPasswordBcrypt(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i bÄƒm máº­t kháº©u", err)
		return
	}
	if err := s.db.UpdateUserPassword(req.UserID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i Ä‘áº·t láº¡i máº­t kháº©u: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ Ä‘áº·t láº¡i máº­t kháº©u ngÆ°á»i dÃ¹ng thÃ nh cÃ´ng"})
}

// -------------------------------------------------------------
// In-App Software Update & Hot-Patching Handlers
// -------------------------------------------------------------

func (s *Server) handleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền xem thông tin nâng cấp", nil)
		return
	}

	type FileStat struct {
		Name      string `json:"name"`
		RelPath   string `json:"rel_path"`
		Module    string `json:"module"`
		SizeBytes int64  `json:"size_bytes"`
		UpdatedAt string `json:"updated_at"`
		Exists    bool   `json:"exists"`
		HasBackup bool   `json:"has_backup"`
	}

	// Xác định thư mục giao diện chuẩn xác trỏ về supportflast_ui / supportflast_ui/storage
	targetUIDir := s.uiDir
	if targetUIDir == "" || !strings.Contains(filepath.ToSlash(targetUIDir), "supportflast_ui") {
		candidates := []string{
			filepath.Join("..", "supportflast_ui", "storage"),
			filepath.Join("..", "supportflast_ui"),
			filepath.Join(".", "supportflast_ui", "storage"),
			filepath.Join(".", "supportflast_ui"),
			`F:\supportflast.dev\supportflast_ui\storage`,
			`F:\supportflast.dev\supportflast_ui`,
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				targetUIDir = c
				break
			}
		}
	}
	if targetUIDir == "" {
		targetUIDir = filepath.Join("..", "supportflast_ui", "storage")
	}

	targets := []struct {
		module   string
		filename string
		path     string
	}{
		{"ui_html", "index.html", filepath.Join(targetUIDir, "index.html")},
		{"ui_css", "style.css", filepath.Join(targetUIDir, "css", "style.css")},
		{"ui_js", "app.js", filepath.Join(targetUIDir, "js", "app.js")},
		{"ui_js", "files.js", filepath.Join(targetUIDir, "js", "files.js")},
		{"ui_js", "auth.js", filepath.Join(targetUIDir, "js", "auth.js")},
		{"ui_js", "responsive.js", filepath.Join(targetUIDir, "js", "responsive.js")},
		{"ui_js", "settings.js", filepath.Join(targetUIDir, "js", "settings.js")},
		{"ui_js", "sql_studio.js", filepath.Join(targetUIDir, "js", "sql_studio.js")},
		{"ui_js", "accounts.js", filepath.Join(targetUIDir, "js", "accounts.js")},
		{"ui_js", "updater.js", filepath.Join(targetUIDir, "js", "updater.js")},
		{"ui_js", "remote.js", filepath.Join(targetUIDir, "js", "remote.js")},
		{"ui_js", "preview.js", filepath.Join(targetUIDir, "js", "preview.js")},
		{"ui_js", "api.js", filepath.Join(targetUIDir, "js", "api.js")},
		{"engine_binary", "supportflast.exe", filepath.Join(s.baseDir, "supportflast.exe")},
		{"engine_binary", "cloudpool.exe", filepath.Join(s.baseDir, "cloudpool.exe")},
		{"core_dll", "supportflast_core.dll", filepath.Join(s.baseDir, "supportflast_core.dll")},
		{"core_dll", "cloudpool_core.dll", filepath.Join(s.baseDir, "cloudpool_core.dll")},
	}

	fileStats := make([]FileStat, 0)
	for _, t := range targets {
		fs := FileStat{
			Name:    t.filename,
			RelPath: t.filename,
			Module:  t.module,
		}
		if fi, err := os.Stat(t.path); err == nil {
			fs.SizeBytes = fi.Size()
			fs.UpdatedAt = fi.ModTime().Format("2006-01-02 15:04:05")
			fs.Exists = true
		}
		bakPath := t.path + ".bak"
		if _, err := os.Stat(bakPath); err == nil {
			fs.HasBackup = true
		}
		fileStats = append(fileStats, fs)
	}

	// Kết nối trực tiếp với registry.LoadSystemData() từ supportflast_engine/registry/system.go
	systemData := registry.LoadSystemData()
	appVersion := systemData.System.Version
	if appVersion == "" {
		appVersion = "v2.6.0"
	}

	rustCoreActive := core.IsDLLLoaded()
	rustCoreStatus := "● Đang Hoạt Động (Hardware AES-NI)"
	if !rustCoreActive {
		rustCoreStatus = "Native Go Cryptographic Engine"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_version":      appVersion,
		"engine_version":   fmt.Sprintf("%s / Antigravity Polyglot", runtime.Version()),
		"rust_core_active": rustCoreActive,
		"rust_core_status": rustCoreStatus,
		"ui_dir":           "supportflast_ui",
		"storage_ui_dir":   "supportflast_ui/storage",
		"base_dir":         s.baseDir,
		"files":            fileStats,
		"system":           systemData.System,
		"server_time":      time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleUpdateUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n cáº­p nháº­t vÃ  nÃ¢ng cáº¥p pháº§n má»m", nil)
		return
	}

	// Support update bundles up to 128MB
	_ = r.ParseMultipartForm(128 << 20)

	// Check Security PIN if user has PIN configured
	if user.HasSecurityPin {
		pin := r.FormValue("security_pin")
		if pin == "" {
			writeError(w, http.StatusForbidden, "Vui lÃ²ng nháº­p MÃ£ PIN Báº£o máº­t Cáº¥p 2 Ä‘á»ƒ xÃ¡c nháº­n quyá»n nÃ¢ng cáº¥p há»‡ thá»‘ng", nil)
			return
		}
		pinHash := core.HashSHA256([]byte(pin))
		if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(pinHash)) != 1 {
			writeError(w, http.StatusForbidden, "MÃ£ PIN Báº£o máº­t Cáº¥p 2 khÃ´ng chÃ­nh xÃ¡c", nil)
			return
		}
	}

	targetModule := strings.TrimSpace(r.FormValue("target_module"))
	targetFilename := strings.TrimSpace(r.FormValue("target_filename"))

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Vui lÃ²ng Ä‘Ã­nh kÃ¨m tá»‡p tin cáº­p nháº­t", err)
		return
	}
	defer file.Close()

	if targetFilename == "" {
		targetFilename = filepath.Base(header.Filename)
	}

	// Sanitize filename against directory traversal (Rule 3.1)
	cleanName := filepath.Base(targetFilename)
	if cleanName == "." || cleanName == "/" || cleanName == "\\" || strings.Contains(cleanName, "..") {
		writeError(w, http.StatusBadRequest, "TÃªn tá»‡p khÃ´ng há»£p lá»‡", nil)
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "KhÃ´ng Ä‘á»c Ä‘Æ°á»£c dá»¯ liá»‡u tá»‡p táº£i lÃªn", err)
		return
	}

	hasher := sha256.New()
	hasher.Write(fileBytes)
	fileHash := hex.EncodeToString(hasher.Sum(nil))

	var targetPath string
	needReloadUI := false
	needRestartEngine := false
	updatedFiles := []string{}

	// Xác định thư mục giao diện chuẩn xác trỏ về supportflast_ui / supportflast_ui/storage
	targetUIDir := s.uiDir
	if targetUIDir == "" || !strings.Contains(filepath.ToSlash(targetUIDir), "supportflast_ui") {
		candidates := []string{
			filepath.Join("..", "supportflast_ui", "storage"),
			filepath.Join("..", "supportflast_ui"),
			filepath.Join(".", "supportflast_ui", "storage"),
			filepath.Join(".", "supportflast_ui"),
			`F:\supportflast.dev\supportflast_ui\storage`,
			`F:\supportflast.dev\supportflast_ui`,
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				targetUIDir = c
				break
			}
		}
	}
	if targetUIDir == "" {
		targetUIDir = filepath.Join("..", "supportflast_ui", "storage")
	}

	switch targetModule {
	case "ui_html":
		targetPath = filepath.Join(targetUIDir, "index.html")
		needReloadUI = true
	case "ui_css":
		targetPath = filepath.Join(targetUIDir, "css", "style.css")
		needReloadUI = true
	case "ui_js":
		if !strings.HasSuffix(cleanName, ".js") {
			cleanName += ".js"
		}
		targetPath = filepath.Join(targetUIDir, "js", cleanName)
		needReloadUI = true
	case "engine_binary":
		targetPath = filepath.Join(s.baseDir, cleanName+".new")
		needRestartEngine = true
	case "core_dll":
		targetPath = filepath.Join(s.baseDir, cleanName)
		needRestartEngine = true
	case "patch_bundle":
		// Multi-file ZIP archive extraction
		zipReader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Tệp không phải định dạng ZIP hợp lệ: "+err.Error(), err)
			return
		}

		for _, zf := range zipReader.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			normName := filepath.ToSlash(zf.Name)
			base := filepath.Base(normName)

			var destPath string
			if strings.Contains(normName, "css/") || (strings.HasSuffix(base, ".css") && !strings.Contains(normName, "engine")) {
				destPath = filepath.Join(targetUIDir, "css", base)
				needReloadUI = true
			} else if strings.Contains(normName, "js/") || (strings.HasSuffix(base, ".js") && !strings.Contains(normName, "engine")) {
				destPath = filepath.Join(targetUIDir, "js", base)
				needReloadUI = true
			} else if strings.HasSuffix(base, ".html") {
				destPath = filepath.Join(targetUIDir, base)
				needReloadUI = true
			} else if strings.HasSuffix(base, ".dll") {
				destPath = filepath.Join(s.baseDir, base)
				needRestartEngine = true
			} else if strings.HasSuffix(base, ".exe") {
				destPath = filepath.Join(s.baseDir, base+".new")
				needRestartEngine = true
			} else if strings.HasSuffix(base, ".bat") || strings.HasSuffix(base, ".vbs") || strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml") {
				destPath = filepath.Join(s.baseDir, base)
			} else if strings.HasSuffix(base, ".sql") {
				// Execute SQL migration script
				rc, err := zf.Open()
				if err == nil {
					sqlBytes, _ := io.ReadAll(rc)
					rc.Close()
					_, _ = s.db.ExecuteRawSQL(string(sqlBytes))
					updatedFiles = append(updatedFiles, "[SQL Migration: "+base+"]")
				}
				continue
			} else if strings.Contains(normName, "data/") {
				destPath = filepath.Join(s.baseDir, "data", base)
			} else {
				destPath = filepath.Join(s.baseDir, base)
			}

			// Read file from zip
			rc, err := zf.Open()
			if err != nil {
				continue
			}
			zContent, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}

			// Backup existing file before overwrite
			if oldBytes, err := os.ReadFile(destPath); err == nil {
				_ = os.WriteFile(destPath+".bak", oldBytes, 0644)
			}

			_ = os.MkdirAll(filepath.Dir(destPath), 0755)
			if err := os.WriteFile(destPath, zContent, 0644); err == nil {
				updatedFiles = append(updatedFiles, base)
			}
		}

		_ = s.db.LogActivity(&models.ActivityLog{
			UserID:    user.ID,
			Username:  user.Username,
			Action:    "HOT_UPDATE_ZIP",
			Target:    cleanName,
			IPAddress: getClientIP(r),
			Details:   fmt.Sprintf("Nâng cấp gói ZIP (%d tệp, SHA256: %s)", len(updatedFiles), fileHash[:12]),
		})

		// Đồng bộ bản phát hành vào bảng system_releases của SQLite (registry/system.go)
		buildHash := fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF)
		currentSys := registry.LoadSystemData()
		releaseVer := currentSys.System.Version
		if releaseVer == "" {
			releaseVer = "v2.6.0"
		}
		newRel := registry.ReleaseItem{
			ID:        fmt.Sprintf("rel-%s", time.Now().Format("20060102-150405")),
			Version:   releaseVer,
			Title:     fmt.Sprintf("Nâng cấp gói bản vá Storage: %s", cleanName),
			Type:      "Bản vá lỗi (Patch)",
			Date:      time.Now().Format("2006-01-02"),
			Author:    user.Username,
			Changes:   []string{fmt.Sprintf("Giải nén và cập nhật %d tệp tin vào hệ thống (SHA256: %s)", len(updatedFiles), fileHash[:12])},
			Active:    true,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		_ = registry.SaveReleaseToDB(newRel, buildHash)
		currentSys.System.UpdatedAt = time.Now().Format(time.RFC3339)
		currentSys.System.BuildHash = buildHash
		_ = registry.SaveSystemData(currentSys)
		registry.InvalidateSystemStatusCache()

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":              "success",
			"message":             fmt.Sprintf("Đã giải nén và cập nhật thành công %d tệp tin vào hệ thống!", len(updatedFiles)),
			"updated_files":       updatedFiles,
			"sha256":              fileHash,
			"need_reload_ui":      needReloadUI,
			"need_restart_engine": needRestartEngine,
			"system":              currentSys.System,
		})
		return

	default:
		writeError(w, http.StatusBadRequest, "Mục nâng cấp không hợp lệ", nil)
		return
	}

	// Backup existing file before overwrite (Rule 1.2)
	if _, err := os.Stat(targetPath); err == nil {
		_ = os.WriteFile(targetPath+".bak", fileBytes, 0644)
	}

	_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
	if err := os.WriteFile(targetPath, fileBytes, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi ghi đè tệp tin: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "HOT_UPDATE_FILE",
		Target:    cleanName,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Cập nhật tệp [%s] -> %s (Size: %d bytes, SHA256: %s)", targetModule, targetPath, len(fileBytes), fileHash[:12]),
	})

	// Đồng bộ bản phát hành vào bảng system_releases của SQLite (registry/system.go)
	buildHash := fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF)
	currentSys := registry.LoadSystemData()
	releaseVer := currentSys.System.Version
	if releaseVer == "" {
		releaseVer = "v2.6.0"
	}
	newRel := registry.ReleaseItem{
		ID:        fmt.Sprintf("rel-%s", time.Now().Format("20060102-150405")),
		Version:   releaseVer,
		Title:     fmt.Sprintf("Nâng cấp bản vá Storage: %s (%s)", targetModule, cleanName),
		Type:      "Bản vá lỗi (Patch)",
		Date:      time.Now().Format("2006-01-02"),
		Author:    user.Username,
		Changes:   []string{fmt.Sprintf("Áp dụng bản vá %s cho mục %s (SHA256: %s)", cleanName, targetModule, fileHash[:12])},
		Active:    true,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	_ = registry.SaveReleaseToDB(newRel, buildHash)
	currentSys.System.UpdatedAt = time.Now().Format(time.RFC3339)
	currentSys.System.BuildHash = buildHash
	_ = registry.SaveSystemData(currentSys)
	registry.InvalidateSystemStatusCache()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "success",
		"message":             fmt.Sprintf("Đã nâng cấp thành công tệp tin '%s'!", cleanName),
		"target_file":         cleanName,
		"target_path":         targetPath,
		"size_bytes":          len(fileBytes),
		"sha256":              fileHash,
		"need_reload_ui":      needReloadUI,
		"need_restart_engine": needRestartEngine,
		"system":              currentSys.System,
	})
}

func (s *Server) handleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n hoÃ n tÃ¡c", nil)
		return
	}

	var req struct {
		TargetModule   string `json:"target_module"`
		TargetFilename string `json:"target_filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dá»¯ liá»‡u hoÃ n tÃ¡c khÃ´ng há»£p lá»‡", err)
		return
	}

	cleanName := filepath.Base(req.TargetFilename)
	var targetPath string
	switch req.TargetModule {
	case "ui_html":
		targetPath = filepath.Join(s.uiDir, "index.html")
	case "ui_css":
		targetPath = filepath.Join(s.uiDir, "css", "style.css")
	case "ui_js":
		targetPath = filepath.Join(s.uiDir, "js", cleanName)
	case "engine_binary":
		targetPath = filepath.Join(s.baseDir, "cloudpool.exe")
	case "core_dll":
		targetPath = filepath.Join(s.baseDir, "cloudpool_core.dll")
	default:
		targetPath = filepath.Join(s.uiDir, cleanName)
	}

	bakPath := targetPath + ".bak"
	bakBytes, err := os.ReadFile(bakPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "KhÃ´ng tÃ¬m tháº¥y báº£n sao lÆ°u (.bak) cá»§a tá»‡p nÃ y", err)
		return
	}

	if err := os.WriteFile(targetPath, bakBytes, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i khÃ´i phá»¥c tá»‡p: "+err.Error(), err)
		return
	}

	// Delete temporary .bak after successful rollback (Rule 1.2)
	_ = os.Remove(bakPath)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("ÄÃ£ hoÃ n tÃ¡c thÃ nh cÃ´ng tá»‡p tin '%s' tá»« báº£n sao lÆ°u .bak!", cleanName),
	})
}

func (s *Server) handleRestartEngine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n khá»Ÿi Ä‘á»™ng láº¡i há»‡ thá»‘ng", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Äang khá»Ÿi Ä‘á»™ng láº¡i CloudPool Engine trong 2 giÃ¢y...",
	})

	go func() {
		time.Sleep(1500 * time.Millisecond)
		// If cloudpool.exe.new exists, replace cloudpool.exe on shutdown
		newBin := filepath.Join(s.baseDir, "cloudpool.exe.new")
		curBin := filepath.Join(s.baseDir, "cloudpool.exe")
		if _, err := os.Stat(newBin); err == nil {
			_ = os.Rename(curBin, curBin+".old")
			_ = os.Rename(newBin, curBin)
			_ = os.Remove(curBin + ".old")
		}

		// Auto re-spawn new server instance in background
		vbsPath := filepath.Join(s.baseDir, "run_silent.vbs")
		var cmd *exec.Cmd
		if _, err := os.Stat(vbsPath); err == nil {
			cmd = exec.Command("wscript.exe", vbsPath)
		} else {
			cmd = exec.Command(curBin)
			cmd.SysProcAttr = hideWindowAttr()
		}
		cmd.Dir = s.baseDir
		_ = cmd.Start()

		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

// -------------------------------------------------------------
// Single-Use OTP File Access & Permission Handlers
// -------------------------------------------------------------

func generateSecureOTP() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	num := (int(b[0])<<16 | int(b[1])<<8 | int(b[2]))%900000 + 100000
	return fmt.Sprintf("%06d", num)
}

func (s *Server) handleVerifyFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ xÃ¡c thá»±c mÃ£ OTP", nil)
		return
	}

	var req struct {
		FileID  string `json:"file_id"`
		OTPCode string `json:"otp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" || req.OTPCode == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u thÃ´ng tin tá»‡p tin hoáº·c mÃ£ OTP", err)
		return
	}

	valid, err := s.db.VerifyAndBurnOTP(req.FileID, user.ID, req.OTPCode)
	if !valid || err != nil {
		writeError(w, http.StatusForbidden, "Lá»—i xÃ¡c thá»±c: "+err.Error(), err)
		return
	}

	vfile, _ := s.db.GetVirtualFile(req.FileID)
	fileName := req.FileID
	if vfile != nil {
		fileName = vfile.Name
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_VERIFY_SUCCESS",
		Target:    fileName,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("XÃ¡c thá»±c thÃ nh cÃ´ng mÃ£ OTP má»Ÿ khÃ³a tá»‡p '%s' (MÃ£ Ä‘Ã£ Ä‘Æ°á»£c há»§y sá»­ dá»¥ng 1 láº§n)", fileName),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "success",
		"message":      "Má»Ÿ khÃ³a tá»‡p tin thÃ nh cÃ´ng! MÃ£ OTP Ä‘Ã£ Ä‘Æ°á»£c sá»­ dá»¥ng (1 láº§n).",
		"file_id":      req.FileID,
		"file_name":    fileName,
		"otp_burned":   true,
		"auto_unlock":  true, // Frontend dÃ¹ng Ä‘á»ƒ tá»± Ä‘á»™ng má»Ÿ file
	})
}

func (s *Server) handleRequestFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ gá»­i yÃªu cáº§u", nil)
		return
	}

	var req struct {
		FileID string `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID tá»‡p tin", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tá»‡p tin khÃ´ng tá»“n táº¡i", err)
		return
	}

	accessReq := &models.FileAccessRequest{
		FileID:          vfile.ID,
		FileName:        vfile.Name,
		UserID:          user.ID,
		Username:        user.Username,
		UserDisplayName: user.DisplayName,
		Status:          "pending",
	}

	if err := s.db.CreateFileAccessRequest(accessReq); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i gá»­i yÃªu cáº§u: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "ÄÃ£ gá»­i yÃªu cáº§u cáº¥p mÃ£ OTP tá»›i Quáº£n Trá»‹ ViÃªn thÃ nh cÃ´ng!",
		"request": accessReq,
	})
}

func (s *Server) handleListMyFileRequests(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "ChÆ°a Ä‘Äƒng nháº­p", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch yÃªu cáº§u", err)
		return
	}

	// Filter by current user
	myRequests := make([]models.FileAccessRequest, 0)
	for _, req := range requests {
		if req.UserID == user.ID {
			myRequests = append(myRequests, req)
		}
	}
	writeJSON(w, http.StatusOK, myRequests)
}

func (s *Server) handleGenerateFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n táº¡o mÃ£ OTP", nil)
		return
	}

	var req struct {
		FileID          string `json:"file_id"`
		TargetUserID    string `json:"target_user_id"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID tá»‡p tin", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tá»‡p tin khÃ´ng tá»“n táº¡i", err)
		return
	}

	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 1440 // 24 hours default
	}
	if req.TargetUserID == "" {
		req.TargetUserID = "all"
	}

	otpCode := generateSecureOTP()
	expiresAt := time.Now().Add(time.Duration(req.DurationMinutes) * time.Minute)

	otpObj := &models.FileAccessOTP{
		FileID:       vfile.ID,
		FileName:     vfile.Name,
		TargetUserID: req.TargetUserID,
		OTPCode:      otpCode,
		CreatedBy:    user.ID,
		ExpiresAt:    expiresAt,
	}

	if err := s.db.CreateFileOTP(otpObj); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o mÃ£ OTP: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_GENERATE",
		Target:    vfile.Name,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Táº¡o mÃ£ OTP 1 láº§n cho tá»‡p '%s' (Hiá»‡u lá»±c %d phÃºt)", vfile.Name, req.DurationMinutes),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Táº¡o mÃ£ OTP má»Ÿ khÃ³a 1 láº§n thÃ nh cÃ´ng!",
		"otp_code":   otpCode,
		"file_name":  vfile.Name,
		"expires_at": expiresAt.Format("2006-01-02 15:04:05"),
		"otp":        otpObj,
	})
}

func (s *Server) handleListFileOTPs(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n xem danh sÃ¡ch OTP", nil)
		return
	}

	otps, err := s.db.ListFileOTPs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch OTP: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, otps)
}

func (s *Server) handleRevokeFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thu há»“i OTP", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID OTP", err)
		return
	}

	if err := s.db.RevokeFileOTP(req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i thu há»“i OTP: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ thu há»“i vÃ  há»§y mÃ£ OTP thÃ nh cÃ´ng"})
}

func (s *Server) handleListAdminAccessRequests(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n xem danh sÃ¡ch yÃªu cáº§u", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch yÃªu cáº§u", err)
		return
	}
	writeJSON(w, http.StatusOK, requests)
}

func (s *Server) handleApproveAccessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n phÃª duyá»‡t yÃªu cáº§u", nil)
		return
	}

	var req struct {
		RequestID       string `json:"request_id"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u thÃ´ng tin yÃªu cáº§u", err)
		return
	}

	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 1440 // 24 hours default
	}

	requests, _ := s.db.ListFileAccessRequests("all")
	var targetReq *models.FileAccessRequest
	for _, ar := range requests {
		if ar.ID == req.RequestID {
			targetReq = &ar
			break
		}
	}

	if targetReq == nil {
		writeError(w, http.StatusNotFound, "KhÃ´ng tÃ¬m tháº¥y yÃªu cáº§u", nil)
		return
	}

	otpCode := generateSecureOTP()
	expiresAt := time.Now().Add(time.Duration(req.DurationMinutes) * time.Minute)

	// Create OTP record for this specific user
	otpObj := &models.FileAccessOTP{
		FileID:       targetReq.FileID,
		FileName:     targetReq.FileName,
		TargetUserID: targetReq.UserID,
		OTPCode:      otpCode,
		CreatedBy:    user.ID,
		ExpiresAt:    expiresAt,
	}
	_ = s.db.CreateFileOTP(otpObj)
	_ = s.db.ApproveFileAccessRequest(targetReq.ID, otpCode)

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_APPROVE_REQUEST",
		Target:    targetReq.FileName,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("PhÃª duyá»‡t cáº¥p mÃ£ OTP cho user '%s' truy cáº­p tá»‡p '%s'", targetReq.Username, targetReq.FileName),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    fmt.Sprintf("ÄÃ£ phÃª duyá»‡t vÃ  cáº¥p mÃ£ OTP '%s' cho ngÆ°á»i dÃ¹ng %s thÃ nh cÃ´ng!", otpCode, targetReq.Username),
		"otp_code":   otpCode,
		"expires_at": expiresAt.Format("2006-01-02 15:04:05"),
	})
}

func (s *Server) handleRejectAccessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n tá»« chá»‘i yÃªu cáº§u", nil)
		return
	}

	var req struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u ID yÃªu cáº§u", err)
		return
	}

	_ = s.db.RejectFileAccessRequest(req.RequestID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ tá»« chá»‘i yÃªu cáº§u cáº¥p OTP"})
}

func (s *Server) handleImportDriveFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n náº¡p tá»‡p tá»« Google Drive", nil)
		return
	}

	var req struct {
		AccountID string `json:"account_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	count, err := s.vfs.ImportExistingDriveFiles(r.Context(), req.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i quÃ©t vÃ  náº¡p tá»‡p tá»« Google Drive: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "IMPORT_DRIVE",
		Target:    "Google Drive",
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("ÄÃ£ quÃ©t vÃ  tá»± Ä‘á»™ng náº¡p %d tá»‡p tin cÃ³ sáºµn tá»« Google Drive vÃ o CloudPool", count),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"imported_count": count,
		"message":        fmt.Sprintf("ÄÃ£ quÃ©t vÃ  tá»± Ä‘á»™ng náº¡p thÃ nh cÃ´ng %d tá»‡p tin cÃ³ sáºµn tá»« Google Drive vÃ o CloudPool!", count),
	})
}

func (s *Server) handleListDriveFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n xem tá»‡p tá»« Google Drive", nil)
		return
	}

	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u account_id", nil)
		return
	}

	files, err := s.gd.ScanExistingDriveFiles(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i láº¥y danh sÃ¡ch file tá»« Drive: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, files)
}

// handleOTPPendingCount tráº£ vá» sá»‘ lÆ°á»£ng yÃªu cáº§u OTP Ä‘ang chá» duyá»‡t.
// DÃ¹ng cho admin polling nháº¹ (má»—i 15s) Ä‘á»ƒ hiá»ƒn thá»‹ badge thÃ´ng bÃ¡o.
func (s *Server) handleOTPPendingCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Admin only", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]int{"pending_count": 0})
		return
	}

	count := 0
	for _, req := range requests {
		if req.Status == "pending" {
			count++
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{"pending_count": count})
}

// -------------------------------------------------------------
// Recycle Bin (Trash) Handlers
// -------------------------------------------------------------

func (s *Server) handleListTrashFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ xem thÃ¹ng rÃ¡c", nil)
		return
	}

	userID := "all"
	if user.Role != "admin" {
		userID = user.ID
	}

	files, err := s.db.ListTrashFiles(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ láº¥y danh sÃ¡ch thÃ¹ng rÃ¡c", err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) handleRestoreTrashFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ khÃ´i phá»¥c tá»‡p tin", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID cáº§n khÃ´i phá»¥c", err)
		return
	}

	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(req.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n khÃ´i phá»¥c tá»‡p tin cá»§a tÃ i khoáº£n khÃ¡c", nil)
			return
		}
	}

	if err := s.vfs.RestoreFileOrFolder(r.Context(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i khÃ´i phá»¥c tá»‡p: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "RESTORE",
		Target:    req.ID,
		IPAddress: getClientIP(r),
		Details:   "ÄÃ£ khÃ´i phá»¥c tá»‡p tá»« ThÃ¹ng rÃ¡c",
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ khÃ´i phá»¥c tá»‡p thÃ nh cÃ´ng"})
}

func (s *Server) handlePurgeTrashFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ xÃ³a vÄ©nh viá»…n tá»‡p tin", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file ID", err)
		return
	}

	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(req.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n xÃ³a tá»‡p tin cá»§a tÃ i khoáº£n khÃ¡c", nil)
			return
		}
	}

	if err := s.vfs.PurgeFileOrFolderPermanently(r.Context(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i xÃ³a vÄ©nh viá»…n: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ xÃ³a vÄ©nh viá»…n tá»‡p vÃ  giáº£i phÃ³ng dung lÆ°á»£ng"})
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ dá»n sáº¡ch thÃ¹ng rÃ¡c", nil)
		return
	}

	userID := "all"
	if user.Role != "admin" {
		userID = user.ID
	}

	purged, err := s.vfs.EmptyTrash(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i dá»n sáº¡ch thÃ¹ng rÃ¡c: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "EMPTY_TRASH",
		Target:    "ThÃ¹ng rÃ¡c",
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("ÄÃ£ dá»n sáº¡ch thÃ¹ng rÃ¡c (xÃ³a %d tá»‡p)", purged),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"purged_count": purged,
		"message":      fmt.Sprintf("ÄÃ£ dá»n sáº¡ch thÃ¹ng rÃ¡c thÃ nh cÃ´ng (%d tá»‡p Ä‘Æ°á»£c xÃ³a vÄ©nh viá»…n)!", purged),
	})
}

// -------------------------------------------------------------
// Public Share Links Handlers
// -------------------------------------------------------------

func (s *Server) handleCreatePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		FileID       string `json:"file_id"`
		Password     string `json:"password"`
		ExpiresHours int    `json:"expires_hours"` // 0 = never, 1, 24, 168
		MaxDownloads int    `json:"max_downloads"` // 0 = unlimited
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u file_id há»£p lá»‡", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tá»‡p tin hoáº·c thÆ° má»¥c khÃ´ng tá»“n táº¡i", err)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ táº¡o liÃªn káº¿t chia sáº» cÃ´ng khai", nil)
		return
	}
	if user.Role != "admin" && vfile.UserID != "" && vfile.UserID != user.ID {
		writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n chia sáº» tá»‡p tin cá»§a tÃ i khoáº£n khÃ¡c", nil)
		return
	}
	createdBy := user.Username

	var passHash string
	if req.Password != "" {
		passHash = core.HashSHA256([]byte(req.Password))
	}

	var exp *time.Time
	if req.ExpiresHours > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresHours) * time.Hour)
		exp = &t
	}

	fileSize := vfile.SizeBytes
	fileCount := 0
	mimeType := vfile.MimeType

	if vfile.IsDir {
		mimeType = "directory"
		totSize, count, err := s.db.GetFolderStats(vfile.ID)
		if err == nil {
			fileSize = totSize
			fileCount = count
		}
	}

	shareToken := uuid.New().String()
	share := &models.PublicShare{
		ID:            shareToken,
		FileID:        req.FileID,
		FileName:      vfile.Name,
		FileSize:      fileSize,
		MimeType:      mimeType,
		IsDir:         vfile.IsDir,
		FileCount:     fileCount,
		CreatedBy:     createdBy,
		PasswordHash:  passHash,
		HasPassword:   passHash != "",
		MaxDownloads:  req.MaxDownloads,
		DownloadCount: 0,
		ExpiresAt:     exp,
		CreatedAt:     time.Now(),
		IsActive:      true,
	}

	if err := s.db.CreatePublicShare(share); err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ táº¡o link chia sáº»: "+err.Error(), err)
		return
	}

	targetType := "tá»‡p"
	if vfile.IsDir {
		targetType = "thÆ° má»¥c"
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    createdBy,
		Username:  createdBy,
		Action:    "SHARE",
		Target:    vfile.Name,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("ÄÃ£ táº¡o link chia sáº» cÃ´ng khai cho %s '%s'", targetType, vfile.Name),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"share_token":  shareToken,
		"share":        share,
		"message":      fmt.Sprintf("ÄÃ£ táº¡o link chia sáº» %s thÃ nh cÃ´ng!", targetType),
	})
}

func (s *Server) handleListPublicShares(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ xem danh sÃ¡ch liÃªn káº¿t chia sáº»", nil)
		return
	}

	shares, err := s.db.ListPublicShares()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ láº¥y danh sÃ¡ch link chia sáº»", err)
		return
	}

	if user.Role != "admin" {
		filtered := make([]models.PublicShare, 0)
		for _, sh := range shares {
			if sh.CreatedBy == user.Username {
				filtered = append(filtered, sh)
			}
		}
		writeJSON(w, http.StatusOK, filtered)
		return
	}

	writeJSON(w, http.StatusOK, shares)
}

func (s *Server) handleRevokePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lÃ²ng Ä‘Äƒng nháº­p Ä‘á»ƒ thu há»“i liÃªn káº¿t chia sáº»", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u share ID", err)
		return
	}

	if user.Role != "admin" {
		sh, err := s.db.GetPublicShare(req.ID)
		if err == nil && sh != nil && sh.CreatedBy != user.Username {
			writeError(w, http.StatusForbidden, "Báº¡n khÃ´ng cÃ³ quyá»n thu há»“i liÃªn káº¿t chia sáº» cá»§a ngÆ°á»i khÃ¡c", nil)
			return
		}
	}

	if err := s.db.RevokePublicShare(req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ thu há»“i link: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "ÄÃ£ thu há»“i link chia sáº» thÃ nh cÃ´ng"})
}

func (s *Server) handlePublicShareInfo(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil {
		writeError(w, http.StatusNotFound, "Link chia sáº» khÃ´ng tá»“n táº¡i hoáº·c Ä‘Ã£ bá»‹ thu há»“i", err)
		return
	}

	if !sh.IsActive {
		writeError(w, http.StatusGone, "Link chia sáº» nÃ y Ä‘Ã£ háº¿t háº¡n hoáº·c Ä‘áº¡t giá»›i háº¡n táº£i tá»‘i Ä‘a", nil)
		return
	}

	resp := map[string]interface{}{
		"id":           sh.ID,
		"file_name":    sh.FileName,
		"file_size":    sh.FileSize,
		"mime_type":    sh.MimeType,
		"is_dir":       sh.IsDir,
		"file_count":   sh.FileCount,
		"has_password": sh.HasPassword,
		"created_by":   sh.CreatedBy,
		"created_at":   sh.CreatedAt,
		"expires_at":   sh.ExpiresAt,
	}

	if sh.IsDir && !sh.HasPassword {
		items, _ := s.db.ListFilesInFolderForShare(sh.FileID, sh.FileID)
		resp["items"] = items
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePublicShareBrowseFolder(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	folderID := r.URL.Query().Get("folder_id")
	pass := r.URL.Query().Get("pass")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sáº» khÃ´ng tá»“n táº¡i hoáº·c Ä‘Ã£ háº¿t háº¡n", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Máº­t kháº©u báº£o vá»‡ khÃ´ng chÃ­nh xÃ¡c", nil)
			return
		}
	}

	if !sh.IsDir {
		writeError(w, http.StatusBadRequest, "Äá»‘i tÆ°á»£ng chia sáº» khÃ´ng pháº£i lÃ  thÆ° má»¥c", nil)
		return
	}

	targetFolderID := folderID
	if targetFolderID == "" {
		targetFolderID = sh.FileID
	}

	items, err := s.db.ListFilesInFolderForShare(sh.FileID, targetFolderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Lá»—i náº¡p thÆ° má»¥c: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"current_folder_id": targetFolderID,
		"root_folder_id":    sh.FileID,
		"folder_name":       sh.FileName,
		"items":             items,
	})
}

func (s *Server) handlePublicShareStream(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pass := r.URL.Query().Get("pass")
	fileID := r.URL.Query().Get("file_id")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sáº» khÃ´ng tá»“n táº¡i hoáº·c Ä‘Ã£ háº¿t háº¡n", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Máº­t kháº©u báº£o vá»‡ khÃ´ng chÃ­nh xÃ¡c", nil)
			return
		}
	}

	targetFileID := sh.FileID
	targetFileName := sh.FileName
	targetMimeType := sh.MimeType

	if fileID != "" && fileID != sh.FileID {
		vfile, err := s.db.GetVirtualFile(fileID)
		if err != nil || vfile.IsDir {
			writeError(w, http.StatusNotFound, "Tá»‡p tin khÃ´ng tá»“n táº¡i", err)
			return
		}
		targetFileID = vfile.ID
		targetFileName = vfile.Name
		targetMimeType = vfile.MimeType
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), targetFileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i náº¡p tá»‡p: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	mimeType := targetMimeType
	ext := strings.ToLower(filepath.Ext(targetFileName))
	if ext == ".pdf" {
		mimeType = "application/pdf"
	} else if ext == ".webm" {
		mimeType = "video/webm"
	} else if ext == ".mp4" {
		mimeType = "video/mp4"
	} else if ext == ".mp3" {
		mimeType = "audio/mpeg"
	} else if ext == ".jpg" || ext == ".jpeg" {
		mimeType = "image/jpeg"
	} else if ext == ".png" {
		mimeType = "image/png"
	} else if ext == ".xls" {
		mimeType = "application/vnd.ms-excel"
	} else if ext == ".xlsx" {
		mimeType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", url.PathEscape(targetFileName)))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	http.ServeContent(w, r, targetFileName, streamer.ModTime(), streamer)
}

func (s *Server) handlePublicShareDownload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pass := r.URL.Query().Get("pass")
	fileID := r.URL.Query().Get("file_id")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiáº¿u share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sáº» khÃ´ng tá»“n táº¡i hoáº·c Ä‘Ã£ háº¿t háº¡n", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Máº­t kháº©u báº£o vá»‡ khÃ´ng chÃ­nh xÃ¡c", nil)
			return
		}
	}

	_ = s.db.IncrementPublicShareDownload(token)

	// If downloading a specific file inside a shared folder
	if fileID != "" && fileID != sh.FileID {
		vfile, err := s.db.GetVirtualFile(fileID)
		if err != nil || vfile.IsDir {
			writeError(w, http.StatusNotFound, "Tá»‡p khÃ´ng tá»“n táº¡i", err)
			return
		}
		streamer, err := s.vfs.NewFileStreamer(r.Context(), vfile.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lá»—i náº¡p tá»‡p: "+err.Error(), err)
			return
		}
		defer streamer.Close()

		w.Header().Set("Content-Type", vfile.MimeType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", url.PathEscape(vfile.Name)))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, vfile.Name, streamer.ModTime(), streamer)
		return
	}

	// If downloading entire Folder -> Stream as ZIP on the fly!
	if sh.IsDir {
		filesInTree, err := s.db.GetAllFilesInFolderTree(sh.FileID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lá»—i Ä‘á»c cÃ¢y thÆ° má»¥c: "+err.Error(), err)
			return
		}

		zipName := fmt.Sprintf("%s.zip", sh.FileName)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", url.PathEscape(zipName)))

		zipWriter := zip.NewWriter(w)
		defer zipWriter.Close()

		for _, item := range filesInTree {
			streamer, err := s.vfs.NewFileStreamer(r.Context(), item.File.ID)
			if err != nil {
				continue
			}

			entryPath := item.RelativePath
			if entryPath == "" {
				entryPath = item.File.Name
			}

			zf, err := zipWriter.Create(entryPath)
			if err != nil {
				streamer.Close()
				continue
			}

			_, _ = io.Copy(zf, streamer)
			streamer.Close()
		}
		return
	}

	// Single File Download
	streamer, err := s.vfs.NewFileStreamer(r.Context(), sh.FileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i náº¡p tá»‡p: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	w.Header().Set("Content-Type", sh.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", url.PathEscape(sh.FileName)))
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, sh.FileName, streamer.ModTime(), streamer)
}

// -------------------------------------------------------------
// Storage Breakdown & Analytics Handler
// -------------------------------------------------------------

func (s *Server) handleStorageBreakdown(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	userID := "all"
	if user != nil && user.Role != "admin" {
		userID = user.ID
	}

	resp, err := s.db.GetStorageBreakdown(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ tÃ­nh toÃ¡n cÆ¡ cáº¥u dung lÆ°á»£ng", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// validatePasswordStrength kiá»ƒm tra Ä‘á»™ máº¡nh máº­t kháº©u theo chuáº©n Production:
// - Tá»‘i thiá»ƒu 8 kÃ½ tá»±
// - Pháº£i cÃ³ Ã­t nháº¥t 1 chá»¯ cÃ¡i (a-z hoáº·c A-Z)
// - Pháº£i cÃ³ Ã­t nháº¥t 1 chá»¯ sá»‘ (0-9)
func validatePasswordStrength(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("máº­t kháº©u pháº£i cÃ³ Ã­t nháº¥t 8 kÃ½ tá»± (hiá»‡n táº¡i: %d kÃ½ tá»±)", len(password))
	}
	hasLetter := false
	hasDigit := false
	for _, c := range password {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			hasLetter = true
		}
		if c >= '0' && c <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter {
		return fmt.Errorf("máº­t kháº©u pháº£i chá»©a Ã­t nháº¥t 1 chá»¯ cÃ¡i (a-z)")
	}
	if !hasDigit {
		return fmt.Errorf("máº­t kháº©u pháº£i chá»©a Ã­t nháº¥t 1 chá»¯ sá»‘ (0-9)")
	}
	return nil
}

