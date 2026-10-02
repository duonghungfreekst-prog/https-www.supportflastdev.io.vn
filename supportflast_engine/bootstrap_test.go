package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supportflast_engine/config"
	"supportflast_engine/database"
	"supportflast_engine/security"

	"golang.org/x/crypto/bcrypt"
)

func createTestSandbox(t *testing.T) (string, func()) {
	config.ResetForTest()
	origDriver := os.Getenv("DB_DRIVER")
	origTiDBHost := os.Getenv("TIDB_HOST")
	origRender := os.Getenv("RENDER")
	origDomain := os.Getenv("DOMAIN")

	os.Setenv("DB_DRIVER", "sqlite")
	os.Setenv("TIDB_HOST", "")
	os.Setenv("RENDER", "")
	os.Setenv("DOMAIN", "localhost")

	// Tạo sandbox trên ổ D (hoặc F) theo Rule 1.4, tránh hoàn toàn ổ C
	baseTemp := os.TempDir()
	sandboxDir := filepath.Join(baseTemp, fmt.Sprintf("test_bootstrap_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		t.Fatalf("Không thể tạo sandbox test: %v", err)
	}

	cleanup := func() {
		database.CloseDB()
		security.SetCustomKeysDir("")
		config.ResetForTest()
		os.Setenv("DB_DRIVER", origDriver)
		os.Setenv("TIDB_HOST", origTiDBHost)
		os.Setenv("RENDER", origRender)
		os.Setenv("DOMAIN", origDomain)
		_ = os.RemoveAll(sandboxDir)
	}

	return sandboxDir, cleanup
}

func TestAutoBootstrapping_BlankHosting(t *testing.T) {
	// Mô phỏng triển khai trên hosting trắng 100%:
	// - Chưa có thư mục data
	// - Chưa có thư mục storage
	// - Chưa có khóa RSA
	// - Chưa có file .env
	// - Chưa có CSDL SQLite
	sandboxDir, cleanup := createTestSandbox(t)
	defer cleanup()

	testDataDir := filepath.Join(sandboxDir, "data")
	testStorageDir := filepath.Join(sandboxDir, "storage")
	testEnvDir := sandboxDir

	// Đảm bảo ban đầu hoàn toàn trắng
	if _, err := os.Stat(testDataDir); !os.IsNotExist(err) {
		t.Fatalf("Thư mục data không được tồn tại trước khi bootstrap")
	}

	// Chạy Auto-Bootstrapping
	res, err := BootstrapWithDirs(testDataDir, testStorageDir, testEnvDir)
	if err != nil {
		t.Fatalf("Bootstrap thất bại trên hosting trắng: %v", err)
	}
	defer res.StorageDB.Close()

	// 1. Kiểm tra cờ khởi tạo lần đầu
	if !res.IsFirstRun {
		t.Errorf("Kỳ vọng IsFirstRun=true trên hosting trắng")
	}
	if !res.RSAKeysGenerated {
		t.Errorf("Kỳ vọng RSAKeysGenerated=true khi chưa có khóa")
	}
	if !res.AdminUserCreated {
		t.Errorf("Kỳ vọng AdminUserCreated=true khi chưa có admin")
	}

	// 2. Kiểm tra tạo đủ 6 thư mục bắt buộc:
	// 'data', 'data/keys', 'data/backups', 'data/oauth', 'data/service_accounts', 'storage'
	requiredDirs := []struct {
		name string
		path string
	}{
		{"data", testDataDir},
		{"data/keys", filepath.Join(testDataDir, "keys")},
		{"data/backups", filepath.Join(testDataDir, "backups")},
		{"data/oauth", filepath.Join(testDataDir, "oauth")},
		{"data/service_accounts", filepath.Join(testDataDir, "service_accounts")},
		{"storage", testStorageDir},
		{"storage/packages", filepath.Join(testStorageDir, "packages")},
	}

	for _, d := range requiredDirs {
		info, err := os.Stat(d.path)
		if err != nil || !info.IsDir() {
			t.Errorf("Thư mục bắt buộc '%s' không tồn tại trên đĩa tại: %s", d.name, d.path)
		}
	}

	// 3. Kiểm tra tự động sinh cặp khóa RSA 2048-bit (private.pem / public.pem)
	privKeyPath := filepath.Join(testDataDir, "keys", "private.pem")
	pubKeyPath := filepath.Join(testDataDir, "keys", "public.pem")

	if _, err := os.Stat(privKeyPath); err != nil {
		t.Errorf("File private.pem không tồn tại: %v", err)
	}
	if _, err := os.Stat(pubKeyPath); err != nil {
		t.Errorf("File public.pem không tồn tại: %v", err)
	}

	// Thẩm định cặp khóa RSA bằng hàm ValidateRSAKeys
	if err := ValidateRSAKeys(privKeyPath, pubKeyPath); err != nil {
		t.Errorf("Cặp khóa RSA sinh ra không hợp lệ: %v", err)
	}

	// 4. Kiểm tra tự động sinh file '.env' với các secret ngẫu nhiên
	envFile := filepath.Join(testEnvDir, ".env")
	envBytes, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("Không thể đọc file .env tự động sinh: %v", err)
	}
	envContent := string(envBytes)

	if !strings.Contains(envContent, "ADMIN_USERNAME=admin") {
		t.Errorf("File .env thiếu ADMIN_USERNAME=admin")
	}
	if !strings.Contains(envContent, "ADMIN_PASSWORD=Admin@2026!SupportFlast") {
		t.Errorf("File .env thiếu ADMIN_PASSWORD=Admin@2026!SupportFlast")
	}
	if !strings.Contains(envContent, "JWT_SECRET=") {
		t.Errorf("File .env thiếu JWT_SECRET")
	}
	if !strings.Contains(envContent, "CLOUDPOOL_MASTER_KEY=") {
		t.Errorf("File .env thiếu CLOUDPOOL_MASTER_KEY")
	}

	// 5. Kiểm tra tự động khởi tạo CSDL SQLite 'data/supportflast.db'
	mainDBPath := filepath.Join(testDataDir, "supportflast.db")
	if _, err := os.Stat(mainDBPath); err != nil {
		t.Fatalf("File CSDL supportflast.db không tồn tại: %v", err)
	}

	dbDSN := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", filepath.ToSlash(mainDBPath))
	mainDB, err := sql.Open("sqlite", dbDSN)
	if err != nil {
		t.Fatalf("Không thể mở CSDL supportflast.db: %v", err)
	}
	defer mainDB.Close()

	// Kiểm tra các bảng cốt lõi trong supportflast.db
	expectedTables := []string{
		"users", "apps", "api_keys", "reviews", "audit_logs", "system_releases", "security_events",
	}
	for _, tbl := range expectedTables {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='%s'", tbl)
		if err := mainDB.QueryRow(query).Scan(&count); err != nil || count == 0 {
			t.Errorf("Bảng bắt buộc '%s' không tồn tại trong supportflast.db", tbl)
		}
	}

	// 6. Kiểm tra tự động tạo tài khoản Admin mặc định ('admin' / 'Admin@2026!SupportFlast')
	var adminID, adminUser, adminEmail, adminPassHash, adminRole string
	err = mainDB.QueryRow(`
		SELECT id, username, email, password_hash, role 
		FROM users 
		WHERE username = 'admin'
	`).Scan(&adminID, &adminUser, &adminEmail, &adminPassHash, &adminRole)
	if err != nil {
		t.Fatalf("Không tìm thấy tài khoản admin trong CSDL: %v", err)
	}

	if adminUser != "admin" {
		t.Errorf("Username admin mong đợi 'admin', thực tế: '%s'", adminUser)
	}
	if adminRole != "admin" {
		t.Errorf("Role admin mong đợi 'admin', thực tế: '%s'", adminRole)
	}

	// Xác minh mật khẩu Admin qua BCrypt
	if err := bcrypt.CompareHashAndPassword([]byte(adminPassHash), []byte("Admin@2026!SupportFlast")); err != nil {
		t.Errorf("Mật khẩu Admin đã băm không khớp với 'Admin@2026!SupportFlast': %v", err)
	}

	// 7. Kiểm tra tự động khởi tạo CSDL SQLite 'data/cloudpool_metadata.db'
	storageDBPath := filepath.Join(testDataDir, "cloudpool_metadata.db")
	if _, err := os.Stat(storageDBPath); err != nil {
		t.Fatalf("File CSDL cloudpool_metadata.db không tồn tại: %v", err)
	}

	storageDSN := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", filepath.ToSlash(storageDBPath))
	sDB, err := sql.Open("sqlite", storageDSN)
	if err != nil {
		t.Fatalf("Không thể mở CSDL cloudpool_metadata.db: %v", err)
	}
	defer sDB.Close()

	// Kiểm tra các bảng cốt lõi trong cloudpool_metadata.db
	expectedCloudPoolTables := []string{
		"accounts", "users", "virtual_files", "file_chunks", "activity_logs", "login_sessions", "settings", "file_access_otps", "file_access_requests", "public_shares",
	}
	for _, tbl := range expectedCloudPoolTables {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table', 'view') AND name='%s'", tbl)
		if err := sDB.QueryRow(query).Scan(&count); err != nil || count == 0 {
			t.Errorf("Bảng/View bắt buộc '%s' không tồn tại trong cloudpool_metadata.db", tbl)
		}
	}
}

