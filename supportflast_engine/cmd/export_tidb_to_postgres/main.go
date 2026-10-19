package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"supportflast_engine/database"
)

// List of all 25 tables to export from TiDB to PostgreSQL
var allTables = []string{
	// 1. SupportFlast Core
	"users",
	"apps",
	"api_keys",
	"reviews",
	"audit_logs",
	"system_releases",
	"security_events",
	"revoked_tokens",

	// 2. CloudPool VFS & Storage
	"accounts",
	"cloudpool_users",
	"virtual_files",
	"file_chunks",
	"activity_logs",
	"login_sessions",
	"settings",
	"file_access_otps",
	"file_access_requests",
	"public_shares",
	"gdrive_backups",

	// 3. OmniRouter AI Gateway
	"omni_providers",
	"omni_client_keys",
	"omni_settings",
	"omni_proxy_pools",
	"omni_kv",
	"omni_usage_history",
}

// Map MySQL types to PostgreSQL types
func mapMySQLTypeToPostgres(mysqlType string) string {
	lower := strings.ToLower(mysqlType)
	switch {
	case strings.Contains(lower, "bigint") && strings.Contains(lower, "auto_increment"):
		return "BIGSERIAL PRIMARY KEY"
	case strings.Contains(lower, "int") && strings.Contains(lower, "auto_increment"):
		return "SERIAL PRIMARY KEY"
	case strings.Contains(lower, "tinyint(1)"):
		return "BOOLEAN"
	case strings.Contains(lower, "tinyint") || strings.Contains(lower, "smallint"):
		return "SMALLINT"
	case strings.Contains(lower, "bigint"):
		return "BIGINT"
	case strings.Contains(lower, "int"):
		return "INTEGER"
	case strings.Contains(lower, "longtext") || strings.Contains(lower, "mediumtext") || strings.Contains(lower, "text"):
		return "TEXT"
	case strings.Contains(lower, "varchar"):
		return "TEXT" // PostgreSQL handles TEXT efficiently
	case strings.Contains(lower, "datetime") || strings.Contains(lower, "timestamp"):
		return "TIMESTAMP WITH TIME ZONE"
	case strings.Contains(lower, "double") || strings.Contains(lower, "float"):
		return "DOUBLE PRECISION"
	case strings.Contains(lower, "blob"):
		return "BYTEA"
	default:
		return "TEXT"
	}
}

func escapePGValue(val interface{}) string {
	return escapePGValueWithType(val, "")
}

func escapePGValueWithType(val interface{}, pgType string) string {
	if val == nil {
		return "NULL"
	}
	if pgType == "BOOLEAN" {
		switch v := val.(type) {
		case bool:
			if v {
				return "TRUE"
			}
			return "FALSE"
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			if fmt.Sprintf("%d", v) == "1" {
				return "TRUE"
			}
			return "FALSE"
		case []byte:
			s := strings.TrimSpace(string(v))
			if s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "t") {
				return "TRUE"
			}
			return "FALSE"
		case string:
			s := strings.TrimSpace(v)
			if s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "t") {
				return "TRUE"
			}
			return "FALSE"
		default:
			s := fmt.Sprintf("%v", v)
			if s == "1" || strings.EqualFold(s, "true") {
				return "TRUE"
			}
			return "FALSE"
		}
	}
	switch v := val.(type) {
	case []byte:
		s := string(v)
		// Escape single quotes for PostgreSQL
		s = strings.ReplaceAll(s, "'", "''")
		return "'" + s + "'"
	case string:
		s := strings.ReplaceAll(v, "'", "''")
		return "'" + s + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case float32, float64:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case time.Time:
		return fmt.Sprintf("'%s'", v.Format(time.RFC3339))
	default:
		s := fmt.Sprintf("%v", v)
		s = strings.ReplaceAll(s, "'", "''")
		return "'" + s + "'"
	}
}

