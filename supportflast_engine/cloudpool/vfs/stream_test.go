package vfs

import (
	"bytes"
	"container/list"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
)

// TestChunkCacheLRUAndMemoryLimit kiểm tra bộ nhớ đệm LRU tuân thủ nghiêm ngặt giới hạn dung lượng RAM (Rule PHAN 7.1 & 7.2)
func TestChunkCacheLRUAndMemoryLimit(t *testing.T) {
	cache := &ChunkCache{
		entries:    make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: 4,
		maxBytes:   10 * 1024 * 1024, // 10 MB limit for test
	}

	// 1. Thêm 3 blocks 2MB
	b1 := make([]byte, 2*1024*1024)
	b1[0] = 1
	b2 := make([]byte, 2*1024*1024)
	b2[0] = 2
	b3 := make([]byte, 2*1024*1024)
	b3[0] = 3

	cache.Set("block1", b1)
	cache.Set("block2", b2)
	cache.Set("block3", b3)

	if cache.curBytes != 6*1024*1024 {
		t.Fatalf("curBytes mong đợi 6MB, thực tế: %d", cache.curBytes)
	}

	// 2. Thêm block 4 (2MB) và block 5 (3MB) -> tổng dung lượng sẽ vượt 10MB -> phải thu hồi phần tử cũ nhất (LRU eviction)
	b4 := make([]byte, 2*1024*1024)
	b5 := make([]byte, 3*1024*1024)
	cache.Set("block4", b4)
	cache.Set("block5", b5)

	if cache.curBytes > cache.maxBytes {
		t.Fatalf("curBytes %d vượt quá maxBytes %d", cache.curBytes, cache.maxBytes)
	}

	// block1 phải bị evicted đầu tiên do là LRU
	if _, found := cache.Get("block1"); found {
		t.Fatalf("block1 đáng lẽ phải bị evict do LRU!")
	}

	// block5 mới nhất phải còn trong cache
	if _, found := cache.Get("block5"); !found {
		t.Fatalf("block5 phải tồn tại trong cache!")
	}
}

// TestChunkCacheTTL kiểm tra hết hạn TTL của cache entry
func TestChunkCacheTTL(t *testing.T) {
	cache := &ChunkCache{
		entries:    make(map[string]*list.Element),
		lruList:    list.New(),
		maxEntries: 10,
		maxBytes:   50 * 1024 * 1024,
	}

	data := []byte("hello stream cache")
	cache.Set("test_key", data)

	val, found := cache.Get("test_key")
	if !found || string(val) != "hello stream cache" {
		t.Fatalf("Không lấy được dữ liệu vừa cache")
	}

	// Giả lập entry đã hết hạn
	cache.mu.Lock()
	elem := cache.entries["test_key"]
	entry := elem.Value.(*ChunkCacheEntry)
	entry.ExpiresAt = time.Now().Add(-1 * time.Second)
	cache.mu.Unlock()

	// Truy vấn lại sau khi hết hạn -> phải tự động xóa và trả về false
	_, foundAfterExpiry := cache.Get("test_key")
	if foundAfterExpiry {
		t.Fatalf("Entry đã hết hạn nhưng Get vẫn trả về true!")
	}

	if cache.curBytes != 0 {
		t.Fatalf("curBytes sau khi evict expired phải bằng 0, thực tế: %d", cache.curBytes)
	}
}

// TestStreamerChunkMissing404 kiểm tra xử lý triệt để lỗi 404 khi chunk bị thiếu trên Drive
func TestStreamerChunkMissing404(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "streamer_test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Tạo DB test thất bại: %v", err)
	}
	defer db.Close()

	vfs := NewVFS(db, nil)

	// Trường hợp 1: Tệp có SizeBytes > 0 nhưng không có chunk nào trong DB (404)
	vFileMissingChunks := &models.VirtualFile{
		ID:          "file_missing_all_chunks",
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "missing.mp4",
		Path:        "/missing.mp4",
		IsDir:       false,
		SizeBytes:   10 * 1024 * 1024, // 10MB
		MimeType:    "video/mp4",
		Extension:   ".mp4",
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vFileMissingChunks); err != nil {
		t.Fatalf("Lưu virtual file thất bại: %v", err)
	}

	// Khởi tạo Streamer phải trả về lỗi 404 rõ ràng
	_, errStreamer := vfs.NewFileStreamer(context.Background(), vFileMissingChunks.ID)
	if errStreamer == nil {
		t.Fatalf("Kỳ vọng lỗi khi file không có chunk, nhưng nhận được nil!")
	}
	if !strings.Contains(errStreamer.Error(), "404") {
		t.Fatalf("Kỳ vọng lỗi chứa '404', thực tế nhận: %v", errStreamer)
	}
}

