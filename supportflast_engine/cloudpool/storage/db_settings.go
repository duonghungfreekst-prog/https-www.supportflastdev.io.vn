package storage

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
)


func (s *DB) SaveAccount(acc *models.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = now
	}
	acc.UpdatedAt = now
	masterKey := s.getMasterKey()
	
	encEmail := core.EncryptSecret(masterKey, acc.Email)
	encName := core.EncryptSecret(masterKey, acc.Name)
	encAvatar := core.EncryptSecret(masterKey, acc.AvatarURL)
	encCreds := core.EncryptSecret(masterKey, acc.CredentialsJSON)
	encToken := core.EncryptSecret(masterKey, acc.TokenJSON)
	
	emailHash := core.BlindIndexHash(masterKey, acc.Email)
	nameHash := core.BlindIndexHash(masterKey, acc.Name)

	var query string
	if s.IsMySQLOrTiDB() {
		query = `INSERT INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			email=VALUES(email),
			email_hash=VALUES(email_hash),
			name=VALUES(name),
			name_hash=VALUES(name_hash),
			avatar_url=VALUES(avatar_url),
			credentials_json=VALUES(credentials_json),
			token_json=VALUES(token_json),
			root_folder_id=VALUES(root_folder_id),
			total_quota_bytes=VALUES(total_quota_bytes),
			used_quota_bytes=VALUES(used_quota_bytes),
			free_quota_bytes=VALUES(free_quota_bytes),
			status=VALUES(status),
			last_error=VALUES(last_error),
			updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO accounts (id, email, email_hash, name, name_hash, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			email=excluded.email,
			email_hash=excluded.email_hash,
			name=excluded.name,
			name_hash=excluded.name_hash,
			avatar_url=excluded.avatar_url,
			credentials_json=excluded.credentials_json,
			token_json=excluded.token_json,
			root_folder_id=excluded.root_folder_id,
			total_quota_bytes=excluded.total_quota_bytes,
			used_quota_bytes=excluded.used_quota_bytes,
			free_quota_bytes=excluded.free_quota_bytes,
			status=excluded.status,
			last_error=excluded.last_error,
			updated_at=excluded.updated_at`
	}

	_, err := s.db.Exec(query,
		acc.ID, encEmail, emailHash, encName, nameHash, encAvatar, acc.AuthType,
		encCreds, encToken, acc.RootFolderID,
		acc.TotalQuotaBytes, acc.UsedQuotaBytes, acc.FreeQuotaBytes,
		acc.Status, acc.LastError, acc.CreatedAt, acc.UpdatedAt,
	)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}



func (s *DB) GetAccount(id string) (*models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts WHERE id = ?`, id)

	var a models.Account
	var lastErr sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if lastErr.Valid {
		a.LastError = lastErr.String
	}
	a.CreatedAt = parseFlexibleTime(rawCreated)
	a.UpdatedAt = parseFlexibleTime(rawUpdated)
	if a.TotalQuotaBytes > 0 {
		a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
	}

	masterKey := s.getMasterKey()
	a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
	a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
	a.Email = core.DecryptSecret(masterKey, a.Email)
	a.Name = core.DecryptSecret(masterKey, a.Name)
	a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
	a.IsUploadExcluded = a.IsUploadExcludedAccount()
	return &a, nil
}

// GetAccountByEmail tìm kiếm tài khoản theo email (sử dụng Blind Index email_hash)


func (s *DB) GetAccountByEmail(email string) (*models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	masterKey := s.getMasterKey()
	emailHash := core.BlindIndexHash(masterKey, email)

	row := s.db.QueryRow(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts WHERE email_hash = ?`, emailHash)

	var a models.Account
	var lastErr sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if lastErr.Valid {
		a.LastError = lastErr.String
	}
	a.CreatedAt = parseFlexibleTime(rawCreated)
	a.UpdatedAt = parseFlexibleTime(rawUpdated)
	if a.TotalQuotaBytes > 0 {
		a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
	}

	a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
	a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
	a.Email = core.DecryptSecret(masterKey, a.Email)
	a.Name = core.DecryptSecret(masterKey, a.Name)
	a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
	a.IsUploadExcluded = a.IsUploadExcludedAccount()
	return &a, nil
}



