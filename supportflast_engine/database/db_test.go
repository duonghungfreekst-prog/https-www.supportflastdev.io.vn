package database

import (
	"strings"
	"testing"
)

func TestActiveDriver_AlwaysPostgres(t *testing.T) {
	drv := ActiveDriver()
	if drv != "postgres" {
		t.Fatalf("Kỳ vọng ActiveDriver là 'postgres', thực tế: '%s'", drv)
	}

	aliasDrv := GetActiveDriver()
	if aliasDrv != "postgres" {
		t.Fatalf("Kỳ vọng GetActiveDriver là 'postgres', thực tế: '%s'", aliasDrv)
	}
}

func TestGetInitialAdminPassword(t *testing.T) {
	pw := GetInitialAdminPassword()
	if len(pw) < 8 {
		t.Fatalf("Mật khẩu admin khởi tạo quá ngắn: '%s'", pw)
	}
}

func TestSchemaDDL_PostgresCompatibility(t *testing.T) {
	if !strings.Contains(SchemaDDL, "CREATE TABLE IF NOT EXISTS users") {
		t.Fatal("SchemaDDL thiếu bảng users")
	}
	if !strings.Contains(SchemaDDL, "CREATE TABLE IF NOT EXISTS apps") {
		t.Fatal("SchemaDDL thiếu bảng apps")
	}
	if !strings.Contains(SchemaDDL, "CREATE TABLE IF NOT EXISTS api_keys") {
		t.Fatal("SchemaDDL thiếu bảng api_keys")
	}
	if !strings.Contains(SchemaDDL, "CREATE TABLE IF NOT EXISTS audit_logs") {
		t.Fatal("SchemaDDL thiếu bảng audit_logs")
	}
}

func TestRebindPostgres(t *testing.T) {
	// Kiểm tra rebind dấu '?' sang '$1', '$2'
	query := "SELECT * FROM users WHERE username = ? AND role = ?"
	rebound := RebindPostgres(query)
	expected := "SELECT * FROM users WHERE username = $1 AND role = $2"
	if rebound != expected {
		t.Fatalf("RebindPostgres sai: kỳ vọng '%s', thực tế '%s'", expected, rebound)
	}

	// Kiểm tra INSERT IGNORE INTO -> INSERT INTO ... ON CONFLICT DO NOTHING
	insertQ := "INSERT IGNORE INTO users (id, username) VALUES (?, ?)"
	reboundInsert := RebindPostgres(insertQ)
	if !strings.Contains(reboundInsert, "INSERT INTO users") || !strings.Contains(reboundInsert, "ON CONFLICT DO NOTHING") {
		t.Fatalf("RebindPostgres cho INSERT IGNORE không đúng: '%s'", reboundInsert)
	}
}
