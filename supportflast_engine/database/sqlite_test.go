package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenSQLiteConnection_WALAndPragmas(t *testing.T) {
	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_db")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_open_sqlite_conn.db")
	_ = os.Remove(testDBPath)
	defer func() {
		_ = os.Remove(testDBPath)
		_ = os.Remove(testDBPath + "-wal")
		_ = os.Remove(testDBPath + "-shm")
	}()

	db, mode, err := OpenSQLiteConnection(testDBPath)
	if err != nil {
		t.Fatalf("OpenSQLiteConnection failed: %v", err)
	}
	defer db.Close()

	if mode != "wal" && mode != "truncate" && mode != "delete" {
		t.Errorf("Unexpected journal mode returned: %s", mode)
	}

	// 1. Kiểm tra busy_timeout = 5000ms
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout;").Scan(&timeout); err != nil {
		t.Fatalf("Failed to query PRAGMA busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("Kỳ vọng busy_timeout = 5000ms, thực tế: %d", timeout)
	}

	// 2. Kiểm tra foreign_keys = 1 (ON)
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&fk); err != nil {
		t.Fatalf("Failed to query PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("Kỳ vọng foreign_keys = 1, thực tế: %d", fk)
	}
}

func TestConfigureJournalModeWithFallback_FallbackModes(t *testing.T) {
	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_db")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_fallback_modes.db")
	_ = os.Remove(testDBPath)
	defer func() {
		_ = os.Remove(testDBPath)
		_ = os.Remove(testDBPath + "-wal")
		_ = os.Remove(testDBPath + "-shm")
	}()

	cleanPath := filepath.ToSlash(testDBPath)
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", cleanPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	// 1. Kiểm tra chế độ WAL mặc định
	mode, err := ConfigureJournalModeWithFallback(db)
	if err != nil {
		t.Fatalf("ConfigureJournalModeWithFallback failed: %v", err)
	}
	if mode != "wal" && mode != "truncate" && mode != "delete" {
		t.Fatalf("Unexpected active mode: %s", mode)
	}

	// 2. Mô phỏng kịch bản NFS / GlusterFS / CIFS / Shared Hosting:
	// Khi WAL mode không thể dùng được (hoặc bị ép buộc chuyển sang TRUNCATE/DELETE)
	// Đảm bảo TRUNCATE fallback hoạt động hoàn hảo:
	var truncateMode string
	if err := db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&truncateMode); err != nil {
		t.Fatalf("Failed to set TRUNCATE: %v", err)
	}
	applySafePragmas(db)

	var activeMode string
	_ = db.QueryRow("PRAGMA journal_mode;").Scan(&activeMode)
	if activeMode != "truncate" && activeMode != "delete" {
		t.Errorf("Kỳ vọng journal_mode TRUNCATE hoặc DELETE, thực tế: %s", activeMode)
	}

	// 3. Đảm bảo DELETE fallback hoạt động hoàn hảo:
	var deleteMode string
	if err := db.QueryRow("PRAGMA journal_mode = DELETE;").Scan(&deleteMode); err != nil {
		t.Fatalf("Failed to set DELETE: %v", err)
	}
	applySafePragmas(db)

	_ = db.QueryRow("PRAGMA journal_mode;").Scan(&activeMode)
	if activeMode != "delete" {
		t.Errorf("Kỳ vọng journal_mode DELETE, thực tế: %s", activeMode)
	}

	// 4. Kiểm tra busy_timeout vẫn luôn là 5000ms sau fallback
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout;").Scan(&timeout); err != nil {
		t.Fatalf("Failed to query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("Kỳ vọng busy_timeout = 5000ms sau fallback, thực tế: %d", timeout)
	}
}

func TestFallbackJournalMode_ConcurrentAccess(t *testing.T) {
	// Kiểm tra độ tin cậy khi chạy ở chế độ TRUNCATE (mô phỏng NFS / Network volume)
	// với nhiều goroutines đọc ghi đồng thời kèm busy_timeout = 5000ms
	testDir := filepath.Join(`f:\supportflast.dev\data`, "test_db")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_concurrent_truncate.db")
	_ = os.Remove(testDBPath)
	defer func() {
		_ = os.Remove(testDBPath)
		_ = os.Remove(testDBPath + "-journal")
	}()

	cleanPath := filepath.ToSlash(testDBPath)
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", cleanPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	// Ép sang TRUNCATE mode để mô phỏng môi trường Shared Hosting / Network Volume
	var mode string
	_ = db.QueryRow("PRAGMA journal_mode = TRUNCATE;").Scan(&mode)
	applySafePragmas(db)
	db.SetMaxOpenConns(1)
	// Tuân thủ quy tắc SQLite non-WAL mode: tuần tự hóa kết nối qua connection pool MaxOpenConns=1
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS test_counter (id INTEGER PRIMARY KEY, count INTEGER);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = db.Exec("INSERT OR REPLACE INTO test_counter (id, count) VALUES (1, 0);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var wg sync.WaitGroup
	workers := 15
	errChan := make(chan error, workers*2)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Đọc
			var count int
			if err := db.QueryRow("SELECT count FROM test_counter WHERE id = 1;").Scan(&count); err != nil {
				errChan <- fmt.Errorf("worker %d read failed: %w", workerID, err)
				return
			}
			// Ghi
			if _, err := db.Exec("UPDATE test_counter SET count = count + 1 WHERE id = 1;"); err != nil {
				errChan <- fmt.Errorf("worker %d write failed: %w", workerID, err)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent access error in TRUNCATE fallback mode: %v", err)
	}

	var finalCount int
	if err := db.QueryRow("SELECT count FROM test_counter WHERE id = 1;").Scan(&finalCount); err != nil {
		t.Fatalf("Final count scan failed: %v", err)
	}
	if finalCount != workers {
		t.Errorf("Kỳ vọng final count = %d, thực tế: %d", workers, finalCount)
	}
}
