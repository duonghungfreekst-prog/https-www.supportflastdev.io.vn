package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/registry"
)

func setupMimeTestEnvironment(t *testing.T) (*Server, *storage.DB, string, func()) {
	t.Helper()
	// Tuân thủ Rule 1.4: Không lưu trên ổ C, lưu trong thư mục data của workspace F:
	testDir := filepath.Join(`f:\supportflast.dev\data`, fmt.Sprintf("test_api_mime_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Không thể tạo test directory: %v", err)
	}

	dbPath := filepath.Join(testDir, "test_metadata.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		_ = os.RemoveAll(testDir)
		t.Fatalf("Không thể khởi tạo test DB: %v", err)
	}

	gd := gdrive.NewManager(db)
	vfsEngine := vfs.NewVFS(db, gd)

	if err := InitJWTKeys(testDir); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
		t.Fatalf("InitJWTKeys thất bại: %v", err)
	}

	server := NewServer(db, gd, vfsEngine, 8080, "", testDir)

	// Tạo tài khoản admin test cho JWT
	adminHubUser := registry.User{
		ID:       "user_admin",
		Username: "admin",
		Email:    "admin@supportflastdev.io.vn",
		Role:     "admin",
	}
	adminToken, err := registry.IssueRS256Token(adminHubUser, 24*time.Hour)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
		t.Fatalf("IssueRS256Token thất bại: %v", err)
	}

	// Tạo tài khoản Drive mặc định để file_chunks hợp lệ foreign key
	defaultAcc := &models.Account{
		ID:              "acc_mime_test",
		Email:           "mimetest@example.com",
		Name:            "MIME Test Account",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.SaveAccount(defaultAcc); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
		t.Fatalf("SaveAccount thất bại: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
	}

	return server, db, adminToken, cleanup
}

func createTestVirtualFileWithChunk(t *testing.T, db *storage.DB, fileID, fileName string, size int64, content []byte) *models.VirtualFile {
	t.Helper()
	mimeType := models.ResolveMimeType(fileName)
	vf := &models.VirtualFile{
		ID:          fileID,
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        fileName,
		Path:        "/" + fileName,
		IsDir:       false,
		SizeBytes:   size,
		MimeType:    mimeType,
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vf); err != nil {
		t.Fatalf("SaveVirtualFile(%q) thất bại: %v", fileName, err)
	}

	chunkID := "chk_" + fileID
	gdriveID := "gd_" + fileID
	chunk := models.FileChunk{
		ChunkID:            chunkID,
		FileID:             fileID,
		ChunkIndex:         0,
		AccountID:          "acc_mime_test",
		GDriveFileID:       gdriveID,
		ChunkSizeBytes:     size,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk}); err != nil {
		t.Fatalf("SaveChunks(%q) thất bại: %v", chunkID, err)
	}

	// Đưa nội dung vào Range Cache để streamer đọc tức thì không cần Drive API
	vfs.SetRangeCache("acc_mime_test", gdriveID, 0, content)

	return vf
}

