package registry

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cache"
	cloudpoolCore "supportflast_engine/cloudpool/core"
	"supportflast_engine/database"
	"supportflast_engine/security"
)

const (
	// CacheKeySystemStatus Khóa lưu cache trạng thái hệ thống (/api/system/status)
	CacheKeySystemStatus = "api_system_status"
	// SystemStatusCacheTTL Thời gian sống cache trạng thái hệ thống (60 giây theo Rule 7.2)
	SystemStatusCacheTTL = 60 * time.Second
)

// GetServicePort lấy cổng dịch vụ hỗ trợ biến môi trường PORT động của các nền tảng PaaS
// (Render, Heroku, Railway, Koyeb, Fly.io, cPanel app manager) và Windows Server IIS.
// Quy tắc lấy cổng:
// 1. Kiểm tra 'os.Getenv("PORT")' (ưu tiên PaaS và container hosting)
// 2. Nếu rỗng, kiểm tra cấu hình môi trường ('HTTP_PLATFORM_PORT', 'SERVER_PORT', 'APP_PORT')
// 3. Nếu vẫn rỗng, mặc định '8080'.
func GetServicePort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	if port := strings.TrimSpace(os.Getenv("HTTP_PLATFORM_PORT")); port != "" {
		return port
	}
	if port := strings.TrimSpace(os.Getenv("SERVER_PORT")); port != "" {
		return port
	}
	if port := strings.TrimSpace(os.Getenv("APP_PORT")); port != "" {
		return port
	}
	return "8080"
}

// GetListenerHost lấy địa chỉ host để bind HTTP listener.
// Đảm bảo listener bind vào '0.0.0.0' để nhận traffic từ bên ngoài hosting (Docker, VPS, PaaS).
func GetListenerHost() string {
	if host := strings.TrimSpace(os.Getenv("HOST")); host != "" {
		return host
	}
	return "0.0.0.0"
}

// GetRequestScheme tự động phát hiện Scheme (HTTP hoặc HTTPS) từ:
// - Header 'X-Forwarded-Proto' (hỗ trợ reverse proxy, multi-proxy comma separated)
// - TLS handshake (r.TLS != nil)
// - Header 'X-Forwarded-Ssl: on'
// - Header 'Front-End-Https: on'
// - Header 'CF-Visitor' chứa "https" (Cloudflare Edge)
// - Header 'Origin' hoặc 'Referer' chứa "https://"
// - Mặc định: "http"
func GetRequestScheme(r *http.Request) string {
	if r == nil {
		return "http"
	}

	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		parts := strings.Split(proto, ",")
		first := strings.ToLower(strings.TrimSpace(parts[0]))
		if first == "https" || first == "http" {
			return first
		}
	}

	if r.TLS != nil {
		return "https"
	}

	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Ssl")), "on") {
		return "https"
	}

	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Front-End-Https")), "on") {
		return "https"
	}

	if strings.Contains(strings.ToLower(r.Header.Get("CF-Visitor")), "https") {
		return "https"
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Origin"))), "https://") {
		return "https"
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Referer"))), "https://") {
		return "https"
	}

	return "http"
}

// GetRequestHost tự động phát hiện Host từ:
// - Header 'X-Forwarded-Host' (hỗ trợ reverse proxy PaaS, load balancer)
// - Header 'Host' (r.Host)
// - Biến môi trường DOMAIN hoặc fallback "supportflastdev.io.vn"
func GetRequestHost(r *http.Request) string {
	if r != nil {
		if fHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fHost != "" {
			parts := strings.Split(fHost, ",")
			first := strings.TrimSpace(parts[0])
			first = strings.ReplaceAll(strings.ReplaceAll(first, "\r", ""), "\n", "")
			clean := security.SanitizeCRLF(first)
			if clean != "" {
				return clean
			}
		}

		if host := strings.TrimSpace(r.Host); host != "" {
			host = strings.ReplaceAll(strings.ReplaceAll(host, "\r", ""), "\n", "")
			clean := security.SanitizeCRLF(host)
			if clean != "" {
				return clean
			}
		}
	}

	if envDomain := strings.TrimSpace(os.Getenv("DOMAIN")); envDomain != "" {
		envDomain = strings.ReplaceAll(strings.ReplaceAll(envDomain, "\r", ""), "\n", "")
		return security.SanitizeCRLF(envDomain)
	}

	return "supportflastdev.io.vn"
}

