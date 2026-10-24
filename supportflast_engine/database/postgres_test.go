package database

import (
	"os"
	"strings"
	"testing"
)

func TestPostgresConnectionAndRebind(t *testing.T) {
	if GetPostgresURI() == "" {
		t.Skip("Bỏ qua test do chưa cấu hình biến môi trường AIVEN_PG_URI")
	}
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

func TestPostgresUsageHistorySequence(t *testing.T) {
	if GetPostgresURI() == "" {
		t.Skip("Bỏ qua test do chưa cấu hình biến môi trường AIVEN_PG_URI")
	}
	db, err := OpenPostgresConnection()
	if err != nil {
		t.Fatalf("ERR: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE SEQUENCE IF NOT EXISTS omni_usage_history_id_seq;")
	if err != nil {
		t.Fatalf("Err create seq: %v", err)
	}
	_, err = db.Exec("ALTER TABLE omni_usage_history ALTER COLUMN id SET DEFAULT nextval('omni_usage_history_id_seq');")
	if err != nil {
		t.Fatalf("Err alter table: %v", err)
	}

	// Thử INSERT một dòng mà không truyền cột id
	res, err := db.Exec(`
		INSERT INTO omni_usage_history 
		(timestamp, provider, model, connection_id, api_key, endpoint, prompt_tokens, completion_tokens, total_tokens, cost, status, latency_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "2026-10-22T10:24:00Z", "test_prov", "test_model", "test_conn_reg", "test_key", "/test", 10, 20, 30, 0.0, "success", 100)
	if err != nil {
		t.Fatalf("LỖI INSERT SAU KHI FIX SEQUENCE: %v", err)
	}
	t.Logf("✔ INSERT THÀNH CÔNG VÀO POSTGRESQL! res: %v", res)

	// Dọn dòng test
	_, _ = db.Exec("DELETE FROM omni_usage_history WHERE connection_id = 'test_conn_reg'")
}

func TestGetPostgresURI_NoEnv(t *testing.T) {
	oldAiven := os.Getenv("AIVEN_PG_URI")
	oldURL := os.Getenv("DATABASE_URL")
	oldPG := os.Getenv("POSTGRES_URI")
	oldPGQL := os.Getenv("POSTGRESQL_URI")
	oldPGU := os.Getenv("PG_URI")
	defer func() {
		_ = os.Setenv("AIVEN_PG_URI", oldAiven)
		_ = os.Setenv("DATABASE_URL", oldURL)
		_ = os.Setenv("POSTGRES_URI", oldPG)
		_ = os.Setenv("POSTGRESQL_URI", oldPGQL)
		_ = os.Setenv("PG_URI", oldPGU)
	}()

	_ = os.Unsetenv("AIVEN_PG_URI")
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Unsetenv("POSTGRES_URI")
	_ = os.Unsetenv("POSTGRESQL_URI")
	_ = os.Unsetenv("PG_URI")

	uri := GetPostgresURI()
	if uri != "" {
		t.Fatalf("Kỳ vọng uri trả về rỗng khi không có biến môi trường (Audit P0), nhận được: %q", uri)
	}

	// Kiểm tra trường hợp truyền trực tiếp URI rỗng vào OpenPostgresConnection
	_, err := OpenPostgresConnection("   ")
	if err == nil {
		t.Fatal("Kỳ vọng OpenPostgresConnection trả về lỗi khi truyền URI rỗng, nhưng không có lỗi")
	}
	if !strings.Contains(err.Error(), "URI kết nối PostgreSQL rỗng") {
		t.Fatalf("Thông báo lỗi không đúng kỳ vọng: %v", err)
	}
}

func TestGetPostgresURI_WithEnv(t *testing.T) {
	oldAiven := os.Getenv("AIVEN_PG_URI")
	oldURL := os.Getenv("DATABASE_URL")
	defer func() {
		_ = os.Setenv("AIVEN_PG_URI", oldAiven)
		_ = os.Setenv("DATABASE_URL", oldURL)
	}()

	testURI := "postgres://user:secret@localhost:5432/testdb"
	_ = os.Setenv("AIVEN_PG_URI", testURI)
	_ = os.Setenv("DATABASE_URL", "postgres://other:pass@localhost:5432/otherdb")

	uri := GetPostgresURI()
	if uri != testURI {
		t.Fatalf("Kỳ vọng AIVEN_PG_URI được ưu tiên hàng đầu (%s), nhận được: %s", testURI, uri)
	}
}
