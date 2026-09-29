package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	cloudpoolApi "supportflast_engine/cloudpool/api"
	cloudpoolGDrive "supportflast_engine/cloudpool/gdrive"
	cloudpoolStorage "supportflast_engine/cloudpool/storage"
	cloudpoolVFS "supportflast_engine/cloudpool/vfs"
	"supportflast_engine/database"
	"supportflast_engine/registry"
	"supportflast_engine/security"
	"testing"
)

func TestEnvironmentConfiguration_Defaults(t *testing.T) {
	// Kiểm tra defaultPort và defaultHost
	if defaultPort != "8080" {
		t.Errorf("Expected defaultPort to be 8080, got: %s", defaultPort)
	}
	if defaultHost != "0.0.0.0" {
		t.Errorf("Expected defaultHost to be 0.0.0.0, got: %s", defaultHost)
	}
}

func TestDynamicPortAndHostResolution(t *testing.T) {
	// 1. Kiểm tra biến môi trường PORT từ các nền tảng PaaS
	t.Setenv("PORT", "5000")
	if p := registry.GetServicePort(); p != "5000" {
		t.Errorf("Kỳ vọng PORT=5000, nhận: %s", p)
	}

	// 2. Mặc định bind vào 0.0.0.0 để nhận traffic hosting
	t.Setenv("HOST", "")
	if h := registry.GetListenerHost(); h != "0.0.0.0" {
		t.Errorf("Kỳ vọng 0.0.0.0, nhận: %s", h)
	}
}

func TestEnvironmentConfiguration_AIEngineURL(t *testing.T) {
	// 1. Khi chưa set AI_ENGINE_URL
	t.Setenv("AI_ENGINE_URL", "")
	if url := getAIEngineURL(); url != "http://127.0.0.1:8000" {
		t.Errorf("Expected fallback AI engine URL, got: %s", url)
	}

	// 2. Khi set AI_ENGINE_URL
	t.Setenv("AI_ENGINE_URL", "http://supportflast_ai:8000/")
	if url := getAIEngineURL(); url != "http://supportflast_ai:8000" {
		t.Errorf("Expected trimmed AI engine URL, got: %s", url)
	}
}

func TestEnvironmentConfiguration_DataDirResolution(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DATA_DIR", tempDir)

	dbPath := database.ResolveDBPath()
	expectedPath := filepath.Join(tempDir, "supportflast.db")

	if dbPath != expectedPath {
		t.Errorf("Expected dbPath to be '%s', got '%s'", expectedPath, dbPath)
	}
}

func TestEnvironmentConfiguration_AllowedOrigins(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://supportflast-frontend.vercel.app, https://my-frontend.pages.dev")
	security.LoadAllowedOriginsFromEnv()

	if !security.IsOriginAllowed("https://supportflast-frontend.vercel.app") {
		t.Errorf("Expected https://supportflast-frontend.vercel.app to be allowed via ALLOWED_ORIGINS")
	}
	if !security.IsOriginAllowed("https://my-frontend.pages.dev") {
		t.Errorf("Expected https://my-frontend.pages.dev to be allowed via ALLOWED_ORIGINS")
	}
	if security.IsOriginAllowed("https://untrusted-site.com") {
		t.Errorf("Security check failed: untrusted-site.com should NOT be allowed")
	}
}

func TestCORS_PreflightIntegration(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://custom-deploy.railway.app")
	security.LoadAllowedOriginsFromEnv()

	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	handler := security.SecurityHeadersMiddleware(dummy)

	req := httptest.NewRequest("OPTIONS", "/api/apps", nil)
	req.Header.Set("Origin", "https://custom-deploy.railway.app")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for OPTIONS preflight, got: %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "https://custom-deploy.railway.app" {
		t.Errorf("Expected Access-Control-Allow-Origin to match request origin, got: %s", origin)
	}
	if creds := rec.Header().Get("Access-Control-Allow-Credentials"); creds != "true" {
		t.Errorf("Expected Access-Control-Allow-Credentials: true, got: %s", creds)
	}
}