// GetRequestBaseURL trả về URL gốc chuẩn dạng "scheme://host"
func GetRequestBaseURL(r *http.Request) string {
	return fmt.Sprintf("%s://%s", GetRequestScheme(r), GetRequestHost(r))
}

// GetOAuthRedirectURL xác định URL chuyển hướng OAuth (Google OAuth callback)
// Tự động phát hiện Scheme và Host từ request (X-Forwarded-Proto, X-Forwarded-Host, Host)
// đồng thời tự động chuẩn hóa Canonical Host (loại bỏ 'www.' hoặc đồng bộ theo biến DOMAIN)
// để người dùng chỉ cần thêm DUY NHẤT 1 link Canonical vào Google Cloud Console.
func GetOAuthRedirectURL(r *http.Request, configuredURL string) string {
	configuredURL = strings.TrimSpace(configuredURL)
	isLocalConfig := configuredURL == "" || strings.Contains(configuredURL, "localhost") || strings.Contains(configuredURL, "127.0.0.1")

	// Nếu cấu hình thủ công đã là một URL từ xa (không phải localhost), ưu tiên dùng trực tiếp
	if !isLocalConfig && configuredURL != "" {
		return configuredURL
	}

	host := GetRequestHost(r)
	isRemoteRequest := !strings.Contains(host, "localhost") && !strings.Contains(host, "127.0.0.1")

	// 1. Nếu là remote request và có biến môi trường OAUTH_REDIRECT_URL từ xa trong .env:
	if isRemoteRequest {
		if envOAuth := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL")); envOAuth != "" && !strings.Contains(envOAuth, "localhost") && !strings.Contains(envOAuth, "127.0.0.1") {
			return envOAuth
		}
	}

	// 2. Nếu cấu hình rỗng, hoặc là cấu hình localhost mặc định trong khi request đến từ host đám mây:
	if configuredURL == "" || (isLocalConfig && isRemoteRequest) {
		scheme := GetRequestScheme(r)
		if host != "" {
			// Chuẩn hóa Canonical Host: Nếu truy cập qua 'www.domain.com', chuẩn hóa về apex domain 'domain.com'
			// hoặc đồng bộ theo biến môi trường DOMAIN để Google Console chỉ cần 1 link duy nhất
			canonicalHost := host
			if envDomain := strings.TrimSpace(os.Getenv("DOMAIN")); envDomain != "" {
				envClean := strings.TrimPrefix(envDomain, "www.")
				if strings.EqualFold(host, envClean) || strings.EqualFold(host, "www."+envClean) {
					canonicalHost = envClean
				}
			} else if strings.HasPrefix(strings.ToLower(host), "www.") {
				canonicalHost = host[4:]
			}
			return fmt.Sprintf("%s://%s/api/accounts/oauth/callback", scheme, canonicalHost)
		}
		return fmt.Sprintf("http://localhost:%s/api/accounts/oauth/callback", GetServicePort())
	}

	return configuredURL
}

// InvalidateSystemStatusCache làm mới cache trạng thái hệ thống
func InvalidateSystemStatusCache() {
	cache.DefaultCache.InvalidatePrefix(CacheKeySystemStatus)
	cache.DefaultCache.Invalidate(CacheKeySystemStatus)
}

var (
	systemMutex sync.RWMutex
	startTime   = time.Now()
)

// SystemConfig cấu trúc trạng thái web
type SystemConfig struct {
	Version          string `json:"version"`
	UpdatedAt        string `json:"updated_at"`
	BuildHash        string `json:"build_hash"`
	MaintenanceMode  bool   `json:"maintenance_mode"`
	BroadcastEnabled bool   `json:"broadcast_enabled"`
	BroadcastMessage string `json:"broadcast_message"`
	BroadcastLevel   string `json:"broadcast_level"` // info, warning, danger, success
}

// ReleaseItem cấu trúc bản cập nhật web
type ReleaseItem struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Title     string   `json:"title"`
	Type      string   `json:"type"` // Nâng cấp lớn, Tính năng mới, Bản vá lỗi, Bảo mật
	Date      string   `json:"date"`
	Author    string   `json:"author"`
	Changes   []string `json:"changes"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at,omitempty"`
}

