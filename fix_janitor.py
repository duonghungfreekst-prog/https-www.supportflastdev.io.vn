import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'r', encoding='utf-8') as f:
    content = f.read()

# Add StartJanitor logic to stream.go
janitor_code = '''
func init() {
	go globalChunkCache.StartJanitor()
}

func (c *ChunkCache) StartJanitor() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		var next *list.Element
		for e := c.lruList.Front(); e != nil; e = next {
			next = e.Next()
			entry := e.Value.(*ChunkCacheEntry)
			if now.After(entry.ExpiresAt) {
				c.lruList.Remove(e)
				delete(c.entries, entry.Key)
				c.curBytes -= int64(len(entry.Data))
			}
		}
		c.mu.Unlock()
	}
}

func (c *ChunkCache) Get(key string) ([]byte, bool) {'''

content = content.replace('func (c *ChunkCache) Get(key string) ([]byte, bool) {', janitor_code)

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'w', encoding='utf-8') as f:
    f.write(content)
