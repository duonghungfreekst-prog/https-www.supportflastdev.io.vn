package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/registry"
)

func setupIntegrityTestEnvironment(t *testing.T) (*Server, *storage.DB, string, string, func()) {
	t.Helper()
	testDir := filepath.Join(`f:\supportflast.dev\data`, fmt.Sprintf("test_integrity_api_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Không thể tạo test directory: %v", err)
	}

	dbPath := filepath.Join(testDir, "test_metadata.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		_ = os.RemoveAll(testDir)
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}

	gd := gdrive.NewManager(db)
	vfsEngine := vfs.NewVFS(db, gd)

	if err := InitJWTKeys(testDir); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	server := NewServer(db, gd, vfsEngine, 8080, "", testDir)
	server.setupSubMuxes()

	// Tạo tài khoản admin test cho JWT
	adminHubUser := registry.User{
		ID:       "user_admin",
		Username: "admin",
		Email:    "admin@supportflastdev.io.vn",
		Role:     "admin",
	}
	adminToken, err := registry.IssueRS256Token(adminHubUser, 24*time.Hour)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
		t.Fatalf("IssueRS256Token thất bại: %v", err)
	}

	// Tạo tài khoản Drive mặc định để file_chunks hợp lệ foreign key
	acc := &models.Account{
		ID:        "acc_integrity_test",
		Email:     "integrity@example.com",
		Name:      "Drive Storage 1",
		AuthType:  "oauth",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = db.SaveAccount(acc)

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
	}

	return server, db, adminToken, acc.ID, cleanup
}

func TestFileStatusAPI(t *testing.T) {
	server, db, adminToken, accID, cleanup := setupIntegrityTestEnvironment(t)
	defer cleanup()

	// 1. Tạo tệp lành lặn
	healthyFile := &models.VirtualFile{
		ID:               "vfile_status_healthy",
		UserID:           "user_admin",
		ParentID:         "root",
		Name:             "sample_video.mp4",
		Path:             "/sample_video.mp4",
		IsDir:            false,
		SizeBytes:        1024 * 1024,
		MimeType:         "video/mp4",
		ChunkCount:       1,
		IsEncrypted:      true,
		HasMissingChunks: false,
	}
	_ = db.SaveVirtualFile(healthyFile)
	_ = db.SaveChunks([]models.FileChunk{
		{
			ChunkID:        "chk_healthy_1",
			FileID:         healthyFile.ID,
			ChunkIndex:     0,
			AccountID:      accID,
			GDriveFileID:   "drive_file_1",
			ChunkSizeBytes: 1024 * 1024,
			Status:         "uploaded",
		},
	})

	// 2. Tạo tệp bị thiếu dữ liệu nguồn
	corruptedFile := &models.VirtualFile{
		ID:               "vfile_status_corrupted",
		UserID:           "user_admin",
		ParentID:         "root",
		Name:             "broken_archive.zip",
		Path:             "/broken_archive.zip",
		IsDir:            false,
		SizeBytes:        2 * 1024 * 1024,
		MimeType:         "application/zip",
		ChunkCount:       2,
		IsEncrypted:      true,
		HasMissingChunks: true,
	}
	_ = db.SaveVirtualFile(corruptedFile)
	_ = db.SaveChunks([]models.FileChunk{
		{
			ChunkID:        "chk_corr_1",
			FileID:         corruptedFile.ID,
			ChunkIndex:     0,
			AccountID:      accID,
			GDriveFileID:   "drive_file_2",
			ChunkSizeBytes: 1024 * 1024,
			Status:         "uploaded",
		},
		{
			ChunkID:        "chk_corr_2",
			FileID:         corruptedFile.ID,
			ChunkIndex:     1,
			AccountID:      accID,
			GDriveFileID:   "drive_file_3_deleted",
			ChunkSizeBytes: 1024 * 1024,
			Status:         "missing", // Chunk bị 404
		},
	})

	// Test 1: Kiểm tra tệp lành lặn
	reqOK := httptest.NewRequest("GET", "/api/files/status?id="+healthyFile.ID, nil)
	reqOK.Header.Set("Authorization", "Bearer "+adminToken)
	wOK := httptest.NewRecorder()
	server.FilesHandler(wOK, reqOK)

	if wOK.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200, thực tế: %d", wOK.Code)
	}

	var resOK map[string]interface{}
	if err := json.Unmarshal(wOK.Body.Bytes(), &resOK); err != nil {
		t.Fatalf("Lỗi decode JSON: %v", err)
	}

	if resOK["ready"] != true {
		t.Errorf("Kỳ vọng ready == true, thực tế: %v", resOK["ready"])
	}
	if resOK["has_missing_chunks"] != false {
		t.Errorf("Kỳ vọng has_missing_chunks == false, thực tế: %v", resOK["has_missing_chunks"])
	}
	if resOK["status"] != "ready" {
		t.Errorf("Kỳ vọng status == 'ready', thực tế: %v", resOK["status"])
	}

	// Test 2: Kiểm tra tệp bị thiếu dữ liệu nguồn
	reqCorrupt := httptest.NewRequest("GET", "/api/files/status?id="+corruptedFile.ID, nil)
	reqCorrupt.Header.Set("Authorization", "Bearer "+adminToken)
	wCorrupt := httptest.NewRecorder()
	server.FilesHandler(wCorrupt, reqCorrupt)

	if wCorrupt.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng HTTP 200 cho status check, thực tế: %d", wCorrupt.Code)
	}

	var resCorrupt map[string]interface{}
	if err := json.Unmarshal(wCorrupt.Body.Bytes(), &resCorrupt); err != nil {
		t.Fatalf("Lỗi decode JSON: %v", err)
	}

	if resCorrupt["ready"] != false {
		t.Errorf("Kỳ vọng ready == false cho tệp thiếu chunk, thực tế: %v", resCorrupt["ready"])
	}
	if resCorrupt["has_missing_chunks"] != true {
		t.Errorf("Kỳ vọng has_missing_chunks == true cho tệp thiếu chunk, thực tế: %v", resCorrupt["has_missing_chunks"])
	}
	if resCorrupt["status"] != "missing_chunks" {
		t.Errorf("Kỳ vọng status == 'missing_chunks', thực tế: %v", resCorrupt["status"])
	}

	// Test 3: Thử mở stream trực tiếp tệp bị thiếu dữ liệu nguồn -> Server phải chặn với HTTP 410 Gone
	reqStream := httptest.NewRequest("GET", "/api/files/stream?id="+corruptedFile.ID, nil)
	reqStream.Header.Set("Authorization", "Bearer "+adminToken)
	wStream := httptest.NewRecorder()
	server.FilesHandler(wStream, reqStream)

	if wStream.Code != http.StatusGone {
		t.Errorf("Kỳ vọng stream tệp thiếu chunk bị chặn với HTTP 410 Gone, thực tế: %d", wStream.Code)
	}

	// Test 4: Thử tải về tệp bị thiếu dữ liệu nguồn -> Server phải chặn với HTTP 410 Gone
	reqDownload := httptest.NewRequest("GET", "/api/files/download?id="+corruptedFile.ID, nil)
	reqDownload.Header.Set("Authorization", "Bearer "+adminToken)
	wDownload := httptest.NewRecorder()
	server.FilesHandler(wDownload, reqDownload)

	if wDownload.Code != http.StatusGone {
		t.Errorf("Kỳ vọng download tệp thiếu chunk bị chặn với HTTP 410 Gone, thực tế: %d", wDownload.Code)
	}
}

