package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	uriFlag := flag.String("uri", "", "Aiven PostgreSQL Service URI")
	flag.Parse()

	uri := *uriFlag
	if uri == "" {
		uri = os.Getenv("AIVEN_PG_URI")
	}
	if uri == "" {
		uri = "postgres://avnadmin:AVNS_u3Qmxf4ne10H3N2fQ21@pg-333a3094-duonghungfreekst-ee66.d.aivencloud.com:17204/defaultdb?sslmode=require"
	}

	db, err := sql.Open("postgres", uri)
	if err != nil {
		log.Fatalf("Lỗi kết nối PostgreSQL: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Lỗi ping PostgreSQL: %v", err)
	}

	log.Println("======================================================================")
	log.Println("PHÂN TÍCH CHI TIẾT DUNG LƯỢNG LƯU TRỮ AIVEN POSTGRESQL (pg-333a3094)")
	log.Println("======================================================================")

	// 1. Kích thước các Database trong Cluster
	log.Println("\n1. DUNG LƯỢNG TỪNG DATABASE TRONG CỤM:")
	dbRows, err := db.Query(`
		SELECT datname, 
		       pg_database_size(datname) AS size_bytes,
		       pg_size_pretty(pg_database_size(datname)) AS size_pretty
		FROM pg_database
		ORDER BY size_bytes DESC;
	`)
	if err != nil {
		log.Printf("Lỗi lấy kích thước database: %v", err)
	} else {
		defer dbRows.Close()
		var totalClusterBytes int64
		for dbRows.Next() {
			var datname, sizePretty string
			var sizeBytes int64
			_ = dbRows.Scan(&datname, &sizeBytes, &sizePretty)
			totalClusterBytes += sizeBytes
			log.Printf("  - Database %-15s: %10s (%d bytes)", datname, sizePretty, sizeBytes)
		}
		log.Printf("  => Tổng dung lượng các database: %s (%d bytes)\n", formatBytes(totalClusterBytes), totalClusterBytes)
	}

	// 2. Kích thước chi tiết từng bảng người dùng (Table data, Indexes, TOAST)
	log.Println("2. DUNG LƯỢNG CHI TIẾT TỪNG BẢNG (Bảng dữ liệu, Index, TOAST data):")
	tableRows, err := db.Query(`
		SELECT 
			c.relname AS table_name,
			pg_total_relation_size(c.oid) AS total_bytes,
			pg_relation_size(c.oid) AS table_bytes,
			pg_indexes_size(c.oid) AS index_bytes,
			COALESCE(pg_total_relation_size(c.reltoastrelid), 0) AS toast_bytes,
			COALESCE(s.n_live_tup, 0) AS live_rows,
			COALESCE(s.n_dead_tup, 0) AS dead_rows
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		ORDER BY total_bytes DESC;
	`)
	if err != nil {
		log.Printf("Lỗi phân tích bảng: %v", err)
	} else {
		defer tableRows.Close()
		fmt.Printf("  %-24s | %-10s | %-10s | %-10s | %-10s | %-8s | %-8s\n",
			"Tên Bảng", "Tổng Size", "Dữ liệu", "Index", "TOAST", "Live Rows", "Dead Rows")
		fmt.Println("  -------------------------------------------------------------------------------------------------")
		var sumTotal, sumTable, sumIndex, sumToast int64
		for tableRows.Next() {
			var tbl string
			var total, table, index, toast, live, dead int64
			_ = tableRows.Scan(&tbl, &total, &table, &index, &toast, &live, &dead)
			sumTotal += total
			sumTable += table
			sumIndex += index
			sumToast += toast
			fmt.Printf("  %-24s | %-10s | %-10s | %-10s | %-10s | %8d | %8d\n",
				tbl, formatBytes(total), formatBytes(table), formatBytes(index), formatBytes(toast), live, dead)
		}
		fmt.Println("  -------------------------------------------------------------------------------------------------")
		fmt.Printf("  TỔNG CỘNG CÁC BẢNG      | %-10s | %-10s | %-10s | %-10s\n",
			formatBytes(sumTotal), formatBytes(sumTable), formatBytes(sumIndex), formatBytes(sumToast))
	}

	// 3. Kiểm tra WAL và các file hệ thống
	log.Println("\n3. KIỂM TRA HỆ THỐNG GHI LOG & TIẾN TRÌNH:")
	var version string
	_ = db.QueryRow("SELECT version()").Scan(&version)
	log.Printf("  - PostgreSQL Version: %s", version)

	var currentWAL string
	_ = db.QueryRow("SELECT pg_walfile_name(pg_current_wal_lsn())").Scan(&currentWAL)
	log.Printf("  - Current WAL File: %s", currentWAL)

	// Kiểm tra các cài đặt WAL
	walSettings := []string{"min_wal_size", "max_wal_size", "wal_keep_size", "checkpoint_timeout", "default_toast_compression"}
	for _, sName := range walSettings {
		var sVal string
		if err := db.QueryRow(fmt.Sprintf("SHOW %s", sName)).Scan(&sVal); err == nil {
			log.Printf("  - Setting %-25s: %s", sName, sVal)
		}
	}

	// Kiểm tra các extension đang bật
	extRows, err := db.Query("SELECT extname, extversion FROM pg_extension ORDER BY extname")
	if err == nil {
		defer extRows.Close()
		var exts []string
		for extRows.Next() {
			var name, ver string
			_ = extRows.Scan(&name, &ver)
			exts = append(exts, fmt.Sprintf("%s (%s)", name, ver))
		}
		log.Printf("  - Các Extension đang bật: %s", exts)
	}

	// 4. Thực thi dọn dẹp và tối ưu hoá: CHECKPOINT & VACUUM FULL & ANALYZE
	log.Println("\n4. TIẾN HÀNH DỌN DẸP, TỐI ƯU HÓA DỮ LIỆU & GIẢI PHÓNG DISK (CHECKPOINT + VACUUM FULL & ANALYZE)...")
	if _, err := db.Exec("CHECKPOINT;"); err != nil {
		log.Printf("  ⚠ Cảnh báo CHECKPOINT: %v", err)
	} else {
		log.Println("  ✔ Đã flush checkpoint thành công!")
	}

	_, err = db.Exec("VACUUM FULL ANALYZE;")
	if err != nil {
		log.Printf("  ⚠ Cảnh báo VACUUM FULL: %v", err)
	} else {
		log.Println("  ✔ Đã dọn sạch dead tuples và tối ưu toàn bộ các bảng thành công!")
	}

	// Kiểm tra lại sau VACUUM
	var newDbSize int64
	var newDbPretty string
	_ = db.QueryRow("SELECT pg_database_size('defaultdb'), pg_size_pretty(pg_database_size('defaultdb'))").Scan(&newDbSize, &newDbPretty)
	log.Printf("  => Dung lượng database 'defaultdb' sau khi tối ưu: %s (%d bytes)\n", newDbPretty, newDbSize)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
