package vfs

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/api/googleapi"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
)

const (
	// NativeRangeBlockSize kích thước khối tải dải byte (4MB) cho các tệp native Google Drive không mã hóa.
	// 4MB giúp tăng gấp đôi lượng đệm video/audio, giảm 50% số lượng HTTP range request tới Google Drive,
	// giúp tua và phát mượt mà không bị khựng, đồng thời vẫn kiểm soát nghiêm ngặt dung lượng RAM (Rule PHAN 7.1).
	NativeRangeBlockSize int64 = 4 * 1024 * 1024 // 4 MB
)

// ChunkCacheEntry holds decrypted chunk bytes or range block bytes in memory with LRU tracking
type ChunkCacheEntry struct {
	Key          string
	Data         []byte
	ExpiresAt    time.Time
	LastAccessed time.Time
}

// ChunkCache implements a thread-safe LRU memory buffer with strict byte limit (Rule PHAN 7.1 & 7.2)
type ChunkCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lruList    *list.List
	maxEntries int
	maxBytes   int64
	curBytes   int64
}

var globalChunkCache = &ChunkCache{
	entries:    make(map[string]*list.Element),
	lruList:    list.New(),
	maxEntries: 256,               // Tối đa 256 entries trong bộ nhớ đệm
	maxBytes:   256 * 1024 * 1024, // 256MB giới hạn trần RAM nghiêm ngặt (an toàn dưới ngưỡng 500MB trong Rule PHAN 7.1)
}

func (c *ChunkCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, found := c.entries[key]
	if !found {
		return nil, false
	}

	entry := elem.Value.(*ChunkCacheEntry)
	now := time.Now()
	if now.After(entry.ExpiresAt) {
		c.lruList.Remove(elem)
		delete(c.entries, key)
		c.curBytes -= int64(len(entry.Data))
		entry.Data = nil
		return nil, false
	}

	entry.LastAccessed = now
	c.lruList.MoveToFront(elem)
	return entry.Data, true
}

func (c *ChunkCache) Set(key string, data []byte) {
	if len(data) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	dataLen := int64(len(data))

	// Tránh nạp khối dữ liệu đơn lẻ vượt quá trần dung lượng cache vào RAM
	if dataLen > c.maxBytes {
		return
	}

	// Nếu đã tồn tại key, cập nhật dữ liệu và đưa lên đầu danh sách LRU
	if elem, found := c.entries[key]; found {
		entry := elem.Value.(*ChunkCacheEntry)
		c.curBytes -= int64(len(entry.Data))
		entry.Data = data
		c.curBytes += dataLen
		entry.ExpiresAt = now.Add(5 * time.Minute) // 5 minute TTL (Rule PHAN 7.2)
		entry.LastAccessed = now
		c.lruList.MoveToFront(elem)
		c.evictOldestLocked()
		return
	}

	// Thu hồi các phần tử cũ nhất (LRU eviction) khi vượt quá số lượng hoặc dung lượng tối đa
	for (len(c.entries) >= c.maxEntries || (c.curBytes+dataLen > c.maxBytes && c.curBytes > 0)) && c.lruList.Len() > 0 {
		oldest := c.lruList.Back()
		if oldest == nil {
			break
		}
		c.lruList.Remove(oldest)
		oldEntry := oldest.Value.(*ChunkCacheEntry)
		delete(c.entries, oldEntry.Key)
		c.curBytes -= int64(len(oldEntry.Data))
		oldEntry.Data = nil
	}

	newEntry := &ChunkCacheEntry{
		Key:          key,
		Data:         data,
		ExpiresAt:    now.Add(5 * time.Minute),
		LastAccessed: now,
	}
	elem := c.lruList.PushFront(newEntry)
	c.entries[key] = elem
	c.curBytes += dataLen
}

func (c *ChunkCache) evictOldestLocked() {
	for (len(c.entries) > c.maxEntries || c.curBytes > c.maxBytes) && c.lruList.Len() > 0 {
		oldest := c.lruList.Back()
		if oldest == nil {
			break
		}
		c.lruList.Remove(oldest)
		oldEntry := oldest.Value.(*ChunkCacheEntry)
		delete(c.entries, oldEntry.Key)
		c.curBytes -= int64(len(oldEntry.Data))
		oldEntry.Data = nil
	}
}

