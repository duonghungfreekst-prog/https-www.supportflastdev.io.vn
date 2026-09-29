package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestLRUBasicGetSet kiểm tra các hàm Get và Set cơ bản
func TestLRUBasicGetSet(t *testing.T) {
	c := NewLRUCache(10, 0)
	defer c.Close()

	c.Set("k1", "v1", 10*time.Second)
	c.Set("k2", "v2", 10*time.Second)

	val, ok := c.Get("k1")
	if !ok || val != "v1" {
		t.Fatalf("Mong đợi 'v1', nhận được: %v", val)
	}

	val, ok = c.Get("k2")
	if !ok || val != "v2" {
		t.Fatalf("Mong đợi 'v2', nhận được: %v", val)
	}

	_, ok = c.Get("k3")
	if ok {
		t.Fatalf("Không nên tìm thấy key không tồn tại")
	}
}

// TestLRUEviction kiểm tra việc loại bỏ phần tử cũ nhất (LRU eviction) khi đạt maxEntries
func TestLRUEviction(t *testing.T) {
	c := NewLRUCache(3, 0) // Dung lượng tối đa 3 phần tử
	defer c.Close()

	c.Set("a", 1, 10*time.Second)
	c.Set("b", 2, 10*time.Second)
	c.Set("c", 3, 10*time.Second)

	// Truy cập "a" để "a" trở thành Most Recently Used (MRU). Thứ tự LRU lúc này: "b" (cũ nhất), "c", "a"
	c.Get("a")

	// Thêm phần tử thứ 4 -> "b" phải bị loại bỏ
	c.Set("d", 4, 10*time.Second)

	if _, ok := c.Get("b"); ok {
		t.Fatalf("Key 'b' lẽ ra phải bị loại bỏ theo chính sách LRU")
	}

	if _, ok := c.Get("a"); !ok {
		t.Fatalf("Key 'a' phải còn tồn tại vì vừa được truy cập")
	}

	if _, ok := c.Get("c"); !ok {
		t.Fatalf("Key 'c' phải còn tồn tại")
	}

	if _, ok := c.Get("d"); !ok {
		t.Fatalf("Key 'd' phải còn tồn tại")
	}

	if c.Len() != 3 {
		t.Fatalf("Kích thước cache mong đợi là 3, nhận được: %d", c.Len())
	}
}

// TestTTLExpiry kiểm tra phần tử hết hạn theo thời gian sống TTL
func TestTTLExpiry(t *testing.T) {
	c := NewLRUCache(10, 0)
	defer c.Close()

	// Set key với TTL rất ngắn: 50ms
	c.Set("short_lived", "data", 50*time.Millisecond)

	val, ok := c.Get("short_lived")
	if !ok || val != "data" {
		t.Fatalf("Key phải còn tồn tại ngay sau khi set")
	}

	// Đợi hết hạn
	time.Sleep(80 * time.Millisecond)

	// Truy cập sau khi hết hạn -> phải trả về false và bị xóa khỏi cache
	val, ok = c.Get("short_lived")
	if ok {
		t.Fatalf("Key 'short_lived' lẽ ra phải hết hạn, nhưng vẫn nhận được: %v", val)
	}

	if c.Len() != 0 {
		t.Fatalf("Cache phải rỗng sau khi key hết hạn bị dọn dẹp, Len = %d", c.Len())
	}
}

// TestCleanExpiredBackground kiểm tra hàm dọn dẹp chủ động CleanExpired
func TestCleanExpiredBackground(t *testing.T) {
	c := NewLRUCache(10, 0)
	defer c.Close()

	c.Set("k1", "data1", 30*time.Millisecond)
	c.Set("k2", "data2", 300*time.Millisecond)
	c.Set("k3", "data3", 30*time.Millisecond)

	time.Sleep(50 * time.Millisecond)

	// CleanExpired phải loại bỏ k1 và k3
	evicted := c.CleanExpired()
	if evicted != 2 {
		t.Fatalf("Mong đợi dọn dẹp 2 keys hết hạn, thực tế: %d", evicted)
	}

	if c.Len() != 1 {
		t.Fatalf("Mong đợi còn lại 1 key (k2), thực tế: %d", c.Len())
	}

	if val, ok := c.Get("k2"); !ok || val != "data2" {
		t.Fatalf("Key k2 vẫn phải còn sống")
	}
}

// TestInvalidateAndPrefix kiểm tra Invalidate và InvalidatePrefix
func TestInvalidateAndPrefix(t *testing.T) {
	c := NewLRUCache(10, 0)
	defer c.Close()

	c.Set("app:1", "item1", 10*time.Second)
	c.Set("app:2", "item2", 10*time.Second)
	c.Set("system:status", "ok", 10*time.Second)

	// Invalidate single key
	deleted := c.Invalidate("system:status")
	if !deleted {
		t.Fatalf("Mong đợi Invalidate thành công cho 'system:status'")
	}
	if _, ok := c.Get("system:status"); ok {
		t.Fatalf("Key 'system:status' không được tồn tại sau khi Invalidate")
	}

	// InvalidatePrefix
	evicted := c.InvalidatePrefix("app:")
	if evicted != 2 {
		t.Fatalf("Mong đợi xóa 2 keys với prefix 'app:', nhận được: %d", evicted)
	}
	if c.Len() != 0 {
		t.Fatalf("Cache phải rỗng sau khi xóa tiền tố, Len = %d", c.Len())
	}
}