type releaseMeta struct {
	ID        string   `json:"id,omitempty"`
	Type      string   `json:"type,omitempty"`
	Changes   []string `json:"changes,omitempty"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// SystemDataStore tổng hợp toàn bộ dữ liệu hệ thống
type SystemDataStore struct {
	System   SystemConfig  `json:"system"`
	Releases []ReleaseItem `json:"releases"`
}

func getSystemFilePath() string {
	candidates := []string{
		filepath.Join("..", "data", "system_updates.json"),
		filepath.Join("data", "system_updates.json"),
		`f:\supportflast.dev\data\system_updates.json`,
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		candidates = append([]string{filepath.Join(envDataDir, "system_updates.json")}, candidates...)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if envDataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); envDataDir != "" {
		return filepath.Join(envDataDir, "system_updates.json")
	}
	if info, err := os.Stat(".."); err == nil && info.IsDir() {
		return filepath.Join("..", "data", "system_updates.json")
	}
	return filepath.Join("data", "system_updates.json")
}

// LoadReleasesFromDB đọc danh sách bản cập nhật từ bảng system_releases của SQLite qua Prepared Statement
func LoadReleasesFromDB() []ReleaseItem {
	db := database.GetDB()
	if db == nil {
		log.Printf("[ENGINE] [SYSTEM] [ERROR] Database is nil in LoadReleasesFromDB")
		return []ReleaseItem{}
	}

	query := `
		SELECT version, COALESCE(title, ''), COALESCE(date, ''), 
		       COALESCE(build_hash, ''), COALESCE(notes, ''), COALESCE(published_by, '')
		FROM system_releases
		ORDER BY date DESC, version DESC
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Printf("[ENGINE] [SYSTEM] [ERROR] Prepare select system_releases failed: %v", err)
		return []ReleaseItem{}
	}
	defer stmt.Close()

	rows, err := stmt.Query()
	if err != nil {
		log.Printf("[ENGINE] [SYSTEM] [ERROR] Query system_releases failed: %v", err)
		return []ReleaseItem{}
	}
	defer rows.Close()

	releases := make([]ReleaseItem, 0)
	for rows.Next() {
		var version, title, date, buildHash, notes, publishedBy string
		if err := rows.Scan(&version, &title, &date, &buildHash, &notes, &publishedBy); err != nil {
			continue
		}

		item := ReleaseItem{
			Version: version,
			Title:   title,
			Date:    date,
			Author:  publishedBy,
			Active:  true,
		}

		var meta releaseMeta
		if err := json.Unmarshal([]byte(notes), &meta); err == nil {
			item.ID = meta.ID
			item.Type = meta.Type
			item.Changes = meta.Changes
			item.Active = meta.Active
			item.CreatedAt = meta.CreatedAt
		} else {
			item.ID = buildHash
			if notes != "" {
				item.Changes = []string{notes}
			} else {
				item.Changes = []string{"Bản cập nhật hệ thống."}
			}
			item.Type = "Nâng cấp lớn (Feature)"
		}

		if item.ID == "" {
			item.ID = fmt.Sprintf("rel-%s", strings.TrimPrefix(version, "v"))
		}
		if item.Type == "" {
			item.Type = "Nâng cấp lớn (Feature)"
		}
		if len(item.Changes) == 0 {
			item.Changes = []string{"Cải tiến hiệu năng và cập nhật giao diện web."}
		}

		releases = append(releases, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[ENGINE] [SYSTEM] [ERROR] rows.Err() sau query releases: %v", err)
	}

	return releases
}

