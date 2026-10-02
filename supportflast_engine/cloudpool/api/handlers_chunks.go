package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"github.com/google/uuid"
)


type ChunkedUploadSession struct {
	UploadID    string              `json:"upload_id"`
	FileName    string              `json:"file_name"`
	ParentID    string              `json:"parent_id"`
	UserID      string              `json:"user_id"`
	TotalSize   int64               `json:"total_size"`
	TotalChunks int                 `json:"total_chunks"`
	TempDir     string              `json:"temp_dir"` // Thư mục lưu các mảnh trên ổ đĩa: data/temp_chunks/{upload_id}
	Received    map[int]bool        `json:"received"` // Đánh dấu các index mảnh đã nhận thành công
	Status      string              `json:"status"`   // "uploading", "assembling", "completed", "error"
	ResultFile  *models.VirtualFile `json:"result_file,omitempty"` // Resulting VirtualFile when completed
	ErrorMsg    string              `json:"error_msg,omitempty"`   // Error details if failed
	mu          sync.Mutex          `json:"-"`
	CreatedAt   time.Time           `json:"created_at"`
}

// chunkedSessions stores active chunked upload sessions keyed by upload_id.


var chunkedSessions sync.Map



func (s *Server) getOrRestoreChunkedSession(uploadID string) (*ChunkedUploadSession, bool) {
	if uploadID == "" {
		return nil, false
	}
	if val, ok := chunkedSessions.Load(uploadID); ok {
		return val.(*ChunkedUploadSession), true
	}

	// Thử khôi phục từ tệp session.json trên ổ đĩa nếu server vừa được khởi động lại
	sessionFile := filepath.Join(s.baseDir, "data", "temp_chunks", uploadID, "session.json")
	sBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil, false
	}

	var session ChunkedUploadSession
	if err := json.Unmarshal(sBytes, &session); err != nil {
		return nil, false
	}
	if session.Received == nil {
		session.Received = make(map[int]bool)
	}

	// Đọc lại các mảnh chunk đã ghi trên ổ đĩa
	if entries, err := os.ReadDir(session.TempDir); err == nil {
		for _, entry := range entries {
			var idx int
			if n, _ := fmt.Sscanf(entry.Name(), "chunk_%d", &idx); n == 1 {
				session.Received[idx] = true
			}
		}
	}

	chunkedSessions.Store(uploadID, &session)
	log.Printf("[ENGINE] [RESTORE] Đã khôi phục thành công phiên tải lên từ đĩa: %s (file: %s, chunks: %d/%d, status: %s)",
		uploadID, session.FileName, len(session.Received), session.TotalChunks, session.Status)

	// Nếu phiên có trạng thái assembling nhưng server vừa khởi động lại, kiểm tra xem tệp đã hoàn tất trên VFS chưa
	if session.Status == "assembling" && s.db != nil {
		if existing, err := s.db.FindFileByNameInParent(session.UserID, session.ParentID, session.FileName); err == nil && existing != nil {
			session.Status = "completed"
			session.ResultFile = existing
			log.Printf("[ENGINE] [RESTORE] Tệp %s của phiên %s đã hoàn tất trên VFS, chuyển trạng thái sang completed", session.FileName, uploadID)
		}
	}

	return &session, true
}

// cleanupExpiredSessions removes chunked upload sessions older than 2 hours and deletes temp files.


func cleanupExpiredSessions(baseDir string) {
	chunkedSessions.Range(func(key, value interface{}) bool {
		session, ok := value.(*ChunkedUploadSession)
		if !ok {
			chunkedSessions.Delete(key)
			return true
		}
		if time.Since(session.CreatedAt) > 2*time.Hour {
			if session.TempDir != "" {
				_ = os.RemoveAll(session.TempDir)
			}
			chunkedSessions.Delete(key)
			log.Printf("[ENGINE] Cleaned up expired chunked upload session and temp dir: %s", key)
		} else if session.Status == "completed" && time.Since(session.CreatedAt) > 15*time.Minute {
			chunkedSessions.Delete(key)
		}
		return true
	})

	if baseDir != "" {
		tempRootDir := filepath.Join(baseDir, "temp_chunks")
		entries, err := os.ReadDir(tempRootDir)
		if err == nil {
			now := time.Now()
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				info, iErr := entry.Info()
				if iErr != nil {
					continue
				}
				if now.Sub(info.ModTime()) > 2*time.Hour {
					dirPath := filepath.Join(tempRootDir, entry.Name())
					if val, exists := chunkedSessions.Load(entry.Name()); exists {
						if sess, ok := val.(*ChunkedUploadSession); ok && sess.Status == "assembling" {
							continue
						}
					}
					if rErr := os.RemoveAll(dirPath); rErr != nil {
						log.Printf("[ENGINE] [CLEANUP] [WARN] Không thể xóa thư mục tạm mồ côi %s: %v", dirPath, rErr)
					} else {
						chunkedSessions.Delete(entry.Name())
						log.Printf("[ENGINE] [CLEANUP] Đã dọn dẹp thư mục tạm mồ côi: %s", dirPath)
					}
				}
			}
		}
	}
}



