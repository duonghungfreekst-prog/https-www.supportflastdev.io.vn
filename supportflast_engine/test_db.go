package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	dsn := "2KGt5QqixkveQPP.root:JIxWb1nGVINnKzap@tcp(gateway01.ap-southeast-1.prod.aws.tidbcloud.com:4000)/supportflast?tls=true"
	db, err := sql.Open("mysql", dsn)
	if err != nil { log.Fatal(err) }
	defer db.Close()

	row := db.QueryRow("SELECT id, username, email, password_hash, COALESCE(security_pin_hash, ''), COALESCE(security_tier, 1), display_name, avatar_url, role, quota_bytes, used_bytes, failed_login_count, locked_until, created_at, updated_at FROM cloudpool_users WHERE id = 'user_admin'")
	
	var id, username, email, pwd, pin, disp, avatar, role string
	var tier, quota, used, failed int
	var rawLockedUntil, rawCreated, rawUpdated interface{}

	if err := row.Scan(&id, &username, &email, &pwd, &pin, &tier, &disp, &avatar, &role, &quota, &used, &failed, &rawLockedUntil, &rawCreated, &rawUpdated); err != nil {
		log.Fatalf("Scan error: %v", err)
	}
	fmt.Printf("Success! locked_until: %v\n", rawLockedUntil)
}
