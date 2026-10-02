package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"supportflast_engine/cloudpool/models"
	"github.com/google/uuid"
)


func (s *DB) CreatePublicShare(share *models.PublicShare) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if share.ID == "" {
		share.ID = "sh_" + uuid.New().String()
	}
	if share.CreatedAt.IsZero() {
		share.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`INSERT INTO public_shares (id, file_id, created_by, password_hash, max_downloads, download_count, expires_at, created_at, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		share.ID, share.FileID, share.CreatedBy, share.PasswordHash, share.MaxDownloads, share.DownloadCount, share.ExpiresAt, share.CreatedAt, 1)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}



func (s *DB) GetPublicShare(id string) (*models.PublicShare, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT s.id, s.file_id, s.created_by, s.password_hash, s.max_downloads, s.download_count, s.expires_at, s.created_at, s.is_active,
		COALESCE(f.name, 'Tệp tin đã xóa'), COALESCE(f.size_bytes, 0), COALESCE(f.mime_type, 'application/octet-stream'), COALESCE(f.is_dir, 0)
		FROM public_shares s
		LEFT JOIN virtual_files f ON s.file_id = f.id
		WHERE s.id = ?`

	row := s.db.QueryRow(query, id)
	var sh models.PublicShare
	var rawExp, rawCreated interface{}
	var passHash string
	var isActiveInt, isDirInt int

	err := row.Scan(&sh.ID, &sh.FileID, &sh.CreatedBy, &passHash, &sh.MaxDownloads, &sh.DownloadCount, &rawExp, &rawCreated, &isActiveInt, &sh.FileName, &sh.FileSize, &sh.MimeType, &isDirInt)
	if err != nil {
		return nil, err
	}

	sh.PasswordHash = passHash
	sh.HasPassword = (passHash != "")
	sh.ExpiresAt = parseFlexibleTimePtr(rawExp)
	sh.CreatedAt = parseFlexibleTime(rawCreated)
	sh.IsActive = (isActiveInt == 1)
	sh.IsDir = (isDirInt == 1)

	// If folder, calculate total size and file count
	if sh.IsDir {
		sh.MimeType = "directory"
		totSize, count, err := s.GetFolderStats(sh.FileID)
		if err == nil {
			sh.FileSize = totSize
			sh.FileCount = count
		}
	}

	// Check expiry
	if sh.ExpiresAt != nil && time.Now().After(*sh.ExpiresAt) {
		sh.IsActive = false
	}
	// Check download limit
	if sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads {
		sh.IsActive = false
	}

	return &sh, nil
}



func (s *DB) ListPublicShares() ([]models.PublicShare, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT s.id, s.file_id, s.created_by, s.password_hash, s.max_downloads, s.download_count, s.expires_at, s.created_at, s.is_active,
		COALESCE(f.name, 'Tệp tin đã xóa'), COALESCE(f.size_bytes, 0), COALESCE(f.mime_type, 'application/octet-stream'), COALESCE(f.is_dir, 0)
		FROM public_shares s
		LEFT JOIN virtual_files f ON s.file_id = f.id
		ORDER BY s.created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.PublicShare, 0)
	now := time.Now()
	for rows.Next() {
		var sh models.PublicShare
		var rawExp, rawCreated interface{}
		var passHash string
		var isActiveInt, isDirInt int

		if err := rows.Scan(&sh.ID, &sh.FileID, &sh.CreatedBy, &passHash, &sh.MaxDownloads, &sh.DownloadCount, &rawExp, &rawCreated, &isActiveInt, &sh.FileName, &sh.FileSize, &sh.MimeType, &isDirInt); err != nil {
			return nil, err
		}

		sh.PasswordHash = passHash
		sh.HasPassword = (passHash != "")
		sh.ExpiresAt = parseFlexibleTimePtr(rawExp)
		sh.CreatedAt = parseFlexibleTime(rawCreated)
		sh.IsActive = (isActiveInt == 1)
		sh.IsDir = (isDirInt == 1)

		if sh.IsDir {
			sh.MimeType = "directory"
			totSize, count, err := s.GetFolderStats(sh.FileID)
			if err == nil {
				sh.FileSize = totSize
				sh.FileCount = count
			}
		}

		if sh.ExpiresAt != nil && now.After(*sh.ExpiresAt) {
			sh.IsActive = false
		}
		if sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads {
			sh.IsActive = false
		}

		list = append(list, sh)
	}
	return list, nil
}



