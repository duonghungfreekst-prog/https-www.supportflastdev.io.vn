package omnirouter

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	routerGzipOnce  sync.Once
	routerRawBytes  []byte
	routerGzipBytes []byte
	routerETag      string
)

// initRouterCache nén sẵn file router HTML vào RAM để phục vụ 0ms
func initRouterCache(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	routerRawBytes = data

	// Băm ETag SHA-256
	h := sha256.Sum256(data)
	routerETag = fmt.Sprintf(`"%s"`, hex.EncodeToString(h[:16]))

	// Nén Gzip Level 9
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err == nil {
		_, _ = gz.Write(data)
		_ = gz.Close()
		routerGzipBytes = buf.Bytes()
		log.Printf("[ENGINE] [OMNIROUTER] ✔ Đã nén sẵn router/index.html lên RAM: %d KB -> %d KB (ETag: %s)", len(routerRawBytes)/1024, len(routerGzipBytes)/1024, routerETag)
	}
}

// RegisterOmniRouterRoutes đăng ký toàn bộ routes của 9Router vào ServeMux của hệ thống
func RegisterOmniRouterRoutes(mux *http.ServeMux, db *sql.DB, isTiDB bool, uiDir string) {
	// 1. Khởi tạo cấu trúc lưu trữ bảng
	if err := InitStorage(db, isTiDB); err != nil {
		log.Printf("[ENGINE] [OMNIROUTER] [WARN] Khởi tạo bảng OmniRouter: %v", err)
	}

	// 2. Tự động nạp hạt nhân 19 tài khoản AI vào TiDB Cloud/SQLite nếu bảng providers đang trống
	go func() {
		providers := GetCachedActiveProviders()
		if len(providers) == 0 {
			log.Println("[ENGINE] [OMNIROUTER] Kho tài khoản rỗng, đang tự động nạp cố định 19 tài khoản AI vào TiDB/SQLite...")
			if err := SeedEmbeddedDataToDB(db); err != nil {
				log.Printf("[ENGINE] [OMNIROUTER] [WARN] Seed hạt nhân thất bại: %v", err)
			}
		}
	}()

	// 3. Khởi chạy worker tự động làm mới token OAuth ngầm
	StartAutoTokenRefreshWorker(context.Background(), db)

	// 4. API Ping siêu nhẹ (0 RU TiDB, 0.1ms, giữ Render thức 24/24)
	mux.HandleFunc("/ping", HandlePing)

	// 5. API Quản lý API Key cho Web UI & CLI Tools (Dùng /api/router/keys)
	keysHandler := HandleManageKeys(db)
	mux.HandleFunc("/api/router/keys", keysHandler)
	mux.HandleFunc("/api/router/keys/", keysHandler)
	mux.HandleFunc("/api/router/keys/toggle", HandleToggleKey(db))

	// 6. API Danh sách tài khoản AI & Tương tác (Hỗ trợ cả dạng /api/providers và /api/router/providers)
	providersHandler := HandleListProviders(db)
	mux.HandleFunc("/api/providers", providersHandler)
	mux.HandleFunc("/api/providers/", providersHandler)
	mux.HandleFunc("/api/router/providers", providersHandler)
	mux.HandleFunc("/api/router/providers/", providersHandler)
	mux.HandleFunc("/api/router/providers/toggle", HandleToggleProvider(db))
	mux.HandleFunc("/api/providers/toggle", HandleToggleProvider(db))

	// 6.0 API Available Models của Provider (/api/models/provider)
	providerModelsHandler := HandleProviderModels(db)
	mux.HandleFunc("/api/models/provider", providerModelsHandler)
	mux.HandleFunc("/api/models/provider/", providerModelsHandler)
	mux.HandleFunc("/api/router/models/provider", providerModelsHandler)
	mux.HandleFunc("/api/router/models/provider/", providerModelsHandler)

	// 6.1 API Quota Hạn mức chi tiết (/api/usage/{id}, /api/usage/stats, /api/usage-stats)
	usageHandler := HandleAccountUsage(db)
	usageStatsHandler := HandleUsageStats(db)
	mux.HandleFunc("/api/usage/stats", usageStatsHandler)
	mux.HandleFunc("/api/usage-stats", usageStatsHandler)
	mux.HandleFunc("/api/usage/", usageHandler)
	mux.HandleFunc("/api/usage", usageHandler)

	// 6.2 API Proxy Pools (/api/proxy-pools)
	proxyPoolsHandler := HandleProxyPools(db)
	mux.HandleFunc("/api/proxy-pools", proxyPoolsHandler)
	mux.HandleFunc("/api/proxy-pools/", proxyPoolsHandler)
	mux.HandleFunc("/api/router/proxy/switch", HandleSwitchProxy(db))
	mux.HandleFunc("/api/proxy-pools/switch", HandleSwitchProxy(db))

	// 6.3 API CLI Tools & Model Aliases (/api/cli-tools/antigravity-mitm/alias...)
	mitmAliasesHandler := HandleMitmAliases(db)
	mitmStatusHandler := HandleMitmStatus(db)
	mux.HandleFunc("/api/cli-tools/antigravity-mitm/alias", mitmAliasesHandler)
	mux.HandleFunc("/api/cli-tools/antigravity-mitm", mitmStatusHandler)
	mux.HandleFunc("/api/cli-tools/all-statuses", mitmStatusHandler)

	// 7. API Live Telemetry Logs qua SSE & JSON fallback
	unifiedLogHandler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			HandleLogStream(w, r)
		} else {
			HandleRealtimeLogsJSON(w, r)
		}
	}
	mux.HandleFunc("/api/router/logs/stream", HandleLogStream)
	mux.HandleFunc("/api/realtime/stream-logs", unifiedLogHandler)
	mux.HandleFunc("/api/logs", unifiedLogHandler)
	mux.HandleFunc("/api/translator/console-logs", unifiedLogHandler)
	mux.HandleFunc("/api/translator/console-logs/stream", HandleLogStream)

	// 8. API Kích hoạt đồng bộ thủ công từ data.libsql (nếu có file)
	mux.HandleFunc("/api/router/sync-libsql", HandleSyncLibSQLEndpoint(db))

	// 9. API Đồng bộ, Cấu hình MITM & DNS và Báo cáo dành riêng cho App PC / Termux / CMD
	clientConfigHandler := HandleClientConfig(db)
	mux.HandleFunc("/api/v1/client/config", clientConfigHandler)
	mux.HandleFunc("/api/v1/client/config.json", clientConfigHandler)
	mux.HandleFunc("/api/v1/client/setup.bat", clientConfigHandler)
	mux.HandleFunc("/api/v1/client/setup.sh", clientConfigHandler)
	mux.HandleFunc("/api/v1/config", clientConfigHandler)
	mux.HandleFunc("/api/v1/mitm-dns", clientConfigHandler)
	mux.HandleFunc("/api/v1/client/sync", HandleClientSync(db))
	mux.HandleFunc("/api/v1/client/report", HandleClientReport(db))
	mux.HandleFunc("/api/v1/sync", HandleClientSync(db))
	mux.HandleFunc("/api/v1/report", HandleClientReport(db))

	// 9.1 API Tra cứu Quota & Ping DNS trực tiếp qua CMD / Terminal / cURL
	clientQuotaHandler := HandleClientQuotaCLI(db)
	mux.HandleFunc("/api/v1/client/quota", clientQuotaHandler)
	mux.HandleFunc("/api/v1/client/quota.bat", clientQuotaHandler)
	mux.HandleFunc("/api/v1/client/ping", clientQuotaHandler)
	mux.HandleFunc("/api/v1/quota", clientQuotaHandler)
	mux.HandleFunc("/api/v1/ping", clientQuotaHandler)

	// 10. Cổng Gateway Proxy trực tiếp chuẩn OpenAI
	chatHandler := HandleChatCompletions(db)
	mux.HandleFunc("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/api/v1/chat/completions", chatHandler)

	mux.HandleFunc("/v1/models", HandleModels)
	mux.HandleFunc("/api/v1/models", HandleModels)

	sttHandler := HandleAudioTranscriptions(db)
	mux.HandleFunc("/v1/audio/transcriptions", sttHandler)
	mux.HandleFunc("/api/v1/audio/transcriptions", sttHandler)
	mux.HandleFunc("/v1/models/stt", HandleSTTModels)

	// 11. Phục vụ tĩnh thư mục Logo nhà cung cấp (/providers/ và /router/providers/)
	providersCandidates := []string{
		filepath.Join(uiDir, "providers"),
		filepath.Join(uiDir, "router", "providers"),
		filepath.Join("..", uiDir, "providers"),
		filepath.Join("..", uiDir, "router", "providers"),
		filepath.Join("..", "..", "supportflast_ui", "providers"),
		filepath.Join("..", "..", "supportflast_ui", "router", "providers"),
		filepath.Join("..", "supportflast_ui", "providers"),
		filepath.Join("..", "supportflast_ui", "router", "providers"),
		filepath.Join("supportflast_ui", "providers"),
		filepath.Join("supportflast_ui", "router", "providers"),
		"providers",
	}
	var resolvedProvidersDir string
	for _, cand := range providersCandidates {
		if info, err := os.Stat(cand); err == nil && info.IsDir() {
			resolvedProvidersDir = cand
			break
		}
	}
	if resolvedProvidersDir != "" {
		fileServer := http.FileServer(http.Dir(resolvedProvidersDir))
		mux.Handle("/providers/", http.StripPrefix("/providers/", fileServer))
		mux.Handle("/router/providers/", http.StripPrefix("/router/providers/", fileServer))
		log.Printf("[ENGINE] [OMNIROUTER] ✔ Đã gắn cổng phục vụ tĩnh 160+ Logo nhà cung cấp từ: %s", resolvedProvidersDir)
	}

	// 12. Giao diện Web Dashboard Quản lý 9Router với Pre-compressed Gzip & ETag HTTP 304
	routerUIFile := filepath.Join(uiDir, "router", "index.html")
	routerHandler := func(w http.ResponseWriter, r *http.Request) {
		// Nạp cache nén sẵn một lần duy nhất
		routerGzipOnce.Do(func() {
			initRouterCache(routerUIFile)
		})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval' https: data: blob:; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://challenges.cloudflare.com data: blob:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.tailwindcss.com; font-src 'self' https://fonts.gstatic.com https://cdnjs.cloudflare.com data:; img-src 'self' https: data: blob:; connect-src 'self' https: ws: wss: http://127.0.0.1:* http://localhost:*;")

		// Kiểm tra ETag HTTP 304 Not Modified
		if routerETag != "" {
			w.Header().Set("ETag", routerETag)
			if r.Header.Get("If-None-Match") == routerETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-cache, must-revalidate")

		// Phục vụ Gzip nén sẵn trực tiếp từ RAM nếu client hỗ trợ
		if len(routerGzipBytes) > 0 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(routerGzipBytes)
			return
		}

		if len(routerRawBytes) > 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(routerRawBytes)
			return
		}

		if _, err := os.Stat(routerUIFile); err == nil {
			http.ServeFile(w, r, routerUIFile)
			return
		}

		// Fallback nếu chưa copy file UI
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>9Router Gateway Dashboard</title><meta charset="utf-8"/></head>
<body style="background:#0f172a;color:#f8fafc;font-family:sans-serif;padding:40px;">
	<h1>⚡ 9Router Master Gateway</h1>
	<p>Web Dashboard đang khởi động...</p>
</body>
</html>`))
	}

	mux.HandleFunc("/router", routerHandler)
	mux.HandleFunc("/router/", routerHandler)
	mux.HandleFunc("/router/index.html", routerHandler)

	log.Println("[ENGINE] [OMNIROUTER] ✔ Đã kích hoạt toàn bộ 15 cổng định tuyến 9Router Master Gateway")
}
