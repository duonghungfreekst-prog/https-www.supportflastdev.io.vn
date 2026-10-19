package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"supportflast_engine/database"
)

func main() {
	fmt.Println("================================================================================")
	fmt.Println("     RÀ SOÁT TOÀN DIỆN MỌI CƠ SỞ DỮ LIỆU TRÊN TIDB CLOUD SO VỚI AIVEN PG       ")
	fmt.Println("================================================================================")

	// 1. Kết nối TiDB Cloud
	cfg := database.DefaultTiDBConfig()
	cfg.AutoMigrate = false
	tidbDB, err := database.OpenTiDBConnection(cfg)
	if err != nil {
		log.Fatalf("Lỗi kết nối TiDB Cloud: %v", err)
	}
	defer tidbDB.Close()

	// 2. Liệt kê tất cả databases trên TiDB Cloud
	dbRows, err := tidbDB.Query("SHOW DATABASES")
	if err != nil {
		log.Fatalf("Lỗi SHOW DATABASES trên TiDB: %v", err)
	}
	defer dbRows.Close()

	var dbNames []string
	for dbRows.Next() {
		var dbName string
		if err := dbRows.Scan(&dbName); err == nil {
			dbNames = append(dbNames, dbName)
		}
	}
	fmt.Printf("[TIDB] Danh sách các Database hiện có trên TiDB Cloud: %v\n\n", dbNames)

	// 3. Quét từng database và đếm số bảng + số dòng
	for _, dbName := range dbNames {
		if dbName == "INFORMATION_SCHEMA" || dbName == "PERFORMANCE_SCHEMA" || dbName == "METRICS_SCHEMA" || dbName == "mysql" || dbName == "sys" {
			continue
		}
		fmt.Printf("--- DATABASE: %s ---\n", dbName)
		_, _ = tidbDB.Exec("USE " + dbName)
		tblRows, err := tidbDB.Query("SHOW TABLES")
		if err != nil {
			fmt.Printf("  Lỗi SHOW TABLES: %v\n", err)
			continue
		}
		var tables []string
		for tblRows.Next() {
			var tbl string
			if err := tblRows.Scan(&tbl); err == nil {
				tables = append(tables, tbl)
			}
		}
		tblRows.Close()

		fmt.Printf("  Tổng số bảng trong '%s': %d bảng\n", dbName, len(tables))
		totalRowsInDB := 0
		for _, tbl := range tables {
			var count int
			_ = tidbDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&count)
			fmt.Printf("    %-25s : %6d dòng\n", tbl, count)
			totalRowsInDB += count
		}
		fmt.Printf("  ==> TỔNG SỐ DÒNG trong '%s': %d dòng\n\n", dbName, totalRowsInDB)
	}

	// 4. Kết nối Aiven PostgreSQL để so sánh đối chiếu từng bảng một
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("ĐỐI CHIẾU SỐ LƯỢNG DÒNG GIỮA TIDB ('supportflast') VÀ AIVEN ('defaultdb'):")
	fmt.Println("--------------------------------------------------------------------------------")
	pgURI := "postgres://avnadmin:AVNS_u3Qmxf4ne10H3N2fQ21@pg-333a3094-duonghungfreekst-ee66.d.aivencloud.com:17204/defaultdb?sslmode=require"
	pgDB, err := sql.Open("postgres", pgURI)
	if err != nil {
		log.Fatalf("Lỗi kết nối Aiven PG: %v", err)
	}
	defer pgDB.Close()
	pgDB.SetMaxOpenConns(2)

	_, _ = tidbDB.Exec("USE supportflast")
	tblRows, _ := tidbDB.Query("SHOW TABLES")
	var tidbTables []string
	for tblRows.Next() {
		var tbl string
		if err := tblRows.Scan(&tbl); err == nil {
			tidbTables = append(tidbTables, tbl)
		}
	}
	tblRows.Close()

	fmt.Printf("%-25s | %-12s | %-12s | %s\n", "BẢNG", "SỐ DÒNG TIDB", "SỐ DÒNG AIVEN", "TRẠNG THÁI")
	fmt.Println("--------------------------------------------------------------------------------")

	for _, tbl := range tidbTables {
		var tidbCount int
		_ = tidbDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&tidbCount)

		var pgCount int
		err := pgDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&pgCount)

		status := "✅ KHỚP HOÀN TOÀN"
		if err != nil {
			status = fmt.Sprintf("❌ LỖI TRÊN AIVEN: %v", err)
		} else if tidbCount != pgCount {
			status = fmt.Sprintf("⚠️ LỆCH (TiDB: %d vs Aiven: %d)", tidbCount, pgCount)
		}

		fmt.Printf("%-25s | %12d | %12d | %s\n", tbl, tidbCount, pgCount, status)
	}
}
