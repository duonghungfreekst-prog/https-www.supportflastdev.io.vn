package omnirouter

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

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

	// 5. API Quản lý API Key cho Web UI
	mux.HandleFunc("/api/router/keys", HandleManageKeys(db))
	mux.HandleFunc("/api/router/keys/", HandleManageKeys(db))
	mux.HandleFunc("/api/router/keys/toggle", HandleToggleKey(db))

	// 6. API Danh sách tài khoản AI & Tương tác (Hỗ trợ cả dạng /api/providers và /api/router/providers)
	providersHandler := HandleListProviders(db)
	mux.HandleFunc("/api/providers", providersHandler)
	mux.HandleFunc("/api/providers/", providersHandler)
	mux.HandleFunc("/api/router/providers", providersHandler)
	mux.HandleFunc("/api/router/providers/toggle", HandleToggleProvider(db))
	mux.HandleFunc("/api/providers/toggle", HandleToggleProvider(db))

	// 6.1 API Quota Hạn mức chi tiết (/api/usage/{id})
	usageHandler := HandleAccountUsage(db)
	mux.HandleFunc("/api/usage/", usageHandler)
	mux.HandleFunc("/api/usage", usageHandler)

	// 6.2 API Proxy Pools (/api/proxy-pools)
	proxyPoolsHandler := HandleProxyPools(db)
	mux.HandleFunc("/api/proxy-pools", proxyPoolsHandler)
	mux.HandleFunc("/api/proxy-pools/", proxyPoolsHandler)
	mux.HandleFunc("/api/router/proxy/switch", HandleSwitchProxy(db))
	mux.HandleFunc("/api/proxy-pools/switch", HandleSwitchProxy(db))

	// 7. API Live Telemetry Logs qua SSE
	mux.HandleFunc("/api/router/logs/stream", HandleLogStream)
	mux.HandleFunc("/api/realtime/stream-logs", HandleLogStream)
	mux.HandleFunc("/api/translator/console-logs/stream", HandleLogStream)

	// 8. API Kích hoạt đồng bộ thủ công từ data.libsql (nếu có file)
	mux.HandleFunc("/api/router/sync-libsql", HandleSyncLibSQLEndpoint(db))

	// 9. API Đồng bộ và Báo cáo dành riêng cho App Go PC / Termux
	mux.HandleFunc("/api/v1/client/sync", HandleClientSync(db))
	mux.HandleFunc("/api/v1/client/report", HandleClientReport(db))
	mux.HandleFunc("/api/v1/sync", HandleClientSync(db))
	mux.HandleFunc("/api/v1/report", HandleClientReport(db))

	// 9. Cổng Gateway Proxy trực tiếp chuẩn OpenAI
	chatHandler := HandleChatCompletions(db)
	mux.HandleFunc("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/api/v1/chat/completions", chatHandler)

	mux.HandleFunc("/v1/models", HandleModels)
	mux.HandleFunc("/api/v1/models", HandleModels)

	sttHandler := HandleAudioTranscriptions(db)
	mux.HandleFunc("/v1/audio/transcriptions", sttHandler)
	mux.HandleFunc("/api/v1/audio/transcriptions", sttHandler)
	mux.HandleFunc("/v1/models/stt", HandleSTTModels)

	// 10. Giao diện Web Dashboard Quản lý 9Router (/router, /router/, /router/index.html)
	routerUIFile := filepath.Join(uiDir, "router", "index.html")
	routerHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval' https: data: blob:; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://challenges.cloudflare.com data: blob:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.tailwindcss.com; font-src 'self' https://fonts.gstatic.com https://cdnjs.cloudflare.com data:; img-src 'self' https: data: blob:; connect-src 'self' https: ws: wss: http://127.0.0.1:* http://localhost:*;")
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

	log.Println("[ENGINE] [OMNIROUTER] ✔ Đã kích hoạt toàn bộ 10 cổng định tuyến 9Router Master Gateway")
}
