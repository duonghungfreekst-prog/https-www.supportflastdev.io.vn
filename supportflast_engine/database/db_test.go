package database

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func setupTestDB(t *testing.T) (string, func()) {
	t.Helper()
	// Đảm bảo lưu file test trên ổ F theo Workspace Rules (Rule 1.4)
	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_db")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_"+t.Name()+".db")
	_ = CloseDB()
	_ = os.Remove(testDBPath)
	_ = os.Remove(testDBPath + "-wal")
	_ = os.Remove(testDBPath + "-shm")

	cleanup := func() {
		_ = CloseDB()
		_ = os.Remove(testDBPath)
		_ = os.Remove(testDBPath + "-wal")
		_ = os.Remove(testDBPath + "-shm")
		_ = os.RemoveAll(testDir)
	}

	return testDBPath, cleanup
}

func TestInitDB_WALAndForeignKeys(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	// Reset state để test độc lập
	_ = CloseDB()

	db, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}
	if db == nil {
		t.Fatal("DB instance nhận về là nil")
	}

	// 1. Kiểm tra WAL mode
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Lỗi truy vấn PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Kỳ vọng journal_mode = 'wal', thực tế: '%s'", journalMode)
	}

	// 2. Kiểm tra Foreign Keys
	var foreignKeys int
	err = db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("Lỗi truy vấn PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("Kỳ vọng foreign_keys = 1 (ON), thực tế: %d", foreignKeys)
	}
}

func TestTablesAndIndexesExist(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	db, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	requiredTables := []string{
		"users",
		"apps",
		"api_keys",
		"reviews",
		"audit_logs",
		"system_releases",
		"security_events",
	}

	for _, table := range requiredTables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?;", table).Scan(&name)
		if err != nil || name != table {
			t.Errorf("Bảng bắt buộc '%s' không tồn tại trong CSDL: %v", table, err)
		}
	}

	requiredIndexes := []string{
		"idx_users_username",
		"idx_users_email",
		"idx_users_role",
		"idx_apps_user_id",
		"idx_apps_status",
		"idx_apps_category",
		"idx_apps_platform",
		"idx_api_keys_user_id",
		"idx_api_keys_key_hash",
		"idx_reviews_app_id",
		"idx_reviews_user_id",
		"idx_audit_logs_user_id",
		"idx_audit_logs_action",
		"idx_system_releases_date",
		"idx_security_events_ip",
		"idx_security_events_type",
		"idx_security_events_created_at",
		"idx_security_events_blocked_until",
	}

	for _, index := range requiredIndexes {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?;", index).Scan(&name)
		if err != nil || name != index {
			t.Errorf("Chỉ mục (INDEX) '%s' không tồn tại: %v", index, err)
		}
	}
}