// Clear làm rỗng toàn bộ bộ đệm RAM (hỗ trợ kiểm thử và giải phóng tức thì)
func (c *ChunkCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, elem := range c.entries {
		entry := elem.Value.(*ChunkCacheEntry)
		entry.Data = nil
	}
	c.entries = make(map[string]*list.Element)
	c.lruList = list.New()
	c.curBytes = 0
}

// SetChunkCache allows pre-caching decrypted or raw chunk bytes in memory (Rule PHAN 7.2)
func SetChunkCache(accountID, gdriveFileID string, data []byte) {
	cacheKey := fmt.Sprintf("chunk_%s_%s", accountID, gdriveFileID)
	globalChunkCache.Set(cacheKey, data)
}

// SetRangeCache allows pre-caching a byte range block in memory
func SetRangeCache(accountID, gdriveFileID string, start int64, data []byte) {
	cacheKey := fmt.Sprintf("range_%s_%s_%d", accountID, gdriveFileID, start)
	globalChunkCache.Set(cacheKey, data)
}

// is404 kiểm tra xem lỗi trả về từ Google Drive có phải do chunk/file bị 404 (không tồn tại) hay không
func is404(err error) bool {
	if err == nil {
		return false
	}
	var gErr *googleapi.Error
	if errors.As(err, &gErr) && gErr.Code == 404 {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "404") ||
		strings.Contains(s, "notfound") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "chunk missing on drive")
}

// FileStreamer implements io.ReadSeeker, io.ReaderAt, and io.Closer over virtual distributed chunks
type FileStreamer struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	vfs                 *VFS
	file                *models.VirtualFile
	chunks              []models.FileChunk
	encKey              [32]byte
	offset              int64
	chunkSize           int64
	mu                  sync.Mutex
	lastPrefetchedIdx   atomic.Int32
	lastPrefetchedBlock atomic.Int64
}

// NewFileStreamer creates a seeker-capable stream reader for a virtual file
func (v *VFS) NewFileStreamer(ctx context.Context, fileID string) (*FileStreamer, error) {
	vFile, err := v.db.GetVirtualFile(fileID)
	if err != nil {
		return nil, err
	}
	if vFile.IsDir {
		return nil, errors.New("cannot stream a directory")
	}

	chunks, err := v.db.GetChunksForFile(fileID)
	if err != nil {
		return nil, err
	}

	// Xử lý triệt để: Nếu tệp có dung lượng nhưng không có chunk nào trong DB (404 chunk missing)
	if len(chunks) == 0 && vFile.SizeBytes > 0 {
		fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s has size %d but 0 chunks recorded)\n", fileID, vFile.SizeBytes)
		return nil, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: file %s has no chunks recorded", fileID)
	}

	settings, err := v.db.GetSettings()
	if err != nil {
		return nil, err
	}

	encKey := core.DeriveKey(settings.MasterPassphrase, nil)

	var chunkSize int64 = 20 * 1024 * 1024
	if len(chunks) > 0 && chunks[0].ChunkSizeBytes > 0 {
		chunkSize = chunks[0].ChunkSizeBytes
	} else if len(chunks) == 1 {
		chunkSize = vFile.SizeBytes
		if chunkSize <= 0 {
			chunkSize = 20 * 1024 * 1024
		}
	}
	if chunkSize <= 0 {
		chunkSize = 20 * 1024 * 1024
	}

	streamCtx, streamCancel := context.WithCancel(ctx)

	fs := &FileStreamer{
		ctx:          streamCtx,
		cancel:       streamCancel,
		vfs:          v,
		file:         vFile,
		chunks:       chunks,
		encKey:       encKey,
		offset:       0,
		chunkSize:    chunkSize,
	}
	fs.lastPrefetchedIdx.Store(-1)
	fs.lastPrefetchedBlock.Store(-1)
	return fs, nil
}

// Size returns total virtual file size in bytes
func (s *FileStreamer) Size() int64 {
	return s.file.SizeBytes
}

// ModTime returns the file modification time
func (s *FileStreamer) ModTime() time.Time {
	return s.file.UpdatedAt
}

// MimeType returns the virtual file MIME type
func (s *FileStreamer) MimeType() string {
	if s.file == nil {
		return "application/octet-stream"
	}
	return s.file.MimeType
}

