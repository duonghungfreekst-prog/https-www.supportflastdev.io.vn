package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"supportflast_engine/cloudpool/webdav"
	"testing"
	"time"
)

func setupTestWebDAVEnvironment(t *testing.T) (*storage.DB, *vfs.VFS, http.Handler, func()) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_cloudpool.db")

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Khởi tạo test DB thất bại: %v", err)
	}

	// Cấu hình settings chuẩn
	settings, err := db.GetSettings()
	if err != nil {
		t.Fatalf("Không lấy được settings: %v", err)
	}
	settings.WebDAVEnabled = true
	settings.WebDAVUsername = "admin"
	settings.WebDAVPassword = "admin123"
	settings.ChunkSizeBytes = 20 * 1024 * 1024 // 20 MB
	if err := db.SaveSettings(settings); err != nil {
		t.Fatalf("Cập nhật settings thất bại: %v", err)
	}

	// Đảm bảo có Account hợp lệ để thỏa mãn FOREIGN KEY constraint của file_chunks
	defaultAcc := &models.Account{
		ID:              "test_account_default",
		Email:           "admin_storage@supportflastdev.io.vn",
		Name:            "Default CloudPool Account",
		AuthType:        "service_account",
		Status:          "active",
		HealthStatus:    "healthy",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
	}
	if err := db.SaveAccount(defaultAcc); err != nil {
		t.Fatalf("Lưu account mặc định thất bại: %v", err)
	}

	gdManager := gdrive.NewManager(db)
	vfsEngine := vfs.NewVFS(db, gdManager)

	// Tạo native WebDAV handler
	davHandler := webdav.CreateWebDAVHandler(vfsEngine, db)

	// Tạo HTTP Mux tích hợp native giống main.go
	mux := http.NewServeMux()
	mux.Handle("/webdav", davHandler)
	mux.Handle("/webdav/", davHandler)

	cleanup := func() {
		db.Close()
		_ = os.RemoveAll(tempDir)
	}

	return db, vfsEngine, mux, cleanup
}

func TestWebDAV_BasicAuth(t *testing.T) {
	_, _, mux, cleanup := setupTestWebDAVEnvironment(t)
	defer cleanup()

	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. Gửi request không có Auth -> mong đợi 401 Unauthorized
	req, _ := http.NewRequest("PROPFIND", server.URL+"/webdav/", nil)
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Gửi request thất bại: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Mong đợi HTTP 401 Unauthorized, nhận được: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Errorf("Header WWW-Authenticate không hợp lệ: %s", resp.Header.Get("WWW-Authenticate"))
	}

	// 2. Gửi request sai Auth -> mong đợi 401 Unauthorized
	reqWrong, _ := http.NewRequest("PROPFIND", server.URL+"/webdav/", nil)
	reqWrong.SetBasicAuth("admin", "wrong_password")
	reqWrong.Header.Set("Depth", "1")
	respWrong, err := http.DefaultClient.Do(reqWrong)
	if err != nil {
		t.Fatalf("Gửi request sai auth thất bại: %v", err)
	}
	defer respWrong.Body.Close()

	if respWrong.StatusCode != http.StatusUnauthorized {
		t.Errorf("Mong đợi HTTP 401 cho sai password, nhận được: %d", respWrong.StatusCode)
	}

	// 3. Gửi request đúng Auth (admin:admin123) -> mong đợi 207 Multi-Status
	reqOK, _ := http.NewRequest("PROPFIND", server.URL+"/webdav/", nil)
	reqOK.SetBasicAuth("admin", "admin123")
	reqOK.Header.Set("Depth", "1")
	respOK, err := http.DefaultClient.Do(reqOK)
	if err != nil {
		t.Fatalf("Gửi request đúng auth thất bại: %v", err)
	}
	defer respOK.Body.Close()

	if respOK.StatusCode != http.StatusMultiStatus {
		t.Errorf("Mong đợi HTTP 207 Multi-Status, nhận được: %d", respOK.StatusCode)
	}
}

