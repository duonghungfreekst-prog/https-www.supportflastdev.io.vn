package api

import (
	"archive/zip"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"
	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
)


func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)

	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	if user == nil && guestMode == "strict" {
		writeError(w, http.StatusUnauthorized, "Chế độ bảo mật nghiêm ngặt. Vui lòng đăng nhập để xem danh sách tệp tin.", nil)
		return
	}

	accountID := r.URL.Query().Get("account_id")
	if accountID != "" {
		files, err := s.db.ListFilesByAccount(accountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Không thể tải danh sách tệp của tài khoản", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"parent": nil,
			"files":  files,
		})
		return
	}

	parentID := r.URL.Query().Get("parent_id")
	if parentID == "" {
		parentID = "root"
	}

	var targetUserID string
	if user == nil {
		targetUserID = "guest"
	} else if user.Role == "admin" {
		filterUser := r.URL.Query().Get("user_id")
		if filterUser != "" {
			targetUserID = filterUser
		} else {
			targetUserID = "all"
		}
	} else {
		// Regular user strictly isolated to their own partition
		targetUserID = user.ID
	}

	files, err := s.vfs.ListDirectory(targetUserID, parentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tải danh sách tệp", err)
		return
	}

	// Also retrieve current folder info if not root
	var currentFolder *models.VirtualFile
	if parentID != "root" {
		currentFolder, _ = s.db.GetVirtualFile(parentID)
	}

	// Tính toán ETag nhanh dựa trên targetUserID, parentID, số lượng tệp và max updated_at
	var maxUpdated int64
	for _, f := range files {
		t := f.UpdatedAt.UnixNano()
		if t > maxUpdated {
			maxUpdated = t
		}
	}
	if currentFolder != nil && currentFolder.UpdatedAt.UnixNano() > maxUpdated {
		maxUpdated = currentFolder.UpdatedAt.UnixNano()
	}

	etag := fmt.Sprintf(`W/"vfs-%s-%s-%d-%d"`, targetUserID, parentID, len(files), maxUpdated)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")

	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"parent": currentFolder,
		"files":  files,
	})
}



