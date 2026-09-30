# -*- coding: utf-8 -*-
import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# 1. Add atomic and runtime
if '"runtime"' not in code:
    code = code.replace('"context"', '"context"\n\t"runtime"\n\t"sync/atomic"')

# 2. SLRU Replace
new_cache_code = '''
const (
	NativeRangeBlockSize int64 = 4 * 1024 * 1024 
	MaxConcurrentPrefetch = 32
	MaxPrefetchAhead = 2
	DefaultChunkTTL = 15 * time.Minute
	ProtectedChunkTTL = 30 * time.Minute
	CorrelatedAccessWindow = 8 * time.Second
)

var (
	flightMu    sync.Mutex
	inFlight    = make(map[string]*chunkFlight)
	prefetchSem = make(chan struct{}, MaxConcurrentPrefetch)
)

type ChunkCacheEntry struct {
	Key           string
	Data          []byte
	ExpiresAt     time.Time
	FirstAccessed time.Time
	LastAccessed  time.Time
	Hits          int32
	IsProtected   bool
}

type ChunkCache struct {
	mu             sync.Mutex
	entries        map[string]*list.Element
	lruList        *list.List 
	probationList  *list.List 
	protectedList  *list.List 
	maxEntries     int
	maxBytes       int64
	curBytes       int64
	protectedBytes int64
	ttl            time.Duration
	hits           atomic.Uint64
	misses         atomic.Uint64
}

func initChunkCache() *ChunkCache {
	maxBytes := int64(256 * 1024 * 1024) 
	if envMB := os.Getenv("STREAM_CACHE_MAX_BYTES_MB"); envMB != "" {
		if mb, err := strconv.ParseInt(envMB, 10, 64); err == nil && mb > 0 {
			maxBytes = mb * 1024 * 1024
		}
	}
	prob := list.New()
	return &ChunkCache{
		entries:       make(map[string]*list.Element),
		lruList:       prob,
		probationList: prob,
		protectedList: list.New(),
		maxEntries:    1024, 
		maxBytes:      maxBytes,
		ttl:           DefaultChunkTTL,
	}
}

var globalChunkCache = initChunkCache()

func init() {
	go globalChunkCache.StartJanitor()
}

func (c *ChunkCache) ensureListsLocked() {
	if c.probationList == nil {
		if c.lruList != nil {
			c.probationList = c.lruList
		} else {
			c.probationList = list.New()
			c.lruList = c.probationList
		}
	}
	if c.protectedList == nil {
		c.protectedList = list.New()
	}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	if c.ttl <= 0 {
		c.ttl = DefaultChunkTTL
	}
	if c.maxEntries <= 0 {
		c.maxEntries = 1024
	}
}

func (c *ChunkCache) StartJanitor() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		c.ensureListsLocked()

		var next *list.Element
		for e := c.probationList.Front(); e != nil; e = next {
			next = e.Next()
			entry := e.Value.(*ChunkCacheEntry)
			if now.After(entry.ExpiresAt) {
				c.removeElementLocked(e)
			}
		}

		for e := c.protectedList.Front(); e != nil; e = next {
			next = e.Next()
			entry := e.Value.(*ChunkCacheEntry)
			if now.After(entry.ExpiresAt) {
				c.removeElementLocked(e)
			}
		}
		c.mu.Unlock()
	}
}

func (c *ChunkCache) Contains(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, found := c.entries[key]
	if !found {
		return false
	}
	entry := elem.Value.(*ChunkCacheEntry)
	return time.Now().Before(entry.ExpiresAt)
}

func (c *ChunkCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureListsLocked()

	elem, found := c.entries[key]
	if !found {
		c.misses.Add(1)
		return nil, false
	}

	entry := elem.Value.(*ChunkCacheEntry)
	now := time.Now()
	if now.After(entry.ExpiresAt) {
		c.removeElementLocked(elem)
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	entry.LastAccessed = now
	entry.Hits++

	if entry.IsProtected {
		entry.ExpiresAt = now.Add(ProtectedChunkTTL)
		c.protectedList.MoveToFront(elem)
	} else {
		entry.ExpiresAt = now.Add(c.ttl)
		if strings.HasSuffix(entry.Key, "_0") || (now.Sub(entry.FirstAccessed) >= CorrelatedAccessWindow && entry.Hits > 1) {
			c.promoteToProtectedLocked(elem)
		} else {
			c.probationList.MoveToFront(elem)
		}
	}

	return entry.Data, true
}

func (c *ChunkCache) Set(key string, data []byte) {
	if len(data) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureListsLocked()

	now := time.Now()
	dataLen := int64(len(data))

	if dataLen > c.maxBytes {
		return
	}

	if elem, found := c.entries[key]; found {
		entry := elem.Value.(*ChunkCacheEntry)
		oldLen := int64(len(entry.Data))
		c.curBytes = c.curBytes - oldLen + dataLen
		entry.Data = data
		entry.LastAccessed = now

		if entry.IsProtected {
			c.protectedBytes = c.protectedBytes - oldLen + dataLen
			entry.ExpiresAt = now.Add(ProtectedChunkTTL)
			c.protectedList.MoveToFront(elem)
		} else {
			entry.ExpiresAt = now.Add(c.ttl)
			c.probationList.MoveToFront(elem)
		}
		c.evictOldestLocked()
		return
	}

	for (len(c.entries) >= c.maxEntries || (c.curBytes+dataLen > c.maxBytes && c.curBytes > 0)) && (c.probationList.Len() > 0 || c.protectedList.Len() > 0) {
		if c.probationList.Len() > 0 {
			oldest := c.probationList.Back()
			if oldest != nil {
				c.removeElementLocked(oldest)
				continue
			}
		}
		if c.protectedList.Len() > 0 {
			oldest := c.protectedList.Back()
			if oldest != nil {
				c.removeElementLocked(oldest)
				continue
			}
		}
		break
	}

	newEntry := &ChunkCacheEntry{
		Key:           key,
		Data:          data,
		ExpiresAt:     now.Add(c.ttl),
		FirstAccessed: now,
		LastAccessed:  now,
		Hits:          1,
		IsProtected:   false,
	}

	if strings.HasSuffix(key, "_0") && c.protectedBytes+dataLen <= c.maxBytes*3/4 {
		newEntry.IsProtected = true
		newEntry.ExpiresAt = now.Add(ProtectedChunkTTL)
		elem := c.protectedList.PushFront(newEntry)
		c.entries[key] = elem
		c.protectedBytes += dataLen
	} else {
		elem := c.probationList.PushFront(newEntry)
		c.entries[key] = elem
	}
	c.curBytes += dataLen
}

func (c *ChunkCache) promoteToProtectedLocked(elem *list.Element) {
	entry := elem.Value.(*ChunkCacheEntry)
	c.probationList.Remove(elem)

	newElem := c.protectedList.PushFront(entry)
	c.entries[entry.Key] = newElem
	entry.IsProtected = true
	dataLen := int64(len(entry.Data))
	c.protectedBytes += dataLen

	maxProtected := c.maxBytes * 3 / 4
	for c.protectedBytes > maxProtected && c.protectedList.Len() > 0 {
		oldestProtected := c.protectedList.Back()
		if oldestProtected == nil {
			break
		}
		c.demoteToProbationLocked(oldestProtected)
	}
}

func (c *ChunkCache) demoteToProbationLocked(elem *list.Element) {
	entry := elem.Value.(*ChunkCacheEntry)
	c.protectedList.Remove(elem)
	c.protectedBytes -= int64(len(entry.Data))
	entry.IsProtected = false
	entry.Hits = 1
	entry.FirstAccessed = time.Now()

	newElem := c.probationList.PushFront(entry)
	c.entries[entry.Key] = newElem
}

func (c *ChunkCache) removeElementLocked(elem *list.Element) {
	entry := elem.Value.(*ChunkCacheEntry)
	if entry.IsProtected {
		c.protectedList.Remove(elem)
		c.protectedBytes -= int64(len(entry.Data))
	} else {
		c.probationList.Remove(elem)
	}
	delete(c.entries, entry.Key)
	c.curBytes -= int64(len(entry.Data))
	entry.Data = nil
}

func (c *ChunkCache) evictOldestLocked() {
	for (len(c.entries) > c.maxEntries || c.curBytes > c.maxBytes) && (c.probationList.Len() > 0 || c.protectedList.Len() > 0) {
		if c.probationList.Len() > 0 {
			oldest := c.probationList.Back()
			if oldest != nil {
				c.removeElementLocked(oldest)
				continue
			}
		}
		if c.protectedList.Len() > 0 {
			oldest := c.protectedList.Back()
			if oldest != nil {
				c.removeElementLocked(oldest)
				continue
			}
		}
		break
	}
}

func (c *ChunkCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, elem := range c.entries {
		entry := elem.Value.(*ChunkCacheEntry)
		entry.Data = nil
	}
	c.entries = make(map[string]*list.Element)
	c.probationList = list.New()
	c.protectedList = list.New()
	c.lruList = c.probationList
	c.curBytes = 0
	c.protectedBytes = 0
}

func (c *ChunkCache) Stats() (entries int, curBytes int64, protectedBytes int64, hitCount uint64, missCount uint64, hitRate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries = len(c.entries)
	curBytes = c.curBytes
	protectedBytes = c.protectedBytes
	hitCount = c.hits.Load()
	missCount = c.misses.Load()
	total := hitCount + missCount
	if total > 0 {
		hitRate = float64(hitCount) / float64(total) * 100.0
	}
	return
}

func SetChunkCache(accountID, gdriveFileID string, data []byte) {
	cacheKey := fmt.Sprintf("chunk_%s_%s", accountID, gdriveFileID)
	globalChunkCache.Set(cacheKey, data)
}

func SetRangeCache(accountID, gdriveFileID string, start int64, data []byte) {
	cacheKey := fmt.Sprintf("range_%s_%s_%d", accountID, gdriveFileID, start)
	globalChunkCache.Set(cacheKey, data)
}
'''
code = re.sub(r'const \(\n\s*// NativeRangeBlockSize.*?var \(\n\s*flightMu.*?prefetchSem = make\(chan struct\{\}, 8\)\n\)', '', code, flags=re.DOTALL)
code = re.sub(r'type ChunkCacheEntry struct.*?func SetRangeCache.*?\n}', new_cache_code, code, flags=re.DOTALL)