func TestWebDAV_VideoStreamingAndRange(t *testing.T) {
	db, _, mux, cleanup := setupTestWebDAVEnvironment(t)
	defer cleanup()

	// Chuẩn bị dữ liệu video mẫu kích thước 20MB (20 * 1024 * 1024 bytes)
	videoSize := int64(20 * 1024 * 1024)
	videoData := make([]byte, videoSize)
	for i := range videoData {
		videoData[i] = byte((i * 31) % 256)
	}
	videoHash := sha256.Sum256(videoData)
	videoHashHex := hex.EncodeToString(videoHash[:])

	// Tạo virtual file video trong DB
	videoFileID := "file_test_video_20mb"
	videoFileName := "video_test_20mb.webm"
	vFile := &models.VirtualFile{
		ID:          videoFileID,
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        videoFileName,
		Path:        "/" + videoFileName,
		IsDir:       false,
		SizeBytes:   videoSize,
		MimeType:    "video/webm",
		Extension:   ".webm",
		SHA256:      videoHashHex,
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vFile); err != nil {
		t.Fatalf("Lưu virtual file thất bại: %v", err)
	}

	// Tạo chunk metadata và nạp vào in-memory RAM cache
	chunkAccountID := "test_account_default"
	chunkGDriveID := "gd_video_chunk_001"
	chunk := models.FileChunk{
		ChunkID:            "chk_video_0",
		FileID:             videoFileID,
		ChunkIndex:         0,
		AccountID:          chunkAccountID,
		GDriveFileID:       chunkGDriveID,
		ChunkSizeBytes:     videoSize,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk}); err != nil {
		t.Fatalf("Lưu chunk thất bại: %v", err)
	}

	// Nạp dữ liệu vào cache để stream trực tiếp không qua proxy
	vfs.SetChunkCache(chunkAccountID, chunkGDriveID, videoData)

	server := httptest.NewServer(mux)
	defer server.Close()

	// --- BƯỚC 1: Streaming video từ đầu (0 - 1MB) - Mô phỏng trình duyệt nạp metadata/header video ---
	reqRange1, _ := http.NewRequest("GET", server.URL+"/webdav/"+videoFileName, nil)
	reqRange1.SetBasicAuth("admin", "admin123")
	reqRange1.Header.Set("Range", "bytes=0-1048575") // 1 MB đầu
	respRange1, err := http.DefaultClient.Do(reqRange1)
	if err != nil {
		t.Fatalf("Stream video Range 1 thất bại: %v", err)
	}
	defer respRange1.Body.Close()

	if respRange1.StatusCode != http.StatusPartialContent {
		t.Errorf("Mong đợi HTTP 206 Partial Content, nhận được: %d", respRange1.StatusCode)
	}
	if respRange1.Header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("Mong đợi Accept-Ranges: bytes, nhận được: %s", respRange1.Header.Get("Accept-Ranges"))
	}
	expectedContentRange1 := fmt.Sprintf("bytes 0-1048575/%d", videoSize)
	if respRange1.Header.Get("Content-Range") != expectedContentRange1 {
		t.Errorf("Content-Range không đúng. Mong đợi '%s', nhận được '%s'", expectedContentRange1, respRange1.Header.Get("Content-Range"))
	}

	receivedChunk1, err := io.ReadAll(respRange1.Body)
	if err != nil {
		t.Fatalf("Lỗi đọc body stream 1: %v", err)
	}
	if len(receivedChunk1) != 1048576 {
		t.Errorf("Kích thước chunk 1 không đúng: mong đợi 1048576, nhận được %d", len(receivedChunk1))
	}
	if !bytes.Equal(receivedChunk1, videoData[0:1048576]) {
		t.Errorf("Nội dung stream 1 không khớp với dữ liệu video gốc!")
	}
	t.Logf("✅ Stream video 1MB đầu thành công: HTTP 206, Content-Range=%s, Size=%d bytes",
		respRange1.Header.Get("Content-Range"), len(receivedChunk1))

	// --- BƯỚC 2: Video Player Seek đến giữa video (10MB -> 15MB) ---
	seekStart := int64(10 * 1024 * 1024)
	seekEnd := int64(15*1024*1024 - 1)
	reqSeek, _ := http.NewRequest("GET", server.URL+"/webdav/"+videoFileName, nil)
	reqSeek.SetBasicAuth("admin", "admin123")
	reqSeek.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seekStart, seekEnd))
	respSeek, err := http.DefaultClient.Do(reqSeek)
	if err != nil {
		t.Fatalf("Seek video Range thất bại: %v", err)
	}
	defer respSeek.Body.Close()

	if respSeek.StatusCode != http.StatusPartialContent {
		t.Errorf("Mong đợi HTTP 206 cho Video Seek, nhận được: %d", respSeek.StatusCode)
	}
	expectedSeekRange := fmt.Sprintf("bytes %d-%d/%d", seekStart, seekEnd, videoSize)
	if respSeek.Header.Get("Content-Range") != expectedSeekRange {
		t.Errorf("Content-Range Video Seek không đúng: mong đợi '%s', nhận: '%s'", expectedSeekRange, respSeek.Header.Get("Content-Range"))
	}

	seekData, err := io.ReadAll(respSeek.Body)
	if err != nil {
		t.Fatalf("Lỗi đọc body seek: %v", err)
	}
	expectedSeekLen := int(seekEnd - seekStart + 1)
	if len(seekData) != expectedSeekLen {
		t.Errorf("Kích thước seekData không đúng: mong đợi %d, nhận được %d", expectedSeekLen, len(seekData))
	}
	if !bytes.Equal(seekData, videoData[seekStart:seekEnd+1]) {
		t.Errorf("Dữ liệu video seek không khớp với dữ liệu gốc!")
	}
	t.Logf("✅ Video Seek tới giữa file (10MB-15MB) thành công: HTTP 206, Content-Range=%s, Size=%d bytes",
		respSeek.Header.Get("Content-Range"), len(seekData))
}

