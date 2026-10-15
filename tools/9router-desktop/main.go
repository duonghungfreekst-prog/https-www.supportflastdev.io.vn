package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config cấu hình Desktop Client
type Config struct {
	RenderURL    string `json:"render_url"`
	MasterAPIKey string `json:"master_api_key"`
	LocalPort    int    `json:"local_port"`
}

// DesktopClient điều phối toàn bộ ứng dụng Desktop
type DesktopClient struct {
	config         Config
	mu             sync.RWMutex
	tokens         []TokenItem
	rrCounter      uint64
	totalReqs      uint64
	totalTokens    uint64
	reportedReqs   uint64
	reportedTokens uint64
	proxyEnabled   bool
	lastSync       time.Time
}

func (c *DesktopClient) isProxyEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.proxyEnabled
}

func (c *DesktopClient) setProxyEnabled(val bool) {
	c.mu.Lock()
	c.proxyEnabled = val
	c.mu.Unlock()
}

func (c *DesktopClient) loadConfig(path string) {
	c.proxyEnabled = true // Mặc định bật proxy
	f, err := os.Open(path)
	if err != nil {
		c.config = Config{
			RenderURL:    "https://supportflastdev.io.vn",
			MasterAPIKey: "sk-9router-xxxxxxxxxxxxxxxx",
			LocalPort:    8080,
		}
		data, _ := json.MarshalIndent(c.config, "", "  ")
		_ = os.WriteFile(path, data, 0644)
		return
	}
	defer f.Close()
	_ = json.NewDecoder(f).Decode(&c.config)

	if c.config.LocalPort <= 0 {
		c.config.LocalPort = 8080
	}
}

func main() {
	configFlag := flag.String("config", "config.json", "Đường dẫn file config.json")
	flag.Parse()

	log.Println("=====================================================================")
	log.Println("             9ROUTER DESKTOP CLIENT (SYSTEM TRAY & PROXY)            ")
	log.Println("=====================================================================")

	execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	cfgPath := *configFlag
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(execDir, cfgPath)
	}

	client := &DesktopClient{}
	client.loadConfig(cfgPath)

	// 1. Luồng ngầm đồng bộ Token & Quota với Render + TiDB mỗi 3 phút
	go client.startSyncWorker()

	// 2. Thiết lập HTTP ServeMux
	mux := http.NewServeMux()

	// Cổng Proxy cho AI Tools (Antigravity, Cursor, Claude Code)
	mux.HandleFunc("/v1/chat/completions", client.handleProxy)
	mux.HandleFunc("/chat/completions", client.handleProxy)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]string{
				{"id": "gemini-3.1-pro-high"},
				{"id": "gemini-2.5-pro"},
				{"id": "gemini-2.5-flash"},
				{"id": "claude-3-5-sonnet"},
				{"id": "claude-3-opus"},
			},
		})
	})

	// API Quản lý Giao diện Desktop
	mux.HandleFunc("/api/status", client.handleStatusAPI)
	mux.HandleFunc("/api/config", client.handleConfigAPI)
	mux.HandleFunc("/api/toggle", client.handleToggleProxy)
	mux.HandleFunc("/api/sync-now", client.handleManualSyncEndpoint)

	// Giao diện Desktop UI
	mux.HandleFunc("/desktop", client.handleDesktopUI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/desktop", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", client.config.LocalPort)
	log.Printf("[DESKTOP] 🚀 Proxy Core đang chạy tại: http://%s/v1", addr)
	log.Printf("[DESKTOP] 💻 Bảng Điều Khiển: http://%s/desktop", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Chạy HTTP Server trong goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Không thể mở cổng %s: %v", addr, err)
		}
	}()

	// 3. Khởi động System Tray và Cửa sổ Desktop (Chạy Message Loop của Windows)
	StartSystemTray(client)
}