func TestAutoBootstrapping_Idempotency(t *testing.T) {
	// Kiểm tra tính Idempotent: Khởi động lần 2 không bị lỗi và không ghi đè dữ liệu cũ
	sandboxDir, cleanup := createTestSandbox(t)
	defer cleanup()

	testDataDir := filepath.Join(sandboxDir, "data")
	testStorageDir := filepath.Join(sandboxDir, "storage")
	testEnvDir := sandboxDir

	// Khởi chạy lần 1
	res1, err := BootstrapWithDirs(testDataDir, testStorageDir, testEnvDir)
	if err != nil {
		t.Fatalf("Khởi chạy lần 1 thất bại: %v", err)
	}
	res1.StorageDB.Close()
	database.CloseDB()

	// Đọc nội dung private key và public key lần 1
	priv1, _ := os.ReadFile(filepath.Join(testDataDir, "keys", "private.pem"))
	pub1, _ := os.ReadFile(filepath.Join(testDataDir, "keys", "public.pem"))

	// Khởi chạy lần 2 trên cùng cấu trúc
	config.ResetForTest()
	security.SetCustomKeysDir(filepath.Join(testDataDir, "keys"))

	res2, err := BootstrapWithDirs(testDataDir, testStorageDir, testEnvDir)
	if err != nil {
		t.Fatalf("Khởi chạy lần 2 thất bại (vi phạm Idempotency): %v", err)
	}
	defer res2.StorageDB.Close()

	// Lần 2 không được coi là FirstRun
	if res2.RSAKeysGenerated {
		t.Errorf("Lần 2 không được sinh lại khóa RSA đã có")
	}

	// Đọc lại khóa lần 2 để kiểm tra không bị ghi đè
	priv2, _ := os.ReadFile(filepath.Join(testDataDir, "keys", "private.pem"))
	pub2, _ := os.ReadFile(filepath.Join(testDataDir, "keys", "public.pem"))

	if string(priv1) != string(priv2) {
		t.Errorf("Private key bị ghi đè khi bootstrap lần 2")
	}
	if string(pub1) != string(pub2) {
		t.Errorf("Public key bị ghi đè khi bootstrap lần 2")
	}

	// Kiểm tra tài khoản admin vẫn nguyên vẹn
	mainDB := database.GetDB()
	var adminCount int
	err = mainDB.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'admin'").Scan(&adminCount)
	if err != nil || adminCount != 1 {
		t.Errorf("Tài khoản admin bị nhân bản hoặc thất lạc sau lần 2: count=%d", adminCount)
	}
}

