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
// Cố gắng kích hoạt 'PRAGMA journal_mode = WAL;'. Nếu gặp lỗi khóa (POSIX lock / shared memory)
// trên các môi trường hosting đặc thù (NFS, GlusterFS, CIFS, Shared Hosting), tự động fallback
// an toàn sang 'TRUNCATE' hoặc 'DELETE' kết hợp 'PRAGMA busy_timeout = 5000;' để đảm bảo ứng dụng
// hoạt động ổn định 100%, không bao giờ bị crash trên mọi loại hosting.
func ConfigureJournalModeWithFallback(db *sql.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database connection is nil")
	}

	// 1. Luôn cấu hình busy_timeout = 5000ms trước tiên để tránh SQLITE_BUSY tức thì
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

	// Gặp lỗi locking hoặc filesystem không hỗ trợ shared memory (-shm POSIX lock)
	log.Printf("[ENGINE] [DATABASE] [WARN] SQLite không thể kích hoạt WAL mode (err=%v, active_mode=%s). Phát hiện môi trường Shared Hosting/Network Volume (NFS, GlusterFS, CIFS). Bắt đầu fallback an toàn...", walErr, activeMode)

	// 3. Fallback 1: Thử PRAGMA journal_mode = TRUNCATE
	// TRUNCATE giữ lại file journal và chỉ set độ dài về 0, tối ưu cho network storage do giảm thao tác xóa/tạo file metadata
	var truncateMode string
	truncateErr := db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&truncateMode)
	truncateMode = strings.ToLower(strings.TrimSpace(truncateMode))

	if truncateErr == nil && (truncateMode == "truncate" || truncateMode == "delete") {
		log.Printf("[ENGINE] [DATABASE] SQLite đã fallback an toàn sang journal_mode = TRUNCATE (chế độ thực tế: %s)", truncateMode)
		applySafePragmas(db)
		return truncateMode, nil
	}

	log.Printf("[ENGINE] [DATABASE] [WARN] Chế độ TRUNCATE thất bại (err=%v, mode=%s), tiếp tục fallback sang DELETE...", truncateErr, truncateMode)

	// 4. Fallback 2: Thử PRAGMA journal_mode = DELETE (Rollback journal truyền thống, tương thích 100% mọi filesystem)
	var deleteMode string
	deleteErr := db.QueryRow("PRAGMA journal_mode = DELETE;").Scan(&deleteMode)
	deleteMode = strings.ToLower(strings.TrimSpace(deleteMode))

	if deleteErr == nil && deleteMode != "" {
		log.Printf("[ENGINE] [DATABASE] SQLite đã fallback an toàn sang journal_mode = DELETE (chế độ thực tế: %s)", deleteMode)
		applySafePragmas(db)
		return deleteMode, nil
	}

	// Đảm bảo các pragma an toàn vẫn được thực thi
	applySafePragmas(db)
	return activeMode, fmt.Errorf("không thể thiết lập journal mode an toàn (wal_err: %v, truncate_err: %v, delete_err: %v)", walErr, truncateErr, deleteErr)
}

// applySafePragmas cấu hình các tham số bảo vệ concurrency và toàn vẹn dữ liệu
func applySafePragmas(db *sql.DB) {
	pragmas := []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA cache_size = -2000;", // Giới hạn cache DB tối đa 2MB RAM (chống phình RAM trên Shared Hosting)
		"PRAGMA temp_store = MEMORY;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			log.Printf("[ENGINE] [DATABASE] [WARN] Thực thi pragma '%s' cảnh báo: %v", p, err)
		}
	}
}

// OpenSQLiteConnection mở kết nối SQLite với đường dẫn dbPath,
// tự động tạo thư mục cha nếu chưa có, cấu hình DSN an toàn,
// thiết lập connection pool, kiểm tra ping và áp dụng fallback journal mode.
func OpenSQLiteConnection(dbPath string) (*sql.DB, string, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create database directory '%s': %w", dir, err)
	}

	cleanPath := filepath.ToSlash(dbPath)
	// DSN cấu hình busy_timeout, foreign_keys và synchronous mà không ép cứng journal_mode trong DSN
	// để cho phép hàm ConfigureJournalModeWithFallback thương lượng chế độ phù hợp với filesystem
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", cleanPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open sqlite connection to '%s': %w", dbPath, err)
	}

	// Kiểm tra kết nối ban đầu
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, "", fmt.Errorf("ping failed on sqlite database '%s': %w", dbPath, err)
	}

	// Áp dụng cơ chế journal_mode với fallback an toàn cho mọi loại hosting
	activeMode, err := ConfigureJournalModeWithFallback(db)
	if err != nil {
		log.Printf("[ENGINE] [DATABASE] [WARN] ConfigureJournalModeWithFallback returned warning: %v", err)
	}

	// Cấu hình Connection Pool tối ưu theo chế độ journal (Rule PHAN 7.1):
	// - WAL mode: cho phép đa kết nối đọc đồng thời (MaxOpenConns=25)
	// - TRUNCATE / DELETE mode (Shared Hosting / Network Volumes): SQLite cần MaxOpenConns=1
	//   để tuần tự hóa các thao tác ghi qua pool, loại trừ triệt để lỗi locking (SQLITE_BUSY)
	if activeMode == "wal" {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(10)
	} else {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		log.Printf("[ENGINE] [DATABASE] Tự động cấu hình MaxOpenConns=1 cho chế độ %s để ngăn ngừa xung đột khóa kết nối", strings.ToUpper(activeMode))
	}
	db.SetConnMaxLifetime(time.Hour)

	return db, activeMode, nil
}

// openDatabaseLocked mở kết nối SQLite, áp dụng PRAGMA với fallback an toàn và migrate schema
func openDatabaseLocked(dbPath string) (*sql.DB, error) {
	db, activeMode, err := OpenSQLiteConnection(dbPath)
	if err != nil {
		return nil, err
	}

	// Khởi tạo Schema DDL (Tạo các bảng và chỉ mục nếu chưa tồn tại)
	if _, err := db.Exec(SchemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute database schema DDL: %w", err)
	}

	// Tự động seed tài khoản admin chuẩn nếu bảng users rỗng
	if err := SeedInitialData(db); err != nil {
		log.Printf("[ENGINE] [DATABASE] [WARN] SeedInitialData failed: %v", err)
	}

	log.Printf("[ENGINE] [DATABASE] SQLite initialized successfully at '%s' (journal_mode=%s, foreign_keys=ON)", dbPath, strings.ToUpper(activeMode))
	return db, nil
}