# 3. FileStreamer
new_fs = """type FileStreamer struct {
	ctx               context.Context
	cancel            context.CancelFunc
	vfs               *VFS
	file              *models.VirtualFile
	chunks            []models.FileChunk
	encKey            [32]byte
	offset            int64
	chunkSize         int64
	mu                sync.Mutex
	lastPrefetchChunk atomic.Int32
	lastPrefetchBlock atomic.Int64
}"""
code = re.sub(r'type FileStreamer struct \{.*?lastPrefetchedBlk atomic\.Int64\n\}', new_fs, code, flags=re.DOTALL)

code = code.replace("fs.lastPrefetchedIdx.Store(-1)", "fs.lastPrefetchChunk.Store(-1)")
code = code.replace("fs.lastPrefetchedBlk.Store(-1)", "fs.lastPrefetchBlock.Store(-1)")

# 4. Prefetch Range
new_range = """func (s *FileStreamer) triggerRangePrefetchAhead(chunk models.FileChunk, currentBlockIdx int64, chunkTotalSize int64) {
	allQueued := true

	for step := int64(1); step <= MaxPrefetchAhead; step++ {
		targetBlockIdx := currentBlockIdx + step
		blockStart := targetBlockIdx * NativeRangeBlockSize
		if chunkTotalSize > 0 && blockStart >= chunkTotalSize {
			break
		}
		blockEnd := blockStart + NativeRangeBlockSize - 1
		if chunkTotalSize > 0 && blockEnd >= chunkTotalSize {
			blockEnd = chunkTotalSize - 1
		}

		rangeKey := fmt.Sprintf("range_%s_%s_%d", chunk.AccountID, chunk.GDriveFileID, blockStart)

		if globalChunkCache.Contains(rangeKey) {
			continue
		}

		flightMu.Lock()
		if _, running := inFlight[rangeKey]; running {
			flightMu.Unlock()
			continue
		}
		flightMu.Unlock()

		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		if mem.Alloc > 400*1024*1024 { 
			allQueued = false
			break
		}

		select {
		case prefetchSem <- struct{}{}:
			go func(bIdx, bStart, bEnd int64) {
				defer func() {
					<-prefetchSem
					if r := recover(); r != nil {
					}
				}()
				if s.ctx != nil && s.ctx.Err() != nil {
					return
				}
				_, err := s.fetchNativeRangeBlock(chunk, bIdx, bStart, bEnd)
				if err != nil && is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 in prefetch\\n")
				}
			}(targetBlockIdx, blockStart, blockEnd)
		default:
			allQueued = false
			break
		}
	}

	if allQueued {
		s.lastPrefetchBlock.Store(currentBlockIdx)
	}
}"""
code = re.sub(r'func \(s \*FileStreamer\) triggerRangePrefetch\(.*?^\}', new_range, code, flags=re.DOTALL | re.MULTILINE)

