package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/cloudpool/webdav"
	"supportflast_engine/registry"
)

var (
	// DefaultServer giữ instance của CloudPool Server đã khởi tạo
	DefaultServer *Server
	initOnce      sync.Once

	// Exported Native HTTP Handlers theo chuẩn CloudPool API
	FilesHandler       http.HandlerFunc
	AccountsHandler    http.HandlerFunc
	StatsHandler       http.HandlerFunc
	SharesHandler      http.HandlerFunc
	SQLStudioHandler   http.HandlerFunc
	SettingsHandler    http.HandlerFunc
	RemoteHandler      http.HandlerFunc
	TunnelHandler      http.HandlerFunc
	AdminDriveHandler  http.HandlerFunc
	AdminOTPHandler    http.HandlerFunc
	AdminStorageHandler http.HandlerFunc
	PublicShareHandler http.HandlerFunc
	UpdateHandler      http.HandlerFunc
	WebDAVHandler      http.Handler

	// Auth & Admin User Handlers
	VerifyAdminPassHandler http.HandlerFunc
	UpdateUserQuotaHandler http.HandlerFunc
	DeleteUserHandler      http.HandlerFunc
)

// InitHandlers khởi tạo và liên kết toàn bộ Native Route Handlers cho CloudPool
func InitHandlers(db *storage.DB, gd *gdrive.Manager, vfsEngine *vfs.VFS, uiDir string, baseDir string) *Server {
	initOnce.Do(func() {
		if err := InitJWTKeys(baseDir); err != nil {
			log.Printf("[ENGINE] [CLOUDPOOL] [WARN] Khởi tạo khóa JWT keys thất bại: %v", err)
		}

		portInt, _ := strconv.Atoi(registry.GetServicePort())
		if portInt <= 0 {
			portInt = 8080
		}
		s := NewServer(db, gd, vfsEngine, portInt, uiDir, baseDir)
		s.setupSubMuxes()
		DefaultServer = s

		FilesHandler = s.FilesHandler
		AccountsHandler = s.AccountsHandler
		StatsHandler = s.StatsHandler
		SharesHandler = s.SharesHandler
		SQLStudioHandler = s.SQLStudioHandler
		SettingsHandler = s.SettingsHandler
		RemoteHandler = s.RemoteHandler
		TunnelHandler = s.TunnelHandler
		AdminDriveHandler = s.AdminDriveHandler
		AdminOTPHandler = s.AdminOTPHandler
		AdminStorageHandler = s.AdminStorageHandler
		PublicShareHandler = s.PublicShareHandler
		UpdateHandler = s.UpdateHandler
		WebDAVHandler = s.WebDAVHandler()

		// Auth & Admin User Handlers
		VerifyAdminPassHandler = s.handleVerifyAdminPass
		UpdateUserQuotaHandler = s.handleUpdateUserQuota
		DeleteUserHandler = s.handleDeleteUser

		log.Printf("[ENGINE] [CLOUDPOOL] Toàn bộ Native Route Handlers đã được tích hợp thành công vào Go Engine!")
	})
	return DefaultServer
}

type cloudPoolSubMuxes struct {
	filesMux       *http.ServeMux
	accountsMux    *http.ServeMux
	statsMux       *http.ServeMux
	sharesMux      *http.ServeMux
	sqlMux         *http.ServeMux
	settingsMux    *http.ServeMux
	remoteMux      *http.ServeMux
	tunnelMux      *http.ServeMux
	driveMux       *http.ServeMux
	otpMux         *http.ServeMux
	publicShareMux *http.ServeMux
	updateMux      *http.ServeMux
	storageMux     *http.ServeMux
	davHandler     http.Handler
}

var subMuxRegistry = make(map[*Server]*cloudPoolSubMuxes)
var subMuxMu sync.RWMutex