func (s *DB) listAccountsFromDB() ([]models.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, email, name, avatar_url, auth_type, credentials_json, token_json, root_folder_id, total_quota_bytes, used_quota_bytes, free_quota_bytes, status, last_error, created_at, updated_at FROM accounts ORDER BY email ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	masterKey := s.getMasterKey()
	list := make([]models.Account, 0)
	for rows.Next() {
		var a models.Account
		var lastErr sql.NullString
		var rawCreated, rawUpdated interface{}
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.AuthType, &a.CredentialsJSON, &a.TokenJSON, &a.RootFolderID, &a.TotalQuotaBytes, &a.UsedQuotaBytes, &a.FreeQuotaBytes, &a.Status, &lastErr, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if lastErr.Valid {
			a.LastError = lastErr.String
		}
		a.CreatedAt = parseFlexibleTime(rawCreated)
		a.UpdatedAt = parseFlexibleTime(rawUpdated)
		if a.TotalQuotaBytes > 0 {
			a.UsagePercent = float64(a.UsedQuotaBytes) / float64(a.TotalQuotaBytes) * 100.0
		}
		a.CredentialsJSON = core.DecryptSecret(masterKey, a.CredentialsJSON)
		a.TokenJSON = core.DecryptSecret(masterKey, a.TokenJSON)
		a.Email = core.DecryptSecret(masterKey, a.Email)
		a.Name = core.DecryptSecret(masterKey, a.Name)
		a.AvatarURL = core.DecryptSecret(masterKey, a.AvatarURL)
		a.IsUploadExcluded = a.IsUploadExcludedAccount()
		list = append(list, a)
	}
	return list, nil
}



func (s *DB) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}



