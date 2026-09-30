package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
)

func TestDatabaseOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_cloudpool.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	if db.Path() != dbPath {
		t.Fatalf("Expected db.Path() == %s, got %s", dbPath, db.Path())
	}

	// 1. Test Default Admin User Created
	admin, err := db.GetUserByUsername("admin")
	if err != nil {
		t.Fatalf("Default admin user not found: %v", err)
	}
	if admin.Role != "admin" {
		t.Fatalf("Expected admin role, got %s", admin.Role)
	}

	// 2. Test Account Save & Auto-Encryption
	acc := &models.Account{
		ID:              "acc_test_1",
		Email:           "test_drive@gmail.com",
		Name:            "Test Drive",
		AuthType:        "oauth",
		CredentialsJSON: `{"client_id":"test_id"}`,
		TokenJSON:       `{"access_token":"ya29.test"}`,
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  15 * 1024 * 1024 * 1024,
		Status:          "active",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	loadedAcc, err := db.GetAccount("acc_test_1")
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if loadedAcc.CredentialsJSON != `{"client_id":"test_id"}` {
		t.Fatalf("Decrypted credentials mismatch: %s", loadedAcc.CredentialsJSON)
	}
	if loadedAcc.TokenJSON != `{"access_token":"ya29.test"}` {
		t.Fatalf("Decrypted token mismatch: %s", loadedAcc.TokenJSON)
	}

	// Test GetAccountByEmail
	loadedByEmail, err := db.GetAccountByEmail("test_drive@gmail.com")
	if err != nil {
		t.Fatalf("GetAccountByEmail failed: %v", err)
	}
	if loadedByEmail.ID != "acc_test_1" {
		t.Fatalf("Expected ID acc_test_1, got %s", loadedByEmail.ID)
	}
	if loadedByEmail.Email != "test_drive@gmail.com" {
		t.Fatalf("Expected email test_drive@gmail.com, got %s", loadedByEmail.Email)
	}

	// 3. Test Brute-Force Lockout
	for i := 1; i <= 4; i++ {
		fails, isLocked, err := db.RecordLoginFailure("admin")
		if err != nil || isLocked || fails != i {
			t.Fatalf("Failure recording incorrect on attempt %d: fails=%d, locked=%v", i, fails, isLocked)
		}
	}
	fails, isLocked, err := db.RecordLoginFailure("admin")
	if err != nil || !isLocked || fails != 5 {
		t.Fatalf("Expected lockout on 5th attempt: fails=%d, locked=%v", fails, isLocked)
	}

	if err := db.ResetLoginFailure("admin"); err != nil {
		t.Fatalf("ResetLoginFailure failed: %v", err)
	}
	userAfterReset, _ := db.GetUserByUsername("admin")
	if userAfterReset.FailedLoginCount != 0 || userAfterReset.LockedUntil != nil {
		t.Fatalf("Login failures not reset properly")
	}

	// 4. Test Login Session Logging
	sess := &models.LoginSession{
		ID:           "sess_test_1",
		UserID:       "user_admin",
		Username:     "admin",
		IPAddress:    "127.0.0.1",
		DeviceInfo:   "Windows PC Â· Chrome",
		LocationInfo: "Localhost",
		Status:       "SUCCESS",
		UserAgent:    "Mozilla/5.0",
		CreatedAt:    time.Now(),
	}
	if err := db.LogLoginSession(sess); err != nil {
		t.Fatalf("LogLoginSession failed: %v", err)
	}

	sessions, err := db.ListLoginSessions(10)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("ListLoginSessions failed: %v", err)
	}

	// 5. Test SQL Studio Query with Data Masking
	res, err := db.ExecuteRawSQL("SELECT * FROM accounts;")
	if err != nil {
		t.Fatalf("ExecuteRawSQL failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("Expected at least 1 account row")
	}

	// 6. Test Integrity Check & Optimization
	status, err := db.CheckDatabaseIntegrity()
	if err != nil || status != "ok" {
		t.Fatalf("Integrity check failed: status=%s, err=%v", status, err)
	}

	if err := db.OptimizeDatabase(); err != nil {
		t.Fatalf("OptimizeDatabase failed: %v", err)
	}

	// 7. Test Backup
	backupPath := filepath.Join(tempDir, "test_backup.db")
	if err := db.BackupDatabase(backupPath); err != nil {
		t.Fatalf("BackupDatabase failed: %v", err)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatalf("Backup file was not created: %s", backupPath)
	}
}

