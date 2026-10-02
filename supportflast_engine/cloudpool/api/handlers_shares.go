package api

import (
	"archive/zip"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"github.com/google/uuid"
)


func (s *Server) handleCreatePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		FileID       string `json:"file_id"`
		Password     string `json:"password"`
		ExpiresHours int    `json:"expires_hours"` // 0 = never, 1, 24, 168
		MaxDownloads int    `json:"max_downloads"` // 0 = unlimited
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file_id hợp lệ", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tệp tin hoặc thư mục không tồn tại", err)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để tạo liên kết chia sẻ công khai", nil)
		return
	}
	if user.Role != "admin" && vfile.UserID != "" && vfile.UserID != user.ID {
		writeError(w, http.StatusForbidden, "Bạn không có quyền chia sẻ tệp tin của tài khoản khác", nil)
		return
	}
	createdBy := user.Username

	var passHash string
	if req.Password != "" {
		passHash = core.HashSHA256([]byte(req.Password))
	}

	var exp *time.Time
	if req.ExpiresHours > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresHours) * time.Hour)
		exp = &t
	}

	fileSize := vfile.SizeBytes
	fileCount := 0
	mimeType := vfile.MimeType

	if vfile.IsDir {
		mimeType = "directory"
		totSize, count, err := s.db.GetFolderStats(vfile.ID)
		if err == nil {
			fileSize = totSize
			fileCount = count
		}
	}

	shareToken := uuid.New().String()
	share := &models.PublicShare{
		ID:            shareToken,
		FileID:        req.FileID,
		FileName:      vfile.Name,
		FileSize:      fileSize,
		MimeType:      mimeType,
		IsDir:         vfile.IsDir,
		FileCount:     fileCount,
		CreatedBy:     createdBy,
		PasswordHash:  passHash,
		HasPassword:   passHash != "",
		MaxDownloads:  req.MaxDownloads,
		DownloadCount: 0,
		ExpiresAt:     exp,
		CreatedAt:     time.Now(),
		IsActive:      true,
	}

	if err := s.db.CreatePublicShare(share); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tạo link chia sẻ: "+err.Error(), err)
		return
	}

	targetType := "tệp"
	if vfile.IsDir {
		targetType = "thư mục"
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    createdBy,
		Username:  createdBy,
		Action:    "SHARE",
		Target:    vfile.Name,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Đã tạo link chia sẻ công khai cho %s '%s'", targetType, vfile.Name),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"share_token":  shareToken,
		"share":        share,
		"message":      fmt.Sprintf("Đã tạo link chia sẻ %s thành công!", targetType),
	})
}



func (s *Server) handleListPublicShares(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để xem danh sách liên kết chia sẻ", nil)
		return
	}

	shares, err := s.db.ListPublicShares()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lấy danh sách link chia sẻ", err)
		return
	}

	if user.Role != "admin" {
		filtered := make([]models.PublicShare, 0)
		for _, sh := range shares {
			if sh.CreatedBy == user.Username {
				filtered = append(filtered, sh)
			}
		}
		writeJSON(w, http.StatusOK, filtered)
		return
	}

	writeJSON(w, http.StatusOK, shares)
}



func (s *Server) handleRevokePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để thu hồi liên kết chia sẻ", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu share ID", err)
		return
	}

	if user.Role != "admin" {
		sh, err := s.db.GetPublicShare(req.ID)
		if err == nil && sh != nil && sh.CreatedBy != user.Username {
			writeError(w, http.StatusForbidden, "Bạn không có quyền thu hồi liên kết chia sẻ của người khác", nil)
			return
		}
	}

	if err := s.db.RevokePublicShare(req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể thu hồi link: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã thu hồi link chia sẻ thành công"})
}



func (s *Server) handlePublicShareInfo(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiếu share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil {
		writeError(w, http.StatusNotFound, "Link chia sẻ không tồn tại hoặc đã bị thu hồi", err)
		return
	}

	if !sh.IsActive {
		writeError(w, http.StatusGone, "Link chia sẻ này đã hết hạn hoặc đạt giới hạn tải tối đa", nil)
		return
	}

	resp := map[string]interface{}{
		"id":           sh.ID,
		"file_name":    sh.FileName,
		"file_size":    sh.FileSize,
		"mime_type":    sh.MimeType,
		"is_dir":       sh.IsDir,
		"file_count":   sh.FileCount,
		"has_password": sh.HasPassword,
		"created_by":   sh.CreatedBy,
		"created_at":   sh.CreatedAt,
		"expires_at":   sh.ExpiresAt,
	}

	if sh.IsDir && !sh.HasPassword {
		items, _ := s.db.ListFilesInFolderForShare(sh.FileID, sh.FileID)
		resp["items"] = items
	}

	writeJSON(w, http.StatusOK, resp)
}