func StartSessionCleanupWorker(baseDir string) {
	cleanupWorkerOnce.Do(func() {
		go func() {
			cleanupExpiredSessions(baseDir)
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				cleanupExpiredSessions(baseDir)
			}
		}()
		log.Printf("[ENGINE] [CLEANUP] Worker dọn dẹp phiên tải lên và rác đĩa mồ côi đã được kích hoạt thành công")
	})
}



var cleanupWorkerOnce sync.Once

// StartSessionCleanupWorker khởi chạy background worker định kỳ quét dọn session và thư mục tạm mồ côi


func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	} else {
		if guestMode == "strict" {
			writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để tải lên tệp tin", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Chế độ Khách (Guest) chỉ cho phép xem và tải xuống. Vui lòng đăng nhập để tải lên.", nil)
			return
		}
	}

	// Support up to 500GB uploads via stream parsing
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart request", err)
		return
	}

	var parentID string
	var uploadedFile *models.VirtualFile

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Error reading multipart part", err)
			return
		}

		formName := part.FormName()
		if formName == "parent_id" {
			data, _ := io.ReadAll(part)
			parentID = string(data)
			continue
		}
		if formName == "rel_path" {
			data, _ := io.ReadAll(part)
			relPath := string(data)
			if relPath != "" {
				// Auto-create directory structure for folder uploads
				targetParentID, err := s.vfs.EnsureDirectoryPath(currentUserID, parentID, relPath)
				if err == nil && targetParentID != "" {
					parentID = targetParentID
				}
			}
			continue
		}

		if formName == "file" {
			if parentID == "" {
				parentID = "root"
				log.Printf("[ENGINE] Warning: parent_id not set before file part, defaulting to root")
			}

			fileName := part.FileName()
			if fileName == "" {
				fileName = fmt.Sprintf("upload_%d.bin", time.Now().Unix())
			}

			// If filename contains relative folder path (from webkitRelativePath), extract folder
			if strings.Contains(fileName, "/") || strings.Contains(fileName, "\\") {
				normPath := strings.ReplaceAll(fileName, "\\", "/")
				dirPart := path.Dir(normPath)
				baseName := path.Base(normPath)
				if dirPart != "." && dirPart != "/" && dirPart != "" {
					targetParentID, err := s.vfs.EnsureDirectoryPath(currentUserID, parentID, dirPart)
					if err == nil && targetParentID != "" {
						parentID = targetParentID
					}
					fileName = baseName
				}
			}

			// Kiểm tra trước xem có file trùng tên không (để báo với frontend)
			existingOld, _ := s.db.FindFileByNameInParent(currentUserID, parentID, fileName)
			wasReplaced := existingOld != nil

			// Kiểm tra hạn ngạch người dùng trước khi upload stream (P1: Quota Enforcement)
			if currentUserID != "user_admin" && user != nil && user.Role != "admin" {
				dbUser, uErr := s.db.GetUserByID(currentUserID)
				if uErr == nil && dbUser != nil && dbUser.QuotaBytes > 0 {
					remQuota := dbUser.QuotaBytes - dbUser.UsedBytes
					if existingOld != nil {
						remQuota += existingOld.SizeBytes
					}
					if remQuota <= 0 {
						writeError(w, http.StatusInsufficientStorage, "Hạn ngạch dung lượng tài khoản của bạn đã đầy. Vui lòng giải phóng bớt dung lượng.", nil)
						return
					}
				}
			}

			vfile, err := s.vfs.UploadFile(r.Context(), currentUserID, parentID, fileName, part, -1)
			if err != nil {
				log.Printf("[ENGINE] Upload error for user %s: %v", currentUserID, err)
				errMsg := err.Error()
				switch {
				case strings.Contains(errMsg, "không có tài khoản Google Drive nào còn đủ dung lượng") || strings.Contains(errMsg, "storage") || strings.Contains(errMsg, "quota"):
					writeError(w, http.StatusInsufficientStorage, "Hết dung lượng lưu trữ. Vui lòng thêm tài khoản Google Drive hoặc giải phóng dung lượng.", err)
				case strings.Contains(errMsg, "parent folder not found"):
					writeError(w, http.StatusNotFound, "Thư mục đích không tồn tại.", err)
				case strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context deadline"):
					writeError(w, http.StatusRequestTimeout, "Yêu cầu tải lên đã hết thời gian hoặc bị hủy.", err)
				case strings.Contains(errMsg, "error reading file data"):
					writeError(w, http.StatusBadRequest, "Lỗi đọc dữ liệu tệp. Kết nối có thể đã bị ngắt.", err)
				default:
					writeError(w, http.StatusInternalServerError, "Lỗi tải tệp lên. Vui lòng thử lại sau.", err)
				}
				return
			}
			uploadedFile = vfile
			uploadedFile.Replaced = wasReplaced
		}

	}

	if uploadedFile == nil {
		writeError(w, http.StatusBadRequest, "Không tìm thấy tệp đính kèm trong request", nil)
		return
	}

	writeJSON(w, http.StatusOK, uploadedFile)
}



