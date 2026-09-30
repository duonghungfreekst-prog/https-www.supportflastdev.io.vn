package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supportflast_engine/database"
)

func TestDBDriver_SQLiteDefaults(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_driver_sqlite.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	if db.Driver() != "sqlite" {
		t.Errorf("Expected driver 'sqlite', got '%s'", db.Driver())
	}
	if !db.IsSQLite() {
		t.Errorf("Expected db.IsSQLite() to be true")
	}
	if db.IsMySQLOrTiDB() {
		t.Errorf("Expected db.IsMySQLOrTiDB() to be false for SQLite")
	}
	if db.SQLDB() == nil {
		t.Errorf("Expected db.SQLDB() to be non-nil")
	}
}

func TestNewDBWithConfig_SQLite(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_config_sqlite.db")

	db, err := NewDBWithConfig("sqlite", "", dbPath)
	if err != nil {
		t.Fatalf("NewDBWithConfig sqlite failed: %v", err)
	}
	defer db.Close()

	if db.Driver() != "sqlite" {
		t.Errorf("Expected driver 'sqlite', got '%s'", db.Driver())
	}
	if !db.IsSQLite() {
		t.Errorf("Expected IsSQLite() true")
	}
}

func TestNewDBWithConfig_AutoDriverFromEnv(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_env_sqlite.db")

	t.Setenv("CLOUDPOOL_DB_DRIVER", "sqlite")
	db, err := NewDBWithConfig("", "", dbPath)
	if err != nil {
		t.Fatalf("NewDBWithConfig with CLOUDPOOL_DB_DRIVER failed: %v", err)
	}
	defer db.Close()

	if db.Driver() != "sqlite" {
		t.Errorf("Expected driver 'sqlite', got '%s'", db.Driver())
	}
}

func TestNewTiDB_PingFailureOnInvalidHost(t *testing.T) {
	cfg := database.TiDBConfig{
		Host:            "127.0.0.1",
		Port:            54321, // Cổng không có service nào lắng nghe
		User:            "test_user",
		Password:        "test_pass",
		Database:        "test_db",
		TLSConfigName:   "tidb",
		ConnectTimeout:  100 * time.Millisecond,
		PingTimeout:     200 * time.Millisecond,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 3 * time.Minute,
		AutoMigrate:     false,
	}

	db, err := NewTiDB(cfg)
	if err == nil {
		if db != nil {
			_ = db.Close()
		}
		t.Fatal("Expected NewTiDB to fail when connecting to non-existent host/port")
	}

	if !strings.Contains(err.Error(), "ping check kết nối tới TiDB Cloud thất bại") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestNewDBWithConfig_TiDB_InvalidDSN(t *testing.T) {
	_, err := NewDBWithConfig("tidb", "invalid_dsn_format_@#$%", "")
	if err == nil {
		t.Fatal("Expected error with invalid DSN")
	}
}

func TestMaintenanceMethods_BypassPragmaOnMySQL(t *testing.T) {
	// Giả lập instance DB với driver 'tidb'
	mockDB := &DB{
		driver: "tidb",
		path:   "mock-endpoint",
	}

	if !mockDB.IsMySQLOrTiDB() {
		t.Errorf("Expected mockDB.IsMySQLOrTiDB() to be true")
	}
	if mockDB.IsSQLite() {
		t.Errorf("Expected mockDB.IsSQLite() to be false")
	}

	// OptimizeDatabase phải trả về nil mà không chạy VACUUM / PRAGMA
	if err := mockDB.OptimizeDatabase(); err != nil {
		t.Errorf("OptimizeDatabase failed on TiDB driver: %v", err)
	}

	// Checkpoint phải trả về nil mà không chạy PRAGMA wal_checkpoint
	if err := mockDB.Checkpoint(); err != nil {
		t.Errorf("Checkpoint failed on TiDB driver: %v", err)
	}

	// BackupDatabase phải trả về lỗi thông báo dùng BR hoặc mysqldump
	err := mockDB.BackupDatabase("backup.db")
	if err == nil {
		t.Errorf("Expected error from BackupDatabase on TiDB driver")
	} else if !strings.Contains(err.Error(), "VACUUM INTO chỉ khả dụng trên SQLite") {
		t.Errorf("Unexpected error message: %v", err)
	}
}
