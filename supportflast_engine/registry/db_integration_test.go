package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cache"
	"supportflast_engine/database"
)

// setupIntegrationTestDB thiết lập cơ sở dữ liệu SQLite cô lập cho bài test
func setupIntegrationTestDB(t *testing.T) func() {
	testDir := filepath.Join(os.TempDir(), fmt.Sprintf("sf_test_db_%d", time.Now().UnixNano()))
	os.MkdirAll(testDir, 0755)
	dbPath := filepath.Join(testDir, "test.db")

	database.CloseDB()
	db, err := database.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	_, _ = db.Exec("DELETE FROM apps; DELETE FROM system_releases; DELETE FROM reviews;")

	return func() {
		database.CloseDB()
		os.RemoveAll(testDir)
	}
}

// TestApps_SQLiteIntegration kiểm tra toàn diện tích hợp bảng apps của SQLite
func TestApps_SQLiteIntegration(t *testing.T) {
	cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	cache.DefaultCache.Clear()

	// 1. Kiểm tra ban đầu: LoadApps() phải rỗng (không seed app rác theo Rule 9.1)
	apps := LoadApps()
	if len(apps) != 0 {
		t.Fatalf("Kỳ vọng danh sách app rỗng ban đầu, nhận được: %d", len(apps))
	}

	// 2. Kiểm tra SaveApp() với Prepared Statement
	testApp := AppItem{
		ID:            "APP-TEST-001",
		Name:          "SupportFlast Studio",
		Version:       "1.0.0",
		Platform:      "Windows",
		Category:      "Developer Tools",
		Desc:          "Công cụ lập trình tối ưu",
		FileName:      "supportflast_studio.zip",
		SizeBytes:     1024 * 1024 * 10,
		SizeFormatted: "10.0 MB",
		SHA256:        "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Author:        "Quốc Việt",
		Downloads:     5,
		Status:        "published",
		PublishedAt:   time.Now().Format(time.RFC3339),
		DownloadURL:   "/api/apps/download/APP-TEST-001",
	}

	if err := SaveApp(testApp); err != nil {
		t.Fatalf("SaveApp failed: %v", err)
	}

	// 3. Kiểm tra LoadApps() đọc lại chính xác từ SQLite
	appsAfter := LoadApps()
	if len(appsAfter) != 1 {
		t.Fatalf("Kỳ vọng có 1 app sau khi lưu, nhận được: %d", len(appsAfter))
	}
	if appsAfter[0].ID != testApp.ID || appsAfter[0].Name != testApp.Name {
		t.Fatalf("Dữ liệu app không khớp: %+v", appsAfter[0])
	}

	// 4. Kiểm tra ListHandler() qua HTTP
	req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	rec := httptest.NewRecorder()
	ListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ListHandler kỳ vọng status 200, nhận: %d", rec.Code)
	}

	var listResp struct {
		Status    string    `json:"status"`
		TotalApps int       `json:"total_apps"`
		Apps      []AppItem `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal list response failed: %v", err)
	}
	if listResp.TotalApps != 1 || len(listResp.Apps) != 1 {
		t.Fatalf("TotalApps kỳ vọng 1, nhận: %d", listResp.TotalApps)
	}

	// 5. Kiểm tra DownloadHandler() tăng lượt tải trực tiếp trong SQLite
	downReq := httptest.NewRequest(http.MethodGet, "/api/apps/download/APP-TEST-001", nil)
	downRec := httptest.NewRecorder()
	DownloadHandler(downRec, downReq)

	if downRec.Code != http.StatusOK {
		t.Fatalf("DownloadHandler kỳ vọng status 200, nhận: %d", downRec.Code)
	}

	appsAfterDown := LoadApps()
	if len(appsAfterDown) != 1 || appsAfterDown[0].Downloads != 6 {
		t.Fatalf("Downloads kỳ vọng tăng lên 6, nhận: %d", appsAfterDown[0].Downloads)
	}

	// 6. Kiểm tra PublishHandler() lưu app mới vào SQLite
	pubPayload := map[string]string{
		"name":     "SupportFlast Terminal",
		"version":  "2.0.0",
		"platform": "Linux",
		"category": "Terminal",
		"desc":     "Terminal emulator",
		"author":   "Admin",
	}
	pubBytes, _ := json.Marshal(pubPayload)
	pubReq := httptest.NewRequest(http.MethodPost, "/api/apps/publish", bytes.NewBuffer(pubBytes))
	pubReq.Header.Set("Content-Type", "application/json")
	// Tạo API key hợp lệ cho publish request theo quy chế bảo mật mới
	testKey, keyErr := GenerateNewKey("Test Publish Key", 1)
	if keyErr != nil {
		t.Fatalf("Không thể tạo API Key cho test: %v", keyErr)
	}
	pubReq.Header.Set("X-API-Key", testKey.Key)
	pubRec := httptest.NewRecorder()
	PublishHandler(pubRec, pubReq)

	if pubRec.Code != http.StatusCreated {
		t.Fatalf("PublishHandler kỳ vọng status 201, nhận: %d (body: %s)", pubRec.Code, pubRec.Body.String())
	}

	appsFinal := LoadApps()
	if len(appsFinal) != 2 {
		t.Fatalf("Kỳ vọng có 2 app sau Publish, nhận: %d", len(appsFinal))
	}
}

// TestAuth_SQLiteIntegration kiểm tra đăng ký, đăng nhập và /api/auth/me với SQLite
func TestAuth_SQLiteIntegration(t *testing.T) {
	cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	InitAuth()

	// 1. Kiểm tra tài khoản admin mặc định đã có trong bảng users
	admin, err := GetUserByUsernameOrEmail("admin")
	if err != nil || admin == nil {
		t.Fatalf("Admin user không tồn tại trong SQLite: %v", err)
	}
	if admin.Role != "admin" {
		t.Fatalf("Admin role kỳ vọng 'admin', nhận: %s", admin.Role)
	}

	// 2. Kiểm tra đăng ký tài khoản mới qua RegisterHandler
	regPayload := map[string]string{
		"username":     "new_developer",
		"email":        "developer@supportflastdev.io.vn",
		"password":     "SecretPass2026!",
		"display_name": "New Developer",
	}
	regBytes, _ := json.Marshal(regPayload)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBuffer(regBytes))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	RegisterHandler(regRec, regReq)

	if regRec.Code != http.StatusCreated {
		t.Fatalf("RegisterHandler kỳ vọng status 201, nhận: %d (body: %s)", regRec.Code, regRec.Body.String())
	}

	var regResp struct {
		Status string   `json:"status"`
		Token  string   `json:"token"`
		User   SafeUser `json:"user"`
	}
	if err := json.Unmarshal(regRec.Body.Bytes(), &regResp); err != nil {
		t.Fatalf("Unmarshal register response error: %v", err)
	}
	if regResp.User.Username != "new_developer" {
		t.Fatalf("Kỳ vọng username là new_developer, nhận: %s", regResp.User.Username)
	}
	if regResp.Token == "" {
		t.Fatalf("Kỳ vọng token không rỗng")
	}

	// 3. Kiểm tra trùng lặp đăng ký lại cùng username
	regRec2 := httptest.NewRecorder()
	regReq2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBuffer(regBytes))
	regReq2.Header.Set("Content-Type", "application/json")
	RegisterHandler(regRec2, regReq2)
	if regRec2.Code != http.StatusConflict {
		t.Fatalf("Kỳ vọng 409 Conflict khi trùng username, nhận: %d", regRec2.Code)
	}

	// 4. Kiểm tra đăng nhập với LoginHandler
	loginPayload := map[string]string{
		"username_or_email": "developer@supportflastdev.io.vn",
		"password":          "SecretPass2026!",
	}
	loginBytes, _ := json.Marshal(loginPayload)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBuffer(loginBytes))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.RemoteAddr = "127.0.0.1:12345"
	loginRec := httptest.NewRecorder()
	LoginHandler(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("LoginHandler kỳ vọng status 200, nhận: %d (body: %s)", loginRec.Code, loginRec.Body.String())
	}

	var loginResp struct {
		Status string   `json:"status"`
		Token  string   `json:"token"`
		User   SafeUser `json:"user"`
	}
	json.Unmarshal(loginRec.Body.Bytes(), &loginResp)
	if loginResp.User.Username != "new_developer" || loginResp.Token == "" {
		t.Fatalf("Login response không hợp lệ: %+v", loginResp)
	}

	// 5. Kiểm tra MeHandler với Token
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	meRec := httptest.NewRecorder()
	MeHandler(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("MeHandler kỳ vọng status 200, nhận: %d", meRec.Code)
	}

	var meResp struct {
		Status string   `json:"status"`
		User   SafeUser `json:"user"`
	}
	json.Unmarshal(meRec.Body.Bytes(), &meResp)
	if meResp.User.Email != "developer@supportflastdev.io.vn" {
		t.Fatalf("MeHandler trả về user không đúng: %+v", meResp.User)
	}
}

// TestSystem_SQLiteIntegration kiểm tra đọc ghi bảng system_releases của SQLite
func TestSystem_SQLiteIntegration(t *testing.T) {
	cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	cache.DefaultCache.Clear()

	// 1. Kiểm tra ban đầu
	initialRels := LoadReleasesFromDB()
	initialCount := len(initialRels)

	// 2. Lưu bản cập nhật mới qua SaveReleaseToDB
	item := ReleaseItem{
		ID:        "rel-20260924-test",
		Version:   "v3.0.0",
		Title:     "Phiên Bản SQLite 3.0",
		Type:      "Nâng cấp lớn (Feature)",
		Date:      "2026-09-24",
		Author:    "Backend Team",
		Changes:   []string{"Chuyển đổi hoàn toàn sang SQLite Prepared Statements", "Hiệu năng truy vấn tăng 10x"},
		Active:    true,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	if err := SaveReleaseToDB(item, "hash_300"); err != nil {
		t.Fatalf("SaveReleaseToDB failed: %v", err)
	}

	// 3. Đọc lại từ LoadReleasesFromDB
	relsAfter := LoadReleasesFromDB()
	if len(relsAfter) != initialCount+1 {
		t.Fatalf("Kỳ vọng %d release trong SQLite, nhận: %d", initialCount+1, len(relsAfter))
	}
	var foundRelease *ReleaseItem
	for _, r := range relsAfter {
		if r.Version == "v3.0.0" {
			copyRel := r
			foundRelease = &copyRel
			break
		}
	}
	if foundRelease == nil || foundRelease.Title != item.Title {
		t.Fatalf("Dữ liệu release không khớp hoặc không tìm thấy v3.0.0: %+v", foundRelease)
	}
	if len(foundRelease.Changes) != 2 {
		t.Fatalf("Changes không bảo toàn: %+v", foundRelease.Changes)
	}

	// 4. Kiểm tra SystemUpdatesHandler() trả về đúng release từ SQLite
	updReq := httptest.NewRequest(http.MethodGet, "/api/system/updates", nil)
	updRec := httptest.NewRecorder()
	SystemUpdatesHandler(updRec, updReq)

	if updRec.Code != http.StatusOK {
		t.Fatalf("SystemUpdatesHandler kỳ vọng status 200, nhận: %d", updRec.Code)
	}

	var updatesResp struct {
		Status        string        `json:"status"`
		TotalReleases int           `json:"total_releases"`
		Releases      []ReleaseItem `json:"releases"`
	}
	if err := json.Unmarshal(updRec.Body.Bytes(), &updatesResp); err != nil {
		t.Fatalf("Unmarshal updates response failed: %v", err)
	}
	if updatesResp.TotalReleases != 1 || len(updatesResp.Releases) != 1 {
		t.Fatalf("TotalReleases kỳ vọng 1, nhận: %d", updatesResp.TotalReleases)
	}
	if updatesResp.Releases[0].Version != "v3.0.0" {
		t.Fatalf("Version kỳ vọng v3.0.0, nhận: %s", updatesResp.Releases[0].Version)
	}
}

// TestApps_AdminUpdateDeleteAudit kiểm tra toàn diện chức năng sửa, xóa và gọi 5 subagents thẩm định app
func TestApps_AdminUpdateDeleteAudit(t *testing.T) {
	cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	cache.DefaultCache.Clear()

	// 1. Tạo app ban đầu
	testApp := AppItem{
		ID:            "APP-AUDIT-TEST",
		Name:          "Sentinel Test Tool",
		Version:       "v1.0.0",
		Platform:      "Windows, Linux",
		Category:      "Security",
		Desc:          "Công cụ kiểm thử bảo mật nội bộ",
		FileName:      "sentinel-test.zip",
		SizeBytes:     1048576,
		SizeFormatted: "1.0 MB",
		SHA256:        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Author:        "Cyber Team",
		Downloads:     10,
		Status:        "published",
		PublishedAt:   time.Now().Format(time.RFC3339),
		DownloadURL:   "/api/apps/download/APP-AUDIT-TEST",
	}
	if err := SaveApp(testApp); err != nil {
		t.Fatalf("SaveApp thất bại: %v", err)
	}

	// 2. Tạo API Key quyền Admin để test
	adminKey, err := GenerateNewKey("Admin Test Key", 1)
	if err != nil {
		t.Fatalf("GenerateNewKey thất bại: %v", err)
	}

	// 3. Test AppUpdateHandler:
	// a. Gọi không có token -> phải bị 401
	unauthBody := bytes.NewBufferString(`{"id":"APP-AUDIT-TEST","name":"Hacked Name"}`)
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/apps/update", unauthBody)
	unauthReq.Header.Set("Content-Type", "application/json")
	unauthRec := httptest.NewRecorder()
	AppUpdateHandler(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("AppUpdateHandler không có token kỳ vọng 401, nhận: %d", unauthRec.Code)
	}

	// b. Gọi với API Key hợp lệ -> phải thành công 200
	updateBody := bytes.NewBufferString(`{"id":"APP-AUDIT-TEST","name":"Sentinel Suite Pro","version":"v2.5.0","desc":"Bản nâng cấp tối ưu"}`)
	updateReq := httptest.NewRequest(http.MethodPost, "/api/apps/update", updateBody)
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-API-Key", adminKey.Key)
	updateRec := httptest.NewRecorder()
	AppUpdateHandler(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("AppUpdateHandler với admin key kỳ vọng 200, nhận: %d (body: %s)", updateRec.Code, updateRec.Body.String())
	}

	// Kiểm tra dữ liệu trong SQLite đã cập nhật
	apps := LoadApps()
	var found *AppItem
	for _, a := range apps {
		if a.ID == "APP-AUDIT-TEST" {
			found = &a
			break
		}
	}
	if found == nil || found.Name != "Sentinel Suite Pro" || found.Version != "v2.5.0" {
		t.Fatalf("Dữ liệu sau update không khớp: %+v", found)
	}

	// 4. Test AppAuditHandler:
	auditBody := bytes.NewBufferString(`{"id":"APP-AUDIT-TEST","name":"Sentinel Suite Pro","version":"v2.5.0"}`)
	auditReq := httptest.NewRequest(http.MethodPost, "/api/apps/audit", auditBody)
	auditReq.Header.Set("Content-Type", "application/json")
	auditReq.Header.Set("X-API-Key", adminKey.Key)
	auditRec := httptest.NewRecorder()
	AppAuditHandler(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("AppAuditHandler kỳ vọng 200, nhận: %d", auditRec.Code)
	}

	var auditResp struct {
		Status        string                   `json:"status"`
		SecurityScore int                      `json:"security_score"`
		AuditStatus   string                   `json:"audit_status"`
		Steps         []map[string]interface{} `json:"steps"`
	}
	if err := json.Unmarshal(auditRec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("Unmarshal audit response thất bại: %v", err)
	}
	if auditResp.Status != "success" || auditResp.AuditStatus != "VERIFIED_CLEAN" || len(auditResp.Steps) != 5 {
		t.Fatalf("Kết quả 5 subagents audit không đầy đủ: %+v", auditResp)
	}

	// 5. Test AppDeleteHandler:
	deleteReq := httptest.NewRequest(http.MethodPost, "/api/apps/delete?id=APP-AUDIT-TEST", nil)
	deleteReq.Header.Set("X-API-Key", adminKey.Key)
	deleteRec := httptest.NewRecorder()
	AppDeleteHandler(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("AppDeleteHandler kỳ vọng 200, nhận: %d", deleteRec.Code)
	}

	// Kiểm tra đã bị xóa khỏi SQLite
	appsAfterDelete := LoadApps()
	for _, a := range appsAfterDelete {
		if a.ID == "APP-AUDIT-TEST" {
			t.Fatalf("Ứng dụng APP-AUDIT-TEST vẫn còn trong SQLite sau khi xóa!")
		}
	}
}