func TestWebDAV_AudioStreamingAndSeeking(t *testing.T) {
	db, _, mux, cleanup := setupTestWebDAVEnvironment(t)
	defer cleanup()

	// Chuẩn bị file audio mẫu 5MB
	audioSize := int64(5 * 1024 * 1024)
	audioData := make([]byte, audioSize)
	for i := range audioData {
		audioData[i] = byte((i * 17) % 256)
	}

	audioFileID := "file_test_audio_5mb"
	audioFileName := "track_audio_test.mp3"
	vFile := &models.VirtualFile{
		ID:          audioFileID,
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        audioFileName,
		Path:        "/" + audioFileName,
		IsDir:       false,
		SizeBytes:   audioSize,
		MimeType:    "audio/mpeg",
		Extension:   ".mp3",
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vFile); err != nil {
		t.Fatalf("Lưu virtual audio file thất bại: %v", err)
	}

	chunkAccountID := "test_account_default"
	chunkGDriveID := "gd_audio_chunk_001"
	chunk := models.FileChunk{
		ChunkID:            "chk_audio_0",
		FileID:             audioFileID,
		ChunkIndex:         0,
		AccountID:          chunkAccountID,
		GDriveFileID:       chunkGDriveID,
		ChunkSizeBytes:     audioSize,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk}); err != nil {
		t.Fatalf("Lưu audio chunk thất bại: %v", err)
	}

	vfs.SetChunkCache(chunkAccountID, chunkGDriveID, audioData)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Request phát audio đoạn đầu (0 - 512KB)
	reqAudio, _ := http.NewRequest("GET", server.URL+"/webdav/"+audioFileName, nil)
	reqAudio.SetBasicAuth("admin", "admin123")
	reqAudio.Header.Set("Range", "bytes=0-524287")
	respAudio, err := http.DefaultClient.Do(reqAudio)
	if err != nil {
		t.Fatalf("Stream audio thất bại: %v", err)
	}
	defer respAudio.Body.Close()

	if respAudio.StatusCode != http.StatusPartialContent {
		t.Errorf("Mong đợi HTTP 206 cho audio stream, nhận: %d", respAudio.StatusCode)
	}
	dataAudio, _ := io.ReadAll(respAudio.Body)
	if len(dataAudio) != 524288 {
		t.Errorf("Kích thước audio chunk không đúng: mong đợi 524288, nhận %d", len(dataAudio))
	}
	if !bytes.Equal(dataAudio, audioData[0:524288]) {
		t.Errorf("Dữ liệu audio stream không khớp dữ liệu gốc!")
	}
	t.Logf("✅ Stream audio thành công: HTTP 206, Content-Range=%s, Size=%d bytes",
		respAudio.Header.Get("Content-Range"), len(dataAudio))

	// Request seek tới cuối audio file (4.5MB - 5MB)
	reqAudioTail, _ := http.NewRequest("GET", server.URL+"/webdav/"+audioFileName, nil)
	reqAudioTail.SetBasicAuth("admin", "admin123")
	reqAudioTail.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", 4500000, audioSize-1))
	respAudioTail, err := http.DefaultClient.Do(reqAudioTail)
	if err != nil {
		t.Fatalf("Stream audio tail thất bại: %v", err)
	}
	defer respAudioTail.Body.Close()

	if respAudioTail.StatusCode != http.StatusPartialContent {
		t.Errorf("Mong đợi HTTP 206 cho audio seek cuối file, nhận: %d", respAudioTail.StatusCode)
	}
	dataTail, _ := io.ReadAll(respAudioTail.Body)
	expectedTailLen := int(audioSize - 4500000)
	if len(dataTail) != expectedTailLen {
		t.Errorf("Kích thước audio tail không đúng: mong đợi %d, nhận %d", expectedTailLen, len(dataTail))
	}
	if !bytes.Equal(dataTail, audioData[4500000:]) {
		t.Errorf("Dữ liệu audio seek cuối file không khớp!")
	}
	t.Logf("✅ Seek audio cuối file thành công: HTTP 206, Content-Range=%s, Size=%d bytes",
		respAudioTail.Header.Get("Content-Range"), len(dataTail))
}

