package omnirouter

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DefaultLibSQLPath đường dẫn mặc định tới cơ sở dữ liệu LibSQL của 9Router
const DefaultLibSQLPath = `F:\9router-stt-upgraded\data\db\data.libsql`

// SyncResult kết quả sau khi đồng bộ dữ liệu
type SyncResult struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	ProvidersCount int    `json:"providersCount"`
	SettingsCount  int    `json:"settingsCount"`
	ProxiesCount   int    `json:"proxiesCount"`
	KVCount        int    `json:"kvCount"`
	KeysCount      int    `json:"keysCount"`
	LibSQLPath     string `json:"libsqlPath"`
}

// SyncFromLibSQL đồng bộ toàn diện dữ liệu từ tệp data.libsql vào TiDB / SQLite của Web
func SyncFromLibSQL(targetDB *sql.DB, customLibSQLPath string) (*SyncResult, error) {
	libPath := customLibSQLPath
	if libPath == "" {
		if envP := os.Getenv("LIBSQL_DATA_PATH"); envP != "" {
			libPath = envP
		} else {
			libPath = DefaultLibSQLPath
		}
	}

	if fi, err := os.Stat(libPath); err != nil || fi.IsDir() {
		// Thử tìm theo thư mục tương đối nếu đang chạy trên máy khác
		candidates := []string{
			libPath,
			filepath.Join("..", "9router-stt-upgraded", "data", "db", "data.libsql"),
			filepath.Join("data", "9router.libsql"),
		}
		found := false
		for _, c := range candidates {
			if f, e := os.Stat(c); e == nil && !f.IsDir() {
				libPath = c
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("không tìm thấy tệp CSDL LibSQL tại: %s", libPath)
		}
	}

	dsn := fmt.Sprintf("file:%s?mode=ro", libPath)
	srcDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("không thể mở tệp LibSQL: %w", err)
	}
	defer srcDB.Close()

	res := &SyncResult{
		LibSQLPath: libPath,
	}

	// 1. Đồng bộ Provider Connections (Tài khoản AI)
	provRows, err := srcDB.Query("SELECT id, provider, authType, name, email, priority, isActive, data, createdAt, updatedAt FROM providerConnections")
	if err == nil {
		defer provRows.Close()
		provCount := 0
		for provRows.Next() {
			var id, provider, authType, name, email, data, createdAt, updatedAt string
			var priority, isActive int
			if err := provRows.Scan(&id, &provider, &authType, &name, &email, &priority, &isActive, &data, &createdAt, &updatedAt); err == nil {
				_, insErr := targetDB.Exec(`
					INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE 
						provider = VALUES(provider),
						auth_type = VALUES(auth_type),
						name = VALUES(name),
						email = VALUES(email),
						priority = VALUES(priority),
						is_active = VALUES(is_active),
						data = VALUES(data),
						updated_at = VALUES(updated_at)
				`, id, provider, authType, name, email, priority, isActive, data, createdAt, updatedAt)

				// Nếu là SQLite, thử cú pháp ON CONFLICT nếu lệnh trên lỗi
				if insErr != nil && strings.Contains(insErr.Error(), "syntax error") {
					_, _ = targetDB.Exec(`
						INSERT INTO omni_providers (id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET
							provider = excluded.provider,
							auth_type = excluded.auth_type,
							name = excluded.name,
							email = excluded.email,
							priority = excluded.priority,
							is_active = excluded.is_active,
							data = excluded.data,
							updated_at = excluded.updated_at
					`, id, provider, authType, name, email, priority, isActive, data, createdAt, updatedAt)
				}
				provCount++
			}
		}
		res.ProvidersCount = provCount
	}

	// 2. Đồng bộ Settings
	setRows, err := srcDB.Query("SELECT id, data FROM settings")
	if err == nil {
		defer setRows.Close()
		setCount := 0
		for setRows.Next() {
			var id int
			var data string
			if err := setRows.Scan(&id, &data); err == nil {
				_, insErr := targetDB.Exec(`
					INSERT INTO omni_settings (id, data) VALUES (?, ?)
					ON DUPLICATE KEY UPDATE data = VALUES(data)
				`, id, data)
				if insErr != nil && strings.Contains(insErr.Error(), "syntax error") {
					_, _ = targetDB.Exec(`
						INSERT INTO omni_settings (id, data) VALUES (?, ?)
						ON CONFLICT(id) DO UPDATE SET data = excluded.data
					`, id, data)
				}
				setCount++
			}
		}
		res.SettingsCount = setCount
	}

	// 3. Đồng bộ Proxy Pools
	poolRows, err := srcDB.Query("SELECT id, isActive, testStatus, data, createdAt, updatedAt FROM proxyPools")
	if err == nil {
		defer poolRows.Close()
		poolCount := 0
		for poolRows.Next() {
			var id, testStatus, data, createdAt, updatedAt string
			var isActive int
			if err := poolRows.Scan(&id, &isActive, &testStatus, &data, &createdAt, &updatedAt); err == nil {
				_, insErr := targetDB.Exec(`
					INSERT INTO omni_proxy_pools (id, is_active, test_status, data, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE 
						is_active = VALUES(is_active),
						test_status = VALUES(test_status),
						data = VALUES(data),
						updated_at = VALUES(updated_at)
				`, id, isActive, testStatus, data, createdAt, updatedAt)
				if insErr != nil && strings.Contains(insErr.Error(), "syntax error") {
					_, _ = targetDB.Exec(`
						INSERT INTO omni_proxy_pools (id, is_active, test_status, data, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET
							is_active = excluded.is_active,
							test_status = excluded.test_status,
							data = excluded.data,
							updated_at = excluded.updated_at
					`, id, isActive, testStatus, data, createdAt, updatedAt)
				}
				poolCount++
			}
		}
		res.ProxiesCount = poolCount
	}

	// 4. Đồng bộ KV
	kvRows, err := srcDB.Query("SELECT scope, key, value FROM kv")
	if err == nil {
		defer kvRows.Close()
		kvCount := 0
		for kvRows.Next() {
			var scope, key, val string
			if err := kvRows.Scan(&scope, &key, &val); err == nil {
				_, insErr := targetDB.Exec(`
					INSERT INTO omni_kv (scope, key_name, val_data)
					VALUES (?, ?, ?)
					ON DUPLICATE KEY UPDATE val_data = VALUES(val_data)
				`, scope, key, val)
				if insErr != nil && strings.Contains(insErr.Error(), "syntax error") {
					_, _ = targetDB.Exec(`
						INSERT INTO omni_kv (scope, key_name, val_data)
						VALUES (?, ?, ?)
						ON CONFLICT(scope, key_name) DO UPDATE SET val_data = excluded.val_data
					`, scope, key, val)
				}
				kvCount++
			}
		}
		res.KVCount = kvCount
	}

	// Cập nhật lại bộ đệm RAM
	RefreshProviderCache(targetDB)
	RefreshKeyCache(targetDB)

	res.Success = true
	res.Message = fmt.Sprintf("Đồng bộ hoàn tất từ LibSQL: %d tài khoản AI, %d settings, %d proxy pools, %d kv entries",
		res.ProvidersCount, res.SettingsCount, res.ProxiesCount, res.KVCount)

	log.Printf("[ENGINE] [OMNIROUTER] [SYNC] %s", res.Message)
	return res, nil
}