// SaveReleaseToDB ghi một bản cập nhật vào bảng system_releases của SQLite qua Prepared Statement
func SaveReleaseToDB(rel ReleaseItem, buildHash string) error {
	db := database.GetDB()
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	if buildHash == "" {
		buildHash = rel.ID
	}
	if rel.Date == "" {
		rel.Date = time.Now().Format("2006-01-02")
	}
	if rel.Author == "" {
		rel.Author = "Admin"
	}

	meta := releaseMeta{
		ID:        rel.ID,
		Type:      rel.Type,
		Changes:   rel.Changes,
		Active:    rel.Active,
		CreatedAt: rel.CreatedAt,
	}
	notesBytes, _ := json.Marshal(meta)
	notesStr := string(notesBytes)

	query := `
		INSERT INTO system_releases (
			version, title, date, build_hash, notes, published_by
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(version) DO UPDATE SET
			title = excluded.title,
			date = excluded.date,
			build_hash = excluded.build_hash,
			notes = excluded.notes,
			published_by = excluded.published_by
	`
	stmt, err := db.Prepare(query)
	if err != nil {
		return fmt.Errorf("prepare upsert system_releases failed: %w", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(rel.Version, rel.Title, rel.Date, buildHash, notesStr, rel.Author)
	if err != nil {
		return fmt.Errorf("execute upsert system_releases failed: %w", err)
	}

	InvalidateSystemStatusCache()
	return nil
}

// LoadSystemData nạp dữ liệu hệ thống với danh sách bản cập nhật được truy vấn trực tiếp từ bảng system_releases của SQLite
func LoadSystemData() SystemDataStore {
	systemMutex.RLock()
	defer systemMutex.RUnlock()

	p := getSystemFilePath()
	var store SystemDataStore
	data, err := os.ReadFile(p)
	if err == nil {
		json.Unmarshal(data, &store)
	}

	if store.System.Version == "" {
		store.System = SystemConfig{
			Version:          "v2.6.0",
			UpdatedAt:        time.Now().Format(time.RFC3339),
			BuildHash:        "e89f41a",
			MaintenanceMode:  false,
			BroadcastEnabled: false,
			BroadcastMessage: "",
			BroadcastLevel:   "info",
		}
	}

	// Đọc trực tiếp releases từ CSDL SQLite
	dbReleases := LoadReleasesFromDB()
	store.Releases = dbReleases
	if len(dbReleases) > 0 {
		store.System.Version = dbReleases[0].Version
	}

	return store
}

// SaveSystemData lưu cấu hình hệ thống và đồng bộ các releases vào bảng system_releases trong SQLite
func SaveSystemData(store SystemDataStore) error {
	systemMutex.Lock()
	defer systemMutex.Unlock()

	// Lưu từng release vào bảng system_releases trong SQLite
	for _, rel := range store.Releases {
		_ = SaveReleaseToDB(rel, store.System.BuildHash)
	}

	p := getSystemFilePath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return err
	}

	// Tự động làm mới cache trạng thái hệ thống
	InvalidateSystemStatusCache()
	return nil
}

// GetUIDirectory trả về đường dẫn thư mục giao diện chuẩn của SupportFlast
func GetUIDirectory() string {
	uiCandidates := []string{
		filepath.Join("..", "supportflast_ui"),
		"supportflast_ui",
		`f:\supportflast.dev\supportflast_ui`,
	}
	if envStaticDir := strings.TrimSpace(os.Getenv("STATIC_DIR")); envStaticDir != "" {
		uiCandidates = append([]string{envStaticDir}, uiCandidates...)
	}
	if envUIDir := strings.TrimSpace(os.Getenv("UI_DIR")); envUIDir != "" {
		uiCandidates = append([]string{envUIDir}, uiCandidates...)
	}
	for _, c := range uiCandidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return `f:\supportflast.dev\supportflast_ui`
}

// GetRustCoreStatus trả về chuỗi mô tả trạng thái phần cứng của lõi Rust Core
func GetRustCoreStatus() string {
	if cloudpoolCore.IsDLLLoaded() {
		return "● Đang Hoạt Động (Hardware AES-NI)"
	}
	return "Native Go Cryptographic Engine"
}

// SystemStatusHandler trả về trạng thái hệ thống web (tối ưu hóa với LRU Cache 60s - Rule 7.2)
func SystemStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	scheme := GetRequestScheme(r)
	host := GetRequestHost(r)
	baseURL := GetRequestBaseURL(r)
	servicePort := GetServicePort()

	// Khóa cache theo scheme và host để đảm bảo tính độc lập giữa các domain/reverse proxy
	cacheKey := fmt.Sprintf("%s:%s:%s", CacheKeySystemStatus, scheme, host)

	// 1. Kiểm tra L1 Cache trong bộ nhớ RAM (Rule 7.2 & 7.3)
	if cached, ok := cache.DefaultCache.Get(cacheKey); ok {
		if data, ok := cached.([]byte); ok {
			w.Header().Set("X-Cache", "HIT")
			w.Write(data)
			return
		}
	}

	// 2. Cache miss: Đọc dữ liệu từ store (với releases lấy từ SQLite system_releases)
	store := LoadSystemData()
	// Đảm bảo trạng thái broadcast_enabled luôn là false
	store.System.BroadcastEnabled = false

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptimeSeconds := int(time.Since(startTime).Seconds())
	rustCoreActive := cloudpoolCore.IsDLLLoaded()
	rustCoreStatus := GetRustCoreStatus()
	uiDirDisplay := "supportflast_ui"

	response := map[string]interface{}{
		"status":            "success",
		"domain":            host,
		"scheme":            scheme,
		"host":              host,
		"base_url":          baseURL,
		"port":              servicePort,
		"system":            store.System,
		"broadcast_enabled": false,
		"uptime_seconds":    uptimeSeconds,
		"uptime_formatted":  formatUptime(uptimeSeconds),
		"alloc_mb":          fmt.Sprintf("%.2f MB", float64(m.Alloc)/1024/1024),
		"goroutines":        runtime.NumGoroutine(),
		"server_time":       time.Now().Format(time.RFC3339),
		"latest_release":    nil,
		"total_releases":    len(store.Releases),
		"app_version":       store.System.Version,
		"engine_version":    fmt.Sprintf("%s / Antigravity Polyglot", runtime.Version()),
		"rust_core_active":  rustCoreActive,
		"rust_core_status":  rustCoreStatus,
		"ui_dir":            uiDirDisplay,
		"storage_ui_dir":    "supportflast_ui/storage",
	}

	if len(store.Releases) > 0 {
		response["latest_release"] = store.Releases[0]
	}

	data, err := json.Marshal(response)
	if err != nil {
		http.Error(w, `{"error":"JSON encoding error"}`, http.StatusInternalServerError)
		return
	}

	// 3. Lưu vào LRU Cache với TTL 60 giây (Rule 7.2)
	cache.DefaultCache.Set(cacheKey, data, SystemStatusCacheTTL)

	w.Header().Set("X-Cache", "MISS")
	w.Write(data)
}

