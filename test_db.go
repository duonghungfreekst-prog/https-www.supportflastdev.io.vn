package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	dsn := "supportflast.1169641738:f297be7dffc79f8263eb87a1779ddb2c011e@tcp(gateway01.ap-southeast-1.prod.aws.tidbcloud.com:4000)/supportflast?tls=true"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM cloudpool_users WHERE id = 'user_admin'").Scan(&count)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		fmt.Printf("cloudpool_users count: %d\n", count)
	}
}
