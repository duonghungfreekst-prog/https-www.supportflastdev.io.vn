package vfs

import (
	"context"
	"fmt"
	"io"
	"log"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"

	"github.com/google/uuid"
)

type VFS struct {
	db            *storage.DB
	gd            *gdrive.Manager
	rrIndex       int
	inFlightQuota map[string]int64
	rateLimits    map[string]time.Time
	mu            sync.Mutex
}

func NewVFS(db *storage.DB, gd *gdrive.Manager) *VFS {
	return &VFS{
		db:            db,
		gd:            gd,
		inFlightQuota: make(map[string]int64),
		rateLimits:    make(map[string]time.Time),
	}
}

// ReserveInFlightQuota táº¡m giá»¯ dung lÆ°á»£ng áº£o cho má»™t tÃ i khoáº£n trong quÃ¡ trÃ¬nh upload chunk
func (v *VFS) ReserveInFlightQuota(accountID string, bytes int64) {
	if accountID == "" || bytes <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inFlightQuota == nil {
		v.inFlightQuota = make(map[string]int64)
	}
	v.inFlightQuota[accountID] += bytes
}

// ReleaseInFlightQuota giáº£i phÃ³ng dung lÆ°á»£ng áº£o Ä‘Ã£ táº¡m giá»¯ khi chunk hoÃ n táº¥t hoáº·c tháº¥t báº¡i
func (v *VFS) ReleaseInFlightQuota(accountID string, bytes int64) {
	if accountID == "" || bytes <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inFlightQuota == nil {
		return
	}
	v.inFlightQuota[accountID] -= bytes
	if v.inFlightQuota[accountID] <= 0 {
		delete(v.inFlightQuota, accountID)
	}
}

// GetInFlightQuota tráº£ vá» dung lÆ°á»£ng áº£o Ä‘ang táº¡m giá»¯ cá»§a má»™t tÃ i khoáº£n
func (v *VFS) GetInFlightQuota(accountID string) int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inFlightQuota == nil {
		return 0
	}
	return v.inFlightQuota[accountID]
}

// DownloadStream streams direct raw content from Google Drive for native Drive files with zero buffering delay
func (v *VFS) DownloadStream(ctx context.Context, accountID, gdriveFileID string) (io.ReadCloser, int64, error) {
	return v.gd.DownloadStream(ctx, accountID, gdriveFileID)
}