// Read implements io.Reader
func (s *FileStreamer) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.offset >= s.file.SizeBytes {
		return 0, io.EOF
	}

	n, err = s.readInternal(p, s.offset)
	s.offset += int64(n)
	return n, err
}

// Seek implements io.Seeker
func (s *FileStreamer) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = s.offset + offset
	case io.SeekEnd:
		newOffset = s.file.SizeBytes + offset
	default:
		return s.offset, errors.New("invalid seek whence")
	}

	if newOffset < 0 {
		return s.offset, errors.New("negative seek offset")
	}

	s.offset = newOffset
	return s.offset, nil
}

// ReadAt implements io.ReaderAt (essential for random access, video seeking, and HTTP 206 Partial Content)
func (s *FileStreamer) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 || off >= s.file.SizeBytes {
		return 0, io.EOF
	}

	n, err = s.readInternal(p, off)
	if err == nil && n < len(p) {
		return n, io.EOF
	}
	return n, err
}

// readInternal thực hiện đọc tuần tự hoặc ngẫu nhiên từ vị trí startOff với cơ chế Range tối ưu
func (s *FileStreamer) readInternal(p []byte, startOff int64) (int, error) {
	if startOff >= s.file.SizeBytes {
		return 0, io.EOF
	}

	readTotal := 0
	bufLen := len(p)
	curOff := startOff

	for readTotal < bufLen && curOff < s.file.SizeBytes {
		chunkIdx := int(curOff / s.chunkSize)
		if chunkIdx >= len(s.chunks) {
			if readTotal > 0 {
				return readTotal, nil
			}
			fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunkIdx %d >= totalChunks %d at curOff %d, size %d)\n",
				s.file.ID, chunkIdx, len(s.chunks), curOff, s.file.SizeBytes)
			return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: chunk %d not found", chunkIdx)
		}

		offsetInChunk := curOff % s.chunkSize
		chunk := s.chunks[chunkIdx]
		isNative := !s.file.IsEncrypted || chunk.EncryptedSizeBytes == 0

		if isNative {
			// 1. Kiểm tra RAM cache: nếu toàn bộ chunk đã được cache sẵn (ví dụ unit test, pre-cached chunk)
			chunkCacheKey := fmt.Sprintf("chunk_%s_%s", chunk.AccountID, chunk.GDriveFileID)
			if fullData, found := globalChunkCache.Get(chunkCacheKey); found {
				if int(offsetInChunk) >= len(fullData) {
					if readTotal > 0 {
						return readTotal, nil
					}
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, offsetInChunk %d >= len(fullData) %d)\n",
						s.file.ID, offsetInChunk, len(fullData))
					return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: offset beyond chunk data")
				}
				avail := len(fullData) - int(offsetInChunk)
				want := bufLen - readTotal
				toCopy := min(avail, want)
				copy(p[readTotal:], fullData[offsetInChunk:int(offsetInChunk)+toCopy])
				readTotal += toCopy
				curOff += int64(toCopy)
				continue
			}

			// 2. Tối ưu cực đại tệp native Google Drive: tải đúng dải byte được yêu cầu (2MB block) qua DownloadRange
			chunkTotalSize := chunk.ChunkSizeBytes
			if chunkTotalSize <= 0 {
				chunkTotalSize = s.file.SizeBytes
			}

			blockIdx := offsetInChunk / NativeRangeBlockSize
			blockStart := blockIdx * NativeRangeBlockSize
			blockEnd := blockStart + NativeRangeBlockSize - 1
			if chunkTotalSize > 0 && blockEnd >= chunkTotalSize {
				blockEnd = chunkTotalSize - 1
			}

			if blockStart > blockEnd || (chunkTotalSize > 0 && blockStart >= chunkTotalSize) {
				break
			}

			blockData, err := s.fetchNativeRangeBlock(chunk, blockIdx, blockStart, blockEnd)
			if err != nil {
				if is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, range %d-%d, driveID %s): %v\n",
						s.file.ID, blockStart, blockEnd, chunk.GDriveFileID, err)
					if readTotal > 0 {
						return readTotal, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
					}
					return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
				}
				if readTotal > 0 {
					return readTotal, err
				}
				return 0, err
			}

			// Kích hoạt nạp trước block 2MB tiếp theo vào RAM cache để video không bị khựng
			if blockEnd+1 < chunkTotalSize {
				s.triggerRangePrefetch(chunk, blockIdx+1, chunkTotalSize)
			}

			offsetInBlock := int(offsetInChunk - blockStart)
			if offsetInBlock >= len(blockData) {
				if readTotal > 0 {
					return readTotal, nil
				}
				fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, range [%d-%d] returned %d bytes, offsetInBlock %d)\n",
					s.file.ID, blockStart, blockEnd, len(blockData), offsetInBlock)
				return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: range returned insufficient data")
			}

			avail := len(blockData) - offsetInBlock
			want := bufLen - readTotal
			toCopy := min(avail, want)

			copy(p[readTotal:], blockData[offsetInBlock:offsetInBlock+toCopy])
			readTotal += toCopy
			curOff += int64(toCopy)
		} else {
			// Chunk đã mã hóa AES: tải full chunk 20MB và giải mã bằng multi-key fallback
			chunkData, err := s.fetchAndDecryptChunk(chunk)
			if err != nil {
				if is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunk %d, driveID %s): %v\n",
						s.file.ID, chunkIdx, chunk.GDriveFileID, err)
					if readTotal > 0 {
						return readTotal, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
					}
					return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
				}
				if readTotal > 0 {
					return readTotal, err
				}
				return 0, fmt.Errorf("failed to load chunk %d: %w", chunkIdx, err)
			}

			// Kích hoạt nạp trước chunk tiếp theo vào RAM cache (chỉ trigger 1 lần khi chuyển chunk)
			if chunkIdx+1 < len(s.chunks) && int(s.lastPrefetchedIdx.Load()) != chunkIdx+1 {
				s.triggerPrefetch(chunkIdx + 1)
			}

			if int(offsetInChunk) >= len(chunkData) {
				if readTotal > 0 {
					return readTotal, nil
				}
				fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, offsetInChunk %d >= len(chunkData) %d)\n",
					s.file.ID, offsetInChunk, len(chunkData))
				return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: offset beyond chunk data")
			}

			avail := len(chunkData) - int(offsetInChunk)
			want := bufLen - readTotal
			toCopy := min(avail, want)

			copy(p[readTotal:], chunkData[offsetInChunk:int(offsetInChunk)+toCopy])
			readTotal += toCopy
			curOff += int64(toCopy)
		}
	}

	if readTotal == 0 {
		if curOff >= s.file.SizeBytes {
			return 0, io.EOF
		}
		fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (readTotal 0, curOff %d < size %d, file %s)\n",
			curOff, s.file.SizeBytes, s.file.ID)
		return 0, fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: stream stalled at offset %d", curOff)
	}

	return readTotal, nil
}