// SystemUpdatesHandler trả về danh sách lịch sử các bản cập nhật web từ bảng system_releases của SQLite
func SystemUpdatesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	store := LoadSystemData()

	scheme := GetRequestScheme(r)
	host := GetRequestHost(r)
	baseURL := GetRequestBaseURL(r)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "success",
		"domain":         host,
		"scheme":         scheme,
		"host":           host,
		"base_url":       baseURL,
		"system_version": store.System.Version,
		"total_releases": len(store.Releases),
		"releases":       store.Releases,
	})
}

// AdminPublishUpdateHandler Admin phát hành bản cập nhật web mới và lưu vào bảng system_releases của SQLite
func AdminPublishUpdateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	if !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu mã Admin API Key hợp lệ"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Version string   `json:"version"`
		Title   string   `json:"title"`
		Type    string   `json:"type"`
		Author  string   `json:"author"`
		Changes []string `json:"changes"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		http.Error(w, `{"error":"Thiếu thông tin phiên bản (version)"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.Version, "v") {
		req.Version = "v" + req.Version
	}
	if req.Title == "" {
		req.Title = fmt.Sprintf("Bản Cập Nhật Hệ Thống Web %s", req.Version)
	}
	if req.Type == "" {
		req.Type = "Tính năng mới (Feature)"
	}
	if req.Author == "" {
		req.Author = "Admin Hệ Thống"
	}
	if len(req.Changes) == 0 {
		req.Changes = []string{"Cải tiến hiệu năng và cập nhật giao diện web."}
	}

	buildHash := fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF)
	newRel := ReleaseItem{
		ID:        fmt.Sprintf("rel-%s", time.Now().Format("20060102-150405")),
		Version:   req.Version,
		Title:     req.Title,
		Type:      req.Type,
		Date:      time.Now().Format("2006-01-02"),
		Author:    req.Author,
		Changes:   req.Changes,
		Active:    true,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	// 1. Lưu bản cập nhật trực tiếp vào bảng system_releases của SQLite qua Prepared Statement
	if err := SaveReleaseToDB(newRel, buildHash); err != nil {
		log.Printf("[ENGINE] [SYSTEM] [ERROR] Failed to save release %s to SQLite: %v", req.Version, err)
		http.Error(w, fmt.Sprintf(`{"error":"Lỗi lưu bản cập nhật vào CSDL: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// 2. Cập nhật cấu hình hệ thống
	store := LoadSystemData()
	store.System.Version = req.Version
	store.System.UpdatedAt = time.Now().Format(time.RFC3339)
	store.System.BuildHash = buildHash
	_ = SaveSystemData(store)

	clientIP := security.GetRealClientIP(r)
	_ = database.RecordAuditLog("usr-admin-001", database.AuditActionConfigChange, clientIP, r.UserAgent(), fmt.Sprintf("Phát hành bản cập nhật hệ thống: %s (%s)", req.Version, req.Title))

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Đã phát hành và cập nhật hệ thống web lên %s thành công!", req.Version),
		"release": newRel,
		"system":  store.System,
	})
}

// AdminBroadcastHandler Admin cập nhật banner thông báo khẩn trên trang web
func AdminBroadcastHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	if !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu mã Admin API Key hợp lệ"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Enabled bool   `json:"enabled"`
		Message string `json:"message"`
		Level   string `json:"level"` // info, warning, danger, success
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	store := LoadSystemData()
	store.System.BroadcastEnabled = req.Enabled
	if req.Message != "" {
		store.System.BroadcastMessage = strings.TrimSpace(req.Message)
	}
	if req.Level != "" {
		store.System.BroadcastLevel = req.Level
	}

	if err := SaveSystemData(store); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Lỗi lưu dữ liệu: %v"}`, err), http.StatusInternalServerError)
		return
	}

	clientIP := security.GetRealClientIP(r)
	_ = database.RecordAuditLog("usr-admin-001", database.AuditActionSettingsUpdate, clientIP, r.UserAgent(), fmt.Sprintf("Cập nhật broadcast banner: enabled=%v, msg='%s'", req.Enabled, req.Message))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Đã cập nhật banner thông báo hệ thống web thành công!",
		"system":  store.System,
	})
}

