package main

import (
	"bufio"
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
)

func loadEnv(filepath string) map[string]string {
	env := make(map[string]string)
	f, err := os.Open(filepath)
	if err != nil {
		return env
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return env
}

func main() {
	env := loadEnv("../../.env")
	if len(env) == 0 {
		env = loadEnv(".env")
	}

	host := env["TIDB_HOST"]
	port := env["TIDB_PORT"]
	user := env["TIDB_USER"]
	pass := env["TIDB_PASSWORD"]
	dbName := env["TIDB_DATABASE"]

	_ = mysql.RegisterTLSConfig("tidb", &tls.Config{
		MinVersion: tls.VersionTLS12,
	})

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true&loc=UTC&tls=tidb",
		user, pass, host, port, dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("sql.Open err: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM apps").Scan(&count)
	if err != nil {
		log.Fatalf("QueryRow COUNT(*) err: %v", err)
	}
	fmt.Printf("TOTAL APPS IN TIDB: %d\n", count)

	query := "SELECT id, name, version, COALESCE(platform, ''), COALESCE(category, ''), COALESCE(`desc`, ''), COALESCE(file_name, ''), COALESCE(size_bytes, 0), COALESCE(size_formatted, ''), COALESCE(sha256, ''), COALESCE(author, ''), COALESCE(downloads, 0), COALESCE(status, 'published'), COALESCE(published_at, ''), COALESCE(download_url, ''), COALESCE(video_url, ''), COALESCE(guide, ''), COALESCE(user_id, '') FROM apps ORDER BY published_at DESC, id DESC"
	stmt, err := db.Prepare(query)
	if err != nil {
		fmt.Printf("PREPARE ERROR ON TIDB: %v\n", err)
	} else {
		defer stmt.Close()
		fmt.Println("PREPARE SUCCESS ON TIDB!")
		rows, err := stmt.Query()
		if err != nil {
			fmt.Printf("QUERY ERROR: %v\n", err)
		} else {
			defer rows.Close()
			count := 0
			for rows.Next() {
				count++
			}
			fmt.Printf("QUERY SUCCESS! Read %d apps from TiDB!\n", count)
		}
	}
}
