package vfs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
)

func setupTestVFS(t *testing.T) (*VFS, *storage.DB, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_vfs.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Khởi tạo test DB thất bại: %v", err)
	}

	vfs := NewVFS(db, nil)

	cleanup := func() {
		_ = db.Close()
	}
	return vfs, db, cleanup
}

func TestVFS_InFlightQuota(t *testing.T) {
	vfs, _, cleanup := setupTestVFS(t)
	defer cleanup()

	accID := "acc_inflight_test"
	if vfs.GetInFlightQuota(accID) != 0 {
		t.Fatalf("Kỳ vọng in-flight ban đầu = 0")
	}

	vfs.ReserveInFlightQuota(accID, 20*1024*1024)
	if vfs.GetInFlightQuota(accID) != 20*1024*1024 {
		t.Fatalf("Kỳ vọng in-flight = 20MB, thực tế = %d", vfs.GetInFlightQuota(accID))
	}

	vfs.ReserveInFlightQuota(accID, 10*1024*1024)
	if vfs.GetInFlightQuota(accID) != 30*1024*1024 {
		t.Fatalf("Kỳ vọng in-flight = 30MB, thực tế = %d", vfs.GetInFlightQuota(accID))
	}

	vfs.ReleaseInFlightQuota(accID, 20*1024*1024)
	if vfs.GetInFlightQuota(accID) != 10*1024*1024 {
		t.Fatalf("Kỳ vọng in-flight sau release = 10MB, thực tế = %d", vfs.GetInFlightQuota(accID))
	}

	vfs.ReleaseInFlightQuota(accID, 10*1024*1024)
	if vfs.GetInFlightQuota(accID) != 0 {
		t.Fatalf("Kỳ vọng in-flight sau release hết = 0, thực tế = %d", vfs.GetInFlightQuota(accID))
	}
}

func TestVFS_DirectoryOperations(t *testing.T) {
	vfs, _, cleanup := setupTestVFS(t)
	defer cleanup()

	userID := "user_test_dir"

	// 1. Mkdir
	folder, err := vfs.Mkdir(userID, "root", "Documents")
	if err != nil {
		t.Fatalf("Mkdir thất bại: %v", err)
	}
	if folder.Name != "Documents" || folder.Path != "/Documents" || !folder.IsDir {
		t.Fatalf("Thư mục tạo ra không khớp metadata: %+v", folder)
	}

	// Không cho phép tạo trùng
	_, err = vfs.Mkdir(userID, "root", "Documents")
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi khi tạo trùng thư mục, nhưng không có lỗi")
	}

	// 2. EnsureDirectoryPath
	deepFolderID, err := vfs.EnsureDirectoryPath(userID, folder.ID, "Projects/SupportFlast/Engine")
	if err != nil {
		t.Fatalf("EnsureDirectoryPath thất bại: %v", err)
	}
	if deepFolderID == "" || deepFolderID == folder.ID {
		t.Fatalf("Kỳ vọng deepFolderID hợp lệ, nhận được: %s", deepFolderID)
	}

	// 3. ListDirectory
	items, err := vfs.ListDirectory(userID, "root")
	if err != nil {
		t.Fatalf("ListDirectory thất bại: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("Kỳ vọng có ít nhất 1 thư mục trong root")
	}

	// 4. GetFileByPath
	f, err := vfs.GetFileByPath("/Documents")
	if err != nil || f == nil {
		t.Fatalf("GetFileByPath('/Documents') thất bại: %v", err)
	}

	// 5. Rename
	err = vfs.RenameFileOrFolder(folder.ID, "MyDocuments")
	if err != nil {
		t.Fatalf("RenameFileOrFolder thất bại: %v", err)
	}
}

func TestVFS_SoftDeleteAndRestore(t *testing.T) {
	vfs, db, cleanup := setupTestVFS(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user_admin"

	folder, err := vfs.Mkdir(userID, "root", "TrashFolder")
	if err != nil {
		t.Fatalf("Mkdir thất bại: %v", err)
	}

	// Soft delete
	err = vfs.SoftDeleteFileOrFolder(ctx, folder.ID)
	if err != nil {
		t.Fatalf("SoftDeleteFileOrFolder thất bại: %v", err)
	}

	f, err := db.GetVirtualFile(folder.ID)
	if err != nil || f == nil || !f.IsTrashed {
		t.Fatalf("Kỳ vọng file có trạng thái IsTrashed = true")
	}

	// Restore
	err = vfs.RestoreFileOrFolder(ctx, folder.ID)
	if err != nil {
		t.Fatalf("RestoreFileOrFolder thất bại: %v", err)
	}

	f, err = db.GetVirtualFile(folder.ID)
	if err != nil || f == nil || f.IsTrashed {
		t.Fatalf("Kỳ vọng file có trạng thái IsTrashed = false sau khi restore")
	}
}

func TestVFS_StrictExcludedAccountsNoAlloc(t *testing.T) {
	vfs, db, cleanup := setupTestVFS(t)
	defer cleanup()

	// 3 tài khoản bị cấm
	_ = db.SaveAccount(&models.Account{
		ID:              "acc_banned_1",
		Email:           "duongmanhhung9900@gmail.com",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	})
	_ = db.SaveAccount(&models.Account{
		ID:              "acc_banned_2",
		Email:           "duongmanhhunghospitol@gmail.com",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	})
	_ = db.SaveAccount(&models.Account{
		ID:              "acc_banned_3",
		Email:           "phephabaylac@gmail.com",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	})

	// Khi chỉ có 3 tài khoản này, PickAccount TUYỆT ĐỐI không được chọn bất kỳ ai trong số họ!
	_, err := vfs.PickAccount("least_used", 1024*1024)
	if err == nil {
		t.Fatalf("Kỳ vọng PickAccount trả về lỗi vì chỉ có 3 tài khoản bị cấm, nhưng lại trả về thành công!")
	}

	_, err = vfs.SelectAccountForChunk("round_robin", 1024*1024)
	if err == nil {
		t.Fatalf("Kỳ vọng SelectAccountForChunk trả về lỗi vì chỉ có 3 tài khoản bị cấm, nhưng lại trả về thành công!")
	}
}