func (s *DB) UpdateAccountQuota(id string, total, used, free int64, status, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE accounts SET total_quota_bytes=?, used_quota_bytes=?, free_quota_bytes=?, status=?, last_error=?, updated_at=? WHERE id=?`,
		total, used, free, status, lastErr, time.Now(), id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

// UpdateAccountToken chỉ cập nhật cột token_json cho tài khoản, không ghi đè quota hay các trường khác.
// Sử dụng khi persistingTokenSource refresh token để tránh overwrite quota đã stale từ snapshot cũ.


func (s *DB) UpdateAccountToken(accountID, tokenJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	masterKey := s.getMasterKey()
	encToken := core.EncryptSecret(masterKey, tokenJSON)
	_, err := s.db.Exec("UPDATE accounts SET token_json = ?, updated_at = ? WHERE id = ?", encToken, time.Now(), accountID)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}

// -------------------------------------------------------------
// Virtual Files & Folders
// -------------------------------------------------------------



func (s *DB) ToggleAccountStatus(id, newStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE accounts SET status = ?, updated_at = ? WHERE id = ?", newStatus, time.Now(), id)
	if err == nil {
		s.InvalidateAccountsCache()
	}
	return err
}



func (s *DB) getSettingsFromDB() (*models.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT `key`, `value` FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	setMap := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			setMap[k] = v
		}
	}

	chunkSize, _ := strconv.ParseInt(setMap["chunk_size_bytes"], 10, 64)
	if chunkSize <= 0 {
		chunkSize = 20971520 // 20 MB
	}
	port, _ := strconv.Atoi(setMap["server_port"])
	if port <= 0 {
		port = 8080
	}

	redirectURL := setMap["redirect_url"]
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/api/accounts/oauth/callback"
	}

	guestMode := setMap["guest_access_mode"]
	if guestMode == "" {
		guestMode = "view_only"
	}
	allowSelfReg := setMap["allow_self_registration"] != "false"

	masterKey := s.getMasterKey()
	clientID := core.DecryptSecret(masterKey, setMap["google_client_id"])
	clientSecret := core.DecryptSecret(masterKey, setMap["google_client_secret"])

	// Fallback biến môi trường nếu trong DB chưa có cấu hình cụ thể
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID"))
		if clientID == "" {
			clientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
		}
		if clientID == "" {
			clientID = DefaultGoogleClientID
		}
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET"))
		if clientSecret == "" {
			clientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
		}
		if clientSecret == "" {
			clientSecret = DefaultGoogleClientSecret
		}
	}
	// Nếu redirect_url trong DB là mặc định localhost nhưng môi trường có OAUTH_REDIRECT_URL hợp lệ:
	if redirectURL == "" || strings.Contains(redirectURL, "localhost") || strings.Contains(redirectURL, "127.0.0.1") {
		if envRedirect := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_URL")); envRedirect != "" && !strings.Contains(envRedirect, "localhost") && !strings.Contains(envRedirect, "127.0.0.1") {
			redirectURL = envRedirect
		}
	}

	turnstileEnabled := setMap["turnstile_enabled"] != "false"
	turnstileSiteKey := setMap["turnstile_site_key"]
	if turnstileSiteKey == "" {
		turnstileSiteKey = strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY"))
		if turnstileSiteKey == "" {
			turnstileSiteKey = "1x00000000000000000000AA" // Cloudflare official testing site key
		}
	}

	turnstileSecretKey := core.DecryptSecret(masterKey, setMap["turnstile_secret_key"])
	if turnstileSecretKey == "" {
		turnstileSecretKey = strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY"))
		if turnstileSecretKey == "" {
			turnstileSecretKey = "1x0000000000000000000000000000000AA" // Cloudflare official testing secret key
		}
	}

	return &models.Settings{
		MasterPassphrase:      setMap["master_passphrase"],
		ChunkSizeBytes:        chunkSize,
		AllocationStrategy:    setMap["allocation_strategy"],
		GuestAccessMode:       guestMode,
		AllowSelfRegistration: allowSelfReg,
		WebDAVEnabled:         setMap["webdav_enabled"] == "true",
		WebDAVUsername:        setMap["webdav_username"],
		WebDAVPassword:        setMap["webdav_password"],
		ServerPort:            port,
		GoogleClientID:        clientID,
		GoogleClientSecret:    clientSecret,
		RedirectURL:           redirectURL,
		TurnstileEnabled:      turnstileEnabled,
		TurnstileSiteKey:      turnstileSiteKey,
		TurnstileSecretKey:    turnstileSecretKey,
	}, nil
}



func (s *DB) SaveSettings(set *models.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if set.RedirectURL == "" {
		set.RedirectURL = "http://localhost:8080/api/accounts/oauth/callback"
	}
	if set.GuestAccessMode == "" {
		set.GuestAccessMode = "view_only"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Lấy passphrase hiện tại trong DB làm oldKey
	var oldPass string
	_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&oldPass)
	if oldPass == "" {
		oldPass = "cloudpool_secure_master_key_2026"
	}
	oldKey := core.DeriveKey(oldPass, nil)

	// 2. Xác định master_passphrase mới: nếu trống hoặc là placeholder '********', giữ nguyên khóa cũ
	if set.MasterPassphrase == "" || set.MasterPassphrase == "********" {
		set.MasterPassphrase = oldPass
	}
	masterKey := core.DeriveKey(set.MasterPassphrase, nil)

	// 3. Giải mã dữ liệu Google OAuth bằng khóa cũ nếu còn prefix ENC: trước khi mã hóa lại bằng masterKey mới
	clientID := set.GoogleClientID
	if clientID == "" || clientID == "********" {
		var oldVal string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_id'").Scan(&oldVal)
		pt := core.DecryptSecret(oldKey, oldVal)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldVal)
		}
		if pt != "" {
			clientID = pt
		} else {
			clientID = DefaultGoogleClientID
		}
	} else if strings.HasPrefix(clientID, "ENC:") {
		pt := core.DecryptSecret(oldKey, clientID)
		if !strings.HasPrefix(pt, "ENC:") {
			clientID = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, clientID)
			if !strings.HasPrefix(ptNew, "ENC:") {
				clientID = ptNew
			}
		}
	}
	if clientID == "" {
		clientID = DefaultGoogleClientID
	}

	clientSecret := set.GoogleClientSecret
	if clientSecret == "" || clientSecret == "********" {
		var oldVal string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_secret'").Scan(&oldVal)
		pt := core.DecryptSecret(oldKey, oldVal)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldVal)
		}
		if pt != "" {
			clientSecret = pt
		} else {
			clientSecret = DefaultGoogleClientSecret
		}
	} else if strings.HasPrefix(clientSecret, "ENC:") {
		pt := core.DecryptSecret(oldKey, clientSecret)
		if !strings.HasPrefix(pt, "ENC:") {
			clientSecret = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, clientSecret)
			if !strings.HasPrefix(ptNew, "ENC:") {
				clientSecret = ptNew
			}
		}
	}
	if clientSecret == "" {
		clientSecret = DefaultGoogleClientSecret
	}

	// 4. Xử lý Turnstile Secret Key
	turnstileSecret := set.TurnstileSecretKey
	if turnstileSecret == "" || turnstileSecret == "********" {
		var oldTurnstileSecret string
		_ = tx.QueryRow("SELECT `value` FROM settings WHERE `key` = 'turnstile_secret_key'").Scan(&oldTurnstileSecret)
		pt := core.DecryptSecret(oldKey, oldTurnstileSecret)
		if pt == "" {
			pt = core.DecryptSecret(masterKey, oldTurnstileSecret)
		}
		if pt != "" {
			turnstileSecret = pt
		} else {
			turnstileSecret = "1x0000000000000000000000000000000AA"
		}
	} else if strings.HasPrefix(turnstileSecret, "ENC:") {
		pt := core.DecryptSecret(oldKey, turnstileSecret)
		if !strings.HasPrefix(pt, "ENC:") {
			turnstileSecret = pt
		} else {
			ptNew := core.DecryptSecret(masterKey, turnstileSecret)
			if !strings.HasPrefix(ptNew, "ENC:") {
				turnstileSecret = ptNew
			}
		}
	}
	if turnstileSecret == "" {
		turnstileSecret = "1x0000000000000000000000000000000AA"
	}
	if set.TurnstileSiteKey == "" {
		set.TurnstileSiteKey = "1x00000000000000000000AA"
	}

	encClientID := core.EncryptSecret(masterKey, clientID)
	encClientSecret := core.EncryptSecret(masterKey, clientSecret)
	encTurnstileSecret := core.EncryptSecret(masterKey, turnstileSecret)

	pairs := map[string]string{
		"master_passphrase":       set.MasterPassphrase,
		"chunk_size_bytes":        strconv.FormatInt(set.ChunkSizeBytes, 10),
		"allocation_strategy":     set.AllocationStrategy,
		"guest_access_mode":       set.GuestAccessMode,
		"allow_self_registration": strconv.FormatBool(set.AllowSelfRegistration),
		"webdav_enabled":          strconv.FormatBool(set.WebDAVEnabled),
		"webdav_username":         set.WebDAVUsername,
		"webdav_password":         set.WebDAVPassword,
		"server_port":             strconv.Itoa(set.ServerPort),
		"google_client_id":        encClientID,
		"google_client_secret":    encClientSecret,
		"redirect_url":            set.RedirectURL,
		"turnstile_enabled":       strconv.FormatBool(set.TurnstileEnabled),
		"turnstile_site_key":      set.TurnstileSiteKey,
		"turnstile_secret_key":    encTurnstileSecret,
	}

	for k, v := range pairs {
		var q string
		if s.IsMySQLOrTiDB() {
			q = "INSERT INTO settings (`key`, `value`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `value`=VALUES(`value`)"
		} else {
			q = "INSERT INTO settings (`key`, `value`) VALUES (?, ?) ON CONFLICT(`key`) DO UPDATE SET `value`=excluded.value"
		}
		if _, err := tx.Exec(q, k, v); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.InvalidateSettingsCache()
	return nil
}



func (s *DB) getStatsFromDB() (*models.StorageStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var stats models.StorageStats

	// Tối ưu hóa triệt để (Query Optimizer):
	// Gộp toàn bộ 5 truy vấn riêng lẻ thành 1 câu truy vấn duy nhất (Single Round-Trip),
	// loại bỏ hoàn toàn độ trễ mạng tích lũy (5 x 50ms = 250ms -> 1 x 50ms) tới TiDB Cloud Singapore.
	query := `SELECT 
		COALESCE((SELECT COUNT(*) FROM accounts), 0),
		COALESCE((SELECT SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) FROM accounts), 0),
		COALESCE((SELECT SUM(total_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT SUM(used_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT SUM(free_quota_bytes) FROM accounts), 0),
		COALESCE((SELECT COUNT(*) FROM virtual_files WHERE is_dir = 0 AND id != 'root'), 0),
		COALESCE((SELECT COUNT(*) FROM virtual_files WHERE is_dir = 1 AND id != 'root'), 0),
		COALESCE((SELECT COUNT(*) FROM file_chunks), 0),
		COALESCE((SELECT COUNT(*) FROM cloudpool_users), 0)`

	err := s.db.QueryRow(query).Scan(
		&stats.TotalAccounts,
		&stats.ActiveAccounts,
		&stats.TotalCapacityBytes,
		&stats.TotalUsedBytes,
		&stats.TotalFreeBytes,
		&stats.TotalFilesCount,
		&stats.TotalFoldersCount,
		&stats.TotalChunksCount,
		&stats.TotalUsersCount,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch storage stats: %w", err)
	}

	if stats.TotalCapacityBytes > 0 {
		stats.OverallUsagePercent = float64(stats.TotalUsedBytes) / float64(stats.TotalCapacityBytes) * 100.0
	}

	return &stats, nil
}

// -------------------------------------------------------------
// Activity Audit Logs
// -------------------------------------------------------------



func (s *DB) GetStorageBreakdown(userID string) (*models.StorageBreakdownResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var totalUsed, totalCap, freeBytes int64
	var totalFiles int

	// 1. Quotas
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(total_quota_bytes), 0), COALESCE(SUM(used_quota_bytes), 0), COALESCE(SUM(free_quota_bytes), 0) FROM accounts WHERE status = 'active'`).Scan(&totalCap, &totalUsed, &freeBytes)

	// 2. Fetch non-deleted files
	var query string
	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "user_admin" || userID == "admin" {
		query = `SELECT id, name, size_bytes, mime_type, updated_at FROM virtual_files WHERE is_dir = 0 AND is_deleted = 0 ORDER BY size_bytes DESC`
		rows, err = s.db.Query(query)
	} else {
		query = `SELECT id, name, size_bytes, mime_type, updated_at FROM virtual_files WHERE is_dir = 0 AND is_deleted = 0 AND user_id = ? ORDER BY size_bytes DESC`
		rows, err = s.db.Query(query, userID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type catAccumulator struct {
		Label string
		Icon  string
		Color string
		Bytes int64
		Count int
	}

	cats := map[string]*catAccumulator{
		"video":        {Label: "Video & Phim", Icon: "🎬", Color: "#ef4444"},
		"image":        {Label: "Hình ảnh", Icon: "🖼️", Color: "#10b981"},
		"audio":        {Label: "Âm thanh & Nhạc", Icon: "🎵", Color: "#f59e0b"},
		"document":     {Label: "Tài liệu & PDF", Icon: "📄", Color: "#8b5cf6"},
		"spreadsheet":  {Label: "Bảng tính & Excel", Icon: "📊", Color: "#22c55e"},
		"presentation": {Label: "Trình chiếu Slide", Icon: "📽️", Color: "#f97316"},
		"archive":      {Label: "Tệp nén & ISO", Icon: "📦", Color: "#ec4899"},
		"code":         {Label: "Mã nguồn & CSDL", Icon: "💻", Color: "#06b6d4"},
		"design":       {Label: "Thiết kế & 3D", Icon: "🎨", Color: "#a855f7"},
		"app":          {Label: "Ứng dụng & Cài đặt", Icon: "⚙️", Color: "#6366f1"},
		"other":        {Label: "Định dạng khác", Icon: "📁", Color: "#64748b"},
	}

	var allFilesTotalBytes int64
	var topFiles []models.TopFileItem

	for rows.Next() {
		var id, name, mimeType string
		var size int64
		var rawUpdated interface{}

		if err := rows.Scan(&id, &name, &size, &mimeType, &rawUpdated); err != nil {
			return nil, err
		}
		updated := parseFlexibleTime(rawUpdated)

		totalFiles++
		allFilesTotalBytes += size

		// Add to Top 10
		if len(topFiles) < 10 {
			topFiles = append(topFiles, models.TopFileItem{
				ID:        id,
				Name:      name,
				SizeBytes: size,
				MimeType:  mimeType,
				UpdatedAt: updated,
			})
		}

		// Categorize
		lowerMime := strings.ToLower(mimeType)
		lowerName := strings.ToLower(name)

		if strings.HasPrefix(lowerMime, "video/") || strings.HasSuffix(lowerName, ".mp4") || strings.HasSuffix(lowerName, ".webm") || strings.HasSuffix(lowerName, ".mkv") || strings.HasSuffix(lowerName, ".avi") || strings.HasSuffix(lowerName, ".mov") || strings.HasSuffix(lowerName, ".wmv") || strings.HasSuffix(lowerName, ".flv") || strings.HasSuffix(lowerName, ".m4v") || strings.HasSuffix(lowerName, ".ts") {
			cats["video"].Bytes += size
			cats["video"].Count++
		} else if strings.HasPrefix(lowerMime, "image/") || strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") || strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp") || strings.HasSuffix(lowerName, ".gif") || strings.HasSuffix(lowerName, ".svg") || strings.HasSuffix(lowerName, ".bmp") || strings.HasSuffix(lowerName, ".ico") || strings.HasSuffix(lowerName, ".heic") {
			cats["image"].Bytes += size
			cats["image"].Count++
		} else if strings.HasPrefix(lowerMime, "audio/") || strings.HasSuffix(lowerName, ".mp3") || strings.HasSuffix(lowerName, ".flac") || strings.HasSuffix(lowerName, ".wav") || strings.HasSuffix(lowerName, ".aac") || strings.HasSuffix(lowerName, ".m4a") || strings.HasSuffix(lowerName, ".ogg") || strings.HasSuffix(lowerName, ".wma") || strings.HasSuffix(lowerName, ".opus") {
			cats["audio"].Bytes += size
			cats["audio"].Count++
		} else if strings.Contains(lowerMime, "excel") || strings.Contains(lowerMime, "sheet") || strings.Contains(lowerMime, "csv") || strings.HasSuffix(lowerName, ".xls") || strings.HasSuffix(lowerName, ".xlsx") || strings.HasSuffix(lowerName, ".csv") || strings.HasSuffix(lowerName, ".tsv") || strings.HasSuffix(lowerName, ".ods") || strings.HasSuffix(lowerName, ".numbers") || strings.HasSuffix(lowerName, ".parquet") {
			cats["spreadsheet"].Bytes += size
			cats["spreadsheet"].Count++
		} else if strings.Contains(lowerMime, "presentation") || strings.Contains(lowerMime, "powerpoint") || strings.HasSuffix(lowerName, ".ppt") || strings.HasSuffix(lowerName, ".pptx") || strings.HasSuffix(lowerName, ".ppsx") || strings.HasSuffix(lowerName, ".key") || strings.HasSuffix(lowerName, ".odp") {
			cats["presentation"].Bytes += size
			cats["presentation"].Count++
		} else if strings.Contains(lowerMime, "pdf") || strings.Contains(lowerMime, "word") || strings.Contains(lowerMime, "document") || strings.HasSuffix(lowerName, ".pdf") || strings.HasSuffix(lowerName, ".doc") || strings.HasSuffix(lowerName, ".docx") || strings.HasSuffix(lowerName, ".odt") || strings.HasSuffix(lowerName, ".rtf") || strings.HasSuffix(lowerName, ".epub") || strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".log") {
			cats["document"].Bytes += size
			cats["document"].Count++
		} else if strings.Contains(lowerMime, "zip") || strings.Contains(lowerMime, "compressed") || strings.Contains(lowerMime, "tar") || strings.HasSuffix(lowerName, ".zip") || strings.HasSuffix(lowerName, ".rar") || strings.HasSuffix(lowerName, ".7z") || strings.HasSuffix(lowerName, ".tar") || strings.HasSuffix(lowerName, ".gz") || strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".iso") || strings.HasSuffix(lowerName, ".img") || strings.HasSuffix(lowerName, ".dmg") {
			cats["archive"].Bytes += size
			cats["archive"].Count++
		} else if strings.HasSuffix(lowerName, ".go") || strings.HasSuffix(lowerName, ".rs") || strings.HasSuffix(lowerName, ".py") || strings.HasSuffix(lowerName, ".js") || strings.HasSuffix(lowerName, ".ts") || strings.HasSuffix(lowerName, ".jsx") || strings.HasSuffix(lowerName, ".tsx") || strings.HasSuffix(lowerName, ".java") || strings.HasSuffix(lowerName, ".c") || strings.HasSuffix(lowerName, ".cpp") || strings.HasSuffix(lowerName, ".cs") || strings.HasSuffix(lowerName, ".php") || strings.HasSuffix(lowerName, ".sql") || strings.HasSuffix(lowerName, ".sqlite") || strings.HasSuffix(lowerName, ".db") || strings.HasSuffix(lowerName, ".json") || strings.HasSuffix(lowerName, ".yaml") || strings.HasSuffix(lowerName, ".yml") || strings.HasSuffix(lowerName, ".xml") || strings.HasSuffix(lowerName, ".html") || strings.HasSuffix(lowerName, ".css") || strings.HasSuffix(lowerName, ".sh") || strings.HasSuffix(lowerName, ".bat") || strings.HasSuffix(lowerName, ".ps1") || strings.HasSuffix(lowerName, ".md") {
			cats["code"].Bytes += size
			cats["code"].Count++
		} else if strings.HasSuffix(lowerName, ".psd") || strings.HasSuffix(lowerName, ".ai") || strings.HasSuffix(lowerName, ".eps") || strings.HasSuffix(lowerName, ".fig") || strings.HasSuffix(lowerName, ".xd") || strings.HasSuffix(lowerName, ".sketch") || strings.HasSuffix(lowerName, ".blend") || strings.HasSuffix(lowerName, ".obj") || strings.HasSuffix(lowerName, ".fbx") || strings.HasSuffix(lowerName, ".stl") || strings.HasSuffix(lowerName, ".dwg") || strings.HasSuffix(lowerName, ".dxf") {
			cats["design"].Bytes += size
			cats["design"].Count++
		} else if strings.HasSuffix(lowerName, ".exe") || strings.HasSuffix(lowerName, ".msi") || strings.HasSuffix(lowerName, ".apk") || strings.HasSuffix(lowerName, ".aab") || strings.HasSuffix(lowerName, ".ipa") || strings.HasSuffix(lowerName, ".deb") || strings.HasSuffix(lowerName, ".rpm") || strings.HasSuffix(lowerName, ".appimage") || strings.HasSuffix(lowerName, ".bin") || strings.HasSuffix(lowerName, ".pkg") {
			cats["app"].Bytes += size
			cats["app"].Count++
		} else {
			cats["other"].Bytes += size
			cats["other"].Count++
		}
	}

	orderedKeys := []string{"video", "image", "audio", "document", "spreadsheet", "presentation", "archive", "code", "design", "app", "other"}
	var catList []models.StorageCategoryBreakdown

	for _, k := range orderedKeys {
		c := cats[k]
		pct := 0.0
		if allFilesTotalBytes > 0 {
			pct = float64(c.Bytes) / float64(allFilesTotalBytes) * 100.0
		}
		catList = append(catList, models.StorageCategoryBreakdown{
			Category:   k,
			Label:      c.Label,
			Icon:       c.Icon,
			Color:      c.Color,
			TotalBytes: c.Bytes,
			FileCount:  c.Count,
			Percentage: pct,
		})
	}

	return &models.StorageBreakdownResponse{
		TotalUsedBytes: totalUsed,
		TotalCapBytes:  totalCap,
		FreeBytes:      freeBytes,
		TotalFiles:     totalFiles,
		Categories:     catList,
		TopFiles:       topFiles,
	}, nil
}




