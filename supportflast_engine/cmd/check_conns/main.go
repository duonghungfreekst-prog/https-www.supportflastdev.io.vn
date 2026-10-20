package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

func main() {
	uri := "postgres://avnadmin:AVNS_u3Qmxf4ne10H3N2fQ21@pg-333a3094-duonghungfreekst-ee66.d.aivencloud.com:17204/defaultdb?sslmode=require"
	db, err := sql.Open("postgres", uri)
	if err != nil {
		log.Fatalf("Lỗi mở kết nối: %v", err)
	}
	defer db.Close()

	var usedReq, quotaLimit int64
	var lastUsedAt string
	err = db.QueryRow("SELECT used_requests, quota_limit, last_used_at FROM omni_client_keys WHERE id = 'key_1792372553409283328_b9b08b62'").Scan(&usedReq, &quotaLimit, &lastUsedAt)
	if err != nil {
		log.Fatalf("Lỗi query: %v", err)
	}
	fmt.Printf("\n>>> KẾT QUẢ CẬP NHẬT TRÊN AIVEN POSTGRESQL <<<\nKhóa v12 hiện tại: Used = %d / %d req | LastUsedAt = %s\n", usedReq, quotaLimit, lastUsedAt)
}