func (s *Server) setupSubMuxes() {
	m := &cloudPoolSubMuxes{}

	// 1. Files & Folders Sub-Mux
	m.filesMux = http.NewServeMux()
	m.filesMux.HandleFunc("/api/files", s.handleListFiles)
	m.filesMux.HandleFunc("/api/files/", s.handleListFiles)
	m.filesMux.HandleFunc("/api/files/status", s.handleFileStatus)
	m.filesMux.HandleFunc("/api/files/status/", s.handleFileStatus)
	m.filesMux.HandleFunc("/api/files/mkdir", s.handleMkdir)
	m.filesMux.HandleFunc("/api/files/rename", s.handleRenameFile)
	m.filesMux.HandleFunc("/api/files/delete", s.handleDeleteFile)
	m.filesMux.HandleFunc("/api/files/bulk-delete", s.handleBulkDeleteFiles)
	m.filesMux.HandleFunc("/api/files/upload", s.handleUploadFile)
	m.filesMux.HandleFunc("/api/files/upload-chunked", s.handleChunkedUpload)
	m.filesMux.HandleFunc("/api/files/upload-status", s.handleChunkedUploadStatus)
	m.filesMux.HandleFunc("/api/files/download", s.handleDownloadFile)
	m.filesMux.HandleFunc("/api/files/stream", s.handleStreamFile)
	m.filesMux.HandleFunc("/api/files/chunks", s.handleFileChunks)
	m.filesMux.HandleFunc("/api/files/zip", s.handleDownloadZip)

	// Recycle Bin (Trash) Endpoints
	m.filesMux.HandleFunc("/api/files/trash", s.handleListTrashFiles)
	m.filesMux.HandleFunc("/api/files/trash/restore", s.handleRestoreTrashFile)
	m.filesMux.HandleFunc("/api/files/trash/delete-forever", s.handlePurgeTrashFile)
	m.filesMux.HandleFunc("/api/files/trash/empty", s.handleEmptyTrash)

	// Single-Use OTP File Access & Permission Endpoints
	m.filesMux.HandleFunc("/api/files/otp/verify", s.handleVerifyFileOTP)
	m.filesMux.HandleFunc("/api/files/otp/request", s.handleRequestFileOTP)
	m.filesMux.HandleFunc("/api/files/otp/my-requests", s.handleListMyFileRequests)

	// 2. Accounts Sub-Mux
	m.accountsMux = http.NewServeMux()
	m.accountsMux.HandleFunc("/api/accounts", s.handleAccounts)
	m.accountsMux.HandleFunc("/api/accounts/", s.handleAccounts)
	m.accountsMux.HandleFunc("/api/accounts/oauth/url", s.handleOAuthURL)
	m.accountsMux.HandleFunc("/api/accounts/oauth/callback", s.handleOAuthCallback)
	m.accountsMux.HandleFunc("/api/accounts/service-account", s.handleAddServiceAccount)
	m.accountsMux.HandleFunc("/api/accounts/delete", s.handleDeleteAccount)
	m.accountsMux.HandleFunc("/api/accounts/refresh", s.handleRefreshAccount)
	m.accountsMux.HandleFunc("/api/accounts/toggle", s.handleToggleAccount)

	// 3. Stats Sub-Mux
	m.statsMux = http.NewServeMux()
	m.statsMux.HandleFunc("/api/stats", s.handleStats)
	m.statsMux.HandleFunc("/api/stats/", s.handleStats)
	m.statsMux.HandleFunc("/api/stats/breakdown", s.handleStorageBreakdown)

	// 4. Shares Sub-Mux
	m.sharesMux = http.NewServeMux()
	m.sharesMux.HandleFunc("/api/shares", s.handleListPublicShares)
	m.sharesMux.HandleFunc("/api/shares/", s.handleListPublicShares)
	m.sharesMux.HandleFunc("/api/shares/create", s.handleCreatePublicShare)
	m.sharesMux.HandleFunc("/api/shares/list", s.handleListPublicShares)
	m.sharesMux.HandleFunc("/api/shares/revoke", s.handleRevokePublicShare)

	// 5. SQL Studio Sub-Mux
	m.sqlMux = http.NewServeMux()
	m.sqlMux.HandleFunc("/api/sql", s.handleSQLTables)
	m.sqlMux.HandleFunc("/api/sql/", s.handleSQLTables)
	m.sqlMux.HandleFunc("/api/sql/tables", s.handleSQLTables)
	m.sqlMux.HandleFunc("/api/sql/query", s.handleSQLQuery)
	m.sqlMux.HandleFunc("/api/sql/download", s.handleSQLDownloadDB)
	m.sqlMux.HandleFunc("/api/sql/optimize", s.handleSQLOptimize)
	m.sqlMux.HandleFunc("/api/sql/backup", s.handleSQLBackup)
	m.sqlMux.HandleFunc("/api/sql/backup/gdrive", s.handleGDriveBackup)
	m.sqlMux.HandleFunc("/api/sql/backup/history", s.handleGDriveBackupHistory)
	m.sqlMux.HandleFunc("/api/sql/check", s.handleSQLCheck)

	// 6. Settings Sub-Mux
	m.settingsMux = http.NewServeMux()
	m.settingsMux.HandleFunc("/api/settings", s.handleSettings)
	m.settingsMux.HandleFunc("/api/settings/", s.handleSettings)

	// 7. Remote Sub-Mux
	m.remoteMux = http.NewServeMux()
	m.remoteMux.HandleFunc("/api/remote", s.handleRemoteInfo)
	m.remoteMux.HandleFunc("/api/remote/", s.handleRemoteInfo)
	m.remoteMux.HandleFunc("/api/remote/info", s.handleRemoteInfo)

	// 8. Tunnel Sub-Mux
	m.tunnelMux = http.NewServeMux()
	m.tunnelMux.HandleFunc("/api/tunnel", s.handleTunnelStatus)
	m.tunnelMux.HandleFunc("/api/tunnel/", s.handleTunnelStatus)
	m.tunnelMux.HandleFunc("/api/tunnel/start", s.handleTunnelStart)
	m.tunnelMux.HandleFunc("/api/tunnel/stop", s.handleTunnelStop)
	m.tunnelMux.HandleFunc("/api/tunnel/status", s.handleTunnelStatus)

	// 9. Admin Drive Sub-Mux
	m.driveMux = http.NewServeMux()
	m.driveMux.HandleFunc("/api/admin/drive", s.handleListDriveFiles)
	m.driveMux.HandleFunc("/api/admin/drive/", s.handleListDriveFiles)
	m.driveMux.HandleFunc("/api/admin/drive/import", s.handleImportDriveFiles)
	m.driveMux.HandleFunc("/api/admin/drive/files", s.handleListDriveFiles)
	m.driveMux.HandleFunc("/api/admin/storage/integrity-check", s.handleIntegrityCheck)
	m.driveMux.HandleFunc("/api/admin/storage/integrity-check/", s.handleIntegrityCheck)

	// 10. Admin OTP Sub-Mux
	m.otpMux = http.NewServeMux()
	m.otpMux.HandleFunc("/api/admin/otp", s.handleListFileOTPs)
	m.otpMux.HandleFunc("/api/admin/otp/", s.handleListFileOTPs)
	m.otpMux.HandleFunc("/api/admin/otp/generate", s.handleGenerateFileOTP)
	m.otpMux.HandleFunc("/api/admin/otp/list", s.handleListFileOTPs)
	m.otpMux.HandleFunc("/api/admin/otp/revoke", s.handleRevokeFileOTP)
	m.otpMux.HandleFunc("/api/admin/otp/requests", s.handleListAdminAccessRequests)
	m.otpMux.HandleFunc("/api/admin/otp/approve-request", s.handleApproveAccessRequest)
	m.otpMux.HandleFunc("/api/admin/otp/reject-request", s.handleRejectAccessRequest)
	m.otpMux.HandleFunc("/api/admin/otp/pending-count", s.handleOTPPendingCount)

	// Public Share Sub-Mux
	m.publicShareMux = http.NewServeMux()
	m.publicShareMux.HandleFunc("/api/public/share/info", s.handlePublicShareInfo)
	m.publicShareMux.HandleFunc("/api/public/share/folder/browse", s.handlePublicShareBrowseFolder)
	m.publicShareMux.HandleFunc("/api/public/share/stream", s.handlePublicShareStream)
	m.publicShareMux.HandleFunc("/api/public/share/download", s.handlePublicShareDownload)

	// 11. Update Sub-Mux (In-App Software Update & Granular Hot-Patching)
	m.updateMux = http.NewServeMux()
	m.updateMux.HandleFunc("/api/admin/update", s.handleUpdateInfo)
	m.updateMux.HandleFunc("/api/admin/update/", s.handleUpdateInfo)
	m.updateMux.HandleFunc("/api/admin/update/info", s.handleUpdateInfo)
	m.updateMux.HandleFunc("/api/admin/update/upload", s.handleUpdateUpload)
	m.updateMux.HandleFunc("/api/admin/update/rollback", s.handleUpdateRollback)
	m.updateMux.HandleFunc("/api/admin/update/restart", s.handleRestartEngine)

	// 12. Admin Storage Sub-Mux (Integrity & Maintenance)
	m.storageMux = http.NewServeMux()
	m.storageMux.HandleFunc("/api/admin/storage/integrity-check", s.handleIntegrityCheck)
	m.storageMux.HandleFunc("/api/admin/storage/integrity-check/", s.handleIntegrityCheck)

	// WebDAV Handler
	m.davHandler = webdav.CreateWebDAVHandler(s.vfs, s.db)

	subMuxMu.Lock()
	subMuxRegistry[s] = m
	subMuxMu.Unlock()
}