func (s *DB) GetFolderStats(folderID string) (int64, int, error) {
	query := `
	WITH RECURSIVE folder_tree AS (
		SELECT id, is_dir, size_bytes FROM virtual_files WHERE id = ? AND is_deleted = 0
		UNION ALL
		SELECT vf.id, vf.is_dir, vf.size_bytes FROM virtual_files vf
		JOIN folder_tree ft ON vf.parent_id = ft.id
		WHERE vf.is_deleted = 0
	)
	SELECT COALESCE(SUM(CASE WHEN is_dir = 0 THEN size_bytes ELSE 0 END), 0),
	       COUNT(CASE WHEN is_dir = 0 THEN 1 END)
	FROM folder_tree;
	`
	var totalSize int64
	var fileCount int
	err := s.db.QueryRow(query, folderID).Scan(&totalSize, &fileCount)
	return totalSize, fileCount, err
}



func (s *DB) ListFilesInFolderForShare(rootFolderID, currentFolderID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Verify currentFolderID is rootFolderID or a descendant of rootFolderID
	if currentFolderID != rootFolderID {
		var isDescendant int
		checkQuery := `
		WITH RECURSIVE folder_tree AS (
			SELECT id FROM virtual_files WHERE id = ? AND is_deleted = 0
			UNION ALL
			SELECT vf.id FROM virtual_files vf
			JOIN folder_tree ft ON vf.parent_id = ft.id
			WHERE vf.is_deleted = 0
		)
		SELECT COUNT(*) FROM folder_tree WHERE id = ?;
		`
		_ = s.db.QueryRow(checkQuery, rootFolderID, currentFolderID).Scan(&isDescendant)
		if isDescendant == 0 {
			return nil, fmt.Errorf("thư mục không thuộc cây thư mục được chia sẻ")
		}
	}

	rows, err := s.db.Query(`SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, created_at, updated_at 
		FROM virtual_files 
		WHERE parent_id = ? AND is_deleted = 0 
		ORDER BY is_dir DESC, name ASC`, currentFolderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.VirtualFile, 0)
	for rows.Next() {
		var f models.VirtualFile
		var sha, uid sql.NullString
		var isDel sql.NullBool
		var rawDelAt, rawCreated, rawUpdated interface{}
		if err := rows.Scan(&f.ID, &uid, &f.ParentID, &f.Name, &f.Path, &f.IsDir, &f.SizeBytes, &f.MimeType, &sha, &f.ChunkCount, &f.IsEncrypted, &isDel, &rawDelAt, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		if sha.Valid {
			f.SHA256 = sha.String
		}
		if uid.Valid {
			f.UserID = uid.String
		}
		f.DeletedAt = parseFlexibleTimePtr(rawDelAt)
		f.CreatedAt = parseFlexibleTime(rawCreated)
		f.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, f)
	}
	return list, nil
}

// IsFileDescendantOfFolder kiểm tra xem targetFileID có phải là tệp con cháu nằm trong rootFolderID hay không (Bảo vệ chống IDOR qua Public Share)


func (s *DB) IsFileDescendantOfFolder(rootFolderID, targetFileID string) bool {
	if rootFolderID == "" || targetFileID == "" {
		return false
	}
	if rootFolderID == targetFileID {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var isDescendant int
	checkQuery := `
	WITH RECURSIVE folder_tree AS (
		SELECT id FROM virtual_files WHERE id = ? AND is_deleted = 0
		UNION ALL
		SELECT vf.id FROM virtual_files vf
		JOIN folder_tree ft ON vf.parent_id = ft.id
		WHERE vf.is_deleted = 0
	)
	SELECT COUNT(*) FROM folder_tree WHERE id = ?;
	`
	_ = s.db.QueryRow(checkQuery, rootFolderID, targetFileID).Scan(&isDescendant)
	return isDescendant > 0
}



type FileInTree struct {
	File         models.VirtualFile
	RelativePath string
}



func (s *DB) GetAllFilesInFolderTree(rootFolderID string) ([]FileInTree, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var concatExpr string
	if s.IsMySQLOrTiDB() {
		concatExpr = "CONCAT(ft.rel_path, '/', vf.name)"
	} else {
		concatExpr = "ft.rel_path || '/' || vf.name"
	}

	query := fmt.Sprintf(`
	WITH RECURSIVE folder_tree AS (
		SELECT id, name, parent_id, is_dir, size_bytes, mime_type, '' AS rel_path
		FROM virtual_files
		WHERE id = ? AND is_deleted = 0
		UNION ALL
		SELECT vf.id, vf.name, vf.parent_id, vf.is_dir, vf.size_bytes, vf.mime_type,
		       CASE WHEN ft.rel_path = '' THEN vf.name ELSE %s END
		FROM virtual_files vf
		JOIN folder_tree ft ON vf.parent_id = ft.id
		WHERE vf.is_deleted = 0
	)
	SELECT id, name, parent_id, is_dir, size_bytes, mime_type, rel_path
	FROM folder_tree
	WHERE is_dir = 0;
	`, concatExpr)
	rows, err := s.db.Query(query, rootFolderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []FileInTree
	for rows.Next() {
		var f models.VirtualFile
		var relPath string
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.IsDir, &f.SizeBytes, &f.MimeType, &relPath); err != nil {
			return nil, err
		}
		files = append(files, FileInTree{
			File:         f,
			RelativePath: relPath,
		})
	}
	return files, nil
}



func (s *DB) RevokePublicShare(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM public_shares WHERE id = ?`, id)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}



func (s *DB) IncrementPublicShareDownload(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE public_shares SET download_count = download_count + 1 WHERE id = ?`, id)
	return err
}

