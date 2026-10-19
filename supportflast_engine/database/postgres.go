package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Hằng số kết nối mặc định tới cụm Aiven PostgreSQL
const (
	DefaultAivenPostgresURI = "postgres://avnadmin:AVNS_u3Qmxf4ne10H3N2fQ21@pg-333a3094-duonghungfreekst-ee66.d.aivencloud.com:17204/defaultdb?sslmode=require"
	DefaultPGMaxOpenConns   = 50
	DefaultPGMaxIdleConns   = 25
	DefaultPGConnLifetime   = 5 * time.Minute
	DefaultPGConnIdleTime   = 3 * time.Minute
	DefaultPGConnectTimeout = 10 * time.Second
)

var (
	pgInstance *sql.DB
	pgMutex    sync.RWMutex
)

// GetPostgresURI phân giải chuỗi kết nối Aiven PostgreSQL từ các biến môi trường
func GetPostgresURI() string {
	candidates := []string{
		os.Getenv("AIVEN_PG_URI"),
		os.Getenv("DATABASE_URL"),
		os.Getenv("POSTGRES_URI"),
		os.Getenv("POSTGRESQL_URI"),
		os.Getenv("PG_URI"),
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c != "" && (strings.HasPrefix(c, "postgres://") || strings.HasPrefix(c, "postgresql://")) {
			return c
		}
	}
	return DefaultAivenPostgresURI
}

// OpenPostgresConnection mở kết nối tới Aiven PostgreSQL với driver pgcompat (tương thích đa cú pháp)
func OpenPostgresConnection(customURI ...string) (*sql.DB, error) {
	uri := GetPostgresURI()
	if len(customURI) > 0 && strings.TrimSpace(customURI[0]) != "" {
		uri = strings.TrimSpace(customURI[0])
	}

	// Đảm bảo sslmode=require khi kết nối tới Aiven Cloud
	if !strings.Contains(uri, "sslmode=") {
		if strings.Contains(uri, "?") {
			uri += "&sslmode=require"
		} else {
			uri += "?sslmode=require"
		}
	}

	db, err := sql.Open("pgcompat", uri)
	if err != nil {
		return nil, fmt.Errorf("lỗi mở kết nối PostgreSQL: %w", err)
	}

	// Thiết lập Connection Pool theo tiêu chuẩn PHẦN 7.1
	maxOpen := parseIntEnv("PG_MAX_OPEN_CONNS", DefaultPGMaxOpenConns)
	maxIdle := parseIntEnv("PG_MAX_IDLE_CONNS", DefaultPGMaxIdleConns)
	lifetime := parseDurationEnv("PG_CONN_MAX_LIFETIME", DefaultPGConnLifetime)
	idleTime := parseDurationEnv("PG_CONN_MAX_IDLE_TIME", DefaultPGConnIdleTime)

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(lifetime)
	db.SetConnMaxIdleTime(idleTime)

	// Ping check kiểm tra tính sẵn sàng của kết nối mạng và SSL
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPGConnectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping kiểm tra kết nối Aiven PostgreSQL thất bại: %w", err)
	}

	log.Printf("[ENGINE] [POSTGRES] ✔ Kết nối Aiven PostgreSQL thành công! (Pool: MaxOpen=%d, MaxIdle=%d, Lifetime=%v)",
		maxOpen, maxIdle, lifetime)

	return db, nil
}

// InitPostgres khởi tạo singleton connection Aiven PostgreSQL
func InitPostgres(customURI ...string) (*sql.DB, error) {
	pgMutex.Lock()
	defer pgMutex.Unlock()

	if pgInstance != nil {
		dbMutex.Lock()
		dbInstance = pgInstance
		activeDriver.Store("postgres")
		dbMutex.Unlock()
		return pgInstance, nil
	}

	db, err := OpenPostgresConnection(customURI...)
	if err != nil {
		return nil, err
	}

	pgInstance = db
	dbMutex.Lock()
	dbInstance = db
	activeDriver.Store("postgres")
	dbMutex.Unlock()
	return pgInstance, nil
}

// GetPostgres trả về con trỏ kết nối Aiven PostgreSQL hiện hành
func GetPostgres() *sql.DB {
	pgMutex.RLock()
	defer pgMutex.RUnlock()
	return pgInstance
}

// ClosePostgres đóng kết nối an toàn
func ClosePostgres() error {
	pgMutex.Lock()
	defer pgMutex.Unlock()

	if pgInstance != nil {
		err := pgInstance.Close()
		pgInstance = nil
		return err
	}
	return nil
}

// CheckPostgresHealth kiểm tra sức khỏe cụm Aiven PostgreSQL
func CheckPostgresHealth(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("kết nối PostgreSQL là nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
