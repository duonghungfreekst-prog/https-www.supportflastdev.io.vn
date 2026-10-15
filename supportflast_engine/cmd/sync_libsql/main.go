package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"supportflast_engine/database"
	"supportflast_engine/omnirouter"
)

func main() {
	libsqlPath := flag.String("libsql", omnirouter.DefaultLibSQLPath, "Đường dẫn tới tệp data.libsql")
	flag.Parse()

	log.Println("=====================================================================")
	log.Println("       CÔNG CỤ ĐỒNG BỘ DỮ LIỆU TỪ LIBSQL SANG WEB / TIDB CLOUD       ")
	log.Println("=====================================================================")

	db := database.GetDB()
	if db == nil {
		// Khởi tạo kết nối DB nếu chạy standalone
		var err error
		db, err = database.InitDB()
		if err != nil {
			log.Fatalf("[FATAL] Không thể kết nối cơ sở dữ liệu hệ thống: %v", err)
		}
	}

	isTiDB := database.ActiveDriver() == "tidb" || database.ActiveDriver() == "mysql"
	if err := omnirouter.InitStorage(db, isTiDB); err != nil {
		log.Fatalf("[FATAL] Khởi tạo bảng OmniRouter thất bại: %v", err)
	}

	res, err := omnirouter.SyncFromLibSQL(db, *libsqlPath)
	if err != nil {
		log.Fatalf("[FATAL] Đồng bộ thất bại: %v", err)
	}

	fmt.Println("\n---------------------------------------------------------------------")
	fmt.Printf("✔ %s\n", res.Message)
	fmt.Printf("   • Tài khoản AI (Providers): %d\n", res.ProvidersCount)
	fmt.Printf("   • Cấu hình Settings:        %d\n", res.SettingsCount)
	fmt.Printf("   • Proxy Pools:              %d\n", res.ProxiesCount)
	fmt.Printf("   • Cặp khóa KV:              %d\n", res.KVCount)
	fmt.Println("---------------------------------------------------------------------")
	fmt.Println("🎉 Hoàn tất đồng bộ toàn diện từ LibSQL!")
	os.Exit(0)
}