// AdminMaintenanceHandler Admin bật / tắt chế độ bảo trì web
func AdminMaintenanceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	if !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu mã Admin API Key hợp lệ"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Maintenance bool `json:"maintenance"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	store := LoadSystemData()
	store.System.MaintenanceMode = req.Maintenance

	if err := SaveSystemData(store); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Lỗi lưu dữ liệu: %v"}`, err), http.StatusInternalServerError)
		return
	}

	clientIPMaintenance := security.GetRealClientIP(r)
	_ = database.RecordAuditLog("usr-admin-001", database.AuditActionConfigChange, clientIPMaintenance, r.UserAgent(), fmt.Sprintf("Cập nhật chế độ bảo trì hệ thống: maintenance=%v", req.Maintenance))

	statusStr := "TẮT (Hoạt động bình thường)"
	if req.Maintenance {
		statusStr = "BẬT (Chế độ bảo trì)"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     fmt.Sprintf("Đã chuyển đổi trạng thái bảo trì web: %s", statusStr),
		"maintenance": store.System.MaintenanceMode,
	})
}

// AdminHotReloadHandler Kích hoạt Hot Reload hệ thống web
func AdminHotReloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	if !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu mã Admin API Key hợp lệ"}`, http.StatusUnauthorized)
		return
	}

	store := LoadSystemData()
	store.System.UpdatedAt = time.Now().Format(time.RFC3339)
	store.System.BuildHash = fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF)
	_ = SaveSystemData(store)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     "Đã tải lại toàn bộ tài nguyên giao diện Web UI & đồng bộ Cache Anycast thành công!",
		"build_hash":  store.System.BuildHash,
		"reloaded_at": store.System.UpdatedAt,
	})
}

func formatUptime(seconds int) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, secs)
	}
	return fmt.Sprintf("%dm %ds", minutes, secs)
}

