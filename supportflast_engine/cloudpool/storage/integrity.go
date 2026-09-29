package storage

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// DriveChunkChecker định nghĩa giao diện kiểm tra tính tồn tại của file chunk trên bộ lưu trữ đám mây (Google Drive)
type DriveChunkChecker interface {
	CheckChunkExists(ctx context.Context, accountID, gdriveFileID string) (bool, error)
}

// IntegrityCheckOptions tùy chọn cho quá trình quét chẩn đoán toàn vẹn
type IntegrityCheckOptions struct {
	FileID      string `json:"file_id,omitempty"`      // Chỉ định 1 file cụ thể cần kiểm tra (để trống = quét toàn bộ hệ thống)
	AccountID   string `json:"account_id,omitempty"`   // Lọc theo 1 tài khoản Drive cụ thể
	Concurrency int    `json:"concurrency,omitempty"`  // Số luồng kiểm tra đồng thời (mặc định 5, tối đa 20)
	Force       bool   `json:"force,omitempty"`        // Bắt buộc quét lại ngay cả khi chunk đã được đánh dấu
}

// ChunkIntegrityIssue chi tiết về chunk bị lỗi hoặc biến mất
type ChunkIntegrityIssue struct {
	ChunkID      string `json:"chunk_id"`
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	ChunkIndex   int    `json:"chunk_index"`
	AccountID    string `json:"account_id"`
	GDriveFileID string `json:"gdrive_file_id"`
	Status       string `json:"status"` // "missing", "error"
	ErrorMessage string `json:"error_message,omitempty"`
}

// FileIntegrityReport báo cáo tính toàn vẹn của một tệp ảo
type FileIntegrityReport struct {
	FileID           string                `json:"file_id"`
	FileName         string                `json:"file_name"`
	Path             string                `json:"path"`
	TotalChunks      int                   `json:"total_chunks"`
	HealthyChunks    int                   `json:"healthy_chunks"`
	MissingChunks    int                   `json:"missing_chunks"`
	HasMissingChunks bool                  `json:"has_missing_chunks"`
	Issues           []ChunkIntegrityIssue `json:"issues,omitempty"`
}

// IntegrityReport báo cáo tổng kết toàn diện quá trình chẩn đoán tính toàn vẹn
type IntegrityReport struct {
	TotalFilesScanned   int                   `json:"total_files_scanned"`
	HealthyFilesCount   int                   `json:"healthy_files_count"`
	CorruptedFilesCount int                   `json:"corrupted_files_count"`
	TotalChunksScanned  int                   `json:"total_chunks_scanned"`
	HealthyChunksCount  int                   `json:"healthy_chunks_count"`
	MissingChunksCount  int                   `json:"missing_chunks_count"`
	ErrorCount          int                   `json:"error_count"`
	DurationMs          int64                 `json:"duration_ms"`
	CorruptedFiles      []FileIntegrityReport `json:"corrupted_files,omitempty"`
	CheckedAt           time.Time             `json:"checked_at"`
}

// IntegrityService cung cấp công cụ chẩn đoán và khắc phục tính toàn vẹn dữ liệu Chunks
type IntegrityService struct {
	db      *DB
	checker DriveChunkChecker
}

// NewIntegrityService khởi tạo dịch vụ kiểm tra tính toàn vẹn Chunks
func NewIntegrityService(db *DB, checker DriveChunkChecker) *IntegrityService {
	return &IntegrityService{
		db:      db,
		checker: checker,
	}
}

