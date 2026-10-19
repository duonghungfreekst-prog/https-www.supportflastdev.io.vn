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
	t.Logf("✔ Test pgcompat hoàn tất 100%% thành công!")
}
