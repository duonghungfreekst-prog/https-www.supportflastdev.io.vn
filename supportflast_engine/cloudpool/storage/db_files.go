package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"supportflast_engine/cloudpool/models"
)


func (s *DB) SaveVirtualFile(f *models.VirtualFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if f.UserID == "" {
		f.UserID = "user_admin"
	}

	var query string
	if s.IsMySQLOrTiDB() {
		query = `INSERT INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			user_id=VALUES(user_id),
			parent_id=VALUES(parent_id),
			name=VALUES(name),
			path=VALUES(path),
			size_bytes=VALUES(size_bytes),
			mime_type=VALUES(mime_type),
			sha256=VALUES(sha256),
			chunk_count=VALUES(chunk_count),
			is_encrypted=VALUES(is_encrypted),
			is_deleted=VALUES(is_deleted),
			deleted_at=VALUES(deleted_at),
			has_missing_chunks=VALUES(has_missing_chunks),
			updated_at=VALUES(updated_at)`
	} else {
		query = `INSERT INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			user_id=excluded.user_id,
			parent_id=excluded.parent_id,
			name=excluded.name,
			path=excluded.path,
			size_bytes=excluded.size_bytes,
			mime_type=excluded.mime_type,
			sha256=excluded.sha256,
			chunk_count=excluded.chunk_count,
			is_encrypted=excluded.is_encrypted,
			is_deleted=excluded.is_deleted,
			deleted_at=excluded.deleted_at,
			has_missing_chunks=excluded.has_missing_chunks,
			updated_at=excluded.updated_at`
	}

	_, err := s.db.Exec(query,
		f.ID, f.UserID, f.ParentID, f.Name, f.Path, f.IsDir, f.SizeBytes,
		f.MimeType, f.SHA256, f.ChunkCount, f.IsEncrypted, f.IsTrashed, f.DeletedAt, f.HasMissingChunks, f.CreatedAt, f.UpdatedAt,
	)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

// parseFlexibleTime chuyển đổi an toàn mọi giá trị ngày tháng từ DB (time.Time, string, []byte) sang time.Time chuẩn


func parseFlexibleTime(val interface{}) time.Time {
	if val == nil {
		return time.Now()
	}
	switch v := val.(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
		return time.Now()
	case sql.NullTime:
		if v.Valid {
			return v.Time
		}
		return time.Now()
	case *sql.NullTime:
		if v != nil && v.Valid {
			return v.Time
		}
		return time.Now()
	case sql.NullString:
		if v.Valid {
			return parseTimeString(v.String)
		}
		return time.Now()
	case *sql.NullString:
		if v != nil && v.Valid {
			return parseTimeString(v.String)
		}
		return time.Now()
	case []byte:
		return parseTimeString(string(v))
	case string:
		return parseTimeString(v)
	default:
		return time.Now()
	}
}



func parseFlexibleTimePtr(val interface{}) *time.Time {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case time.Time:
		return &v
	case *time.Time:
		return v
	case sql.NullTime:
		if v.Valid {
			return &v.Time
		}
		return nil
	case *sql.NullTime:
		if v != nil && v.Valid {
			return &v.Time
		}
		return nil
	case sql.NullString:
		if !v.Valid {
			return nil
		}
		str := strings.TrimSpace(v.String)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case *sql.NullString:
		if v == nil || !v.Valid {
			return nil
		}
		str := strings.TrimSpace(v.String)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case []byte:
		str := strings.TrimSpace(string(v))
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	case string:
		str := strings.TrimSpace(v)
		if str == "" || str == "NULL" || str == "null" || str == "0000-00-00 00:00:00" || str == "0000-00-00" {
			return nil
		}
		t := parseTimeString(str)
		return &t
	default:
		return nil
	}
}



func parseTimeString(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "0000-00-00 00:00:00" || s == "0000-00-00" || s == "NULL" || s == "null" {
		return time.Now()
	}
	if idx := strings.Index(s, " m="); idx != -1 {
		s = s[:idx]
	}
	formats := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700 -07",
		"2006-01-02 15:04:05.999999999 +0700 +07",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Now()
}



func (s *DB) GetVirtualFile(id string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE id = ?`, id)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}



func (s *DB) GetVirtualFileByPath(path string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE path = ? AND is_deleted = 0`, path)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}