func (s *Server) getSubMuxes() *cloudPoolSubMuxes {
	subMuxMu.RLock()
	defer subMuxMu.RUnlock()
	return subMuxRegistry[s]
}

// -----------------------------------------------------------------------
// Server Method Handlers (Cung cấp trực tiếp từ struct *Server)
// -----------------------------------------------------------------------

func (s *Server) FilesHandler(w http.ResponseWriter, r *http.Request) {
	// Cho phép stream media an toàn (Rule Phần 3.4)
	if strings.HasPrefix(r.URL.Path, "/api/files/stream") {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; media-src 'self' https: data: blob:; img-src 'self' https: data: blob:; style-src 'unsafe-inline';")
	}
	m := s.getSubMuxes()
	if m != nil && m.filesMux != nil {
		m.filesMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Files handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) AccountsHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.accountsMux != nil {
		m.accountsMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Accounts handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) StatsHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.statsMux != nil {
		m.statsMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Stats handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) SharesHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.sharesMux != nil {
		m.sharesMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Shares handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) SQLStudioHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.sqlMux != nil {
		m.sqlMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"SQL Studio handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) SettingsHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.settingsMux != nil {
		m.settingsMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Settings handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) RemoteHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.remoteMux != nil {
		m.remoteMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Remote handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) TunnelHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.tunnelMux != nil {
		m.tunnelMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Tunnel handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) AdminDriveHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.driveMux != nil {
		m.driveMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Admin Drive handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) AdminOTPHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.otpMux != nil {
		m.otpMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Admin OTP handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) PublicShareHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/public/share/stream") {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; media-src data:;")
	}
	m := s.getSubMuxes()
	if m != nil && m.publicShareMux != nil {
		m.publicShareMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Public share handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.updateMux != nil {
		m.updateMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Update handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) AdminStorageHandler(w http.ResponseWriter, r *http.Request) {
	m := s.getSubMuxes()
	if m != nil && m.storageMux != nil {
		m.storageMux.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"Admin Storage handler not initialized"}`, http.StatusServiceUnavailable)
}

func (s *Server) WebDAVHandler() http.Handler {
	m := s.getSubMuxes()
	if m != nil && m.davHandler != nil {
		return m.davHandler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"WebDAV handler not initialized"}`, http.StatusServiceUnavailable)
	})
}

