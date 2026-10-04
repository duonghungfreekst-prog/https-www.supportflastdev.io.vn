package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
)

func TestHandleListFilesGuestPolicy(t *testing.T) {
	testDir := t.TempDir()

	dbPath := filepath.Join(testDir, "test_guest_api.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}
	defer db.Close()

	gd := gdrive.NewManager(db)
	vfsEngine := vfs.NewVFS(db, gd)

	if err := InitJWTKeys(testDir); err != nil {
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	server := NewServer(db, gd, vfsEngine, 8080, "", testDir)
	server.setupSubMuxes()

	// 1. Lưu 1 file thuộc admin vào thư mục root
	adminFile := &models.VirtualFile{
		ID:        "file_admin_sample",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "bao_cao_tai_chinh.xlsx",
		Path:      "/bao_cao_tai_chinh.xlsx",
		IsDir:     false,
		SizeBytes: 4096,
		MimeType:  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	if err := db.SaveVirtualFile(adminFile); err != nil {
		t.Fatalf("SaveVirtualFile adminFile thất bại: %v", err)
	}

	// 2. Guest gọi /api/files ở chế độ mặc định ("view_only")
	req := httptest.NewRequest("GET", "/api/files?parent_id=root", nil)
	rec := httptest.NewRecorder()
	server.handleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200 cho khách ở view_only mode, thực tế: %d, body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Files []models.VirtualFile `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal response thất bại: %v", err)
	}

	if len(res.Files) != 1 {
		t.Fatalf("Kỳ vọng 1 file hiển thị cho khách, thực tế: %d", len(res.Files))
	}
	if res.Files[0].ID != "file_admin_sample" {
		t.Errorf("File ID không khớp: %s", res.Files[0].ID)
	}
	if !res.Files[0].IsAdminOwned {
		t.Errorf("Kỳ vọng IsAdminOwned = true")
	}
	if !res.Files[0].RequiresOTP {
		t.Errorf("Kỳ vọng RequiresOTP = true vì chưa được share")
	}

	// 3. Đổi cấu hình sang "strict"
	settings, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings thất bại: %v", err)
	}
	settings.GuestAccessMode = "strict"
	if err := db.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings strict thất bại: %v", err)
	}

	reqStrict := httptest.NewRequest("GET", "/api/files?parent_id=root", nil)
	recStrict := httptest.NewRecorder()
	server.handleListFiles(recStrict, reqStrict)

	if recStrict.Code != http.StatusUnauthorized {
		t.Fatalf("Kỳ vọng HTTP 401 Unauthorized khi khách truy cập ở strict mode, thực tế: %d", recStrict.Code)
	}
}

func TestHandleCleanupAndRepairAccountsAPI(t *testing.T) {
	testDir := t.TempDir()

	dbPath := filepath.Join(testDir, "test_acc_api.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}
	defer db.Close()

	gd := gdrive.NewManager(db)
	vfsEngine := vfs.NewVFS(db, gd)

	if err := InitJWTKeys(testDir); err != nil {
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	server := NewServer(db, gd, vfsEngine, 8080, "", testDir)
	server.setupSubMuxes()

	// 1. Test POST /api/accounts/cleanup
	reqCleanup := httptest.NewRequest("POST", "/api/accounts/cleanup", nil)
	recCleanup := httptest.NewRecorder()
	server.AccountsHandler(recCleanup, reqCleanup)

	if recCleanup.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200 cho /api/accounts/cleanup, thực tế: %d, body: %s", recCleanup.Code, recCleanup.Body.String())
	}

	var cleanupRes struct {
		Status       string `json:"status"`
		CleanedCount int    `json:"cleaned_count"`
		MergedCount  int    `json:"merged_count"`
		Message      string `json:"message"`
	}
	if err := json.Unmarshal(recCleanup.Body.Bytes(), &cleanupRes); err != nil {
		t.Fatalf("Unmarshal cleanup response thất bại: %v", err)
	}
	if cleanupRes.Status != "success" {
		t.Errorf("Kỳ vọng status == success, nhận được: %s", cleanupRes.Status)
	}

	// 2. Test POST /api/accounts/repair
	reqRepair := httptest.NewRequest("POST", "/api/accounts/repair", nil)
	recRepair := httptest.NewRecorder()
	server.AccountsHandler(recRepair, reqRepair)

	if recRepair.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200 cho /api/accounts/repair, thực tế: %d, body: %s", recRepair.Code, recRepair.Body.String())
	}

	var repairRes struct {
		Status        string `json:"status"`
		RepairedCount int    `json:"repaired_count"`
		Message       string `json:"message"`
	}
	if err := json.Unmarshal(recRepair.Body.Bytes(), &repairRes); err != nil {
		t.Fatalf("Unmarshal repair response thất bại: %v", err)
	}
	if repairRes.Status != "success" {
		t.Errorf("Kỳ vọng status == success, nhận được: %s", repairRes.Status)
	}
}