// ConsumePublicShareDownload tăng download_count một cách nguyên tử và kiểm tra max_downloads (P1.5)


func (s *DB) ConsumePublicShareDownload(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	res, err := s.db.Exec(`
		UPDATE public_shares 
		SET download_count = download_count + 1,
		    is_active = CASE WHEN max_downloads > 0 AND download_count + 1 >= max_downloads THEN 0 ELSE is_active END
		WHERE id = ? 
		  AND is_active = 1 
		  AND (expires_at IS NULL OR expires_at > ?) 
		  AND (max_downloads = 0 OR download_count < max_downloads)
	`, id, now)
	if err != nil {
		return fmt.Errorf("lỗi thực thi trừ lượt tải: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("lỗi kiểm tra kết quả trừ lượt tải: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("liên kết chia sẻ đã hết lượt tải hoặc đã hết hạn")
	}

	s.InvalidateVFSCache()
	return nil
}

// -------------------------------------------------------------
// Storage Breakdown & Analytics
// -------------------------------------------------------------



func (s *DB) CreateFileOTP(otp *models.FileAccessOTP) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if otp.ID == "" {
		otp.ID = "otp_" + uuid.New().String()
	}
	if otp.CreatedAt.IsZero() {
		otp.CreatedAt = time.Now()
	}
	if otp.CreatedBy == "" {
		otp.CreatedBy = "user_admin"
	}
	if otp.TargetUserID == "" {
		otp.TargetUserID = "all"
	}

	_, err := s.db.Exec(`INSERT INTO file_access_otps (id, file_id, file_name, target_user_id, otp_code, created_by, is_used, expires_at, created_at) 
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		otp.ID, otp.FileID, otp.FileName, otp.TargetUserID, otp.OTPCode, otp.CreatedBy, otp.ExpiresAt, otp.CreatedAt)
	return err
}



func (s *DB) VerifyAndBurnOTP(fileID, userID, otpCode string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	otpCode = strings.TrimSpace(otpCode)
	if otpCode == "" {
		return false, fmt.Errorf("Mã OTP không được để trống")
	}

	var otpID string
	var rawExpires interface{}
	var isUsed int

	row := s.db.QueryRow(`SELECT id, expires_at, is_used FROM file_access_otps 
		WHERE file_id = ? AND otp_code = ? AND (target_user_id = 'all' OR target_user_id = ?) 
		ORDER BY created_at DESC LIMIT 1`, fileID, otpCode, userID)

	if err := row.Scan(&otpID, &rawExpires, &isUsed); err != nil {
		return false, fmt.Errorf("Mã OTP không chính xác hoặc không áp dụng cho tệp tin này")
	}

	expiresAt := parseFlexibleTime(rawExpires)

	if isUsed == 1 {
		return false, fmt.Errorf("Mã OTP này đã được sử dụng (Mỗi mã chỉ có giá trị 1 lần duy nhất)")
	}

	if time.Now().After(expiresAt) {
		return false, fmt.Errorf("Mã OTP đã hết thời hạn hiệu lực")
	}

	// Burn OTP immediately (Single-use per Rule)
	now := time.Now()
	_, err := s.db.Exec(`UPDATE file_access_otps SET is_used = 1, used_by = ?, used_at = ? WHERE id = ?`, userID, now, otpID)
	if err != nil {
		return false, err
	}

	return true, nil
}

// VerifyOTPOnly kiểm tra tính hợp lệ của mã OTP mà không huỷ/burn mã (dùng cho status check hoặc preview probe)


func (s *DB) VerifyOTPOnly(fileID, userID, otpCode string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	otpCode = strings.TrimSpace(otpCode)
	if otpCode == "" {
		return false, fmt.Errorf("Mã OTP không được để trống")
	}

	var otpID string
	var rawExpires interface{}
	var isUsed int

	row := s.db.QueryRow(`SELECT id, expires_at, is_used FROM file_access_otps 
		WHERE file_id = ? AND otp_code = ? AND (target_user_id = 'all' OR target_user_id = ?) 
		ORDER BY created_at DESC LIMIT 1`, fileID, otpCode, userID)

	if err := row.Scan(&otpID, &rawExpires, &isUsed); err != nil {
		return false, fmt.Errorf("Mã OTP không chính xác hoặc không áp dụng cho tệp tin này")
	}

	expiresAt := parseFlexibleTime(rawExpires)

	if isUsed == 1 {
		return false, fmt.Errorf("Mã OTP này đã được sử dụng")
	}

	if time.Now().After(expiresAt) {
		return false, fmt.Errorf("Mã OTP đã hết thời hạn hiệu lực")
	}

	return true, nil
}



func (s *DB) ListFileOTPs(limit int) ([]models.FileAccessOTP, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`SELECT o.id, o.file_id, o.file_name, o.target_user_id, COALESCE(u.username, o.target_user_id), o.otp_code, o.created_by, o.is_used, COALESCE(o.used_by, ''), o.used_at, o.expires_at, o.created_at 
		FROM file_access_otps o 
		LEFT JOIN cloudpool_users u ON o.target_user_id = u.id 
		ORDER BY o.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.FileAccessOTP, 0)
	now := time.Now()
	for rows.Next() {
		var o models.FileAccessOTP
		var isUsedInt int
		var usedBy string
		var rawUsedAt, rawExpiresAt, rawCreatedAt interface{}
		var targetUsername string

		if err := rows.Scan(&o.ID, &o.FileID, &o.FileName, &o.TargetUserID, &targetUsername, &o.OTPCode, &o.CreatedBy, &isUsedInt, &usedBy, &rawUsedAt, &rawExpiresAt, &rawCreatedAt); err != nil {
			return nil, err
		}
		o.IsUsed = (isUsedInt == 1)
		o.UsedBy = usedBy
		o.TargetUsername = targetUsername
		o.UsedAt = parseFlexibleTimePtr(rawUsedAt)
		o.ExpiresAt = parseFlexibleTime(rawExpiresAt)
		o.CreatedAt = parseFlexibleTime(rawCreatedAt)

		if o.IsUsed {
			o.Status = "used"
		} else if now.After(o.ExpiresAt) {
			o.Status = "expired"
		} else {
			o.Status = "active"
		}

		list = append(list, o)
	}
	return list, nil
}



