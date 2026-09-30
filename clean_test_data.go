package main

import (
	"database/sql"
	"fmt"
	"os"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load("../.env"); err != nil {
		log.Println("No ../.env file found")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?tls=true",
		os.Getenv("TIDB_USER"),
		os.Getenv("TIDB_PASSWORD"),
		os.Getenv("TIDB_HOST"),
		os.Getenv("TIDB_PORT"),
		os.Getenv("TIDB_DATABASE"),
	)
	
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Error opening db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Error pinging db: %v", err)
	}

	// Find the test file IDs
	rows, err := db.Query("SELECT id FROM virtual_files WHERE size_bytes = 1024 OR name IN ('movie.mp4', 'missing.mp4', 'test_stream.mp4') OR name LIKE 'TrashFolder' OR name LIKE 'MyDocuments'")
	if err != nil {
		log.Fatalf("Query error: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			log.Fatalf("Scan error: %v", err)
		}
		ids = append(ids, id)
	}

	fmt.Printf("Found %d test files/folders to delete\n", len(ids))

	// Delete them
	for _, id := range ids {
		db.Exec("DELETE FROM file_chunks WHERE file_id = ?", id)
		db.Exec("DELETE FROM public_shares WHERE file_id = ?", id)
		db.Exec("DELETE FROM virtual_files WHERE id = ?", id)
	}

	fmt.Println("Cleanup successful!")
}