# 5. Prefetch Chunk
new_prefetch = """func (s *FileStreamer) triggerPrefetchAhead(currentChunkIdx int) {
	allQueued := true

	for step := 1; step <= MaxPrefetchAhead; step++ {
		targetIdx := currentChunkIdx + step
		if targetIdx >= len(s.chunks) {
			break
		}

		targetChunk := s.chunks[targetIdx]
		cacheKey := fmt.Sprintf("chunk_%s_%s", targetChunk.AccountID, targetChunk.GDriveFileID)

		if globalChunkCache.Contains(cacheKey) {
			continue
		}

		flightMu.Lock()
		if _, running := inFlight[cacheKey]; running {
			flightMu.Unlock()
			continue
		}
		flightMu.Unlock()

		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		if mem.Alloc > 400*1024*1024 {
			allQueued = false
			break
		}

		select {
		case prefetchSem <- struct{}{}:
			go func(chk models.FileChunk, idx int) {
				defer func() {
					<-prefetchSem
					if r := recover(); r != nil {
					}
				}()
				if s.ctx != nil && s.ctx.Err() != nil {
					return
				}
				_, err := s.fetchAndDecryptChunk(chk)
				if err != nil && is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404\\n")
				}
			}(targetChunk, targetIdx)
		default:
			allQueued = false
			break
		}
	}

	if allQueued {
		s.lastPrefetchChunk.Store(int32(currentChunkIdx))
	}
}"""
code = re.sub(r'func \(s \*FileStreamer\) triggerPrefetch\(.*?^\}', new_prefetch, code, flags=re.DOTALL | re.MULTILINE)

# 6. Replace call sites
code = code.replace("if s.lastPrefetchedBlk.Load() != nextBlockIdx && blockEnd+1 < chunkTotalSize {", "if s.lastPrefetchBlock.Load() != blockIdx && blockEnd+1 < chunkTotalSize {")
code = code.replace("s.triggerRangePrefetch(chunk, nextBlockIdx, chunkTotalSize)", "s.triggerRangePrefetchAhead(chunk, blockIdx, chunkTotalSize)")

code = code.replace("if s.lastPrefetchedIdx.Load() != int32(nextChunkIdx) && nextChunkIdx < len(s.chunks) {", "if s.lastPrefetchChunk.Load() != int32(chunkIdx) && chunkIdx+1 < len(s.chunks) {")
code = code.replace("s.triggerPrefetch(nextChunkIdx)", "s.triggerPrefetchAhead(chunkIdx)")

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)

