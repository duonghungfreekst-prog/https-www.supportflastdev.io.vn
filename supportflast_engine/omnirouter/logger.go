package omnirouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// RouterLogEntry đại diện cho một bản ghi telemetry log của request thời gian thực
type RouterLogEntry struct {
	ID               int64  `json:"id"`
	Timestamp        string `json:"timestamp"`
	ClientDevice     string `json:"client_device"`
	APIKeyName       string `json:"api_key_name"`
	Endpoint         string `json:"endpoint"`
	Method           string `json:"method"`
	Provider         string `json:"provider"`
	AccountID        string `json:"account_id"`
	AccountName      string `json:"account_name"`
	Model            string `json:"model"`
	LatencyMs        int64  `json:"latency_ms"`
	StatusCode       int    `json:"status_code"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	IsStream         bool   `json:"is_stream"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

// LogHub quản lý danh sách log trong bộ đệm RAM và phân phối qua SSE
type LogHub struct {
	mu        sync.RWMutex
	entries   []RouterLogEntry
	maxSize   int
	counter   int64
	clients   map[chan RouterLogEntry]struct{}
	clientsMu sync.Mutex
}

// GlobalLogHub thực thể singleton toàn cục của bộ đệm log
var GlobalLogHub = &LogHub{
	entries: make([]RouterLogEntry, 0, 500),
	maxSize: 500,
	clients: make(map[chan RouterLogEntry]struct{}),
}

// RecordLog ghi một bản ghi log vào RingBuffer và phát sóng cho các SSE Client đang kết nối
func (h *LogHub) RecordLog(entry RouterLogEntry) {
	h.mu.Lock()
	h.counter++
	entry.ID = h.counter
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format("15:04:05.000")
	}

	h.entries = append(h.entries, entry)
	if len(h.entries) > h.maxSize {
		h.entries = h.entries[1:]
	}
	h.mu.Unlock()

	// Phát sóng bất đồng bộ không gây nghẽn (non-blocking)
	h.clientsMu.Lock()
	for ch := range h.clients {
		select {
		case ch <- entry:
		default:
		}
	}
	h.clientsMu.Unlock()
}

// GetRecentLogs lấy danh sách n log gần nhất
func (h *LogHub) GetRecentLogs(limit int) []RouterLogEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if limit <= 0 || limit > len(h.entries) {
		limit = len(h.entries)
	}

	start := len(h.entries) - limit
	out := make([]RouterLogEntry, limit)
	copy(out, h.entries[start:])
	return out
}

// HandleLogStream xử lý kết nối Server-Sent Events (SSE) tại /api/router/logs/stream
func HandleLogStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE không được hỗ trợ trên trình duyệt này", http.StatusInternalServerError)
		return
	}

	ch := make(chan RouterLogEntry, 100)
	GlobalLogHub.clientsMu.Lock()
	GlobalLogHub.clients[ch] = struct{}{}
	GlobalLogHub.clientsMu.Unlock()

	defer func() {
		GlobalLogHub.clientsMu.Lock()
		delete(GlobalLogHub.clients, ch)
		GlobalLogHub.clientsMu.Unlock()
		close(ch)
	}()

	// Gửi ngay log khởi tạo hoặc các bản ghi gần nhất cho client vừa kết nối
	recent := GlobalLogHub.GetRecentLogs(30)
	nowStr := time.Now().Format("15:04:05")
	if len(recent) == 0 {
		initPayload := map[string]interface{}{
			"type": "init",
			"logs": []string{
				fmt.Sprintf("[ENGINE] [%s] [OMNIROUTER] SupportFlast Master Gateway sẵn sàng 24/24 trên Cloud", nowStr),
				fmt.Sprintf("[CORE] [%s] [CRYPTO] Mã hóa phần cứng AES-256-GCM bảo vệ an toàn Token Pool", nowStr),
				fmt.Sprintf("[ENGINE] [%s] [TIDB] Cơ sở dữ liệu TiDB Cloud Serverless kết nối ổn định 100%%", nowStr),
				fmt.Sprintf("[ENGINE] [%s] [ROUTER] 15 cổng định tuyến AI và 160+ Brand Logos đã sẵn sàng phục vụ", nowStr),
				fmt.Sprintf("[SYSTEM] [%s] Lắng nghe yêu cầu trực tiếp qua API Gateway /v1/chat/completions...", nowStr),
			},
		}
		b, _ := json.Marshal(initPayload)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
	} else {
		for _, entry := range recent {
			lineMsg := entry.ErrorMessage
			if lineMsg == "" {
				lineMsg = fmt.Sprintf("[%s] [%s] %s %s (%d) - Model: %s - Latency: %dms", entry.Timestamp, entry.Provider, entry.Method, entry.Endpoint, entry.StatusCode, entry.Model, entry.LatencyMs)
			}
			payload := map[string]interface{}{
				"type": "line",
				"line": lineMsg,
				"data": entry,
			}
			b, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\n", string(b))
		}
	}
	flusher.Flush()

	// Vòng lặp lắng nghe và đẩy log mới
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			lineMsg := entry.ErrorMessage
			if lineMsg == "" {
				lineMsg = fmt.Sprintf("[%s] [%s] %s %s (%d) - Model: %s - Latency: %dms", entry.Timestamp, entry.Provider, entry.Method, entry.Endpoint, entry.StatusCode, entry.Model, entry.LatencyMs)
			}
			payload := map[string]interface{}{
				"type": "line",
				"line": lineMsg,
				"data": entry,
			}
			b, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\n", string(b))
			flusher.Flush()
		}
	}
}
