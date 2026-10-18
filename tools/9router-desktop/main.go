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
			MasterAPIKey: "sk-omnirouter-client",
			LocalPort:    20128,
		}
		data, _ := json.MarshalIndent(c.config, "", "  ")
		_ = os.WriteFile(path, data, 0644)
		return
	}
	defer f.Close()
	_ = json.NewDecoder(f).Decode(&c.config)

	if c.config.LocalPort <= 0 {
		c.config.LocalPort = 20128
	}
	if c.config.RenderURL == "" {
		c.config.RenderURL = "https://supportflastdev.io.vn"
	}
}

func main() {
	configFlag := flag.String("config", "config.json", "Đường dẫn file config.json")
	portFlag := flag.Int("port", 0, "Ghi đè cổng lắng nghe")
	flag.Parse()

	log.Println("=====================================================================")
	log.Println("         OMNIROUTER CLIENT - NATIVE DESKTOP GUI & SMART DNS          ")
	log.Println("=====================================================================")

	execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	cfgPath := *configFlag
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(execDir, cfgPath)
	}

	client := &DesktopClient{}
	client.loadConfig(cfgPath)
	if *portFlag > 0 {
		client.config.LocalPort = *portFlag
	}

	// 1. Luồng ngầm đồng bộ Token & Quota với Render Web Master mỗi 3 phút
	go client.startSyncWorker()

	// 2. Thiết lập HTTP ServeMux
	mux := http.NewServeMux()

	// Cổng Proxy AI Cho Antigravity, Cursor, Claude Code (0ms Local Routing)
	mux.HandleFunc("/v1/chat/completions", corsMiddleware(client.handleProxy))
	mux.HandleFunc("/chat/completions", corsMiddleware(client.handleProxy))
	mux.HandleFunc("/v1/models", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]string{
				{"id": "gemini-3.1-pro-high"},
				{"id": "gemini-2.5-pro"},
				{"id": "gemini-2.5-flash"},
				{"id": "claude-3-5-sonnet"},
				{"id": "claude-3-opus"},
				{"id": "gpt-4o"},
			},
		})
	}))

	// API Quản lý Giao diện Desktop & 4 Tabs
	mux.HandleFunc("/ping", corsMiddleware(client.handlePing))
	mux.HandleFunc("/api/status", corsMiddleware(client.handleStatusAPI))
	mux.HandleFunc("/api/config", corsMiddleware(client.handleConfigAPI))
	mux.HandleFunc("/api/toggle", corsMiddleware(client.handleToggleProxy))
	mux.HandleFunc("/api/sync-now", corsMiddleware(client.handleManualSyncEndpoint))

	// Endpoint hỗ trợ Tab 1 (MITM) & Tab 2 (DNS)
	mux.HandleFunc("/api/cli-tools/antigravity-mitm", corsMiddleware(client.handleMitmApi))
	mux.HandleFunc("/api/flush-dns", corsMiddleware(client.handleFlushDNS))

	// Endpoint hỗ trợ Tab 4 (Quota & Skills)
	mux.HandleFunc("/api/v1/client/quota", corsMiddleware(client.handleQuotaAPI))
	mux.HandleFunc("/api/client/sync", corsMiddleware(client.handleClientSyncAPI))
	mux.HandleFunc("/api/v1/client/sync", corsMiddleware(client.handleClientSyncAPI))

	// Phục vụ Favicon & Static assets
	mux.HandleFunc("/app.ico", func(w http.ResponseWriter, r *http.Request) {
		icoPath := filepath.Join(execDir, "app.ico")
		if data, err := os.ReadFile(icoPath); err == nil {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		icoPath := filepath.Join(execDir, "app.ico")
		if data, err := os.ReadFile(icoPath); err == nil {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	})

	// Giao diện Cyberpunk 4 Tabs chính thức
	mux.HandleFunc("/client", client.handleDesktopUI)
	mux.HandleFunc("/desktop", client.handleDesktopUI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			client.handleDesktopUI(w, r)
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", client.config.LocalPort)
	log.Printf("[DESKTOP] 🚀 OmniRouter Client đang chạy tại: http://%s", addr)
	log.Printf("[DESKTOP] 💻 Bảng Điều Khiển Native App: http://%s/", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}

	// Chạy HTTP Server trong goroutine ngầm
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Không thể mở cổng %s: %v", addr, err)
		}
	}()

	// 3. Khởi động System Tray và Cửa sổ Desktop (Message Loop Windows native)
	StartSystemTray(client)
}
