package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	uri := os.Getenv("AIVEN_PG_URI")
	if uri == "" {
		log.Fatal("Lỗi: Biến môi trường AIVEN_PG_URI chưa được thiết lập!")
	}
	db, err := sql.Open("postgres", uri)
	if err != nil {
		log.Fatalf("Lỗi mở kết nối: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, provider, auth_type, name, email, is_active FROM omni_providers ORDER BY priority ASC, id ASC")
	if err != nil {
		log.Fatalf("Lỗi query omni_providers: %v", err)
	}
	defer rows.Close()

	fmt.Printf("\n>>> DANH SÁCH 20 TÀI KHOẢN TRONG OMNI_PROVIDERS <<<\n")
	count := 0
	for rows.Next() {
		count++
		var id, provider, authType, name, email string
		var active int
		_ = rows.Scan(&id, &provider, &authType, &name, &email, &active)
		fmt.Printf("[%02d] %-30s | %-12s | %-10s | Active: %d | %s\n", count, name, provider, authType, active, email)
	}
}
