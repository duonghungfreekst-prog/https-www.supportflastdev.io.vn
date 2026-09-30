import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# Fix struct
code = code.replace("lastPrefetchedIdx   atomic.Int32", "lastPrefetchChunk atomic.Int32")
code = code.replace("lastPrefetchedBlock atomic.Int64", "lastPrefetchBlock atomic.Int64")
code = code.replace("lastPrefetchedBlk atomic.Int64", "lastPrefetchBlock atomic.Int64")

# Fix triggerRangePrefetch in readInternal
code = code.replace("s.triggerRangePrefetch(chunk, nextBlockIdx, chunkTotalSize)", "s.triggerRangePrefetchAhead(chunk, blockIdx, chunkTotalSize)")
code = code.replace("s.triggerPrefetch(nextChunkIdx)", "s.triggerPrefetchAhead(chunkIdx)")

# Ensure Contains is present
if "func (c *ChunkCache) Contains(key string) bool {" not in code:
    print("Contains not found! We need to add it.")
    insert_pos = code.find("func (c *ChunkCache) Get(key string) ([]byte, bool) {")
    contains_code = '''func (c *ChunkCache) Contains(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, found := c.entries[key]
	if !found {
		return false
	}
	entry := elem.Value.(*ChunkCacheEntry)
	return time.Now().Before(entry.ExpiresAt)
}
'''
    code = code[:insert_pos] + contains_code + code[insert_pos:]

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
