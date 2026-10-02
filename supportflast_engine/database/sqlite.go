package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ConfigureJournalModeWithFallback thiết lập chế độ journal cho SQLite với cơ chế chịu lỗi cao.
// Cố gắng kích hoạt 'PRAGMA journal_mode = WAL;'. Nếu gặp lỗi khóa trên môi trường đặc thù,
// tự động fallback an toàn sang 'TRUNCATE' hoặc 'DELETE' kết hợp 'PRAGMA busy_timeout = 5000;'.
func ConfigureJournalModeWithFallback(db *sql.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database connection is nil")
	}

	// 1. Luôn cấu hình busy_timeout = 5000ms trước tiên
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		log.Printf("[ENGINE] [DATABASE] [WARN] Cấu hình PRAGMA busy_timeout=5000 thất bại: %v", err)
	}

	// 2. Thử kích hoạt WAL mode (Write-Ahead Logging)
	var activeMode string
	walErr := db.QueryRow("PRAGMA journal_mode = WAL;").Scan(&activeMode)
	activeMode = strings.ToLower(strings.TrimSpace(activeMode))

	if walErr == nil && activeMode == "wal" {
		log.Printf("[ENGINE] [DATABASE] SQLite kích hoạt thành công chế độ journal_mode = WAL")
		applySafePragmas(db)
		return "wal", nil
	}

	log.Printf("[ENGINE] [DATABASE] [WARN] SQLite không thể kích hoạt WAL mode. Bắt đầu fallback sang TRUNCATE...")

	// 3. Fallback 1: TRUNCATE
	var truncateMode string
	truncateErr := db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&truncateMode)
	truncateMode = strings.ToLower(strings.TrimSpace(truncateMode))

	if truncateErr == nil && (truncateMode == "truncate" || truncateMode == "delete") {
		log.Printf("[ENGINE] [DATABASE] SQLite đã fallback an toàn sang journal_mode = TRUNCATE (%s)", truncateMode)
		applySafePragmas(db)
		return truncateMode, nil
	}

	// 4. Fallback 2: DELETE
	var deleteMode string
	deleteErr := db.QueryRow("PRAGMA journal_mode = DELETE;").Scan(&deleteMode)
	deleteMode = strings.ToLower(strings.TrimSpace(deleteMode))

	if deleteErr == nil && deleteMode != "" {
		log.Printf("[ENGINE] [DATABASE] SQLite đã fallback sang journal_mode = DELETE (%s)", deleteMode)
		applySafePragmas(db)
		return deleteMode, nil
	}

	applySafePragmas(db)
	return activeMode, fmt.Errorf("không thể thiết lập journal mode an toàn (wal_err: %v, truncate_err: %v, delete_err: %v)", walErr, truncateErr, deleteErr)
}

// applySafePragmas cấu hình các tham số bảo vệ concurrency và toàn vẹn dữ liệu
func applySafePragmas(db *sql.DB) {
	pragmas := []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA cache_size = -2000;",
		"PRAGMA temp_store = MEMORY;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			log.Printf("[ENGINE] [DATABASE] [WARN] Thực thi pragma '%s' cảnh báo: %v", p, err)
		}
	}
}

// OpenSQLiteConnection mở kết nối SQLite với đường dẫn dbPath
func OpenSQLiteConnection(dbPath string) (*sql.DB, string, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create database directory '%s': %w", dir, err)
	}

	cleanPath := filepath.ToSlash(dbPath)
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", cleanPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open sqlite connection to '%s': %w", dbPath, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, "", fmt.Errorf("ping failed on sqlite database '%s': %w", dbPath, err)
	}

	activeMode, err := ConfigureJournalModeWithFallback(db)
	if err != nil {
		log.Printf("[ENGINE] [DATABASE] [WARN] ConfigureJournalModeWithFallback warning: %v", err)
	}

	if activeMode == "wal" {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(10)
	} else {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	db.SetConnMaxLifetime(time.Hour)

	return db, activeMode, nil
}

// openDatabaseLocked mở kết nối SQLite, cấu hình PRAGMA và khởi tạo DDL schema
func openDatabaseLocked(dbPath string) (*sql.DB, error) {
	db, activeMode, err := OpenSQLiteConnection(dbPath)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(SchemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute database schema DDL: %w", err)
	}

	if err := SeedInitialData(db, filepath.Dir(dbPath)); err != nil {
		log.Printf("[ENGINE] [DATABASE] [WARN] SeedInitialData failed: %v", err)
	}

	log.Printf("[ENGINE] [DATABASE] SQLite initialized successfully at '%s' (journal_mode=%s, foreign_keys=ON)", dbPath, strings.ToUpper(activeMode))
	return db, nil
}

// ResolveDBPath phân giải đường dẫn database SQLite
func ResolveDBPath(customPath ...string) string {
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		return customPath[0]
	}
	if envPath := strings.TrimSpace(os.Getenv("DB_PATH")); envPath != "" {
		return envPath
	}
	return filepath.Join(".", "data", "supportflast.db")
}

// InitSQLite khởi tạo kết nối database SQLite (dùng cho local/testing/offline)
func InitSQLite(customPath ...string) (*sql.DB, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if dbInstance != nil {
		return dbInstance, nil
	}

	targetPath := ResolveDBPath(customPath...)
	db, err := openDatabaseLocked(targetPath)
	if err != nil {
		return nil, err
	}

	dbInstance = db
	activeDriver.Store("sqlite")
	return db, nil
}
