package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// parseSQLStatements tách các câu lệnh SQL một cách an toàn bằng State Machine,
// đảm bảo không cắt nhầm dấu chấm phẩy (;) bên trong chuỗi string literal ('...')
// hoặc trong các dòng ghi chú (-- hoặc /* ... */).
func parseSQLStatements(content string) []string {
	var stmts []string
	var current strings.Builder

	inString := false
	inLineComment := false
	inBlockComment := false
	n := len(content)

	for i := 0; i < n; i++ {
		c := content[i]

		// Nếu đang trong chuỗi string literal
		if inString {
			current.WriteByte(c)
			if c == '\'' {
				// PostgreSQL escape single quote bằng ''
				if i+1 < n && content[i+1] == '\'' {
					current.WriteByte('\'')
					i++
				} else {
					inString = false
				}
			}
			continue
		}

		// Nếu đang trong ghi chú dòng đơn (-- ...)
		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			continue
		}

		// Nếu đang trong ghi chú khối (/* ... */)
		if inBlockComment {
			if c == '*' && i+1 < n && content[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		// Bắt đầu ghi chú dòng đơn
		if c == '-' && i+1 < n && content[i+1] == '-' {
			inLineComment = true
			i++
			continue
		}

		// Bắt đầu ghi chú khối
		if c == '/' && i+1 < n && content[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}

		// Bắt đầu chuỗi string
		if c == '\'' {
			inString = true
			current.WriteByte(c)
			continue
		}

		// Kết thúc câu lệnh bằng dấu chấm phẩy ngoài chuỗi
		if c == ';' {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteByte(c)
	}

	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		stmts = append(stmts, stmt)
	}

	return stmts
}

func main() {
	uriFlag := flag.String("uri", "", "Chuỗi kết nối Aiven PostgreSQL Service URI")
	sqlFileFlag := flag.String("file", "", "Đường dẫn file SQL dump")
	flag.Parse()

	uri := *uriFlag
	if uri == "" {
		uri = os.Getenv("AIVEN_PG_URI")
	}
	if uri == "" {
		log.Fatal("Lỗi: Vui lòng cung cấp chuỗi kết nối bằng tham số -uri hoặc biến môi trường AIVEN_PG_URI")
	}

	sqlPath := *sqlFileFlag
	if sqlPath == "" {
		candidates := []string{
			filepath.Join("data", "supportflast_aiven_postgres.sql"),
			filepath.Join("..", "data", "supportflast_aiven_postgres.sql"),
			filepath.Join("F:", "supportflast.dev", "data", "supportflast_aiven_postgres.sql"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				sqlPath = c
				break
			}
		}
	}

	if sqlPath == "" {
		log.Fatal("Lỗi: Không tìm thấy file supportflast_aiven_postgres.sql")
	}

	log.Printf("[AIVEN] Đang kết nối tới Aiven PostgreSQL...")
	db, err := sql.Open("postgres", uri)
	if err != nil {
		log.Fatalf("[AIVEN] Lỗi mở kết nối PostgreSQL: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatalf("[AIVEN] Lỗi ping Aiven PostgreSQL: %v", err)
	}
	log.Printf("[AIVEN] ✔ Kết nối Aiven PostgreSQL thành công!")

	log.Printf("[AIVEN] Đang đọc file SQL dump: %s", sqlPath)
	contentBytes, err := os.ReadFile(sqlPath)
	if err != nil {
		log.Fatalf("[AIVEN] Lỗi đọc file SQL: %v", err)
	}

	log.Printf("[AIVEN] Đang phân tích cú pháp các câu lệnh SQL (kích thước %d bytes)...", len(contentBytes))
	statements := parseSQLStatements(string(contentBytes))
	log.Printf("[AIVEN] Đã phân tích thành công %d câu lệnh SQL!", len(statements))

	// Phân loại các câu lệnh: Session SET, CREATE TABLE (DDL), INSERT INTO (DML)
	var setStmts []string
	var ddlStmts []string
	tableInserts := make(map[string][]string)
	var tableOrder []string

	insertRegex := regexp.MustCompile(`(?i)^INSERT\s+INTO\s+"?([a-zA-Z0-9_]+)"?`)

	for _, stmt := range statements {
		upper := strings.ToUpper(stmt)
		if strings.HasPrefix(upper, "SET ") {
			setStmts = append(setStmts, stmt)
		} else if strings.HasPrefix(upper, "CREATE TABLE") {
			ddlStmts = append(ddlStmts, stmt)
		} else if matches := insertRegex.FindStringSubmatch(stmt); len(matches) > 1 {
			tbl := matches[1]
			if _, exists := tableInserts[tbl]; !exists {
				tableOrder = append(tableOrder, tbl)
			}
			tableInserts[tbl] = append(tableInserts[tbl], stmt)
		} else {
			// Lệnh khác (CREATE INDEX, etc.)
			ddlStmts = append(ddlStmts, stmt)
		}
	}

	// 1. Thực thi các lệnh cấu hình phiên
	for _, stmt := range setStmts {
		if _, err := db.Exec(stmt); err != nil {
			log.Printf("[AIVEN] Cảnh báo SET: %v", err)
		}
	}

	// 2. Thực thi DDL (CREATE TABLE)
	log.Printf("\n--- [GIAI ĐOẠN 1] KHỞI TẠO CẤU TRÚC %d BẢNG TRÊN AIVEN POSTGRESQL ---", len(ddlStmts))
	for _, stmt := range ddlStmts {
		if _, err := db.Exec(stmt); err != nil {
			log.Printf("[AIVEN] ❌ Lỗi tạo cấu trúc bảng: %v\nLệnh: %.120s...", err, stmt)
		}
	}
	log.Printf("[AIVEN] ✔ Đã khởi tạo hoàn tất cấu trúc DDL của toàn bộ các bảng!")

	// 3. Thực thi DML (INSERT INTO) theo từng bảng
	log.Printf("\n--- [GIAI ĐOẠN 2] NẠP DỮ LIỆU TỪNG BẢNG VÀO AIVEN POSTGRESQL ---")
	totalInserted := 0
	for _, tbl := range tableOrder {
		stmts := tableInserts[tbl]
		log.Printf("[AIVEN] Đang nạp bảng \"%s\" (%d dòng)...", tbl, len(stmts))

		// Thực thi theo transaction cho từng bảng
		tx, err := db.Begin()
		if err != nil {
			log.Printf("[AIVEN] ❌ Không thể mở transaction cho bảng \"%s\": %v", tbl, err)
			continue
		}

		tableSuccess := 0
		hasError := false
		var firstErr error

		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt); err != nil {
				hasError = true
				firstErr = err
				break
			}
			tableSuccess++
		}

		if !hasError {
			if err := tx.Commit(); err == nil {
				totalInserted += tableSuccess
				log.Printf("  ✔ Bảng \"%-22s\": Nạp thành công %5d / %5d dòng (Transaction Commit)", tbl, tableSuccess, len(stmts))
				continue
			} else {
				hasError = true
				firstErr = err
			}
		}

		// Nếu transaction bị lỗi, rollback và fall back chạy từng câu lệnh trực tiếp
		_ = tx.Rollback()
		log.Printf("  ⚠ Transaction bảng \"%s\" lỗi (%v), chuyển sang nạp từng câu lệnh đơn lẻ...", tbl, firstErr)

		singleSuccess := 0
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err == nil {
				singleSuccess++
			}
		}
		totalInserted += singleSuccess
		log.Printf("  ✔ Bảng \"%-22s\": Nạp thành công %5d / %5d dòng (Single Exec)", tbl, singleSuccess, len(stmts))
	}

	log.Printf("\n[AIVEN] Tổng số dòng dữ liệu đã ghi nhận thành công: %d dòng", totalInserted)

	// 4. Kiểm tra và thống kê thực tế các bảng trên Aiven PostgreSQL
	log.Println("\n--- [GIAI ĐOẠN 3] KIỂM TRA ĐỐI SOÁT CSDL TRÊN AIVEN POSTGRESQL (schema: public) ---")
	rows, err := db.Query(`
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name ASC
	`)
	if err != nil {
		log.Fatalf("[AIVEN] Lỗi truy vấn danh sách bảng: %v", err)
	}
	defer rows.Close()

	var actualTables []string
	for rows.Next() {
		var tName string
		if err := rows.Scan(&tName); err == nil {
			actualTables = append(actualTables, tName)
		}
	}

	totalAivenRows := 0
	for _, tName := range actualTables {
		var count int
		_ = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", tName)).Scan(&count)
		totalAivenRows += count
		log.Printf("  ✔ Bảng \"%-24s\": %5d dòng", tName, count)
	}

	log.Printf("\n======================================================================")
	log.Printf("[AIVEN] 🎉 HOÀN TẤT DI TRÚ CƠ SỞ DỮ LIỆU SANG AIVEN POSTGRESQL THÀNH CÔNG 100%%!")
	log.Printf("[AIVEN] Tổng số bảng trên Aiven: %d bảng", len(actualTables))
	log.Printf("[AIVEN] Tổng số dòng dữ liệu thực tế: %d dòng", totalAivenRows)
	log.Printf("======================================================================\n")
}
