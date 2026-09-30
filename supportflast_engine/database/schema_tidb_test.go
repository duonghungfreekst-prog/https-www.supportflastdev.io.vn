package database

import (
	"strings"
	"testing"
	"time"
)

func TestTiDBSchemaDDL_ContainsRequiredTables(t *testing.T) {
	requiredTables := []string{
		"users",
		"apps",
		"api_keys",
		"reviews",
		"audit_logs",
		"system_releases",
		"security_events",
		"revoked_tokens",
	}

	if len(TiDBTableMigrations) != len(requiredTables) {
		t.Fatalf("Kỳ vọng đúng %d bảng trong TiDBTableMigrations, thực tế: %d", len(requiredTables), len(TiDBTableMigrations))
	}

	for i, expected := range requiredTables {
		actual := TiDBTableMigrations[i].Name
		if actual != expected {
			t.Errorf("Bảng tại vị trí %d: kỳ vọng '%s', thực tế '%s'", i, expected, actual)
		}
	}
}

func TestTiDBSchemaDDL_SyntaxAndKeywords(t *testing.T) {
	for _, m := range TiDBTableMigrations {
		ddl := m.DDL

		// 1. Kiểm tra CREATE TABLE IF NOT EXISTS
		if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS") {
			t.Errorf("Bảng %s thiếu mệnh đề CREATE TABLE IF NOT EXISTS", m.Name)
		}

		// 2. Kiểm tra charset utf8mb4 và collation
		if !strings.Contains(ddl, "utf8mb4") {
			t.Errorf("Bảng %s thiếu charset utf8mb4", m.Name)
		}
		if !strings.Contains(ddl, "ENGINE=InnoDB") {
			t.Errorf("Bảng %s thiếu ENGINE=InnoDB", m.Name)
		}

		// 3. Kiểm tra kiểu dữ liệu VARCHAR
		if !strings.Contains(ddl, "VARCHAR") {
			t.Errorf("Bảng %s thiếu kiểu dữ liệu VARCHAR", m.Name)
		}

		// 4. Kiểm tra kiểu dữ liệu DATETIME
		if !strings.Contains(ddl, "DATETIME") {
			t.Errorf("Bảng %s thiếu kiểu dữ liệu DATETIME", m.Name)
		}

		// 5. Kiểm tra chỉ mục INDEX / PRIMARY KEY
		if !strings.Contains(ddl, "PRIMARY KEY") {
			t.Errorf("Bảng %s thiếu PRIMARY KEY", m.Name)
		}
		if !strings.Contains(ddl, "INDEX") {
			t.Errorf("Bảng %s thiếu INDEX", m.Name)
		}
	}
}

func TestTiDBSchemaDDL_ForeignKeys(t *testing.T) {
	tablesWithFK := map[string][]string{
		"apps":       {"fk_apps_user_id", "FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)"},
		"api_keys":   {"fk_api_keys_user_id", "FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)"},
		"reviews":    {"fk_reviews_app_id", "fk_reviews_user_id", "REFERENCES `apps` (`id`)", "REFERENCES `users` (`id`)"},
		"audit_logs": {"fk_audit_logs_user_id", "FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)"},
	}

	for tableName, expectedClauses := range tablesWithFK {
		var foundDDL string
		for _, m := range TiDBTableMigrations {
			if m.Name == tableName {
				foundDDL = m.DDL
				break
			}
		}
		if foundDDL == "" {
			t.Fatalf("Không tìm thấy bảng '%s' trong migrations", tableName)
		}

		for _, clause := range expectedClauses {
			if !strings.Contains(foundDDL, clause) {
				t.Errorf("Bảng '%s' thiếu ràng buộc khóa ngoại: '%s'", tableName, clause)
			}
		}
	}
}

func TestTiDBSchemaDDL_TextFields(t *testing.T) {
	tablesWithText := []string{"apps", "api_keys", "reviews", "audit_logs", "system_releases", "security_events"}
	for _, tableName := range tablesWithText {
		var foundDDL string
		for _, m := range TiDBTableMigrations {
			if m.Name == tableName {
				foundDDL = m.DDL
				break
			}
		}
		if !strings.Contains(foundDDL, "TEXT") {
			t.Errorf("Bảng '%s' kỳ vọng có trường kiểu TEXT", tableName)
		}
	}
}

func TestMigrateTiDBSchema_NilDB(t *testing.T) {
	err := MigrateTiDBSchema(nil)
	if err == nil {
		t.Error("Kỳ vọng lỗi khi truyền db = nil vào MigrateTiDBSchema")
	}
}

func TestVerifyTiDBSchema_NilDB(t *testing.T) {
	_, err := VerifyTiDBSchema(nil, "supportflast")
	if err == nil {
		t.Error("Kỳ vọng lỗi khi truyền db = nil vào VerifyTiDBSchema")
	}
}

func TestTiDBConfig_FormatDSN(t *testing.T) {
	cfg := TiDBConfig{
		Host:            "gateway01.ap-southeast-1.prod.aws.tidbcloud.com",
		Port:            4000,
		User:            "admin.root",
		Password:        "Secret@123",
		Database:        "supportflast",
		TLS:             "true",
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 10 * time.Minute,
	}

	dsn := cfg.FormatDSN()
	if !strings.Contains(dsn, "gateway01.ap-southeast-1.prod.aws.tidbcloud.com:4000") {
		t.Errorf("DSN không chứa host:port chính xác: %s", dsn)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("DSN thiếu charset=utf8mb4: %s", dsn)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN thiếu parseTime=true: %s", dsn)
	}
	if !strings.Contains(dsn, "tls=true") {
		t.Errorf("DSN thiếu tls=true: %s", dsn)
	}
}
