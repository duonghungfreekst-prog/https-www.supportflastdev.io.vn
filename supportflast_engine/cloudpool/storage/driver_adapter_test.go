package storage

import (
	"path/filepath"
	"strings"
	"testing"

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
	if db.IsMySQL() {
		t.Errorf("Expected db.IsMySQL() to be false for SQLite")
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

func TestNewDBWithConfig_Postgres_InvalidDSN(t *testing.T) {
	_, err := NewDBWithConfig("postgres", "postgres://invalid:invalid@127.0.0.1:54321/defaultdb?sslmode=disable", "")
	if err == nil {
		t.Fatal("Expected error with invalid DSN")
	}
}

func TestMaintenanceMethods_BypassPragmaOnMySQL(t *testing.T) {
	// Giả lập instance DB với driver 'postgres'
	mockDB := &DB{
		driver: "mysql",
		path:   "mock-endpoint",
	}

	if !mockDB.IsMySQL() {
		t.Errorf("Expected mockDB.IsMySQL() to be true")
	}
	if mockDB.IsSQLite() {
		t.Errorf("Expected mockDB.IsSQLite() to be false")
	}

	// OptimizeDatabase phải trả về nil mà không chạy VACUUM / PRAGMA
	if err := mockDB.OptimizeDatabase(); err != nil {
		t.Errorf("OptimizeDatabase failed on Aiven PostgreSQL driver: %v", err)
	}

	// Checkpoint phải trả về nil mà không chạy PRAGMA wal_checkpoint
	if err := mockDB.Checkpoint(); err != nil {
		t.Errorf("Checkpoint failed on Aiven PostgreSQL driver: %v", err)
	}

	// BackupDatabase phải trả về lỗi thông báo dùng BR hoặc mysqldump
	err := mockDB.BackupDatabase("backup.db")
	if err == nil {
		t.Errorf("Expected error from BackupDatabase on Aiven PostgreSQL driver")
	} else if !strings.Contains(err.Error(), "VACUUM INTO chỉ khả dụng trên SQLite") {
		t.Errorf("Unexpected error message: %v", err)
	}
}