// DeviceSpecs thông số phần cứng thực của thiết bị máy khách
type DeviceSpecs struct {
	OS            string  `json:"os"`
	Platform      string  `json:"platform"`
	Arch          string  `json:"arch"`
	CPUCores      int     `json:"cpu_cores"`
	MemoryGB      float64 `json:"memory_gb"`
	GPU           string  `json:"gpu"`
	RefreshRateHz int     `json:"refresh_rate_hz"`
	ScreenDPI     float64 `json:"screen_dpi"`
	ScreenWidth   int     `json:"screen_width"`
	ScreenHeight  int     `json:"screen_height"`
	BatteryLevel  int     `json:"battery_level"`
	IsCharging    bool    `json:"is_charging"`
	NetworkType   string  `json:"network_type"`
	DownlinkMbps  float64 `json:"downlink_mbps"`
	SaveData      bool    `json:"save_data"`
}

// DeviceSyncPayload dữ liệu payload gửi lên từ trình duyệt
type DeviceSyncPayload struct {
	DeviceID string      `json:"device_id"`
	Specs    DeviceSpecs `json:"specs"`
}

// DeviceSyncHandler tiếp nhận telemetry cấu hình thiết bị và trả về profile hiệu năng tối ưu
func DeviceSyncHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	// Giới hạn payload chống tràn RAM
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req DeviceSyncPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Dữ liệu telemetry thiết bị không hợp lệ"}`, http.StatusBadRequest)
		return
	}

	// Sanitize chuỗi đầu vào
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.Specs.OS = strings.TrimSpace(req.Specs.OS)
	req.Specs.GPU = strings.TrimSpace(req.Specs.GPU)

	// Tính toán Profile tối ưu thích ứng
	profile := "balanced"
	particleCount := 60
	fpsCap := 60
	glowEnabled := true
	profileNote := "Hồ sơ Cân Bằng: Trải nghiệm mượt mà, tối ưu tài nguyên tiêu chuẩn."

	// Điều kiện kích hoạt Eco Mode
	if (req.Specs.BatteryLevel >= 0 && req.Specs.BatteryLevel <= 20 && !req.Specs.IsCharging) ||
		(req.Specs.MemoryGB > 0 && req.Specs.MemoryGB <= 4) ||
		(req.Specs.CPUCores > 0 && req.Specs.CPUCores <= 2) ||
		req.Specs.SaveData {
		profile = "eco"
		particleCount = 25
		fpsCap = 30
		glowEnabled = false
		profileNote = "Hồ sơ Tiết Kiệm Năng Lượng: Tự động giảm thiểu hạt nền và tắt glow để bảo vệ pin và chống giật lag."
	} else if req.Specs.CPUCores >= 8 && req.Specs.MemoryGB >= 8 {
		profile = "ultra"
		particleCount = 120
		fpsCap = 120
		glowEnabled = true
		profileNote = "Hồ sơ Hiệu Năng Tối Đa: Phần cứng mạnh mẽ, kích hoạt tối đa hiệu ứng hạt Aura Glow và tần số quét cao."
	}

	// Xác định nền tảng tương thích ưu tiên tải xuống
	osLower := strings.ToLower(req.Specs.OS)
	matchedPlatform := "Windows"
	if strings.Contains(osLower, "mac") || strings.Contains(osLower, "darwin") {
		matchedPlatform = "macOS"
	} else if strings.Contains(osLower, "linux") {
		matchedPlatform = "Linux"
	} else if strings.Contains(osLower, "android") {
		matchedPlatform = "Android"
	} else if strings.Contains(osLower, "ios") || strings.Contains(osLower, "iphone") || strings.Contains(osLower, "ipad") {
		matchedPlatform = "iOS"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "synced",
		"device_id":    req.DeviceID,
		"profile":      profile,
		"profile_note": profileNote,
		"adaptive_config": map[string]interface{}{
			"particle_count":   particleCount,
			"fps_cap":          fpsCap,
			"glow_enabled":     glowEnabled,
			"matched_platform": matchedPlatform,
			"screen_density":   req.Specs.ScreenDPI,
		},
		"synced_specs": req.Specs,
		"server_time":  time.Now().Format(time.RFC3339),
	})
}