// UploadFile streams data, chunks it, encrypts it, and distributes it in parallel across Google Drive accounts
func (v *VFS) UploadFile(ctx context.Context, userID, parentID, fileName string, src io.Reader, sizeHint int64) (*models.VirtualFile, error) {
	if userID == "" {
		userID = "user_admin"
	}
	settings, err := v.db.GetSettings()
	if err != nil {
		return nil, err
	}

	chunkSize := settings.ChunkSizeBytes
	if chunkSize <= 0 {
		chunkSize = 20 * 1024 * 1024 // 20 MB
	}

	encKey := core.DeriveKey(settings.MasterPassphrase, nil)

	// Resolve parent folder
	parentPath := "/"
	if parentID != "" && parentID != "root" {
		pFolder, err := v.db.GetVirtualFile(parentID)
		if err != nil {
			return nil, fmt.Errorf("parent folder not found: %w", err)
		}
		parentPath = pFolder.Path
	}

	filePath := path.Clean(path.Join(parentPath, fileName))
	mimeType := models.ResolveMimeType(fileName)

	fileID := "file_" + uuid.New().String()
	var totalSize int64
	var chunkIndex int

	type uploadJob struct {
		chunkIndex     int
		plainData      []byte
		plainSize      int64
		encLen         int64
		chunkHash      string
		accountID      string
		gfileID        string
		isDeduplicated bool
		err            error
	}

	// Bounded worker pool tối ưu đa luồng theo CPU cores (Rule PHAN 7.1)
	numWorkers := runtime.NumCPU() * 2
	if numWorkers < 4 {
		numWorkers = 4
	}
	if numWorkers > 8 {
		numWorkers = 8
	}

	jobChan := make(chan *uploadJob, numWorkers*2)
	var wg sync.WaitGroup
	var uploadErr error
	var errMu sync.Mutex

	// Track chunks đã upload thành công để cleanup khi lỗi (Bug #5: orphan chunks)
	type uploadedChunkInfo struct {
		AccountID string
		FileID    string
	}
	var uploadedChunkIDs []uploadedChunkInfo
	var chunkIDsMu sync.Mutex

	// Khởi động các worker trong pool: xử lý song song SHA256, AES Encrypt và Upload
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				errMu.Lock()
				if uploadErr != nil {
					errMu.Unlock()
					job.plainData = nil
					continue // Drain channel nhưng bỏ qua xử lý để dừng nhanh
				}
				errMu.Unlock()

				// 1. Tính toán SHA256 song song trên worker
				chunkHash := core.HashSHA256(job.plainData)
				job.chunkHash = chunkHash

				// 2. Kiểm tra Deduplication trong DB
				existingChunk, _ := v.db.FindChunkByHash(chunkHash)
				if existingChunk != nil && existingChunk.GDriveFileID != "" {
					job.isDeduplicated = true
					job.accountID = existingChunk.AccountID
					job.gfileID = existingChunk.GDriveFileID
					job.encLen = existingChunk.EncryptedSizeBytes
					job.plainData = nil // Giải phóng bộ nhớ RAM ngay
					continue
				}

				// 3. Mã hóa AES-256-GCM song song trên worker
				encData, encErr := core.EncryptChunk(encKey, job.plainData)
				job.plainData = nil // Giải phóng plainData ngay lập tức sau khi mã hóa để tiết kiệm RAM!
				if encErr != nil {
					errMu.Lock()
					if uploadErr == nil {
						uploadErr = fmt.Errorf("lỗi mã hóa AES chunk %d: %w", job.chunkIndex, encErr)
					}
					errMu.Unlock()
					continue
				}

				job.encLen = int64(len(encData))

				// 4. Lựa chọn tài khoản Google Drive: Tuyệt đối LOẠI TRỪ 3 tài khoản bị cấm
				acc, selErr := v.PickAccount(settings.AllocationStrategy, job.encLen)
				if selErr != nil {
					errMu.Lock()
					if uploadErr == nil {
						uploadErr = fmt.Errorf("lỗi chọn tài khoản Google Drive cho chunk %d: %w", job.chunkIndex, selErr)
					}
					errMu.Unlock()
					continue
				}

				job.accountID = acc.ID
				v.ReserveInFlightQuota(acc.ID, job.encLen)

				// 5. Upload lên Google Drive với cơ chế Tự động Retry & Rate-limit Handling & Failover
				chunkRemoteName := fmt.Sprintf("chunk_%s_%d.enc", fileID, job.chunkIndex)
				gfileID, err := v.gd.UploadChunk(ctx, job.accountID, chunkRemoteName, encData)

				if err != nil {
					failedAccIDs := []string{job.accountID}
					maxRetries := 3
					for retry := 1; retry <= maxRetries; retry++ {
						// Nếu gặp rate limit, đánh dấu cooldown tạm thời cho tài khoản này
						if IsRateLimitError(err) {
							v.MarkAccountRateLimited(job.accountID, 60*time.Second)
						}

						// Tìm tài khoản thay thế khả dụng (tự động né các tài khoản bị rate-limit và bị cấm)
						altAcc, altErr := v.PickAccount(settings.AllocationStrategy, job.encLen, failedAccIDs...)
						if altErr == nil && altAcc != nil && altAcc.ID != job.accountID {
							// Failover sang tài khoản thay thế
							oldAccID := job.accountID
							v.ReleaseInFlightQuota(oldAccID, job.encLen)
							v.ReserveInFlightQuota(altAcc.ID, job.encLen)
							job.accountID = altAcc.ID
							failedAccIDs = append(failedAccIDs, altAcc.ID)

							gfileID, err = v.gd.UploadChunk(ctx, altAcc.ID, chunkRemoteName, encData)
							if err == nil {
								break
							}
						} else {
							// Không còn tài khoản khác, thực hiện Exponential Backoff retry
							backoff := time.Duration(retry) * 1 * time.Second
							select {
							case <-ctx.Done():
								err = ctx.Err()
								break
							case <-time.After(backoff):
							}
							gfileID, err = v.gd.UploadChunk(ctx, job.accountID, chunkRemoteName, encData)
							if err == nil {
								break
							}
						}
					}
				}

				encData = nil // Giải phóng bộ nhớ RAM ngay lập tức sau khi upload

				if err != nil {
					errMu.Lock()
					if uploadErr == nil {
						uploadErr = fmt.Errorf("lỗi tải chunk %d lên Google Drive: %w", job.chunkIndex, err)
					}
					errMu.Unlock()
				} else {
					job.gfileID = gfileID
					job.err = nil
					// Ghi nhận chunk đã upload thành công để cleanup nếu phiên upload thất bại
					chunkIDsMu.Lock()
					uploadedChunkIDs = append(uploadedChunkIDs, uploadedChunkInfo{
						AccountID: job.accountID,
						FileID:    gfileID,
					})
					chunkIDsMu.Unlock()
				}
			}
		}()
	}

	buf := make([]byte, chunkSize)
	var jobs []*uploadJob

	// Producer: Đọc stream dữ liệu và đẩy vào jobChan (Blocking channel kiểm soát chặt chẽ dung lượng RAM)
	for {
		errMu.Lock()
		if uploadErr != nil {
			errMu.Unlock()
			break
		}
		errMu.Unlock()

		n, readErr := io.ReadFull(src, buf)
		if n > 0 {
			chunkData := make([]byte, n)
			copy(chunkData, buf[:n])
			totalSize += int64(n)

			job := &uploadJob{
				chunkIndex: chunkIndex,
				plainData:  chunkData,
				plainSize:  int64(n),
			}

			jobs = append(jobs, job)
			jobChan <- job // Blocking tại đây giới hạn lượng RAM tối đa an toàn (numWorkers * 2 * chunkSize)

			chunkIndex++
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			errMu.Lock()
			uploadErr = fmt.Errorf("lỗi đọc dữ liệu tệp: %w", readErr)
			errMu.Unlock()
			break
		}
	}
	close(jobChan)
	wg.Wait()

	releaseAllInFlight := func() {
		for _, job := range jobs {
			if !job.isDeduplicated && job.accountID != "" {
				v.ReleaseInFlightQuota(job.accountID, job.encLen)
			}
		}
	}

	if uploadErr != nil {
		releaseAllInFlight()
		// Cleanup orphan chunks đã upload lên Drive để tránh chiếm dung lượng vĩnh viễn
		if len(uploadedChunkIDs) > 0 {
			go func(chunks []uploadedChunkInfo) {
				for _, c := range chunks {
					if err := v.gd.DeleteChunk(context.Background(), c.AccountID, c.FileID); err != nil {
						log.Printf("[ENGINE] Cảnh báo: không thể xóa orphan chunk %s: %v", c.FileID, err)
					}
				}
			}(uploadedChunkIDs)
		}
		return nil, uploadErr
	}

	var chunks []models.FileChunk
	for _, job := range jobs {
		chunks = append(chunks, models.FileChunk{
			ChunkID:            "chk_" + uuid.New().String(),
			FileID:             fileID,
			ChunkIndex:         job.chunkIndex,
			AccountID:          job.accountID,
			GDriveFileID:       job.gfileID,
			ChunkSizeBytes:     job.plainSize,
			EncryptedSizeBytes: job.encLen,
			SHA256:             job.chunkHash,
			RefCount:           1,
			Status:             "uploaded",
		})
	}

	vFile := &models.VirtualFile{
		ID:          fileID,
		UserID:      userID,
		ParentID:    parentID,
		Name:        fileName,
		Path:        filePath,
		IsDir:       false,
		SizeBytes:   totalSize,
		MimeType:    mimeType,
		ChunkCount:  chunkIndex,
		IsEncrypted: true,
	}

	// Auto-replace: Náº¿u cÃ³ file trÃ¹ng tÃªn trong cÃ¹ng thÆ° má»¥c cha,
	// xÃ³a file cÅ© (chunks trÃªn Drive + báº£n ghi DB) trÆ°á»›c khi lÆ°u file má»›i.
	existingFile, findErr := v.db.FindFileByNameInParent(parentID, fileName)
	if findErr == nil && existingFile != nil {
		// Láº¥y danh sÃ¡ch chunk cÅ© Ä‘á»ƒ xÃ³a trÃªn Google Drive (kiá»ƒm tra ref count)
		oldChunks, chunkErr := v.db.GetChunksForFile(existingFile.ID)
		if chunkErr == nil {
			for _, oldChunk := range oldChunks {
				// Only delete on Drive if no other file is sharing this chunk
				refCount, _ := v.db.CountChunkReferences(oldChunk.GDriveFileID)
				if refCount <= 1 {
					_ = v.gd.DeleteChunk(ctx, oldChunk.AccountID, oldChunk.GDriveFileID)
					if acc, accErr := v.db.GetAccount(oldChunk.AccountID); accErr == nil {
						freed := oldChunk.EncryptedSizeBytes
						newUsed := acc.UsedQuotaBytes - freed
						if newUsed < 0 {
							newUsed = 0
						}
						_ = v.db.UpdateAccountQuota(
							acc.ID,
							acc.TotalQuotaBytes,
							newUsed,
							acc.FreeQuotaBytes+freed,
							acc.Status,
							"",
						)
					}
				}
			}
		}
		// XÃ³a báº£n ghi chunk cÅ© trong DB
		_ = v.db.DeleteChunksForFile(existingFile.ID)
		// HoÃ n tráº£ dung lÆ°á»£ng Ä‘Ã£ dÃ¹ng cá»§a user
		_ = v.db.UpdateUserUsage(existingFile.UserID, -existingFile.SizeBytes)
		// XÃ³a báº£n ghi virtual file cÅ©
		_ = v.db.DeleteVirtualFile(existingFile.ID)
	}

	if err := v.db.SaveVirtualFile(vFile); err != nil {
		// Fallback: Náº¿u váº«n bá»‹ lá»—i UNIQUE constraint (do frontend cÅ© gá»­i sai parent_id,
		// hoáº·c thao tÃ¡c song song bá»‹ Ä‘á»¥ng path), tÃ¬m vÃ  xÃ³a luÃ´n theo Path.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "2067") {
			if conflictFile, _ := v.db.GetVirtualFileByPath(vFile.Path); conflictFile != nil {
				_ = v.db.DeleteChunksForFile(conflictFile.ID)
				_ = v.db.UpdateUserUsage(conflictFile.UserID, -conflictFile.SizeBytes)
				_ = v.db.DeleteVirtualFile(conflictFile.ID)
				
				if retryErr := v.db.SaveVirtualFile(vFile); retryErr != nil {
					releaseAllInFlight()
					return nil, fmt.Errorf("failed to save virtual file metadata after auto-replace retry: %w", retryErr)
				}
			} else {
				releaseAllInFlight()
				return nil, fmt.Errorf("failed to save virtual file metadata: %w", err)
			}
		} else {
			releaseAllInFlight()
			return nil, fmt.Errorf("failed to save virtual file metadata: %w", err)
		}
	}
	if err := v.db.SaveChunks(chunks); err != nil {
		releaseAllInFlight()
		return nil, fmt.Errorf("failed to save chunk metadata: %w", err)
	}

	// Cáº­p nháº­t dung lÆ°á»£ng thá»±c táº¿ vÃ o CSDL cho cÃ¡c tÃ i khoáº£n nháº­n chunk má»›i vÃ  giáº£i phÃ³ng in-flight reservation
	accUsage := make(map[string]int64)
	for _, job := range jobs {
		if !job.isDeduplicated && job.accountID != "" && job.gfileID != "" && job.err == nil {
			accUsage[job.accountID] += job.encLen
		}
	}
	for accID, bytesAdded := range accUsage {
		if acc, accErr := v.db.GetAccount(accID); accErr == nil {
			newUsed := acc.UsedQuotaBytes + bytesAdded
			newFree := acc.FreeQuotaBytes - bytesAdded
			if newFree < 0 {
				newFree = 0
			}
			_ = v.db.UpdateAccountQuota(acc.ID, acc.TotalQuotaBytes, newUsed, newFree, acc.Status, "")
		}
		v.ReleaseInFlightQuota(accID, bytesAdded)
	}

	// Update user storage usage
	_ = v.db.UpdateUserUsage(userID, totalSize)

	return vFile, nil
}