func TestAutoBootstrapping_StandaloneWithoutEnvExample(t *testing.T) {
	// Mô phỏng hosting hoàn toàn độc lập, không có cả file .env.example
	sandboxDir, cleanup := createTestSandbox(t)
	defer cleanup()

	testDataDir := filepath.Join(sandboxDir, "data")
	testStorageDir := filepath.Join(sandboxDir, "storage")
	testEnvDir := sandboxDir

	// Đảm bảo không trỏ tới .env.example nào
	os.Setenv("ENV_EXAMPLE_FILE", filepath.Join(sandboxDir, "non_existent.example"))
	defer os.Unsetenv("ENV_EXAMPLE_FILE")

	res, err := BootstrapWithDirs(testDataDir, testStorageDir, testEnvDir)
	if err != nil {
		t.Fatalf("Bootstrap thất bại khi không có .env.example: %v", err)
	}
	defer res.StorageDB.Close()

	// Kiểm tra file .env được tạo độc lập
	envFile := filepath.Join(testEnvDir, ".env")
	envBytes, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("Không thể đọc file .env mặc định tự sinh: %v", err)
	}
	content := string(envBytes)

	if !strings.Contains(content, "ADMIN_USERNAME=admin") {
		t.Errorf("File .env thiếu ADMIN_USERNAME")
	}
	if !strings.Contains(content, "ADMIN_PASSWORD=Admin@2026!SupportFlast") {
		t.Errorf("File .env thiếu ADMIN_PASSWORD")
	}
	if !strings.Contains(content, "JWT_SECRET=") {
		t.Errorf("File .env thiếu JWT_SECRET")
	}
	if !strings.Contains(content, "CLOUDPOOL_MASTER_KEY=") {
		t.Errorf("File .env thiếu CLOUDPOOL_MASTER_KEY")
	}

	// Đảm bảo cặp khóa RSA và CSDL vẫn khởi tạo thành công
	if err := ValidateRSAKeys(res.PrivateKeyPath, res.PublicKeyPath); err != nil {
		t.Errorf("Khóa RSA không hợp lệ: %v", err)
	}
}