func (s *DB) getVirtualFileUnsafe(id string) (*models.VirtualFile, error) {
	row := s.db.QueryRow(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE id = ?`, id)
	var f models.VirtualFile
	var sha, uid sql.NullString
	var isDel, hasMissing sql.NullBool
	var rawDelAt, rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated)
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	if isDel.Valid {
		f.IsTrashed = isDel.Bool
	}
	if hasMissing.Valid {
		f.HasMissingChunks = hasMissing.Bool
	}
	f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

// UpdateFileMissingChunks cập nhật cờ cảnh báo thiếu chunks cho một virtual file


func (s *DB) listVirtualFilesFromDB(userID, parentID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "user_admin" || userID == "admin" {
		// Admin sees all files and system partitions
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
	} else if userID == "guest" {
		guestMode := "view_only"
		var val string
		if errMode := s.db.QueryRow("SELECT `value` FROM settings WHERE `key` = 'guest_access_mode'").Scan(&val); errMode == nil && strings.TrimSpace(val) != "" {
			guestMode = strings.TrimSpace(val)
		}
		if guestMode != "strict" {
			// Khi guest_access_mode != "strict", cho phép khách xem danh sách tệp tin trong thư mục parent_id
			rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
		} else {
			// Trong chế độ strict, khách chỉ xem được phân vùng riêng của khách
			rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = 'guest' AND parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, parentID)
		}
	} else {
		// Child user is strictly isolated to their own uploaded files only
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = ? AND parent_id = ? AND id != 'root' AND is_deleted = 0 ORDER BY is_dir DESC, name ASC`, userID, parentID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)

		list = append(list, f)
	}

	// Xử lý quyền OTP cho người dùng bị hạn chế (guest hoặc child user)
	isRestrictedUser := userID != "user_admin" && userID != "all" && userID != "admin"
	if !isRestrictedUser {
		return list, nil
	}

	// Đánh dấu các file thuộc admin và kiểm tra xem thư mục có chứa tệp tin của admin không
	hasAdminFiles := false
	for i := range list {
		if list[i].UserID == "user_admin" || list[i].UserID == "admin" || list[i].UserID == "" {
			list[i].IsAdminOwned = true
			hasAdminFiles = true
		}
	}

	if !hasAdminFiles {
		return list, nil
	}

	// Tối ưu hóa triệt để (Query & CTE Optimizer):
	// Thay vì chạy N câu truy vấn đệ quy CTE (N+1 query) gây nghẽn mạng tới TiDB Cloud qua Internet,
	// chỉ cần thực hiện 1 truy vấn duy nhất lấy danh sách active public shares:
	shareQuery := `
	SELECT ps.file_id, COALESCE(vf.is_dir, 0) 
	FROM public_shares ps 
	LEFT JOIN virtual_files vf ON ps.file_id = vf.id 
	WHERE ps.is_active = 1 
	  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP) 
	  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads)`

	shareRows, err := s.db.Query(shareQuery)
	if err != nil {
		// Trong trường hợp lỗi truy vấn shares, fallback an toàn: toàn bộ file admin yêu cầu OTP
		for i := range list {
			if list[i].IsAdminOwned {
				list[i].RequiresOTP = true
			}
		}
		return list, nil
	}
	defer shareRows.Close()

	sharedFileIDs := make(map[string]bool)
	sharedFolderIDs := make(map[string]bool)
	hasSharedFolders := false

	for shareRows.Next() {
		var sFileID string
		var sIsDir int
		if err := shareRows.Scan(&sFileID, &sIsDir); err == nil && sFileID != "" {
			sharedFileIDs[sFileID] = true
			if sIsDir == 1 {
				sharedFolderIDs[sFileID] = true
				hasSharedFolders = true
			}
		}
	}

	// Nếu không có bất kỳ active share nào (chiếm 99% thời gian trong thực tế):
	// Toàn bộ các file admin đều yêu cầu OTP. KHÔNG CẦN GỌI BẤT KỲ TRUY VẤN ĐỆ QUY NÀO NỮA!
	if len(sharedFileIDs) == 0 {
		for i := range list {
			if list[i].IsAdminOwned {
				list[i].RequiresOTP = true
			}
		}
		return list, nil
	}

	// Nếu có active shares:
	// Kiểm tra xem chuỗi tổ tiên của thư mục parentID có được chia sẻ hay không.
	// Vì tất cả các tệp tin trong danh sách này đều thuộc cùng parentID, ta chỉ cần kiểm tra tổ tiên 1 lần duy nhất cho toàn bộ thư mục!
	parentChainIsShared := false
	if hasSharedFolders && parentID != "" && parentID != "root" {
		if sharedFolderIDs[parentID] {
			parentChainIsShared = true
		} else {
			// Truy vấn đệ quy CTE duy nhất 1 lần để lấy tất cả tổ tiên của parentID
			ancestorQuery := `
			WITH RECURSIVE parent_ancestors AS (
				SELECT id, parent_id FROM virtual_files WHERE id = ?
				UNION ALL
				SELECT vf.id, vf.parent_id FROM virtual_files vf
				JOIN parent_ancestors pa ON vf.id = pa.parent_id
				WHERE vf.id != 'root' AND vf.id != ''
			)
			SELECT id FROM parent_ancestors WHERE id != ?;`
			if aRows, err := s.db.Query(ancestorQuery, parentID, parentID); err == nil {
				for aRows.Next() {
					var ancestorID string
					if aRows.Scan(&ancestorID) == nil {
						if sharedFolderIDs[ancestorID] {
							parentChainIsShared = true
							break
						}
					}
				}
				aRows.Close()
			}
		}
	}

	// Cập nhật RequiresOTP cho từng file
	for i := range list {
		if !list[i].IsAdminOwned {
			continue
		}
		if parentChainIsShared || sharedFileIDs[list[i].ID] {
			list[i].RequiresOTP = false
		} else {
			list[i].RequiresOTP = true
		}
	}

	return list, nil
}

