package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"supportflast_engine/database"
)

func main() {
	log.Println("[SYNC] Bắt đầu đồng bộ bổ sung omni_usage_history từ TiDB sang Aiven...")

	cfg := database.DefaultTiDBConfig()
	cfg.AutoMigrate = false
	tidbDB, err := database.OpenTiDBConnection(cfg)
	if err != nil {
		log.Fatalf("Lỗi TiDB: %v", err)
	}
	defer tidbDB.Close()

	pgURI := "postgres://avnadmin:AVNS_u3Qmxf4ne10H3N2fQ21@pg-333a3094-duonghungfreekst-ee66.d.aivencloud.com:17204/defaultdb?sslmode=require"
	pgDB, err := sql.Open("postgres", pgURI)
	if err != nil {
		log.Fatalf("Lỗi Aiven PG: %v", err)
	}
	defer pgDB.Close()
	pgDB.SetMaxOpenConns(2)

	rows, err := tidbDB.Query(`
		SELECT id, timestamp, provider, model, connection_id, api_key, endpoint,
		       prompt_tokens, completion_tokens, total_tokens, cost, status,
		       tokens, meta, latency_ms, ttft_ms, tps, cached_tokens, status_code, error_code
		FROM omni_usage_history
	`)
	if err != nil {
		log.Fatalf("Lỗi đọc TiDB: %v", err)
	}
	defer rows.Close()

	insertStmt, err := pgDB.Prepare(`
		INSERT INTO omni_usage_history (
			id, timestamp, provider, model, connection_id, api_key, endpoint,
			prompt_tokens, completion_tokens, total_tokens, cost, status,
			tokens, meta, latency_ms, ttft_ms, tps, cached_tokens, status_code, error_code
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		log.Fatalf("Lỗi prepare PG: %v", err)
	}
	defer insertStmt.Close()

	synced := 0
	for rows.Next() {
		var id int64
		var timestamp, provider, model, connectionID, status string
		var apiKey, endpoint, tokens, meta, errorCode sql.NullString
		var promptTokens, completionTokens, totalTokens, latencyMs, ttftMs, cachedTokens int64
		var cost, tps float64
		var statusCode int

		err := rows.Scan(
			&id, &timestamp, &provider, &model, &connectionID, &apiKey, &endpoint,
			&promptTokens, &completionTokens, &totalTokens, &cost, &status,
			&tokens, &meta, &latencyMs, &ttftMs, &tps, &cachedTokens, &statusCode, &errorCode,
		)
		if err != nil {
			log.Printf("Lỗi scan dòng %d: %v", id, err)
			continue
		}

		res, err := insertStmt.Exec(
			id, timestamp, provider, model, connectionID, apiKey, endpoint,
			promptTokens, completionTokens, totalTokens, cost, status,
			tokens, meta, latencyMs, ttftMs, tps, cachedTokens, statusCode, errorCode,
		)
		if err != nil {
			log.Printf("Lỗi insert dòng %d: %v", id, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			synced++
		}
	}

	var count int
	_ = pgDB.QueryRow("SELECT count(*) FROM omni_usage_history").Scan(&count)
	fmt.Printf("[SYNC] Đã đồng bộ thêm %d dòng mới vào Aiven PG! Tổng số dòng hiện tại trên Aiven: %d dòng\n", synced, count)
}
