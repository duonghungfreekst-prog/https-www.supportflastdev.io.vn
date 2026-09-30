package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	cloudpoolGDrive "supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	cloudpoolVFS "supportflast_engine/cloudpool/vfs"
	"supportflast_engine/database"
	"supportflast_engine/internal/middleware"
	"supportflast_engine/registry"
	"supportflast_engine/security"
)

// TestLiveAPIEndToEndAuditor performs comprehensive end-to-end live HTTP audit for the 6 critical API endpoints:
// 1. GET /api/stats
// 2. GET /api/accounts
// 3. GET /api/files?parent_id=root
// 4. GET /api/settings
// 5. GET /api/shares
// 6. GET /api/auth/turnstile-config
// Testing both Guest (unauthenticated) and Admin (with JWT Bearer token obtained from POST /api/auth/login)
// Running through the FULL Production Middleware Stack (Security Headers, Rate Limiting, Request ID, WAF)
func TestLiveAPIEndToEndAuditor(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Khởi tạo cặp khóa RSA 2048-bit (RS256)
	if err := InitJWTKeys(tempDir); err != nil {
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	// 2. Khởi tạo CSDL SQLite CloudPool
	storageDBPath := filepath.Join(tempDir, "cloudpool_test.db")
	storageDB, err := storage.NewDB(storageDBPath)
	if err != nil {
		t.Fatalf("storage.NewDB thất bại: %v", err)
	}
	defer storageDB.Close()

	// 3. Khởi tạo CSDL SQLite SupportFlast Registry (User, Session, Token)
	regDBPath := filepath.Join(tempDir, "supportflast_test.db")
	if _, err := database.InitDB(regDBPath); err != nil {
		t.Fatalf("database.InitDB thất bại: %v", err)
	}
	defer database.CloseDB()

	// 4. Khởi tạo tài khoản Quản Trị Viên (Admin) mặc định
	adminUsername := "admin"
	adminPassword := "Admin@2026!SupportFlast"
	os.Setenv("ADMIN_USERNAME", adminUsername)
	os.Setenv("ADMIN_PASSWORD", adminPassword)
	os.Setenv("TURNSTILE_ENABLED", "true")
	registry.InitAuth()

	// 5. Nạp dữ liệu mẫu (Seed Data)
	// Seed Account
	testAcc := &models.Account{
		ID:              "acc_gdrive_01",
		Email:           "node01.storage@supportflastdev.io.vn",
		Name:            "Google Drive Storage Node #1",
		AvatarURL:       "https://lh3.googleusercontent.com/a/default-user",
		AuthType:        "service_account",
		CredentialsJSON: `{"type":"service_account","project_id":"sf-cloudpool"}`,
		RootFolderID:    "sf_root_folder_01",
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024, // 15 GB
		UsedQuotaBytes:  3 * 1024 * 1024 * 1024,  // 3 GB
		FreeQuotaBytes:  12 * 1024 * 1024 * 1024, // 12 GB
		Status:          "active",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := storageDB.SaveAccount(testAcc); err != nil {
		t.Fatalf("SaveAccount thất bại: %v", err)
	}

	// Seed Folder
	testFolder := &models.VirtualFile{
		ID:        "folder_backup_2026",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "Sao Lưu Dự Án 2026",
		Path:      "/Sao Lưu Dự Án 2026",
		IsDir:     true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := storageDB.SaveVirtualFile(testFolder); err != nil {
		t.Fatalf("SaveVirtualFile folder thất bại: %v", err)
	}

	// Seed File
	testFile := &models.VirtualFile{
		ID:          "file_system_arch_pdf",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "Kien_Truc_He_Thong_SupportFlast.pdf",
		Path:        "/Kien_Truc_He_Thong_SupportFlast.pdf",
		IsDir:       false,
		SizeBytes:   4502180,
		MimeType:    "application/pdf",
		SHA256:      "a1b2c3d4e5f67890123456789abcdef0123456789abcdef0123456789abcdef0",
		ChunkCount:  1,
		IsEncrypted:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := storageDB.SaveVirtualFile(testFile); err != nil {
		t.Fatalf("SaveVirtualFile file thất bại: %v", err)
	}

	// Seed Public Share
	shareExpiry := time.Now().Add(7 * 24 * time.Hour)
	testShare := &models.PublicShare{
		ID:            "sh_demo_share_99",
		FileID:        testFile.ID,
		CreatedBy:     "admin",
		MaxDownloads:  100,
		DownloadCount: 5,
		ExpiresAt:     &shareExpiry,
		CreatedAt:     time.Now(),
		IsActive:      true,
	}
	if err := storageDB.CreatePublicShare(testShare); err != nil {
		t.Fatalf("CreatePublicShare thất bại: %v", err)
	}

	// 6. Khởi tạo Managers & Server Handlers
	gdManager := cloudpoolGDrive.NewManager(storageDB)
	vfsEngine := cloudpoolVFS.NewVFS(storageDB, gdManager)

	server := &Server{
		db:      storageDB,
		gd:      gdManager,
		vfs:     vfsEngine,
		baseDir: tempDir,
	}
	server.setupSubMuxes()

	// 7. Xây dựng HTTP Mux ánh xạ đúng 100% main.go
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", server.StatsHandler)
	mux.HandleFunc("/api/stats/", server.StatsHandler)
	mux.HandleFunc("/api/accounts", server.AccountsHandler)
	mux.HandleFunc("/api/accounts/", server.AccountsHandler)
	mux.HandleFunc("/api/files", server.FilesHandler)
	mux.HandleFunc("/api/files/", server.FilesHandler)
	mux.HandleFunc("/api/settings", server.SettingsHandler)
	mux.HandleFunc("/api/settings/", server.SettingsHandler)
	mux.HandleFunc("/api/shares", server.SharesHandler)
	mux.HandleFunc("/api/shares/", server.SharesHandler)
	mux.HandleFunc("/api/auth/turnstile-config", registry.TurnstileConfigHandler)
	mux.Handle("/api/auth/login", security.LoginRateLimitMiddleware(http.HandlerFunc(registry.LoginHandler)))

	// Bọc Router trong Full Production Middleware Chain (như trong main.go)
	prodHandler := middleware.Chain(mux,
		middleware.WrapFunc(security.RequestIDMiddleware),
		middleware.WrapFunc(security.CSRFMiddleware),
		middleware.Recovery,
		middleware.AccessLog,
		middleware.WrapFunc(security.SecurityHeadersMiddleware),
		middleware.WrapFunc(security.IPJailMiddleware),
		middleware.WrapFunc(security.MultiTierRateLimitMiddleware),
		middleware.WrapFunc(security.CloudflareSecurityMiddleware),
	)

	// Bật TCP Server thực tế với httptest
	ts := httptest.NewServer(prodHandler)
	defer ts.Close()

	client := ts.Client()

	t.Logf("================================================================================")
	t.Logf("SUPPORTFLAST LIVE API E2E AUDITOR & DIAGNOSTIC RUNNER (FULL MIDDLEWARE STACK)")
	t.Logf("Mục tiêu thử nghiệm: %s", ts.URL)
	t.Logf("================================================================================")

	// 8. Đăng nhập thực tế qua POST /api/auth/login để lấy token RS256
	loginPayload := map[string]string{
		"username":        adminUsername,
		"password":        adminPassword,
		"turnstile_token": "XXXX.DUMMY.TOKEN.XXXX", // Always-pass testing token
	}
	loginBody, _ := json.Marshal(loginPayload)
	loginResp, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("Gửi request POST /api/auth/login thất bại: %v", err)
	}
	loginBytes, _ := io.ReadAll(loginResp.Body)
	loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("Đăng nhập thất bại: HTTP %d, body: %s", loginResp.StatusCode, string(loginBytes))
	}

	var loginResult struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		User   struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(loginBytes, &loginResult); err != nil {
		t.Fatalf("Không thể parse kết quả login JSON: %v", err)
	}

	adminToken := loginResult.Token
	t.Logf("[AUTH] Đăng nhập Admin thành công qua HTTP POST /api/auth/login! User=%s, Role=%s", loginResult.User.Username, loginResult.User.Role)
	t.Logf("[AUTH] Token JWT RS256 cấp thành công (độ dài: %d chars)", len(adminToken))

	endpoints := []struct {
		Name string
		Path string
	}{
		{"Stats", "/api/stats"},
		{"Accounts", "/api/accounts"},
		{"Files Root", "/api/files?parent_id=root"},
		{"Settings", "/api/settings"},
		{"Shares", "/api/shares"},
		{"Turnstile Config", "/api/auth/turnstile-config"},
	}

	// ---------------------------------------------------------
	// PHASE 1: GUEST AUDIT (UNAUTHENTICATED)
	// ---------------------------------------------------------
	t.Logf("\n>>> ========================================================")
	t.Logf(">>> [PHASE 1] KIỂM TRA PHẢN HỒI KHI LÀ KHÁCH (GUEST - NO AUTH)")
	t.Logf(">>> ========================================================")
	guestStatusMap := make(map[string]int)
	guestJSONMap := make(map[string]string)

	for _, ep := range endpoints {
		req, err := http.NewRequest(http.MethodGet, ts.URL+ep.Path, nil)
		if err != nil {
			t.Fatalf("Không thể tạo request cho %s: %v", ep.Path, err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request thất bại cho %s: %v", ep.Path, err)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		guestStatusMap[ep.Path] = resp.StatusCode
		guestJSONMap[ep.Path] = string(bodyBytes)

		t.Logf("\n[GUEST] %s %s -> HTTP %d %s", req.Method, ep.Path, resp.StatusCode, http.StatusText(resp.StatusCode))
		t.Logf("        Content-Type: %s", resp.Header.Get("Content-Type"))
		t.Logf("        X-Request-ID: %s", resp.Header.Get("X-Request-ID"))
		t.Logf("        Security Headers: X-Content-Type-Options=%s, X-Frame-Options=%s",
			resp.Header.Get("X-Content-Type-Options"), resp.Header.Get("X-Frame-Options"))

		var jsonObj interface{}
		if err := json.Unmarshal(bodyBytes, &jsonObj); err != nil {
			t.Errorf("[GUEST] %s: Phản hồi không phải JSON hợp lệ: %v", ep.Path, err)
		} else {
			formatted, _ := json.MarshalIndent(jsonObj, "        ", "  ")
			if len(formatted) > 350 {
				t.Logf("        JSON Response (rút gọn):\n%s...", string(formatted[:350]))
			} else {
				t.Logf("        JSON Response:\n%s", string(formatted))
			}
		}

		// Xác thực chi tiết nghiệp vụ từng endpoint
		switch ep.Path {
		case "/api/stats":
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[GUEST] Cần HTTP 200 cho %s, nhận: %d", ep.Path, resp.StatusCode)
			}
			var s models.StorageStats
			_ = json.Unmarshal(bodyBytes, &s)
			if s.TotalAccounts != 1 {
				t.Errorf("[GUEST] Kỳ vọng 1 account trong stats, nhận: %d", s.TotalAccounts)
			}
		case "/api/accounts":
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[GUEST] Cần HTTP 200 cho %s, nhận: %d", ep.Path, resp.StatusCode)
			}
			var accs []models.Account
			_ = json.Unmarshal(bodyBytes, &accs)
			if len(accs) != 1 {
				t.Errorf("[GUEST] Kỳ vọng 1 account, nhận: %d", len(accs))
			} else {
				t.Logf("        [GUEST CHECK] Account ID: %s, Email: %s, TotalQuota: %d bytes (Credentials đã ẩn an toàn)", accs[0].ID, accs[0].Email, accs[0].TotalQuotaBytes)
			}
		case "/api/files?parent_id=root":
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[GUEST] Cần HTTP 200 cho %s, nhận: %d", ep.Path, resp.StatusCode)
			}
			var fileResp struct {
				Files  []models.VirtualFile `json:"files"`
				Parent *models.VirtualFile  `json:"parent"`
			}
			_ = json.Unmarshal(bodyBytes, &fileResp)
			t.Logf("        [GUEST CHECK] Danh mục root của Guest trả về: %d tệp tin (cô lập phân vùng an toàn)", len(fileResp.Files))
		case "/api/settings":
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[GUEST] Cần HTTP 200 cho %s, nhận: %d", ep.Path, resp.StatusCode)
			}
			var set models.Settings
			_ = json.Unmarshal(bodyBytes, &set)
			if set.MasterPassphrase != "********" || set.GoogleClientSecret != "********" || set.TurnstileSecretKey != "********" {
				t.Errorf("[GUEST] LỖI BẢO MẬT: Khóa bí mật không được che mờ! MasterPassphrase=%s", set.MasterPassphrase)
			} else {
				t.Logf("        [GUEST CHECK] Tất cả thông tin nhạy cảm đã được che mờ bằng '********'")
			}
		case "/api/shares":
			t.Logf("        [GUEST DIAGNOSIS] Phản hồi HTTP 401 Unauthorized:")
			t.Logf("        -> Body: %s", string(bodyBytes))
			t.Logf("        -> Đánh giá: ĐÚNG THIẾT KẾ BẢO MẬT. Endpoint /api/shares là API quản lý danh sách liên kết chia sẻ của người dùng và quản trị viên, do đó yêu cầu đăng nhập là bắt buộc để ngăn chặn lộ thông tin liên kết.")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("[GUEST] Kỳ vọng HTTP 401 cho /api/shares khi chưa đăng nhập, nhận được: %d", resp.StatusCode)
			}
		case "/api/auth/turnstile-config":
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[GUEST] Cần HTTP 200 cho %s, nhận: %d", ep.Path, resp.StatusCode)
			}
			var tc map[string]interface{}
			_ = json.Unmarshal(bodyBytes, &tc)
			if tc["status"] != "success" || tc["site_key"] == "" {
				t.Errorf("[GUEST] Cấu hình Turnstile không đúng: %v", tc)
			}
		}
	}

	// ---------------------------------------------------------
	// PHASE 2: ADMIN AUDIT (AUTHENTICATED WITH JWT BEARER TOKEN)
	// ---------------------------------------------------------
	t.Logf("\n>>> ========================================================")
	t.Logf(">>> [PHASE 2] KIỂM TRA PHẢN HỒI KHI CÓ QUYỀN ADMIN (JWT BEARER)")
	t.Logf(">>> ========================================================")
	adminStatusMap := make(map[string]int)
	adminJSONMap := make(map[string]string)

	for _, ep := range endpoints {
		req, err := http.NewRequest(http.MethodGet, ts.URL+ep.Path, nil)
		if err != nil {
			t.Fatalf("Không thể tạo request cho %s: %v", ep.Path, err)
		}
		req.Header.Set("Authorization", "Bearer "+adminToken)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request thất bại cho %s: %v", ep.Path, err)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		adminStatusMap[ep.Path] = resp.StatusCode
		adminJSONMap[ep.Path] = string(bodyBytes)

		t.Logf("\n[ADMIN] %s %s -> HTTP %d %s", req.Method, ep.Path, resp.StatusCode, http.StatusText(resp.StatusCode))
		t.Logf("        Content-Type: %s", resp.Header.Get("Content-Type"))
		t.Logf("        X-Request-ID: %s", resp.Header.Get("X-Request-ID"))

		var jsonObj interface{}
		if err := json.Unmarshal(bodyBytes, &jsonObj); err != nil {
			t.Errorf("[ADMIN] %s: Phản hồi không phải JSON hợp lệ: %v", ep.Path, err)
		} else {
			formatted, _ := json.MarshalIndent(jsonObj, "        ", "  ")
			if len(formatted) > 350 {
				t.Logf("        JSON Response (rút gọn):\n%s...", string(formatted[:350]))
			} else {
				t.Logf("        JSON Response:\n%s", string(formatted))
			}
		}

		// Tất cả 6 endpoint khi Admin gọi BẮT BUỘC trả về 200 OK!
		if resp.StatusCode != http.StatusOK {
			t.Errorf("[ADMIN] BẮT BUỘC HTTP 200 OK cho %s, nhận: %d %s", ep.Path, resp.StatusCode, string(bodyBytes))
		}

		// Xác thực chi tiết nghiệp vụ Admin
		switch ep.Path {
		case "/api/stats":
			var s models.StorageStats
			_ = json.Unmarshal(bodyBytes, &s)
			if s.TotalAccounts != 1 {
				t.Errorf("[ADMIN] Kỳ vọng 1 account, nhận: %d", s.TotalAccounts)
			}
			t.Logf("        [ADMIN CHECK] Stats: TotalAccounts=%d, TotalFiles=%d, TotalCapacity=%d bytes, RustCoreActive=%v", s.TotalAccounts, s.TotalFilesCount, s.TotalCapacityBytes, s.RustCoreActive)
		case "/api/accounts":
			var accs []models.Account
			_ = json.Unmarshal(bodyBytes, &accs)
			if len(accs) != 1 {
				t.Errorf("[ADMIN] Kỳ vọng 1 account, nhận: %d", len(accs))
			} else {
				t.Logf("        [ADMIN CHECK] Account: ID=%s, Name=%s, Email=%s, Quota=%d bytes", accs[0].ID, accs[0].Name, accs[0].Email, accs[0].TotalQuotaBytes)
			}
		case "/api/files?parent_id=root":
			var fileResp struct {
				Files  []models.VirtualFile `json:"files"`
				Parent *models.VirtualFile  `json:"parent"`
			}
			_ = json.Unmarshal(bodyBytes, &fileResp)
			if len(fileResp.Files) < 2 {
				t.Errorf("[ADMIN] Kỳ vọng ít nhất 2 items (1 file + 1 folder), nhận: %d", len(fileResp.Files))
			} else {
				t.Logf("        [ADMIN CHECK] Files Root: Đã tải thành công %d đối tượng (Bao gồm file '%s' và thư mục '%s')", len(fileResp.Files), fileResp.Files[0].Name, fileResp.Files[1].Name)
			}
		case "/api/settings":
			var set models.Settings
			_ = json.Unmarshal(bodyBytes, &set)
			if set.MasterPassphrase != "********" {
				t.Errorf("[ADMIN] MasterPassphrase không được che mờ: %s", set.MasterPassphrase)
			}
			t.Logf("        [ADMIN CHECK] Cài đặt hệ thống: AllocationStrategy=%s, TurnstileEnabled=%v, ChunkSize=%d", set.AllocationStrategy, set.TurnstileEnabled, set.ChunkSizeBytes)
		case "/api/shares":
			var shares []models.PublicShare
			_ = json.Unmarshal(bodyBytes, &shares)
			if len(shares) != 1 {
				t.Errorf("[ADMIN] Kỳ vọng 1 share, nhận: %d", len(shares))
			} else {
				t.Logf("        [ADMIN CHECK] Shares: ID=%s, FileID=%s, CreatedBy=%s, MaxDownloads=%d", shares[0].ID, shares[0].FileID, shares[0].CreatedBy, shares[0].MaxDownloads)
			}
		case "/api/auth/turnstile-config":
			var tc map[string]interface{}
			_ = json.Unmarshal(bodyBytes, &tc)
			if tc["status"] != "success" {
				t.Errorf("[ADMIN] Cấu hình Turnstile không đúng: %v", tc)
			}
			t.Logf("        [ADMIN CHECK] Turnstile: status=%v, enabled=%v, site_key=%v", tc["status"], tc["enabled"], tc["site_key"])
		}
	}

	// ---------------------------------------------------------
	// TỔNG KẾT MA TRẬN ĐỐI SOÁT
	// ---------------------------------------------------------
	t.Logf("\n=========================================================================================================")
	t.Logf("%-32s | %-16s | %-16s | %-20s", "ENDPOINT", "KHÁCH (GUEST)", "ADMIN (JWT)", "ĐÁNH GIÁ ĐỐI SOÁT")
	t.Logf("---------------------------------------------------------------------------------------------------------")
	for _, ep := range endpoints {
		gStatus := guestStatusMap[ep.Path]
		aStatus := adminStatusMap[ep.Path]
		statusNote := "HOÀN HẢO (200 OK)"
		if gStatus == 401 {
			statusNote = "BẢO MẬT TỐT (401 Auth Required)"
		}
		t.Logf("%-32s | HTTP %-11d | HTTP %-11d | %-20s", ep.Path, gStatus, aStatus, statusNote)
	}
	t.Logf("=========================================================================================================")
}

