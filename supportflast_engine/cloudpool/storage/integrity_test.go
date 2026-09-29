package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
)

// mockDriveChunkChecker mô phỏng phản hồi từ Google Drive API cho Unit Test
type mockDriveChunkChecker struct {
	existingFileIDs map[string]bool
	errorFileIDs    map[string]error
}

func (m *mockDriveChunkChecker) CheckChunkExists(ctx context.Context, accountID, gdriveFileID string) (bool, error) {
	if err, hasErr := m.errorFileIDs[gdriveFileID]; hasErr {
		return false, err
	}
	exists := m.existingFileIDs[gdriveFileID]
	return exists, nil
}

func TestIntegrityDiagnosticsAndRemediation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_integrity.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("Khởi tạo test DB thất bại: %v", err)
	}
	defer db.Close()

	// 1. Tạo tài khoản mẫu
	acc := &models.Account{
		ID:        "acc_test_drive",
		Email:     "integrity_test@gmail.com",
		Name:      "Integrity Test Drive",
		AuthType:  "oauth",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("Lưu account thất bại: %v", err)
	}

	// 2. Tạo 2 tệp ảo:
	// - file_ok: 2 chunks, cả 2 đều tồn tại trên Drive
	// - file_broken: 2 chunks, chunk_1 tồn tại, chunk_2 bị 404 trên Drive
	fileOK := &models.VirtualFile{
		ID:          "vfile_healthy_1",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "video_healthy.mp4",
		Path:        "/video_healthy.mp4",
		IsDir:       false,
		SizeBytes:   40 * 1024 * 1024,
		MimeType:    "video/mp4",
		ChunkCount:  2,
		IsEncrypted: true,
	}
	if err := db.SaveVirtualFile(fileOK); err != nil {
		t.Fatalf("Lưu fileOK thất bại: %v", err)
	}

	chunksOK := []models.FileChunk{
		{
			ChunkID:        "chk_ok_0",
			FileID:         fileOK.ID,
			ChunkIndex:     0,
			AccountID:      acc.ID,
			GDriveFileID:   "gdrive_chk_ok_0",
			ChunkSizeBytes: 20 * 1024 * 1024,
			Status:         "uploaded",
		},
		{
			ChunkID:        "chk_ok_1",
			FileID:         fileOK.ID,
			ChunkIndex:     1,
			AccountID:      acc.ID,
			GDriveFileID:   "gdrive_chk_ok_1",
			ChunkSizeBytes: 20 * 1024 * 1024,
			Status:         "uploaded",
		},
	}
	if err := db.SaveChunks(chunksOK); err != nil {
		t.Fatalf("Lưu chunksOK thất bại: %v", err)
	}

	fileBroken := &models.VirtualFile{
		ID:          "vfile_broken_2",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "dataset_corrupted.zip",
		Path:        "/dataset_corrupted.zip",
		IsDir:       false,
		SizeBytes:   40 * 1024 * 1024,
		MimeType:    "application/zip",
		ChunkCount:  2,
		IsEncrypted: true,
	}
	if err := db.SaveVirtualFile(fileBroken); err != nil {
		t.Fatalf("Lưu fileBroken thất bại: %v", err)
	}

	chunksBroken := []models.FileChunk{
		{
			ChunkID:        "chk_brk_0",
			FileID:         fileBroken.ID,
			ChunkIndex:     0,
			AccountID:      acc.ID,
			GDriveFileID:   "gdrive_chk_brk_0",
			ChunkSizeBytes: 20 * 1024 * 1024,
			Status:         "uploaded",
		},
		{
			ChunkID:        "chk_brk_1",
			FileID:         fileBroken.ID,
			ChunkIndex:     1,
			AccountID:      acc.ID,
			GDriveFileID:   "gdrive_chk_brk_1_deleted", // chunk này đã bị xóa trên Drive (404)
			ChunkSizeBytes: 20 * 1024 * 1024,
			Status:         "uploaded",
		},
	}
	if err := db.SaveChunks(chunksBroken); err != nil {
		t.Fatalf("Lưu chunksBroken thất bại: %v", err)
	}

	mockChecker := &mockDriveChunkChecker{
		existingFileIDs: map[string]bool{
			"gdrive_chk_ok_0":  true,
			"gdrive_chk_ok_1":  true,
			"gdrive_chk_brk_0": true,
			// "gdrive_chk_brk_1_deleted" không tồn tại -> false (mô phỏng 404 Not Found)
		},
		errorFileIDs: make(map[string]error),
	}

	integrityService := NewIntegrityService(db, mockChecker)

	// 3. Chạy quá trình quét kiểm tra toàn vẹn
	ctx := context.Background()
	report, err := integrityService.RunCheck(ctx, IntegrityCheckOptions{
		Concurrency: 2,
		Force:       true,
	})
	if err != nil {
		t.Fatalf("RunCheck thất bại: %v", err)
	}

	// 4. Kiểm tra các số liệu báo cáo
	if report.TotalFilesScanned != 2 {
		t.Errorf("Kỳ vọng quét 2 tệp, thực tế: %d", report.TotalFilesScanned)
	}
	if report.HealthyFilesCount != 1 {
		t.Errorf("Kỳ vọng 1 tệp lành lặn, thực tế: %d", report.HealthyFilesCount)
	}
	if report.CorruptedFilesCount != 1 {
		t.Errorf("Kỳ vọng 1 tệp bị hỏng dữ liệu nguồn, thực tế: %d", report.CorruptedFilesCount)
	}
	if report.TotalChunksScanned != 4 {
		t.Errorf("Kỳ vọng quét 4 chunks, thực tế: %d", report.TotalChunksScanned)
	}
	if report.HealthyChunksCount != 3 {
		t.Errorf("Kỳ vọng 3 chunks còn nguyên, thực tế: %d", report.HealthyChunksCount)
	}
	if report.MissingChunksCount != 1 {
		t.Errorf("Kỳ vọng 1 chunk bị thiếu (404), thực tế: %d", report.MissingChunksCount)
	}

	// 5. Kiểm tra dữ liệu được cập nhật trong cơ sở dữ liệu cloudpool_metadata.db
	// fileOK phải có has_missing_chunks = false
	savedOK, err := db.GetVirtualFile(fileOK.ID)
	if err != nil {
		t.Fatalf("GetVirtualFile(fileOK) thất bại: %v", err)
	}
	if savedOK.HasMissingChunks {
		t.Errorf("fileOK không được có cờ HasMissingChunks = true")
	}

	// fileBroken PHẢI có cờ has_missing_chunks = true để giao diện web hiển thị huy hiệu cảnh báo
	savedBroken, err := db.GetVirtualFile(fileBroken.ID)
	if err != nil {
		t.Fatalf("GetVirtualFile(fileBroken) thất bại: %v", err)
	}
	if !savedBroken.HasMissingChunks {
		t.Errorf("fileBroken BẮT BUỘC phải có cờ HasMissingChunks = true!")
	}

	// Chunk bị 404 phải được cập nhật status = 'missing'
	brokenChunksFromDB, err := db.GetChunksForFile(fileBroken.ID)
	if err != nil {
		t.Fatalf("GetChunksForFile thất bại: %v", err)
	}
	for _, c := range brokenChunksFromDB {
		if c.GDriveFileID == "gdrive_chk_brk_1_deleted" {
			if c.Status != "missing" {
				t.Errorf("Kỳ vọng chunk 404 có status = 'missing', thực tế: '%s'", c.Status)
			}
		} else {
			if c.Status != "uploaded" {
				t.Errorf("Kỳ vọng chunk tồn tại có status = 'uploaded', thực tế: '%s'", c.Status)
			}
		}
	}

	// 6. Test đơn lẻ cho 1 file qua CheckFile
	singleReport, err := integrityService.CheckFile(ctx, fileBroken.ID)
	if err != nil {
		t.Fatalf("CheckFile thất bại: %v", err)
	}
	if !singleReport.HasMissingChunks {
		t.Errorf("Kỳ vọng singleReport.HasMissingChunks = true")
	}
	if singleReport.MissingChunks != 1 {
		t.Errorf("Kỳ vọng singleReport.MissingChunks = 1, thực tế: %d", singleReport.MissingChunks)
	}

	// 7. Khắc phục (Remediation): Giả định người dùng đã khôi phục chunk bị 404 trên Drive
	mockChecker.existingFileIDs["gdrive_chk_brk_1_deleted"] = true
	reportRemediated, err := integrityService.RunCheck(ctx, IntegrityCheckOptions{
		FileID: fileBroken.ID,
		Force:  true,
	})
	if err != nil {
		t.Fatalf("RunCheck sau remediation thất bại: %v", err)
	}
	if reportRemediated.CorruptedFilesCount != 0 {
		t.Errorf("Sau khi chunk được khôi phục, fileBroken không được bị tính là hỏng nữa")
	}

	savedRestored, _ := db.GetVirtualFile(fileBroken.ID)
	if savedRestored.HasMissingChunks {
		t.Errorf("fileBroken phải được tự động xóa cờ HasMissingChunks = false sau khi khôi phục")
	}
}
