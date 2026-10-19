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
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	var count int
	err = db.QueryRow("SELECT count(1) FROM pg_stat_activity").Scan(&count)
	if err != nil {
		log.Fatalf("Lỗi truy vấn: %v", err)
	}
	fmt.Printf("[AIVEN] Tổng số active connections hiện tại: %d\n", count)

	rows, err := db.Query("SELECT pid, usename, client_addr, state, left(query, 60) FROM pg_stat_activity ORDER BY pid")
	if err != nil {
		log.Fatalf("Lỗi xem danh sách: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pid int
		var usename, state, query sql.NullString
		var clientAddr sql.NullString
		if err := rows.Scan(&pid, &usename, &clientAddr, &state, &query); err == nil {
			fmt.Printf("PID: %d | User: %s | ClientIP: %s | State: %s | Query: %s\n",
				pid, usename.String, clientAddr.String, state.String, query.String)
		}
	}
}
