package cache

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

const (
	// MaxEntriesLimit Giới hạn tối đa 10.000 entries theo Rule 7.2
	MaxEntriesLimit = 10000

	// DefaultCleanupInterval Chu kỳ tự động quét dọn dẹp các key hết hạn (30 giây)
	DefaultCleanupInterval = 30 * time.Second
)

// cacheEntry đại diện cho một phần tử được lưu trong cache
type cacheEntry struct {
	key       string
	value     interface{}
	expiresAt time.Time
}

// isExpired kiểm tra xem entry đã hết hạn chưa
func (e *cacheEntry) isExpired(now time.Time) bool {
	if e.expiresAt.IsZero() {
		return false
	}
	return now.After(e.expiresAt)
}

// LRUCache cấu trúc bộ nhớ đệm Thread-safe LRU Cache có TTL
type LRUCache struct {
	mu         sync.RWMutex
	maxEntries int
	items      map[string]*list.Element
	evictList  *list.List
	stopClean  chan struct{}
	isClosed   bool
}

// NewLRUCache khởi tạo đối tượng LRUCache mới
// maxEntries: Giới hạn số lượng phần tử (tối đa 10.000 theo Rule 7.2)
// cleanupInterval: Chu kỳ tự động quét dọn dẹp các key hết hạn (nếu <= 0, dùng DefaultCleanupInterval)
func NewLRUCache(maxEntries int, cleanupInterval time.Duration) *LRUCache {
	if maxEntries <= 0 || maxEntries > MaxEntriesLimit {
		maxEntries = MaxEntriesLimit
	}
	if cleanupInterval <= 0 {
		cleanupInterval = DefaultCleanupInterval
	}

	c := &LRUCache{
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element),
		evictList:  list.New(),
		stopClean:  make(chan struct{}),
	}

	// Khởi động goroutine dọn dẹp ngầm các key hết hạn
	go c.startJanitor(cleanupInterval)

	return c
}

// Set lưu một giá trị vào cache với thời gian sống TTL cụ thể (Rule 7.2)
func (c *LRUCache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// Nếu key đã tồn tại: Cập nhật giá trị và đưa lên đầu danh sách (MRU)
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.value = value
		entry.expiresAt = expiresAt
		return
	}

	// Nếu bộ nhớ đệm đã đầy (đạt maxEntries), loại bỏ phần tử cũ nhất (LRU)
	for c.evictList.Len() >= c.maxEntries {
		c.removeOldest()
	}

	// Thêm entry mới vào đầu danh sách
	entry := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: expiresAt,
	}
	elem := c.evictList.PushFront(entry)
	c.items[key] = elem
}

// Get lấy một giá trị từ cache. Trả về (value, true) nếu tìm thấy và chưa hết hạn, ngược lại (nil, false)
func (c *LRUCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)
	// Kiểm tra nếu entry đã hết hạn -> xóa khỏi cache ngay lập tức (eviction on expiry)
	if entry.isExpired(time.Now()) {
		c.removeElement(elem)
		return nil, false
	}

	// Đưa phần tử được truy cập lên đầu danh sách (MRU)
	c.evictList.MoveToFront(elem)
	return entry.value, true
}

// Peek lấy giá trị mà không làm thay đổi thứ tự LRU (chỉ đọc)
func (c *LRUCache) Peek(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)
	if entry.isExpired(time.Now()) {
		return nil, false
	}

	return entry.value, true
}

// Delete xóa một key khỏi cache
func (c *LRUCache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.removeElement(elem)
		return true
	}
	return false
}

// Invalidate làm mới / xóa bỏ một key khỏi cache (alias của Delete)
func (c *LRUCache) Invalidate(key string) bool {
	return c.Delete(key)
}

// InvalidatePrefix xóa toàn bộ các key có tiền tố (prefix) bắt đầu bằng chuỗi truyền vào
func (c *LRUCache) InvalidatePrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	evicted := 0
	for elem := c.evictList.Back(); elem != nil; {
		prev := elem.Prev()
		entry := elem.Value.(*cacheEntry)
		if strings.HasPrefix(entry.key, prefix) {
			c.removeElement(elem)
			evicted++
		}
		elem = prev
	}
	return evicted
}

// Clear xóa sạch toàn bộ phần tử trong cache
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.evictList.Init()
}

// Len trả về số lượng phần tử hiện tại trong cache
func (c *LRUCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// CleanExpired duyệt và loại bỏ tất cả các key đã hết thời gian sống TTL
func (c *LRUCache) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	evicted := 0

	for elem := c.evictList.Back(); elem != nil; {
		prev := elem.Prev()
		entry := elem.Value.(*cacheEntry)
		if entry.isExpired(now) {
			c.removeElement(elem)
			evicted++
		}
		elem = prev
	}
	return evicted
}

// removeOldest loại bỏ phần tử cuối cùng (LRU - Least Recently Used)
func (c *LRUCache) removeOldest() {
	elem := c.evictList.Back()
	if elem != nil {
		c.removeElement(elem)
	}
}

// removeElement giải phóng một element khỏi cả list và map tra cứu
func (c *LRUCache) removeElement(elem *list.Element) {
	c.evictList.Remove(elem)
	entry := elem.Value.(*cacheEntry)
	delete(c.items, entry.key)
}

// startJanitor vòng lặp dọn dẹp định kỳ các entry hết hạn
func (c *LRUCache) startJanitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.CleanExpired()
		case <-c.stopClean:
			return
		}
	}
}

// Close giải phóng tài nguyên và dừng goroutine dọn dẹp ngầm
func (c *LRUCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isClosed {
		c.isClosed = true
		close(c.stopClean)
	}
}

// DefaultCache Thể hiện mặc định dùng chung toàn bộ engine
var DefaultCache = NewLRUCache(MaxEntriesLimit, DefaultCleanupInterval)

// Get hàm tiện ích sử dụng DefaultCache
func Get(key string) (interface{}, bool) {
	return DefaultCache.Get(key)
}

// Set hàm tiện ích sử dụng DefaultCache
func Set(key string, value interface{}, ttl time.Duration) {
	DefaultCache.Set(key, value, ttl)
}

// Invalidate hàm tiện ích làm mới key trên DefaultCache
func Invalidate(key string) bool {
	return DefaultCache.Invalidate(key)
}

// Clear hàm tiện ích xóa toàn bộ DefaultCache
func Clear() {
	DefaultCache.Clear()
}

// Len hàm tiện ích lấy kích thước DefaultCache
func Len() int {
	return DefaultCache.Len()
}