func TestSeedInitialData_AdminOnly(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	db, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	// Kiểm tra tài khoản admin được seed
	var (
		id          string
		username    string
		email       string
		role        string
		displayName string
		hash        string
	)
	err = db.QueryRow(`
		SELECT id, username, email, role, display_name, password_hash
		FROM users
		WHERE username = 'admin';
	`).Scan(&id, &username, &email, &role, &displayName, &hash)
	if err != nil {
		t.Fatalf("Không tìm thấy tài khoản admin đã seed: %v", err)
	}

	if username != "admin" {
		t.Errorf("Kỳ vọng username='admin', thực tế: '%s'", username)
	}
	if email != "admin@supportflastdev.io.vn" {
		t.Errorf("Kỳ vọng email='admin@supportflastdev.io.vn', thực tế: '%s'", email)
	}
	if role != "admin" {
		t.Errorf("Kỳ vọng role='admin', thực tế: '%s'", role)
	}
	if displayName != "Quản Trị Viên Hệ Thống" {
		t.Errorf("Kỳ vọng display_name='Quản Trị Viên Hệ Thống', thực tế: '%s'", displayName)
	}

	// Kiểm tra hash BCrypt hợp lệ
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(DefaultAdminPassword))
	if err != nil {
		t.Errorf("Mật khẩu băm BCrypt không khớp với mật khẩu mặc định: %v", err)
	}

	// TUÂN THỦ RULE 9.1: Tuyệt đối không có app hoặc review rác/demo/fake được seed
	var mockAppCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM apps WHERE id LIKE '%mock%' OR id LIKE '%fake%' OR id LIKE '%demo%'").Scan(&mockAppCount); err != nil {
		t.Fatalf("Lỗi kiểm tra app demo: %v", err)
	}
	if mockAppCount != 0 {
		t.Errorf("VI PHẠM RULE 9.1: Bảng apps bị seed dữ liệu giả lập demo (%d records)", mockAppCount)
	}

	var mockReviewCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM reviews WHERE id LIKE '%mock%' OR id LIKE '%fake%' OR id LIKE '%demo%'").Scan(&mockReviewCount); err != nil {
		t.Fatalf("Lỗi kiểm tra review demo: %v", err)
	}
	if mockReviewCount != 0 {
		t.Errorf("VI PHẠM RULE 9.1: Bảng reviews bị seed dữ liệu giả lập demo (%d records)", mockReviewCount)
	}

	// Chạy lại SeedInitialData lần 2: Đảm bảo tính idempotent, không bị lỗi duplicate
	err = SeedInitialData(db)
	if err != nil {
		t.Fatalf("Chạy lại SeedInitialData bị lỗi: %v", err)
	}

	var userCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	if userCount != 1 {
		t.Errorf("Kỳ vọng chính xác 1 user sau khi seed lại, thực tế: %d", userCount)
	}
}

func TestConcurrentGetDB(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	_, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	var wg sync.WaitGroup
	concurrentCount := 20
	errorsChan := make(chan error, concurrentCount)

	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			db := GetDB()
			if db == nil {
				t.Errorf("Goroutine %d: GetDB() trả về nil", id)
				return
			}
			var dummy int
			if err := db.QueryRow("SELECT 1;").Scan(&dummy); err != nil {
				errorsChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errorsChan)

	for err := range errorsChan {
		t.Errorf("Lỗi truy vấn đồng thời: %v", err)
	}
}

