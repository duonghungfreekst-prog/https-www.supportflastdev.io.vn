package omnirouter

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

var (
	keyCacheMu sync.RWMutex
	keyCache   = make(map[string]*ClientKey)

	providerCacheMu sync.RWMutex
	providerCache   []ProviderConnection
)

// InitStorage khởi tạo các bảng CSDL cho OmniRouter trên TiDB hoặc SQLite
func InitStorage(db *sql.DB, isTiDB bool) error {
	var queries []string

	if isTiDB {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS omni_providers (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				provider VARCHAR(64) NOT NULL,
				auth_type VARCHAR(32) NOT NULL,
				name VARCHAR(191) NOT NULL,
				email VARCHAR(191) DEFAULT '',
				priority INT DEFAULT 1,
				is_active INT DEFAULT 1,
				data LONGTEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL,
				INDEX idx_omni_prov_active (is_active, priority)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_client_keys (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				name VARCHAR(128) NOT NULL,
				api_key VARCHAR(191) NOT NULL UNIQUE,
				key_hash VARCHAR(191) DEFAULT '',
				used_requests BIGINT DEFAULT 0,
				total_tokens BIGINT DEFAULT 0,
				is_active INT DEFAULT 1,
				created_at VARCHAR(64) NOT NULL,
				last_used_at VARCHAR(64) DEFAULT '',
				INDEX idx_omni_key_active (is_active)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_settings (
				id INT NOT NULL PRIMARY KEY,
				data LONGTEXT NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_proxy_pools (
				id VARCHAR(64) NOT NULL PRIMARY KEY,
				is_active INT DEFAULT 1,
				test_status VARCHAR(64) DEFAULT '',
				data LONGTEXT NOT NULL,
				created_at VARCHAR(64) NOT NULL,
				updated_at VARCHAR(64) NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_kv (
				scope VARCHAR(64) NOT NULL,
				key_name VARCHAR(128) NOT NULL,
				val_data LONGTEXT NOT NULL,
				PRIMARY KEY (scope, key_name)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

			`CREATE TABLE IF NOT EXISTS omni_usage_history (
				id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
				timestamp VARCHAR(64) NOT NULL,
				provider VARCHAR(64) NOT NULL,
				model VARCHAR(128) NOT NULL,
				connection_id VARCHAR(64) NOT NULL,
				api_key VARCHAR(128) DEFAULT '',
				endpoint VARCHAR(128) DEFAULT '',
				prompt_tokens BIGINT DEFAULT 0,
				completion_tokens BIGINT DEFAULT 0,
				total_tokens BIGINT DEFAULT 0,
				cost DOUBLE DEFAULT 0,
				status VARCHAR(64) DEFAULT 'success',
				tokens TEXT,
				meta LONGTEXT,
				latency_ms BIGINT DEFAULT 0,
				ttft_ms BIGINT DEFAULT 0,
				tps DOUBLE DEFAULT 0,
				cached_tokens BIGINT DEFAULT 0,
				status_code INT DEFAULT 200,
				error_code VARCHAR(64) DEFAULT '',
				INDEX idx_omni_uh_ts (timestamp),
				INDEX idx_omni_uh_conn (connection_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		}
	} else {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS omni_providers (
				id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				auth_type TEXT NOT NULL,
				name TEXT NOT NULL,
				email TEXT DEFAULT '',
				priority INTEGER DEFAULT 1,
				is_active INTEGER DEFAULT 1,
				data TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_prov_active ON omni_providers(is_active, priority);`,

			`CREATE TABLE IF NOT EXISTS omni_client_keys (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				api_key TEXT UNIQUE NOT NULL,
				key_hash TEXT DEFAULT '',
				used_requests INTEGER DEFAULT 0,
				total_tokens INTEGER DEFAULT 0,
				is_active INTEGER DEFAULT 1,
				created_at TEXT NOT NULL,
				last_used_at TEXT DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_key_active ON omni_client_keys(is_active);`,

			`CREATE TABLE IF NOT EXISTS omni_settings (
				id INTEGER PRIMARY KEY,
				data TEXT NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_proxy_pools (
				id TEXT PRIMARY KEY,
				is_active INTEGER DEFAULT 1,
				test_status TEXT DEFAULT '',
				data TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,

			`CREATE TABLE IF NOT EXISTS omni_kv (
				scope TEXT NOT NULL,
				key_name TEXT NOT NULL,
				val_data TEXT NOT NULL,
				PRIMARY KEY (scope, key_name)
			);`,

			`CREATE TABLE IF NOT EXISTS omni_usage_history (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				timestamp TEXT NOT NULL,
				provider TEXT NOT NULL,
				model TEXT NOT NULL,
				connection_id TEXT NOT NULL,
				api_key TEXT DEFAULT '',
				endpoint TEXT DEFAULT '',
				prompt_tokens INTEGER DEFAULT 0,
				completion_tokens INTEGER DEFAULT 0,
				total_tokens INTEGER DEFAULT 0,
				cost REAL DEFAULT 0,
				status TEXT DEFAULT 'success',
				tokens TEXT,
				meta TEXT,
				latency_ms INTEGER DEFAULT 0,
				ttft_ms INTEGER DEFAULT 0,
				tps REAL DEFAULT 0,
				cached_tokens INTEGER DEFAULT 0,
				status_code INTEGER DEFAULT 200,
				error_code TEXT DEFAULT ''
			);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_ts ON omni_usage_history(timestamp);`,
			`CREATE INDEX IF NOT EXISTS idx_omni_uh_conn ON omni_usage_history(connection_id);`,
		}
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("lỗi khởi tạo bảng omnirouter: %w (query: %s)", err, q)
		}
	}

	log.Println("[ENGINE] [OMNIROUTER] Đã khởi tạo hoàn tất cấu trúc lưu trữ 9Router trong Database")
	// Nạp sẵn cache
	RefreshKeyCache(db)
	RefreshProviderCache(db)
	return nil
}

// GenerateAPIKey tạo chuỗi API Key duy nhất dạng sk-9router-xxxxxxxx
func GenerateAPIKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-9router-" + hex.EncodeToString(b)
}

// HashString băm chuỗi SHA-256
func HashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// RefreshKeyCache nạp toàn bộ API key hợp lệ lên RAM để kiểm tra 0ms
func RefreshKeyCache(db *sql.DB) {
	rows, err := db.Query("SELECT id, name, api_key, key_hash, used_requests, total_tokens, is_active, created_at, last_used_at FROM omni_client_keys WHERE is_active = 1")
	if err != nil {
		return
	}
	defer rows.Close()

	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	keyCache = make(map[string]*ClientKey)

	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.KeyHash, &k.UsedRequests, &k.TotalTokens, &k.IsActive, &k.CreatedAt, &k.LastUsedAt); err == nil {
			keyCache[k.Key] = &k
		}
	}
}

// ValidateClientKey kiểm tra xem client key có hợp lệ không (RAM cache 0ms + constant-time comparison)
func ValidateClientKey(rawKey string) (*ClientKey, bool) {
	keyCacheMu.RLock()
	defer keyCacheMu.RUnlock()

	rawBytes := []byte(rawKey)
	for cachedK, item := range keyCache {
		if subtle.ConstantTimeCompare([]byte(cachedK), rawBytes) == 1 {
			if item.IsActive == 1 {
				return item, true
			}
			return nil, false
		}
	}
	return nil, false
}

// CreateNewClientKey sinh mã API Key mới và lưu xuống DB + RAM
func CreateNewClientKey(db *sql.DB, name string) (*ClientKey, error) {
	if strings.TrimSpace(name) == "" {
		name = "Antigravity-Device"
	}
	keyStr := GenerateAPIKey()
	id := fmt.Sprintf("key_%d", time.Now().UnixNano())
	now := NowISO()
	keyHash := HashString(keyStr)

	_, err := db.Exec(`
		INSERT INTO omni_client_keys (id, name, api_key, key_hash, used_requests, total_tokens, is_active, created_at, last_used_at)
		VALUES (?, ?, ?, ?, 0, 0, 1, ?, ?)
	`, id, name, keyStr, keyHash, now, now)
	if err != nil {
		return nil, err
	}

	item := &ClientKey{
		ID:           id,
		Name:         name,
		Key:          keyStr,
		KeyHash:      keyHash,
		UsedRequests: 0,
		TotalTokens:  0,
		IsActive:     1,
		CreatedAt:    now,
		LastUsedAt:   now,
	}

	keyCacheMu.Lock()
	keyCache[keyStr] = item
	keyCacheMu.Unlock()

	return item, nil
}

// ListAllClientKeys lấy toàn bộ danh sách API Key
func ListAllClientKeys(db *sql.DB) ([]ClientKey, error) {
	rows, err := db.Query("SELECT id, name, api_key, key_hash, used_requests, total_tokens, is_active, created_at, last_used_at FROM omni_client_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ClientKey
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.KeyHash, &k.UsedRequests, &k.TotalTokens, &k.IsActive, &k.CreatedAt, &k.LastUsedAt); err == nil {
			list = append(list, k)
		}
	}
	return list, nil
}

// RevokeClientKey khóa một API Key
func RevokeClientKey(db *sql.DB, id string) error {
	_, err := db.Exec("UPDATE omni_client_keys SET is_active = 0 WHERE id = ?", id)
	if err == nil {
		RefreshKeyCache(db)
	}
	return err
}

// DeleteClientKey xóa vĩnh viễn một API Key
func DeleteClientKey(db *sql.DB, id string) error {
	_, err := db.Exec("DELETE FROM omni_client_keys WHERE id = ?", id)
	if err == nil {
		RefreshKeyCache(db)
	}
	return err
}

// RecordKeyUsageAsync cập nhật số lượt và số token tiêu thụ ngầm
func RecordKeyUsageAsync(db *sql.DB, apiKey string, reqCount int64, tokenCount int64) {
	go func() {
		now := NowISO()
		_, _ = db.Exec(`
			UPDATE omni_client_keys 
			SET used_requests = used_requests + ?, 
			    total_tokens = total_tokens + ?,
			    last_used_at = ?
			WHERE api_key = ?
		`, reqCount, tokenCount, now, apiKey)

		keyCacheMu.Lock()
		if item, ok := keyCache[apiKey]; ok {
			item.UsedRequests += reqCount
			item.TotalTokens += tokenCount
			item.LastUsedAt = now
		}
		keyCacheMu.Unlock()
	}()
}

// RefreshProviderCache đọc các tài khoản AI từ DB nạp vào RAM
func RefreshProviderCache(db *sql.DB) {
	rows, err := db.Query("SELECT id, provider, auth_type, name, email, priority, is_active, data, created_at, updated_at FROM omni_providers WHERE is_active = 1 ORDER BY priority ASC")
	if err != nil {
		return
	}
	defer rows.Close()

	var list []ProviderConnection
	for rows.Next() {
		var p ProviderConnection
		var dataRaw string
		if err := rows.Scan(&p.ID, &p.Provider, &p.AuthType, &p.Name, &p.Email, &p.Priority, &p.IsActive, &dataRaw, &p.CreatedAt, &p.UpdatedAt); err == nil {
			p.Data = json.RawMessage(dataRaw)
			list = append(list, p)
		}
	}

	providerCacheMu.Lock()
	providerCache = list
	providerCacheMu.Unlock()
}

// GetCachedActiveProviders lấy danh sách tài khoản AI từ RAM
func GetCachedActiveProviders() []ProviderConnection {
	providerCacheMu.RLock()
	defer providerCacheMu.RUnlock()
	res := make([]ProviderConnection, len(providerCache))
	copy(res, providerCache)
	return res
}

// UpdateProviderData cập nhật data (token, quota...) của một provider
func UpdateProviderData(db *sql.DB, id string, dataMap map[string]interface{}) error {
	bytes, err := json.Marshal(dataMap)
	if err != nil {
		return err
	}
	now := NowISO()
	_, err = db.Exec("UPDATE omni_providers SET data = ?, updated_at = ? WHERE id = ?", string(bytes), now, id)
	if err == nil {
		RefreshProviderCache(db)
	}
	return err
}