func TestCloudPoolNativeRoutes(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudpool_metadata.db")
	storageDB, err := cloudpoolStorage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test storage DB: %v", err)
	}
	defer storageDB.Close()

	gdManager := cloudpoolGDrive.NewManager(storageDB)
	vfsEngine := cloudpoolVFS.NewVFS(storageDB, gdManager)

	server := cloudpoolApi.InitHandlers(storageDB, gdManager, vfsEngine, tempDir, tempDir)
	if server == nil {
		t.Fatal("Expected server instance, got nil")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/files", cloudpoolApi.FilesHandler)
	mux.HandleFunc("/api/files/", cloudpoolApi.FilesHandler)
	mux.HandleFunc("/api/accounts", cloudpoolApi.AccountsHandler)
	mux.HandleFunc("/api/accounts/", cloudpoolApi.AccountsHandler)
	mux.HandleFunc("/api/stats", cloudpoolApi.StatsHandler)
	mux.HandleFunc("/api/stats/", cloudpoolApi.StatsHandler)
	mux.HandleFunc("/api/shares", cloudpoolApi.SharesHandler)
	mux.HandleFunc("/api/shares/", cloudpoolApi.SharesHandler)
	mux.HandleFunc("/api/sql", cloudpoolApi.SQLStudioHandler)
	mux.HandleFunc("/api/sql/", cloudpoolApi.SQLStudioHandler)
	mux.HandleFunc("/api/settings", cloudpoolApi.SettingsHandler)
	mux.HandleFunc("/api/settings/", cloudpoolApi.SettingsHandler)
	mux.HandleFunc("/api/remote", cloudpoolApi.RemoteHandler)
	mux.HandleFunc("/api/remote/", cloudpoolApi.RemoteHandler)
	mux.HandleFunc("/api/tunnel", cloudpoolApi.TunnelHandler)
	mux.HandleFunc("/api/tunnel/", cloudpoolApi.TunnelHandler)
	mux.HandleFunc("/api/admin/drive", cloudpoolApi.AdminDriveHandler)
	mux.HandleFunc("/api/admin/drive/", cloudpoolApi.AdminDriveHandler)
	mux.HandleFunc("/api/admin/otp", cloudpoolApi.AdminOTPHandler)
	mux.HandleFunc("/api/admin/otp/", cloudpoolApi.AdminOTPHandler)
	mux.HandleFunc("/api/admin/update", cloudpoolApi.UpdateHandler)
	mux.HandleFunc("/api/admin/update/", cloudpoolApi.UpdateHandler)
	mux.HandleFunc("/api/public/share/", cloudpoolApi.PublicShareHandler)
	mux.Handle("/webdav", cloudpoolApi.WebDAVHandler)
	mux.Handle("/webdav/", cloudpoolApi.WebDAVHandler)

	endpoints := []string{
		"/api/files",
		"/api/accounts",
		"/api/stats",
		"/api/shares",
		"/api/sql/tables",
		"/api/settings",
		"/api/remote/info",
		"/api/tunnel/status",
		"/api/admin/drive/files",
		"/api/admin/otp/list",
		"/api/admin/update/info",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Tuyệt đối không được trả về 404 (chưa đăng ký) hoặc 502 (Bad Gateway của Proxy cũ)
		if rec.Code == http.StatusNotFound {
			t.Errorf("Endpoint %s returned 404 Not Found", ep)
		}
		if rec.Code == http.StatusBadGateway {
			t.Errorf("Endpoint %s returned 502 Bad Gateway (Proxy still active!)", ep)
		}
	}
}

func TestAutoHTTPSConfig_DisabledByDefault(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AUTO_HTTPS", "")
	t.Setenv("DOMAIN", "")

	cfg := getAutoHTTPSConfig(tempDir)
	if cfg.Enabled {
		t.Errorf("Expected Auto-HTTPS to be disabled by default, got enabled")
	}
}

func TestAutoHTTPSConfig_EnabledWithoutDomainFallback(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AUTO_HTTPS", "true")
	t.Setenv("DOMAIN", "")
	t.Setenv("DOMAINS", "")

	cfg := getAutoHTTPSConfig(tempDir)
	if cfg.Enabled {
		t.Errorf("Expected Auto-HTTPS to fallback to disabled when no DOMAIN provided, got enabled")
	}
}