func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
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
			writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để tạo thư mục", nil)
			return
		}
		if guestMode == "view_only" {
			writeError(w, http.StatusForbidden, "Chế độ Khách chỉ cho phép xem và tải xuống. Vui lòng đăng nhập để tạo thư mục.", nil)
			return
		}
	}

	var body struct {
		ParentID string `json:"parent_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "Tên thư mục không hợp lệ", err)
		return
	}

	folder, err := s.vfs.Mkdir(currentUserID, body.ParentID, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, folder)
}



func (s *Server) handleRenameFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" || body.NewName == "" {
		writeError(w, http.StatusBadRequest, "Dữ liệu đổi tên không hợp lệ", err)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để đổi tên tệp tin", nil)
		return
	}
	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(body.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Bạn không có quyền đổi tên tệp tin của tài khoản khác", nil)
			return
		}
	}

	if err := s.vfs.RenameFileOrFolder(body.ID, body.NewName); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi đổi tên: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã đổi tên thành công"})
}



func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để xóa tệp tin", nil)
		return
	}
	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(id)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Bạn không có quyền xóa tệp tin của tài khoản khác", nil)
			return
		}
	}

	if err := s.vfs.DeleteFileOrFolder(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi xóa tệp/thư mục: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã xóa thành công"})
}



func (s *Server) handleBulkDeleteFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "Danh sách tệp không hợp lệ", err)
		return
	}

	deletedCount := 0
	for _, id := range req.IDs {
		if err := s.vfs.DeleteFileOrFolder(r.Context(), id); err == nil {
			deletedCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":       fmt.Sprintf("Đã xóa thành công %d tệp/thư mục", deletedCount),
		"deleted_count": deletedCount,
	})
}



func toASCIIFallback(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case 'à', 'á', 'ả', 'ã', 'ạ', 'ă', 'ằ', 'ắ', 'ẳ', 'ẵ', 'ặ', 'â', 'ầ', 'ấ', 'ẩ', 'ẫ', 'ậ':
			sb.WriteRune('a')
		case 'À', 'Á', 'Ả', 'Ã', 'Ạ', 'Ă', 'Ằ', 'Ắ', 'Ẳ', 'Ẵ', 'Ặ', 'Â', 'Ầ', 'Ấ', 'Ẩ', 'Ẫ', 'Ậ':
			sb.WriteRune('A')
		case 'đ':
			sb.WriteRune('d')
		case 'Đ':
			sb.WriteRune('D')
		case 'è', 'é', 'ẻ', 'ẽ', 'ẹ', 'ê', 'ề', 'ế', 'ể', 'ễ', 'ệ':
			sb.WriteRune('e')
		case 'È', 'É', 'Ẻ', 'Ẽ', 'Ẹ', 'Ê', 'Ề', 'Ế', 'Ể', 'Ễ', 'Ệ':
			sb.WriteRune('E')
		case 'ì', 'í', 'ỉ', 'ĩ', 'ị':
			sb.WriteRune('i')
		case 'Ì', 'Í', 'Ỉ', 'Ĩ', 'Ị':
			sb.WriteRune('I')
		case 'ò', 'ó', 'ỏ', 'õ', 'ọ', 'ô', 'ồ', 'ố', 'ổ', 'ỗ', 'ộ', 'ơ', 'ờ', 'ớ', 'ở', 'ỡ', 'ợ':
			sb.WriteRune('o')
		case 'Ò', 'Ó', 'Ỏ', 'Õ', 'Ọ', 'Ô', 'Ồ', 'Ố', 'Ổ', 'Ỗ', 'Ộ', 'Ơ', 'Ờ', 'Ớ', 'Ở', 'Ỡ', 'Ợ':
			sb.WriteRune('O')
		case 'ù', 'ú', 'ủ', 'ũ', 'ụ', 'ư', 'ừ', 'ứ', 'ử', 'ữ', 'ự':
			sb.WriteRune('u')
		case 'Ù', 'Ú', 'Ủ', 'Ũ', 'Ụ', 'Ư', 'Ừ', 'Ứ', 'Ử', 'Ữ', 'Ự':
			sb.WriteRune('U')
		case 'ỳ', 'ý', 'ỷ', 'ỹ', 'ỵ':
			sb.WriteRune('y')
		case 'Ỳ', 'Ý', 'Ỷ', 'Ỹ', 'Ỵ':
			sb.WriteRune('Y')
		default:
			if r > 31 && r < 127 && r != '"' && r != '\\' && r != ';' {
				sb.WriteRune(r)
			} else if r == ' ' {
				sb.WriteRune(' ')
			} else {
				sb.WriteRune('_')
			}
		}
	}
	res := strings.TrimSpace(sb.String())
	if res == "" {
		return "download"
	}
	return res
}

// formatBytes chuyển đổi số bytes sang chuỗi định dạng KB, MB, GB


func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	letters := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp < len(letters) {
		return fmt.Sprintf("%.2f %s", float64(b)/float64(div), letters[exp])
	}
	return fmt.Sprintf("%.2f EB", float64(b)/float64(div))
}

// formatContentDisposition tạo header Content-Disposition tuân thủ RFC 6266 và RFC 5987
// Hỗ trợ tiếng Việt có dấu chuẩn xác và chống CRLF Header Injection (Rule PHAN 3.6)


func formatContentDisposition(dispositionType, filename string) string {
	if dispositionType == "" {
		dispositionType = "attachment"
	}
	// Sanitize chống CRLF Header Injection (Rule PHAN 3.6)
	cleanName := strings.ReplaceAll(filename, "\r", "")
	cleanName = strings.ReplaceAll(cleanName, "\n", "")
	cleanName = strings.ReplaceAll(cleanName, `"`, `_`)

	// Fallback ASCII cho client cũ không hỗ trợ RFC 5987
	fallback := toASCIIFallback(cleanName)

	// RFC 5987 percent-encode:
	// attr-char = ALPHA / DIGIT / "!" / "#" / "$" / "&" / "+" / "-" / "." / "^" / "_" / "`" / "|" / "~"
	var rfc5987 strings.Builder
	for i := 0; i < len(cleanName); i++ {
		b := cleanName[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '!' || b == '#' || b == '$' || b == '&' || b == '+' || b == '-' ||
			b == '.' || b == '^' || b == '_' || b == '`' || b == '|' || b == '~' {
			rfc5987.WriteByte(b)
		} else {
			rfc5987.WriteString(fmt.Sprintf("%%%02X", b))
		}
	}

	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, dispositionType, fallback, rfc5987.String())
}



