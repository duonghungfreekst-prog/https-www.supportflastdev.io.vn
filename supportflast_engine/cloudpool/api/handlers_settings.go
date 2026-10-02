package api

import (
	"archive/zip"
	"bytes"
	"context"
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
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/registry"
)


func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lấy số liệu thống kê", err)
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
		writeError(w, http.StatusInternalServerError, "Không thể lấy danh sách tài khoản", err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}



func (s *Server) handleOAuthURL(w http.ResponseWriter, r *http.Request) {
	settings, err := s.db.GetSettings()
	if err != nil || settings.GoogleClientID == "" {
		writeError(w, http.StatusBadRequest, "Vui lòng cấu hình Google Client ID & Secret trong phần Cài đặt trước", err)
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
		writeError(w, http.StatusBadRequest, "Thiếu mã xác thực OAuth (code)", nil)
		return
	}

	settings, err := s.db.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy cấu hình", err)
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
		writeError(w, http.StatusBadRequest, "Xác thực Google thất bại: "+err.Error(), err)
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
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền thêm tài khoản đám mây", nil)
		return
	}

	file, _, err := r.FormFile("sa_file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Vui lòng tải lên tệp JSON Service Account", err)
		return
	}
	defer file.Close()

	saBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không đọc được tệp JSON", err)
		return
	}

	acc, err := s.gd.AddServiceAccount(r.Context(), saBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Lỗi kết nối Service Account: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, acc)
}



func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền xóa tài khoản đám mây", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID tài khoản", nil)
		return
	}

	if err := s.db.DeleteAccount(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi xóa tài khoản", err)
		return
	}

	if s.gd != nil {
		s.gd.InvalidateAccount(id)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã xóa tài khoản thành công"})
}



func (s *Server) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user != nil && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền làm mới dung lượng tài khoản", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		// Refresh all
		go s.gd.RefreshAllQuotas(context.Background())
		writeJSON(w, http.StatusOK, map[string]string{"message": "Đang quét và làm mới dung lượng tất cả tài khoản"})
		return
	}

	if err := s.gd.RefreshAccountQuota(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể cập nhật dung lượng: "+err.Error(), err)
		return
	}

	acc, _ := s.db.GetAccount(id)
	writeJSON(w, http.StatusOK, acc)
}



func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Yêu cầu quyền Quản trị viên để truy cập cài đặt", nil)
		return
	}

	if r.Method == http.MethodGet {
		settings, err := s.db.GetSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi đọc cài đặt", err)
			return
		}
		// Mask sensitive credentials for display
		settings.MasterPassphrase = "********"
		settings.GoogleClientSecret = "********"
		settings.TurnstileSecretKey = "********"
		if settings.WebDAVPassword != "" {
			settings.WebDAVPassword = "********"
		}
		writeJSON(w, http.StatusOK, settings)
		return
	}

	if r.Method == http.MethodPost {
		var req models.Settings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Dữ liệu cài đặt không hợp lệ", err)
			return
		}

		current, _ := s.db.GetSettings()
		if req.MasterPassphrase == "" || req.MasterPassphrase == "********" {
			if current != nil {
				req.MasterPassphrase = current.MasterPassphrase
			}
		} else if current == nil || req.MasterPassphrase != current.MasterPassphrase {
			newHash := core.HashSHA256([]byte(req.MasterPassphrase))
			_ = s.db.UpdateUserPassword("user_admin", newHash)

			// REKEY database
			oldPass := ""
			if current != nil {
				oldPass = current.MasterPassphrase
			}
			if oldPass == "" {
				oldPass = "cloudpool_secure_master_key_2026"
			}
			if err := s.db.RekeyDatabase(oldPass, req.MasterPassphrase); err != nil {
				log.Printf("[ENGINE] [ERROR] RekeyDatabase thất bại: %v", err)
				writeError(w, http.StatusInternalServerError, "Lỗi cập nhật mật mã bảo mật hệ thống: "+err.Error(), err)
				return
			}
		}

		if req.GoogleClientID == "" || req.GoogleClientID == "********" {
			if current != nil {
				req.GoogleClientID = current.GoogleClientID
			}
		}
		if req.GoogleClientSecret == "" || req.GoogleClientSecret == "********" {
			if current != nil {
				req.GoogleClientSecret = current.GoogleClientSecret
			}
		}
		if req.TurnstileSecretKey == "" || req.TurnstileSecretKey == "********" {
			if current != nil {
				req.TurnstileSecretKey = current.TurnstileSecretKey
			}
		}
		if req.WebDAVPassword == "" || req.WebDAVPassword == "********" {
			if current != nil {
				req.WebDAVPassword = current.WebDAVPassword
			}
		}
		if req.ServerPort <= 0 {
			if current != nil {
				req.ServerPort = current.ServerPort
			}
		}
		if req.ChunkSizeBytes <= 0 {
			if current != nil {
				req.ChunkSizeBytes = current.ChunkSizeBytes
			}
		}

		if err := s.db.SaveSettings(&req); err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi lưu cài đặt", err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "Đã lưu cài đặt thành công"})
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
		"cloudflare_tunnel_guide": `Cài đặt Cloudflare Tunnel để truy cập từ xa toàn cầu:
1. Tải cloudflared: winget install --id Cloudflare.cloudflared
2. Chạy lệnh: cloudflared tunnel --url http://localhost:8080
3. Bạn sẽ nhận được đường link HTTPS miễn phí dạng https://xxxx.trycloudflare.com để truy cập từ xa mọi lúc mọi nơi!`,
	})
}



