package database

import (
	"testing"
)

func TestPostgresConnectionAndRebind(t *testing.T) {
	db, err := OpenPostgresConnection()
	if err != nil {
		t.Fatalf("Không thể kết nối Aiven PostgreSQL: %v", err)
	}
	defer db.Close()

	// 1. Thử câu lệnh SELECT dùng placeholder '?'
	var userCount int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE role = ?", "admin").Scan(&userCount)
	if err != nil {
		t.Fatalf("Lỗi truy vấn với placeholder '?': %v", err)
	}
	t.Logf("✔ Đếm admin users qua placeholder '?': %d", userCount)

	// 2. Thử câu lệnh với backtick `desc`
	var appCount int
	err = db.QueryRow("SELECT COUNT(*) FROM apps WHERE `status` = ?", "Đã xuất bản").Scan(&appCount)
	if err != nil {
		t.Fatalf("Lỗi truy vấn với backticks: %v", err)
	}
	t.Logf("✔ Đếm apps qua backticks: %d", appCount)

	// 3. Thử INSERT IGNORE INTO
	res, err := db.Exec("INSERT IGNORE INTO users (id, username, email, password_hash) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING;",
		"test-id-pgcompat", "test_user_compat", "test@compat.dev", "hash")
	if err != nil {
		t.Fatalf("Lỗi INSERT IGNORE INTO: %v", err)
	}
	_ = res

	// Dọn dẹp dòng test
	_, _ = db.Exec("DELETE FROM users WHERE id = ?", "test-id-pgcompat")
	// 4. Kiểm tra tự động chuyển đổi BOOLEAN (is_deleted = 0, is_dir = 1...) cho PostgreSQL
	var countFiles int
	err = db.QueryRow("SELECT COUNT(*) FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0", "root").Scan(&countFiles)
	if err != nil {
		t.Fatalf("Lỗi truy vấn với is_deleted = 0 qua pgcompat: %v", err)
	}
	t.Logf("✔ Đếm virtual_files qua is_deleted = 0: %d", countFiles)

	// 5. Kiểm tra toàn bộ câu query ListVirtualFiles từ handlers_files
	rows, err := db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, "root")
	if err != nil {
		t.Fatalf("Lỗi ListVirtualFiles qua pgcompat: %v", err)
	}
	defer rows.Close()
	rowCount := 0
	for rows.Next() {
		rowCount++
	}
	t.Logf("✔ Query ListVirtualFiles thành công! Lấy được: %d tệp/thư mục", rowCount)

	t.Logf("✔ Test pgcompat hoàn tất 100%% thành công!")
}
