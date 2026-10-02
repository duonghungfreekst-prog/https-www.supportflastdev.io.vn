package database

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultTiDBConfig(t *testing.T) {
	// Kiểm tra cấu hình mặc định khi không có biến môi trường
	cfg := DefaultTiDBConfig()

	if cfg.Port != DefaultTiDBPort {
		t.Errorf("Kỳ vọng cổng mặc định %d, thực tế: %d", DefaultTiDBPort, cfg.Port)
	}
	if cfg.Database != DefaultTiDBDatabase {
		t.Errorf("Kỳ vọng database mặc định '%s', thực tế: '%s'", DefaultTiDBDatabase, cfg.Database)
	}
	expectedUser := DefaultTiDBUser
	if cfg.User != expectedUser {
		t.Errorf("Kỳ vọng user mặc định '%s', thực tế: '%s'", expectedUser, cfg.User)
	}
	if cfg.TLSConfigName != DefaultTiDBTLSConfig {
		t.Errorf("Kỳ vọng TLS config '%s', thực tế: '%s'", DefaultTiDBTLSConfig, cfg.TLSConfigName)
	}
	if cfg.MinTLSVersion != tls.VersionTLS12 {
		t.Errorf("Kỳ vọng MinTLSVersion là TLS 1.2 (0x%04x), thực tế: 0x%04x", tls.VersionTLS12, cfg.MinTLSVersion)
	}
	if cfg.MaxOpenConns != DefaultTiDBMaxOpenConns {
		t.Errorf("Kỳ vọng MaxOpenConns %d, thực tế: %d", DefaultTiDBMaxOpenConns, cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != DefaultTiDBMaxIdleConns {
		t.Errorf("Kỳ vọng MaxIdleConns %d, thực tế: %d", DefaultTiDBMaxIdleConns, cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != DefaultTiDBConnMaxLifetime {
		t.Errorf("Kỳ vọng ConnMaxLifetime %v, thực tế: %v", DefaultTiDBConnMaxLifetime, cfg.ConnMaxLifetime)
	}
	if !cfg.AutoMigrate {
		t.Errorf("Kỳ vọng AutoMigrate mặc định là true")
	}
}

func TestDefaultTiDBConfig_EnvOverrides(t *testing.T) {
	// Thiết lập các biến môi trường giả lập
	os.Setenv("TIDB_HOST", "test-cluster.tidbcloud.com")
	os.Setenv("TIDB_PORT", "4001")
	os.Setenv("TIDB_USER", "custom_user.root")
	os.Setenv("TIDB_PASSWORD", "CustomPass123!")
	os.Setenv("TIDB_DATABASE", "custom_sf_db")
	os.Setenv("TIDB_MAX_OPEN_CONNS", "50")
	os.Setenv("TIDB_MAX_IDLE_CONNS", "20")
	os.Setenv("TIDB_CONN_MAX_LIFETIME", "15m")
	os.Setenv("TIDB_CONN_MAX_IDLE_TIME", "5m")
	os.Setenv("TIDB_CONNECT_TIMEOUT", "12s")
	os.Setenv("TIDB_AUTO_MIGRATE", "false")
	defer func() {
		os.Unsetenv("TIDB_HOST")
		os.Unsetenv("TIDB_PORT")
		os.Unsetenv("TIDB_USER")
		os.Unsetenv("TIDB_PASSWORD")
		os.Unsetenv("TIDB_DATABASE")
		os.Unsetenv("TIDB_MAX_OPEN_CONNS")
		os.Unsetenv("TIDB_MAX_IDLE_CONNS")
		os.Unsetenv("TIDB_CONN_MAX_LIFETIME")
		os.Unsetenv("TIDB_CONN_MAX_IDLE_TIME")
		os.Unsetenv("TIDB_CONNECT_TIMEOUT")
		os.Unsetenv("TIDB_AUTO_MIGRATE")
	}()

	cfg := DefaultTiDBConfig()

	if cfg.Host != "test-cluster.tidbcloud.com" {
		t.Errorf("Kỳ vọng Host 'test-cluster.tidbcloud.com', thực tế: '%s'", cfg.Host)
	}
	if cfg.Port != 4001 {
		t.Errorf("Kỳ vọng Port 4001, thực tế: %d", cfg.Port)
	}
	if cfg.User != "custom_user.root" {
		t.Errorf("Kỳ vọng User 'custom_user.root', thực tế: '%s'", cfg.User)
	}
	if cfg.Password != "CustomPass123!" {
		t.Errorf("Kỳ vọng Password 'CustomPass123!', thực tế: '%s'", cfg.Password)
	}
	if cfg.Database != "custom_sf_db" {
		t.Errorf("Kỳ vọng Database 'custom_sf_db', thực tế: '%s'", cfg.Database)
	}
	if cfg.MaxOpenConns != 50 {
		t.Errorf("Kỳ vọng MaxOpenConns 50, thực tế: %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 20 {
		t.Errorf("Kỳ vọng MaxIdleConns 20, thực tế: %d", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 15*time.Minute {
		t.Errorf("Kỳ vọng ConnMaxLifetime 15m, thực tế: %v", cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Errorf("Kỳ vọng ConnMaxIdleTime 5m, thực tế: %v", cfg.ConnMaxIdleTime)
	}
	if cfg.ConnectTimeout != 12*time.Second {
		t.Errorf("Kỳ vọng ConnectTimeout 12s, thực tế: %v", cfg.ConnectTimeout)
	}
	if cfg.AutoMigrate {
		t.Errorf("Kỳ vọng AutoMigrate false, thực tế: true")
	}
}

func TestBuildTiDBDSN_ValidConfig(t *testing.T) {
	cfg := TiDBConfig{
		Host:            "gateway01.ap-southeast-1.prod.aws.tidbcloud.com",
		Port:            4000,
		User:            "sf_app.root",
		Password:        "P@ssw0rdSecure!",
		Database:        "supportflast",
		TLSConfigName:   "tidb",
		ConnectTimeout:  10 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
	}

	dsn, err := BuildTiDBDSN(cfg)
	if err != nil {
		t.Fatalf("BuildTiDBDSN trả về lỗi: %v", err)
	}

	// Kiểm tra các thành phần bắt buộc trong DSN
	expectedParts := []string{
		"sf_app.root",
		"gateway01.ap-southeast-1.prod.aws.tidbcloud.com:4000",
		"/supportflast",
		"tls=tidb",
		"timeout=10s",
		"readTimeout=30s",
		"writeTimeout=30s",
		"charset=utf8mb4",
		"parseTime=true",
	}

	for _, part := range expectedParts {
		if !strings.Contains(dsn, part) {
			t.Errorf("DSN thiếu thành phần bắt buộc '%s': %s", part, dsn)
		}
	}
}

func TestBuildTiDBDSN_EmptyHost(t *testing.T) {
	cfg := TiDBConfig{
		Host: "",
	}
	_, err := BuildTiDBDSN(cfg)
	if err == nil {
		t.Fatal("Kỳ vọng lỗi khi TIDB_HOST rỗng, nhưng không có lỗi")
	}
}

func TestBuildTiDBDSN_CustomDSN(t *testing.T) {
	customDSN := "custom_user:pass@tcp(custom-host:4000)/custom_db?charset=utf8mb4"
	cfg := TiDBConfig{
		DSN:           customDSN,
		TLSConfigName: "tidb",
	}

	dsn, err := BuildTiDBDSN(cfg)
	if err != nil {
		t.Fatalf("BuildTiDBDSN với custom DSN thất bại: %v", err)
	}

	if !strings.Contains(dsn, "custom_user") || !strings.Contains(dsn, "custom-host:4000") {
		t.Errorf("Custom DSN không được bảo tồn: %s", dsn)
	}
	if !strings.Contains(dsn, "tls=tidb") {
		t.Errorf("Custom DSN thiếu tls=tidb tự động gán: %s", dsn)
	}
}

func TestRegisterTiDBTLSConfig_EnforceTLS12(t *testing.T) {
	// Kiểm tra cấu hình TLS mặc định "tidb" đã được đăng ký trong init()
	if !IsTLSRegistered("tidb") {
		t.Error("Cấu hình TLS 'tidb' phải được tự động đăng ký trong init()")
	}

	// Kiểm tra đăng ký cấu hình với min version thấp (ví dụ TLS 1.0 hoặc TLS 1.1)
	// Hàm RegisterTiDBTLSConfig phải tự động nâng lên ít nhất TLS 1.2
	err := RegisterTiDBTLSConfig("tidb-test-tls", tls.VersionTLS10, "", false)
	if err != nil {
		t.Fatalf("RegisterTiDBTLSConfig thất bại: %v", err)
	}
	if !IsTLSRegistered("tidb-test-tls") {
		t.Error("Cấu hình 'tidb-test-tls' chưa được ghi nhận trong registry")
	}

	// Kiểm tra đăng ký với đường dẫn file CA không tồn tại
	errBadCA := RegisterTiDBTLSConfig("tidb-bad-ca", tls.VersionTLS12, "non_existent_ca_file.pem", false)
	if errBadCA == nil {
		t.Error("Kỳ vọng lỗi khi file CA không tồn tại")
	}
}

func TestRegisterTiDBTLSConfig_WithValidPEM(t *testing.T) {
	// Tạo file PEM tạm trên ổ F (tuân thủ Rule 1.4)
	tempDir := filepath.Join(`f:\supportflast.dev\data`, "test_tidb")
	_ = os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	fakePEMPath := filepath.Join(tempDir, "fake_ca.pem")
	// Chuỗi PEM không hợp lệ để kiểm tra parse error
	_ = os.WriteFile(fakePEMPath, []byte("NOT_A_VALID_PEM_CERTIFICATE"), 0644)

	err := RegisterTiDBTLSConfig("tidb-invalid-pem", tls.VersionTLS12, fakePEMPath, false)
	if err == nil {
		t.Error("Kỳ vọng lỗi khi file PEM chứa nội dung không hợp lệ")
	}
}

func TestTiDBSingletonManagement(t *testing.T) {
	// Đảm bảo đóng nếu có
	_ = CloseTiDB()

	if db := GetTiDB(); db != nil {
		t.Error("Kỳ vọng GetTiDB() là nil ban đầu")
	}

	// Gán mock instance
	SetTiDBInstance(nil)
	if db := GetTiDB(); db != nil {
		t.Error("Kỳ vọng GetTiDB() là nil sau khi set nil")
	}

	// CloseTiDB khi nil không được gây panic
	if err := CloseTiDB(); err != nil {
		t.Errorf("CloseTiDB() trả về lỗi bất ngờ: %v", err)
	}
}

func TestSeedInitialTiDBAdmin_NilDB(t *testing.T) {
	err := SeedInitialTiDBAdmin(nil)
	if err == nil {
		t.Error("Kỳ vọng lỗi khi db là nil trong SeedInitialTiDBAdmin")
	}
}

func TestOpenTiDBConnection_PingFailOnInvalidHost(t *testing.T) {
	// Kiểm tra hành vi khi ping tới host không tồn tại:
	// Hệ thống phải timeout nhanh chóng, đóng kết nối sạch sẽ và trả về lỗi rõ ràng
	cfg := TiDBConfig{
		Host:           "127.0.0.1",
		Port:           59999, // Cổng không có service nào lắng nghe
		User:           "root",
		Password:       "fake_pass",
		Database:       "supportflast",
		TLSConfigName:  "tidb",
		ConnectTimeout: 100 * time.Millisecond,
		PingTimeout:    200 * time.Millisecond,
		AutoMigrate:    false,
	}

	start := time.Now()
	db, err := OpenTiDBConnection(cfg)
	duration := time.Since(start)

	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("Kỳ vọng OpenTiDBConnection thất bại khi kết nối tới cổng không tồn tại")
	}

	if !strings.Contains(err.Error(), "ping check kết nối tới TiDB Cloud") {
		t.Errorf("Thông báo lỗi không đúng format mong đợi: %v", err)
	}

	if duration > 3*time.Second {
		t.Errorf("Quá trình ping check tốn quá nhiều thời gian (%v), kỳ vọng timeout nhanh", duration)
	}
}
