package cache

import (
	"log"
	"runtime"
	"sync"
	"time"
)

// MemoryStats chứa thông tin thống kê tài nguyên bộ nhớ
type MemoryStats struct {
	AllocMB          float64   `json:"alloc_mb"`
	TotalAllocMB     float64   `json:"total_alloc_mb"`
	SysMB            float64   `json:"sys_mb"`
	NumGC            uint32    `json:"num_gc"`
	Goroutines       int       `json:"goroutines"`
	GrowthStreak     int       `json:"growth_streak"`
	ExceedsThreshold bool      `json:"exceeds_threshold"`
	Timestamp        time.Time `json:"timestamp"`
}

// MemoryMonitor quản lý giám sát tài nguyên bộ nhớ theo Rule 7.4 & Rule 7.1
type MemoryMonitor struct {
	mu             sync.Mutex
	lastAllocBytes uint64
	growthCount    int
	thresholdMB    float64
	stopChan       chan struct{}
	isClosed       bool
}

// NewMemoryMonitor tạo bộ giám sát tài nguyên bộ nhớ
func NewMemoryMonitor(thresholdMB float64) *MemoryMonitor {
	if thresholdMB <= 0 {
		thresholdMB = 500.0 // Ngưỡng 500MB quy định tại Rule 7.1
	}
	return &MemoryMonitor{
		thresholdMB: thresholdMB,
		stopChan:    make(chan struct{}),
	}
}

// CheckAndLog đo lường các chỉ số runtime.ReadMemStats() và ghi log chuẩn Rule 7.4
func (m *MemoryMonitor) CheckAndLog() MemoryStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return m.evaluateMetrics(ms.Alloc, ms.TotalAlloc, ms.Sys, ms.NumGC, runtime.NumGoroutine())
}

// CheckWithMetrics nhận các thông số trực tiếp (dùng cho testing hoặc custom telemetry)
func (m *MemoryMonitor) CheckWithMetrics(allocBytes uint64, totalAllocBytes uint64, sysBytes uint64, numGC uint32, goroutines int) MemoryStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.evaluateMetrics(allocBytes, totalAllocBytes, sysBytes, numGC, goroutines)
}

func (m *MemoryMonitor) evaluateMetrics(allocBytes uint64, totalAllocBytes uint64, sysBytes uint64, numGC uint32, goroutines int) MemoryStats {
	allocMB := float64(allocBytes) / (1024 * 1024)
	totalAllocMB := float64(totalAllocBytes) / (1024 * 1024)
	sysMB := float64(sysBytes) / (1024 * 1024)

	// 1. Log định kỳ bắt buộc: Alloc, NumGC, Goroutine count (Rule 7.4 & Rule 4.4)
	log.Printf("[ENGINE] [MONITOR] Memory Stats: Alloc=%.2fMB, TotalAlloc=%.2fMB, Sys=%.2fMB, NumGC=%d, Goroutines=%d",
		allocMB, totalAllocMB, sysMB, numGC, goroutines)

	// 2. Phát hiện tăng trưởng liên tục qua 3 lần đo -> log WARN + notify SIEM (Rule 7.4)
	if m.lastAllocBytes > 0 && allocBytes > m.lastAllocBytes {
		m.growthCount++
		if m.growthCount >= 3 {
			log.Printf("[ENGINE] [WARN] [SIEM] Cảnh báo: Bộ nhớ Alloc tăng liên tục qua %d lần đo! Alloc hiện tại: %.2fMB. Kiểm tra rò rỉ bộ nhớ (Memory Leak).",
				m.growthCount, allocMB)
		}
	} else if allocBytes < m.lastAllocBytes {
		m.growthCount = 0
	}
	m.lastAllocBytes = allocBytes

	// 3. Cảnh báo khi Alloc vượt ngưỡng 500MB (Rule 7.1)
	exceeds := allocMB > m.thresholdMB
	if exceeds {
		log.Printf("[ENGINE] [WARN] [SIEM] Cảnh báo: Bộ nhớ Alloc (%.2fMB) vượt ngưỡng an toàn %.0fMB!", allocMB, m.thresholdMB)
	}

	return MemoryStats{
		AllocMB:          allocMB,
		TotalAllocMB:     totalAllocMB,
		SysMB:            sysMB,
		NumGC:            numGC,
		Goroutines:       goroutines,
		GrowthStreak:     m.growthCount,
		ExceedsThreshold: exceeds,
		Timestamp:        time.Now(),
	}
}

// Start kích hoạt vòng lặp giám sát định kỳ
func (m *MemoryMonitor) Start(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.CheckAndLog()
			case <-m.stopChan:
				return
			}
		}
	}()
}

// Stop dừng giám sát định kỳ
func (m *MemoryMonitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isClosed {
		m.isClosed = true
		close(m.stopChan)
	}
}

// DefaultMemoryMonitor phiên bản dùng chung mặc định của engine
var DefaultMemoryMonitor = NewMemoryMonitor(500.0)

// LogMemoryStats ghi log và trả về thông số bộ nhớ hiện tại
func LogMemoryStats() MemoryStats {
	return DefaultMemoryMonitor.CheckAndLog()
}