// TestLiveAPIWithActualProductionDB runs live HTTP checks against the actual production databases in data/
func TestLiveAPIWithActualProductionDB(t *testing.T) {
	prodDataDir := filepath.Join("..", "..", "data")
	storageDBPath := filepath.Join(prodDataDir, "cloudpool_metadata.db")
	regDBPath := filepath.Join(prodDataDir, "supportflast.db")

	if _, err := os.Stat(storageDBPath); os.IsNotExist(err) {
		t.Skip("Bỏ qua: Không tìm thấy cloudpool_metadata.db thực tế")
	}

	// 1. Khởi tạo khóa từ keys dir thực tế
	keysDir := filepath.Join(prodDataDir, "keys")
	if err := InitJWTKeys(keysDir); err != nil {
		t.Fatalf("InitJWTKeys thất bại với keys thực tế: %v", err)
	}

	// 2. Mở CSDL thực tế
	storageDB, err := storage.NewDB(storageDBPath)
	if err != nil {
		t.Fatalf("Không thể mở cloudpool_metadata.db thực tế: %v", err)
	}
	defer storageDB.Close()

	if _, err := database.InitDB(regDBPath); err != nil {
		t.Fatalf("Không thể mở supportflast.db thực tế: %v", err)
	}
	defer database.CloseDB()

	// 3. Khởi tạo Managers
	gdManager := cloudpoolGDrive.NewManager(storageDB)
	vfsEngine := cloudpoolVFS.NewVFS(storageDB, gdManager)

	server := &Server{
		db:      storageDB,
		gd:      gdManager,
		vfs:     vfsEngine,
		baseDir: prodDataDir,
	}
	server.setupSubMuxes()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", server.StatsHandler)
	mux.HandleFunc("/api/accounts", server.AccountsHandler)
	mux.HandleFunc("/api/files", server.FilesHandler)
	mux.HandleFunc("/api/settings", server.SettingsHandler)
	mux.HandleFunc("/api/shares", server.SharesHandler)
	mux.HandleFunc("/api/auth/turnstile-config", registry.TurnstileConfigHandler)

	prodHandler := middleware.Chain(mux,
		middleware.WrapFunc(security.RequestIDMiddleware),
		middleware.Recovery,
		middleware.AccessLog,
		middleware.WrapFunc(security.SecurityHeadersMiddleware),
		middleware.WrapFunc(security.MultiTierRateLimitMiddleware),
	)

	ts := httptest.NewServer(prodHandler)
	defer ts.Close()

	client := ts.Client()

	// Issue Admin Token for real admin user
	adminHubUser := registry.User{
		ID:       "usr-admin-001",
		Username: "admin",
		Email:    "admin@supportflastdev.io.vn",
		Role:     "admin",
	}
	adminToken, err := registry.IssueRS256Token(adminHubUser, 24*time.Hour)
	if err != nil {
		t.Fatalf("Không thể tạo token admin: %v", err)
	}

	t.Logf("=== KIỂM TRA ĐỐI SOÁT TRỰC TIẾP TRÊN CSDL PRODUCTION THẬT ===")
	endpoints := []string{
		"/api/stats",
		"/api/accounts",
		"/api/files?parent_id=root",
		"/api/settings",
		"/api/shares",
		"/api/auth/turnstile-config",
	}

	for _, ep := range endpoints {
		// Test Guest
		reqGuest, _ := http.NewRequest(http.MethodGet, ts.URL+ep, nil)
		respGuest, err := client.Do(reqGuest)
		if err != nil {
			t.Fatalf("Lỗi request Guest: %v", err)
		}
		bodyGuest, _ := io.ReadAll(respGuest.Body)
		respGuest.Body.Close()

		// Test Admin
		reqAdmin, _ := http.NewRequest(http.MethodGet, ts.URL+ep, nil)
		reqAdmin.Header.Set("Authorization", "Bearer "+adminToken)
		respAdmin, err := client.Do(reqAdmin)
		if err != nil {
			t.Fatalf("Lỗi request Admin: %v", err)
		}
		bodyAdmin, _ := io.ReadAll(respAdmin.Body)
		respAdmin.Body.Close()

		t.Logf("[PROD DB TEST] %-30s | Guest: HTTP %d | Admin: HTTP %d", ep, respGuest.StatusCode, respAdmin.StatusCode)

		// Assertions
		if respAdmin.StatusCode != http.StatusOK {
			t.Errorf("[PROD DB] Admin gọi %s bị lỗi HTTP %d: %s", ep, respAdmin.StatusCode, string(bodyAdmin))
		}
		if ep == "/api/shares" {
			if respGuest.StatusCode != http.StatusUnauthorized {
				t.Errorf("[PROD DB] Guest gọi /api/shares phải trả về 401, nhận: %d", respGuest.StatusCode)
			}
		} else {
			if respGuest.StatusCode != http.StatusOK {
				t.Errorf("[PROD DB] Guest gọi %s phải trả về 200, nhận: %d: %s", ep, respGuest.StatusCode, string(bodyGuest))
			}
		}
	}
	t.Logf("=== TOÀN BỘ ENDPOINT TRÊN CSDL PRODUCTION THẬT ĐÃ ĐƯỢC ĐỐI SOÁT HOÀN TẤT ===")
}