// isFileOrAncestorSharedUnlocked checks if the given file or any parent folder in its hierarchy has an active public share link


func (s *DB) isFileOrAncestorSharedUnlocked(fileID string) bool {
	if fileID == "" || fileID == "root" {
		return false
	}

	// 1. Kiểm tra trực tiếp fileID có được chia sẻ hay không (tận dụng index trên file_id)
	var directCount int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM public_shares 
		WHERE file_id = ? AND is_active = 1 
		  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) 
		  AND (max_downloads = 0 OR download_count < max_downloads)`, fileID).Scan(&directCount)
	if err == nil && directCount > 0 {
		return true
	}

	// 2. Kiểm tra nhanh xem toàn hệ thống có thư mục nào đang được public share không
	var folderShareCount int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM public_shares ps
		INNER JOIN virtual_files vf ON ps.file_id = vf.id
		WHERE vf.is_dir = 1 AND ps.is_active = 1 
		  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP) 
		  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads)`).Scan(&folderShareCount)
	if err != nil || folderShareCount == 0 {
		return false
	}

	// 3. Nếu có thư mục được chia sẻ, mới chạy CTE đệ quy kiểm tra tổ tiên
	query := `
	WITH RECURSIVE file_ancestors AS (
		SELECT id, parent_id FROM virtual_files WHERE id = ?
		UNION ALL
		SELECT vf.id, vf.parent_id FROM virtual_files vf
		JOIN file_ancestors fa ON vf.id = fa.parent_id
		WHERE vf.id != 'root' AND vf.id != ''
	)
	SELECT COUNT(*) FROM public_shares ps
	JOIN file_ancestors fa ON ps.file_id = fa.id
	WHERE ps.is_active = 1 
	  AND (ps.expires_at IS NULL OR ps.expires_at > CURRENT_TIMESTAMP)
	  AND (ps.max_downloads = 0 OR ps.download_count < ps.max_downloads);
	`
	var count int
	_ = s.db.QueryRow(query, fileID).Scan(&count)
	return count > 0
}

// IsFileOrAncestorShared checks if the given file or any parent folder in its hierarchy has an active public share link


func (s *DB) IsFileOrAncestorShared(fileID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.isFileOrAncestorSharedUnlocked(fileID)
}