// TestThreadSafety kiểm tra an toàn đa luồng (Concurrent Read/Write)
func TestThreadSafety(t *testing.T) {
	c := NewLRUCache(100, 0)
	defer c.Close()

	var wg sync.WaitGroup
	numRoutines := 20
	opsPerRoutine := 100

	// Ghi đồng thời
	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				key := fmt.Sprintf("key_%d", (workerID*opsPerRoutine+j)%50)
				c.Set(key, j, 5*time.Second)
			}
		}(i)
	}

	// Đọc đồng thời
	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				key := fmt.Sprintf("key_%d", (workerID*opsPerRoutine+j)%50)
				c.Get(key)
			}
		}(i)
	}

	wg.Wait()

	if c.Len() > 100 {
		t.Fatalf("Cache vượt quá giới hạn maxEntries 100: %d", c.Len())
	}
}

// TestMaxEntriesLimitRule kiểm tra giới hạn trần 10.000 entries theo Rule 7.2
func TestMaxEntriesLimitRule(t *testing.T) {
	c := NewLRUCache(20000, 0) // Yêu cầu vượt 10.000
	defer c.Close()

	if c.maxEntries != MaxEntriesLimit {
		t.Fatalf("Mong đợi maxEntries bị giới hạn tại %d, thực tế: %d", MaxEntriesLimit, c.maxEntries)
	}
}

// TestMemoryMonitorCheck kiểm tra bộ giám sát bộ nhớ
func TestMemoryMonitorCheck(t *testing.T) {
	mon := NewMemoryMonitor(500.0)
	stats := mon.CheckAndLog()

	if stats.AllocMB < 0 {
		t.Fatalf("AllocMB không hợp lệ: %f", stats.AllocMB)
	}
	if stats.Goroutines <= 0 {
		t.Fatalf("Số Goroutine phải lớn hơn 0")
	}
}

// TestMemoryMonitorConsecutiveGrowth kiểm tra cảnh báo tăng trưởng bộ nhớ liên tục qua 3 lần đo (Rule 7.4)
func TestMemoryMonitorConsecutiveGrowth(t *testing.T) {
	mon := NewMemoryMonitor(500.0)

	// Lần 1: Khởi tạo 10MB
	s1 := mon.CheckWithMetrics(10*1024*1024, 10*1024*1024, 20*1024*1024, 1, 5)
	if s1.GrowthStreak != 0 {
		t.Fatalf("Lần đầu tiên chưa có streak tăng trưởng, got: %d", s1.GrowthStreak)
	}

	// Lần 2: Tăng lên 20MB -> streak = 1
	s2 := mon.CheckWithMetrics(20*1024*1024, 20*1024*1024, 30*1024*1024, 1, 5)
	if s2.GrowthStreak != 1 {
		t.Fatalf("Lần 2 phải có streak = 1, got: %d", s2.GrowthStreak)
	}

	// Lần 3: Tăng lên 30MB -> streak = 2
	s3 := mon.CheckWithMetrics(30*1024*1024, 30*1024*1024, 40*1024*1024, 1, 5)
	if s3.GrowthStreak != 2 {
		t.Fatalf("Lần 3 phải có streak = 2, got: %d", s3.GrowthStreak)
	}

	// Lần 4: Tăng lên 40MB -> streak = 3 (Kích hoạt cảnh báo WARN + SIEM)
	s4 := mon.CheckWithMetrics(40*1024*1024, 40*1024*1024, 50*1024*1024, 1, 5)
	if s4.GrowthStreak != 3 {
		t.Fatalf("Lần 4 phải có streak = 3, got: %d", s4.GrowthStreak)
	}

	// Lần 5: Giảm xuống 25MB -> streak reset về 0
	s5 := mon.CheckWithMetrics(25*1024*1024, 45*1024*1024, 50*1024*1024, 2, 5)
	if s5.GrowthStreak != 0 {
		t.Fatalf("Khi bộ nhớ giảm, streak phải reset về 0, got: %d", s5.GrowthStreak)
	}
}

// TestMemoryMonitorThresholdExceeded kiểm tra cảnh báo khi Alloc vượt 500MB (Rule 7.1)
func TestMemoryMonitorThresholdExceeded(t *testing.T) {
	mon := NewMemoryMonitor(500.0)

	// Đo dưới ngưỡng (100MB)
	sNormal := mon.CheckWithMetrics(100*1024*1024, 100*1024*1024, 200*1024*1024, 2, 8)
	if sNormal.ExceedsThreshold {
		t.Fatalf("100MB không được vượt ngưỡng 500MB")
	}

	// Đo vượt ngưỡng (550MB)
	sExceed := mon.CheckWithMetrics(550*1024*1024, 550*1024*1024, 700*1024*1024, 2, 8)
	if !sExceed.ExceedsThreshold {
		t.Fatalf("550MB phải kích hoạt cảnh báo ExceedsThreshold")
	}
}