// StartAutoBackup initiates a background routine that backs up the database every 12 hours (Rule PHAN 7.3)


func (s *DB) LogActivity(entry *models.ActivityLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = "log_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO activity_logs (id, user_id, username, action, target, ip_address, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.UserID, entry.Username, entry.Action, entry.Target, entry.IPAddress, entry.Details, entry.CreatedAt)
	return err
}



func (s *DB) ListActivityLogs(limit int) ([]models.ActivityLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.db.Query(`SELECT id, user_id, username, action, target, ip_address, details, created_at 
		FROM activity_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.ActivityLog, 0)
	for rows.Next() {
		var l models.ActivityLog
		var rawCreatedAt interface{}
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Action, &l.Target, &l.IPAddress, &l.Details, &rawCreatedAt); err == nil {
			l.CreatedAt = parseFlexibleTime(rawCreatedAt)
			list = append(list, l)
		}
	}
	return list, nil
}

// -------------------------------------------------------------
// User Management & Private Storage
// -------------------------------------------------------------



type SQLResult struct {
	Columns       []string        `json:"columns"`
	Rows          [][]interface{} `json:"rows"`
	AffectedRows  int64           `json:"affected_rows"`
	ExecutionMs   float64         `json:"execution_ms"`
	IsSelectQuery bool            `json:"is_select"`
}



type TableColumnInfo struct {
	CID        int    `json:"cid"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	DefaultVal string `json:"default_val"`
	IsPK       bool   `json:"is_pk"`
}



type TableInfo struct {
	Name     string            `json:"name"`
	RowCount int64             `json:"row_count"`
	Columns  []TableColumnInfo `json:"columns"`
}



func (s *DB) GetDatabaseTables() ([]TableInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.IsMySQLOrTiDB() {
		rows, err := s.db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name ASC")
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		tables := make([]TableInfo, 0)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				var count int64
				_ = s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", name)).Scan(&count)

				tInfo := TableInfo{
					Name:     name,
					RowCount: count,
					Columns:  make([]TableColumnInfo, 0),
				}

				colRows, err := s.db.Query("SELECT ordinal_position, column_name, column_type, is_nullable, column_default, column_key FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position ASC", name)
				if err == nil {
					for colRows.Next() {
						var cid int
						var cname, ctype, isNullable, colKey string
						var dflt sql.NullString
						if err := colRows.Scan(&cid, &cname, &ctype, &isNullable, &dflt, &colKey); err == nil {
							tInfo.Columns = append(tInfo.Columns, TableColumnInfo{
								CID:        cid,
								Name:       cname,
								Type:       ctype,
								NotNull:    strings.ToUpper(isNullable) == "NO",
								DefaultVal: dflt.String,
								IsPK:       strings.ToUpper(colKey) == "PRI",
							})
						}
					}
					colRows.Close()
				}
				tables = append(tables, tInfo)
			}
		}
		return tables, nil
	}

	rows, err := s.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make([]TableInfo, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			var count int64
			_ = s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", name)).Scan(&count)

			tInfo := TableInfo{
				Name:     name,
				RowCount: count,
				Columns:  make([]TableColumnInfo, 0),
			}

			colRows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", name))
			if err == nil {
				for colRows.Next() {
					var cid, notnull, pk int
					var cname, ctype string
					var dflt sql.NullString
					if err := colRows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err == nil {
						tInfo.Columns = append(tInfo.Columns, TableColumnInfo{
							CID:        cid,
							Name:       cname,
							Type:       ctype,
							NotNull:    notnull == 1,
							DefaultVal: dflt.String,
							IsPK:       pk == 1,
						})
					}
				}
				colRows.Close()
			}

			tables = append(tables, tInfo)
		}
	}
	return tables, nil
}



