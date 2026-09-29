package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// TableDef định nghĩa thông tin bảng cần chuyển đổi và câu lệnh DDL tương ứng trên TiDB Cloud
type TableDef struct {
	Name       string
	PrimaryKey []string
	DDL        string
}

// Danh sách các bảng cần di chuyển dữ liệu theo đúng thứ tự ràng buộc khóa ngoại
var migrationTables = []TableDef{
	{
		Name:       "users",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    display_name VARCHAR(255),
    role VARCHAR(50) NOT NULL DEFAULT 'user',
    avatar VARCHAR(500),
    created_at VARCHAR(64),
    updated_at VARCHAR(64),
    last_login VARCHAR(64),
    INDEX idx_users_username (username),
    INDEX idx_users_email (email),
    INDEX idx_users_role (role),
    INDEX idx_users_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		Name:       "apps",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS apps (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(50) NOT NULL,
    platform VARCHAR(100),
    category VARCHAR(100),
    ` + "`desc`" + ` TEXT,
    file_name VARCHAR(255),
    size_bytes BIGINT DEFAULT 0,
    size_formatted VARCHAR(50),
    sha256 VARCHAR(128),
    author VARCHAR(255),
    downloads INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'published',
    published_at VARCHAR(64),
    download_url TEXT,
    video_url TEXT,
    guide TEXT,
    user_id VARCHAR(64),
    INDEX idx_apps_user_id (user_id),
    INDEX idx_apps_status (status),
    INDEX idx_apps_category (category),
    INDEX idx_apps_platform (platform),
    INDEX idx_apps_published_at (published_at),
    INDEX idx_apps_downloads (downloads)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		Name:       "reviews",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS reviews (
    id VARCHAR(64) PRIMARY KEY,
    app_id VARCHAR(64),
    user_id VARCHAR(64),
    author_name VARCHAR(255) NOT NULL,
    author_role VARCHAR(100),
    stars INT NOT NULL,
    text TEXT NOT NULL,
    status VARCHAR(50) DEFAULT 'approved',
    created_at VARCHAR(64),
    INDEX idx_reviews_app_id (app_id),
    INDEX idx_reviews_user_id (user_id),
    INDEX idx_reviews_status (status),
    INDEX idx_reviews_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		Name:       "system_releases",
		PrimaryKey: []string{"version"},
		DDL: `CREATE TABLE IF NOT EXISTS system_releases (
    version VARCHAR(64) PRIMARY KEY,
    title VARCHAR(255),
    date VARCHAR(64),
    build_hash VARCHAR(128),
    notes TEXT,
    published_by VARCHAR(255),
    INDEX idx_system_releases_date (date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		Name:       "audit_logs",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS audit_logs (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64),
    action VARCHAR(100) NOT NULL,
    ip_address VARCHAR(100),
    user_agent VARCHAR(500),
    details TEXT,
    created_at VARCHAR(64),
    INDEX idx_audit_logs_user_id (user_id),
    INDEX idx_audit_logs_action (action),
    INDEX idx_audit_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	// Các bảng bổ sung an ninh nếu có trong SQLite
	{
		Name:       "api_keys",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS api_keys (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64),
    name VARCHAR(255),
    key_hash VARCHAR(128) UNIQUE,
    prefix VARCHAR(32),
    status VARCHAR(50) DEFAULT 'active',
    permissions TEXT,
    created_at VARCHAR(64),
    INDEX idx_api_keys_user_id (user_id),
    INDEX idx_api_keys_key_hash (key_hash),
    INDEX idx_api_keys_prefix (prefix),
    INDEX idx_api_keys_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
	{
		Name:       "security_events",
		PrimaryKey: []string{"id"},
		DDL: `CREATE TABLE IF NOT EXISTS security_events (
    id VARCHAR(64) PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    ip_address VARCHAR(100) NOT NULL,
    severity VARCHAR(50) NOT NULL DEFAULT 'warning',
    details TEXT,
    blocked_until VARCHAR(64),
    created_at VARCHAR(64),
    INDEX idx_security_events_ip (ip_address),
    INDEX idx_security_events_type (event_type),
    INDEX idx_security_events_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	},
}

// Config chứa thông tin kết nối môi trường
type Config struct {
	SQLitePath   string
	TiDBHost     string
	TiDBPort     string
	TiDBUser     string
	TiDBPassword string
	TiDBDatabase string
	TiDBTLS      string
	TiDBDSN      string
	BatchSize    int
	Mode         string // upsert, ignore, replace
	DryRun       bool
	Verbose      bool
	DropTables   bool
}

func main() {
	var (
		flagSQLite     = flag.String("sqlite", "", "Đường dẫn file SQLite nguồn (mặc định: tự động tìm data/supportflast.db)")
		flagEnv        = flag.String("env", "", "Đường dẫn file .env (mặc định: tự động tìm .env)")
		flagHost       = flag.String("host", "", "TiDB Cloud Host")
		flagPort       = flag.String("port", "", "TiDB Cloud Port (mặc định: 4000)")
		flagUser       = flag.String("user", "", "TiDB Cloud User")
		flagPass       = flag.String("password", "", "TiDB Cloud Password")
		flagDB         = flag.String("database", "", "TiDB Cloud Database (mặc định: supportflast)")
		flagTLS        = flag.String("tls", "", "Bật TLS cho TiDB (true/false, mặc định: true)")
		flagDSN        = flag.String("dsn", "", "Custom DSN cho TiDB Cloud (bỏ qua các tham số host/user/pass nếu dùng DSN)")
		flagBatch      = flag.Int("batch", 100, "Kích thước mỗi lô chèn dữ liệu (Batch size)")
		flagMode       = flag.String("mode", "upsert", "Chế độ chèn: upsert (ON DUPLICATE KEY UPDATE), ignore (INSERT IGNORE), replace (REPLACE INTO)")
		flagDryRun     = flag.Bool("dry-run", false, "Chế độ chạy thử: chỉ đọc SQLite và thống kê, không ghi vào TiDB Cloud")
		flagDropTables = flag.Bool("drop-tables", false, "Xóa bảng cũ trên TiDB Cloud trước khi tạo lại (CẨN THẬN!)")
		flagVerbose    = flag.Bool("verbose", false, "Hiển thị chi tiết từng bản ghi xử lý")
	)
	flag.Parse()

	printBanner()

	// 1. Nạp file .env nếu có
	envPath := resolveEnvPath(*flagEnv)
	if envPath != "" {
		if err := loadEnvFile(envPath); err != nil {
			log.Printf("[CẢNH BÁO] Không thể đọc file .env tại '%s': %v", envPath, err)
		} else {
			fmt.Printf("📄 [CẤU HÌNH] Đã nạp biến môi trường từ file: %s\n", envPath)
		}
	}

	// 2. Thu thập cấu hình
	cfg := Config{
		SQLitePath:   resolveSQLitePath(*flagSQLite),
		TiDBHost:     getEnvOrDefault("TIDB_HOST", *flagHost, "gateway01.ap-southeast-1.prod.aws.tidbcloud.com"),
		TiDBPort:     getEnvOrDefault("TIDB_PORT", *flagPort, "4000"),
		TiDBUser:     getEnvOrDefault("TIDB_USER", *flagUser, "vjwru2Q7m5oBvH2.root"),
		TiDBPassword: getEnvOrDefault("TIDB_PASSWORD", *flagPass, ""),
		TiDBDatabase: getEnvOrDefault("TIDB_DATABASE", *flagDB, "supportflast"),
		TiDBTLS:      getEnvOrDefault("TIDB_TLS", *flagTLS, "true"),
		TiDBDSN:      getEnvOrDefault("TIDB_DSN", *flagDSN, ""),
		BatchSize:    *flagBatch,
		Mode:         strings.ToLower(strings.TrimSpace(*flagMode)),
		DryRun:       *flagDryRun,
		Verbose:      *flagVerbose,
		DropTables:   *flagDropTables,
	}

	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}

	// Kiểm tra đường dẫn SQLite
	if _, err := os.Stat(cfg.SQLitePath); os.IsNotExist(err) {
		log.Fatalf("❌ [LỖI] Không tìm thấy file SQLite nguồn tại: %s\nVui lòng chỉ định bằng cờ: -sqlite <đường_dẫn>", cfg.SQLitePath)
	}
	fmt.Printf("📁 [SQLITE NGUỒN] File: %s\n", cfg.SQLitePath)

	// 3. Kết nối SQLite
	sqliteDB, err := sql.Open("sqlite", cfg.SQLitePath)
	if err != nil {
		log.Fatalf("❌ [LỖI] Không thể mở kết nối SQLite: %v", err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.Ping(); err != nil {
		log.Fatalf("❌ [LỖI] Không thể ping tới SQLite: %v", err)
	}
	fmt.Println("✅ [SQLITE] Kết nối thành công!")

	// 4. Nếu là chế độ Dry-Run, chỉ kiểm tra và thống kê
	if cfg.DryRun {
		fmt.Println("\n🔍 [DRY-RUN] Đang chạy ở chế độ kiểm tra dữ liệu SQLite (Không ghi vào TiDB)...")
		runDryRun(sqliteDB)
		fmt.Println("\n🎉 [DRY-RUN] Hoàn tất kiểm tra dữ liệu SQLite an toàn!")
		return
	}

	// 5. Kiểm tra mật khẩu TiDB Cloud
	if cfg.TiDBDSN == "" && cfg.TiDBPassword == "" {
		log.Fatalf(`❌ [LỖI] Thiếu mật khẩu kết nối TiDB Cloud (TIDB_PASSWORD)!
Vui lòng điền 'TIDB_PASSWORD=...' trong file .env hoặc truyền qua cờ: -password <mật_khẩu>
Hoặc đặt biến môi trường: $env:TIDB_PASSWORD="<mật_khẩu>"`)
	}

	// 6. Xây dựng DSN và kết nối TiDB Cloud
	tidbDSN := cfg.TiDBDSN
	if tidbDSN == "" {
		tlsParam := "false"
		if strings.ToLower(cfg.TiDBTLS) == "true" || cfg.TiDBTLS == "1" {
			tlsParam = "true"
		}
		tidbDSN = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?tls=%s&charset=utf8mb4&parseTime=True&loc=Local&interpolateParams=true&multiStatements=true",
			cfg.TiDBUser,
			cfg.TiDBPassword,
			cfg.TiDBHost,
			cfg.TiDBPort,
			cfg.TiDBDatabase,
			tlsParam,
		)
	}

	maskedHost := cfg.TiDBHost
	fmt.Printf("☁️ [TIDB CLOUD] Đang kết nối tới %s:%s (CSDL: %s, User: %s)...\n",
		maskedHost, cfg.TiDBPort, cfg.TiDBDatabase, cfg.TiDBUser)

	tidbDB, err := sql.Open("mysql", tidbDSN)
	if err != nil {
		log.Fatalf("❌ [LỖI] Không thể mở driver MySQL/TiDB: %v", err)
	}
	defer tidbDB.Close()

	tidbDB.SetConnMaxLifetime(5 * time.Minute)
	tidbDB.SetMaxOpenConns(10)
	tidbDB.SetMaxIdleConns(5)

	ctxPing, cancelPing := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelPing()

	if err := tidbDB.PingContext(ctxPing); err != nil {
		log.Fatalf("❌ [LỖI] Không thể kết nối tới TiDB Cloud: %v\nVui lòng kiểm tra lại Host, Port, User, Password và kết nối mạng.", err)
	}
	fmt.Println("✅ [TIDB CLOUD] Kết nối thành công!")

	// 7. Thực hiện di chuyển dữ liệu
	startTime := time.Now()
	totalMigrated := 0

	// Tắt foreign key checks tạm thời để nạp mượt mà không lo thứ tự chèn
	if _, err := tidbDB.Exec("SET foreign_key_checks = 0;"); err != nil {
		log.Printf("[CẢNH BÁO] Không thể đặt foreign_key_checks=0: %v (tiếp tục...)", err)
	}
	defer tidbDB.Exec("SET foreign_key_checks = 1;")

	fmt.Println("\n========================= BẮT ĐẦU DI CHUYỂN =========================")

	for _, tbl := range migrationTables {
		// Kiểm tra bảng có tồn tại trong SQLite không
		existsInSQLite, err := checkTableExistsSQLite(sqliteDB, tbl.Name)
		if err != nil {
			log.Printf("⚠️ [CẢNH BÁO] Kiểm tra bảng '%s' trong SQLite thất bại: %v", tbl.Name, err)
			continue
		}
		if !existsInSQLite {
			if cfg.Verbose {
				fmt.Printf("ℹ️ [BỎ QUA] Bảng '%s' không tồn tại trong SQLite.\n", tbl.Name)
			}
			continue
		}

		// Xử lý tạo bảng trên TiDB
		if cfg.DropTables {
			fmt.Printf("⚠️ [DROP] Đang xóa bảng cũ '%s' trên TiDB Cloud...\n", tbl.Name)
			if _, err := tidbDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`;", tbl.Name)); err != nil {
				log.Fatalf("❌ [LỖI] Xóa bảng '%s' thất bại: %v", tbl.Name, err)
			}
		}

		if _, err := tidbDB.Exec(tbl.DDL); err != nil {
			log.Fatalf("❌ [LỖI] Tạo bảng '%s' trên TiDB thất bại: %v\nDDL: %s", tbl.Name, err, tbl.DDL)
		}
		if cfg.Verbose {
			fmt.Printf("🛠️ [DDL] Bảng '%s' trên TiDB Cloud đã sẵn sàng.\n", tbl.Name)
		}

		// Đọc và chuyển dữ liệu
		count, err := migrateTable(sqliteDB, tidbDB, tbl, cfg)
		if err != nil {
			log.Fatalf("❌ [LỖI] Thất bại khi di chuyển bảng '%s': %v", tbl.Name, err)
		}

		totalMigrated += count
	}

	duration := time.Since(startTime)
	fmt.Println("=====================================================================")
	fmt.Printf("🎉 [HOÀN TẤT] Tổng cộng đã di chuyển thành công %d bản ghi trong %s!\n", totalMigrated, duration.Round(time.Millisecond))
	fmt.Println("🔒 Cơ chế chống trùng lặp (Upsert/ON DUPLICATE KEY UPDATE) đã bảo vệ toàn bộ dữ liệu an toàn.")
	fmt.Println("=====================================================================\n")

	// 8. Báo cáo đối chiếu số lượng bản ghi
	verifyCounts(sqliteDB, tidbDB)
}

// migrateTable di chuyển dữ liệu của một bảng từ SQLite sang TiDB theo lô với cơ chế chống trùng lặp
func migrateTable(sqliteDB, tidbDB *sql.DB, tbl TableDef, cfg Config) (int, error) {
	// Lấy danh sách cột từ SQLite
	rows, err := sqliteDB.Query(fmt.Sprintf("SELECT * FROM `%s`", tbl.Name))
	if err != nil {
		return 0, fmt.Errorf("truy vấn SQLite thất bại: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("không thể lấy tên cột: %w", err)
	}

	colCount := len(columns)
	if colCount == 0 {
		fmt.Printf("ℹ️ [BẢNG '%s'] Không có cột nào, bỏ qua.\n", tbl.Name)
		return 0, nil
	}

	// Đọc toàn bộ các hàng vào bộ nhớ theo lô
	var batchRecords [][]interface{}
	totalRows := 0

	for rows.Next() {
		// Tạo slice chứa con trỏ để scan
		values := make([]interface{}, colCount)
		valuePtrs := make([]interface{}, colCount)
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return 0, fmt.Errorf("scan hàng thất bại: %w", err)
		}

		// Chuẩn hóa kiểu dữ liệu
		normalizedRow := make([]interface{}, colCount)
		for i, val := range values {
			if b, ok := val.([]byte); ok {
				normalizedRow[i] = string(b)
			} else {
				normalizedRow[i] = val
			}
		}

		batchRecords = append(batchRecords, normalizedRow)
		totalRows++

		if len(batchRecords) >= cfg.BatchSize {
			if err := insertBatch(tidbDB, tbl, columns, batchRecords, cfg.Mode); err != nil {
				return 0, fmt.Errorf("chèn lô %d bản ghi vào bảng '%s' thất bại: %w", len(batchRecords), tbl.Name, err)
			}
			batchRecords = batchRecords[:0] // reset lô
		}
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("lỗi duyệt bản ghi SQLite: %w", err)
	}

	// Chèn lô còn lại
	if len(batchRecords) > 0 {
		if err := insertBatch(tidbDB, tbl, columns, batchRecords, cfg.Mode); err != nil {
			return 0, fmt.Errorf("chèn lô cuối (%d bản ghi) vào bảng '%s' thất bại: %w", len(batchRecords), tbl.Name, err)
		}
	}

	fmt.Printf("📦 [BẢNG '%s'] Đã di chuyển: %d bản ghi (Chống trùng lặp: %s)\n", tbl.Name, totalRows, strings.ToUpper(cfg.Mode))
	return totalRows, nil
}

// insertBatch thực hiện chèn dữ liệu theo lô với cú pháp Upsert chống trùng lặp
func insertBatch(db *sql.DB, tbl TableDef, columns []string, records [][]interface{}, mode string) error {
	if len(records) == 0 {
		return nil
	}

	quotedCols := make([]string, len(columns))
	for i, c := range columns {
		quotedCols[i] = fmt.Sprintf("`%s`", c)
	}

	numCols := len(columns)
	rowPlaceholder := "(" + strings.Repeat("?, ", numCols-1) + "?)"

	placeholders := make([]string, len(records))
	var allArgs []interface{}
	allArgs = make([]interface{}, 0, len(records)*numCols)

	for i, r := range records {
		placeholders[i] = rowPlaceholder
		allArgs = append(allArgs, r...)
	}

	var query string
	switch mode {
	case "replace":
		query = fmt.Sprintf("REPLACE INTO `%s` (%s) VALUES %s",
			tbl.Name,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
		)
	case "ignore":
		query = fmt.Sprintf("INSERT IGNORE INTO `%s` (%s) VALUES %s",
			tbl.Name,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
		)
	default: // "upsert"
		// Tạo mệnh đề ON DUPLICATE KEY UPDATE cho các cột không thuộc Primary Key
		var updateClauses []string
		pkMap := make(map[string]bool)
		for _, pk := range tbl.PrimaryKey {
			pkMap[strings.ToLower(pk)] = true
		}

		for _, col := range columns {
			if !pkMap[strings.ToLower(col)] {
				updateClauses = append(updateClauses, fmt.Sprintf("`%s` = VALUES(`%s`)", col, col))
			}
		}

		if len(updateClauses) > 0 {
			query = fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s ON DUPLICATE KEY UPDATE %s",
				tbl.Name,
				strings.Join(quotedCols, ", "),
				strings.Join(placeholders, ", "),
				strings.Join(updateClauses, ", "),
			)
		} else {
			// Nếu toàn bộ bảng chỉ là khóa chính
			query = fmt.Sprintf("INSERT IGNORE INTO `%s` (%s) VALUES %s",
				tbl.Name,
				strings.Join(quotedCols, ", "),
				strings.Join(placeholders, ", "),
			)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, query, allArgs...)
	return err
}

// runDryRun thống kê số lượng dữ liệu và kiểm tra cấu trúc SQLite mà không tác động tới TiDB Cloud
func runDryRun(db *sql.DB) {
	fmt.Printf("\n%-20s | %-12s | %-10s\n", "Tên Bảng", "Số Bản Ghi", "Trạng Thái")
	fmt.Println(strings.Repeat("-", 48))

	total := 0
	for _, tbl := range migrationTables {
		exists, err := checkTableExistsSQLite(db, tbl.Name)
		if err != nil {
			fmt.Printf("%-20s | %-12s | Lỗi: %v\n", tbl.Name, "N/A", err)
			continue
		}
		if !exists {
			fmt.Printf("%-20s | %-12s | Không tồn tại\n", tbl.Name, "0")
			continue
		}

		var count int
		err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tbl.Name)).Scan(&count)
		if err != nil {
			fmt.Printf("%-20s | %-12s | Lỗi đếm: %v\n", tbl.Name, "N/A", err)
			continue
		}

		total += count
		fmt.Printf("%-20s | %-12d | Sẵn sàng di chuyển\n", tbl.Name, count)
	}

	fmt.Println(strings.Repeat("-", 48))
	fmt.Printf("%-20s | %-12d | Tổng số bản ghi nguồn\n", "TỔNG CỘNG", total)
}

// verifyCounts đối chiếu số lượng bản ghi giữa SQLite và TiDB Cloud
func verifyCounts(sqliteDB, tidbDB *sql.DB) {
	fmt.Println("📊 [ĐỐI CHIẾU DỮ LIỆU] So sánh số lượng bản ghi giữa SQLite và TiDB Cloud:")
	fmt.Printf("%-20s | %-12s | %-12s | %-10s\n", "Tên Bảng", "SQLite", "TiDB Cloud", "Đồng Bộ")
	fmt.Println(strings.Repeat("-", 60))

	allMatch := true
	for _, tbl := range migrationTables {
		existsInSQLite, _ := checkTableExistsSQLite(sqliteDB, tbl.Name)
		if !existsInSQLite {
			continue
		}

		var countSQLite int
		_ = sqliteDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tbl.Name)).Scan(&countSQLite)

		var countTiDB int
		err := tidbDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tbl.Name)).Scan(&countTiDB)
		if err != nil {
			fmt.Printf("%-20s | %-12d | %-12s | Lỗi đếm TiDB\n", tbl.Name, countSQLite, "N/A")
			allMatch = false
			continue
		}

		matchStr := "✅ KHỚP"
		if countSQLite != countTiDB {
			matchStr = "⚠️ KHÁC BIỆT"
			allMatch = false
		}

		fmt.Printf("%-20s | %-12d | %-12d | %-10s\n", tbl.Name, countSQLite, countTiDB, matchStr)
	}

	fmt.Println(strings.Repeat("-", 60))
	if allMatch {
		fmt.Println("✨ [KẾT QUẢ] Tất cả các bảng đã được đồng bộ chuẩn xác 100%!")
	} else {
		fmt.Println("ℹ️ [LƯU Ý] Một số bảng có sự khác biệt về số lượng bản ghi (do đã có sẵn dữ liệu trước đó trên TiDB).")
	}
}

// checkTableExistsSQLite kiểm tra xem một bảng có tồn tại trong cơ sở dữ liệu SQLite hay không
func checkTableExistsSQLite(db *sql.DB, tableName string) (bool, error) {
	var count int
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;"
	err := db.QueryRow(query, tableName).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// resolveSQLitePath tìm đường dẫn file SQLite hợp lệ
func resolveSQLitePath(custom string) string {
	if custom != "" {
		return custom
	}

	candidates := []string{
		filepath.Join("data", "supportflast.db"),
		filepath.Join("..", "data", "supportflast.db"),
		filepath.Join("..", "..", "data", "supportflast.db"),
		`f:\supportflast.dev\data\supportflast.db`,
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return filepath.Join("data", "supportflast.db")
}

// resolveEnvPath tìm đường dẫn file .env hợp lệ
func resolveEnvPath(custom string) string {
	if custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
	}

	candidates := []string{
		".env",
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
		`f:\supportflast.dev\.env`,
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// loadEnvFile phân tích cú pháp file .env và nạp vào môi trường nếu biến chưa tồn tại
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// Loại bỏ dấu ngoặc đơn hoặc kép bao quanh giá trị nếu có
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		// Chỉ gán nếu biến môi trường chưa được thiết lập từ ngoài
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// getEnvOrDefault lấy giá trị từ biến môi trường, nếu rỗng thì trả về giá trị dự phòng
func getEnvOrDefault(envKey, flagVal, defaultVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultVal
}

func printBanner() {
	banner := `
╔═══════════════════════════════════════════════════════════════════════════╗
║       SUPPORTFLAST - CÔNG CỤ DI CHUYỂN DỮ LIỆU SQLITE SANG TIDB CLOUD     ║
║                    (An toàn - Phân lô - Chống trùng lặp)                  ║
╚═══════════════════════════════════════════════════════════════════════════╝
`
	fmt.Println(banner)
}