// chunkFlight tracks in-flight chunk fetching to prevent duplicate downloads
type chunkFlight struct {
	done chan struct{}
	data []byte
	err  error
}

var (
	flightMu sync.Mutex
	inFlight = make(map[string]*chunkFlight)
	// Semaphore to limit concurrent background prefetches to max 4 (Rule PHAN 7.1)
	prefetchSem = make(chan struct{}, 4)
)

// fetchNativeRangeBlock tải dải byte 2MB của tệp native Google Drive
func (s *FileStreamer) fetchNativeRangeBlock(chunk models.FileChunk, blockIdx int64, blockStart, blockEnd int64) ([]byte, error) {
	rangeKey := fmt.Sprintf("range_%s_%s_%d", chunk.AccountID, chunk.GDriveFileID, blockStart)
	if data, found := globalChunkCache.Get(rangeKey); found {
		return data, nil
	}

	// Singleflight: deduplicate simultaneous range downloads
	flightMu.Lock()
	if flight, ok := inFlight[rangeKey]; ok {
		flightMu.Unlock()
		select {
		case <-flight.done:
			return flight.data, flight.err
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}

	flight := &chunkFlight{done: make(chan struct{})}
	inFlight[rangeKey] = flight
	flightMu.Unlock()

	defer func() {
		flightMu.Lock()
		delete(inFlight, rangeKey)
		flightMu.Unlock()
		close(flight.done)
	}()

	if s.vfs == nil || s.vfs.gd == nil {
		flight.err = errors.New("gdrive manager not initialized")
		return nil, flight.err
	}

	// Sử dụng DownloadRange để tải dải byte 2MB với timeout bảo vệ chống rò rỉ goroutine
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer dlCancel()

	rangeBytes, err := s.vfs.gd.DownloadRange(dlCtx, chunk.AccountID, chunk.GDriveFileID, blockStart, blockEnd)
	if err != nil {
		if is404(err) {
			fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunk %d, driveID %s, range %d-%d): %v\n",
				s.file.ID, chunk.ChunkIndex, chunk.GDriveFileID, blockStart, blockEnd, err)
			flight.err = fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
			return nil, flight.err
		}
		flight.err = fmt.Errorf("download range [%d-%d] failed: %w", blockStart, blockEnd, err)
		return nil, flight.err
	}

	if len(rangeBytes) == 0 && blockEnd >= blockStart {
		fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunk %d, driveID %s, empty range [%d-%d])\n",
			s.file.ID, chunk.ChunkIndex, chunk.GDriveFileID, blockStart, blockEnd)
		flight.err = fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: empty range [%d-%d] received from Drive", blockStart, blockEnd)
		return nil, flight.err
	}

	// Lưu khối range 2MB vào LRU cache với bộ nhớ kiểm soát nghiêm ngặt
	globalChunkCache.Set(rangeKey, rangeBytes)
	flight.data = rangeBytes
	return rangeBytes, nil
}

