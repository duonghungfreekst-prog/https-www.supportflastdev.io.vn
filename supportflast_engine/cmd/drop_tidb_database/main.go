package main

import (
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"supportflast_engine/database"
)

func main() {
	fmt.Println("================================================================================")
	fmt.Println("             XÓA HOÀN TOÀN CƠ SỞ DỮ LIỆU TIDB CLOUD THEO YÊU CẦU               ")
	fmt.Println("================================================================================")

	cfg := database.DefaultTiDBConfig()
	cfg.AutoMigrate = false
	db, err := database.OpenTiDBConnection(cfg)
	if err != nil {
		log.Fatalf("Lỗi kết nối TiDB Cloud: %v", err)
	}
	defer db.Close()

	// 1. Thực thi xóa bỏ toàn bộ database supportflast trên TiDB Cloud
	log.Println("[TIDB] Đang thực thi: DROP DATABASE IF EXISTS supportflast ...")
	_, err = db.Exec("DROP DATABASE IF EXISTS supportflast")
	if err != nil {
		log.Fatalf("Lỗi xóa database TiDB: %v", err)
	}
	log.Println("[TIDB] ✔ ĐÃ XÓA HOÀN TOÀN CSDL 'supportflast' KHỎI CỤM TIDB CLOUD THÀNH CÔNG!")

	// 2. Kiểm tra lại danh sách databases còn lại trên TiDB
	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		log.Fatalf("Lỗi SHOW DATABASES: %v", err)
	}
	defer rows.Close()

	var remaining []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			remaining = append(remaining, name)
		}
	}

	fmt.Printf("\n[TIDB] Danh sách các Database còn lại trên TiDB Cloud: %v\n", remaining)
	foundSupportFlast := false
	for _, n := range remaining {
		if n == "supportflast" {
			foundSupportFlast = true
			break
		}
	}

	if !foundSupportFlast {
		fmt.Println("===> XÁC NHẬN: CSDL 'supportflast' ĐÃ BIẾN MẤT HOÀN TOÀN KHỎI TIDB CLOUD 100%!")
	} else {
		fmt.Println("===> CẢNH BÁO: CSDL 'supportflast' vẫn còn tồn tại!")
	}
}