// EnsureDirectoryPath ensures all folders in a path exist and returns the deepest folder ID
func (v *VFS) EnsureDirectoryPath(userID, parentID, relPath string) (string, error) {
	if relPath == "" || relPath == "." || relPath == "/" {
		if parentID == "" {
			return "root", nil
		}
		return parentID, nil
	}

	cleanPath := strings.ReplaceAll(relPath, "\\", "/")
	cleanPath = strings.Trim(cleanPath, "/")
	if cleanPath == "" {
		if parentID == "" {
			return "root", nil
		}
		return parentID, nil
	}

	segments := strings.Split(cleanPath, "/")
	curParentID := parentID
	if curParentID == "" {
		curParentID = "root"
	}

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." {
			continue
		}

		// Find existing subfolder
		files, err := v.db.ListVirtualFiles(userID, curParentID)
		found := false
		if err == nil {
			for _, f := range files {
				if f.IsDir && f.Name == seg {
					curParentID = f.ID
					found = true
					break
				}
			}
		}

		if !found {
			folder, err := v.Mkdir(userID, curParentID, seg)
			if err != nil {
				// Retry fetching by path
				parentFolder, pErr := v.db.GetVirtualFile(curParentID)
				pPath := "/"
				if pErr == nil && parentFolder != nil {
					pPath = parentFolder.Path
				}
				targetPath := path.Clean(path.Join(pPath, seg))
				if existing, getErr := v.db.GetVirtualFileByPath(targetPath); getErr == nil && existing != nil {
					curParentID = existing.ID
				} else {
					return "", fmt.Errorf("khÃ´ng thá»ƒ táº¡o thÆ° má»¥c '%s': %w", seg, err)
				}
			} else {
				curParentID = folder.ID
			}
		}
	}

	return curParentID, nil
}