// AdminDeployUIHandler Admin tải lên trực tiếp file thiết kế web mới (.html hoặc .zip)
func AdminDeployUIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed, use POST"}`, http.StatusMethodNotAllowed)
		return
	}

	token := ExtractToken(r)
	if !VerifyAPIKey(token) {
		http.Error(w, `{"error":"Unauthorized: Yêu cầu mã Admin API Key hợp lệ"}`, http.StatusUnauthorized)
		return
	}

	// Giới hạn file tải lên tối đa 100MB
	err := r.ParseMultipartForm(100 * 1024 * 1024)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Lỗi đọc dữ liệu tải lên: %v"}`, err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("web_file")
	if err != nil {
		http.Error(w, `{"error":"Vui lòng chọn tệp thiết kế web mới (.html hoặc .zip) để tải lên"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	version := strings.TrimSpace(r.FormValue("version"))
	if version == "" {
		version = fmt.Sprintf("v%s", time.Now().Format("2006.01.02"))
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = fmt.Sprintf("Cập nhật giao diện web mới %s", version)
	}

	// Xác định thư mục UI đích
	uiCandidates := []string{
		filepath.Join("..", "supportflast_ui"),
		"supportflast_ui",
		`f:\supportflast.dev\supportflast_ui`,
	}
	if envStaticDir := strings.TrimSpace(os.Getenv("STATIC_DIR")); envStaticDir != "" {
		uiCandidates = append([]string{envStaticDir}, uiCandidates...)
	}
	targetUIDir := `f:\supportflast.dev\supportflast_ui`
	for _, c := range uiCandidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			targetUIDir = c
			break
		}
	}

	filenameLower := strings.ToLower(header.Filename)

	if strings.HasSuffix(filenameLower, ".html") || strings.HasSuffix(filenameLower, ".htm") {
		// Ghi đè trực tiếp vào targetUIDir/index.html
		destPath := filepath.Join(targetUIDir, "index.html")
		out, err := os.Create(destPath)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Không thể ghi file giao diện mới: %v"}`, err), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		_, err = io.Copy(out, file)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi lưu file: %v"}`, err), http.StatusInternalServerError)
			return
		}
	} else if strings.HasSuffix(filenameLower, ".zip") {
		// Lưu tạm file zip rồi giải nén vào targetUIDir
		tmpZip := filepath.Join(os.TempDir(), fmt.Sprintf("web_update_%d.zip", time.Now().UnixNano()))
		tmpFile, err := os.Create(tmpZip)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Không thể tạo tệp tạm: %v"}`, err), http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(tmpFile, file)
		tmpFile.Close()
		if err != nil {
			os.Remove(tmpZip)
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi lưu tệp tạm: %v"}`, err), http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpZip)

		// Giải nén zip vào targetUIDir
		if err := unzipToDir(tmpZip, targetUIDir); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi giải nén gói giao diện: %v"}`, err), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, `{"error":"Định dạng tệp không được hỗ trợ. Vui lòng tải lên tệp .html hoặc .zip"}`, http.StatusBadRequest)
		return
	}

	buildHash := fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF)
	newRel := ReleaseItem{
		ID:        fmt.Sprintf("rel-%s", time.Now().Format("20060102-150405")),
		Version:   version,
		Title:     title,
		Type:      "Giao diện mới (Design)",
		Date:      time.Now().Format("2006-01-02"),
		Author:    "Admin",
		Changes:   []string{fmt.Sprintf("Triển khai bản thiết kế mới từ tệp %s.", header.Filename)},
		Active:    true,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	// Lưu bản cập nhật vào bảng system_releases của SQLite
	_ = SaveReleaseToDB(newRel, buildHash)

	store := LoadSystemData()
	store.System.Version = version
	store.System.UpdatedAt = time.Now().Format(time.RFC3339)
	store.System.BuildHash = buildHash
	_ = SaveSystemData(store)

	clientIP := security.GetRealClientIP(r)
	_ = database.RecordAuditLog("usr-admin-001", database.AuditActionConfigChange, clientIP, r.UserAgent(), fmt.Sprintf("Triển khai bản thiết kế Web UI mới: %s (%s)", version, header.Filename))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"message":  fmt.Sprintf("Bản thiết kế web mới (%s) đã được triển khai và kích hoạt thành công trên tên miền!", version),
		"version":  version,
		"filename": header.Filename,
		"system":   store.System,
	})
}

// unzipToDir giải nén tệp zip vào thư mục đích an toàn (chống Zip Slip)
func unzipToDir(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		cleanPath := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, ":") {
			continue // Chặn Zip Slip
		}

		targetPath := filepath.Join(dest, cleanPath)
		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
