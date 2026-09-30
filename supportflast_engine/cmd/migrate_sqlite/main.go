package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	"supportflast_engine/database"
)

func main() {
	sqlitePath := flag.String("sqlite", "", "Duong dan den file cloudpool_metadata.db (ban sao luu)")
	flag.Parse()

	if *sqlitePath == "" {
		log.Fatal("Vui long cung cap duong dan den file SQLite bang tham so -sqlite")
	}

	if _, err := os.Stat(*sqlitePath); os.IsNotExist(err) {
		log.Fatalf("File %s khong ton tai", *sqlitePath)
	}

	log.Printf("Dang mo SQLite: %s", *sqlitePath)
	sqliteDB, err := sql.Open("sqlite3", *sqlitePath)
	if err != nil {
		log.Fatalf("Loi mo SQLite: %v", err)
	}
	defer sqliteDB.Close()

	cfg := database.DefaultTiDBConfig()
	dsn, _ := database.BuildTiDBDSN(cfg)
	log.Printf("Dang ket noi TiDB: %s:%d/%s", cfg.Host, cfg.Port, cfg.Database)
	tidb, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Loi ket noi TiDB: %v", err)
	}
	defer tidb.Close()

	tables := []string{
		"virtual_files", "file_chunks", "public_shares", "cloudpool_users",
	}

	for _, table := range tables {
		log.Printf("Dang dong bo bang: %s", table)
		
		srcTable := table
		if table == "cloudpool_users" {
			srcTable = "users"
		}

		rows, err := sqliteDB.Query(fmt.Sprintf("SELECT * FROM %s", srcTable))
		if err != nil {
			log.Printf("Bo qua bang %s: %v", srcTable, err)
			continue
		}

		cols, _ := rows.Columns()
		
		migrated := 0
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			valPtrs := make([]interface{}, len(cols))
			for i := range vals {
				valPtrs[i] = &vals[i]
			}
			rows.Scan(valPtrs...)

			query := fmt.Sprintf("INSERT IGNORE INTO %s (", table)
			for i, col := range cols {
				query += col
				if i < len(cols)-1 {
					query += ", "
				}
			}
			query += ") VALUES ("
			for i := range cols {
				query += "?"
				if i < len(cols)-1 {
					query += ", "
				}
			}
			query += ")"

			_, err = tidb.Exec(query, vals...)
			if err == nil {
				migrated++
			}
		}
		rows.Close()
		log.Printf("Hoan tat %s: %d ban ghi da duoc dong bo", table, migrated)
	}
	
	log.Println("Dong bo SQLite -> TiDB hoan tat thanh cong!")
	time.Sleep(1 * time.Second)
}