func (s *DB) ListFilesByAccount(accountID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT DISTINCT f.id, f.parent_id, f.name, f.path, f.is_dir, f.size_bytes, f.mime_type, f.sha256, f.chunk_count, f.is_encrypted, f.is_deleted, f.deleted_at, f.has_missing_chunks, f.created_at, f.updated_at 
		FROM virtual_files f
		INNER JOIN file_chunks c ON f.id = c.file_id
		WHERE c.account_id = ? AND f.id != 'root' AND f.is_deleted = 0
		ORDER BY f.updated_at DESC`

	rows, err := s.db.Query(query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}

// ensureRootExistsUnlocked đảm bảo thư mục gốc root luôn tồn tại, cấu trúc chuẩn, và không bao giờ bị đánh dấu is_deleted = 1


func (s *DB) ensureRootExistsUnlocked() error {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM virtual_files WHERE id = 'root'").Scan(&count)
	now := time.Now()
	if count == 0 {
		var err error
		if s.IsMySQLOrTiDB() {
			_, err = s.db.Exec(`INSERT IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, is_deleted, created_at, updated_at) 
				VALUES ('root', 'user_admin', '', 'root', '/', 1, 0, 'inode/directory', 0, 0, 0, ?, ?)`, now, now)
		} else {
			_, err = s.db.Exec(`INSERT OR IGNORE INTO virtual_files (id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, chunk_count, is_encrypted, is_deleted, created_at, updated_at) 
				VALUES ('root', 'user_admin', '', 'root', '/', 1, 0, 'inode/directory', 0, 0, 0, ?, ?)`, now, now)
		}
		if err != nil {
			return fmt.Errorf("failed to create root folder: %w", err)
		}
	} else {
		// Đảm bảo root luôn ở trạng thái chuẩn: is_deleted = 0, parent_id = '', is_dir = 1, path = '/'
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL, parent_id = '', is_dir = 1, path = '/' WHERE id = 'root' AND (is_deleted != 0 OR parent_id != '' OR is_dir != 1 OR path != '/')`)
		if err != nil {
			return fmt.Errorf("failed to repair root folder status: %w", err)
		}
	}
	return nil
}

// EnsureRootExists đảm bảo thư mục gốc root luôn tồn tại và không bao giờ bị đánh dấu is_deleted = 1 (thread-safe)


func (s *DB) EnsureRootExists() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureRootExistsUnlocked()
}



