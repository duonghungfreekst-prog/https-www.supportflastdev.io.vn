package gdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
)

func setupTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_gdrive_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	_ = os.MkdirAll(testDir, 0755)

	dbPath := filepath.Join(testDir, "test_metadata.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
	}

	return db, cleanup
}

func TestManager_InitializationAndDirectories(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr := NewManager(db)
	if mgr == nil {
		t.Fatal("NewManager trả về nil")
	}

	// Kiểm tra đường dẫn thư mục mặc định
	dataDir := mgr.GetDataDir()
	if dataDir != DefaultDataDir {
		t.Errorf("Kỳ vọng dataDir = %s, thực tế = %s", DefaultDataDir, dataDir)
	}

	oauthDir := mgr.GetOAuthDir()
	expectedOAuthDir := filepath.Join(DefaultDataDir, "oauth")
	if oauthDir != expectedOAuthDir {
		t.Errorf("Kỳ vọng oauthDir = %s, thực tế = %s", expectedOAuthDir, oauthDir)
	}

	saDir := mgr.GetServiceAccountsDir()
	expectedSADir := filepath.Join(DefaultDataDir, "service_accounts")
	if saDir != expectedSADir {
		t.Errorf("Kỳ vọng serviceAccountsDir = %s, thực tế = %s", expectedSADir, saDir)
	}

	// Đảm bảo thư mục thực sự tồn tại
	if _, err := os.Stat(oauthDir); os.IsNotExist(err) {
		t.Errorf("Thư mục OAuth không tồn tại trên đĩa: %s", oauthDir)
	}
	if _, err := os.Stat(saDir); os.IsNotExist(err) {
		t.Errorf("Thư mục Service Account không tồn tại trên đĩa: %s", saDir)
	}
}

func TestManager_SelectAccountStrategies(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr := NewManager(db)

	// Tạo 3 tài khoản với dung lượng khác nhau
	// Acc 1: 10GB free
	acc1 := &models.Account{
		ID:              "acc_01",
		Email:           "drive1@example.com",
		Name:            "Drive Alpha",
		AuthType:        "oauth",
		RootFolderID:    "root_01",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  90 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024, // 10GB
		Status:          "active",
		CreatedAt:       time.Now().Add(-3 * time.Hour),
	}
	// Acc 2: 80GB free
	acc2 := &models.Account{
		ID:              "acc_02",
		Email:           "drive2@example.com",
		Name:            "Drive Beta",
		AuthType:        "oauth",
		RootFolderID:    "root_02",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  20 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  80 * 1024 * 1024 * 1024, // 80GB
		Status:          "active",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
	}
	// Acc 3: 50GB free
	acc3 := &models.Account{
		ID:              "acc_03",
		Email:           "drive3@example.com",
		Name:            "Drive Gamma",
		AuthType:        "service_account",
		RootFolderID:    "root_03",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  50 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  50 * 1024 * 1024 * 1024, // 50GB
		Status:          "active",
		CreatedAt:       time.Now().Add(-1 * time.Hour),
	}

	if err := db.SaveAccount(acc1); err != nil {
		t.Fatalf("Không thể lưu acc1: %v", err)
	}
	if err := db.SaveAccount(acc2); err != nil {
		t.Fatalf("Không thể lưu acc2: %v", err)
	}
	if err := db.SaveAccount(acc3); err != nil {
		t.Fatalf("Không thể lưu acc3: %v", err)
	}

	ctx := context.Background()

	t.Run("LeastUsed Strategy", func(t *testing.T) {
		// Yêu cầu 1GB -> Acc2 có 80GB, Acc3 có 50GB
		// Vì Acc2 (80GB) và Acc3 (50GB) đều dồi dào dung lượng (> 5GB và > 80/5 = 16GB),
		// cơ chế luân phiên least_used sẽ phân bổ tuần tự giữa Acc2 và Acc3
		chosen1, err := mgr.SelectAccount(ctx, StrategyLeastUsed, 1024*1024*1024)
		if err != nil {
			t.Fatalf("Lỗi chọn account least_used: %v", err)
		}
		if chosen1.ID != "acc_02" && chosen1.ID != "acc_03" {
			t.Errorf("Least_used phải chọn acc_02 hoặc acc_03, thực tế = %s", chosen1.ID)
		}

		// Nếu yêu cầu 60GB -> chỉ có acc_02 đủ điều kiện (80GB)
		chosenBig, err := mgr.SelectAccount(ctx, StrategyLeastUsed, 60*1024*1024*1024)
		if err != nil {
			t.Fatalf("Lỗi chọn account với 60GB: %v", err)
		}
		if chosenBig.ID != "acc_02" {
			t.Errorf("Kỳ vọng acc_02 cho yêu cầu 60GB, thực tế = %s", chosenBig.ID)
		}
	})

	t.Run("Waterfill Strategy", func(t *testing.T) {
		// Waterfill chọn tài khoản đầu tiên còn đủ dung lượng
		chosen, err := mgr.SelectAccount(ctx, StrategyWaterfill, 5*1024*1024*1024)
		if err != nil {
			t.Fatalf("Lỗi chọn account waterfill: %v", err)
		}
		if chosen == nil {
			t.Fatal("Kỳ vọng account hợp lệ, nhận nil")
		}
		if chosen.FreeQuotaBytes < 5*1024*1024*1024 {
			t.Errorf("Account được chọn không đủ dung lượng yêu cầu: %d", chosen.FreeQuotaBytes)
		}
	})

	t.Run("RoundRobin Strategy", func(t *testing.T) {
		// Kiểm tra round-robin hỗ trợ cả "round_robin" và "round-robin"
		picked := make(map[string]int)
		for i := 0; i < 6; i++ {
			strategy := StrategyRoundRobin
			if i%2 == 1 {
				strategy = StrategyRoundRobin2 // "round-robin"
			}
			chosen, err := mgr.SelectAccount(ctx, strategy, 5*1024*1024*1024)
			if err != nil {
				t.Fatalf("Lỗi chọn account round_robin: %v", err)
			}
			picked[chosen.ID]++
		}

		// Với 3 tài khoản và 6 lần lấy, mỗi tài khoản phải được chọn đúng 2 lần
		for _, accID := range []string{"acc_01", "acc_02", "acc_03"} {
			if picked[accID] != 2 {
				t.Errorf("RoundRobin phân bổ không đều: accID=%s được chọn %d lần (kỳ vọng 2)", accID, picked[accID])
			}
		}
	})

	t.Run("Exclude Accounts & Out of Capacity", func(t *testing.T) {
		// Loại trừ acc_02 (80GB) và acc_03 (50GB) -> chỉ còn acc_01 (10GB)
		chosen, err := mgr.SelectAccount(ctx, StrategyLeastUsed, 5*1024*1024*1024, "acc_02", "acc_03")
		if err != nil {
			t.Fatalf("Lỗi chọn account khi loại trừ: %v", err)
		}
		if chosen.ID != "acc_01" {
			t.Errorf("Kỳ vọng acc_01, thực tế = %s", chosen.ID)
		}

		// Yêu cầu 15GB khi loại trừ acc_02 và acc_03 -> acc_01 chỉ có 10GB -> phải báo lỗi
		_, err = mgr.SelectAccount(ctx, StrategyLeastUsed, 15*1024*1024*1024, "acc_02", "acc_03")
		if err == nil {
			t.Error("Kỳ vọng báo lỗi khi không còn tài khoản nào đủ dung lượng")
		}
	})
}

