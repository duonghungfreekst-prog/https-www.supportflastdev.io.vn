package vfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
)

func setupTestVFSWithDB(t *testing.T) (*VFS, *storage.DB, func()) {
	t.Helper()
	// Tuân thủ Rule 1.4: Không lưu trên ổ C, lưu trong thư mục data của workspace F:
	testDir := filepath.Join(`f:\supportflast.dev\data`, fmt.Sprintf("test_vfs_mime_%d", time.Now().UnixNano()))
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
	v := NewVFS(db, gd)

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(testDir)
	}

	return v, db, cleanup
}

func TestVFS_MimeTypesClassification(t *testing.T) {
	vfsEngine, db, cleanup := setupTestVFSWithDB(t)
	defer cleanup()

	testFiles := []struct {
		name         string
		expectedMime string
		category     string
		isStreamable bool
	}{
		// 1. Video Formats
		{"trailer.mp4", "video/mp4", "video", true},
		{"presentation.webm", "video/webm", "video", true},
		{"feature.mkv", "video/x-matroska", "video", true},
		{"old_clip.avi", "video/x-msvideo", "video", true},
		{"screencast.mov", "video/quicktime", "video", true},

		// 2. Audio Formats
		{"theme.mp3", "audio/mpeg", "audio", true},
		{"voice.wav", "audio/wav", "audio", true},
		{"podcast.ogg", "audio/ogg", "audio", true},
		{"master.flac", "audio/flac", "audio", true},
		{"track.m4a", "audio/mp4", "audio", true},

		// 3. Image Formats
		{"banner.jpg", "image/jpeg", "image", false},
		{"avatar.png", "image/png", "image", false},
		{"loading.gif", "image/gif", "image", false},
		{"hero.webp", "image/webp", "image", false},
		{"icon.svg", "image/svg+xml", "image", false},

		// 4. Office & Documents
		{"specs.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "office", false},
		{"budget.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "office", false},
		{"pitch.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "office", false},
		{"whitepaper.pdf", "application/pdf", "office", false},
		{"report.csv", "text/csv; charset=utf-8", "office", false},

		// 5. Archive & Compression
		{"source.zip", "application/zip", "archive", false},
		{"backup.rar", "application/vnd.rar", "archive", false},
		{"release.7z", "application/x-7z-compressed", "archive", false},
		{"dist.tar.gz", "application/gzip", "archive", false},
		{"data.tar", "application/x-tar", "archive", false},
	}

	for idx, tf := range testFiles {
		t.Run(tf.category+"_"+tf.name, func(t *testing.T) {
			fileID := fmt.Sprintf("file_test_%d", idx)
			resolvedMime := models.ResolveMimeType(tf.name)
			if resolvedMime != tf.expectedMime {
				t.Errorf("ResolveMimeType(%q) = %q, want %q", tf.name, resolvedMime, tf.expectedMime)
			}

			// Lưu vào VFS DB
			vfile := &models.VirtualFile{
				ID:          fileID,
				UserID:      "user_admin",
				ParentID:    "root",
				Name:        tf.name,
				Path:        "/" + tf.name,
				IsDir:       false,
				SizeBytes:   1024,
				MimeType:    resolvedMime,
				ChunkCount:  1,
				IsEncrypted: false,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}

			if err := db.SaveVirtualFile(vfile); err != nil {
				t.Fatalf("SaveVirtualFile(%q) failed: %v", tf.name, err)
			}

			retrieved, err := db.GetVirtualFile(fileID)
			if err != nil {
				t.Fatalf("GetVirtualFile(%q) failed: %v", fileID, err)
			}

			if retrieved.MimeType != tf.expectedMime {
				t.Errorf("DB MimeType = %q, want %q", retrieved.MimeType, tf.expectedMime)
			}

			if models.IsMediaStreamable(retrieved.MimeType) != tf.isStreamable {
				t.Errorf("IsMediaStreamable(%q) = %v, want %v", retrieved.MimeType, models.IsMediaStreamable(retrieved.MimeType), tf.isStreamable)
			}
		})
	}

	_ = vfsEngine
}

func TestVFS_StreamerContextCancellation(t *testing.T) {
	vfsEngine, db, cleanup := setupTestVFSWithDB(t)
	defer cleanup()

	testAcc := &models.Account{
		ID:              "acc_test",
		Email:           "test@example.com",
		Name:            "Test Account",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.SaveAccount(testAcc); err != nil {
		t.Fatalf("SaveAccount failed: %v", err)
	}

	// Lưu file video test
	vf := &models.VirtualFile{
		ID:          "file_stream_test",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "test_stream.mp4",
		Path:        "/test_stream.mp4",
		IsDir:       false,
		SizeBytes:   2048,
		MimeType:    models.ResolveMimeType("test_stream.mp4"),
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	_ = db.SaveVirtualFile(vf)
	_ = db.SaveChunks([]models.FileChunk{{
		ChunkID:            "chk_stream_test",
		FileID:             vf.ID,
		ChunkIndex:         0,
		AccountID:          "acc_test",
		GDriveFileID:       "gdrive_test",
		ChunkSizeBytes:     2048,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}})

	// Tạo context đã hủy trước
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	streamer, err := vfsEngine.NewFileStreamer(ctx, vf.ID)
	if err != nil {
		t.Fatalf("NewFileStreamer failed: %v", err)
	}
	defer streamer.Close()

	if streamer.MimeType() != "video/mp4" {
		t.Errorf("streamer.MimeType() = %q, want video/mp4", streamer.MimeType())
	}
}