func (s *DB) RevokeFileOTP(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM file_access_otps WHERE id = ?", id)
	return err
}



func (s *DB) CreateFileAccessRequest(req *models.FileAccessRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.ID == "" {
		req.ID = "req_" + uuid.New().String()
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	if req.UpdatedAt.IsZero() {
		req.UpdatedAt = time.Now()
	}
	if req.Status == "" {
		req.Status = "pending"
	}

	_, err := s.db.Exec(`INSERT INTO file_access_requests (id, file_id, file_name, user_id, username, user_display_name, status, otp_code, created_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.FileID, req.FileName, req.UserID, req.Username, req.UserDisplayName, req.Status, req.OTPCode, req.CreatedAt, req.UpdatedAt)
	return err
}



func (s *DB) ListFileAccessRequests(status string) ([]models.FileAccessRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if status == "all" || status == "" {
		rows, err = s.db.Query(`SELECT id, file_id, file_name, user_id, username, user_display_name, status, COALESCE(otp_code, ''), created_at, updated_at FROM file_access_requests ORDER BY created_at DESC LIMIT 100`)
	} else {
		rows, err = s.db.Query(`SELECT id, file_id, file_name, user_id, username, user_display_name, status, COALESCE(otp_code, ''), created_at, updated_at FROM file_access_requests WHERE status = ? ORDER BY created_at DESC LIMIT 100`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]models.FileAccessRequest, 0)
	for rows.Next() {
		var r models.FileAccessRequest
		var rawCreated, rawUpdated interface{}
		if err := rows.Scan(&r.ID, &r.FileID, &r.FileName, &r.UserID, &r.Username, &r.UserDisplayName, &r.Status, &r.OTPCode, &rawCreated, &rawUpdated); err != nil {
			return nil, err
		}
		r.CreatedAt = parseFlexibleTime(rawCreated)
		r.UpdatedAt = parseFlexibleTime(rawUpdated)
		list = append(list, r)
	}
	return list, nil
}



func (s *DB) ApproveFileAccessRequest(requestID, otpCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE file_access_requests SET status = 'approved', otp_code = ?, updated_at = ? WHERE id = ?`, otpCode, time.Now(), requestID)
	return err
}



func (s *DB) RejectFileAccessRequest(requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE file_access_requests SET status = 'rejected', updated_at = ? WHERE id = ?`, time.Now(), requestID)
	return err
}

// -------------------------------------------------------------
// Deduplication & Reference Counting
// -------------------------------------------------------------

// FindChunkByHash finds an existing uploaded chunk by SHA-256 for instant deduplication