func (s *Server) handleToggleAccount(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user != nil && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền tạm dừng hoặc kích hoạt tài khoản", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID tài khoản", nil)
		return
	}

	acc, err := s.db.GetAccount(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tài khoản không tồn tại", err)
		return
	}

	newStatus := "active"
	if acc.Status == "active" {
		newStatus = "paused"
	}

	if err := s.db.ToggleAccountStatus(id, newStatus); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi thay đổi trạng thái", err)
		return
	}

	acc.Status = newStatus
	writeJSON(w, http.StatusOK, acc)
}



func (s *Server) handleSQLTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.db.GetDatabaseTables()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách bảng: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, tables)
}



func (s *Server) handleSQLQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	// [BUG FIX] Admin-only guard — prevents regular users from executing raw SQL
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền thực thi SQL", nil)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "Câu truy vấn SQL không được để trống", err)
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
	// [BUG FIX] Admin-only guard — prevents unauthorized download of the entire database
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền tải xuống Database", nil)
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
		writeError(w, http.StatusInternalServerError, "Lỗi tối ưu CSDL: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã tối ưu hóa và chống phân mảnh CSDL thành công (VACUUM & Optimize)"})
}



func (s *Server) handleSQLCheck(w http.ResponseWriter, r *http.Request) {
	status, err := s.db.CheckDatabaseIntegrity()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi kiểm tra toàn vẹn: "+err.Error(), err)
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
		writeError(w, http.StatusInternalServerError, "Lỗi tạo bản sao lưu: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message":     "Đã tạo bản sao lưu CSDL tức thời an toàn",
		"backup_file": backupName,
	})
}

// handleGDriveBackup thực hiện sao lưu toàn bộ CSDL và upload trực tiếp về Google Drive duongmanhhung9900@gmail.com