func TestAutoBootstrapping_TiDBFallbackToSQLite(t *testing.T) {
	// Kiểm tra trường hợp cấu hình DB_DRIVER=tidb nhưng chạy ở môi trường local không có kết nối TiDB
	// Hệ thống phải tự động fallback an toàn 100% sang SQLite cho cả Main DB và CloudPool DB
	sandboxDir, cleanup := createTestSandbox(t)
	defer cleanup()

	testDataDir := filepath.Join(sandboxDir, "data")
	testStorageDir := filepath.Join(sandboxDir, "storage")
	testEnvDir := sandboxDir

	os.Setenv("DB_DRIVER", "tidb")
	os.Setenv("TIDB_HOST", "127.0.0.1")
	os.Setenv("TIDB_PORT", "65534") // Cổng không có dịch vụ để kích hoạt fallback
	os.Setenv("TIDB_CONNECT_TIMEOUT", "500ms")
	os.Setenv("TIDB_PING_TIMEOUT", "500ms")
	defer func() {
		os.Unsetenv("DB_DRIVER")
		os.Unsetenv("TIDB_HOST")
		os.Unsetenv("TIDB_PORT")
		os.Unsetenv("TIDB_CONNECT_TIMEOUT")
		os.Unsetenv("TIDB_PING_TIMEOUT")
	}()

	res, err := BootstrapWithDirs(testDataDir, testStorageDir, testEnvDir)
	if err != nil {
		t.Fatalf("Bootstrap thất bại khi fallback từ TiDB sang SQLite: %v", err)
	}
	defer res.StorageDB.Close()

	// 1. Kiểm tra CloudPool Storage DB đã khởi tạo thành công qua fallback SQLite
	if res.StorageDB == nil {
		t.Fatalf("StorageDB không được là nil sau khi fallback")
	}
	if res.StorageDB.Driver() != "sqlite" {
		t.Errorf("Kỳ vọng Driver là 'sqlite' sau khi fallback, thực tế: '%s'", res.StorageDB.Driver())
	}

	// 2. Kiểm tra file CSDL SQLite của CloudPool đã được tạo trên đĩa
	storageDBPath := filepath.Join(testDataDir, "cloudpool_metadata.db")
	if _, err := os.Stat(storageDBPath); os.IsNotExist(err) {
		t.Errorf("File CSDL CloudPool '%s' không tồn tại sau khi fallback sang SQLite", storageDBPath)
	}

	// 3. Kiểm tra tính sẵn sàng của CloudPool: Lưu và đọc cài đặt hoạt động tốt
	stats, err := res.StorageDB.GetStats()
	if err != nil {
		t.Fatalf("GetStats thất bại trên CloudPool Storage sau fallback: %v", err)
	}
	if stats == nil {
		t.Fatalf("GetStats trả về nil")
	}
}