func main() {
	log.Println("[MIGRATION] Bắt đầu xuất toàn bộ CSDL TiDB Cloud sang chuẩn PostgreSQL Aiven...")

	cfg := database.DefaultTiDBConfig()
	cfg.AutoMigrate = false
	db, err := database.OpenTiDBConnection(cfg)
	if err != nil {
		log.Fatalf("[MIGRATION] Lỗi kết nối TiDB Cloud: %v", err)
	}
	defer db.Close()

	outputDir := filepath.Join("..", "data")
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		outputDir = "data"
	}
	_ = os.MkdirAll(outputDir, 0755)

	targetFile := filepath.Join(outputDir, "supportflast_aiven_postgres.sql")
	f, err := os.Create(targetFile)
	if err != nil {
		log.Fatalf("[MIGRATION] Lỗi tạo file đích %s: %v", targetFile, err)
	}
	defer f.Close()

	// Header
	fmt.Fprintf(f, "-- ======================================================================\n")
	fmt.Fprintf(f, "-- SUPPORTFLAST / OMNIROUTER POSTGRESQL DUMP CHO AIVEN POSTGRES\n")
	fmt.Fprintf(f, "-- Nguồn xuất: TiDB Cloud Serverless (supportflast)\n")
	fmt.Fprintf(f, "-- Đích: Aiven PostgreSQL (pg-333a3094)\n")
	fmt.Fprintf(f, "-- Thời gian xuất: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "-- ======================================================================\n\n")
	fmt.Fprintf(f, "SET client_encoding = 'UTF8';\n")
	fmt.Fprintf(f, "SET standard_conforming_strings = on;\n")
	fmt.Fprintf(f, "SET check_function_bodies = false;\n")
	fmt.Fprintf(f, "SET client_min_messages = warning;\n\n")

	totalExportedRows := 0

	for _, tbl := range allTables {
		// Kiểm tra bảng có tồn tại trong TiDB không
		var exists int
		err := db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE table_schema = 'supportflast' AND table_name = ?", tbl).Scan(&exists)
		if err != nil || exists == 0 {
			log.Printf("[MIGRATION] Bỏ qua bảng %s (không tồn tại trong TiDB)", tbl)
			continue
		}

		// Lấy cấu trúc cột từ information_schema.COLUMNS
		colRows, err := db.Query(`
			SELECT column_name, data_type, column_type, is_nullable, column_default, extra
			FROM information_schema.COLUMNS 
			WHERE table_schema = 'supportflast' AND table_name = ?
			ORDER BY ordinal_position ASC
		`, tbl)
		if err != nil {
			log.Printf("[MIGRATION] Lỗi lấy cột bảng %s: %v", tbl, err)
			continue
		}

		type ColInfo struct {
			Name     string
			DataType string
			ColType  string
			Nullable string
			Default  sql.NullString
			Extra    string
		}

		var cols []ColInfo
		for colRows.Next() {
			var c ColInfo
			_ = colRows.Scan(&c.Name, &c.DataType, &c.ColType, &c.Nullable, &c.Default, &c.Extra)
			cols = append(cols, c)
		}
		colRows.Close()

		if len(cols) == 0 {
			continue
		}

		// Lấy Primary Keys
		pkRows, err := db.Query(`
			SELECT column_name 
			FROM information_schema.KEY_COLUMN_USAGE 
			WHERE table_schema = 'supportflast' AND table_name = ? AND constraint_name = 'PRIMARY'
			ORDER BY ordinal_position ASC
		`, tbl)
		var pks []string
		if err == nil {
			for pkRows.Next() {
				var pkCol string
				_ = pkRows.Scan(&pkCol)
				pks = append(pks, pkCol)
			}
			pkRows.Close()
		}

		// Tạo câu lệnh CREATE TABLE cho PostgreSQL
		fmt.Fprintf(f, "-- ----------------------------------------------------------------------\n")
		fmt.Fprintf(f, "-- Bảng: %s\n", tbl)
		fmt.Fprintf(f, "-- ----------------------------------------------------------------------\n")
		fmt.Fprintf(f, "CREATE TABLE IF NOT EXISTS \"%s\" (\n", tbl)

		var colDefs []string
		hasSerialPK := false
		for _, c := range cols {
			pgType := mapMySQLTypeToPostgres(c.ColType)
			if strings.Contains(pgType, "SERIAL PRIMARY KEY") {
				hasSerialPK = true
				colDefs = append(colDefs, fmt.Sprintf("    \"%s\" %s", c.Name, pgType))
				continue
			}

			nullStr := ""
			if c.Nullable == "NO" {
				nullStr = " NOT NULL"
			}
			defStr := ""
			if c.Default.Valid {
				d := c.Default.String
				if pgType == "BOOLEAN" {
					if d == "1" || d == "'1'" || strings.EqualFold(d, "true") {
						defStr = " DEFAULT TRUE"
					} else {
						defStr = " DEFAULT FALSE"
					}
				} else if d == "CURRENT_TIMESTAMP" {
					defStr = " DEFAULT CURRENT_TIMESTAMP"
				} else if d != "" {
					defStr = fmt.Sprintf(" DEFAULT '%s'", strings.ReplaceAll(d, "'", "''"))
				}
			}
			colDefs = append(colDefs, fmt.Sprintf("    \"%s\" %s%s%s", c.Name, pgType, nullStr, defStr))
		}

		if len(pks) > 0 && !hasSerialPK {
			var quotedPKs []string
			for _, pk := range pks {
				quotedPKs = append(quotedPKs, fmt.Sprintf("\"%s\"", pk))
			}
			colDefs = append(colDefs, fmt.Sprintf("    PRIMARY KEY (%s)", strings.Join(quotedPKs, ", ")))
		}

		fmt.Fprintf(f, "%s\n);\n\n", strings.Join(colDefs, ",\n"))

		// Lấy toàn bộ dữ liệu của bảng
		dataRows, err := db.Query(fmt.Sprintf("SELECT * FROM %s", tbl))
		if err != nil {
			log.Printf("[MIGRATION] Lỗi đọc dữ liệu bảng %s: %v", tbl, err)
			continue
		}

		colNames, err := dataRows.Columns()
		if err != nil {
			dataRows.Close()
			continue
		}

		var quotedColNames []string
		colPGTypes := make([]string, len(colNames))
		for i, cn := range colNames {
			quotedColNames = append(quotedColNames, fmt.Sprintf("\"%s\"", cn))
			for _, c := range cols {
				if c.Name == cn {
					colPGTypes[i] = mapMySQLTypeToPostgres(c.ColType)
					break
				}
			}
		}

		tableRowCount := 0
		for dataRows.Next() {
			valPointers := make([]interface{}, len(colNames))
			rawVals := make([]interface{}, len(colNames))
			for i := range rawVals {
				valPointers[i] = &rawVals[i]
			}

			if err := dataRows.Scan(valPointers...); err != nil {
				continue
			}

			var escapedVals []string
			for i, v := range rawVals {
				escapedVals = append(escapedVals, escapePGValueWithType(v, colPGTypes[i]))
			}

			fmt.Fprintf(f, "INSERT INTO \"%s\" (%s) VALUES (%s) ON CONFLICT DO NOTHING;\n",
				tbl, strings.Join(quotedColNames, ", "), strings.Join(escapedVals, ", "))
			tableRowCount++
		}
		dataRows.Close()

		fmt.Fprintf(f, "\n")
		totalExportedRows += tableRowCount
		log.Printf("[MIGRATION] ✔ Đã xuất bảng %-22s: %5d dòng", tbl, tableRowCount)
	}

	log.Printf("[MIGRATION] Hoàn tất xuất CSDL! Tổng cộng %d dòng dữ liệu đã được ghi vào file:\n=> %s", totalExportedRows, targetFile)
}