// fetchAndDecryptChunk tải toàn bộ chunk (20MB) và giải mã đối với tệp mã hóa AES
func (s *FileStreamer) fetchAndDecryptChunk(chunk models.FileChunk) ([]byte, error) {
	cacheKey := fmt.Sprintf("chunk_%s_%s", chunk.AccountID, chunk.GDriveFileID)
	if data, found := globalChunkCache.Get(cacheKey); found {
		return data, nil
	}

	// Singleflight: deduplicate simultaneous requests for the exact same chunk
	flightMu.Lock()
	if flight, ok := inFlight[cacheKey]; ok {
		flightMu.Unlock()
		select {
		case <-flight.done:
			return flight.data, flight.err
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}

	flight := &chunkFlight{done: make(chan struct{})}
	inFlight[cacheKey] = flight
	flightMu.Unlock()

	defer func() {
		flightMu.Lock()
		delete(inFlight, cacheKey)
		flightMu.Unlock()
		close(flight.done)
	}()

	if s.vfs == nil || s.vfs.gd == nil {
		flight.err = errors.New("gdrive manager not initialized")
		return nil, flight.err
	}

	// Download from Google Drive with dedicated context to prevent browser range aborts
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer dlCancel()

	rawBytes, err := s.vfs.gd.DownloadChunk(dlCtx, chunk.AccountID, chunk.GDriveFileID)
	if err != nil {
		if is404(err) {
			fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunk %d, driveID %s): %v\n",
				s.file.ID, chunk.ChunkIndex, chunk.GDriveFileID, err)
			flight.err = fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: %w", err)
			return nil, flight.err
		}
		flight.err = fmt.Errorf("download chunk failed: %w", err)
		return nil, flight.err
	}

	if len(rawBytes) == 0 && chunk.ChunkSizeBytes > 0 {
		fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 (file %s, chunk %d, driveID %s returned 0 bytes, expected %d)\n",
			s.file.ID, chunk.ChunkIndex, chunk.GDriveFileID, chunk.ChunkSizeBytes)
		flight.err = fmt.Errorf("[STREAMER ERROR] Chunk missing on Drive: 404: chunk returned 0 bytes")
		return nil, flight.err
	}

	// If file is an imported native Drive file without encryption
	if !s.file.IsEncrypted || chunk.EncryptedSizeBytes == 0 {
		globalChunkCache.Set(cacheKey, rawBytes)
		flight.data = rawBytes
		return rawBytes, nil
	}

	// Decrypt encrypted chunks with multi-key adaptive fallback
	candidateKeys := [][32]byte{
		s.encKey,
		core.DeriveKey("Hung1999@", nil),
		core.DeriveKey("17c88fc173d5005180d476444daeb1e7", nil),
		core.DeriveKey("cloudpool_secure_master_key_2026", nil),
		core.DeriveKey("", nil),
	}
	if s.vfs != nil && s.vfs.db != nil {
		candidateKeys = append(candidateKeys, s.vfs.db.GetMasterKey())
	}

	var plaintext []byte
	var decErr error
	for _, key := range candidateKeys {
		pt, err := core.DecryptChunk(key, rawBytes)
		if err == nil {
			plaintext = pt
			decErr = nil
			break
		}
		decErr = err
	}

	if decErr != nil {
		flight.err = fmt.Errorf("decrypt chunk failed (all candidate keys): %w", decErr)
		return nil, flight.err
	}

	// Save to RAM cache
	globalChunkCache.Set(cacheKey, plaintext)
	flight.data = plaintext
	return plaintext, nil
}