// RunCheck thực thi quy trình quét chẩn đoán và đồng bộ trạng thái missing cho Chunks và VirtualFile
func (s *IntegrityService) RunCheck(ctx context.Context, opts IntegrityCheckOptions) (*IntegrityReport, error) {
	startTime := time.Now()

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}
	if concurrency > 20 {
		concurrency = 20
	}

	report := &IntegrityReport{
		CorruptedFiles: make([]FileIntegrityReport, 0),
		CheckedAt:      startTime,
	}

	// 1. Lấy danh sách các VirtualFile cần quét từ cơ sở dữ liệu cloudpool_metadata.db
	files, err := s.db.GetAllFilesForIntegrityCheck(opts.FileID)
	if err != nil {
		return nil, fmt.Errorf("lỗi đọc danh sách tệp từ cơ sở dữ liệu: %w", err)
	}

	// Cache lưu kết quả kiểm tra từng gdrive_file_id (tránh gọi API lặp lại khi các file chia sẻ chunk deduplication)
	chunkCache := make(map[string]bool)
	var cacheMu sync.RWMutex

	sem := make(chan struct{}, concurrency)

	for _, file := range files {
		select {
		case <-ctx.Done():
			report.DurationMs = time.Since(startTime).Milliseconds()
			return report, ctx.Err()
		default:
		}

		chunks, err := s.db.GetChunksForFile(file.ID)
		if err != nil {
			log.Printf("[ENGINE] [INTEGRITY] Lỗi đọc chunks cho file '%s' (ID: %s): %v", file.Name, file.ID, err)
			report.ErrorCount++
			continue
		}

		// Nếu có tùy chọn lọc theo AccountID và tệp không chứa chunk nào thuộc tài khoản đó thì bỏ qua
		if opts.AccountID != "" {
			hasAccount := false
			for _, c := range chunks {
				if c.AccountID == opts.AccountID {
					hasAccount = true
					break
				}
			}
			if !hasAccount {
				continue
			}
		}

		report.TotalFilesScanned++
		fileReport := FileIntegrityReport{
			FileID:           file.ID,
			FileName:         file.Name,
			Path:             file.Path,
			TotalChunks:      len(chunks),
			Issues:           make([]ChunkIntegrityIssue, 0),
			HasMissingChunks: false,
		}

		// Nếu tệp có dung lượng > 0 nhưng không có bản ghi chunk nào trong DB
		if file.SizeBytes > 0 && len(chunks) == 0 {
			fileReport.HasMissingChunks = true
			fileReport.MissingChunks = 1
			report.MissingChunksCount++
			report.CorruptedFilesCount++
			_ = s.db.UpdateFileMissingChunks(file.ID, true)
			fileReport.Issues = append(fileReport.Issues, ChunkIntegrityIssue{
				ChunkID:      "",
				FileID:       file.ID,
				FileName:     file.Name,
				Status:       "missing",
				ErrorMessage: "Tệp tin không tìm thấy bất kỳ bản ghi chunk nào trong cơ sở dữ liệu",
			})
			report.CorruptedFiles = append(report.CorruptedFiles, fileReport)
			continue
		}

		fileMissingCount := 0
		fileHealthyCount := 0

		for _, chunk := range chunks {
			report.TotalChunksScanned++

			if chunk.GDriveFileID == "" {
				fileMissingCount++
				report.MissingChunksCount++
				_ = s.db.UpdateChunkStatus(chunk.ChunkID, "missing")
				fileReport.Issues = append(fileReport.Issues, ChunkIntegrityIssue{
					ChunkID:      chunk.ChunkID,
					FileID:       file.ID,
					FileName:     file.Name,
					ChunkIndex:   chunk.ChunkIndex,
					AccountID:    chunk.AccountID,
					GDriveFileID: "",
					Status:       "missing",
					ErrorMessage: "Chunk không có gdrive_file_id",
				})
				continue
			}

			// Nếu không có drive checker (chỉ kiểm tra DB cục bộ)
			if s.checker == nil {
				if chunk.Status == "missing" {
					fileMissingCount++
					report.MissingChunksCount++
					fileReport.Issues = append(fileReport.Issues, ChunkIntegrityIssue{
						ChunkID:      chunk.ChunkID,
						FileID:       file.ID,
						FileName:     file.Name,
						ChunkIndex:   chunk.ChunkIndex,
						AccountID:    chunk.AccountID,
						GDriveFileID: chunk.GDriveFileID,
						Status:       "missing",
						ErrorMessage: "Chunk được đánh dấu missing trong DB",
					})
				} else {
					fileHealthyCount++
					report.HealthyChunksCount++
				}
				continue
			}

			// Kiểm tra qua cache trước
			cacheKey := chunk.AccountID + "_" + chunk.GDriveFileID
			cacheMu.RLock()
			cachedExists, found := chunkCache[cacheKey]
			cacheMu.RUnlock()

			var exists bool
			if found && !opts.Force {
				exists = cachedExists
			} else {
				// Giới hạn luồng gọi đồng thời lên Google Drive
				sem <- struct{}{}
				var chkErr error
				exists, chkErr = s.checker.CheckChunkExists(ctx, chunk.AccountID, chunk.GDriveFileID)
				<-sem

				if chkErr != nil {
					log.Printf("[ENGINE] [INTEGRITY] [WARN] Lỗi kiểm tra chunk %s trên Google Drive (%s): %v", chunk.GDriveFileID, chunk.AccountID, chkErr)
					report.ErrorCount++
					// Nếu gặp lỗi mạng/auth (khác 404), giữ nguyên trạng thái hiện tại để không gây false positive
					if chunk.Status == "missing" {
						fileMissingCount++
						report.MissingChunksCount++
					} else {
						fileHealthyCount++
						report.HealthyChunksCount++
					}
					continue
				}

				cacheMu.Lock()
				chunkCache[cacheKey] = exists
				cacheMu.Unlock()
			}

			if !exists {
				// Chunk bị 404 trên Drive (đã bị xóa/di chuyển ra khỏi drive)
				fileMissingCount++
				report.MissingChunksCount++
				_ = s.db.UpdateChunkStatus(chunk.ChunkID, "missing")

				fileReport.Issues = append(fileReport.Issues, ChunkIntegrityIssue{
					ChunkID:      chunk.ChunkID,
					FileID:       file.ID,
					FileName:     file.Name,
					ChunkIndex:   chunk.ChunkIndex,
					AccountID:    chunk.AccountID,
					GDriveFileID: chunk.GDriveFileID,
					Status:       "missing",
					ErrorMessage: "Chunk không tồn tại trên Google Drive (404/NotFound hoặc đã vào thùng rác)",
				})
			} else {
				fileHealthyCount++
				report.HealthyChunksCount++
				// Nếu chunk trước đó từng bị đánh dấu missing nhưng nay đã được phục hồi
				if chunk.Status == "missing" {
					_ = s.db.UpdateChunkStatus(chunk.ChunkID, "uploaded")
				}
			}
		}

		fileReport.HealthyChunks = fileHealthyCount
		fileReport.MissingChunks = fileMissingCount

		if fileMissingCount > 0 {
			fileReport.HasMissingChunks = true
			report.CorruptedFilesCount++
			// Đánh dấu VirtualFile có cờ cảnh báo has_missing_chunks = true
			_ = s.db.UpdateFileMissingChunks(file.ID, true)
			report.CorruptedFiles = append(report.CorruptedFiles, fileReport)
			log.Printf("[ENGINE] [INTEGRITY] [ALERT] Tệp '%s' (ID: %s) phát hiện %d/%d chunks bị thiếu dữ liệu nguồn!", file.Name, file.ID, fileMissingCount, len(chunks))
		} else {
			fileReport.HasMissingChunks = false
			report.HealthyFilesCount++
			// Nếu tệp này trước đó có cờ has_missing_chunks nhưng nay tất cả chunks đã đủ, xóa cờ cảnh báo
			if file.HasMissingChunks {
				_ = s.db.UpdateFileMissingChunks(file.ID, false)
				log.Printf("[ENGINE] [INTEGRITY] Tệp '%s' (ID: %s) đã phục hồi tính toàn vẹn 100%% chunks!", file.Name, file.ID)
			}
		}
	}

	report.DurationMs = time.Since(startTime).Milliseconds()
	log.Printf("[ENGINE] [INTEGRITY] Hoàn tất chẩn đoán: Quét %d tệp (%d lành lặn, %d thiếu dữ liệu), %d chunks (%d còn nguyên, %d bị 404) trong %dms",
		report.TotalFilesScanned, report.HealthyFilesCount, report.CorruptedFilesCount,
		report.TotalChunksScanned, report.HealthyChunksCount, report.MissingChunksCount,
		report.DurationMs,
	)

	return report, nil
}

// CheckFile kiểm tra tính toàn vẹn của một tệp cụ thể
func (s *IntegrityService) CheckFile(ctx context.Context, fileID string) (*FileIntegrityReport, error) {
	report, err := s.RunCheck(ctx, IntegrityCheckOptions{FileID: fileID, Force: true})
	if err != nil {
		return nil, err
	}
	if len(report.CorruptedFiles) > 0 {
		return &report.CorruptedFiles[0], nil
	}
	// Tệp lành lặn
	file, err := s.db.GetVirtualFile(fileID)
	if err != nil {
		return nil, err
	}
	chunks, _ := s.db.GetChunksForFile(fileID)
	return &FileIntegrityReport{
		FileID:           file.ID,
		FileName:         file.Name,
		Path:             file.Path,
		TotalChunks:      len(chunks),
		HealthyChunks:    len(chunks),
		MissingChunks:    0,
		HasMissingChunks: false,
	}, nil
}
