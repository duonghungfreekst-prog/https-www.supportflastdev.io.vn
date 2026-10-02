package storage

import (
	"database/sql"
	"time"
	"supportflast_engine/cloudpool/models"
)


func (s *DB) SaveChunks(chunks []models.FileChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var insertQuery string
	if s.IsMySQLOrTiDB() {
		insertQuery = `INSERT INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			gdrive_file_id=VALUES(gdrive_file_id),
			chunk_size_bytes=VALUES(chunk_size_bytes),
			encrypted_size_bytes=VALUES(encrypted_size_bytes),
			sha256=VALUES(sha256),
			status=VALUES(status)`
	} else {
		insertQuery = `INSERT INTO file_chunks (chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
			gdrive_file_id=excluded.gdrive_file_id,
			chunk_size_bytes=excluded.chunk_size_bytes,
			encrypted_size_bytes=excluded.encrypted_size_bytes,
			sha256=excluded.sha256,
			status=excluded.status`
	}

	stmt, err := tx.Prepare(insertQuery)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ChunkID, c.FileID, c.ChunkIndex, c.AccountID, c.GDriveFileID, c.ChunkSizeBytes, c.EncryptedSizeBytes, c.SHA256, c.Status); err != nil {
			return err
		}
	}

	err = tx.Commit()
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}



func (s *DB) GetChunksForFile(fileID string) ([]models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status FROM file_chunks WHERE file_id = ? ORDER BY chunk_index ASC`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chunks := make([]models.FileChunk, 0)
	for rows.Next() {
		var c models.FileChunk
		if err := rows.Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.Status); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}



func (s *DB) GetChunkByDriveID(driveFileID string) (*models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var c models.FileChunk
	err := s.db.QueryRow(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, status FROM file_chunks WHERE gdrive_file_id = ? LIMIT 1`, driveFileID).Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.Status)
	if err != nil {
		return nil, err
	}
	return &c, nil
}



func (s *DB) GetChunkDetailsForFile(fileID string) ([]models.ChunkDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT c.chunk_id, c.file_id, c.chunk_index, c.account_id, COALESCE(a.email, 'Unknown'), COALESCE(a.name, 'Google Drive'), c.gdrive_file_id, c.chunk_size_bytes, c.encrypted_size_bytes, c.sha256, c.status 
		FROM file_chunks c 
		LEFT JOIN accounts a ON c.account_id = a.id 
		WHERE c.file_id = ? 
		ORDER BY c.chunk_index ASC`

	rows, err := s.db.Query(query, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	details := make([]models.ChunkDetail, 0)
	for rows.Next() {
		var d models.ChunkDetail
		if err := rows.Scan(&d.ChunkID, &d.FileID, &d.ChunkIndex, &d.AccountID, &d.AccountEmail, &d.AccountName, &d.GDriveFileID, &d.ChunkSizeBytes, &d.EncryptedSizeBytes, &d.SHA256, &d.Status); err != nil {
			return nil, err
		}
		details = append(details, d)
	}
	return details, nil
}



func (s *DB) DeleteChunksForFile(fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM file_chunks WHERE file_id = ?", fileID)
	if err == nil {
		s.InvalidateStatsCache()
	}
	return err
}

// FindFileByNameInParent tìm file (không phải folder) có cùng tên trong cùng thư mục cha của đúng người dùng đó.
// Dùng để kiểm tra trùng tên trước khi upload → auto-replace cho riêng từng user (Chống Cross-Tenant Overwrite).


func (s *DB) FindChunkByHash(hash string) (*models.FileChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if hash == "" {
		return nil, nil
	}

	var c models.FileChunk
	err := s.db.QueryRow(`SELECT chunk_id, file_id, chunk_index, account_id, gdrive_file_id, chunk_size_bytes, encrypted_size_bytes, sha256, COALESCE(ref_count, 1), status 
		FROM file_chunks 
		WHERE sha256 = ? AND status = 'uploaded' 
		LIMIT 1`, hash).
		Scan(&c.ChunkID, &c.FileID, &c.ChunkIndex, &c.AccountID, &c.GDriveFileID, &c.ChunkSizeBytes, &c.EncryptedSizeBytes, &c.SHA256, &c.RefCount, &c.Status)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CountChunkReferences returns how many file chunks reference the same Google Drive file ID


func (s *DB) CountChunkReferences(gdriveFileID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM file_chunks WHERE gdrive_file_id = ?`, gdriveFileID).Scan(&count)
	return count, err
}

// -------------------------------------------------------------
// Public Share Links
// -------------------------------------------------------------



func (s *DB) UpdateChunkStatus(chunkID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE file_chunks SET status = ? WHERE chunk_id = ?", status, chunkID)
	return err
}

// UpdateChunkStatusByDriveID cập nhật trạng thái của chunk theo gdrive_file_id


func (s *DB) UpdateChunkStatusByDriveID(gdriveFileID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE file_chunks SET status = ? WHERE gdrive_file_id = ?", status, gdriveFileID)
	return err
}

// GetAllFilesForIntegrityCheck lấy danh sách các tệp (không phải thư mục) chưa bị xóa để kiểm tra toàn vẹn


func (s *DB) UpdateFileMissingChunks(fileID string, hasMissing bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("UPDATE virtual_files SET has_missing_chunks = ?, updated_at = ? WHERE id = ?", hasMissing, time.Now(), fileID)
	if err == nil {
		s.InvalidateVFSCache()
	}
	return err
}

// UpdateChunkStatus cập nhật trạng thái của chunk ("uploaded", "missing", "failed")


func (s *DB) GetAllFilesForIntegrityCheck(filterFileID string) ([]models.VirtualFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, user_id, parent_id, name, path, is_dir, size_bytes, mime_type, sha256, chunk_count, is_encrypted, is_deleted, deleted_at, has_missing_chunks, created_at, updated_at 
		FROM virtual_files 
		WHERE is_dir = 0 AND id != 'root' AND is_deleted = 0`
	var rows *sql.Rows
	var err error

	if filterFileID != "" {
		query += " AND id = ?"
		rows, err = s.db.Query(query, filterFileID)
	} else {
		query += " ORDER BY updated_at DESC"
		rows, err = s.db.Query(query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []models.VirtualFile
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
		files = append(files, f)
	}
	return files, nil
}