// triggerRangePrefetch nạp trước ngầm khối 2MB tiếp theo vào RAM buffer cho native Drive file
func (s *FileStreamer) triggerRangePrefetch(chunk models.FileChunk, nextBlockIdx int64, chunkTotalSize int64) {
	blockStart := nextBlockIdx * NativeRangeBlockSize
	if chunkTotalSize > 0 && blockStart >= chunkTotalSize {
		return
	}
	blockEnd := blockStart + NativeRangeBlockSize - 1
	if chunkTotalSize > 0 && blockEnd >= chunkTotalSize {
		blockEnd = chunkTotalSize - 1
	}

	old := s.lastPrefetchedBlock.Load()
	if old == nextBlockIdx {
		return
	}
	if !s.lastPrefetchedBlock.CompareAndSwap(old, nextBlockIdx) {
		return
	}

	rangeKey := fmt.Sprintf("range_%s_%s_%d", chunk.AccountID, chunk.GDriveFileID, blockStart)
	if _, found := globalChunkCache.Get(rangeKey); found {
		return
	}

	flightMu.Lock()
	if _, running := inFlight[rangeKey]; running {
		flightMu.Unlock()
		return
	}
	flightMu.Unlock()

	select {
	case prefetchSem <- struct{}{}:
		go func() {
			defer func() {
				<-prefetchSem
				if r := recover(); r != nil {
					// Bảo vệ goroutine không panic
				}
			}()
			if s.ctx != nil && s.ctx.Err() != nil {
				return
			}
			_, err := s.fetchNativeRangeBlock(chunk, nextBlockIdx, blockStart, blockEnd)
			if err != nil && is404(err) {
				fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 in prefetch (file %s, range %d-%d): %v\n",
					s.file.ID, blockStart, blockEnd, err)
			}
		}()
	default:
		// Đang có 2 prefetch khác chạy ngầm, bỏ qua để bảo vệ CPU/RAM
		return
	}
}

// triggerPrefetch nạp trước ngầm chunk mã hóa tiếp theo vào RAM buffer
func (s *FileStreamer) triggerPrefetch(nextChunkIdx int) {
	if nextChunkIdx < 0 || nextChunkIdx >= len(s.chunks) {
		return
	}

	old := s.lastPrefetchedIdx.Load()
	if int(old) == nextChunkIdx {
		return
	}
	if !s.lastPrefetchedIdx.CompareAndSwap(old, int32(nextChunkIdx)) {
		return
	}

	nextChunk := s.chunks[nextChunkIdx]
	cacheKey := fmt.Sprintf("chunk_%s_%s", nextChunk.AccountID, nextChunk.GDriveFileID)
	if _, found := globalChunkCache.Get(cacheKey); found {
		return
	}

	flightMu.Lock()
	if _, running := inFlight[cacheKey]; running {
		flightMu.Unlock()
		return
	}
	flightMu.Unlock()

	// Try acquiring semaphore; if full, do not spawn more goroutines (prevents goroutine explosion)
	select {
	case prefetchSem <- struct{}{}:
		go func() {
			defer func() {
				<-prefetchSem
				if r := recover(); r != nil {
					// Bảo vệ goroutine không ảnh hưởng luồng chính
				}
			}()
			if s.ctx != nil && s.ctx.Err() != nil {
				return
			}
			_, err := s.fetchAndDecryptChunk(nextChunk)
			if err != nil && is404(err) {
				fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 in prefetch (file %s, chunk %d): %v\n",
					s.file.ID, nextChunkIdx, err)
			}
		}()
	default:
		// Đang có 2 prefetch khác chạy ngầm, bỏ qua để bảo vệ CPU/RAM
		return
	}
}

// Close closes the streamer and cancels active background contexts
func (s *FileStreamer) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
