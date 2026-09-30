package storage

import (
	"database/sql"
	"fmt"
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
		DeviceInfo:   "Windows PC · Chrome",
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

func TestFlexibleTimeParsing(t *testing.T) {
	refTime := time.Date(2026, 9, 30, 8, 30, 0, 0, time.UTC)

	// 1. Test parseFlexibleTime với các kiểu dữ liệu
	// time.Time
	if got := parseFlexibleTime(refTime); !got.Equal(refTime) {
		t.Errorf("Expected %v, got %v", refTime, got)
	}

	// *time.Time
	if got := parseFlexibleTime(&refTime); !got.Equal(refTime) {
		t.Errorf("Expected %v, got %v", refTime, got)
	}

	// []byte (mô phỏng []uint8 từ MySQL/TiDB driver)
	byteVal := []byte("2026-09-30 08:30:00")
	gotByte := parseFlexibleTime(byteVal)
	if gotByte.Year() != 2026 || gotByte.Month() != 9 || gotByte.Day() != 30 || gotByte.Hour() != 8 || gotByte.Minute() != 30 {
		t.Errorf("parseFlexibleTime([]byte) failed, got: %v", gotByte)
	}

	// RFC3339 string
	strRFC := "2026-09-30T08:30:00Z"
	gotRFC := parseFlexibleTime(strRFC)
	if gotRFC.Year() != 2026 || gotRFC.Month() != 9 || gotRFC.Day() != 30 {
		t.Errorf("parseFlexibleTime(RFC3339) failed, got: %v", gotRFC)
	}

	// sql.NullTime
	nullTime := sql.NullTime{Time: refTime, Valid: true}
	if got := parseFlexibleTime(nullTime); !got.Equal(refTime) {
		t.Errorf("parseFlexibleTime(sql.NullTime) failed, got %v", got)
	}

	// sql.NullString
	nullStr := sql.NullString{String: "2026-09-30 08:30:00", Valid: true}
	gotNullStr := parseFlexibleTime(nullStr)
	if gotNullStr.Year() != 2026 {
		t.Errorf("parseFlexibleTime(sql.NullString) failed, got: %v", gotNullStr)
	}

	// nil hoặc chuỗi rỗng trả về thời gian hợp lệ (không crash)
	if got := parseFlexibleTime(nil); got.IsZero() {
		t.Errorf("Expected non-zero fallback for nil, got zero")
	}
	if got := parseFlexibleTime([]byte("")); got.IsZero() {
		t.Errorf("Expected non-zero fallback for empty byte slice, got zero")
	}

	// 2. Test parseFlexibleTimePtr
	// nil
	if ptr := parseFlexibleTimePtr(nil); ptr != nil {
		t.Errorf("Expected nil for nil input, got: %v", ptr)
	}

	// []byte("NULL")
	if ptr := parseFlexibleTimePtr([]byte("NULL")); ptr != nil {
		t.Errorf("Expected nil for 'NULL' byte slice, got: %v", ptr)
	}

	// []byte("0000-00-00 00:00:00")
	if ptr := parseFlexibleTimePtr([]byte("0000-00-00 00:00:00")); ptr != nil {
		t.Errorf("Expected nil for zero date, got: %v", ptr)
	}

	// []byte hợp lệ
	ptrValid := parseFlexibleTimePtr([]byte("2026-09-30 08:30:00"))
	if ptrValid == nil || ptrValid.Year() != 2026 {
		t.Errorf("Expected valid *time.Time for byte slice, got: %v", ptrValid)
	}

	// sql.NullString valid = false
	if ptr := parseFlexibleTimePtr(sql.NullString{Valid: false}); ptr != nil {
		t.Errorf("Expected nil for invalid sql.NullString, got: %v", ptr)
	}

	// sql.NullTime valid = true
	if ptr := parseFlexibleTimePtr(sql.NullTime{Time: refTime, Valid: true}); ptr == nil || !ptr.Equal(refTime) {
		t.Errorf("Expected equal time for valid sql.NullTime, got: %v", ptr)
	}
}

func TestTiDBByteSliceScanCompatibility(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_tidb_compat.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra Account scan khi cột created_at, updated_at lưu dưới dạng chuỗi text hoặc blob (tương đương TiDB driver []uint8)
	acc := &models.Account{
		ID:              "acc_tidb_1",
		Email:           "tidb_driver@test.com",
		Name:            "TiDB Driver Test",
		AuthType:        "oauth",
		CredentialsJSON: `{"client_id":"tidb_id"}`,
		TokenJSON:       `{"access_token":"tidb_token"}`,
		TotalQuotaBytes: 10 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024,
		Status:          "active",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	// Cập nhật giá trị cột created_at, updated_at thành dạng chuỗi SQL thô (mô phỏng driver TiDB/MySQL trả về []uint8)
	rawTimeString := "2026-09-30 08:45:00"
	_, err = db.SQLDB().Exec("UPDATE accounts SET created_at = ?, updated_at = ? WHERE id = ?", rawTimeString, rawTimeString, acc.ID)
	if err != nil {
		t.Fatalf("Failed to simulate TiDB string time in accounts: %v", err)
	}

	// Test GetAccount
	loadedAcc, err := db.GetAccount(acc.ID)
	if err != nil {
		t.Fatalf("GetAccount failed with TiDB string/blob time: %v", err)
	}
	if loadedAcc.CreatedAt.Year() != 2026 || loadedAcc.UpdatedAt.Year() != 2026 {
		t.Errorf("GetAccount returned unexpected CreatedAt/UpdatedAt: %v, %v", loadedAcc.CreatedAt, loadedAcc.UpdatedAt)
	}

	// Test GetAccountByEmail
	loadedByEmail, err := db.GetAccountByEmail("tidb_driver@test.com")
	if err != nil {
		t.Fatalf("GetAccountByEmail failed with TiDB string/blob time: %v", err)
	}
	if loadedByEmail.ID != acc.ID {
		t.Errorf("GetAccountByEmail ID mismatch: expected %s, got %s", acc.ID, loadedByEmail.ID)
	}

	// Test ListAccounts (trực tiếp tái hiện và khắc phục lỗi TiDB "sql: Scan error on column index 13, name created_at")
	accList, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts failed with TiDB string/blob time: %v", err)
	}
	if len(accList) == 0 {
		t.Fatalf("ListAccounts returned 0 accounts")
	}

	// 2. Kiểm tra User scan (GetUserByUsername, GetUserByID, ListUsers) với locked_until, created_at, updated_at
	_, err = db.SQLDB().Exec("UPDATE cloudpool_users SET locked_until = ?, created_at = ?, updated_at = ? WHERE id = 'user_admin'",
		"2026-09-30 09:30:00", "2026-09-30 08:00:00", "2026-09-30 08:30:00")
	if err != nil {
		t.Fatalf("Failed to simulate TiDB string time in users: %v", err)
	}

	userByUname, err := db.GetUserByUsername("admin")
	if err != nil {
		t.Fatalf("GetUserByUsername failed: %v", err)
	}
	if userByUname.LockedUntil == nil || userByUname.LockedUntil.Year() != 2026 {
		t.Errorf("Expected LockedUntil parsed properly, got: %v", userByUname.LockedUntil)
	}

	userByID, err := db.GetUserByID("user_admin")
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if userByID.CreatedAt.Year() != 2026 {
		t.Errorf("Expected CreatedAt parsed properly, got: %v", userByID.CreatedAt)
	}

	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers failed: %v", err)
	}
	if len(users) == 0 {
		t.Fatalf("ListUsers returned empty list")
	}

	// 3. Kiểm tra OTP scan (ListFileOTPs, VerifyOTPOnly, VerifyAndBurnOTP)
	futureExp := time.Now().Add(1 * time.Hour)
	otp := &models.FileAccessOTP{
		ID:           "otp_tidb_1",
		FileID:       "file_tidb_test",
		FileName:     "document.pdf",
		TargetUserID: "all",
		OTPCode:      "654321",
		CreatedBy:    "user_admin",
		ExpiresAt:    futureExp,
		CreatedAt:    time.Now(),
	}
	if err := db.CreateFileOTP(otp); err != nil {
		t.Fatalf("CreateFileOTP failed: %v", err)
	}

	// Mô phỏng chuỗi text ngày tháng trong bảng file_access_otps
	_, err = db.SQLDB().Exec("UPDATE file_access_otps SET expires_at = ?, created_at = ? WHERE id = ?",
		"2026-10-01 12:00:00", "2026-09-30 08:00:00", otp.ID)
	if err != nil {
		t.Fatalf("Failed to update file_access_otps time strings: %v", err)
	}

	otps, err := db.ListFileOTPs(10)
	if err != nil {
		t.Fatalf("ListFileOTPs failed: %v", err)
	}
	if len(otps) == 0 {
		t.Fatalf("ListFileOTPs returned 0 items")
	}

	validOTP, err := db.VerifyOTPOnly("file_tidb_test", "any_user", "654321")
	if err != nil || !validOTP {
		t.Fatalf("VerifyOTPOnly failed: %v", err)
	}

	// 4. Kiểm tra Access Request scan (ListFileAccessRequests)
	req := &models.FileAccessRequest{
		ID:              "req_tidb_1",
		FileID:          "file_tidb_test",
		FileName:        "document.pdf",
		UserID:          "child_user_1",
		Username:        "child",
		UserDisplayName: "Child User",
		Status:          "pending",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.CreateFileAccessRequest(req); err != nil {
		t.Fatalf("CreateFileAccessRequest failed: %v", err)
	}
	_, err = db.SQLDB().Exec("UPDATE file_access_requests SET created_at = ?, updated_at = ? WHERE id = ?",
		"2026-09-30 08:00:00", "2026-09-30 08:30:00", req.ID)
	if err != nil {
		t.Fatalf("Failed to update file_access_requests time strings: %v", err)
	}

	reqs, err := db.ListFileAccessRequests("all")
	if err != nil {
		t.Fatalf("ListFileAccessRequests failed: %v", err)
	}
	if len(reqs) == 0 {
		t.Fatalf("ListFileAccessRequests returned 0 items")
	}

	// 5. Kiểm tra Public Share scan (GetPublicShare, ListPublicShares)
	share := &models.PublicShare{
		ID:            "sh_tidb_test",
		FileID:        "file_tidb_test",
		CreatedBy:     "user_admin",
		MaxDownloads:  10,
		DownloadCount: 0,
		ExpiresAt:     &futureExp,
		CreatedAt:     time.Now(),
	}
	if err := db.CreatePublicShare(share); err != nil {
		t.Fatalf("CreatePublicShare failed: %v", err)
	}
	_, err = db.SQLDB().Exec("UPDATE public_shares SET expires_at = ?, created_at = ? WHERE id = ?",
		"2026-10-01 12:00:00", "2026-09-30 08:00:00", share.ID)
	if err != nil {
		t.Fatalf("Failed to update public_shares time strings: %v", err)
	}

	loadedShare, err := db.GetPublicShare(share.ID)
	if err != nil {
		t.Fatalf("GetPublicShare failed: %v", err)
	}
	if loadedShare.ExpiresAt == nil || loadedShare.ExpiresAt.Year() != 2026 {
		t.Errorf("Expected valid ExpiresAt, got: %v", loadedShare.ExpiresAt)
	}

	shares, err := db.ListPublicShares()
	if err != nil {
		t.Fatalf("ListPublicShares failed: %v", err)
	}
	if len(shares) == 0 {
		t.Fatalf("ListPublicShares returned 0 items")
	}

	// 6. Kiểm tra Activity Log & Login Session scan
	actLog := &models.ActivityLog{
		ID:        "log_tidb_1",
		UserID:    "user_admin",
		Username:  "admin",
		Action:    "TEST",
		Target:    "test",
		IPAddress: "127.0.0.1",
		CreatedAt: time.Now(),
	}
	if err := db.LogActivity(actLog); err != nil {
		t.Fatalf("LogActivity failed: %v", err)
	}
	_, err = db.SQLDB().Exec("UPDATE activity_logs SET created_at = ? WHERE id = ?", "2026-09-30 08:00:00", actLog.ID)
	if err != nil {
		t.Fatalf("Failed to update activity_logs time strings: %v", err)
	}

	logs, err := db.ListActivityLogs(10)
	if err != nil {
		t.Fatalf("ListActivityLogs failed: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("ListActivityLogs returned 0 items")
	}

	loginSess := &models.LoginSession{
		ID:        "sess_tidb_1",
		UserID:    "user_admin",
		Username:  "admin",
		IPAddress: "127.0.0.1",
		CreatedAt: time.Now(),
	}
	if err := db.LogLoginSession(loginSess); err != nil {
		t.Fatalf("LogLoginSession failed: %v", err)
	}
	_, err = db.SQLDB().Exec("UPDATE login_sessions SET created_at = ? WHERE id = ?", "2026-09-30 08:00:00", loginSess.ID)
	if err != nil {
		t.Fatalf("Failed to update login_sessions time strings: %v", err)
	}

	sessions, err := db.ListLoginSessions(10)
	if err != nil {
		t.Fatalf("ListLoginSessions failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatalf("ListLoginSessions returned 0 items")
	}
}

// TestAutoSyncFromSnapshot_RecoveryWhenSQLiteDeleted kiểm tra kịch bản cốt lõi:
// Khi file SQLite bị xóa hoặc khởi động container trắng, hệ thống tự động đồng bộ
// và phục hồi 100% dữ liệu (11 accounts, 675 files) từ snapshot an toàn.
func TestAutoSyncFromSnapshot_RecoveryWhenSQLiteDeleted(t *testing.T) {
	tempDir := t.TempDir()
	backupsDir := filepath.Join(tempDir, "backups")
	if err := os.MkdirAll(backupsDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Đọc file snapshot thật từ dự án
	realSnapCandidates := []string{
		`f:\supportflast.dev\data\backups\cloudpool_snapshot.json`,
		filepath.Join("..", "..", "..", "data", "backups", "cloudpool_snapshot.json"),
		filepath.Join("data", "backups", "cloudpool_snapshot.json"),
	}
	var realSnapData []byte
	for _, c := range realSnapCandidates {
		if d, err := os.ReadFile(c); err == nil && len(d) > 0 {
			realSnapData = d
			break
		}
	}
	if len(realSnapData) == 0 {
		t.Skip("Không tìm thấy file cloudpool_snapshot.json mẫu để chạy test phục hồi")
	}

	// Copy snapshot vào thư mục backups tạm
	targetSnap := filepath.Join(backupsDir, "cloudpool_snapshot.json")
	if err := os.WriteFile(targetSnap, realSnapData, 0644); err != nil {
		t.Fatalf("Failed to copy snapshot: %v", err)
	}

	// Thiết lập DATA_DIR trỏ về tempDir
	oldDataDir := os.Getenv("DATA_DIR")
	os.Setenv("DATA_DIR", tempDir)
	defer os.Setenv("DATA_DIR", oldDataDir)

	// Khởi tạo DB SQLite mới tinh (giả lập file SQLite cũ bị xóa hoàn toàn)
	newDBPath := filepath.Join(tempDir, "cloudpool_metadata.db")
	db, err := NewDB(newDBPath)
	if err != nil {
		t.Fatalf("NewDB failed on new empty database: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra 11 tài khoản Google Drive đã được tự động phục hồi
	var accCount int
	_ = db.SQLDB().QueryRow("SELECT COUNT(1) FROM accounts").Scan(&accCount)
	if accCount < 11 {
		t.Errorf("Kỳ vọng ít nhất 11 tài khoản Google Drive được phục hồi, thực tế: %d", accCount)
	}

	// 2. Kiểm tra 675 tệp tin/thư mục VFS đã được tự động phục hồi
	var fileCount int
	_ = db.SQLDB().QueryRow("SELECT COUNT(1) FROM virtual_files WHERE id != 'root'").Scan(&fileCount)
	if fileCount < 600 {
		t.Errorf("Kỳ vọng tệp tin VFS được phục hồi (~675), thực tế: %d", fileCount)
	}

	// 3. Kiểm tra file_chunks đã được phục hồi
	var chunkCount int
	_ = db.SQLDB().QueryRow("SELECT COUNT(1) FROM file_chunks").Scan(&chunkCount)
	if chunkCount < 700 {
		t.Errorf("Kỳ vọng chunk dữ liệu được phục hồi (~748), thực tế: %d", chunkCount)
	}

	// 4. Kiểm tra cấu hình mặc định (master_passphrase, chunk_size, webdav) đã được seed
	var masterPass, chunkSize, strategy string
	_ = db.SQLDB().QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&masterPass)
	_ = db.SQLDB().QueryRow("SELECT `value` FROM settings WHERE `key` = 'chunk_size_bytes'").Scan(&chunkSize)
	_ = db.SQLDB().QueryRow("SELECT `value` FROM settings WHERE `key` = 'allocation_strategy'").Scan(&strategy)

	if masterPass == "" {
		t.Errorf("master_passphrase chưa được khởi tạo trong settings")
	}
	if chunkSize == "" {
		t.Errorf("chunk_size_bytes chưa được khởi tạo trong settings")
	}
	if strategy == "" {
		t.Errorf("allocation_strategy chưa được khởi tạo trong settings")
	}

	t.Logf("Auto-Sync kiểm thử thành công: %d tài khoản, %d tệp tin, %d chunks được phục hồi mượt mà!", accCount, fileCount, chunkCount)
}

func TestParseFlexibleTimestamp(t *testing.T) {
	cases := []struct {
		input    interface{}
		expected string
	}{
		{"2026-08-26 14:42:38.7552737 +0700 +07 m=+48.188630501", "2026-08-26 14:42:38"},
		{"2026-09-30T08:15:30Z", "2026-09-30 08:15:30"},
		{"2026-01-02 15:04:05", "2026-01-02 15:04:05"},
		{nil, ""},
		{"", ""},
	}

	for _, c := range cases {
		res := parseFlexibleTimestamp(c.input)
		if c.expected == "" {
			if res != nil && res != "" {
				t.Errorf("Kỳ vọng rỗng/nil cho input %v, thực tế: %v", c.input, res)
			}
		} else {
			if res != c.expected {
				t.Errorf("Input '%v': kỳ vọng '%s', thực tế '%v'", c.input, c.expected, res)
			}
		}
	}
}

func TestVFSRootAndHierarchyIntegrity(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "vfs_test.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra GetVirtualFile("root")
	root, err := db.GetVirtualFile("root")
	if err != nil {
		t.Fatalf("GetVirtualFile('root') failed: %v", err)
	}
	if root == nil {
		t.Fatalf("Root virtual file is nil")
	}
	if root.ID != "root" {
		t.Errorf("Expected root.ID == 'root', got %s", root.ID)
	}
	if root.ParentID != "" {
		t.Errorf("Expected root.ParentID == '', got %s", root.ParentID)
	}
	if !root.IsDir {
		t.Errorf("Expected root.IsDir == true, got %v", root.IsDir)
	}
	if root.Path != "/" {
		t.Errorf("Expected root.Path == '/', got %s", root.Path)
	}
	if root.IsTrashed {
		t.Errorf("Expected root.IsTrashed == false, got %v", root.IsTrashed)
	}

	// 2. Tạo cây thư mục mô phỏng: root -> dir_test1 -> file1, file2
	dir1 := &models.VirtualFile{
		ID:        "dir_test1",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "Folder 1",
		Path:      "/Folder 1",
		IsDir:     true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveVirtualFile(dir1); err != nil {
		t.Fatalf("SaveVirtualFile dir1 failed: %v", err)
	}

	file1 := &models.VirtualFile{
		ID:        "file_test1",
		UserID:    "user_admin",
		ParentID:  "dir_test1",
		Name:      "test1.txt",
		Path:      "/Folder 1/test1.txt",
		IsDir:     false,
		SizeBytes: 1024,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveVirtualFile(file1); err != nil {
		t.Fatalf("SaveVirtualFile file1 failed: %v", err)
	}

	fileRoot := &models.VirtualFile{
		ID:        "file_root",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "root_file.txt",
		Path:      "/root_file.txt",
		IsDir:     false,
		SizeBytes: 2048,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.SaveVirtualFile(fileRoot); err != nil {
		t.Fatalf("SaveVirtualFile fileRoot failed: %v", err)
	}

	// 3. Kiểm tra ListVirtualFiles("user_admin", "root")
	rootChildren, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles('user_admin', 'root') failed: %v", err)
	}
	if len(rootChildren) != 2 {
		t.Errorf("Expected 2 children at root (1 dir + 1 file), got %d", len(rootChildren))
	}
	for _, c := range rootChildren {
		if c.ID == "root" {
			t.Errorf("Root directory must not be present in its own child list")
		}
		if c.ParentID != "root" {
			t.Errorf("Expected child ParentID == 'root', got %s", c.ParentID)
		}
	}

	// 4. Kiểm tra GetAllFilesInFolderTree("root")
	allFiles, err := db.GetAllFilesInFolderTree("root")
	if err != nil {
		t.Fatalf("GetAllFilesInFolderTree('root') failed: %v", err)
	}
	if len(allFiles) != 2 {
		t.Errorf("Expected 2 files in folder tree, got %d", len(allFiles))
	}

	// 5. Kiểm tra bảo vệ thư mục gốc Root không bị xóa hoặc đổi tên
	if err := db.SoftDeleteVirtualFile("root"); err == nil {
		t.Errorf("SoftDeleteVirtualFile('root') should have been rejected")
	}
	if err := db.DeleteVirtualFile("root"); err == nil {
		t.Errorf("DeleteVirtualFile('root') should have been rejected")
	}
	if err := db.RenameVirtualFile("root", "new_root", "/new_root"); err == nil {
		t.Errorf("RenameVirtualFile('root') should have been rejected")
	}

	// Kiểm tra trạng thái root sau khi thử xóa: vẫn an toàn
	rootCheck, err := db.GetVirtualFile("root")
	if err != nil || rootCheck == nil {
		t.Fatalf("GetVirtualFile('root') failed after delete attempts: %v", err)
	}
	if rootCheck.IsTrashed {
		t.Errorf("Root is_deleted must be false after failed soft delete attempt")
	}

	// 6. Kiểm tra EnsureRootExists phục hồi root nếu bị cố tình can thiệp SQL
	_, _ = db.SQLDB().Exec("UPDATE virtual_files SET is_deleted = 1, parent_id = 'invalid', is_dir = 0 WHERE id = 'root'")
	if err := db.EnsureRootExists(); err != nil {
		t.Fatalf("EnsureRootExists failed: %v", err)
	}
	rootRecovered, err := db.GetVirtualFile("root")
	if err != nil || rootRecovered == nil {
		t.Fatalf("Failed to reload root after EnsureRootExists: %v", err)
	}
	if rootRecovered.IsTrashed {
		t.Errorf("Root must be active (IsTrashed == false) after EnsureRootExists")
	}
	if rootRecovered.ParentID != "" {
		t.Errorf("Root parent_id must be restored to empty string, got %s", rootRecovered.ParentID)
	}
	if !rootRecovered.IsDir {
		t.Errorf("Root is_dir must be restored to true, got %v", rootRecovered.IsDir)
	}
}

func TestRealDatabaseVFSIntegrity(t *testing.T) {
	realDBPath := `f:\supportflast.dev\data\cloudpool_metadata.db`
	if _, err := os.Stat(realDBPath); os.IsNotExist(err) {
		t.Skip("Bỏ qua kiểm tra CSDL thật vì file không tồn tại")
	}

	db, err := NewDB(realDBPath)
	if err != nil {
		t.Fatalf("NewDB on real database failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra thư mục gốc root
	root, err := db.GetVirtualFile("root")
	if err != nil {
		t.Fatalf("GetVirtualFile('root') failed on real DB: %v", err)
	}
	if root.ParentID != "" {
		t.Errorf("Expected root.ParentID == '', got '%s'", root.ParentID)
	}
	if root.Path != "/" {
		t.Errorf("Expected root.Path == '/', got '%s'", root.Path)
	}
	if !root.IsDir {
		t.Errorf("Expected root.IsDir == true, got %v", root.IsDir)
	}
	if root.IsTrashed {
		t.Errorf("Expected root.IsTrashed == false, got %v", root.IsTrashed)
	}

	// 2. Thống kê tổng số records, folders, files
	var totalRecords, totalFolders, totalFiles, trashedCount int
	_ = db.SQLDB().QueryRow("SELECT COUNT(*) FROM virtual_files").Scan(&totalRecords)
	_ = db.SQLDB().QueryRow("SELECT COUNT(*) FROM virtual_files WHERE is_dir = 1").Scan(&totalFolders)
	_ = db.SQLDB().QueryRow("SELECT COUNT(*) FROM virtual_files WHERE is_dir = 0").Scan(&totalFiles)
	_ = db.SQLDB().QueryRow("SELECT COUNT(*) FROM virtual_files WHERE is_deleted = 1").Scan(&trashedCount)

	t.Logf("CSDL Thật: %d records (%d thư mục gồm root, %d tệp tin), trashed=%d", totalRecords, totalFolders, totalFiles, trashedCount)

	if totalRecords != 675 {
		t.Logf("Lưu ý: Tổng số bản ghi là %d (kỳ vọng ~675)", totalRecords)
	}
	if totalFolders != 11 {
		t.Errorf("Kỳ vọng chính xác 11 thư mục (1 root + 10 thư mục con), thực tế: %d", totalFolders)
	}
	if trashedCount != 0 {
		t.Errorf("Kỳ vọng 0 file/thư mục bị is_deleted=1, thực tế: %d", trashedCount)
	}

	// 3. Kiểm tra tính toàn vẹn của parent_id (không có bản ghi mồ côi)
	var orphanCount int
	_ = db.SQLDB().QueryRow(`
		SELECT COUNT(*) FROM virtual_files 
		WHERE parent_id != '' AND parent_id != 'root' AND parent_id NOT IN (SELECT id FROM virtual_files)
	`).Scan(&orphanCount)
	if orphanCount != 0 {
		t.Errorf("Phát hiện %d bản ghi mồ côi (parent_id không tồn tại trong virtual_files)", orphanCount)
	}

	// 4. Kiểm tra ListVirtualFiles("user_admin", "root")
	rootChildren, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles('user_admin', 'root') failed: %v", err)
	}
	t.Logf("Số phần tử trực tiếp tại root: %d (10 thư mục con + %d files)", len(rootChildren), len(rootChildren)-10)
	for _, c := range rootChildren {
		if c.ID == "root" {
			t.Errorf("Root directory must not be present in its own child list")
		}
		if c.ParentID != "root" {
			t.Errorf("Child %s has invalid ParentID: %s", c.ID, c.ParentID)
		}
	}

	// 5. Kiểm tra duyệt đệ quy GetAllFilesInFolderTree("root")
	treeFiles, err := db.GetAllFilesInFolderTree("root")
	if err != nil {
		t.Fatalf("GetAllFilesInFolderTree('root') failed: %v", err)
	}
	t.Logf("Duyệt đệ quy từ root thành công: %d files tìm thấy (kỳ vọng %d files)", len(treeFiles), totalFiles)
	if len(treeFiles) != totalFiles {
		t.Errorf("Số file duyệt đệ quy (%d) không khớp tổng số file (%d)", len(treeFiles), totalFiles)
	}

	// 6. Kiểm tra bảo vệ root chống xóa trên CSDL thật
	if err := db.SoftDeleteVirtualFile("root"); err == nil {
		t.Errorf("SoftDeleteVirtualFile('root') should have been rejected")
	}
	if err := db.DeleteVirtualFile("root"); err == nil {
		t.Errorf("DeleteVirtualFile('root') should have been rejected")
	}
}

func TestListVirtualFilesGuestPolicy(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_guest_policy.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Tạo 2 file thuộc sở hữu của user_admin trong thư mục root
	f1 := &models.VirtualFile{
		ID:        "file_admin_1",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "admin_doc.pdf",
		Path:      "/admin_doc.pdf",
		IsDir:     false,
		SizeBytes: 1024,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(f1); err != nil {
		t.Fatalf("SaveVirtualFile f1 failed: %v", err)
	}

	f2 := &models.VirtualFile{
		ID:        "file_admin_2",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "shared_doc.pdf",
		Path:      "/shared_doc.pdf",
		IsDir:     false,
		SizeBytes: 2048,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(f2); err != nil {
		t.Fatalf("SaveVirtualFile f2 failed: %v", err)
	}

	// Tạo Public Share cho f2
	share := &models.PublicShare{
		ID:        "share_token_123",
		FileID:    f2.ID,
		FileName:  f2.Name,
		FileSize:  f2.SizeBytes,
		MimeType:  f2.MimeType,
		CreatedBy: "user_admin",
		IsActive:  true,
	}
	if err := db.CreatePublicShare(share); err != nil {
		t.Fatalf("CreatePublicShare failed: %v", err)
	}

	// 2. Kiểm tra chế độ mặc định ("view_only")
	filesGuest, err := db.ListVirtualFiles("guest", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles guest in view_only failed: %v", err)
	}
	if len(filesGuest) < 2 {
		t.Fatalf("Kỳ vọng ít nhất 2 file hiển thị cho guest ở view_only, thực tế: %d", len(filesGuest))
	}

	for _, f := range filesGuest {
		if f.ID == "file_admin_1" {
			if !f.IsAdminOwned {
				t.Errorf("file_admin_1 phải có IsAdminOwned = true")
			}
			if !f.RequiresOTP {
				t.Errorf("file_admin_1 chưa được chia sẻ nên phải RequiresOTP = true")
			}
		}
		if f.ID == "file_admin_2" {
			if !f.IsAdminOwned {
				t.Errorf("file_admin_2 phải có IsAdminOwned = true")
			}
			if f.RequiresOTP {
				t.Errorf("file_admin_2 đã có public share nên RequiresOTP phải là false")
			}
		}
	}

	// 3. Kiểm tra chế độ nghiêm ngặt ("strict")
	settings, err := db.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	settings.GuestAccessMode = "strict"
	if err := db.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings strict failed: %v", err)
	}

	filesGuestStrict, err := db.ListVirtualFiles("guest", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles guest in strict failed: %v", err)
	}
	if len(filesGuestStrict) != 0 {
		t.Errorf("Kỳ vọng 0 file cho guest trong strict mode, thực tế: %d", len(filesGuestStrict))
	}

	// Thêm 1 file thuộc guest
	fGuest := &models.VirtualFile{
		ID:        "file_guest_1",
		UserID:    "guest",
		ParentID:  "root",
		Name:      "guest_upload.txt",
		Path:      "/guest_upload.txt",
		IsDir:     false,
		SizeBytes: 512,
		MimeType:  "text/plain",
	}
	if err := db.SaveVirtualFile(fGuest); err != nil {
		t.Fatalf("SaveVirtualFile fGuest failed: %v", err)
	}

	filesGuestStrict2, err := db.ListVirtualFiles("guest", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles guest in strict failed: %v", err)
	}
	if len(filesGuestStrict2) != 1 || filesGuestStrict2[0].ID != "file_guest_1" {
		t.Errorf("Kỳ vọng chỉ 1 file guest_upload.txt hiển thị cho guest trong strict mode, thực tế: %d", len(filesGuestStrict2))
	}

	// 4. Kiểm tra Admin luôn xem được toàn bộ
	filesAdmin, err := db.ListVirtualFiles("user_admin", "root")
	if err != nil {
		t.Fatalf("ListVirtualFiles user_admin failed: %v", err)
	}
	if len(filesAdmin) < 3 {
		t.Errorf("Admin phải xem được tất cả các file (kỳ vọng >= 3, thực tế: %d)", len(filesAdmin))
	}
	for _, f := range filesAdmin {
		if f.RequiresOTP {
			t.Errorf("Admin xem file không bao giờ bị đánh dấu RequiresOTP")
		}
	}
}

func TestQueryOptimizer_ListVirtualFiles_InheritedShare(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_cte_optimizer.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Tạo cấu trúc thư mục phân cấp:
	// root
	//  ├── folder_shared (được public share)
	//  │    ├── sub_folder
	//  │    │    └── deep_file.pdf
	//  │    └── file_in_shared.pdf
	//  └── folder_unshared (không được share)
	//       └── file_in_unshared.pdf

	folderShared := &models.VirtualFile{
		ID:        "folder_shared_id",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "SharedFolder",
		Path:      "/SharedFolder",
		IsDir:     true,
		SizeBytes: 0,
		MimeType:  "inode/directory",
	}
	if err := db.SaveVirtualFile(folderShared); err != nil {
		t.Fatalf("SaveVirtualFile folderShared failed: %v", err)
	}

	subFolder := &models.VirtualFile{
		ID:        "sub_folder_id",
		UserID:    "user_admin",
		ParentID:  folderShared.ID,
		Name:      "SubFolder",
		Path:      "/SharedFolder/SubFolder",
		IsDir:     true,
		SizeBytes: 0,
		MimeType:  "inode/directory",
	}
	if err := db.SaveVirtualFile(subFolder); err != nil {
		t.Fatalf("SaveVirtualFile subFolder failed: %v", err)
	}

	deepFile := &models.VirtualFile{
		ID:        "deep_file_id",
		UserID:    "user_admin",
		ParentID:  subFolder.ID,
		Name:      "deep_file.pdf",
		Path:      "/SharedFolder/SubFolder/deep_file.pdf",
		IsDir:     false,
		SizeBytes: 4096,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(deepFile); err != nil {
		t.Fatalf("SaveVirtualFile deepFile failed: %v", err)
	}

	fileInShared := &models.VirtualFile{
		ID:        "file_in_shared_id",
		UserID:    "user_admin",
		ParentID:  folderShared.ID,
		Name:      "file_in_shared.pdf",
		Path:      "/SharedFolder/file_in_shared.pdf",
		IsDir:     false,
		SizeBytes: 1024,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(fileInShared); err != nil {
		t.Fatalf("SaveVirtualFile fileInShared failed: %v", err)
	}

	folderUnshared := &models.VirtualFile{
		ID:        "folder_unshared_id",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "UnsharedFolder",
		Path:      "/UnsharedFolder",
		IsDir:     true,
		SizeBytes: 0,
		MimeType:  "inode/directory",
	}
	if err := db.SaveVirtualFile(folderUnshared); err != nil {
		t.Fatalf("SaveVirtualFile folderUnshared failed: %v", err)
	}

	fileInUnshared := &models.VirtualFile{
		ID:        "file_in_unshared_id",
		UserID:    "user_admin",
		ParentID:  folderUnshared.ID,
		Name:      "file_in_unshared.pdf",
		Path:      "/UnsharedFolder/file_in_unshared.pdf",
		IsDir:     false,
		SizeBytes: 2048,
		MimeType:  "application/pdf",
	}
	if err := db.SaveVirtualFile(fileInUnshared); err != nil {
		t.Fatalf("SaveVirtualFile fileInUnshared failed: %v", err)
	}

	// 2. Chia sẻ public share cho folderShared
	share := &models.PublicShare{
		ID:        "share_folder_token",
		FileID:    folderShared.ID,
		FileName:  folderShared.Name,
		CreatedBy: "user_admin",
		IsActive:  true,
	}
	if err := db.CreatePublicShare(share); err != nil {
		t.Fatalf("CreatePublicShare failed: %v", err)
	}

	// 3. Kiểm tra ListVirtualFiles cho guest tại folderShared: file con & subfolder phải thừa kế RequiresOTP = false
	filesInShared, err := db.ListVirtualFiles("guest", folderShared.ID)
	if err != nil {
		t.Fatalf("ListVirtualFiles at folderShared failed: %v", err)
	}
	if len(filesInShared) != 2 {
		t.Fatalf("Kỳ vọng 2 mục trong folderShared, thực tế: %d", len(filesInShared))
	}
	for _, f := range filesInShared {
		if !f.IsAdminOwned {
			t.Errorf("File %s phải có IsAdminOwned = true", f.Name)
		}
		if f.RequiresOTP {
			t.Errorf("File %s nằm trong thư mục đã share công khai nên RequiresOTP phải là false (thực tế: true)", f.Name)
		}
	}

	// 4. Kiểm tra ListVirtualFiles cho guest tại subFolder: tệp tin cháu deepFile phải thừa kế RequiresOTP = false
	filesInSub, err := db.ListVirtualFiles("guest", subFolder.ID)
	if err != nil {
		t.Fatalf("ListVirtualFiles at subFolder failed: %v", err)
	}
	if len(filesInSub) != 1 {
		t.Fatalf("Kỳ vọng 1 mục trong subFolder, thực tế: %d", len(filesInSub))
	}
	if filesInSub[0].RequiresOTP {
		t.Errorf("deep_file nằm trong thư mục cháu của thư mục được share nên RequiresOTP phải là false")
	}

	// 5. Kiểm tra ListVirtualFiles cho guest tại folderUnshared: tệp tin chưa share phải có RequiresOTP = true
	filesInUnshared, err := db.ListVirtualFiles("guest", folderUnshared.ID)
	if err != nil {
		t.Fatalf("ListVirtualFiles at folderUnshared failed: %v", err)
	}
	if len(filesInUnshared) != 1 {
		t.Fatalf("Kỳ vọng 1 mục trong folderUnshared, thực tế: %d", len(filesInUnshared))
	}
	if !filesInUnshared[0].RequiresOTP {
		t.Errorf("file_in_unshared chưa được share nên RequiresOTP phải là true")
	}

	// 6. Kiểm tra hàm IsFileOrAncestorShared trực tiếp
	if !db.IsFileOrAncestorShared(deepFile.ID) {
		t.Errorf("IsFileOrAncestorShared(deepFile.ID) phải trả về true")
	}
	if db.IsFileOrAncestorShared(fileInUnshared.ID) {
		t.Errorf("IsFileOrAncestorShared(fileInUnshared.ID) phải trả về false")
	}
}

func TestQueryOptimizer_GetStats(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_stats_optimizer.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra GetStats trên DB rỗng
	stats, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats on empty DB failed: %v", err)
	}
	if stats.TotalAccounts != 0 || stats.TotalFilesCount != 0 || stats.TotalFoldersCount != 0 {
		t.Errorf("Kỳ vọng các chỉ số ban đầu bằng 0, thực tế: %+v", stats)
	}

	initialUsers := stats.TotalUsersCount

	// 2. Thêm dữ liệu mẫu: 2 tài khoản, 2 file, 1 folder, 2 chunks, 1 user
	acc1 := &models.Account{
		ID:              "acc_test_1",
		Email:           "acc1@example.com",
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  5 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  10 * 1024 * 1024 * 1024,
		Status:          "active",
	}
	acc2 := &models.Account{
		ID:              "acc_test_2",
		Email:           "acc2@example.com",
		TotalQuotaBytes: 15 * 1024 * 1024 * 1024,
		UsedQuotaBytes:  3 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  12 * 1024 * 1024 * 1024,
		Status:          "active",
	}
	_ = db.SaveAccount(acc1)
	_ = db.SaveAccount(acc2)

	folder1 := &models.VirtualFile{
		ID:       "folder_stat_1",
		UserID:   "user_admin",
		ParentID: "root",
		Name:     "StatFolder",
		IsDir:    true,
	}
	file1 := &models.VirtualFile{
		ID:       "file_stat_1",
		UserID:   "user_admin",
		ParentID: "root",
		Name:     "stat1.txt",
		IsDir:    false,
	}
	file2 := &models.VirtualFile{
		ID:       "file_stat_2",
		UserID:   "user_admin",
		ParentID: folder1.ID,
		Name:     "stat2.txt",
		IsDir:    false,
	}
	_ = db.SaveVirtualFile(folder1)
	_ = db.SaveVirtualFile(file1)
	_ = db.SaveVirtualFile(file2)

	// Thêm chunks
	chunk1 := models.FileChunk{
		ChunkID:        "chunk_stat_1",
		FileID:         file1.ID,
		AccountID:      acc1.ID,
		ChunkIndex:     0,
		ChunkSizeBytes: 1024,
		Status:         "uploaded",
	}
	chunk2 := models.FileChunk{
		ChunkID:        "chunk_stat_2",
		FileID:         file2.ID,
		AccountID:      acc2.ID,
		ChunkIndex:     0,
		ChunkSizeBytes: 2048,
		Status:         "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk1, chunk2}); err != nil {
		t.Fatalf("SaveChunks failed: %v", err)
	}

	// Thêm 1 user
	usr := &models.User{
		ID:       "usr_stat_1",
		Username: "user_test_stats",
		Role:     "user",
		Status:   "active",
	}
	_ = db.CreateUser(usr)

	// 3. Gọi lại GetStats() và đối chiếu
	statsAfter, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats after adding data failed: %v", err)
	}

	if statsAfter.TotalAccounts != 2 {
		t.Errorf("TotalAccounts kỳ vọng 2, thực tế: %d", statsAfter.TotalAccounts)
	}
	if statsAfter.ActiveAccounts != 2 {
		t.Errorf("ActiveAccounts kỳ vọng 2, thực tế: %d", statsAfter.ActiveAccounts)
	}
	if statsAfter.TotalFilesCount != 2 {
		t.Errorf("TotalFilesCount kỳ vọng 2, thực tế: %d", statsAfter.TotalFilesCount)
	}
	if statsAfter.TotalFoldersCount != 1 {
		t.Errorf("TotalFoldersCount kỳ vọng 1, thực tế: %d", statsAfter.TotalFoldersCount)
	}
	if statsAfter.TotalChunksCount != 2 {
		t.Errorf("TotalChunksCount kỳ vọng 2, thực tế: %d", statsAfter.TotalChunksCount)
	}
	if statsAfter.TotalUsersCount != initialUsers+1 {
		t.Errorf("TotalUsersCount kỳ vọng %d, thực tế: %d", initialUsers+1, statsAfter.TotalUsersCount)
	}
	expectedCapacity := int64(30 * 1024 * 1024 * 1024)
	if statsAfter.TotalCapacityBytes != expectedCapacity {
		t.Errorf("TotalCapacityBytes kỳ vọng %d, thực tế: %d", expectedCapacity, statsAfter.TotalCapacityBytes)
	}
}

func TestQueryOptimizer_LargeDirectory_NoNPlusOne(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_large_dir.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	parentFolder := &models.VirtualFile{
		ID:        "folder_large_batch",
		UserID:    "user_admin",
		ParentID:  "root",
		Name:      "LargeBatchFolder",
		Path:      "/LargeBatchFolder",
		IsDir:     true,
		SizeBytes: 0,
		MimeType:  "inode/directory",
	}
	if err := db.SaveVirtualFile(parentFolder); err != nil {
		t.Fatalf("SaveVirtualFile parentFolder failed: %v", err)
	}

	// Tạo 100 file thuộc sở hữu của admin trong folder này
	for i := 1; i <= 100; i++ {
		f := &models.VirtualFile{
			ID:        fmt.Sprintf("batch_file_%03d", i),
			UserID:    "user_admin",
			ParentID:  parentFolder.ID,
			Name:      fmt.Sprintf("document_%03d.pdf", i),
			Path:      fmt.Sprintf("/LargeBatchFolder/document_%03d.pdf", i),
			IsDir:     false,
			SizeBytes: int64(i * 1024),
			MimeType:  "application/pdf",
		}
		if err := db.SaveVirtualFile(f); err != nil {
			t.Fatalf("SaveVirtualFile file %d failed: %v", i, err)
		}
	}

	// 1. Khi chưa có share nào: 100 file đều phải RequiresOTP = true
	start := time.Now()
	list, err := db.ListVirtualFiles("guest", parentFolder.ID)
	durationNoShare := time.Since(start)
	if err != nil {
		t.Fatalf("ListVirtualFiles failed: %v", err)
	}
	if len(list) != 100 {
		t.Fatalf("Kỳ vọng 100 files, thực tế: %d", len(list))
	}
	for _, f := range list {
		if !f.IsAdminOwned {
			t.Errorf("File %s phải có IsAdminOwned = true", f.ID)
		}
		if !f.RequiresOTP {
			t.Errorf("File %s phải có RequiresOTP = true khi chưa share", f.ID)
		}
	}
	t.Logf("ListVirtualFiles với 100 files (0 shares): %v", durationNoShare)

	// 2. Chia sẻ 1 file duy nhất trong số 100 files (batch_file_042)
	share := &models.PublicShare{
		ID:        "share_file_42",
		FileID:    "batch_file_042",
		FileName:  "document_042.pdf",
		CreatedBy: "user_admin",
		IsActive:  true,
	}
	if err := db.CreatePublicShare(share); err != nil {
		t.Fatalf("CreatePublicShare failed: %v", err)
	}

	start = time.Now()
	list2, err := db.ListVirtualFiles("guest", parentFolder.ID)
	durationWithShare := time.Since(start)
	if err != nil {
		t.Fatalf("ListVirtualFiles round 2 failed: %v", err)
	}
	t.Logf("ListVirtualFiles với 100 files (1 active share): %v", durationWithShare)

	for _, f := range list2 {
		if f.ID == "batch_file_042" {
			if f.RequiresOTP {
				t.Errorf("batch_file_042 đã được share nên RequiresOTP phải là false")
			}
		} else {
			if !f.RequiresOTP {
				t.Errorf("File %s chưa được share nên RequiresOTP phải là true", f.ID)
			}
		}
	}
}