func TestRekeyDatabaseAndSettings(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_rekey.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Initial Settings with Default DB Passphrase
	oldPass := "cloudpool_secure_master_key_2026"
	initSettings := &models.Settings{
		MasterPassphrase:   oldPass,
		GoogleClientID:     "my_google_client_id.apps.googleusercontent.com",
		GoogleClientSecret: "GOCSPX-supersecret123",
	}
	if err := db.SaveSettings(initSettings); err != nil {
		t.Fatalf("SaveSettings failed: %v", err)
	}

	// Verify settings decrypted properly
	loadedSettings, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if loadedSettings.GoogleClientID != initSettings.GoogleClientID || loadedSettings.GoogleClientSecret != initSettings.GoogleClientSecret {
		t.Fatalf("Settings mismatch: clientID=%s, clientSecret=%s", loadedSettings.GoogleClientID, loadedSettings.GoogleClientSecret)
	}

	// 2. Save an account encrypted with old key
	acc := &models.Account{
		ID:              "acc_rekey_1",
		Email:           "secure_user@example.com",
		Name:            "Secure User",
		AuthType:        "oauth",
		CredentialsJSON: `{"token":"old_creds"}`,
		TokenJSON:       `{"access":"old_access"}`,
		TotalQuotaBytes: 1000,
		FreeQuotaBytes:  1000,
		Status:          "active",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	// 3. Perform Rekey
	newPass := "new_master_key_456"
	if err := db.RekeyDatabase(oldPass, newPass); err != nil {
		t.Fatalf("RekeyDatabase failed: %v", err)
	}

	// Verify account can be read and decrypted with new key
	loadedAcc, err := db.GetAccount("acc_rekey_1")
	if err != nil {
		t.Fatalf("GetAccount after rekey failed: %v", err)
	}
	if loadedAcc.Email != acc.Email || loadedAcc.CredentialsJSON != acc.CredentialsJSON {
		t.Fatalf("Account decrypted mismatch after rekey: email=%s, creds=%s", loadedAcc.Email, loadedAcc.CredentialsJSON)
	}

	// Verify settings after rekey
	rekeySettings, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings after rekey failed: %v", err)
	}
	if rekeySettings.GoogleClientID != initSettings.GoogleClientID || rekeySettings.GoogleClientSecret != initSettings.GoogleClientSecret {
		t.Fatalf("Settings decrypted mismatch after rekey: clientID=%s, clientSecret=%s", rekeySettings.GoogleClientID, rekeySettings.GoogleClientSecret)
	}

	// 4. Test SaveSettings with passphrase update directly
	newPass2 := "newer_master_key_789"
	updatedSettings := *rekeySettings
	updatedSettings.MasterPassphrase = newPass2
	updatedSettings.GoogleClientID = "updated_client_id"
	if err := db.SaveSettings(&updatedSettings); err != nil {
		t.Fatalf("SaveSettings with new passphrase failed: %v", err)
	}
	loadedSettings2, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings after SaveSettings failed: %v", err)
	}
	if loadedSettings2.GoogleClientID != "updated_client_id" || loadedSettings2.GoogleClientSecret != initSettings.GoogleClientSecret {
		t.Fatalf("Settings mismatch after SaveSettings: clientID=%s, clientSecret=%s", loadedSettings2.GoogleClientID, loadedSettings2.GoogleClientSecret)
	}

	// 5. Test Transaction Rollback on Decryption Failure
	// Manually corrupt an account's email with invalid ENC:
	_, err = db.db.Exec("UPDATE accounts SET email = 'ENC:ffffffffffffffffffffffffffffffff' WHERE id = 'acc_rekey_1'")
	if err != nil {
		t.Fatalf("Failed to inject corrupt ciphertext: %v", err)
	}
	// Attempt rekey should fail and rollback
	err = db.RekeyDatabase(newPass2, "should_fail_key")
	if err == nil {
		t.Fatalf("Expected RekeyDatabase to fail on corrupted ENC data, but it succeeded")
	}

	// Verify that rollback occurred and master_passphrase did not change to should_fail_key
	var currentPass string
	_ = db.db.QueryRow("SELECT value FROM settings WHERE key = 'master_passphrase'").Scan(&currentPass)
	if currentPass != newPass2 {
		t.Fatalf("Expected master_passphrase to remain %s after rollback, got %s", newPass2, currentPass)
	}
}