func TestManager_OAuthClientLoadingAndURL(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr := NewManager(db)

	// Tạo thư mục tạm để test OAuth credentials
	tempOAuthDir := filepath.Join(mgr.GetDataDir(), "test_oauth_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	_ = os.MkdirAll(tempOAuthDir, 0755)
	defer os.RemoveAll(tempOAuthDir)

	credFile := filepath.Join(tempOAuthDir, "credentials.json")
	credContent := `{
		"installed": {
			"client_id": "test-client-id-12345.apps.googleusercontent.com",
			"client_secret": "test-client-secret-xyz",
			"redirect_uris": ["http://localhost:8080/api/storage/oauth/callback"]
		}
	}`
	if err := os.WriteFile(credFile, []byte(credContent), 0644); err != nil {
		t.Fatalf("Không thể tạo file credentials test: %v", err)
	}

	cfg, err := mgr.LoadOAuthClientConfig(credFile)
	if err != nil {
		t.Fatalf("LoadOAuthClientConfig thất bại: %v", err)
	}

	if cfg.ClientID != "test-client-id-12345.apps.googleusercontent.com" {
		t.Errorf("Kỳ vọng ClientID = test-client-id-12345..., thực tế = %s", cfg.ClientID)
	}
	if cfg.ClientSecret != "test-client-secret-xyz" {
		t.Errorf("Kỳ vọng ClientSecret = test-client-secret-xyz, thực tế = %s", cfg.ClientSecret)
	}

	// Test GetOAuthURL
	authURL := mgr.GetOAuthURL(cfg.ClientID, cfg.ClientSecret, cfg.RedirectURL, "state_test_token")
	if authURL == "" {
		t.Fatal("GetOAuthURL trả về rỗng")
	}
	if !contains(authURL, "test-client-id-12345") {
		t.Errorf("OAuth URL thiếu client_id: %s", authURL)
	}
	if !contains(authURL, "state_test_token") {
		t.Errorf("OAuth URL thiếu state param: %s", authURL)
	}
	if !contains(authURL, "consent") {
		t.Errorf("OAuth URL thiếu prompt consent: %s", authURL)
	}
}

func TestManager_ServiceAccountsDirectoryScan(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr := NewManager(db)

	tempSADir := filepath.Join(mgr.GetDataDir(), "test_sa_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	_ = os.MkdirAll(tempSADir, 0755)
	defer os.RemoveAll(tempSADir)

	// Tạo 1 file không phải service account (file text)
	_ = os.WriteFile(filepath.Join(tempSADir, "notes.txt"), []byte("ghi chú"), 0644)

	// Tạo 1 file json nhưng không phải google service account
	_ = os.WriteFile(filepath.Join(tempSADir, "other.json"), []byte(`{"type": "app_config"}`), 0644)

	// Tạo 1 file service account hợp lệ về cú pháp JSON
	saJSON := map[string]interface{}{
		"type":                        "service_account",
		"project_id":                  "supportflast-cloud",
		"private_key_id":              "key123456",
		"private_key":                 "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQC...\n-----END PRIVATE KEY-----\n",
		"client_email":                "storage-bot@supportflast-cloud.iam.gserviceaccount.com",
		"client_id":                   "1029384756",
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
	}
	saBytes, _ := json.Marshal(saJSON)
	_ = os.WriteFile(filepath.Join(tempSADir, "sa_bot.json"), saBytes, 0644)

	// Khi gọi LoadServiceAccountsFromDir, file giả lập private key sẽ được đọc và thử nạp.
	// Do private_key giả lập, credentials parse sẽ báo lỗi xác thực RSA hợp lệ, nhưng hàm scan không được panic và bắt lỗi chính xác.
	loaded, errList := mgr.LoadServiceAccountsFromDir(tempSADir)
	if loaded != 0 {
		t.Errorf("Kỳ vọng 0 tài khoản thành công với RSA giả lập, nhận: %d", loaded)
	}
	if len(errList) == 0 {
		t.Error("Kỳ vọng bắt được lỗi định dạng key RSA giả lập")
	}
}

func contains(s, substr string) bool {
	return filepath.Clean(s) != "" && (len(s) >= len(substr)) && (s == substr || filepath.Base(s) != "" && (indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestManager_UploadExcludedAccounts(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr := NewManager(db)
	ctx := context.Background()

	// 1. Thêm 3 tài khoản cần né lưu trữ với dung lượng trống rất lớn (100GB mỗi tài khoản)
	excluded1 := &models.Account{
		ID:              "acc_ex_1",
		Email:           "duongmanhhung9900@gmail.com",
		Name:            "Hung Admin Backup",
		AuthType:        "oauth",
		RootFolderID:    "rf_1",
		TotalQuotaBytes: 200 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024, // 100GB
		Status:          "active",
		CreatedAt:       time.Now(),
	}
	excluded2 := &models.Account{
		ID:              "acc_ex_2",
		Email:           "duongmanhhunghospital@gmail.com",
		Name:            "Hospital Backup",
		AuthType:        "oauth",
		RootFolderID:    "rf_2",
		TotalQuotaBytes: 200 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024, // 100GB
		Status:          "active",
		CreatedAt:       time.Now(),
	}
	excluded3 := &models.Account{
		ID:              "acc_ex_3",
		Email:           "phephabaylac@gmail.com",
		Name:            "Special Storage",
		AuthType:        "oauth",
		RootFolderID:    "rf_3",
		TotalQuotaBytes: 200 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024, // 100GB
		Status:          "active",
		CreatedAt:       time.Now(),
	}
	// 2. Thêm 1 tài khoản thông thường chỉ có 10GB trống
	regular := &models.Account{
		ID:              "acc_regular",
		Email:           "normal_user_drive@gmail.com",
		Name:            "Normal Storage Drive",
		AuthType:        "oauth",
		RootFolderID:    "rf_normal",
		TotalQuotaBytes: 50 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  40 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024, // Chỉ 10GB
		Status:          "active",
		CreatedAt:       time.Now(),
	}

	for _, a := range []*models.Account{excluded1, excluded2, excluded3, regular} {
		if err := db.SaveAccount(a); err != nil {
			t.Fatalf("Lỗi tạo account test: %v", err)
		}
	}

	// 3. Khi chọn tài khoản tải lên 1GB:
	// Mặc dù 3 tài khoản bị né có 100GB trống (nhiều hơn nhiều so với 10GB của regular),
	// hệ thống BẮT BUỘC phải bỏ qua 3 tài khoản đó và chỉ chọn tài khoản regular!
	chosen, err := mgr.SelectAccount(ctx, StrategyLeastUsed, 1024*1024*1024)
	if err != nil {
		t.Fatalf("SelectAccount thất bại: %v", err)
	}
	if chosen.ID != "acc_regular" {
		t.Fatalf("Kỳ vọng chọn 'acc_regular' nhưng lại chọn '%s' (%s). Tính năng né lưu trữ thất bại!", chosen.ID, chosen.Email)
	}

	// 4. Nếu yêu cầu 20GB (vượt quá 10GB của regular, trong khi 3 tài khoản né vẫn có 100GB trống):
	// Hệ thống BẮT BUỘC phải báo lỗi không đủ dung lượng chứ TUYỆT ĐỐI KHÔNG ĐƯỢC xâm phạm vào 3 tài khoản né!
	_, errBig := mgr.SelectAccount(ctx, StrategyLeastUsed, 20*1024*1024*1024)
	if errBig == nil {
		t.Fatalf("Kỳ vọng báo lỗi hết dung lượng khả dụng khi không tính 3 tài khoản né, nhưng lại chọn được tài khoản!")
	}
}