func (s *Server) handleChunkedUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	// Auth: copy logic from handleUploadFile
	user := s.getUserFromRequest(r)
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	} else {
		if guestMode == "strict" {
			writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để tải lên tệp tin", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Chế độ Khách (Guest) chỉ cho phép xem và tải xuống. Vui lòng đăng nhập để tải lên.", nil)
			return
		}
	}

	// Determine mode: INIT or CHUNK
	uploadID := r.URL.Query().Get("upload_id")
	chunkIndexStr := r.URL.Query().Get("chunk_index")

	if uploadID == "" && chunkIndexStr == "" {
		// === INIT MODE: Content-Type should be application/json ===
		var initReq struct {
			FileName    string `json:"file_name"`
			ParentID    string `json:"parent_id"`
			TotalSize   int64  `json:"total_size"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&initReq); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body", err)
			return
		}
		if initReq.FileName == "" || initReq.TotalChunks <= 0 {
			writeError(w, http.StatusBadRequest, "Thiếu file_name hoặc total_chunks không hợp lệ", nil)
			return
		}
		if initReq.ParentID == "" {
			initReq.ParentID = "root"
		}

		// Kiểm tra hạn ngạch người dùng trước khi cấp phép phiên tải lên (P1: Quota Enforcement)
		if currentUserID != "user_admin" && user != nil && user.Role != "admin" {
			dbUser, uErr := s.db.GetUserByID(currentUserID)
			if uErr == nil && dbUser != nil && dbUser.QuotaBytes > 0 {
				remQuota := dbUser.QuotaBytes - dbUser.UsedBytes
				if oldFile, _ := s.db.FindFileByNameInParent(currentUserID, initReq.ParentID, initReq.FileName); oldFile != nil {
					remQuota += oldFile.SizeBytes
				}
				if remQuota <= 0 || (initReq.TotalSize > 0 && initReq.TotalSize > remQuota) {
					writeError(w, http.StatusInsufficientStorage, fmt.Sprintf("Hạn ngạch dung lượng tài khoản không đủ (Còn trống %s, tệp yêu cầu %s)", formatBytes(remQuota), formatBytes(initReq.TotalSize)), nil)
					return
				}
			}
		}

		newID := uuid.New().String()
		tempDir := filepath.Join(s.baseDir, "data", "temp_chunks", newID)
		if err := os.MkdirAll(tempDir, 0700); err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi khởi tạo không gian lưu trữ mảnh tạm", err)
			return
		}

		session := &ChunkedUploadSession{
			UploadID:    newID,
			FileName:    initReq.FileName,
			ParentID:    initReq.ParentID,
			UserID:      currentUserID,
			TotalSize:   initReq.TotalSize,
			TotalChunks: initReq.TotalChunks,
			TempDir:     tempDir,
			Received:    make(map[int]bool),
			Status:      "uploading",
			CreatedAt:   time.Now(),
		}
		chunkedSessions.Store(newID, session)

		// Lưu session.json vào ổ đĩa để phiên tải lên không bị mất nếu server reload
		if sBytes, err := json.Marshal(session); err == nil {
			_ = os.WriteFile(filepath.Join(tempDir, "session.json"), sBytes, 0600)
		}

		log.Printf("[ENGINE] Chunked upload session created: %s, file=%s, chunks=%d, size=%d, user=%s (tempDir: %s)",
			newID, initReq.FileName, initReq.TotalChunks, initReq.TotalSize, currentUserID, tempDir)

		writeJSON(w, http.StatusOK, map[string]string{"upload_id": newID})
		return
	}

	// === CHUNK MODE: receive a chunk ===
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu upload_id", nil)
		return
	}

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "chunk_index không hợp lệ", err)
		return
	}

	session, ok := s.getOrRestoreChunkedSession(uploadID)
	if !ok {
		writeError(w, http.StatusNotFound, "Phiên tải lên không tồn tại hoặc đã hết hạn", nil)
		return
	}

	session.mu.Lock()
	sessStatus := session.Status
	session.mu.Unlock()
	if sessStatus != "uploading" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  sessStatus,
			"message": "Phiên tải lên đang được xử lý ghép tệp hoặc đã hoàn tất",
		})
		return
	}

	// Verify user ownership
	if session.UserID != currentUserID && session.UserID != "user_guest" && currentUserID != "user_guest" && (user == nil || user.Role != "admin") {
		writeError(w, http.StatusForbidden, "Không có quyền truy cập phiên tải lên này", nil)
		return
	}

	if chunkIndex < 0 || chunkIndex >= session.TotalChunks {
		writeError(w, http.StatusBadRequest, "chunk_index ngoài phạm vi cho phép", nil)
		return
	}

	// Kiểm tra nếu chunk này đã được nhận trước đó (idempotent, mobile retry mượt mà)
	session.mu.Lock()
	if session.Received[chunkIndex] {
		session.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "chunk_received",
			"chunk_index":  chunkIndex,
			"total_chunks": session.TotalChunks,
			"received":     len(session.Received),
		})
		return
	}
	session.mu.Unlock()

	// Read chunk data from multipart form and stream directly to disk file (Zero RAM allocation)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart request", err)
		return
	}

	chunkFilePath := filepath.Join(session.TempDir, fmt.Sprintf("chunk_%d", chunkIndex))
	chunkFile, err := os.OpenFile(chunkFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo tệp lưu trữ mảnh tạm", err)
		return
	}

	var written int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			chunkFile.Close()
			_ = os.Remove(chunkFilePath)
			writeError(w, http.StatusBadRequest, "Lỗi đọc multipart part", err)
			return
		}
		if part.FormName() == "chunk" {
			written, err = io.Copy(chunkFile, part)
			chunkFile.Close()
			if err != nil {
				_ = os.Remove(chunkFilePath)
				writeError(w, http.StatusInternalServerError, "Lỗi ghi dữ liệu mảnh lên ổ đĩa", err)
				return
			}
			break
		}
	}

	if written == 0 {
		_ = os.Remove(chunkFilePath)
		writeError(w, http.StatusBadRequest, "Không tìm thấy dữ liệu chunk trong request hoặc chunk rỗng", nil)
		return
	}

	// Store chunk status & Atomic CAS Assembling transition (Chống Double Assembling Race Condition)
	var shouldStartAssembly bool
	session.mu.Lock()
	session.Received[chunkIndex] = true
	receivedCount := len(session.Received)
	if receivedCount == session.TotalChunks && session.Status == "uploading" {
		session.Status = "assembling"
		shouldStartAssembly = true
		if sBytes, err := json.Marshal(session); err == nil && session.TempDir != "" {
			_ = os.WriteFile(filepath.Join(session.TempDir, "session.json"), sBytes, 0600)
		}
	}
	session.mu.Unlock()

	log.Printf("[ENGINE] Chunked upload %s: received chunk %d/%d (%d bytes written to disk)",
		uploadID, chunkIndex+1, session.TotalChunks, written)

	if !shouldStartAssembly {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "chunk_received",
			"received": receivedCount,
			"total":    session.TotalChunks,
		})
		return
	}

	log.Printf("[ENGINE] Chunked upload %s: all %d chunks received, starting async assembly for file %s",
		uploadID, session.TotalChunks, session.FileName)

	go func(uID string, sess *ChunkedUploadSession) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		defer func() {
			if sess.TempDir != "" {
				_ = os.RemoveAll(sess.TempDir)
			}
		}()

		pr, pw := io.Pipe()

		go func() {
			defer pw.Close()
			for i := 0; i < sess.TotalChunks; i++ {
				cPath := filepath.Join(sess.TempDir, fmt.Sprintf("chunk_%d", i))
				cf, err := os.Open(cPath)
				if err != nil {
					pw.CloseWithError(fmt.Errorf("mảnh %d bị thiếu trên ổ đĩa: %w", i, err))
					return
				}
				if _, err := io.Copy(pw, cf); err != nil {
					cf.Close()
					_ = os.Remove(cPath)
					pw.CloseWithError(err)
					return
				}
				cf.Close()
				_ = os.Remove(cPath) // Xóa mảnh ngay sau khi stream vào mã hóa Google Drive
			}
		}()

		existingOld, _ := s.db.FindFileByNameInParent(sess.UserID, sess.ParentID, sess.FileName)
		wasReplaced := existingOld != nil

		vfile, err := s.vfs.UploadFile(bgCtx, sess.UserID, sess.ParentID, sess.FileName, pr, sess.TotalSize)
		sess.mu.Lock()
		defer sess.mu.Unlock()

		if err != nil {
			log.Printf("[ENGINE] [ASYNC-UPLOAD] Error assembling file %s for user %s: %v", sess.FileName, sess.UserID, err)
			sess.Status = "error"
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "không có tài khoản Google Drive nào còn đủ dung lượng") || strings.Contains(errMsg, "storage") || strings.Contains(errMsg, "quota"):
				sess.ErrorMsg = "Hết dung lượng lưu trữ. Vui lòng thêm tài khoản Google Drive hoặc giải phóng dung lượng."
			case strings.Contains(errMsg, "parent folder not found"):
				sess.ErrorMsg = "Thư mục đích không tồn tại."
			case strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context deadline"):
				sess.ErrorMsg = "Xử lý tệp đã quá thời gian cho phép hoặc bị hủy."
			case strings.Contains(errMsg, "error reading file data"):
				sess.ErrorMsg = "Lỗi đọc dữ liệu tệp tin."
			default:
				sess.ErrorMsg = "Lỗi ghép và mã hóa tệp lên đám mây. Vui lòng thử lại sau."
			}
			if sBytes, sErr := json.Marshal(sess); sErr == nil && sess.TempDir != "" {
				_ = os.WriteFile(filepath.Join(sess.TempDir, "session.json"), sBytes, 0600)
			}
			return
		}

		vfile.Replaced = wasReplaced
		sess.ResultFile = vfile
		sess.Status = "completed"
		if sBytes, sErr := json.Marshal(sess); sErr == nil && sess.TempDir != "" {
			_ = os.WriteFile(filepath.Join(sess.TempDir, "session.json"), sBytes, 0600)
		}
		log.Printf("[ENGINE] [ASYNC-UPLOAD] Chunked upload %s finished successfully: file=%s, id=%s", uID, sess.FileName, vfile.ID)
	}(uploadID, session)

	// Phản hồi ngay lập tức cho client không để HTTP request bị Cloudflare timeout (Error 524)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "assembling",
		"received":  receivedCount,
		"total":     session.TotalChunks,
		"upload_id": uploadID,
		"message":   "Tất cả mảnh đã tải lên máy chủ. Đang mã hóa và phân tán lên Google Drive...",
	})
}



func (s *Server) handleChunkedUploadStatus(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("upload_id")
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu upload_id", nil)
		return
	}

	session, ok := s.getOrRestoreChunkedSession(uploadID)
	if !ok {
		writeError(w, http.StatusNotFound, "Phiên tải lên không tồn tại hoặc đã hết hạn", nil)
		return
	}

	// Auth check
	user := s.getUserFromRequest(r)
	currentUserID := "user_guest"
	if user != nil {
		currentUserID = user.ID
	}
	if session.UserID != currentUserID && session.UserID != "user_guest" && currentUserID != "user_guest" && (user == nil || user.Role != "admin") {
		writeError(w, http.StatusForbidden, "Không có quyền truy cập phiên này", nil)
		return
	}

	session.mu.Lock()
	status := session.Status
	resultFile := session.ResultFile
	errMsg := session.ErrorMsg
	fileName := session.FileName
	totalSize := session.TotalSize
	totalChunks := session.TotalChunks
	receivedChunks := make([]int, 0, len(session.Received))
	for idx, ok := range session.Received {
		if ok {
			receivedChunks = append(receivedChunks, idx)
		}
	}
	sort.Ints(receivedChunks)
	session.mu.Unlock()

	if status == "completed" && resultFile != nil {
		if session.TempDir != "" {
			_ = os.RemoveAll(session.TempDir)
		}
		// Giữ lại session trong chunkedSessions để các lần kiểm tra tiếp theo không bị 404,
		// cleanupExpiredSessions() sẽ tự động dọn dẹp sau 2 giờ.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "completed",
			"file":   resultFile,
		})
		return
	}

	if status == "error" {
		if session.TempDir != "" {
			_ = os.RemoveAll(session.TempDir)
		}
		// Giữ lại session để client nhận đúng chi tiết lỗi thay vì bị 404 không tìm thấy phiên
		writeError(w, http.StatusInternalServerError, errMsg, nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":          status, // "uploading" hoặc "assembling"
		"upload_id":       uploadID,
		"file_name":       fileName,
		"total_size":      totalSize,
		"total_chunks":    totalChunks,
		"received_chunks": receivedChunks,
		"received_count":  len(receivedChunks),
	})
}

// toASCIIFallback chuyển đổi chuỗi tiếng Việt thành chuỗi ASCII an toàn làm fallback cho client cũ


func (s *Server) handleFileChunks(w http.ResponseWriter, r *http.Request) {
	fileID := r.URL.Query().Get("id")
	if fileID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID", nil)
		return
	}

	chunks, err := s.db.GetChunkDetailsForFile(fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy thông tin chunks: "+err.Error(), err)
		return
	}

	vfile, _ := s.db.GetVirtualFile(fileID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"file":   vfile,
		"chunks": chunks,
	})
}



func (s *Server) handleIntegrityCheck(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản Trị Viên mới có quyền thực hiện kiểm tra tính toàn vẹn lưu trữ", nil)
		return
	}

	var opts storage.IntegrityCheckOptions
	if r.Method == http.MethodPost && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&opts)
	}

	// Ưu tiên tham số URL query nếu có
	if qFileID := r.URL.Query().Get("file_id"); qFileID != "" {
		opts.FileID = qFileID
	}
	if qAccountID := r.URL.Query().Get("account_id"); qAccountID != "" {
		opts.AccountID = qAccountID
	}
	if r.URL.Query().Get("force") == "true" {
		opts.Force = true
	}
	if qConcurrency := r.URL.Query().Get("concurrency"); qConcurrency != "" {
		if c, err := strconv.Atoi(qConcurrency); err == nil && c > 0 {
			opts.Concurrency = c
		}
	}

	integrityService := storage.NewIntegrityService(s.db, s.gd)
	report, err := integrityService.RunCheck(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi trong quá trình kiểm tra tính toàn vẹn: "+err.Error(), err)
		return
	}

	// Ghi nhật ký hoạt động kiểm tra
	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "STORAGE_INTEGRITY_CHECK",
		Target:    fmt.Sprintf("Quét %d tệp, %d chunks", report.TotalFilesScanned, report.TotalChunksScanned),
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Kết quả: %d lành lặn, %d thiếu dữ liệu, %d chunks missing trong %dms", report.HealthyFilesCount, report.CorruptedFilesCount, report.MissingChunksCount, report.DurationMs),
	})

	writeJSON(w, http.StatusOK, report)
}