func TestInitDefaultDB(t *testing.T) {
	_ = CloseDB()

	// Khởi tạo CSDL mặc định tại f:\supportflast.dev\data\supportflast.db
	db, err := InitDB()
	if err != nil {
		t.Fatalf("Khởi tạo CSDL mặc định thất bại: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users;").Scan(&count); err != nil {
		t.Fatalf("Không thể truy vấn bảng users trên CSDL mặc định: %v", err)
	}

	if count == 0 {
		t.Fatal("Bảng users phải có ít nhất 1 tài khoản admin sau khi khởi tạo")
	}

	// Đảm bảo đóng kết nối để giải phóng file lock
	_ = CloseDB()
}

func TestAuditLogs_AllRequiredActions(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	_, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	// 1. Kiểm tra lưu vết đầy đủ mọi hành động theo yêu cầu Subagent 8:
	// - Đăng nhập (login_success, login_failure)
	// - Đổi mật khẩu (change_password)
	// - Đổi mã PIN (change_pin)
	// - Truy cập Honeypot (honeypot_access)
	// - Thay đổi cấu hình (config_change, settings_update)
	testActions := []struct {
		userID    string
		action    string
		ip        string
		ua        string
		details   string
	}{
		{"usr-admin-001", AuditActionLoginSuccess, "192.168.1.10", "Mozilla/5.0", "Admin đăng nhập thành công qua Web UI"},
		{"", AuditActionLoginFailure, "10.0.0.99", "Python/3.11", "Đăng nhập sai mật khẩu lần 1"},
		{"usr-admin-001", AuditActionChangePassword, "192.168.1.10", "Mozilla/5.0", "Quản trị viên đổi mật khẩu thành công"},
		{"usr-admin-001", AuditActionChangePIN, "192.168.1.10", "Mozilla/5.0", "Cài đặt mã PIN Cấp 2 mới (6 số)"},
		{"", AuditActionHoneypotAccess, "45.33.32.156", "SqlMap/1.4", "Phát hiện truy cập đường dẫn bẫy Honeypot: /.env"},
		{"usr-admin-001", AuditActionConfigChange, "192.168.1.10", "Mozilla/5.0", "Thay đổi cấu hình cổng dịch vụ sang 8080"},
		{"usr-admin-001", AuditActionSettingsUpdate, "192.168.1.10", "Mozilla/5.0", "Cập nhật mật khẩu mã hóa Master Passphrase Zero-Knowledge"},
	}

	for _, a := range testActions {
		if err := RecordAuditLog(a.userID, a.action, a.ip, a.ua, a.details); err != nil {
			t.Fatalf("RecordAuditLog thất bại cho hành động '%s': %v", a.action, err)
		}
	}

	// 2. Truy vấn danh sách toàn bộ audit logs bằng Prepared Statement
	allLogs, err := GetAuditLogs(50, 0)
	if err != nil {
		t.Fatalf("GetAuditLogs thất bại: %v", err)
	}
	if len(allLogs) != len(testActions) {
		t.Errorf("Kỳ vọng lấy được %d logs, thực tế: %d", len(testActions), len(allLogs))
	}

	// 3. Truy vấn theo từng loại hành động cụ thể
	pinLogs, err := GetAuditLogsByAction(AuditActionChangePIN, 10, 0)
	if err != nil {
		t.Fatalf("GetAuditLogsByAction(AuditActionChangePIN) thất bại: %v", err)
	}
	if len(pinLogs) != 1 || !strings.Contains(pinLogs[0].Details, "PIN Cấp 2") {
		t.Errorf("Audit log cho ChangePIN không chính xác: %+v", pinLogs)
	}

	honeypotLogs, err := GetAuditLogsByAction(AuditActionHoneypotAccess, 10, 0)
	if err != nil {
		t.Fatalf("GetAuditLogsByAction(AuditActionHoneypotAccess) thất bại: %v", err)
	}
	if len(honeypotLogs) != 1 || !strings.Contains(honeypotLogs[0].Details, "/.env") {
		t.Errorf("Audit log cho HoneypotAccess không chính xác: %+v", honeypotLogs)
	}

	configLogs, err := GetAuditLogsByAction(AuditActionConfigChange, 10, 0)
	if err != nil {
		t.Fatalf("GetAuditLogsByAction(AuditActionConfigChange) thất bại: %v", err)
	}
	if len(configLogs) != 1 {
		t.Errorf("Kỳ vọng 1 log ConfigChange, thực tế: %d", len(configLogs))
	}
}

func TestSecurityEvents_IPJailAndExpiration(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	_, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	// 1. Ghi nhận sự kiện an ninh với thời hạn chặn (Blocked Until) trong tương lai
	blockedUntil := time.Now().Add(24 * time.Hour)
	attackerIP := "198.51.100.77"
	err = RecordSecurityEvent("honeypot_trap", attackerIP, "critical", "Tự động giam giữ IP xâm phạm bẫy /.env", &blockedUntil)
	if err != nil {
		t.Fatalf("RecordSecurityEvent thất bại: %v", err)
	}

	// Kiểm tra trạng thái bị chặn
	isBlocked, expiry, err := IsIPBlocked(attackerIP)
	if err != nil {
		t.Fatalf("IsIPBlocked gặp lỗi: %v", err)
	}
	if !isBlocked {
		t.Errorf("Kỳ vọng IP %s bị chặn, nhưng kết quả là false", attackerIP)
	}
	if expiry.Before(time.Now()) {
		t.Errorf("Thời gian hết hạn %v phải ở trong tương lai", expiry)
	}

	// 2. Kiểm tra IP sạch (không bị chặn)
	cleanIP := "192.168.1.50"
	isCleanBlocked, _, err := IsIPBlocked(cleanIP)
	if err != nil {
		t.Fatalf("IsIPBlocked cho clean IP gặp lỗi: %v", err)
	}
	if isCleanBlocked {
		t.Errorf("IP sạch %s không được phép bị chặn", cleanIP)
	}

	// 3. Ghi nhận sự kiện với thời hạn đã hết hạn (trong quá khứ)
	pastUntil := time.Now().Add(-1 * time.Hour)
	expiredIP := "198.51.100.88"
	err = RecordSecurityEvent("brute_force", expiredIP, "warning", "Khóa tạm thời đã hết hạn", &pastUntil)
	if err != nil {
		t.Fatalf("RecordSecurityEvent cho IP hết hạn thất bại: %v", err)
	}

	isExpiredBlocked, _, err := IsIPBlocked(expiredIP)
	if err != nil {
		t.Fatalf("IsIPBlocked cho expired IP gặp lỗi: %v", err)
	}
	if isExpiredBlocked {
		t.Errorf("IP đã hết hạn %s phải tự động mở khóa", expiredIP)
	}

	// 4. Lấy danh sách security events
	events, err := GetSecurityEvents(10, 0)
	if err != nil {
		t.Fatalf("GetSecurityEvents thất bại: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("Kỳ vọng 2 security events, thực tế: %d", len(events))
	}
}

func TestPreparedStatements_SQLInjectionProof(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	db, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB thất bại: %v", err)
	}

	// Các chuỗi payload tấn công SQL Injection độc hại nguy hiểm nhất
	sqliPayloads := []string{
		`' OR 1=1 --`,
		`admin'--`,
		`' UNION SELECT null, null, null, null, null, null, null --`,
		`'; DROP TABLE users; --`,
		`" OR ""="`,
		`' OR 'a'='a`,
	}

	for _, payload := range sqliPayloads {
		// Gọi hàm lưu Audit Log với payload SQL Injection
		err := RecordAuditLog(payload, "sqli_test", payload, "Malicious UA", payload)
		if err != nil {
			t.Fatalf("RecordAuditLog với payload SQLi '%s' bị lỗi: %v", payload, err)
		}

		// Gọi hàm kiểm tra an ninh với payload
		err = RecordSecurityEvent("sqli_probe", payload, "critical", payload, nil)
		if err != nil {
			t.Fatalf("RecordSecurityEvent với payload SQLi '%s' bị lỗi: %v", payload, err)
		}

		// Gọi hàm kiểm tra IP bị chặn với payload
		isBlocked, _, err := IsIPBlocked(payload)
		if err != nil {
			t.Fatalf("IsIPBlocked với payload SQLi '%s' bị lỗi: %v", payload, err)
		}
		_ = isBlocked
	}

	// CHỨNG MINH 100% Prepared Statements hoạt động an toàn:
	// 1. Bảng users tuyệt đối không bị DROP bởi payload "'; DROP TABLE users; --"
	var userTableCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users';").Scan(&userTableCount)
	if err != nil || userTableCount != 1 {
		t.Fatalf("LỖI BẢO MẬT NGHIÊM TRỌNG: Bảng users đã bị SQL Injection phá hủy!")
	}

	// 2. Tài khoản admin ban đầu vẫn nguyên vẹn
	var adminCount int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE username='admin';").Scan(&adminCount)
	if err != nil || adminCount != 1 {
		t.Fatalf("LỖI BẢO MẬT: Dữ liệu admin bị ảnh hưởng bởi SQL Injection!")
	}

	// 3. Payload độc hại được lưu chính xác dưới dạng chuỗi văn bản thuần túy (không thực thi lệnh SQL)
	logs, err := GetAuditLogsByAction("sqli_test", 50, 0)
	if err != nil {
		t.Fatalf("Truy vấn audit logs sau test SQLi thất bại: %v", err)
	}
	if len(logs) != len(sqliPayloads) {
		t.Errorf("Kỳ vọng %d bản ghi audit logs, thực tế: %d", len(sqliPayloads), len(logs))
	}
}

func TestInitSQLite_DirectAndCustomPath(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	db, err := InitSQLite(testDBPath)
	if err != nil {
		t.Fatalf("InitSQLite thất bại: %v", err)
	}
	if db == nil {
		t.Fatal("DB instance từ InitSQLite là nil")
	}

	if ActiveDriver() != "sqlite" {
		t.Errorf("Kỳ vọng ActiveDriver='sqlite', thực tế: '%s'", ActiveDriver())
	}

	getDB := GetDB()
	if getDB != db {
		t.Errorf("GetDB() phải trả về cùng instance với InitSQLite()")
	}
}

func TestInitDB_DriverSelection(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Kiểm tra khi DB_DRIVER="" hoặc "sqlite"
	_ = CloseDB()
	_ = os.Setenv("DB_DRIVER", "sqlite")
	defer os.Unsetenv("DB_DRIVER")

	db, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB với DB_DRIVER=sqlite thất bại: %v", err)
	}
	if ActiveDriver() != "sqlite" {
		t.Errorf("Kỳ vọng driver là 'sqlite', thực tế: '%s'", ActiveDriver())
	}
	_ = db

	// 2. Kiểm tra khi DB_DRIVER để trống
	_ = CloseDB()
	_ = os.Setenv("DB_DRIVER", "")
	db2, err := InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB khi DB_DRIVER để trống thất bại: %v", err)
	}
	if ActiveDriver() != "sqlite" {
		t.Errorf("Kỳ vọng driver mặc định là 'sqlite', thực tế: '%s'", ActiveDriver())
	}
	_ = db2

	// 3. Kiểm tra khi DB_DRIVER="tidb" không có customPath
	_ = CloseDB()
	_ = os.Setenv("DB_DRIVER", "tidb")
	_ = os.Setenv("TIDB_HOST", "127.0.0.1")
	_ = os.Setenv("TIDB_PORT", "19999") // Cổng không có server để kiểm tra nó gọi InitTiDB
	_ = os.Setenv("TIDB_CONNECT_TIMEOUT", "1s")
	_ = os.Setenv("TIDB_PING_TIMEOUT", "1s")
	defer func() {
		_ = os.Unsetenv("TIDB_HOST")
		_ = os.Unsetenv("TIDB_PORT")
		_ = os.Unsetenv("TIDB_CONNECT_TIMEOUT")
		_ = os.Unsetenv("TIDB_PING_TIMEOUT")
	}()

	// Khi DB_DRIVER=tidb và không truyền customPath, InitDB phải gọi InitTiDB()
	_, errTiDB := InitDB()
	if errTiDB == nil {
		t.Log("InitDB kết nối thành công tới TiDB")
	} else {
		// Kỳ vọng lỗi ping hoặc kết nối tới TiDB (chứng tỏ đã gọi InitTiDB)
		if !strings.Contains(errTiDB.Error(), "TiDB") && !strings.Contains(errTiDB.Error(), "connection refused") && !strings.Contains(errTiDB.Error(), "127.0.0.1:19999") {
			t.Errorf("InitDB với DB_DRIVER=tidb không gọi InitTiDB(): %v", errTiDB)
		}
	}
}

