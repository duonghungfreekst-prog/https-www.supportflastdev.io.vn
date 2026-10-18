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

// safeRegister đăng ký route an toàn, tránh panic nếu pattern đã tồn tại trên ServeMux của Host Server
func safeRegister(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	defer func() {
		_ = recover()
	}()
	mux.HandleFunc(pattern, handler)
}

// safeRegisterHandler đăng ký http.Handler an toàn, tránh panic nếu pattern đã tồn tại
func safeRegisterHandler(mux *http.ServeMux, pattern string, handler http.Handler) {
	defer func() {
		_ = recover()
	}()
	mux.Handle(pattern, handler)
}

// RegisterOmniRouterRoutes đăng ký toàn bộ routes của 9Router vào ServeMux của hệ thống
func RegisterOmniRouterRoutes(mux *http.ServeMux, db *sql.DB, isTiDB bool, uiDir string) {
	// 1. Khởi tạo cấu trúc lưu trữ bảng
	if err := InitStorage(db, isTiDB); err != nil {
		log.Printf("[ENGINE] [OMNIROUTER] [WARN] Khởi tạo bảng OmniRouter: %v", err)
	}
	SetSkillsDB(db)

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

	// 4. API Ping siêu nhẹ & Health check
	safeRegister(mux, "/ping", HandlePing)
	safeRegister(mux, "/api/health", HandleHealth)
	safeRegister(mux, "/api/router/health", HandleHealth)

	// 4.1 Cài đặt hệ thống & CSDL TiDB Cloud
	settingsHandler := HandleSettings(db)
	safeRegister(mux, "/api/settings", settingsHandler)
	safeRegister(mux, "/api/settings/", settingsHandler)
	safeRegister(mux, "/api/router/settings", settingsHandler)
	safeRegister(mux, "/api/router/settings/", settingsHandler)
	safeRegister(mux, "/api/settings/database", HandleSettingsDatabase(db))
	safeRegister(mux, "/api/router/settings/database", HandleSettingsDatabase(db))

	// 5. API Quản lý API Key cho Web UI & CLI Tools (Hỗ trợ cả /api/keys và /api/router/keys)
	keysHandler := HandleManageKeys(db)
	safeRegister(mux, "/api/keys", keysHandler)
	safeRegister(mux, "/api/keys/", keysHandler)
	safeRegister(mux, "/api/router/keys", keysHandler)
	safeRegister(mux, "/api/router/keys/", keysHandler)
	safeRegister(mux, "/api/router/keys/toggle", HandleToggleKey(db))

	// 6. API Danh sách tài khoản AI & Tương tác (Hỗ trợ cả dạng /api/providers và /api/router/providers)
	providersHandler := HandleListProviders(db)
	safeRegister(mux, "/api/providers", providersHandler)
	safeRegister(mux, "/api/providers/", providersHandler)
	safeRegister(mux, "/api/router/providers", providersHandler)
	safeRegister(mux, "/api/router/providers/", providersHandler)
	safeRegister(mux, "/api/router/providers/toggle", HandleToggleProvider(db))
	safeRegister(mux, "/api/providers/toggle", HandleToggleProvider(db))
	safeRegister(mux, "/api/providers/test-batch", HandleBatchHealthCheck(db))
	safeRegister(mux, "/api/providers/health-check-all", HandleBatchHealthCheck(db))
	safeRegister(mux, "/api/quotas/refresh-all", HandleRefreshAllQuotas(db))

	// 6.0 API Available Models của Provider (/api/models/provider)
	providerModelsHandler := HandleProviderModels(db)
	safeRegister(mux, "/api/models/provider", providerModelsHandler)
	safeRegister(mux, "/api/models/provider/", providerModelsHandler)
	safeRegister(mux, "/api/router/models/provider", providerModelsHandler)
	safeRegister(mux, "/api/router/models/provider/", providerModelsHandler)

	// 6.1 API Quota Hạn mức chi tiết (/api/usage/{id}, /api/usage/stats, /api/usage-stats)
	usageHandler := HandleAccountUsage(db)
	usageStatsHandler := HandleUsageStats(db)
	safeRegister(mux, "/api/usage/stats", usageStatsHandler)
	safeRegister(mux, "/api/usage-stats", usageStatsHandler)
	safeRegister(mux, "/api/usage/request-logs", HandleRequestDetails(db))
	safeRegister(mux, "/api/usage/request-details", HandleRequestDetails(db))
	safeRegister(mux, "/api/usage/", usageHandler)
	safeRegister(mux, "/api/usage", usageHandler)

	// 6.2 API Proxy Pools (/api/proxy-pools)
	proxyPoolsHandler := HandleProxyPools(db)
	safeRegister(mux, "/api/proxy-pools", proxyPoolsHandler)
	safeRegister(mux, "/api/proxy-pools/", proxyPoolsHandler)
	safeRegister(mux, "/api/proxy-pools/bind", HandleBindProxyPool(db))
	safeRegister(mux, "/api/router/proxy/switch", HandleSwitchProxy(db))
	safeRegister(mux, "/api/proxy-pools/switch", HandleSwitchProxy(db))

	// 6.2.1 API Combos & Optimizer
	combosHandler := HandleCombos(db)
	safeRegister(mux, "/api/combos", combosHandler)
	safeRegister(mux, "/api/combos/", combosHandler)
	safeRegister(mux, "/api/optimizer/stats", HandleOptimizerStats)
	safeRegister(mux, "/api/cache/stats", HandleCacheStats)
	safeRegister(mux, "/api/cache/clear", HandleCacheStats)
	safeRegister(mux, "/api/cache/toggle", HandleCacheStats)
	safeRegister(mux, "/api/backup/list", HandleBackup)
	safeRegister(mux, "/api/backup/trigger", HandleBackup)

	// 6.2.2 API Tunnel & PxPipe
	safeRegister(mux, "/api/tunnel/status", HandleTunnel)
	safeRegister(mux, "/api/tunnel/tailscale-check", HandleTunnel)
	safeRegister(mux, "/api/tunnel/enable", HandleTunnel)
	safeRegister(mux, "/api/tunnel/disable", HandleTunnel)
	safeRegister(mux, "/api/pxpipe/status", HandleTunnel)

	// 6.2.3 API 149 AI Skills Ecosystem
	safeRegister(mux, "/api/skills/core", HandleCoreSkillsEndpoint)
	safeRegister(mux, "/api/skills/load", HandleLoadSkillEndpoint)
	safeRegister(mux, "/api/skills/active", HandleActiveSkillsEndpoint)
	safeRegister(mux, "/api/skills/scan", HandleScanSkillsEndpoint)
	safeRegister(mux, "/api/skills/toggle", HandleToggleSkillEndpoint)

	// 6.3 API CLI Tools & Model Aliases (/api/cli-tools/antigravity-mitm/alias...)
	mitmAliasesHandler := HandleMitmAliases(db)
	mitmStatusHandler := HandleMitmStatus(db)
	safeRegister(mux, "/api/cli-tools/antigravity-mitm/alias", mitmAliasesHandler)
	safeRegister(mux, "/api/cli-tools/antigravity-mitm", mitmStatusHandler)
	safeRegister(mux, "/api/cli-tools/all-statuses", mitmStatusHandler)
	safeRegister(mux, "/api/cli-tools", mitmStatusHandler)

	// 7. API Live Telemetry Logs qua SSE & JSON fallback
	unifiedLogHandler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			HandleLogStream(w, r)
		} else {
			HandleRealtimeLogsJSON(w, r)
		}
	}
	safeRegister(mux, "/api/router/logs/stream", HandleLogStream)
	safeRegister(mux, "/api/realtime/stream-logs", unifiedLogHandler)
	safeRegister(mux, "/api/logs", unifiedLogHandler)
	safeRegister(mux, "/api/translator/console-logs", unifiedLogHandler)
	safeRegister(mux, "/api/translator/console-logs/stream", HandleLogStream)

	// 8. API Kích hoạt đồng bộ thủ công từ data.libsql (nếu có file)
	safeRegister(mux, "/api/router/sync-libsql", HandleSyncLibSQLEndpoint(db))

	// 8.1 API Tải về gói App Desktop OmniRouter (Đã bỏ LibSQL, trỏ TiDB Cloud)
	appDownloadHandler := HandleClientAppDownload()
	safeRegister(mux, "/api/v1/client/download", appDownloadHandler)
	safeRegister(mux, "/api/apps/download/APP-ROUTER", appDownloadHandler)

	// 9. API Đồng bộ, Cấu hình MITM & DNS và Báo cáo dành riêng cho App PC / Termux / CMD
	clientConfigHandler := HandleClientConfig(db)
	safeRegister(mux, "/api/v1/client/config", clientConfigHandler)
	safeRegister(mux, "/api/v1/client/config.json", clientConfigHandler)
	safeRegister(mux, "/api/v1/client/setup.bat", clientConfigHandler)
	safeRegister(mux, "/api/v1/client/setup.sh", clientConfigHandler)
	safeRegister(mux, "/api/v1/config", clientConfigHandler)
	safeRegister(mux, "/api/v1/mitm-dns", clientConfigHandler)
	safeRegister(mux, "/api/v1/client/sync", HandleClientSync(db))
	safeRegister(mux, "/api/v1/client/report", HandleClientReport(db))
	safeRegister(mux, "/api/v1/sync", HandleClientSync(db))
	safeRegister(mux, "/api/v1/report", HandleClientReport(db))

	// 9.1 API Tra cứu Quota & Ping DNS trực tiếp qua CMD / Terminal / cURL
	clientQuotaHandler := HandleClientQuotaCLI(db)
	safeRegister(mux, "/api/v1/client/quota", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/quota.bat", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/run.bat", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/run", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/link.bat", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/link", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/unlink.bat", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/unlink", clientQuotaHandler)
	safeRegister(mux, "/api/v1/client/ping", clientQuotaHandler)
	safeRegister(mux, "/api/v1/quota", clientQuotaHandler)
	safeRegister(mux, "/api/v1/ping", clientQuotaHandler)

	// 9.2 API Heartbeat tín hiệu kết nối Live từ máy khách CMD / Antigravity IDE
	clientHeartbeatHandler := HandleClientHeartbeat(db)
	safeRegister(mux, "/api/v1/client/heartbeat", clientHeartbeatHandler)
	safeRegister(mux, "/api/client/heartbeat", clientHeartbeatHandler)

	// 10. Cổng Gateway Proxy trực tiếp chuẩn OpenAI
	chatHandler := HandleChatCompletions(db)
	safeRegister(mux, "/v1/chat/completions", chatHandler)
	safeRegister(mux, "/api/v1/chat/completions", chatHandler)

	safeRegister(mux, "/v1/models", HandleModels)
	safeRegister(mux, "/api/v1/models", HandleModels)

	// 10.1 API Billing Subscription & Usage chuẩn OpenAI cho Antigravity IDE / Extension
	billingSubHandler := HandleBillingSubscription(db)
	billingUsageHandler := HandleBillingUsage(db)
	safeRegister(mux, "/v1/dashboard/billing/subscription", billingSubHandler)
	safeRegister(mux, "/api/v1/dashboard/billing/subscription", billingSubHandler)
	safeRegister(mux, "/v1/dashboard/billing/usage", billingUsageHandler)
	safeRegister(mux, "/api/v1/dashboard/billing/usage", billingUsageHandler)
	safeRegister(mux, "/v1/user/keys", HandleManageKeys(db))
	safeRegister(mux, "/v1/me", billingSubHandler)

	sttHandler := HandleAudioTranscriptions(db)
	safeRegister(mux, "/v1/audio/transcriptions", sttHandler)
	safeRegister(mux, "/api/v1/audio/transcriptions", sttHandler)
	safeRegister(mux, "/v1/models/stt", HandleSTTModels)

	// 10.2 Gemini Native Transparent Proxy cho Antigravity IDE (MITM qua hosts file)
	// IDE gửi request thẳng đến daily-cloudcode-pa.googleapis.com/v1internal:...
	// Hosts file redirect về 127.0.0.1 → request đến OmniRouter port 443
	// Handler này thay OAuth token của user bằng token tài khoản AI pool,
	// forward qua IP thật, pipe SSE response nguyên bản về IDE
	geminiProxyHandler := HandleGeminiNativeProxy(db)
	safeRegister(mux, "/v1internal:", geminiProxyHandler) // Catch: /v1internal:streamGenerateContent, /v1internal:generateContent, ...
	safeRegister(mux, "/v1internal/", geminiProxyHandler) // Catch: /v1internal/models/xxx
	safeRegister(mux, "/v1beta/", geminiProxyHandler)     // Catch: /v1beta/models/xxx:generateContent
	log.Printf("[ENGINE] [GEMINI-PROXY] ✔ Đã đăng ký Transparent Gemini Proxy cho Antigravity IDE (upstream IP: %s)", upstreamRealHost)

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
		safeRegisterHandler(mux, "/providers/", http.StripPrefix("/providers/", fileServer))
		safeRegisterHandler(mux, "/router/providers/", http.StripPrefix("/router/providers/", fileServer))
		log.Printf("[ENGINE] [OMNIROUTER] ✔ Đã gắn cổng phục vụ tĩnh 160+ Logo nhà cung cấp từ: %s", resolvedProvidersDir)
	}

	// 12. Giao diện Web Dashboard Quản lý 9Router với Pre-compressed Gzip & ETag HTTP 304
	routerUIFile := filepath.Join(uiDir, "router", "index.html")
	routerHandler := func(w http.ResponseWriter, r *http.Request) {
		// 1. Phục vụ tài nguyên tĩnh trong thư mục router/ (app.png, icons/, images/, i18n/, ...)
		relPath := strings.TrimPrefix(r.URL.Path, "/router")
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath != "" && relPath != "index.html" {
			targetFile := filepath.Join(uiDir, "router", filepath.Clean(relPath))
			if fi, err := os.Stat(targetFile); err == nil && !fi.IsDir() {
				w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
				http.ServeFile(w, r, targetFile)
				return
			}
		}

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

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

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

	safeRegister(mux, "/router", routerHandler)
	safeRegister(mux, "/router/", routerHandler)
	safeRegister(mux, "/router/index.html", routerHandler)

	log.Println("[ENGINE] [OMNIROUTER] ✔ Đã kích hoạt toàn bộ 15 cổng định tuyến 9Router Master Gateway")
}
