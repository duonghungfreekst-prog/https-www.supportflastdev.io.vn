package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func inspectSQLite(dbPath string) {
	fmt.Printf("\n=== KIỂM TRA FILE SQLITE CỤC BỘ: %s ===\n", dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Printf("Lỗi mở %s: %v", dbPath, err)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		log.Printf("Lỗi query table: %v", err)
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

	for _, tbl := range tables {
		var count int
		_ = db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", tbl)).Scan(&count)
		fmt.Printf("  %-25s : %6d dòng\n", tbl, count)
	}
}

func main() {
	inspectSQLite("data/cloudpool_metadata.db")
	inspectSQLite("data/supportflast.db")
}