func (s *Server) handlePublicShareBrowseFolder(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	folderID := r.URL.Query().Get("folder_id")
	pass := r.URL.Query().Get("pass")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiếu share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sẻ không tồn tại hoặc đã hết hạn", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Mật khẩu bảo vệ không chính xác", nil)
			return
		}
	}

	if !sh.IsDir {
		writeError(w, http.StatusBadRequest, "Đối tượng chia sẻ không phải là thư mục", nil)
		return
	}

	targetFolderID := folderID
	if targetFolderID == "" {
		targetFolderID = sh.FileID
	}

	items, err := s.db.ListFilesInFolderForShare(sh.FileID, targetFolderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Lỗi nạp thư mục: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"current_folder_id": targetFolderID,
		"root_folder_id":    sh.FileID,
		"folder_name":       sh.FileName,
		"items":             items,
	})
}



func (s *Server) handlePublicShareStream(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pass := r.URL.Query().Get("pass")
	fileID := r.URL.Query().Get("file_id")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiếu share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sẻ không tồn tại hoặc đã hết hạn", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Mật khẩu bảo vệ không chính xác", nil)
			return
		}
	}

	targetFileID := sh.FileID
	targetFileName := sh.FileName
	targetMimeType := sh.MimeType

	if fileID != "" && fileID != sh.FileID {
		if !sh.IsDir {
			writeError(w, http.StatusForbidden, "Link chia sẻ chỉ dành cho tệp tin đơn lẻ, không được phép chỉ định tệp khác", nil)
			return
		}
		if !s.db.IsFileDescendantOfFolder(sh.FileID, fileID) {
			writeError(w, http.StatusForbidden, "Tệp tin yêu cầu không nằm trong thư mục được chia sẻ", nil)
			return
		}
		vfile, err := s.db.GetVirtualFile(fileID)
		if err != nil || vfile.IsDir {
			writeError(w, http.StatusNotFound, "Tệp tin không tồn tại", err)
			return
		}
		targetFileID = vfile.ID
		targetFileName = vfile.Name
		targetMimeType = vfile.MimeType
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), targetFileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi nạp tệp: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	mimeType := targetMimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = models.ResolveMimeType(targetFileName)
	} else {
		resolved := models.ResolveMimeType(targetFileName)
		if resolved != "application/octet-stream" {
			mimeType = resolved
		}
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("inline", targetFileName))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	
	if models.IsMediaStreamable(mimeType) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
	}
	
	http.ServeContent(w, r, targetFileName, streamer.ModTime(), streamer)
}