func (s *Server) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID", nil)
		return
	}

	vfile, err := s.db.GetVirtualFile(id)
	if err != nil || vfile.IsDir {
		writeError(w, http.StatusNotFound, "Tệp không tồn tại", err)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	isOwner := user != nil && vfile.UserID == user.ID

	if !isAdmin && !isOwner {
		// Kiểm tra quyền qua link chia sẻ công khai (Bắt buộc kèm share_token và mật khẩu hợp lệ)
		isShareAuthorized := false
		shareToken := r.URL.Query().Get("share_token")
		if shareToken == "" {
			shareToken = r.URL.Query().Get("token")
		}
		if shareToken != "" {
			sh, err := s.db.GetPublicShare(shareToken)
			if err == nil && sh != nil && sh.IsActive {
				pass := r.URL.Query().Get("pass")
				if !sh.HasPassword || (pass != "" && subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) == 1) {
					if sh.FileID == vfile.ID || (sh.IsDir && s.db.IsFileDescendantOfFolder(sh.FileID, vfile.ID)) {
						isShareAuthorized = true
					}
				}
			}
		}

		if !isShareAuthorized {
			settings, _ := s.db.GetSettings()
			guestMode := "view_only"
			if settings != nil && settings.GuestAccessMode != "" {
				guestMode = settings.GuestAccessMode
			}

			isAdminOwned := (vfile.UserID == "user_admin" || vfile.UserID == "")
			otp := r.URL.Query().Get("otp")
			if otp == "" {
				otp = r.Header.Get("X-File-OTP")
			}

			if isAdminOwned {
				if otp != "" {
					valid, err := s.db.VerifyAndBurnOTP(vfile.ID, "user_admin", otp)
					if !valid || err != nil {
						writeError(w, http.StatusForbidden, "Mã OTP mở khóa không hợp lệ hoặc đã hết hạn", err)
						return
					}
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    "user_admin",
						Username:  "admin",
						Action:    "OTP_STREAM",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Mở khóa xem tệp tin Admin thành công bằng mã OTP",
					})
				} else if guestMode == "strict" {
					if user == nil {
						writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để xem nội dung.", nil)
						return
					}
					writeError(w, http.StatusForbidden, "Tệp tin này yêu cầu Mã OTP do Quản Trị Viên cấp.", nil)
					return
				}
				// Chế độ view_only hoặc open: Cho phép xem và phát trực tiếp tệp tin trong kho lưu trữ
			} else {
				// Tệp tin riêng tư của người dùng khác
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để xem nội dung.", nil)
					return
				}
				writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập tệp tin riêng tư này.", nil)
				return
			}
		}
	}

	if vfile.HasMissingChunks {
		writeError(w, http.StatusGone, "⚠️ Tệp bị thiếu dữ liệu nguồn (chunk đã bị xóa trên Google Drive). Không thể phát trực tuyến.", nil)
		return
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi khởi tạo luồng stream: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	mimeType := vfile.MimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = models.ResolveMimeType(vfile.Name)
	} else {
		resolved := models.ResolveMimeType(vfile.Name)
		if resolved != "application/octet-stream" {
			mimeType = resolved
		}
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("inline", vfile.Name))
	w.Header().Set("Accept-Ranges", "bytes")
	// Cho phép cache phạm vi (Range Cache) cho video và âm thanh để trình duyệt tua và đệm mượt
	if models.IsMediaStreamable(mimeType) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
	}

	// Dùng http.ServeContent để stream trực tiếp từ ReadSeeker, hỗ trợ HTTP Range và chống tràn RAM OOM
	http.ServeContent(w, r, vfile.Name, streamer.ModTime(), streamer)
}





