package main

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// Cấu hình TiDB Cloud
const (
	tidbHost     = "gateway01.ap-southeast-1.prod.aws.tidbcloud.com"
	tidbPort     = 4000
	tidbUser     = "2KGt5QqixkveQPP.root"
	tidbPassword = "JIxWb1nGVINnKzap"
	tidbDatabase = "supportflast"
)

// Đường dẫn SQLite
const (
	mainDBPath    = `f:\supportflast.dev\data\supportflast.db`
	storageDBPath = `f:\supportflast.dev\data\cloudpool_metadata.db`
)

// 8 bảng chính trong supportflast.db
var mainTables = []struct {
	name    string
	columns []string
}{
	{"users", []string{"id", "username", "email", "password_hash", "display_name", "role", "avatar", "created_at", "updated_at", "last_login"}},
	{"apps", []string{"id", "name", "version", "platform", "category", "`desc`", "file_name", "size_bytes", "size_formatted", "sha256", "author", "downloads", "status", "published_at", "download_url", "video_url", "guide", "user_id"}},
	{"api_keys", []string{"id", "user_id", "name", "key_hash", "prefix", "status", "permissions", "created_at"}},
	{"reviews", []string{"id", "app_id", "user_id", "author_name", "author_role", "stars", "text", "status", "created_at"}},
	{"audit_logs", []string{"id", "user_id", "action", "ip_address", "user_agent", "details", "created_at"}},
	{"system_releases", []string{"version", "title", "date", "build_hash", "notes", "published_by"}},
	{"security_events", []string{"id", "event_type", "ip_address", "severity", "details", "blocked_until", "created_at"}},
	{"revoked_tokens", []string{"token_hash", "expires_at", "revoked_at"}},
}

func main() {
	log.Println("========================================================")
	log.Println("[MIGRATION] Bắt đầu đồng bộ dữ liệu SQLite → TiDB Cloud")
	log.Println("========================================================")

	// 1. Kết nối TiDB Cloud
	tidbDB, err := connectTiDB()
	if err != nil {
		log.Fatalf("[MIGRATION] [FATAL] Không thể kết nối TiDB Cloud: %v", err)
	}
	defer tidbDB.Close()
	log.Println("[MIGRATION] [OK] Đã kết nối TiDB Cloud thành công!")

	// 2. Migrate supportflast.db (8 bảng chính)
	if _, err := os.Stat(mainDBPath); err == nil {
		log.Printf("[MIGRATION] Đang mở SQLite: %s", mainDBPath)
		sqliteMain, err := sql.Open("sqlite", mainDBPath+"?mode=ro")
		if err != nil {
			log.Printf("[MIGRATION] [ERROR] Không thể mở %s: %v", mainDBPath, err)
		} else {
			defer sqliteMain.Close()
			for _, t := range mainTables {
				migrateTable(sqliteMain, tidbDB, t.name, t.columns)
			}
		}
	} else {
		log.Printf("[MIGRATION] [SKIP] File %s không tồn tại", mainDBPath)
	}

	// 3. Migrate cloudpool_metadata.db (dynamic tables)
	if _, err := os.Stat(storageDBPath); err == nil {
		log.Printf("[MIGRATION] Đang mở SQLite: %s", storageDBPath)
		sqliteStorage, err := sql.Open("sqlite", storageDBPath+"?mode=ro")
		if err != nil {
			log.Printf("[MIGRATION] [ERROR] Không thể mở %s: %v", storageDBPath, err)
		} else {
			defer sqliteStorage.Close()
			migrateCloudPoolTables(sqliteStorage, tidbDB)
		}
	} else {
		log.Printf("[MIGRATION] [SKIP] File %s không tồn tại", storageDBPath)
	}

	log.Println("========================================================")
	log.Println("[MIGRATION] HOÀN TẤT đồng bộ SQLite → TiDB Cloud!")
	log.Println("========================================================")
}

func connectTiDB() (*sql.DB, error) {
	// Đăng ký TLS config
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if err := mysql.RegisterTLSConfig("tidb", tlsConfig); err != nil {
		return nil, fmt.Errorf("lỗi đăng ký TLS: %w", err)
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true&loc=UTC&tls=tidb&timeout=30s",
		tidbUser, tidbPassword, tidbHost, tidbPort, tidbDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping TiDB thất bại: %w", err)
	}
	return db, nil
}