func (s *Server) handlePublicShareDownload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pass := r.URL.Query().Get("pass")
	fileID := r.URL.Query().Get("file_id")

	if token == "" {
		writeError(w, http.StatusBadRequest, "Thiếu share token", nil)
		return
	}

	sh, err := s.db.GetPublicShare(token)
	if err != nil || sh == nil || !sh.IsActive {
		writeError(w, http.StatusNotFound, "Link chia sẻ không tồn tại hoặc đã hết hạn", nil)
		return
	}

	if sh.HasPassword {
		if pass == "" || subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "Mật khẩu bảo vệ không chính xác", nil)
			return
		}
	}

	// Trừ lượt tải atomic (P1.5): Ngăn chặn Race Condition tải vượt quá max_downloads
	if err := s.db.ConsumePublicShareDownload(token); err != nil {
		writeError(w, http.StatusGone, "Liên kết chia sẻ đã hết lượt tải cho phép hoặc đã hết hạn", err)
		return
	}

	// If downloading a specific file inside a shared folder
	if fileID != "" && fileID != sh.FileID {
		if !sh.IsDir {
			writeError(w, http.StatusForbidden, "Link chia sẻ chỉ dành cho tệp tin đơn lẻ, không được phép chỉ định tệp khác", nil)
			return
		}
		if !s.db.IsFileDescendantOfFolder(sh.FileID, fileID) {
			writeError(w, http.StatusForbidden, "Tệp tin yêu cầu không nằm trong thư mục được chia sẻ", nil)
			return
		}
		vfile, err := s.db.GetVirtualFile(fileID)
		if err != nil || vfile.IsDir {
			writeError(w, http.StatusNotFound, "Tệp không tồn tại", err)
			return
		}
		streamer, err := s.vfs.NewFileStreamer(r.Context(), vfile.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi nạp tệp: "+err.Error(), err)
			return
		}
		defer streamer.Close()

		downloadMime := vfile.MimeType
		if downloadMime == "" || downloadMime == "application/octet-stream" {
			downloadMime = models.ResolveMimeType(vfile.Name)
		}
		w.Header().Set("Content-Type", downloadMime)
		w.Header().Set("Content-Disposition", formatContentDisposition("attachment", vfile.Name))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, vfile.Name, streamer.ModTime(), streamer)
		return
	}

	// If downloading entire Folder -> Stream as ZIP on the fly!
	if sh.IsDir {
		filesInTree, err := s.db.GetAllFilesInFolderTree(sh.FileID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi đọc cây thư mục: "+err.Error(), err)
			return
		}

		zipName := fmt.Sprintf("%s.zip", sh.FileName)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", formatContentDisposition("attachment", zipName))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")

		zipWriter := zip.NewWriter(w)
		defer zipWriter.Close()

		for _, item := range filesInTree {
			streamer, err := s.vfs.NewFileStreamer(r.Context(), item.File.ID)
			if err != nil {
				continue
			}

			entryPath := item.RelativePath
			if entryPath == "" {
				entryPath = item.File.Name
			}

			modTime := item.File.UpdatedAt
			if modTime.IsZero() {
				modTime = time.Now()
			}

			header := &zip.FileHeader{
				Name:     entryPath,
				Modified: modTime,
			}
			ext := strings.ToLower(filepath.Ext(item.File.Name))
			if ext == ".zip" || ext == ".rar" || ext == ".7z" || ext == ".gz" || ext == ".tar" ||
				ext == ".mp4" || ext == ".mkv" || ext == ".mp3" || ext == ".jpg" || ext == ".jpeg" ||
				ext == ".png" || ext == ".webp" || ext == ".iso" {
				header.Method = zip.Store
			} else {
				header.Method = zip.Deflate
			}

			zf, err := zipWriter.CreateHeader(header)
			if err != nil {
				streamer.Close()
				continue
			}

			_, copyErr := io.Copy(zf, streamer)
			streamer.Close()
			if copyErr != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		_ = zipWriter.Close()
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	// Single File Download
	streamer, err := s.vfs.NewFileStreamer(r.Context(), sh.FileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi nạp tệp: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	w.Header().Set("Content-Type", sh.MimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("attachment", sh.FileName))
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, sh.FileName, streamer.ModTime(), streamer)
}

// -------------------------------------------------------------
// Storage Breakdown & Analytics Handler
// -------------------------------------------------------------



func generateSecureOTP() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	num := (int(b[0])<<16 | int(b[1])<<8 | int(b[2]))%900000 + 100000
	return fmt.Sprintf("%06d", num)
}



func (s *Server) handleVerifyFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để xác thực mã OTP", nil)
		return
	}

	var req struct {
		FileID  string `json:"file_id"`
		OTPCode string `json:"otp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" || req.OTPCode == "" {
		writeError(w, http.StatusBadRequest, "Thiếu thông tin tệp tin hoặc mã OTP", err)
		return
	}

	valid, err := s.db.VerifyAndBurnOTP(req.FileID, user.ID, req.OTPCode)
	if !valid || err != nil {
		writeError(w, http.StatusForbidden, "Lỗi xác thực: "+err.Error(), err)
		return
	}

	vfile, _ := s.db.GetVirtualFile(req.FileID)
	fileName := req.FileID
	if vfile != nil {
		fileName = vfile.Name
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_VERIFY_SUCCESS",
		Target:    fileName,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Xác thực thành công mã OTP mở khóa tệp '%s' (Mã đã được hủy sử dụng 1 lần)", fileName),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "success",
		"message":      "Mở khóa tệp tin thành công! Mã OTP đã được sử dụng (1 lần).",
		"file_id":      req.FileID,
		"file_name":    fileName,
		"otp_burned":   true,
		"auto_unlock":  true, // Frontend dùng để tự động mở file
	})
}



func (s *Server) handleRequestFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để gửi yêu cầu", nil)
		return
	}

	var req struct {
		FileID string `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID tệp tin", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tệp tin không tồn tại", err)
		return
	}

	accessReq := &models.FileAccessRequest{
		FileID:          vfile.ID,
		FileName:        vfile.Name,
		UserID:          user.ID,
		Username:        user.Username,
		UserDisplayName: user.DisplayName,
		Status:          "pending",
	}

	if err := s.db.CreateFileAccessRequest(accessReq); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi gửi yêu cầu: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Đã gửi yêu cầu cấp mã OTP tới Quản Trị Viên thành công!",
		"request": accessReq,
	})
}



func (s *Server) handleListMyFileRequests(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Chưa đăng nhập", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách yêu cầu", err)
		return
	}

	// Filter by current user
	myRequests := make([]models.FileAccessRequest, 0)
	for _, req := range requests {
		if req.UserID == user.ID {
			myRequests = append(myRequests, req)
		}
	}
	writeJSON(w, http.StatusOK, myRequests)
}