func TestAutoHTTPSConfig_EnabledWithDomainAndCustomPorts(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AUTO_HTTPS", "true")
	t.Setenv("DOMAIN", "supportflastdev.io.vn, api.supportflastdev.io.vn")
	t.Setenv("ACME_EMAIL", "admin@supportflastdev.io.vn")
	t.Setenv("HTTP_PORT", "80")
	t.Setenv("HTTPS_PORT", "443")
	t.Setenv("CERT_DIR", "")

	cfg := getAutoHTTPSConfig(tempDir)
	if !cfg.Enabled {
		t.Fatalf("Expected Auto-HTTPS to be enabled, got disabled")
	}
	if len(cfg.Domains) != 2 {
		t.Fatalf("Expected 2 domains, got: %d (%v)", len(cfg.Domains), cfg.Domains)
	}
	if cfg.Domains[0] != "supportflastdev.io.vn" || cfg.Domains[1] != "api.supportflastdev.io.vn" {
		t.Errorf("Domains mismatch: %v", cfg.Domains)
	}
	if cfg.Email != "admin@supportflastdev.io.vn" {
		t.Errorf("Expected email admin@supportflastdev.io.vn, got: %s", cfg.Email)
	}
	if cfg.HTTPPort != "80" || cfg.HTTPSPort != "443" {
		t.Errorf("Ports mismatch: HTTP=%s, HTTPS=%s", cfg.HTTPPort, cfg.HTTPSPort)
	}
	expectedCertDir := filepath.Join(tempDir, "certs")
	if cfg.CertDir != expectedCertDir {
		t.Errorf("Expected certDir '%s', got '%s'", expectedCertDir, cfg.CertDir)
	}
}

func TestSetupAutoCertManager_ValidationAndHostPolicy(t *testing.T) {
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "test_certs")

	cfg := AutoHTTPSConfig{
		Enabled:   true,
		Domains:   []string{"supportflastdev.io.vn", "api.supportflastdev.io.vn"},
		Email:     "secops@supportflastdev.io.vn",
		CertDir:   certDir,
		HTTPPort:  "80",
		HTTPSPort: "443",
	}

	mgr, err := setupAutoCertManager(cfg)
	if err != nil {
		t.Fatalf("setupAutoCertManager failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("Expected autocert.Manager instance, got nil")
	}

	// 1. Kiểm tra HostPolicy: domain trong whitelist phải được chấp nhận
	ctx := context.Background()
	if err := mgr.HostPolicy(ctx, "supportflastdev.io.vn"); err != nil {
		t.Errorf("HostPolicy should allow supportflastdev.io.vn, got error: %v", err)
	}
	if err := mgr.HostPolicy(ctx, "api.supportflastdev.io.vn"); err != nil {
		t.Errorf("HostPolicy should allow api.supportflastdev.io.vn, got error: %v", err)
	}

	// 2. Domain lạ ngoài whitelist phải bị từ chối ngay lập tức để chống giả mạo chứng chỉ
	if err := mgr.HostPolicy(ctx, "attacker.com"); err == nil {
		t.Errorf("HostPolicy should REJECT attacker.com, but got nil error")
	}

	// 3. Kiểm tra TLSConfig được cấu hình GetCertificate tự động
	tlsCfg := mgr.TLSConfig()
	if tlsCfg == nil {
		t.Fatal("Expected TLSConfig to be non-nil")
	}
	if tlsCfg.GetCertificate == nil {
		t.Errorf("Expected TLSConfig.GetCertificate to be set by autocert")
	}
}

func TestSetupAutoCertManager_EmptyDomainsFails(t *testing.T) {
	tempDir := t.TempDir()
	cfg := AutoHTTPSConfig{
		Enabled: true,
		Domains: []string{},
		CertDir: tempDir,
	}

	_, err := setupAutoCertManager(cfg)
	if err == nil {
		t.Errorf("Expected error when domains list is empty, got nil")
	}
}