func TestSaveSecurityEvent_AliasAndRecordSecurityEvent(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	_, err := InitSQLite(testDBPath)
	if err != nil {
		t.Fatalf("InitSQLite thất bại: %v", err)
	}

	ip := "203.0.113.195"
	blockedUntil := time.Now().Add(2 * time.Hour)

	// Gọi SaveSecurityEvent
	err = SaveSecurityEvent("port_scan", ip, "high", "Quét cổng tự động phát hiện", &blockedUntil)
	if err != nil {
		t.Fatalf("SaveSecurityEvent thất bại: %v", err)
	}

	// Kiểm tra qua IsIPBlocked
	blocked, exp, err := IsIPBlocked(ip)
	if err != nil {
		t.Fatalf("IsIPBlocked thất bại: %v", err)
	}
	if !blocked {
		t.Errorf("Kỳ vọng IP %s bị chặn qua SaveSecurityEvent", ip)
	}
	if exp.Before(time.Now()) {
		t.Errorf("Thời hạn mở khóa %v phải trong tương lai", exp)
	}

	// Lấy sự kiện qua GetSecurityEvents
	events, err := GetSecurityEvents(10, 0)
	if err != nil {
		t.Fatalf("GetSecurityEvents thất bại: %v", err)
	}
	if len(events) == 0 || events[0].IPAddress != ip {
		t.Errorf("Sự kiện lưu bởi SaveSecurityEvent không tìm thấy trong GetSecurityEvents: %+v", events)
	}
}

