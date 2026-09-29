package vfs

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
)

// ChunkCacheEntry holds decrypted chunk bytes in memory with LRU tracking
type ChunkCacheEntry struct {
	Key          string
	Data         []byte
	ExpiresAt    time.Time
	LastAccessed time.Time
}

// ChunkCache implements a thread-safe LRU memory buffer for stream chunks (Rule PHAN 7.2)
type ChunkCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lruList *list.List
	maxSize int
}

var globalChunkCache = &ChunkCache{
	entries: make(map[string]*list.Element),
	lruList: list.New(),
	maxSize: 6, // Cache up to 6 chunks in RAM (~120MB max, safely well below 500MB rule PHAN 7.1)
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
		return nil, false
	}

	entry.LastAccessed = now
	c.lruList.MoveToFront(elem)
	return entry.Data, true
}

func (c *ChunkCache) Set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	// If already exists, update data and move to front
	if elem, found := c.entries[key]; found {
		entry := elem.Value.(*ChunkCacheEntry)
		entry.Data = data
		entry.ExpiresAt = now.Add(5 * time.Minute) // 5 minute TTL (Rule PHAN 7.2)
		entry.LastAccessed = now
		c.lruList.MoveToFront(elem)
		return
	}

	// If cache exceeds maxSize, evict least recently used item (at back of lruList)
	if len(c.entries) >= c.maxSize {
		oldest := c.lruList.Back()
		if oldest != nil {
			c.lruList.Remove(oldest)
			oldEntry := oldest.Value.(*ChunkCacheEntry)
			delete(c.entries, oldEntry.Key)
		}
	}

	newEntry := &ChunkCacheEntry{
		Key:          key,
		Data:         data,
		ExpiresAt:    now.Add(5 * time.Minute),
		LastAccessed: now,
	}
	elem := c.lruList.PushFront(newEntry)
	c.entries[key] = elem
}

// SetChunkCache allows pre-caching decrypted or raw chunk bytes in memory (Rule PHAN 7.2)
func SetChunkCache(accountID, gdriveFileID string, data []byte) {
	cacheKey := fmt.Sprintf("chunk_%s_%s", accountID, gdriveFileID)
	globalChunkCache.Set(cacheKey, data)
}

// FileStreamer implements io.ReadSeeker and io.ReaderAt over virtual distributed chunks
type FileStreamer struct {
	ctx               context.Context
	vfs               *VFS
	file              *models.VirtualFile
	chunks            []models.FileChunk
	encKey            [32]byte
	offset            int64
	chunkSize         int64
	mu                sync.Mutex
	lastPrefetchedIdx int
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
		// Avoid zero chunkSize if file is empty
		if chunkSize == 0 {
			chunkSize = 20 * 1024 * 1024
		}
	}

	return &FileStreamer{
		ctx:               ctx,
		vfs:               v,
		file:              vFile,
		chunks:            chunks,
		encKey:            encKey,
		offset:            0,
		chunkSize:         chunkSize,
		lastPrefetchedIdx: -1,
	}, nil
}

// Size returns total virtual file size in bytes
func (s *FileStreamer) Size() int64 {
	return s.file.SizeBytes
}

// ModTime returns the file modification time
func (s *FileStreamer) ModTime() time.Time {
	return s.file.UpdatedAt
}