func (s *DB) SoftDeleteVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "root" || id == "" {
		return fmt.Errorf("không thể xóa thư mục gốc (root folder is protected)")
	}

	vFile, err := s.getVirtualFileUnsafe(id)
	if err != nil {
		return err
	}

	now := time.Now()
	if vFile.IsDir {
		// Soft delete directory and all descendants
		prefix := strings.TrimSuffix(vFile.Path, "/") + "/%"
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 1, deleted_at = ? WHERE id = ? OR path LIKE ?`, now, id, prefix)
		if err == nil {
			s.InvalidateVFSCache()
			s.InvalidateStatsCache()
		}
		return err
	}

	_, err = s.db.Exec(`UPDATE virtual_files SET is_deleted = 1, deleted_at = ? WHERE id = ?`, now, id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) RestoreVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	vFile, err := s.getVirtualFileUnsafe(id)
	if err != nil {
		return err
	}

	if vFile.IsDir {
		prefix := strings.TrimSuffix(vFile.Path, "/") + "/%"
		_, err := s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL WHERE id = ? OR path LIKE ?`, id, prefix)
		if err == nil {
			s.InvalidateVFSCache()
			s.InvalidateStatsCache()
		}
		return err
	}

	_, err = s.db.Exec(`UPDATE virtual_files SET is_deleted = 0, deleted_at = NULL WHERE id = ?`, id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) ListTrashFiles(userID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if userID == "all" || userID == "user_admin" || userID == "admin" {
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE is_deleted = 1 ORDER BY deleted_at DESC`)
	} else {
		rows, err = s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at FROM virtual_files WHERE user_id = ? AND is_deleted = 1 ORDER BY deleted_at DESC`, userID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel, hasMissing sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &hasMissing, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		if isDel.Valid {
			f.IsTrashed = isDel.Bool
		}
		if hasMissing.Valid {
			f.HasMissingChunks = hasMissing.Bool
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}



func (s *DB) GetTrashFileIDs(userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if userID == "all" || userID == "user_admin" || userID == "admin" {
		rows, err = s.db.Query(`SELECT id FROM virtual_files WHERE is_deleted = 1`)
	} else {
		rows, err = s.db.Query(`SELECT id FROM virtual_files WHERE user_id = ? AND is_deleted = 1`, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}



func (s *DB) FindFileByNameInParent(userID, parentID, name string) (*models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var row *sql.Row
	parentCond := "(parent_id = ? OR (? = 'root' AND parent_id = '') OR (? = '' AND parent_id = 'root'))"
	if userID != "" {
		row = s.db.QueryRow(
			fmt.Sprintf(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, created_at, updated_at
			 FROM virtual_files
			 WHERE user_id = ? AND %s AND name = ? AND is_dir = 0 AND (is_deleted = 0 OR is_deleted IS NULL)
			 LIMIT 1`, parentCond),
			userID, parentID, parentID, parentID, name,
		)
	} else {
		row = s.db.QueryRow(
			fmt.Sprintf(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, created_at, updated_at
			 FROM virtual_files
			 WHERE %s AND name = ? AND is_dir = 0 AND (is_deleted = 0 OR is_deleted IS NULL)
			 LIMIT 1`, parentCond),
			parentID, parentID, parentID, name,
		)
	}

	var f models.VirtualFile
	var sha, uid sql.NullString
	var rawCreated, rawUpdated interface{}
	err := row.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir,
		&f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &rawCreated, &rawUpdated)
	if err == sql.ErrNoRows {
		return nil, nil // Không tìm thấy - không phải lỗi
	}
	if err != nil {
		return nil, err
	}
	if sha.Valid {
		f.SHA256 = sha.String
	}
	if uid.Valid {
		f.UserID = uid.String
	}
	f.CreatedAt = parseFlexibleTime(rawCreated)
	f.UpdatedAt = parseFlexibleTime(rawUpdated)
	return &f, nil
}

// -------------------------------------------------------------
// Settings & Stats
// -------------------------------------------------------------



func (s *DB) RenameVirtualFile(id string, newName, newPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "root" || id == "" {
		return fmt.Errorf("không thể đổi tên thư mục gốc (root folder is protected)")
	}

	// 1. Lấy thông tin hiện tại của tệp/thư mục
	var oldName, oldPath string
	var isDir bool
	err := s.db.QueryRow("SELECT name, path, is_dir FROM virtual_files WHERE id = ?", id).Scan(&oldName, &oldPath, &isDir)
	if err != nil {
		return fmt.Errorf("không tìm thấy tệp hoặc thư mục cần đổi tên: %w", err)
	}

	if oldName == newName && oldPath == newPath {
		return nil
	}

	now := time.Now()

	// 2. Mở transaction để cập nhật đồng bộ cây thư mục (P1.4: Folder Rename Cascade Path Update)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("lỗi khởi tạo transaction đổi tên: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Cập nhật bản ghi chính của tệp/thư mục
	_, err = tx.Exec("UPDATE virtual_files SET name=?, path=?, updated_at=? WHERE id=?", newName, newPath, now, id)
	if err != nil {
		return fmt.Errorf("lỗi cập nhật tên đối tượng: %w", err)
	}

	// Nếu là thư mục, cập nhật toàn bộ đường dẫn của tất cả tệp/thư mục con cháu
	if isDir {
		oldPrefix := strings.TrimSuffix(oldPath, "/") + "/"
		newPrefix := strings.TrimSuffix(newPath, "/") + "/"

		// Truy vấn danh sách các con cháu có đường dẫn bắt đầu bằng oldPrefix
		rows, qErr := tx.Query("SELECT id, path FROM virtual_files WHERE path LIKE ?", oldPrefix+"%")
		if qErr != nil {
			return fmt.Errorf("lỗi truy vấn các tệp con khi đổi tên thư mục: %w", qErr)
		}

		type childUpdate struct {
			childID   string
			childPath string
		}
		var updates []childUpdate
		for rows.Next() {
			var cid, cpath string
			if sErr := rows.Scan(&cid, &cpath); sErr == nil {
				if strings.HasPrefix(cpath, oldPrefix) {
					updatedChildPath := newPrefix + strings.TrimPrefix(cpath, oldPrefix)
					updates = append(updates, childUpdate{childID: cid, childPath: updatedChildPath})
				}
			}
		}
		rows.Close()

		for _, up := range updates {
			if _, uErr := tx.Exec("UPDATE virtual_files SET path=?, updated_at=? WHERE id=?", up.childPath, now, up.childID); uErr != nil {
				return fmt.Errorf("lỗi cập nhật đường dẫn tệp con (%s): %w", up.childPath, uErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lỗi hoàn tất transaction đổi tên: %w", err)
	}
	tx = nil

	s.InvalidateVFSCache()
	return nil
}

// -------------------------------------------------------------
// Chunks
// -------------------------------------------------------------



func (s *DB) DeleteVirtualFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "root" || id == "" {
		return fmt.Errorf("không thể xóa vĩnh viễn thư mục gốc (root folder is protected)")
	}
	_, err := s.db.Exec("DELETE FROM virtual_files WHERE id = ?", id)
	if err == nil {
		s.InvalidateVFSCache()
		s.InvalidateStatsCache()
	}
	return err
}