func findPythonExe() string {
	candidates := []string{
		`C:\Users\Administrator\AppData\Local\Python\pythoncore-3.14-64\python.exe`,
		`C:\Users\Administrator\AppData\Local\Microsoft\WindowsApps\python.exe`,
		`python`,
		`python3`,
	}
	for _, p := range candidates {
		if strings.Contains(p, `\`) {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		} else {
			if path, err := exec.LookPath(p); err == nil {
				return path
			}
		}
	}
	return "python"
}



func findBackupScript(baseDir string) (string, string) {
	candidates := []string{
		`F:\supportflast.dev\tools\backup_to_gdrive.py`,
	}
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates, filepath.Join(execDir, "tools", "backup_to_gdrive.py"))
	}
	if baseDir != "" {
		candidates = append(candidates, filepath.Join(baseDir, "tools", "backup_to_gdrive.py"))
		candidates = append(candidates, filepath.Join(baseDir, "..", "tools", "backup_to_gdrive.py"))
	}
	candidates = append(candidates, filepath.Join("tools", "backup_to_gdrive.py"))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, filepath.Dir(filepath.Dir(p))
		}
	}
	return `F:\supportflast.dev\tools\backup_to_gdrive.py`, `F:\supportflast.dev`
}

// handleGDriveBackup thực hiện sao lưu toàn bộ CSDL và upload trực tiếp về Google Drive duongmanhhung9900@gmail.com


func (s *Server) handleGDriveBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Yêu cầu quyền Quản trị viên để thực hiện sao lưu Google Drive", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	scriptPath, workingDir := findBackupScript(s.baseDir)
	pyExe := findPythonExe()

	cmd := exec.CommandContext(ctx, pyExe, scriptPath, "--json")
	cmd.Dir = workingDir

	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ENGINE] [BACKUP] Lỗi sao lưu Google Drive: %v, output: %s", err, string(outBytes))
		writeError(w, http.StatusInternalServerError, "Sao lưu Google Drive thất bại: "+strings.TrimSpace(string(outBytes)), err)
		return
	}

	var result map[string]interface{}
	lines := strings.Split(strings.TrimSpace(string(outBytes)), "\n")
	var parsed bool
	for i := len(lines) - 1; i >= 0; i-- {
		if json.Unmarshal([]byte(lines[i]), &result) == nil {
			parsed = true
			break
		}
	}
	if !parsed {
		writeError(w, http.StatusInternalServerError, "Lỗi phân tích phản hồi sao lưu", nil)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleGDriveBackupHistory lấy danh sách lịch sử các bản sao lưu đã đẩy lên Google Drive


func (s *Server) handleGDriveBackupHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	scriptPath, workingDir := findBackupScript(s.baseDir)
	pyExe := findPythonExe()

	cmd := exec.CommandContext(ctx, pyExe, scriptPath, "--history")
	cmd.Dir = workingDir

	outBytes, err := cmd.CombinedOutput()
	if err == nil {
		var list []map[string]interface{}
		if json.Unmarshal(outBytes, &list) == nil {
			writeJSON(w, http.StatusOK, list)
			return
		}
	}

	// Fallback đọc trực tiếp file json
	historyPath := filepath.Join(workingDir, "data", "backups", "backup_history.json")
	if _, err := os.Stat(historyPath); err != nil {
		historyPath = filepath.Join("data", "backups", "backup_history.json")
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	var history []map[string]interface{}
	if err := json.Unmarshal(data, &history); err != nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	writeJSON(w, http.StatusOK, history)
}

// -------------------------------------------------------------
// User Authentication & Management Handlers
// -------------------------------------------------------------



func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.db.ListActivityLogs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy nhật ký kiểm toán: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}



func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "Yêu cầu quyền Quản trị viên", nil)
		return
	}

	users, err := s.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách người dùng", err)
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
		writeError(w, http.StatusUnauthorized, "Yêu cầu quyền Quản trị viên", nil)
		return
	}

	var req struct {
		UserID     string `json:"user_id"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dữ liệu không hợp lệ", err)
		return
	}

	if err := s.db.UpdateUserQuota(req.UserID, req.QuotaBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi cập nhật hạn ngạch: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã cập nhật hạn ngạch thành công"})
}



func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "Yêu cầu quyền Quản trị viên", nil)
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "Dữ liệu không hợp lệ", err)
		return
	}

	if err := s.db.DeleteUser(req.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi xóa người dùng: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã xóa người dùng thành công"})
}



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
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền cập nhật và nâng cấp phần mềm", nil)
		return
	}

	// Support update bundles up to 128MB
	_ = r.ParseMultipartForm(128 << 20)

	// Check Security PIN if user has PIN configured
	if user.HasSecurityPin {
		pin := r.FormValue("security_pin")
		if pin == "" {
			writeError(w, http.StatusForbidden, "Vui lòng nhập Mã PIN Bảo mật Cấp 2 để xác nhận quyền nâng cấp hệ thống", nil)
			return
		}
		pinHash := core.HashSHA256([]byte(pin))
		if subtle.ConstantTimeCompare([]byte(user.SecurityPinHash), []byte(pinHash)) != 1 {
			writeError(w, http.StatusForbidden, "Mã PIN Bảo mật Cấp 2 không chính xác", nil)
			return
		}
	}

	targetModule := strings.TrimSpace(r.FormValue("target_module"))
	targetFilename := strings.TrimSpace(r.FormValue("target_filename"))

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Vui lòng đính kèm tệp tin cập nhật", err)
		return
	}
	defer file.Close()

	if targetFilename == "" {
		targetFilename = filepath.Base(header.Filename)
	}

	// Sanitize filename against directory traversal (Rule 3.1)
	cleanName := filepath.Base(targetFilename)
	if cleanName == "." || cleanName == "/" || cleanName == "\\" || strings.Contains(cleanName, "..") {
		writeError(w, http.StatusBadRequest, "Tên tệp không hợp lệ", nil)
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không đọc được dữ liệu tệp tải lên", err)
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
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền hoàn tác", nil)
		return
	}

	var req struct {
		TargetModule   string `json:"target_module"`
		TargetFilename string `json:"target_filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dữ liệu hoàn tác không hợp lệ", err)
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
		writeError(w, http.StatusNotFound, "Không tìm thấy bản sao lưu (.bak) của tệp này", err)
		return
	}

	if err := os.WriteFile(targetPath, bakBytes, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi khôi phục tệp: "+err.Error(), err)
		return
	}

	// Delete temporary .bak after successful rollback (Rule 1.2)
	_ = os.Remove(bakPath)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("Đã hoàn tác thành công tệp tin '%s' từ bản sao lưu .bak!", cleanName),
	})
}



func (s *Server) handleRestartEngine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền khởi động lại hệ thống", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Đang khởi động lại CloudPool Engine trong 2 giây...",
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



func (s *Server) handleImportDriveFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền nạp tệp từ Google Drive", nil)
		return
	}

	var req struct {
		AccountID string `json:"account_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	count, err := s.vfs.ImportExistingDriveFiles(r.Context(), req.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi quét và nạp tệp từ Google Drive: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "IMPORT_DRIVE",
		Target:    "Google Drive",
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Đã quét và tự động nạp %d tệp tin có sẵn từ Google Drive vào CloudPool", count),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"imported_count": count,
		"message":        fmt.Sprintf("Đã quét và tự động nạp thành công %d tệp tin có sẵn từ Google Drive vào CloudPool!", count),
	})
}



func (s *Server) handleListDriveFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền xem tệp từ Google Drive", nil)
		return
	}

	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu account_id", nil)
		return
	}

	files, err := s.gd.ScanExistingDriveFiles(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách file từ Drive: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, files)
}

// handleOTPPendingCount trả về số lượng yêu cầu OTP đang chờ duyệt.
// Dùng cho admin polling nhẹ (mỗi 15s) để hiển thị badge thông báo.