// Read implements io.Reader
func (s *FileStreamer) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.offset >= s.file.SizeBytes {
		return 0, io.EOF
	}


	readTotal := 0
	bufLen := len(p)

	for readTotal < bufLen && s.offset < s.file.SizeBytes {
		chunkIdx := int(s.offset / s.chunkSize)
		if chunkIdx >= len(s.chunks) {
			break
		}

		offsetInChunk := s.offset % s.chunkSize
		chunk := s.chunks[chunkIdx]

		chunkData, err := s.fetchAndDecryptChunk(chunk)
		if err != nil {
			fmt.Printf("[STREAMER ERROR] Read: fetchAndDecryptChunk failed: %v\n", err)
			return readTotal, fmt.Errorf("failed to load chunk %d: %w", chunkIdx, err)
		}

		// Kích hoạt nạp trước chunk tiếp theo vào RAM cache (chỉ trigger 1 lần khi chuyển chunk)
		if chunkIdx+1 < len(s.chunks) && s.lastPrefetchedIdx != chunkIdx+1 {
			s.triggerPrefetch(chunkIdx + 1)
		}

		if int(offsetInChunk) >= len(chunkData) {
			fmt.Printf("[STREAMER ERROR] Read: offsetInChunk %d >= len(chunkData) %d (chunkIdx=%d)\n", offsetInChunk, len(chunkData), chunkIdx)
			break
		}

		avail := len(chunkData) - int(offsetInChunk)
		want := bufLen - readTotal
		toCopy := min(avail, want)

		copy(p[readTotal:], chunkData[offsetInChunk:int(offsetInChunk)+toCopy])
		readTotal += toCopy
		s.offset += int64(toCopy)
	}

	if readTotal == 0 && s.offset >= s.file.SizeBytes {
		return 0, io.EOF
	}

	if readTotal == 0 {
		fmt.Printf("[STREAMER WARNING] Read returned 0, nil! offset=%d, size=%d\n", s.offset, s.file.SizeBytes)
	}
	return readTotal, nil
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


	readTotal := 0
	bufLen := len(p)
	curOff := off

	for readTotal < bufLen && curOff < s.file.SizeBytes {
		chunkIdx := int(curOff / s.chunkSize)
		if chunkIdx >= len(s.chunks) {
			break
		}

		offsetInChunk := curOff % s.chunkSize
		chunk := s.chunks[chunkIdx]

		chunkData, err := s.fetchAndDecryptChunk(chunk)
		if err != nil {
			fmt.Printf("[STREAMER ERROR] fetchAndDecryptChunk failed: %v\n", err)
			return readTotal, fmt.Errorf("failed to fetch chunk %d: %w", chunkIdx, err)
		}

		// Kích hoạt nạp trước chunk tiếp theo vào RAM cache (chỉ trigger 1 lần khi chuyển chunk)
		if chunkIdx+1 < len(s.chunks) && s.lastPrefetchedIdx != chunkIdx+1 {
			s.triggerPrefetch(chunkIdx + 1)
		}

		if int(offsetInChunk) >= len(chunkData) {
			fmt.Printf("[STREAMER ERROR] offsetInChunk %d >= len(chunkData) %d\n", offsetInChunk, len(chunkData))
			break
		}

		avail := len(chunkData) - int(offsetInChunk)
		want := bufLen - readTotal
		toCopy := min(avail, want)

		copy(p[readTotal:], chunkData[offsetInChunk:int(offsetInChunk)+toCopy])
		readTotal += toCopy
		curOff += int64(toCopy)
	}

	if readTotal < bufLen {
		fmt.Printf("[STREAMER WARNING] readTotal %d < bufLen %d. Returning EOF.\n", readTotal, bufLen)
		return readTotal, io.EOF
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
	// Semaphore to limit concurrent background prefetches to max 2
	prefetchSem = make(chan struct{}, 2)
)

func (s *FileStreamer) fetchAndDecryptChunk(chunk models.FileChunk) ([]byte, error) {
	cacheKey := fmt.Sprintf("chunk_%s_%s", chunk.AccountID, chunk.GDriveFileID)
	if data, found := globalChunkCache.Get(cacheKey); found {
		return data, nil
	}

	// Singleflight: deduplicate simultaneous requests for the exact same chunk
	flightMu.Lock()
	if flight, ok := inFlight[cacheKey]; ok {
		flightMu.Unlock()
		<-flight.done
		return flight.data, flight.err
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

	// Download from Google Drive with dedicated context to prevent browser range aborts
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer dlCancel()
	rawBytes, err := s.vfs.gd.DownloadChunk(dlCtx, chunk.AccountID, chunk.GDriveFileID)
	if err != nil {
		flight.err = fmt.Errorf("download chunk failed: %w", err)
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

// triggerPrefetch nạp trước ngầm chunk tiếp theo vào RAM buffer để không bị đệm chờ khi phát video
func (s *FileStreamer) triggerPrefetch(nextChunkIdx int) {
	if nextChunkIdx < 0 || nextChunkIdx >= len(s.chunks) {
		return
	}

	s.mu.Lock()
	if s.lastPrefetchedIdx == nextChunkIdx {
		s.mu.Unlock()
		return
	}
	s.lastPrefetchedIdx = nextChunkIdx
	s.mu.Unlock()

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
			_, _ = s.fetchAndDecryptChunk(nextChunk)
		}()
	default:
		// Đang có 2 prefetch khác chạy ngầm, bỏ qua để bảo vệ CPU/RAM
		return
	}
}

// Close closes the streamer
func (s *FileStreamer) Close() error {
	return nil
}