func TestResolveDBPathAndMigratedDB(t *testing.T) {
	resolved := ResolveDBPath()
	if resolved == "" {
		t.Fatalf("ResolveDBPath() returned empty string")
	}
	t.Logf("ResolveDBPath() successfully resolved to: %s", resolved)

	// Test default NewDB with empty string to ensure auto-resolution
	db, err := NewDB("")
	if err != nil {
		t.Fatalf("NewDB(\"\") failed: %v", err)
	}
	defer db.Close()

	// Verify WAL Mode
	var journalMode string
	if err := db.db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("Failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" && journalMode != "WAL" {
		t.Fatalf("Expected journal_mode WAL, got %s", journalMode)
	}

	// Verify Foreign Keys
	var foreignKeys int
	if err := db.db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("Failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("Expected foreign_keys 1, got %d", foreignKeys)
	}

	// Verify required tables exist
	requiredTables := []string{"accounts", "users", "virtual_files", "file_chunks", "activity_logs", "login_sessions", "settings", "file_access_otps", "file_access_requests", "public_shares"}
	for _, tbl := range requiredTables {
		var count int
		err := db.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
		if err != nil || count == 0 {
			t.Fatalf("Required table %s not found in migrated database", tbl)
		}
	}
}

func TestCloudPool_JournalModeFallback(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_cloudpool_fallback.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra busy_timeout = 5000ms
	var timeout int
	if err := db.db.QueryRow("PRAGMA busy_timeout;").Scan(&timeout); err != nil {
		t.Fatalf("Failed to query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("Expected busy_timeout=5000, got %d", timeout)
	}

	// 2. Kiểm tra chế độ WAL mặc định trên ổ cục bộ
	mode, err := ConfigureJournalModeWithFallback(db.db)
	if err != nil {
		t.Fatalf("ConfigureJournalModeWithFallback failed: %v", err)
	}
	if mode != "wal" && mode != "truncate" && mode != "delete" {
		t.Fatalf("Unexpected journal mode: %s", mode)
	}

	// 3. Kiểm tra mô phỏng Fallback sang TRUNCATE (Shared Hosting / Network Volumes)
	var truncateMode string
	if err := db.db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&truncateMode); err != nil {
		t.Fatalf("Failed to set TRUNCATE: %v", err)
	}
	applyStoragePragmas(db.db)

	var activeMode string
	_ = db.db.QueryRow("PRAGMA journal_mode;").Scan(&activeMode)
	if activeMode != "truncate" && activeMode != "delete" {
		t.Errorf("Expected TRUNCATE or DELETE, got %s", activeMode)
	}

	// 4. Kiểm tra mô phỏng Fallback tiếp sang DELETE
	var deleteMode string
	if err := db.db.QueryRow("PRAGMA journal_mode = DELETE;").Scan(&deleteMode); err != nil {
		t.Fatalf("Failed to set DELETE: %v", err)
	}
	applyStoragePragmas(db.db)

	_ = db.db.QueryRow("PRAGMA journal_mode;").Scan(&activeMode)
	if activeMode != "delete" {
		t.Errorf("Expected DELETE, got %s", activeMode)
	}

	// 5. Kiểm tra busy_timeout vẫn bảo lưu 5000ms
	var timeoutAfter int
	if err := db.db.QueryRow("PRAGMA busy_timeout;").Scan(&timeoutAfter); err != nil {
		t.Fatalf("Failed to query busy_timeout after fallback: %v", err)
	}
	if timeoutAfter != 5000 {
		t.Errorf("Expected busy_timeout=5000 after fallback, got %d", timeoutAfter)
	}
}

func TestTiDBAndMySQLCompatibilityHelpers(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_compat.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra driver mặc định là sqlite
	if db.Driver() != "sqlite" {
		t.Fatalf("Expected driver 'sqlite', got '%s'", db.Driver())
	}
	if !db.IsSQLite() {
		t.Fatalf("Expected IsSQLite() == true")
	}
	if db.IsMySQLOrTiDB() {
		t.Fatalf("Expected IsMySQLOrTiDB() == false")
	}

	// 2. Chuyển tạm thời driver sang tidb để kiểm tra các helper rẽ nhánh
	db.driver = "tidb"
	if db.Driver() != "tidb" {
		t.Fatalf("Expected driver 'tidb', got '%s'", db.Driver())
	}
	if db.IsSQLite() {
		t.Fatalf("Expected IsSQLite() == false when driver is tidb")
	}
	if !db.IsMySQLOrTiDB() {
		t.Fatalf("Expected IsMySQLOrTiDB() == true when driver is tidb")
	}

	// 3. Kiểm tra OptimizeDatabase và CheckDatabaseIntegrity trên chế độ TiDB/MySQL
	if err := db.OptimizeDatabase(); err != nil {
		t.Fatalf("OptimizeDatabase failed on TiDB mode: %v", err)
	}

	status, err := db.CheckDatabaseIntegrity()
	if err != nil || status != "ok" {
		t.Fatalf("CheckDatabaseIntegrity failed on TiDB mode: status=%s, err=%v", status, err)
	}

	// 4. Khôi phục lại driver sqlite để đóng DB an toàn
	db.driver = "sqlite"
}


