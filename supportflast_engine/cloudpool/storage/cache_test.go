package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
)

// TestL1Cache_ListVirtualFiles_HitAndInvalidation kiểm tra cơ chế Cache Hit và Invalidation cho ListVirtualFiles
func TestL1Cache_ListVirtualFiles_HitAndInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_l1_vfs.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Tạo file hợp lệ qua SaveVirtualFile
	f1 := &models.VirtualFile{
		ID:        "file_1",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "document1.pdf",
		Path:      "/document1.pdf",
		IsDir:     false,
		SizeBytes: 1024,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(f1); err != nil {
		t.Fatalf("SaveVirtualFile f1 failed: %v", err)
	}

	// 2. Lần đầu gọi ListVirtualFiles: cache miss -> đọc DB và lưu vào cache
	files1, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles lần 1 thất bại: %v", err)
	}
	if len(files1) != 1 {
		t.Fatalf("Kỳ vọng 1 file, nhận được %d", len(files1))
	}

	// 3. Thêm một file lén lút trực tiếp vào DB bằng raw SQL mà KHÔNG qua SaveVirtualFile
	// (để kiểm tra xem ListVirtualFiles có thực sự lấy từ L1 RAM Cache hay không)
	now := time.Now()
	_, err = db.SQLDB().Exec(`INSERT INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, is_deleted, created_at, updated_at)
		VALUES ('file_sneaky', 'user_admin', 'root', 'sneaky.txt', '/sneaky.txt', 0, 500, 'text/plain', 0, 0, 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("Insert raw SQL thất bại: %v", err)
	}

	// 4. Gọi lại ListVirtualFiles: Phải là Cache Hit (vẫn chỉ trả về 1 file, không thấy file_sneaky)
	filesCached, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles lần 2 thất bại: %v", err)
	}
	if len(filesCached) != 1 {
		t.Fatalf("L1 Cache thất bại: Kỳ vọng 1 file từ cache, thực tế nhận được %d", len(filesCached))
	}

	// 5. Kích hoạt Invalidation thông qua SaveVirtualFile
	f2 := &models.VirtualFile{
		ID:        "file_2",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "document2.pdf",
		Path:      "/document2.pdf",
		IsDir:     false,
		SizeBytes: 2048,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(f2); err != nil {
		t.Fatalf("SaveVirtualFile f2 failed: %v", err)
	}

	// 6. Sau khi Invalidate, ListVirtualFiles phải đọc lại từ DB (thấy cả 3 files: f1, f2, file_sneaky)
	filesInvalidated, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles sau invalidation thất bại: %v", err)
	}
	if len(filesInvalidated) != 3 {
		t.Fatalf("Invalidation thất bại: Kỳ vọng 3 files từ DB, thực tế nhận được %d", len(filesInvalidated))
	}

	// 7. Kiểm tra SoftDeleteVirtualFile cũng tự động Invalidate VFS cache
	if err := db.SoftDeleteVirtualFile("file_1"); err != nil {
		t.Fatalf("SoftDeleteVirtualFile thất bại: %v", err)
	}
	filesAfterSoftDelete, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles sau SoftDelete thất bại: %v", err)
	}
	if len(filesAfterSoftDelete) != 2 {
		t.Fatalf("Kỳ vọng 2 files sau SoftDelete, nhận được %d", len(filesAfterSoftDelete))
	}

	// 8. Kiểm tra RestoreVirtualFile cũng tự động Invalidate VFS cache
	if err := db.RestoreVirtualFile("file_1"); err != nil {
		t.Fatalf("RestoreVirtualFile thất bại: %v", err)
	}
	filesAfterRestore, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles sau Restore thất bại: %v", err)
	}
	if len(filesAfterRestore) != 3 {
		t.Fatalf("Kỳ vọng 3 files sau Restore, nhận được %d", len(filesAfterRestore))
	}

	// 9. Kiểm tra RenameVirtualFile cũng tự động Invalidate VFS cache
	if err := db.RenameVirtualFile("file_1", "document1_renamed.pdf", "/document1_renamed.pdf"); err != nil {
		t.Fatalf("RenameVirtualFile thất bại: %v", err)
	}
	filesAfterRename, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles sau Rename thất bại: %v", err)
	}
	foundRenamed := false
	for _, f := range filesAfterRename {
		if f.Name == "document1_renamed.pdf" {
			foundRenamed = true
			break
		}
	}
	if !foundRenamed {
		t.Fatalf("Không tìm thấy file sau rename trong danh sách đã refresh từ DB")
	}

	// 10. Kiểm tra DeleteVirtualFile cũng tự động Invalidate VFS cache
	if err := db.DeleteVirtualFile("file_sneaky"); err != nil {
		t.Fatalf("DeleteVirtualFile thất bại: %v", err)
	}
	filesAfterDelete, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles sau Delete thất bại: %v", err)
	}
	if len(filesAfterDelete) != 2 {
		t.Fatalf("Kỳ vọng 2 files sau Delete, nhận được %d", len(filesAfterDelete))
	}
}

// TestL1Cache_GetStats_HitAndInvalidation kiểm tra L1 Cache và Invalidation cho GetStats
func TestL1Cache_GetStats_HitAndInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_l1_stats.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Gọi GetStats lần đầu: Cache Miss, lưu kết quả
	stats1, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats lần 1 thất bại: %v", err)
	}
	if stats1.TotalAccounts != 0 {
		t.Fatalf("Kỳ vọng 0 tài khoản, nhận được %d", stats1.TotalAccounts)
	}

	// 2. Chèn trực tiếp tài khoản vào DB bằng raw SQL
	_, err = db.SQLDB().Exec(`INSERT INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, created_at, updated_at)
		VALUES ('raw_acc_1', 'raw@test.com', 'h1', 'Raw', 'h2', '', 'oauth', '', '', 'root', 1000, 0, 1000, 'active', ?, ?)`, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Insert raw account thất bại: %v", err)
	}

	// 3. Gọi GetStats lần 2: Cache Hit (vẫn trả về 0 tài khoản từ cache)
	statsCached, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats lần 2 thất bại: %v", err)
	}
	if statsCached.TotalAccounts != 0 {
		t.Fatalf("L1 Cache thất bại: Kỳ vọng 0 tài khoản từ cache, thực tế nhận được %d", statsCached.TotalAccounts)
	}

	// 4. Kích hoạt Invalidation thông qua SaveAccount
	acc := &models.Account{
		ID:              "acc_legit",
		Email:           "legit@example.com",
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  5 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024,
		Status:          "active",
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount thất bại: %v", err)
	}

	// 5. Sau khi SaveAccount, cache stats bị xóa -> GetStats đọc lại từ DB (thấy cả 2 tài khoản)
	statsAfter, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats sau invalidation thất bại: %v", err)
	}
	if statsAfter.TotalAccounts != 2 {
		t.Fatalf("Invalidation thất bại: Kỳ vọng 2 tài khoản sau khi thêm, thực tế nhận được %d", statsAfter.TotalAccounts)
	}
}

// TestL1Cache_ListAccounts_HitAndInvalidation kiểm tra L1 Cache và Invalidation cho ListAccounts
func TestL1Cache_ListAccounts_HitAndInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_l1_accounts.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Thêm 1 tài khoản
	acc := &models.Account{
		ID:        "acc_token_test",
		Email:     "tokentest@example.com",
		TokenJSON: `{"access_token":"token_v1"}`,
		Status:    "active",
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	// 2. Gọi ListAccounts lần 1: cache miss -> đọc DB và giải mã
	accounts1, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts lần 1 thất bại: %v", err)
	}
	if len(accounts1) != 1 || accounts1[0].TokenJSON != `{"access_token":"token_v1"}` {
		t.Fatalf("Dữ liệu tài khoản ban đầu không khớp: %+v", accounts1[0])
	}

	// 3. Gọi ListAccounts lần 2: Cache Hit
	accountsCached, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts lần 2 thất bại: %v", err)
	}
	if len(accountsCached) != 1 {
		t.Fatalf("Cache hit thất bại")
	}

	// 4. Cập nhật token qua UpdateAccountToken
	if err := db.UpdateAccountToken("acc_token_test", `{"access_token":"token_v2"}`); err != nil {
		t.Fatalf("UpdateAccountToken thất bại: %v", err)
	}

	// 5. Gọi ListAccounts lần 3: Phải thấy token_v2 mới vì cache đã bị xóa
	accountsUpdated, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts sau update token thất bại: %v", err)
	}
	if len(accountsUpdated) != 1 || accountsUpdated[0].TokenJSON != `{"access_token":"token_v2"}` {
		t.Fatalf("Invalidation thất bại: Kỳ vọng token_v2, nhận được: %s", accountsUpdated[0].TokenJSON)
	}

	// 6. Xóa tài khoản -> cache phải bị xóa
	if err := db.DeleteAccount("acc_token_test"); err != nil {
		t.Fatalf("DeleteAccount thất bại: %v", err)
	}
	accountsDeleted, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts sau xóa thất bại: %v", err)
	}
	if len(accountsDeleted) != 0 {
		t.Fatalf("Kỳ vọng 0 tài khoản sau khi xóa, thực tế: %d", len(accountsDeleted))
	}
}

// TestL1Cache_GetSettings_HitAndInvalidation kiểm tra L1 Cache và Invalidation cho GetSettings
func TestL1Cache_GetSettings_HitAndInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_l1_settings.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Lần đầu gọi GetSettings
	settings1, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings lần 1 thất bại: %v", err)
	}
	origPort := settings1.ServerPort

	// 2. Chèn/Sửa trực tiếp bằng raw SQL
	_, err = db.SQLDB().Exec("UPDATE settings SET value = '9099' WHERE key = 'server_port'")
	if err != nil {
		t.Fatalf("Raw update server_port thất bại: %v", err)
	}

	// 3. Lần 2 gọi GetSettings: Cache Hit -> vẫn giữ port cũ
	settingsCached, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings lần 2 thất bại: %v", err)
	}
	if settingsCached.ServerPort != origPort {
		t.Fatalf("L1 Cache thất bại: Kỳ vọng giữ port cũ %d, thực tế nhận được %d", origPort, settingsCached.ServerPort)
	}

	// 4. Kích hoạt Invalidation thông qua SaveSettings
	settingsCached.ServerPort = 7777
	if err := db.SaveSettings(settingsCached); err != nil {
		t.Fatalf("SaveSettings thất bại: %v", err)
	}

	// 5. Lần 3 gọi GetSettings: Phải nhận đúng 7777 từ DB
	settingsFresh, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings lần 3 thất bại: %v", err)
	}
	if settingsFresh.ServerPort != 7777 {
		t.Fatalf("Invalidation thất bại: Kỳ vọng port 7777, thực tế nhận được %d", settingsFresh.ServerPort)
	}
}

// TestL1Cache_ThreadSafety_And_NoRace kiểm tra tính thread-safe và khả năng chịu tải đồng thời cao
func TestL1Cache_ThreadSafety_And_NoRace(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_l1_race.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// Khởi tạo một số dữ liệu mẫu
	_ = db.SaveAccount(&models.Account{ID: "acc_conc", Email: "conc@example.com", Status: "active"})
	_ = db.SaveVirtualFile(&models.VirtualFile{ID: "file_conc", UserID: "user_admin", ParentID: "root", Name: "test.txt"})

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// 10 Goroutines liên tục đọc
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					_, _ = db.ListVirtualFiles("user_admin", "root")
					_, _ = db.GetStats()
					_, _ = db.ListAccounts()
					_, _ = db.GetSettings()
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(i)
	}

	// 10 Goroutines liên tục ghi / invalidation
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cnt := 0
			for {
				select {
				case <-stopChan:
					return
				default:
					cnt++
					fID := fmt.Sprintf("file_%d_%d", id, cnt%20)
					_ = db.SaveVirtualFile(&models.VirtualFile{
						ID:       fID,
						UserID:   "user_admin",
						ParentID: "root",
						Name:     fID + ".txt",
					})
					if cnt%5 == 0 {
						db.InvalidateVFSCache()
						db.InvalidateStatsCache()
						db.InvalidateAccountsCache()
						db.InvalidateSettingsCache()
					}
					time.Sleep(2 * time.Millisecond)
				}
			}
		}(i)
	}

	// Chạy kiểm tra đồng thời trong 600ms
	time.Sleep(600 * time.Millisecond)
	close(stopChan)
	wg.Wait()

	// Đảm bảo sau tải đồng thời, hệ thống vẫn hoạt động chính xác
	finalStats, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats sau tải đồng thời thất bại: %v", err)
	}
	if finalStats == nil {
		t.Fatalf("GetStats trả về nil")
	}
}