func (s *Server) handleGenerateFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền tạo mã OTP", nil)
		return
	}

	var req struct {
		FileID          string `json:"file_id"`
		TargetUserID    string `json:"target_user_id"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID tệp tin", err)
		return
	}

	vfile, err := s.db.GetVirtualFile(req.FileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Tệp tin không tồn tại", err)
		return
	}

	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 1440 // 24 hours default
	}
	if req.TargetUserID == "" {
		req.TargetUserID = "all"
	}

	otpCode := generateSecureOTP()
	expiresAt := time.Now().Add(time.Duration(req.DurationMinutes) * time.Minute)

	otpObj := &models.FileAccessOTP{
		FileID:       vfile.ID,
		FileName:     vfile.Name,
		TargetUserID: req.TargetUserID,
		OTPCode:      otpCode,
		CreatedBy:    user.ID,
		ExpiresAt:    expiresAt,
	}

	if err := s.db.CreateFileOTP(otpObj); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo mã OTP: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_GENERATE",
		Target:    vfile.Name,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Tạo mã OTP 1 lần cho tệp '%s' (Hiệu lực %d phút)", vfile.Name, req.DurationMinutes),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Tạo mã OTP mở khóa 1 lần thành công!",
		"otp_code":   otpCode,
		"file_name":  vfile.Name,
		"expires_at": expiresAt.Format("2006-01-02 15:04:05"),
		"otp":        otpObj,
	})
}



func (s *Server) handleListFileOTPs(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền xem danh sách OTP", nil)
		return
	}

	otps, err := s.db.ListFileOTPs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách OTP: "+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, otps)
}



func (s *Server) handleRevokeFileOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền thu hồi OTP", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID OTP", err)
		return
	}

	if err := s.db.RevokeFileOTP(req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi thu hồi OTP: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã thu hồi và hủy mã OTP thành công"})
}



func (s *Server) handleListAdminAccessRequests(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền xem danh sách yêu cầu", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi lấy danh sách yêu cầu", err)
		return
	}
	writeJSON(w, http.StatusOK, requests)
}



func (s *Server) handleApproveAccessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền phê duyệt yêu cầu", nil)
		return
	}

	var req struct {
		RequestID       string `json:"request_id"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu thông tin yêu cầu", err)
		return
	}

	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 1440 // 24 hours default
	}

	requests, _ := s.db.ListFileAccessRequests("all")
	var targetReq *models.FileAccessRequest
	for _, ar := range requests {
		if ar.ID == req.RequestID {
			targetReq = &ar
			break
		}
	}

	if targetReq == nil {
		writeError(w, http.StatusNotFound, "Không tìm thấy yêu cầu", nil)
		return
	}

	otpCode := generateSecureOTP()
	expiresAt := time.Now().Add(time.Duration(req.DurationMinutes) * time.Minute)

	// Create OTP record for this specific user
	otpObj := &models.FileAccessOTP{
		FileID:       targetReq.FileID,
		FileName:     targetReq.FileName,
		TargetUserID: targetReq.UserID,
		OTPCode:      otpCode,
		CreatedBy:    user.ID,
		ExpiresAt:    expiresAt,
	}
	_ = s.db.CreateFileOTP(otpObj)
	_ = s.db.ApproveFileAccessRequest(targetReq.ID, otpCode)

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "OTP_APPROVE_REQUEST",
		Target:    targetReq.FileName,
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Phê duyệt cấp mã OTP cho user '%s' truy cập tệp '%s'", targetReq.Username, targetReq.FileName),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    fmt.Sprintf("Đã phê duyệt và cấp mã OTP '%s' cho người dùng %s thành công!", otpCode, targetReq.Username),
		"otp_code":   otpCode,
		"expires_at": expiresAt.Format("2006-01-02 15:04:05"),
	})
}



func (s *Server) handleRejectAccessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền từ chối yêu cầu", nil)
		return
	}

	var req struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu ID yêu cầu", err)
		return
	}

	_ = s.db.RejectFileAccessRequest(req.RequestID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã từ chối yêu cầu cấp OTP"})
}



func (s *Server) handleOTPPendingCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Admin only", nil)
		return
	}

	requests, err := s.db.ListFileAccessRequests("all")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]int{"pending_count": 0})
		return
	}

	count := 0
	for _, req := range requests {
		if req.Status == "pending" {
			count++
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{"pending_count": count})
}

// -------------------------------------------------------------
// Recycle Bin (Trash) Handlers
// -------------------------------------------------------------