// Mkdir creates a virtual folder
func (v *VFS) Mkdir(userID, parentID, folderName string) (*models.VirtualFile, error) {
	if userID == "" {
		userID = "user_admin"
	}
	parentPath := "/"
	if parentID != "" && parentID != "root" {
		pFolder, err := v.db.GetVirtualFile(parentID)
		if err != nil {
			return nil, fmt.Errorf("parent folder not found: %w", err)
		}
		parentPath = pFolder.Path
	}

	folderPath := path.Clean(path.Join(parentPath, folderName))

	// Check if already exists
	if _, err := v.db.GetVirtualFileByPath(folderPath); err == nil {
		return nil, fmt.Errorf("thÆ° má»¥c '%s' Ä‘Ã£ tá»“n táº¡i", folderName)
	}

	vFolder := &models.VirtualFile{
		ID:          "dir_" + uuid.New().String(),
		UserID:      userID,
		ParentID:    parentID,
		Name:        folderName,
		Path:        folderPath,
		IsDir:       true,
		SizeBytes:   0,
		MimeType:    "inode/directory",
		ChunkCount:  0,
		IsEncrypted: false,
	}

	if err := v.db.SaveVirtualFile(vFolder); err != nil {
		return nil, err
	}
	return vFolder, nil
}

// SoftDeleteFileOrFolder moves a file or directory to Trash
func (v *VFS) SoftDeleteFileOrFolder(ctx context.Context, id string) error {
	return v.db.SoftDeleteVirtualFile(id)
}