func TestRouter_StreamFile_DiverseMimeTypes(t *testing.T) {
	server, db, adminToken, cleanup := setupMimeTestEnvironment(t)
	defer cleanup()

	testMimes := []struct {
		fileName     string
		expectedMime string
		category     string
		isMedia      bool
	}{
		// 1. Video
		{"intro_clip.mp4", "video/mp4", "video", true},
		{"screencast.webm", "video/webm", "video", true},
		{"movie_sample.mkv", "video/x-matroska", "video", true},
		{"archive_tape.avi", "video/x-msvideo", "video", true},

		// 2. Audio
		{"theme_song.mp3", "audio/mpeg", "audio", true},
		{"instrumental.wav", "audio/wav", "audio", true},
		{"album_track.flac", "audio/flac", "audio", true},
		{"voice_note.ogg", "audio/ogg", "audio", true},

		// 3. Image
		{"scenery.jpg", "image/jpeg", "image", false},
		{"diagram_art.png", "image/png", "image", false},
		{"reaction.gif", "image/gif", "image", false},
		{"modern_asset.webp", "image/webp", "image", false},
		{"vector_logo.svg", "image/svg+xml", "image", false},

		// 4. Office
		{"document.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "office", false},
		{"financial.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "office", false},
		{"slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "office", false},
		{"handbook.pdf", "application/pdf", "office", false},
		{"export.csv", "text/csv; charset=utf-8", "office", false},

		// 5. Archive
		{"project_backup.zip", "application/zip", "archive", false},
		{"compressed_data.rar", "application/vnd.rar", "archive", false},
		{"archive_store.7z", "application/x-7z-compressed", "archive", false},
		{"tarball_pack.tar.gz", "application/gzip", "archive", false},
	}

	for idx, tm := range testMimes {
		t.Run(tm.category+"_"+tm.fileName, func(t *testing.T) {
			fileID := fmt.Sprintf("stream_file_%d", idx)
			sampleBytes := []byte(fmt.Sprintf("SAMPLE_DATA_FOR_%s_%d", tm.fileName, idx))
			createTestVirtualFileWithChunk(t, db, fileID, tm.fileName, int64(len(sampleBytes)), sampleBytes)

			req := httptest.NewRequest("GET", "/api/files/stream?id="+fileID, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()

			server.handleStreamFile(rec, req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
				t.Fatalf("handleStreamFile returned status %d, body: %s", rec.Code, rec.Body.String())
			}

			// Kiểm tra Content-Type
			contentType := rec.Header().Get("Content-Type")
			if contentType != tm.expectedMime {
				t.Errorf("File %s: Content-Type = %q, expected %q", tm.fileName, contentType, tm.expectedMime)
			}

			// Kiểm tra Accept-Ranges
			if rec.Header().Get("Accept-Ranges") != "bytes" {
				t.Errorf("File %s: Accept-Ranges header missing or not 'bytes'", tm.fileName)
			}

			// Kiểm tra Cache-Control theo nhóm media / tài liệu
			cacheControl := rec.Header().Get("Cache-Control")
			if tm.isMedia {
				if !strings.Contains(cacheControl, "public") || !strings.Contains(cacheControl, "max-age=3600") {
					t.Errorf("Media file %s: Cache-Control expected public max-age=3600, got %q", tm.fileName, cacheControl)
				}
			} else {
				if !strings.Contains(cacheControl, "no-cache") {
					t.Errorf("Non-media file %s: Cache-Control expected no-cache, got %q", tm.fileName, cacheControl)
				}
			}

			// Kiểm tra Content-Disposition: inline
			contentDisp := rec.Header().Get("Content-Disposition")
			if !strings.HasPrefix(contentDisp, "inline") {
				t.Errorf("File %s: Content-Disposition expected inline prefix, got %q", tm.fileName, contentDisp)
			}
		})
	}
}

func TestRouter_DownloadFile_DiverseMimeTypes(t *testing.T) {
	server, db, adminToken, cleanup := setupMimeTestEnvironment(t)
	defer cleanup()

	testMimes := []struct {
		fileName     string
		expectedMime string
		category     string
	}{
		{"trailer.mp4", "video/mp4", "video"},
		{"song.mp3", "audio/mpeg", "audio"},
		{"banner.png", "image/png", "image"},
		{"contract.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "office"},
		{"export.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "office"},
		{"ebook.pdf", "application/pdf", "office"},
		{"source.zip", "application/zip", "archive"},
	}

	for idx, tm := range testMimes {
		t.Run("download_"+tm.category+"_"+tm.fileName, func(t *testing.T) {
			fileID := fmt.Sprintf("dl_file_%d", idx)
			sampleBytes := []byte("DOWNLOAD_CONTENT_FOR_" + tm.fileName)
			createTestVirtualFileWithChunk(t, db, fileID, tm.fileName, int64(len(sampleBytes)), sampleBytes)

			req := httptest.NewRequest("GET", "/api/files/download?id="+fileID, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()

			server.handleDownloadFile(rec, req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
				t.Fatalf("handleDownloadFile returned status %d, body: %s", rec.Code, rec.Body.String())
			}

			contentType := rec.Header().Get("Content-Type")
			if contentType != tm.expectedMime {
				t.Errorf("File %s: Content-Type = %q, expected %q", tm.fileName, contentType, tm.expectedMime)
			}

			contentDisp := rec.Header().Get("Content-Disposition")
			if !strings.HasPrefix(contentDisp, "attachment") {
				t.Errorf("File %s: Content-Disposition expected attachment prefix, got %q", tm.fileName, contentDisp)
			}
		})
	}
}

func TestRouter_PublicShareStreamAndDownload_DiverseMimeTypes(t *testing.T) {
	server, db, _, cleanup := setupMimeTestEnvironment(t)
	defer cleanup()

	testMimes := []struct {
		fileName     string
		expectedMime string
		category     string
	}{
		{"share_video.mp4", "video/mp4", "video"},
		{"share_audio.mp3", "audio/mpeg", "audio"},
		{"share_pic.jpeg", "image/jpeg", "image"},
		{"share_sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "office"},
		{"share_archive.zip", "application/zip", "archive"},
	}

	for idx, tm := range testMimes {
		t.Run("public_share_"+tm.category+"_"+tm.fileName, func(t *testing.T) {
			fileID := fmt.Sprintf("share_file_%d", idx)
			token := fmt.Sprintf("token_share_%d", idx)
			sampleBytes := []byte("PUBLIC_SHARE_STREAM_" + tm.fileName)
			createTestVirtualFileWithChunk(t, db, fileID, tm.fileName, int64(len(sampleBytes)), sampleBytes)

			// Tạo public share trong DB
			share := &models.PublicShare{
				ID:          token,
				FileID:      fileID,
				FileName:    tm.fileName,
				FileSize:    int64(len(sampleBytes)),
				MimeType:    tm.expectedMime,
				IsDir:       false,
				HasPassword: false,
				IsActive:    true,
				CreatedAt:   time.Now(),
			}
			if err := db.CreatePublicShare(share); err != nil {
				t.Fatalf("CreatePublicShare failed: %v", err)
			}

			// 1. Test Stream Share (/api/shares/public/stream?token=...)
			reqStream := httptest.NewRequest("GET", "/api/shares/public/stream?token="+token, nil)
			recStream := httptest.NewRecorder()
			server.handlePublicShareStream(recStream, reqStream)

			if recStream.Code != http.StatusOK && recStream.Code != http.StatusPartialContent {
				t.Fatalf("handlePublicShareStream returned status %d, body: %s", recStream.Code, recStream.Body.String())
			}

			streamContentType := recStream.Header().Get("Content-Type")
			if streamContentType != tm.expectedMime {
				t.Errorf("Public Stream %s: Content-Type = %q, expected %q", tm.fileName, streamContentType, tm.expectedMime)
			}

			if recStream.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
				t.Errorf("Public Stream %s: X-Frame-Options expected SAMEORIGIN", tm.fileName)
			}

			// 2. Test Download Share (/api/shares/public/download?token=...)
			reqDl := httptest.NewRequest("GET", "/api/shares/public/download?token="+token, nil)
			recDl := httptest.NewRecorder()
			server.handlePublicShareDownload(recDl, reqDl)

			if recDl.Code != http.StatusOK && recDl.Code != http.StatusPartialContent {
				t.Fatalf("handlePublicShareDownload returned status %d, body: %s", recDl.Code, recDl.Body.String())
			}

			dlContentType := recDl.Header().Get("Content-Type")
			if dlContentType != tm.expectedMime {
				t.Errorf("Public Download %s: Content-Type = %q, expected %q", tm.fileName, dlContentType, tm.expectedMime)
			}
		})
	}
}