func TestIntegrityCheckAdminAPI(t *testing.T) {
	server, db, adminToken, accID, cleanup := setupIntegrityTestEnvironment(t)
	defer cleanup()

	// Tạo 1 file có 1 chunk missing trong DB
	f := &models.VirtualFile{
		ID:          "vfile_test_diag",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "document_corrupted.pdf",
		Path:        "/document_corrupted.pdf",
		IsDir:       false,
		SizeBytes:   1024,
		ChunkCount:  1,
		IsEncrypted: true,
	}
	_ = db.SaveVirtualFile(f)
	_ = db.SaveChunks([]models.FileChunk{
		{
			ChunkID:        "chk_diag_1",
			FileID:         f.ID,
			ChunkIndex:     0,
			AccountID:      accID,
			GDriveFileID:   "drive_chk_deleted",
			ChunkSizeBytes: 1024,
			Status:         "missing",
		},
	})

	// Test 1: Khách không có quyền gọi -> 403 Forbidden
	reqGuest := httptest.NewRequest("GET", "/api/admin/storage/integrity-check", nil)
	wGuest := httptest.NewRecorder()
	server.AdminStorageHandler(wGuest, reqGuest)

	if wGuest.Code != http.StatusForbidden {
		t.Errorf("Kỳ vọng khách bị từ chối 403 Forbidden, thực tế: %d", wGuest.Code)
	}

	// Test 2: Admin gọi API -> 200 OK
	reqAdmin := httptest.NewRequest("GET", "/api/admin/storage/integrity-check", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminToken)
	wAdmin := httptest.NewRecorder()
	server.AdminStorageHandler(wAdmin, reqAdmin)

	if wAdmin.Code != http.StatusOK {
		t.Fatalf("Kỳ vọng Admin thực thi thành công HTTP 200, thực tế: %d (Body: %s)", wAdmin.Code, wAdmin.Body.String())
	}

	var report storage.IntegrityReport
	if err := json.Unmarshal(wAdmin.Body.Bytes(), &report); err != nil {
		t.Fatalf("Lỗi decode JSON IntegrityReport: %v", err)
	}

	if report.TotalFilesScanned < 1 {
		t.Errorf("Kỳ vọng quét ít nhất 1 tệp, thực tế: %d", report.TotalFilesScanned)
	}
	if report.CorruptedFilesCount != 1 {
		t.Errorf("Kỳ vọng 1 tệp thiếu dữ liệu, thực tế: %d", report.CorruptedFilesCount)
	}

	// Xác nhận cờ HasMissingChunks trong DB đã được bật
	vfileAfter, _ := db.GetVirtualFile(f.ID)
	if !vfileAfter.HasMissingChunks {
		t.Errorf("Kỳ vọng VirtualFile có HasMissingChunks == true sau khi chạy integrity check")
	}
}
