package vfs

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
)

// TestExcludedUploadAccounts kiểm tra tuyệt đối loại trừ 3 tài khoản theo yêu cầu
func TestExcludedUploadAccounts(t *testing.T) {
	bannedAccounts := []*models.Account{
		{ID: "acc_1", Email: "duongmanhhung9900@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_2", Email: "duongmanhhunghospitol@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_2_alias", Email: "duongmanhhunghospital@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_3", Email: "phephabaylac@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_18cf4b1b8adc5be4", Email: "other_email_1@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_18d9741a9a288708", Email: "other_email_2@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
		{ID: "acc_18cf4b57bb6f3d58", Email: "other_email_3@gmail.com", Status: "active", FreeQuotaBytes: 100 * 1024 * 1024 * 1024},
	}

	for _, acc := range bannedAccounts {
		if !IsExcludedUploadAccount(acc) {
			t.Fatalf("Thất bại: Tài khoản cấm %s (ID: %s) không bị loại trừ!", acc.Email, acc.ID)
		}
	}

	allowedAccounts := []*models.Account{
		{ID: "acc_ok_1", Email: "valid_storage1@gmail.com", Status: "active", FreeQuotaBytes: 50 * 1024 * 1024 * 1024},
		{ID: "acc_ok_2", Email: "enterprise_pool2@gmail.com", Status: "active", FreeQuotaBytes: 50 * 1024 * 1024 * 1024},
	}

	for _, acc := range allowedAccounts {
		if IsExcludedUploadAccount(acc) {
			t.Fatalf("Thất bại: Tài khoản hợp lệ %s lại bị loại trừ nhầm!", acc.Email)
		}
	}
}

// TestRateLimitDetection kiểm tra phát hiện lỗi Rate-Limit từ Google Drive API
func TestRateLimitDetection(t *testing.T) {
	rateLimitErrors := []error{
		errors.New("googleapi: got HTTP response code 429 with body: Rate Limit Exceeded"),
		errors.New("userRateLimitExceeded: User rate limit exceeded"),
		errors.New("quotaExceeded: The project quota has been exceeded"),
		errors.New("dailyLimitExceeded: Daily limit for account has been reached"),
		errors.New("Too Many Requests"),
		errors.New("rpc error: code = ResourceExhausted desc = Rate limit exceeded"),
	}

	for _, err := range rateLimitErrors {
		if !IsRateLimitError(err) {
			t.Fatalf("Thất bại: Lỗi '%v' đáng lẽ phải được nhận diện là Rate-Limit!", err)
		}
	}

	normalErrors := []error{
		errors.New("file not found: 404"),
		errors.New("context deadline exceeded"),
		errors.New("connection reset by peer"),
		nil,
	}

	for _, err := range normalErrors {
		if IsRateLimitError(err) {
			t.Fatalf("Thất bại: Lỗi thường '%v' lại bị nhận diện nhầm là Rate-Limit!", err)
		}
	}
}

// TestRateLimitCooldown kiểm tra quản lý cooldown rate limit trong VFS
func TestRateLimitCooldown(t *testing.T) {
	vfs := &VFS{
		inFlightQuota: make(map[string]int64),
		rateLimits:    make(map[string]time.Time),
	}

	accID := "acc_test_rate_limit"
	if vfs.IsAccountRateLimited(accID) {
		t.Fatalf("Tài khoản chưa bị đánh dấu rate limit")
	}

	vfs.MarkAccountRateLimited(accID, 200*time.Millisecond)
	if !vfs.IsAccountRateLimited(accID) {
		t.Fatalf("Tài khoản phải đang bị rate limit trong cooldown")
	}

	// Đợi hết hạn cooldown
	time.Sleep(250 * time.Millisecond)
	if vfs.IsAccountRateLimited(accID) {
		t.Fatalf("Tài khoản phải tự động hết hạn cooldown")
	}
}

// TestPickAccountBannedAccountsExcludedAndRateLimitHandling kiểm tra hàm PickAccount / SelectAccountForChunk:
// 1. Tuyệt đối không chọn 3 tài khoản bị cấm dù chúng có dung lượng rất lớn.
// 2. Tự động chuyển đổi sang tài khoản khỏe mạnh khi tài khoản hiện tại bị rate-limit.
func TestPickAccountBannedAccountsExcludedAndRateLimitHandling(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_vfs_policy.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Khởi tạo DB test thất bại: %v", err)
	}
	defer db.Close()

	// 3 tài khoản bị cấm nhưng có dung lượng cực lớn (1000 GB trống)
	banned1 := &models.Account{
		ID:              "acc_banned_1",
		Email:           "duongmanhhung9900@gmail.com",
		Name:            "Admin Backup 1",
		Status:          "active",
		TotalQuotaBytes: 1000 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  1000 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	banned2 := &models.Account{
		ID:              "acc_banned_2",
		Email:           "duongmanhhunghospitol@gmail.com",
		Name:            "Admin Backup 2",
		Status:          "active",
		TotalQuotaBytes: 1000 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  1000 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	banned3 := &models.Account{
		ID:              "acc_banned_3",
		Email:           "phephabaylac@gmail.com",
		Name:            "Admin Backup 3",
		Status:          "active",
		TotalQuotaBytes: 1000 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  1000 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// 2 tài khoản lưu trữ thông thường (dung lượng nhỏ hơn: 10 GB và 20 GB)
	worker1 := &models.Account{
		ID:              "acc_worker_1",
		Email:           "worker1_storage@gmail.com",
		Name:            "Worker Storage 1",
		Status:          "active",
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	worker2 := &models.Account{
		ID:              "acc_worker_2",
		Email:           "worker2_storage@gmail.com",
		Name:            "Worker Storage 2",
		Status:          "active",
		TotalQuotaBytes: 30 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  20 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	_ = db.SaveAccount(banned1)
	_ = db.SaveAccount(banned2)
	_ = db.SaveAccount(banned3)
	_ = db.SaveAccount(worker1)
	_ = db.SaveAccount(worker2)

	vfs := NewVFS(db, nil)

	// Kiểm tra 20 lần chọn liên tiếp với các chiến lược khác nhau
	strategies := []string{"least_used", "waterfill", "round_robin", "balanced"}
	for _, strat := range strategies {
		for i := 0; i < 5; i++ {
			chosen, err := vfs.PickAccount(strat, 20*1024*1024)
			if err != nil {
				t.Fatalf("PickAccount '%s' thất bại: %v", strat, err)
			}
			if chosen.Email == "duongmanhhung9900@gmail.com" ||
				chosen.Email == "duongmanhhunghospitol@gmail.com" ||
				chosen.Email == "phephabaylac@gmail.com" {
				t.Fatalf("VI PHẠM NGHIÊM TRỌNG: PickAccount đã chọn tài khoản bị cấm: %s (ID: %s)", chosen.Email, chosen.ID)
			}
			if chosen.ID != "acc_worker_1" && chosen.ID != "acc_worker_2" {
				t.Fatalf("Tài khoản được chọn không hợp lệ: %s", chosen.ID)
			}
		}
	}

	// Kiểm tra cơ chế tự động né Rate-Limit:
	// Khi worker2 bị đánh dấu rate-limit, PickAccount phải tự động chỉ chọn worker1
	vfs.MarkAccountRateLimited("acc_worker_2", 1*time.Minute)
	for i := 0; i < 5; i++ {
		chosen, err := vfs.PickAccount("least_used", 20*1024*1024)
		if err != nil {
			t.Fatalf("PickAccount thất bại khi 1 tài khoản bị rate-limit: %v", err)
		}
		if chosen.ID != "acc_worker_1" {
			t.Fatalf("Kỳ vọng chỉ chọn acc_worker_1 vì acc_worker_2 đang bị rate-limit, nhưng lại chọn: %s", chosen.ID)
		}
	}

	// Khi worker1 cũng bị rate-limit: không còn tài khoản nào, PickAccount trả về lỗi rate-limit,
	// TUYỆT ĐỐI KHÔNG ĐƯỢC CHỌN 3 tài khoản bị cấm!
	vfs.MarkAccountRateLimited("acc_worker_1", 1*time.Minute)
	_, err = vfs.PickAccount("least_used", 20*1024*1024)
	if err == nil {
		t.Fatalf("Kỳ vọng báo lỗi khi tất cả worker bị rate limit, nhưng hàm lại không báo lỗi!")
	}
}