// RestoreFileOrFolder restores a file or directory from Trash
func (v *VFS) RestoreFileOrFolder(ctx context.Context, id string) error {
	return v.db.RestoreVirtualFile(id)
}

// PurgeFileOrFolderPermanently permanently destroys a file/directory and removes unshared Google Drive chunks
func (v *VFS) PurgeFileOrFolderPermanently(ctx context.Context, id string) error {
	vFile, err := v.db.GetVirtualFile(id)
	if err != nil {
		return err
	}

	if vFile.IsDir {
		children, err := v.db.ListVirtualFiles("", id)
		if err == nil {
			for _, child := range children {
				_ = v.PurgeFileOrFolderPermanently(ctx, child.ID)
			}
		}
		return v.db.DeleteVirtualFile(id)
	}

	chunks, err := v.db.GetChunksForFile(id)
	if err == nil {
		for _, c := range chunks {
			refCount, _ := v.db.CountChunkReferences(c.GDriveFileID)
			if refCount <= 1 {
				_ = v.gd.DeleteChunk(ctx, c.AccountID, c.GDriveFileID)
				if acc, err := v.db.GetAccount(c.AccountID); err == nil {
					_ = v.db.UpdateAccountQuota(
						acc.ID,
						acc.TotalQuotaBytes,
						acc.UsedQuotaBytes-c.EncryptedSizeBytes,
						acc.FreeQuotaBytes+c.EncryptedSizeBytes,
						acc.Status,
						"",
					)
				}
			}
		}
		_ = v.db.DeleteChunksForFile(id)
	}

	if vFile.UserID != "" && vFile.SizeBytes > 0 {
		_ = v.db.UpdateUserUsage(vFile.UserID, -vFile.SizeBytes)
	}

	return v.db.DeleteVirtualFile(id)
}

// EmptyTrash permanently purges all files in the trash
func (v *VFS) EmptyTrash(ctx context.Context, userID string) (int, error) {
	trashIDs, err := v.db.GetTrashFileIDs(userID)
	if err != nil {
		return 0, err
	}

	purgedCount := 0
	for _, id := range trashIDs {
		if err := v.PurgeFileOrFolderPermanently(ctx, id); err == nil {
			purgedCount++
		}
	}
	return purgedCount, nil
}