func migrateTable(src *sql.DB, dst *sql.DB, tableName string, columns []string) {
	log.Printf("[MIGRATION] --- Bảng '%s' ---", tableName)

	// Đếm bản ghi nguồn
	var srcCount int
	selectCols := make([]string, len(columns))
	for i, c := range columns {
		if c == "`desc`" {
			selectCols[i] = "`desc`"
		} else {
			selectCols[i] = c
		}
	}

	err := src.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&srcCount)
	if err != nil {
		log.Printf("[MIGRATION] [WARN] Không thể đếm bảng '%s' trong SQLite: %v", tableName, err)
		return
	}
	log.Printf("[MIGRATION]   Nguồn SQLite: %d bản ghi", srcCount)

	if srcCount == 0 {
		log.Printf("[MIGRATION]   → Bảng rỗng, bỏ qua")
		return
	}

	// SELECT * FROM sqlite
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectCols, ", "), tableName)
	rows, err := src.Query(query)
	if err != nil {
		log.Printf("[MIGRATION] [ERROR] Lỗi truy vấn bảng '%s': %v", tableName, err)
		return
	}
	defer rows.Close()

	// Chuẩn bị INSERT IGNORE
	tidbCols := make([]string, len(columns))
	for i, c := range columns {
		if c == "`desc`" {
			tidbCols[i] = "`desc`"
		} else {
			tidbCols[i] = "`" + c + "`"
		}
	}
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT IGNORE INTO `%s` (%s) VALUES (%s)",
		tableName, strings.Join(tidbCols, ", "), strings.Join(placeholders, ", "))

	migrated := 0
	skipped := 0
	errCount := 0

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("[MIGRATION] [ERROR] Lỗi đọc dòng bảng '%s': %v", tableName, err)
			errCount++
			continue
		}

		// Xử lý giá trị NULL và chuyển đổi kiểu dữ liệu
		for i := range values {
			if b, ok := values[i].([]byte); ok {
				values[i] = string(b)
			}
		}

		result, err := dst.Exec(insertSQL, values...)
		if err != nil {
			log.Printf("[MIGRATION] [ERROR] Lỗi INSERT bảng '%s': %v", tableName, err)
			errCount++
			continue
		}
		affected, _ := result.RowsAffected()
		if affected > 0 {
			migrated++
		} else {
			skipped++
		}
	}

	log.Printf("[MIGRATION]   → Đã chèn: %d | Đã tồn tại (bỏ qua): %d | Lỗi: %d", migrated, skipped, errCount)
}

func migrateCloudPoolTables(src *sql.DB, dst *sql.DB) {
	log.Println("[MIGRATION] === CloudPool Metadata (dynamic migration) ===")

	// Lấy danh sách bảng từ SQLite
	rows, err := src.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		log.Printf("[MIGRATION] [ERROR] Không thể liệt kê bảng CloudPool: %v", err)
		return
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}

	log.Printf("[MIGRATION] Tìm thấy %d bảng CloudPool: %s", len(tables), strings.Join(tables, ", "))

	for _, tableName := range tables {
		migrateCloudPoolTable(src, dst, tableName)
	}
}

func migrateCloudPoolTable(src *sql.DB, dst *sql.DB, tableName string) {
	log.Printf("[MIGRATION] --- CloudPool bảng '%s' ---", tableName)

	// Lấy thông tin cột từ PRAGMA
	pragmaRows, err := src.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		log.Printf("[MIGRATION] [ERROR] Không thể lấy schema bảng '%s': %v", tableName, err)
		return
	}
	defer pragmaRows.Close()

	var columns []string
	for pragmaRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue interface{}
		var pk int
		if err := pragmaRows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err == nil {
			columns = append(columns, name)
		}
	}

	if len(columns) == 0 {
		log.Printf("[MIGRATION] [WARN] Bảng '%s' không có cột nào!", tableName)
		return
	}

	// Kiểm tra bảng có tồn tại trong TiDB không
	var tableExists int
	checkQuery := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?"
	err = dst.QueryRow(checkQuery, tidbDatabase, tableName).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		log.Printf("[MIGRATION] [SKIP] Bảng '%s' chưa tồn tại trong TiDB Cloud, bỏ qua", tableName)
		return
	}

	migrateTable(src, dst, tableName, columns)
}