func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID", nil)
		return
	}

	vfile, err := s.db.GetVirtualFile(id)
	if err != nil || vfile.IsDir {
		writeError(w, http.StatusNotFound, "Tệp không tồn tại", err)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	isOwner := user != nil && vfile.UserID == user.ID

	if !isAdmin && !isOwner {
		// Kiểm tra quyền qua link chia sẻ công khai (Bắt buộc kèm share_token và mật khẩu hợp lệ)
		isShareAuthorized := false
		shareToken := r.URL.Query().Get("share_token")
		if shareToken == "" {
			shareToken = r.URL.Query().Get("token")
		}
		if shareToken != "" {
			sh, err := s.db.GetPublicShare(shareToken)
			if err == nil && sh != nil && sh.IsActive {
				pass := r.URL.Query().Get("pass")
				if !sh.HasPassword || (pass != "" && subtle.ConstantTimeCompare([]byte(core.HashSHA256([]byte(pass))), []byte(sh.PasswordHash)) == 1) {
					if sh.FileID == vfile.ID || (sh.IsDir && s.db.IsFileDescendantOfFolder(sh.FileID, vfile.ID)) {
						isShareAuthorized = true
					}
				}
			}
		}

		if !isShareAuthorized {
			settings, _ := s.db.GetSettings()
			guestMode := "view_only"
			if settings != nil && settings.GuestAccessMode != "" {
				guestMode = settings.GuestAccessMode
			}

			isAdminOwned := (vfile.UserID == "user_admin" || vfile.UserID == "")
			otp := r.URL.Query().Get("otp")
			if otp == "" {
				otp = r.Header.Get("X-File-OTP")
			}

			if isAdminOwned {
				if otp != "" {
					valid, err := s.db.VerifyAndBurnOTP(vfile.ID, "user_admin", otp)
					if !valid || err != nil {
						writeError(w, http.StatusForbidden, "Mã OTP mở khóa không hợp lệ hoặc đã hết hạn", err)
						return
					}
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    "user_admin",
						Username:  "admin",
						Action:    "OTP_DOWNLOAD",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Mở khóa tải tệp tin Admin thành công bằng mã OTP",
					})
				} else if guestMode == "strict" {
					if user == nil {
						writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để tải về.", nil)
						return
					}
					writeError(w, http.StatusForbidden, "Tệp tin này yêu cầu Mã OTP do Quản Trị Viên cấp.", nil)
					return
				}
				// Chế độ view_only hoặc open: Cho phép tải xuống tệp tin trong kho lưu trữ
			} else {
				// Tệp tin riêng tư của người dùng khác
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Tệp tin này được bảo mật. Vui lòng đăng nhập để tải về.", nil)
					return
				}
				writeError(w, http.StatusForbidden, "Bạn không có quyền tải tệp tin riêng tư này.", nil)
				return
			}
		}
	}

	if vfile.HasMissingChunks {
		writeError(w, http.StatusGone, "⚠️ Tệp bị thiếu dữ liệu nguồn (chunk đã bị xóa trên Google Drive). Không thể tải về toàn vẹn.", nil)
		return
	}

	streamer, err := s.vfs.NewFileStreamer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi nạp tệp tải xuống: "+err.Error(), err)
		return
	}
	defer streamer.Close()

	mimeType := vfile.MimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = models.ResolveMimeType(vfile.Name)
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("attachment", vfile.Name))
	w.Header().Set("Accept-Ranges", "bytes")

	modTime := streamer.ModTime()
	if modTime.IsZero() {
		modTime = vfile.UpdatedAt
	}
	if modTime.IsZero() {
		modTime = time.Now()
	}

	// Hỗ trợ đầy đủ HTTP Range (cho phép pause/resume quá trình tải xuống qua trình duyệt hoặc IDM)
	http.ServeContent(w, r, vfile.Name, modTime, streamer)
}