// DeleteFileOrFolder moves a file or folder to Recycle Bin (Soft Delete)
func (v *VFS) DeleteFileOrFolder(ctx context.Context, id string) error {
	return v.SoftDeleteFileOrFolder(ctx, id)
}


// RenameFileOrFolder renames a file or folder
func (v *VFS) RenameFileOrFolder(id, newName string) error {
	vFile, err := v.db.GetVirtualFile(id)
	if err != nil {
		return err
	}

	dir := path.Dir(vFile.Path)
	newPath := path.Clean(path.Join(dir, newName))

	return v.db.RenameVirtualFile(id, newName, newPath)
}

// ListDirectory returns contents of a folder for a given user
func (v *VFS) ListDirectory(userID, folderID string) ([]models.VirtualFile, error) {
	if folderID == "" {
		folderID = "root"
	}
	return v.db.ListVirtualFiles(userID, folderID)
}

// GetFileByPath resolves a path to a VirtualFile
func (v *VFS) GetFileByPath(filePath string) (*models.VirtualFile, error) {
	cleanPath := path.Clean("/" + strings.TrimPrefix(filePath, "/"))
	if cleanPath == "/" {
		return v.db.GetVirtualFile("root")
	}
	return v.db.GetVirtualFileByPath(cleanPath)
}

// ImportExistingDriveFiles imports pre-existing files from a specific or all Google Drive accounts into CloudPool VFS
func (v *VFS) ImportExistingDriveFiles(ctx context.Context, accountID string) (int, error) {
	var accounts []models.Account
	if accountID != "" {
		acc, err := v.db.GetAccount(accountID)
		if err != nil {
			return 0, err
		}
		accounts = append(accounts, *acc)
	} else {
		all, err := v.db.ListAccounts()
		if err != nil {
			return 0, err
		}
		for _, a := range all {
			if a.Status == "active" {
				accounts = append(accounts, a)
			}
		}
	}

	totalImported := 0

	for _, acc := range accounts {
		scanCtx, scanCancel := context.WithTimeout(ctx, 20*time.Second)
		files, err := v.gd.ScanExistingDriveFiles(scanCtx, acc.ID)
		scanCancel()
		if err != nil {
			continue
		}

		if len(files) == 0 {
			continue
		}

		// Ensure virtual folder for imported files: e.g. "[Google Drive] duonghungfreekst"
		accName := acc.Email
		if idx := strings.Index(accName, "@"); idx > 0 {
			accName = accName[:idx]
		}
		folderName := fmt.Sprintf("[Google Drive] %s", accName)
		parentFolder, err := v.Mkdir("user_admin", "root", folderName)
		parentID := "root"
		if err == nil && parentFolder != nil {
			parentID = parentFolder.ID
		} else if existingDir, err := v.db.GetVirtualFileByPath("/" + folderName); err == nil && existingDir != nil {
			parentID = existingDir.ID
		}

		for _, f := range files {
			// Check if already imported (by Drive file ID)
			existingChunk, _ := v.db.GetChunkByDriveID(f.Id)
			if existingChunk != nil {
				continue
			}

			fileID := "drive_" + f.Id
			if existingFile, _ := v.db.GetVirtualFile(fileID); existingFile != nil {
				continue
			}

			var modTime time.Time
			if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
				modTime = t
			} else {
				modTime = time.Now()
			}

			mimeType := f.MimeType
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = models.ResolveMimeType(f.Name)
			}

			filePath := path.Clean(path.Join("/"+folderName, f.Name))

			vFile := &models.VirtualFile{
				ID:          fileID,
				UserID:      "user_admin",
				ParentID:    parentID,
				Name:        f.Name,
				Path:        filePath,
				IsDir:       false,
				SizeBytes:   f.Size,
				MimeType:    mimeType,
				ChunkCount:  1,
				IsEncrypted: false,
				CreatedAt:   modTime,
				UpdatedAt:   modTime,
			}

			if err := v.db.SaveVirtualFile(vFile); err != nil {
				continue
			}

			chunk := models.FileChunk{
				ChunkID:            "chk_drive_" + f.Id,
				FileID:             fileID,
				ChunkIndex:         0,
				AccountID:          acc.ID,
				GDriveFileID:       f.Id,
				ChunkSizeBytes:     f.Size,
				EncryptedSizeBytes: 0,
				SHA256:             f.Md5Checksum,
				Status:             "imported",
			}

			if err := v.db.SaveChunks([]models.FileChunk{chunk}); err != nil {
				continue
			}

			totalImported++
		}
	}

	return totalImported, nil
}