// TestStreamerNativeRangeStreaming kiểm tra tối ưu đọc dải byte Range cho tệp native không mã hóa
func TestStreamerNativeRangeStreaming(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "streamer_range_test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Tạo DB test thất bại: %v", err)
	}
	defer db.Close()

	vfs := NewVFS(db, nil)

	// Tạo dữ liệu video mẫu 6MB
	const totalSize int64 = 6 * 1024 * 1024
	testData := make([]byte, totalSize)
	for i := range testData {
		testData[i] = byte(i % 251)
	}

	videoFileID := "file_native_video_test"
	vFile := &models.VirtualFile{
		ID:          videoFileID,
		UserID:      "user_admin",
		ParentID:    "root",
		Name:        "movie.mp4",
		Path:        "/movie.mp4",
		IsDir:       false,
		SizeBytes:   totalSize,
		MimeType:    "video/mp4",
		Extension:   ".mp4",
		ChunkCount:  1,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.SaveVirtualFile(vFile); err != nil {
		t.Fatalf("Lưu virtual file thất bại: %v", err)
	}

	testAcc := &models.Account{
		ID:              "acc_drive_native",
		Email:           "native@example.com",
		Name:            "Drive Native Test",
		Status:          "active",
		TotalQuotaBytes: 100 * 1024 * 1024 * 1024,
		FreeQuotaBytes:  100 * 1024 * 1024 * 1024,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.SaveAccount(testAcc); err != nil {
		t.Fatalf("Lưu account thất bại: %v", err)
	}

	chunk := models.FileChunk{
		ChunkID:            "chk_native_0",
		FileID:             videoFileID,
		ChunkIndex:         0,
		AccountID:          "acc_drive_native",
		GDriveFileID:       "gd_native_file_id_123",
		ChunkSizeBytes:     totalSize,
		EncryptedSizeBytes: 0,
		Status:             "uploaded",
	}
	if err := db.SaveChunks([]models.FileChunk{chunk}); err != nil {
		t.Fatalf("Lưu chunk thất bại: %v", err)
	}

	// Nạp dữ liệu vào cache theo từng khối Range 2MB (mô phỏng Range Download từ Google Drive)
	for start := int64(0); start < totalSize; start += NativeRangeBlockSize {
		end := start + NativeRangeBlockSize
		if end > totalSize {
			end = totalSize
		}
		SetRangeCache(chunk.AccountID, chunk.GDriveFileID, start, testData[start:end])
	}

	streamer, err := vfs.NewFileStreamer(context.Background(), videoFileID)
	if err != nil {
		t.Fatalf("Khởi tạo FileStreamer thất bại: %v", err)
	}
	defer streamer.Close()

	// 1. Kiểm tra Read tuần tự từ đầu
	buf := make([]byte, 1024*1024) // Đọc 1MB đầu
	n, err := streamer.Read(buf)
	if err != nil {
		t.Fatalf("Read thất bại: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("Kỳ vọng đọc %d bytes, thực tế: %d", len(buf), n)
	}
	if !bytes.Equal(buf, testData[:1024*1024]) {
		t.Fatalf("Dữ liệu đọc được không khớp với dữ liệu gốc!")
	}

	// 2. Kiểm tra Seek ngẫu nhiên tới giữa tệp (ví dụ 4.5 MB) và đọc
	seekTarget := int64(4500000)
	newOff, err := streamer.Seek(seekTarget, io.SeekStart)
	if err != nil || newOff != seekTarget {
		t.Fatalf("Seek tới %d thất bại: err=%v, newOff=%d", seekTarget, err, newOff)
	}

	seekBuf := make([]byte, 512*1024) // Đọc 512KB
	nSeek, err := streamer.Read(seekBuf)
	if err != nil {
		t.Fatalf("Read sau Seek thất bại: %v", err)
	}
	if nSeek != len(seekBuf) {
		t.Fatalf("Kỳ vọng đọc %d bytes, thực tế: %d", len(seekBuf), nSeek)
	}
	if !bytes.Equal(seekBuf, testData[seekTarget:seekTarget+int64(nSeek)]) {
		t.Fatalf("Dữ liệu đọc tại vị trí Seek không khớp!")
	}

	// 3. Kiểm tra ReadAt (đọc độc lập đa luồng không ảnh hưởng vị trí Seek hiện tại)
	readAtTarget := int64(2 * 1024 * 1024)
	readAtBuf := make([]byte, 64*1024)
	nAt, err := streamer.ReadAt(readAtBuf, readAtTarget)
	if err != nil {
		t.Fatalf("ReadAt thất bại: %v", err)
	}
	if nAt != len(readAtBuf) {
		t.Fatalf("ReadAt kỳ vọng %d bytes, thực tế %d", len(readAtBuf), nAt)
	}
	if !bytes.Equal(readAtBuf, testData[readAtTarget:readAtTarget+int64(nAt)]) {
		t.Fatalf("Dữ liệu ReadAt không khớp!")
	}
}