func (s *DB) ExecuteRawSQL(query string) (*SQLResult, error) {
	startTime := time.Now()
	trimmed := strings.TrimSpace(strings.ToUpper(query))

	if strings.HasPrefix(trimmed, "SELECT") || strings.HasPrefix(trimmed, "PRAGMA") || strings.HasPrefix(trimmed, "EXPLAIN") {
		s.mu.RLock()
		defer s.mu.RUnlock()

		rows, err := s.db.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}

		resultRows := make([][]interface{}, 0)
		for rows.Next() {
			values := make([]interface{}, len(cols))
			valuePtrs := make([]interface{}, len(cols))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				return nil, err
			}

			rowList := make([]interface{}, len(cols))
			for i, v := range values {
				colName := strings.ToLower(cols[i])
				strVal := ""
				if b, ok := v.([]byte); ok {
					strVal = string(b)
				} else if s, ok := v.(string); ok {
					strVal = s
				}

				if strVal != "" {
					if colName == "credentials_json" || colName == "token_json" {
						rowList[i] = "🔒 [BẢO MẬT: MÃ HÓA AES-256 GCM (" + strconv.Itoa(len(strVal)) + " bytes)]"
					} else if (colName == "password_hash" || colName == "security_pin_hash") && len(strVal) > 6 {
						rowList[i] = "🔒 [HASH BẢO MẬT (" + strVal[:8] + "...)]"
					} else {
						rowList[i] = strVal
					}
				} else {
					rowList[i] = v
				}
			}
			resultRows = append(resultRows, rowList)
		}

		elapsed := float64(time.Since(startTime).Microseconds()) / 1000.0
		return &SQLResult{
			Columns:       cols,
			Rows:          resultRows,
			AffectedRows:  int64(len(resultRows)),
			ExecutionMs:   elapsed,
			IsSelectQuery: true,
		}, nil
	}

	// Non-SELECT (INSERT, UPDATE, DELETE, CREATE, DROP...)
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(query)
	if err != nil {
		return nil, err
	}

	affected, _ := res.RowsAffected()
	elapsed := float64(time.Since(startTime).Microseconds()) / 1000.0

	return &SQLResult{
		Columns:       []string{"status", "affected_rows"},
		Rows:          [][]interface{}{{"Thực thi thành công", affected}},
		AffectedRows:  affected,
		ExecutionMs:   elapsed,
		IsSelectQuery: false,
	}, nil
}