func TestWebDAV_FullChunk20MBLoadAndIntegrity(t *testing.T) {
	db, _, mux, cleanup := setupTestWebDAVEnvironment(t)
	defer cleanup()

	// Chunk chuẩn 20MB (20971520 bytes)
	chunkSize20MB := int64(20 * 1024 * 1024)
	chunkData := make([]byte, chunkSize20MB)
	for i := range chunkData {
		chunkData[i] = byte((i * 47) % 256)
	}
	expectedHasher := sha256.New()
	expectedHasher.Write(chunkData)
	expectedHash := hex.EncodeToString(expectedHasher.Sum(nil))

	fileID := "file_chunk_20mb_benchmark"
	fileName := "benchmark_chunk_20mb.bin"
	vFile := &models.VirtualFile{
		ID:          fileID,
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        fileName,
		Path:        "/" + fileName,
		IsDir:       false,
		SizeBytes:   chunkSize20MB,
		MimeType:    "application/octet-stream",
		SHA256:      expectedHash,
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vFile); err != nil {
		t.Fatalf("Lưu virtual file thất bại: %v", err)
	}

	accountID := "test_account_default"
	gdriveID := "gd_chunk_20mb_001"
	chunk := models.FileChunk{
		ChunkID:            "chk_bench_0",
		FileID:             fileID,
		ChunkIndex:         0,
		AccountID:          accountID,
		GDriveFileID:       gdriveID,
		ChunkSizeBytes:     chunkSize20MB,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk}); err != nil {
		t.Fatalf("Lưu chunk thất bại: %v", err)
	}

	vfs.SetChunkCache(accountID, gdriveID, chunkData)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Tải toàn bộ file chunk 20MB qua WebDAV GET native cổng 8080 (không qua proxy)
	reqFull, _ := http.NewRequest("GET", server.URL+"/webdav/"+fileName, nil)
	reqFull.SetBasicAuth("admin", "admin123")
	startDownload := time.Now()
	respFull, err := http.DefaultClient.Do(reqFull)
	if err != nil {
		t.Fatalf("Download chunk 20MB thất bại: %v", err)
	}
	defer respFull.Body.Close()

	if respFull.StatusCode != http.StatusOK {
		t.Errorf("Mong đợi HTTP 200 OK cho tải toàn bộ file, nhận được: %d", respFull.StatusCode)
	}

	// Đọc và băm đồng thời để kiểm tra tốc độ streaming và toàn vẹn dữ liệu
	hasher := sha256.New()
	totalCopied, err := io.Copy(hasher, respFull.Body)
	duration := time.Since(startDownload)
	if err != nil {
		t.Fatalf("Lỗi đọc stream chunk 20MB: %v", err)
	}

	if totalCopied != chunkSize20MB {
		t.Errorf("Tổng số byte tải về không khớp: mong đợi %d, nhận được %d", chunkSize20MB, totalCopied)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		t.Fatalf("Sai lệch SHA256 checksum! Dữ liệu bị biến đổi.\nMong đợi: %s\nNhận được: %s", expectedHash, actualHash)
	}

	throughputMBps := float64(totalCopied) / 1024 / 1024 / duration.Seconds()
	t.Logf("✅ Tải toàn vẹn chunk 20MB thành công trong %v (Tốc độ: %.2f MB/s)!", duration, throughputMBps)
	t.Logf("   Checksum SHA-256: %s (Trùng khớp 100%%)", actualHash)
}