func (s *Server) handleDownloadZip(w http.ResponseWriter, r *http.Request) {
	var folderID string
	var idsParam string
	var otp string

	otp = r.URL.Query().Get("otp")
	if otp == "" {
		otp = r.Header.Get("X-File-OTP")
	}

	folderID = strings.TrimSpace(r.URL.Query().Get("folder_id"))
	if folderID == "" {
		folderID = strings.TrimSpace(r.FormValue("folder_id"))
	}

	idsParam = strings.TrimSpace(r.URL.Query().Get("file_ids"))
	if idsParam == "" {
		idsParam = strings.TrimSpace(r.URL.Query().Get("ids"))
	}
	if idsParam == "" {
		idsParam = strings.TrimSpace(r.FormValue("file_ids"))
	}
	if idsParam == "" {
		idsParam = strings.TrimSpace(r.FormValue("ids"))
	}

	// Hỗ trợ POST JSON body
	if r.Method == http.MethodPost && folderID == "" && idsParam == "" && r.Body != nil && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var reqBody struct {
			FolderID string   `json:"folder_id"`
			FileIDs  []string `json:"file_ids"`
			IDs      []string `json:"ids"`
			Name     string   `json:"name"`
			OTP      string   `json:"otp"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
			if folderID == "" {
				folderID = strings.TrimSpace(reqBody.FolderID)
			}
			if len(reqBody.FileIDs) > 0 {
				idsParam = strings.Join(reqBody.FileIDs, ",")
			} else if len(reqBody.IDs) > 0 {
				idsParam = strings.Join(reqBody.IDs, ",")
			}
			if otp == "" && reqBody.OTP != "" {
				otp = strings.TrimSpace(reqBody.OTP)
			}
		}
	}

	if folderID == "" && idsParam == "" {
		writeError(w, http.StatusBadRequest, "Vui lòng cung cấp folder_id hoặc danh sách file_ids cần tải", nil)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	settings, _ := s.db.GetSettings()
	guestMode := "view_only"
	if settings != nil && settings.GuestAccessMode != "" {
		guestMode = settings.GuestAccessMode
	}

	type zipEntry struct {
		file    *models.VirtualFile
		zipPath string
	}

	canAccessFile := func(vfile *models.VirtualFile) bool {
		if vfile == nil || vfile.IsTrashed {
			return false
		}
		isOwner := user != nil && vfile.UserID == user.ID
		if isAdmin || isOwner {
			return true
		}
		if s.db.IsFileOrAncestorShared(vfile.ID) {
			return true
		}

		isAdminOwned := (vfile.UserID == "user_admin" || vfile.UserID == "")
		if isAdminOwned {
			if otp != "" {
				verifyUserID := "guest"
				if user != nil {
					verifyUserID = user.ID
				}
				valid, err := s.db.VerifyAndBurnOTP(vfile.ID, verifyUserID, otp)
				if valid && err == nil {
					_ = s.db.LogActivity(&models.ActivityLog{
						UserID:    verifyUserID,
						Username:  verifyUserID,
						Action:    "OTP_DOWNLOAD",
						Target:    vfile.Name,
						IPAddress: getClientIP(r),
						Details:   "Mở khóa tải tệp tin Admin trong gói Zip thành công bằng mã OTP 1 lần",
					})
					return true
				}
			}
			if guestMode != "strict" {
				return true
			}
		}
		return false
	}

	var validFiles []zipEntry
	var targetArchiveName string

	// 1. Trường hợp tải trọn gói 1 thư mục qua folder_id
	if folderID != "" {
		folder, err := s.db.GetVirtualFile(folderID)
		if err != nil || folder.IsTrashed {
			writeError(w, http.StatusNotFound, "Thư mục không tồn tại", err)
			return
		}
		if !folder.IsDir {
			writeError(w, http.StatusBadRequest, "ID được cung cấp không phải là thư mục", nil)
			return
		}
		if !canAccessFile(folder) {
			writeError(w, http.StatusForbidden, "Bạn không có quyền truy cập thư mục này", nil)
			return
		}

		targetArchiveName = folder.Name
		filesInTree, err := s.db.GetAllFilesInFolderTree(folder.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Lỗi đọc cấu trúc thư mục: "+err.Error(), err)
			return
		}

		for _, item := range filesInTree {
			itemCopy := item.File
			if itemCopy.IsTrashed {
				continue
			}
			if canAccessFile(&itemCopy) {
				relPath := item.RelativePath
				if relPath == "" {
					relPath = itemCopy.Name
				}
				validFiles = append(validFiles, zipEntry{
					file:    &itemCopy,
					zipPath: path.Clean(filepath.ToSlash(relPath)),
				})
			}
		}
	} else {
		// 2. Trường hợp tải theo danh sách file_ids
		rawIDs := strings.Split(idsParam, ",")
		var singleName string
		trimmedCount := 0

		for _, fileID := range rawIDs {
			fileID = strings.TrimSpace(fileID)
			if fileID == "" {
				continue
			}
			trimmedCount++
			vfile, err := s.db.GetVirtualFile(fileID)
			if err != nil || vfile.IsTrashed {
				continue
			}

			if vfile.IsDir {
				if trimmedCount == 1 && len(rawIDs) == 1 {
					singleName = vfile.Name
				}
				if !canAccessFile(vfile) {
					continue
				}
				// Lấy toàn bộ cây thư mục con
				filesInTree, err := s.db.GetAllFilesInFolderTree(vfile.ID)
				if err == nil {
					for _, item := range filesInTree {
						itemCopy := item.File
						if itemCopy.IsTrashed {
							continue
						}
						if canAccessFile(&itemCopy) {
							relPath := item.RelativePath
							if relPath == "" {
								relPath = itemCopy.Name
							}
							entryPath := path.Clean(filepath.ToSlash(path.Join(vfile.Name, relPath)))
							validFiles = append(validFiles, zipEntry{
								file:    &itemCopy,
								zipPath: entryPath,
							})
						}
					}
				}
			} else {
				if trimmedCount == 1 && len(rawIDs) == 1 {
					singleName = strings.TrimSuffix(vfile.Name, path.Ext(vfile.Name))
				}
				if canAccessFile(vfile) {
					validFiles = append(validFiles, zipEntry{
						file:    vfile,
						zipPath: vfile.Name,
					})
				}
			}
		}

		if singleName != "" {
			targetArchiveName = singleName
		}
	}

	if len(validFiles) == 0 {
		writeError(w, http.StatusForbidden, "Không có tệp tin nào hợp lệ hoặc bạn không có quyền tải các mục này", nil)
		return
	}

	zipFilename := r.URL.Query().Get("name")
	if zipFilename == "" {
		if targetArchiveName != "" {
			zipFilename = fmt.Sprintf("%s.zip", targetArchiveName)
		} else {
			zipFilename = fmt.Sprintf("cloudpool_archive_%s.zip", time.Now().Format("20060102_150405"))
		}
	}
	if !strings.HasSuffix(strings.ToLower(zipFilename), ".zip") {
		zipFilename += ".zip"
	}

	// Đảm bảo không trùng lặp đường dẫn tệp trong file zip
	usedPaths := make(map[string]int)
	for i := range validFiles {
		targetZipPath := validFiles[i].zipPath
		if count, exists := usedPaths[targetZipPath]; exists {
			usedPaths[targetZipPath] = count + 1
			ext := path.Ext(targetZipPath)
			base := strings.TrimSuffix(targetZipPath, ext)
			validFiles[i].zipPath = fmt.Sprintf("%s (%d)%s", base, count+1, ext)
		} else {
			usedPaths[targetZipPath] = 0
		}
	}

	// Thiết lập Header HTTP cho Zip Streaming tuân thủ RFC 5987 (Rule PHAN 3.4 & PHAN 3.6)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", formatContentDisposition("attachment", zipFilename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")

	// Sử dụng archive/zip stream trực tiếp ra ResponseWriter dạng On-The-Fly (Zero-Copy)
	// Tuân thủ Rule 1.4: Không tạo bất kỳ file tạm nào trên ổ đĩa
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, entry := range validFiles {
		streamer, err := s.vfs.NewFileStreamer(r.Context(), entry.file.ID)
		if err != nil {
			log.Printf("[ENGINE] [ZIP STREAM] Lỗi nạp luồng tệp %s (%s): %v", entry.file.Name, entry.file.ID, err)
			continue
		}

		modTime := entry.file.UpdatedAt
		if modTime.IsZero() {
			modTime = time.Now()
		}

		header := &zip.FileHeader{
			Name:     entry.zipPath,
			Modified: modTime,
		}

		// Tối ưu CPU: Các định dạng tệp đã nén sẵn/media sử dụng Store (0% CPU),
		// các định dạng văn bản/mã nguồn sử dụng Deflate
		ext := strings.ToLower(path.Ext(entry.file.Name))
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
			log.Printf("[ENGINE] [ZIP STREAM] Lỗi tạo header entry %s: %v", entry.zipPath, err)
			continue
		}

		_, copyErr := io.Copy(zf, streamer)
		streamer.Close()
		if copyErr != nil {
			// Client có thể đã đóng tab hoặc ngắt kết nối tải về, dừng stream
			log.Printf("[ENGINE] [ZIP STREAM] Ngắt luồng tải zip %s: %v", entry.zipPath, copyErr)
			return
		}

		// Đẩy dữ liệu ra mạng ngay lập tức cho client on-the-fly
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	_ = zipWriter.Close()
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}



func (s *Server) handleListTrashFiles(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để xem thùng rác", nil)
		return
	}

	userID := "all"
	if user.Role != "admin" {
		userID = user.ID
	}

	files, err := s.db.ListTrashFiles(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể lấy danh sách thùng rác", err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}



func (s *Server) handleRestoreTrashFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để khôi phục tệp tin", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID cần khôi phục", err)
		return
	}

	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(req.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Bạn không có quyền khôi phục tệp tin của tài khoản khác", nil)
			return
		}
	}

	if err := s.vfs.RestoreFileOrFolder(r.Context(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi khôi phục tệp: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "RESTORE",
		Target:    req.ID,
		IPAddress: getClientIP(r),
		Details:   "Đã khôi phục tệp từ Thùng rác",
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã khôi phục tệp thành công"})
}



func (s *Server) handlePurgeTrashFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để xóa vĩnh viễn tệp tin", nil)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "Thiếu file ID", err)
		return
	}

	if user.Role != "admin" {
		vfile, err := s.db.GetVirtualFile(req.ID)
		if err == nil && vfile != nil && vfile.UserID != "" && vfile.UserID != user.ID {
			writeError(w, http.StatusForbidden, "Bạn không có quyền xóa tệp tin của tài khoản khác", nil)
			return
		}
	}

	if err := s.vfs.PurgeFileOrFolderPermanently(r.Context(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi xóa vĩnh viễn: "+err.Error(), err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Đã xóa vĩnh viễn tệp và giải phóng dung lượng"})
}



func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	user := s.getUserFromRequest(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Vui lòng đăng nhập để dọn sạch thùng rác", nil)
		return
	}

	userID := "all"
	if user.Role != "admin" {
		userID = user.ID
	}

	purged, err := s.vfs.EmptyTrash(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi dọn sạch thùng rác: "+err.Error(), err)
		return
	}

	_ = s.db.LogActivity(&models.ActivityLog{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    "EMPTY_TRASH",
		Target:    "Thùng rác",
		IPAddress: getClientIP(r),
		Details:   fmt.Sprintf("Đã dọn sạch thùng rác (xóa %d tệp)", purged),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"purged_count": purged,
		"message":      fmt.Sprintf("Đã dọn sạch thùng rác thành công (%d tệp được xóa vĩnh viễn)!", purged),
	})
}

// -------------------------------------------------------------
// Public Share Links Handlers
// -------------------------------------------------------------



func (s *Server) handleStorageBreakdown(w http.ResponseWriter, r *http.Request) {
	user := s.getUserFromRequest(r)
	userID := "all"
	if user != nil && user.Role != "admin" {
		userID = user.ID
	}

	resp, err := s.db.GetStorageBreakdown(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể tính toán cơ cấu dung lượng", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// validatePasswordStrength kiểm tra độ mạnh mật khẩu theo chuẩn Production:
// - Tối thiểu 8 ký tự
// - Phải có ít nhất 1 chữ cái (a-z hoặc A-Z)
// - Phải có ít nhất 1 chữ số (0-9)


func (s *Server) handleFileStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" && r.Method == http.MethodPost {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id = req.ID
	}

	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "Thiếu id tệp tin", nil)
		return
	}

	vfile, err := s.db.GetVirtualFile(id)
	if err != nil || vfile == nil {
		writeError(w, http.StatusNotFound, "Tệp tin không tồn tại", err)
		return
	}

	user := s.getUserFromRequest(r)
	isAdmin := user != nil && user.Role == "admin"
	isOwner := user != nil && vfile.UserID == user.ID

	// Kiểm tra quyền truy cập tương tự stream/download
	if !isAdmin && !isOwner {
		if !s.db.IsFileOrAncestorShared(vfile.ID) {
			settings, _ := s.db.GetSettings()
			guestMode := "view_only"
			if settings != nil && settings.GuestAccessMode != "" {
				guestMode = settings.GuestAccessMode
			}
			if vfile.UserID == "user_admin" || vfile.UserID == "" {
				otp := r.URL.Query().Get("otp")
				if otp != "" {
					valid, _ := s.db.VerifyOTPOnly(vfile.ID, "user_admin", otp)
					if !valid {
						writeError(w, http.StatusForbidden, "Mã OTP không hợp lệ", nil)
						return
					}
				} else if guestMode == "strict" {
					writeError(w, http.StatusUnauthorized, "Yêu cầu đăng nhập hoặc mã OTP", nil)
					return
				}
			}
		}
	}

	if vfile.IsDir {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"file_id":            vfile.ID,
			"name":               vfile.Name,
			"is_dir":             true,
			"ready":              true,
			"status":             "ready",
			"has_missing_chunks": false,
			"message":            "Thư mục hợp lệ",
		})
		return
	}

	chunks, err := s.db.GetChunksForFile(vfile.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể đọc dữ liệu chunks của tệp", err)
		return
	}

	totalChunks := len(chunks)
	missingCount := 0
	uploadedCount := 0

	for _, c := range chunks {
		if c.Status == "missing" {
			missingCount++
		} else if c.Status == "uploaded" {
			uploadedCount++
		}
	}

	// Tùy chọn kiểm tra trực tiếp Drive nếu tham số verify_drive=true
	if r.URL.Query().Get("verify_drive") == "true" && s.gd != nil && missingCount == 0 {
		for _, c := range chunks {
			if c.GDriveFileID != "" {
				exists, chkErr := s.gd.CheckChunkExists(r.Context(), c.AccountID, c.GDriveFileID)
				if chkErr == nil && !exists {
					missingCount++
					_ = s.db.UpdateChunkStatus(c.ChunkID, "missing")
					_ = s.db.UpdateFileMissingChunks(vfile.ID, true)
					vfile.HasMissingChunks = true
				}
			}
		}
	}

	hasMissing := vfile.HasMissingChunks || missingCount > 0 || (vfile.ChunkCount > 0 && totalChunks == 0)

	statusStr := "ready"
	msg := "Tệp tin sẵn sàng phát hoặc tải về"
	if hasMissing {
		statusStr = "missing_chunks"
		msg = "⚠️ Tệp bị thiếu dữ liệu nguồn (chunk đã bị xóa trên Google Drive)"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"file_id":            vfile.ID,
		"name":               vfile.Name,
		"size_bytes":         vfile.SizeBytes,
		"mime_type":          vfile.MimeType,
		"ready":              !hasMissing,
		"status":             statusStr,
		"has_missing_chunks": hasMissing,
		"chunk_count":        vfile.ChunkCount,
		"total_chunks":       totalChunks,
		"uploaded_chunks":    uploadedCount,
		"missing_chunks":     missingCount,
		"message":            msg,
		"requires_otp":       vfile.RequiresOTP,
		"is_admin_owned":     vfile.IsAdminOwned,
	})
}

// handleIntegrityCheck thực hiện chẩn đoán & khắc phục tính toàn vẹn dữ liệu Chunks
// Endpoint: POST hoặc GET /api/admin/storage/integrity-check
