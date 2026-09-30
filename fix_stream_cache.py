import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# Replace the chunk cache implementation (lines 45-189) with the subagent's SLRU implementation
# I will use a regex to find the start of ChunkCache struct and end of SetRangeCache

new_cache_code = '''
const (
	// NativeRangeBlockSize kích thu?c kh?i t?i d?i byte (4MB) cho các t?p native Google Drive không mã hóa.
	NativeRangeBlockSize int64 = 16 * 1024 * 1024 // 16 MB

	// DefaultChunkTTL Th?i gian s?ng tru?t m?c d?nh cho kh?i trong cache (15 phút, Rule PHAN 7.2)
	DefaultChunkTTL = 15 * time.Minute

	// ProtectedChunkTTL Th?i gian s?ng cho các kh?i Protected/Metadata quan tr?ng (30 phút)
	ProtectedChunkTTL = 30 * time.Minute

	// CorrelatedAccessWindow Ngu?ng th?i gian phân bi?t gi?a d?c tu?n t? 32KB lát c?t và truy c?p d?c l?p (8 giây)
	CorrelatedAccessWindow = 8 * time.Second
)

// ChunkCacheEntry holds decrypted chunk bytes or range block bytes in memory with SLRU tracking
type ChunkCacheEntry struct {
	Key           string
	Data          []byte
	ExpiresAt     time.Time
	FirstAccessed time.Time
	LastAccessed  time.Time
	Hits          int32
	IsProtected   bool
}

// ChunkCache implements a thread-safe Segmented LRU (2Q/SLRU) memory buffer with strict byte limit
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

# Replace from ar globalChunkCache = to unc SetRangeCache block
start_str = "type ChunkCacheEntry struct"
end_str = "func SetRangeCache("
# I will use regex
pattern = re.compile(r'type ChunkCacheEntry struct.*?func SetRangeCache.*?\n}', re.DOTALL)
match = pattern.search(code)
if match:
    # Also I need to remove the NativeRangeBlockSize declaration earlier
    code = re.sub(r'const \(\n\s*NativeRangeBlockSize.*?\n\)', '', code, flags=re.DOTALL)
    
    # We must insert the atomic package if not present
    if '"sync/atomic"' not in code:
        code = code.replace('"sync"', '"sync"\n\t"sync/atomic"')
        
    code = code[:match.start()] + new_cache_code + code[match.end():]
    
    # Update triggerPrefetch with Contains
    code = code.replace('_, found := globalChunkCache.Get(rangeKey); found', 'globalChunkCache.Contains(rangeKey)')
    code = code.replace('_, found := globalChunkCache.Get(cacheKey); found', 'globalChunkCache.Contains(cacheKey)')

    with open(path, 'w', encoding='utf-8') as f:
        f.write(code)
    print("Done patching stream.go")
else:
    print("Could not find the block to replace!")