func TestDualDatabase_DateTimeAndCompatibility(t *testing.T) {
	testDBPath, cleanup := setupTestDB(t)
	defer cleanup()

	_ = CloseDB()
	db, err := InitSQLite(testDBPath)
	if err != nil {
		t.Fatalf("InitSQLite thất bại: %v", err)
	}

	// 1. Giả lập mode "tidb" trên connection DB để kiểm tra nhánh format MySQL/TiDB
	SetDBInstance(db, "tidb")
	if !isMySQLOrTiDB() {
		t.Fatal("isMySQLOrTiDB() phải trả về true khi SetDBInstance với driver='tidb'")
	}

	// Ghi audit log dưới mode TiDB (sử dụng định dạng ngày 2006-01-02 15:04:05)
	err = RecordAuditLog("usr-admin-001", "tidb_audit_action", "10.10.10.10", "TiDB-Client/1.0", "Thao tác trên chế độ TiDB")
	if err != nil {
		t.Fatalf("RecordAuditLog trong mode TiDB thất bại: %v", err)
	}

	// Ghi security event dưới mode TiDB (blockedUntil định dạng DATETIME chuẩn)
	tidbBlockedIP := "203.0.113.88"
	futureTime := time.Now().Add(3 * time.Hour)
	err = SaveSecurityEvent("ddos_flood", tidbBlockedIP, "critical", "Tấn công DDoS từ cụm IP", &futureTime)
	if err != nil {
		t.Fatalf("SaveSecurityEvent trong mode TiDB thất bại: %v", err)
	}

	// Kiểm tra IsIPBlocked hoạt động chính xác trong mode TiDB
	blocked, exp, err := IsIPBlocked(tidbBlockedIP)
	if err != nil {
		t.Fatalf("IsIPBlocked trong mode TiDB thất bại: %v", err)
	}
	if !blocked {
		t.Errorf("IP %s phải bị chặn trong mode TiDB", tidbBlockedIP)
	}
	if exp.Before(time.Now()) {
		t.Errorf("Thời hạn mở khóa %v phải trong tương lai", exp)
	}

	// Chuyển lại mode SQLite và xác nhận cả 2 sự kiện đều đọc và truy vấn được
	SetDBInstance(db, "sqlite")
	if isMySQLOrTiDB() {
		t.Fatal("isMySQLOrTiDB() phải trả về false khi SetDBInstance với driver='sqlite'")
	}

	logs, err := GetAuditLogsByAction("tidb_audit_action", 10, 0)
	if err != nil {
		t.Fatalf("GetAuditLogsByAction thất bại: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("Kỳ vọng 1 log audit ghi từ mode TiDB, thực tế: %d", len(logs))
	}

	blocked2, _, err := IsIPBlocked(tidbBlockedIP)
	if err != nil || !blocked2 {
		t.Errorf("IsIPBlocked trong mode SQLite vẫn phải nhận diện được IP bị chặn: %v, blocked=%v", err, blocked2)
	}
}


