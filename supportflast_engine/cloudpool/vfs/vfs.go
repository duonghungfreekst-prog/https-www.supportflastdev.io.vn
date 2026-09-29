package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"path"
	"path/filepath"
	"sort"
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
	mu            sync.Mutex
}

func NewVFS(db *storage.DB, gd *gdrive.Manager) *VFS {
	return &VFS{
		db:            db,
		gd:            gd,
		inFlightQuota: make(map[string]int64),
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

// SelectAccountForChunk chooses a Google Drive account based on the configured allocation strategy,
// accounting for in-flight reserved quota and optional excluded account IDs (e.g. on failover).
func (v *VFS) SelectAccountForChunk(strategy string, requiredBytes int64, excludeAccountIDs ...string) (*models.Account, error) {
	accounts, err := v.db.ListAccounts()
	if err != nil {
		return nil, err
	}

	excludeMap := make(map[string]bool)
	for _, id := range excludeAccountIDs {
		if id != "" {
			excludeMap[id] = true
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	var activeAccounts []models.Account
	for _, a := range accounts {
		if a.Status != "active" || excludeMap[a.ID] {
			continue
		}

		// TÃ­nh dung lÆ°á»£ng áº£o kháº£ dá»¥ng: trá»« Ä‘i dung lÆ°á»£ng in-flight Ä‘ang chá» upload
		inflight := int64(0)
		if v.inFlightQuota != nil {
			inflight = v.inFlightQuota[a.ID]
		}
		effectiveFree := a.FreeQuotaBytes - inflight
		if effectiveFree >= requiredBytes {
			accCopy := a
			accCopy.FreeQuotaBytes = effectiveFree
			activeAccounts = append(activeAccounts, accCopy)
		}
	}

	if len(activeAccounts) == 0 {
		return nil, errors.New("khÃ´ng cÃ³ tÃ i khoáº£n Google Drive nÃ o cÃ²n Ä‘á»§ dung lÆ°á»£ng kháº£ dá»¥ng")
	}

	switch strategy {
	case "least_used":
		// Sáº¯p xáº¿p giáº£m dáº§n theo dung lÆ°á»£ng áº£o cÃ²n trá»‘ng (nhiá»u chá»— trá»‘ng nháº¥t lÃªn Ä‘áº§u)
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})

		// Há»— trá»£ phÃ¢n bá»• luÃ¢n phiÃªn / phÃ¢n tÃ¡n Ä‘á»u chunk Ä‘a á»• Ä‘Ä©a:
		// Náº¿u cÃ³ nhiá»u tÃ i khoáº£n dá»“i dÃ o dung lÆ°á»£ng kháº£ dá»¥ng, luÃ¢n phiÃªn phÃ¢n bá»• giá»¯a cÃ¡c tÃ i khoáº£n Ä‘Ã³
		// Ä‘á»ƒ cÃ¡c chunk liÃªn tiáº¿p cá»§a má»™t tá»‡p lá»›n Ä‘Æ°á»£c phÃ¢n tÃ¡n Ä‘á»u qua nhiá»u tÃ i khoáº£n Google Drive khÃ¡c nhau.
		if len(activeAccounts) > 1 {
			maxFree := activeAccounts[0].FreeQuotaBytes
			var candidateCount int
			for _, a := range activeAccounts {
				if a.FreeQuotaBytes >= requiredBytes*5 && a.FreeQuotaBytes >= maxFree/5 {
					candidateCount++
				} else {
					break
				}
			}
			if candidateCount > 1 {
				idx := v.rrIndex % candidateCount
				v.rrIndex = (v.rrIndex + 1) % candidateCount
				chosen := activeAccounts[idx]
				return &chosen, nil
			}
		}
		chosen := activeAccounts[0]
		return &chosen, nil

	case "waterfill":
		// Chá»n tÃ i khoáº£n Ä‘áº§u tiÃªn cÃ²n Ä‘á»§ dung lÆ°á»£ng áº£o kháº£ dá»¥ng
		chosen := activeAccounts[0]
		return &chosen, nil

	case "round_robin":
		if v.rrIndex >= len(activeAccounts) {
			v.rrIndex = 0
		}
		chosen := activeAccounts[v.rrIndex]
		v.rrIndex = (v.rrIndex + 1) % len(activeAccounts)
		return &chosen, nil

	default:
		// Máº·c Ä‘á»‹nh: least_used vá»›i há»— trá»£ luÃ¢n phiÃªn phÃ¢n tÃ¡n Ä‘á»u chunk
		sort.Slice(activeAccounts, func(i, j int) bool {
			return activeAccounts[i].FreeQuotaBytes > activeAccounts[j].FreeQuotaBytes
		})
		if len(activeAccounts) > 1 {
			maxFree := activeAccounts[0].FreeQuotaBytes
			var candidateCount int
			for _, a := range activeAccounts {
				if a.FreeQuotaBytes >= requiredBytes*5 && a.FreeQuotaBytes >= maxFree/5 {
					candidateCount++
				} else {
					break
				}
			}
			if candidateCount > 1 {
				idx := v.rrIndex % candidateCount
				v.rrIndex = (v.rrIndex + 1) % candidateCount
				chosen := activeAccounts[idx]
				return &chosen, nil
			}
		}
		chosen := activeAccounts[0]
		return &chosen, nil
	}
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
	ext := filepath.Ext(fileName)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	fileID := "file_" + uuid.New().String()
	var totalSize int64
	var chunkIndex int

	type uploadJob struct {
		chunkIndex     int
		plainSize      int64
		encLen         int64
		encData        []byte
		chunkHash      string
		accountID      string
		gfileID        string
		isDeduplicated bool
		err            error
	}

	// Parallel upload with bounded worker pool (Rule PHAN 7.1)
	numWorkers := 4
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
	
	// Khá»Ÿi Ä‘á»™ng cÃ¡c worker trÆ°á»›c
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				errMu.Lock()
				if uploadErr != nil {
					errMu.Unlock()
					continue // Drain channel but do nothing
				}
				errMu.Unlock()

				if job.isDeduplicated {
					continue
				}

				chunkRemoteName := fmt.Sprintf("chunk_%s_%d.enc", fileID, job.chunkIndex)
				gfileID, err := v.gd.UploadChunk(ctx, job.accountID, chunkRemoteName, job.encData)
				if err != nil {
					// Failover: Thá»­ cÃ¡c tÃ i khoáº£n kháº£ dá»¥ng khÃ¡c, loáº¡i trá»« tÃ i khoáº£n vá»«a bá»‹ há»ng (job.accountID)
					failedAccIDs := []string{job.accountID}
					for retry := 0; retry < 2; retry++ {
						altAcc, altErr := v.SelectAccountForChunk("least_used", job.encLen, failedAccIDs...)
						if altErr != nil || altAcc.ID == job.accountID {
							break
						}
						// Chuyá»ƒn in-flight reservation sang tÃ i khoáº£n thay tháº¿
						oldAccID := job.accountID
						v.ReleaseInFlightQuota(oldAccID, job.encLen)
						v.ReserveInFlightQuota(altAcc.ID, job.encLen)
						job.accountID = altAcc.ID
						failedAccIDs = append(failedAccIDs, altAcc.ID)

						gfileID, err = v.gd.UploadChunk(ctx, altAcc.ID, chunkRemoteName, job.encData)
						if err == nil {
							break
						}
					}
				}

				job.encData = nil // Free memory immediately

				if err != nil {
					errMu.Lock()
					if uploadErr == nil {
						uploadErr = fmt.Errorf("lá»—i táº£i chunk %d lÃªn Google Drive: %w", job.chunkIndex, err)
					}
					errMu.Unlock()
				} else {
					job.gfileID = gfileID
					job.err = nil
					// Track chunk đã upload thành công để cleanup nếu upload fail
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

	// Read and prepare encrypted chunks, pushing to jobChan (Blocks if RAM limit reached)
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

			encData, encErr := core.EncryptChunk(encKey, chunkData)
			if encErr != nil {
				errMu.Lock()
				uploadErr = fmt.Errorf("lá»—i mÃ£ hÃ³a AES chunk %d: %w", chunkIndex, encErr)
				errMu.Unlock()
				break
			}

			chunkHash := core.HashSHA256(chunkData)
			existingChunk, _ := v.db.FindChunkByHash(chunkHash)
			
			job := &uploadJob{
				chunkIndex: chunkIndex,
				plainSize:  int64(n),
				encLen:     int64(len(encData)),
				chunkHash:  chunkHash,
			}

			if existingChunk != nil && existingChunk.GDriveFileID != "" {
				job.isDeduplicated = true
				job.accountID = existingChunk.AccountID
				job.gfileID = existingChunk.GDriveFileID
				job.encData = nil
			} else {
				acc, selErr := v.SelectAccountForChunk(settings.AllocationStrategy, int64(len(encData)))
				if selErr != nil {
					errMu.Lock()
					uploadErr = selErr
					errMu.Unlock()
					break
				}
				job.isDeduplicated = false
				job.accountID = acc.ID
				job.encData = encData
				v.ReserveInFlightQuota(acc.ID, int64(len(encData)))
			}

			jobs = append(jobs, job)
			jobChan <- job // Blocking here limits RAM usage to (numWorkers * 2 * chunkSize)

			chunkIndex++
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			errMu.Lock()
			uploadErr = fmt.Errorf("error reading file data: %w", readErr)
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
						log.Printf("[ENGINE] Warning: failed to delete orphan chunk %s: %v", c.FileID, err)
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
			if mimeType == "" {
				ext := filepath.Ext(f.Name)
				mimeType = mime.TypeByExtension(ext)
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
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