func (s *DB) OptimizeDatabase() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.IsMySQLOrTiDB() {
		return nil
	}
	_, err := s.db.Exec("VACUUM; PRAGMA optimize;")
	return err
}



func (s *DB) CheckDatabaseIntegrity() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.IsMySQLOrTiDB() {
		var pingCheck int = 1
		err := s.db.QueryRow("SELECT 1;").Scan(&pingCheck)
		if err != nil {
			return "fail", err
		}
		return "ok", nil
	}
	var result string
	err := s.db.QueryRow("PRAGMA integrity_check;").Scan(&result)
	return result, err
}



func (s *DB) BackupDatabase(destPath string) error {
	if s.IsMySQLOrTiDB() {
		return fmt.Errorf("sao lưu database qua VACUUM INTO chỉ khả dụng trên SQLite (đối với TiDB vui lòng dùng TiDB Backup & Restore BR hoặc mysqldump)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cleanPath := filepath.ToSlash(destPath)
	_, err := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", cleanPath))
	return err
}



func (s *DB) StartAutoBackup() {
	if s.IsMySQLOrTiDB() {
		log.Println("[ENGINE] [STORAGE] TiDB/MySQL driver detected: local auto-backup skipped (managed by cloud service)")
		return
	}
	go func() {
		backupDir := filepath.Join(filepath.Dir(s.path), "backups")
		os.MkdirAll(backupDir, 0755)

		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()

		for {
			// Do backup immediately on start, then every 12 hours
			func() {
				timestamp := time.Now().Format("20060102_150405")
				backupFile := filepath.Join(backupDir, fmt.Sprintf("cloudpool_metadata_%s.db", timestamp))

				// Use SQLite VACUUM INTO to flush WAL journals and ensure zero data corruption
				if err := s.BackupDatabase(backupFile); err != nil {
					fmt.Printf("[ENGINE] [WARN] VACUUM INTO backup failed (%v), attempting fallback copy...\n", err)
					s.mu.RLock()
					src, errOpen := os.Open(s.path)
					if errOpen != nil {
						s.mu.RUnlock()
						fmt.Printf("[ENGINE] [ERROR] Failed to open DB for backup fallback: %v\n", errOpen)
						return
					}
					dst, errCreate := os.Create(backupFile)
					if errCreate != nil {
						src.Close()
						s.mu.RUnlock()
						fmt.Printf("[ENGINE] [ERROR] Failed to create backup file fallback: %v\n", errCreate)
						return
					}
					_, errCopy := io.Copy(dst, src)
					src.Close()
					dst.Close()
					s.mu.RUnlock()
					if errCopy != nil {
						fmt.Printf("[ENGINE] [ERROR] Fallback copy failed: %v\n", errCopy)
						return
					}
					fmt.Printf("[ENGINE] [BACKUP] Fallback backup created: %s\n", backupFile)
				} else {
					fmt.Printf("[ENGINE] [BACKUP] Clean WAL-safe backup created successfully: %s\n", backupFile)
				}

				// Cleanup old backups (keep last 14 = 7 days)
				files, err := os.ReadDir(backupDir)
				if err == nil && len(files) > 14 {
					// files are sorted by name, which means chronological due to YYYYMMDD_HHMMSS
					for i := 0; i < len(files)-14; i++ {
						os.Remove(filepath.Join(backupDir, files[i].Name()))
					}
				}
			}()

			<-ticker.C
		}
	}()
}

// =============================================================================
// HỆ THỐNG TỰ ĐỘNG ĐỒNG BỘ & KHỞI TẠO DỰ PHÒNG CSDL (AUTO-SYNC & RESILIENCE)
// Tuân thủ Quy chế PHAN 0, PHAN 1.5, PHAN 3.5, PHAN 7.1
// =============================================================================

// EnsureTiDBCloudPoolDataReady kiểm tra tính sẵn sàng của các bảng accounts, virtual_files,
// settings khi khởi động TiDB Cloud. Nếu thiếu cấu hình mặc định (master passphrase, chunk size,
// strategy...) hoặc bảng rỗng, tự động khởi tạo an toàn (seed idempotent / auto-sync).