func TestAutoHTTPS_HTTPRedirectHandler(t *testing.T) {
	tempDir := t.TempDir()
	cfg := AutoHTTPSConfig{
		Enabled:   true,
		Domains:   []string{"supportflastdev.io.vn"},
		CertDir:   filepath.Join(tempDir, "certs"),
		HTTPPort:  "80",
		HTTPSPort: "443",
	}

	mgr, err := setupAutoCertManager(cfg)
	if err != nil {
		t.Fatalf("Failed to setup autocert manager: %v", err)
	}

	redirectServer := createACMERedirectServer("0.0.0.0:80", mgr)
	if redirectServer == nil {
		t.Fatal("Expected redirectServer to be non-nil")
	}

	// Giả lập yêu cầu HTTP thông thường tới domain để kiểm tra chuyển hướng tự động sang HTTPS
	req := httptest.NewRequest("GET", "http://supportflastdev.io.vn/api/health", nil)
	req.Host = "supportflastdev.io.vn"
	rec := httptest.NewRecorder()

	redirectServer.Handler.ServeHTTP(rec, req)

	// autocert.HTTPHandler(nil) chuyển hướng non-ACME sang HTTPS bằng mã 302 hoặc 301
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusFound {
		t.Errorf("Expected redirect 301 or 302, got HTTP %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://supportflastdev.io.vn/api/health") {
		t.Errorf("Expected redirect Location to start with https://supportflastdev.io.vn/api/health, got: %s", loc)
	}
}

func TestCreateServerHelpers(t *testing.T) {
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	// Standard Server
	stdServer := createStandardHTTPServer("0.0.0.0:8080", dummy)
	if stdServer.Addr != "0.0.0.0:8080" {
		t.Errorf("Expected stdServer.Addr 0.0.0.0:8080, got %s", stdServer.Addr)
	}

	// Standalone HTTPS Server
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	httpsServer := createStandaloneHTTPSServer("0.0.0.0:443", dummy, tlsCfg)
	if httpsServer.Addr != "0.0.0.0:443" {
		t.Errorf("Expected httpsServer.Addr 0.0.0.0:443, got %s", httpsServer.Addr)
	}
	if httpsServer.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("Expected TLS MinVersion TLS 1.2, got: %x", httpsServer.TLSConfig.MinVersion)
	}
}

func Test10Subagents_OnDeviceDispatcherAndConcurrencyPool(t *testing.T) {
	// 1. Kiểm tra danh sách fallbackAgents đủ 10 subagents
	if len(fallbackAgents) != 10 {
		t.Fatalf("Kỳ vọng 10 subagents trong Go Engine, nhận được: %d", len(fallbackAgents))
	}

	expectedIDs := []string{
		"agent_triage", "agent_tech", "agent_infra", "agent_security", "agent_billing",
		"agent_mobile", "agent_network", "agent_storage", "agent_analytics", "agent_automation",
	}

	foundMap := make(map[string]bool)
	for _, a := range fallbackAgents {
		foundMap[a.ID] = true
	}
	for _, eid := range expectedIDs {
		if !foundMap[eid] {
			t.Errorf("Thiếu Subagent ID: %s", eid)
		}
	}

	// 2. Kiểm thử trực tiếp hàm On-Device Local AI Dispatcher cho cả 10 Subagents
	for _, eid := range expectedIDs {
		res := dispatchInternalMobileSubagent(ChatPayload{
			Query:   "Kiểm tra hiệu năng máy chủ di động",
			AgentID: eid,
		})

		if res["status"] != "success" {
			t.Errorf("Subagent %s trả về trạng thái thất bại: %v", eid, res["status"])
		}
		if res["agent_id"] != eid {
			t.Errorf("Kỳ vọng agent_id=%s, nhận được=%v", eid, res["agent_id"])
		}
		if execMs, ok := res["execution_time_ms"].(float64); !ok || execMs > 50.0 {
			t.Errorf("Subagent %s thực thi quá chậm (> 50ms): %v", eid, res["execution_time_ms"])
		}
	}

	// 3. Kiểm thử Semaphore Concurrency Worker Pool với đúng 10 Goroutines đồng thời
	doneChan := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			subagentSemaphore <- struct{}{}
			defer func() { <-subagentSemaphore }()
			res := dispatchInternalMobileSubagent(ChatPayload{
				Query:   "Test concurrent mobile server execution",
				AgentID: expectedIDs[idx],
			})
			if res["status"] == "success" {
				doneChan <- true
			} else {
				doneChan <- false
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		ok := <-doneChan
		if !ok {
			t.Errorf("Tác vụ subagent đồng thời thứ %d thất bại", i)
		}
	}
}


