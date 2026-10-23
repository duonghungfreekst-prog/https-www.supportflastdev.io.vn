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
	"strconv"
	"strings"
	"sync"
)

var (
	routerGzipOnce  sync.Once
	routerRawBytes  []byte
	routerGzipBytes []byte
	routerETag      string
)

// initRouterCache nÃ©n sáºµn file router HTML vÃ o RAM Ä‘á»ƒ phá»¥c vá»¥ 0ms
func initRouterCache(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	routerRawBytes = data

	// BÄƒm ETag SHA-256
	h := sha256.Sum256(data)
	routerETag = fmt.Sprintf(`"%s"`, hex.EncodeToString(h[:16]))

	// NÃ©n Gzip Level 9
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err == nil {
		_, _ = gz.Write(data)
		_ = gz.Close()
		routerGzipBytes = buf.Bytes()
		log.Printf("[ENGINE] [OMNIROUTER] âœ” ÄÃ£ nÃ©n sáºµn router/index.html lÃªn RAM: %d KB -> %d KB (ETag: %s)", len(routerRawBytes)/1024, len(routerGzipBytes)/1024, routerETag)
	}
}

// safeRegister Ä‘Äƒng kÃ½ route an toÃ n, trÃ¡nh panic náº¿u pattern Ä‘Ã£ tá»“n táº¡i trÃªn ServeMux cá»§a Host Server
func safeRegister(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	defer func() {
		_ = recover()
	}()
	mux.HandleFunc(pattern, handler)
}

// safeRegisterHandler Ä‘Äƒng kÃ½ http.Handler an toÃ n, trÃ¡nh panic náº¿u pattern Ä‘Ã£ tá»“n táº¡i
func safeRegisterHandler(mux *http.ServeMux, pattern string, handler http.Handler) {
	defer func() {
		_ = recover()
	}()
	mux.Handle(pattern, handler)
}

// RegisterOmniRouterRoutes Ä‘Äƒng kÃ½ toÃ n bá»™ routes cá»§a 9Router vÃ o ServeMux cá»§a há»‡ thá»‘ng
func RegisterOmniRouterRoutes(mux *http.ServeMux, db *sql.DB, isCloudDB bool, uiDir string) {
	// 1. Khá»Ÿi táº¡o cáº¥u trÃºc lÆ°u trá»¯ báº£ng
	if err := InitStorage(db, isCloudDB); err != nil {
		log.Printf("[ENGINE] [OMNIROUTER] [WARN] Khá»Ÿi táº¡o báº£ng OmniRouter: %v", err)
	}
	SetSkillsDB(db)

	// 2. TuÃ¢n thá»§ Rule 9: KhÃ´ng tá»± Ã½ náº¡p tÃ i khoáº£n máº«u/giáº£ láº­p vÃ o CSDL

	// 3. Khá»Ÿi cháº¡y worker tá»± Ä‘á»™ng lÃ m má»›i token OAuth ngáº§m & náº¡p Quota Ä‘á»‹nh ká»³
	StartAutoTokenRefreshWorker(context.Background(), db)
	StartBackgroundQuotaPollingWorker(context.Background(), db)

	// 4. API Ping siÃªu nháº¹ & Health check
	safeRegister(mux, "/ping", HandlePing)
	safeRegister(mux, "/health", HandleHealth)       // alias cho HTML gateway check
	safeRegister(mux, "/api/health", HandleHealth)
	safeRegister(mux, "/api/router/health", HandleHealth)

	// 4.1 CÃ i Ä‘áº·t há»‡ thá»‘ng & CSDL Aiven PostgreSQL Cloud
	settingsHandler := HandleSettings(db)
	safeRegister(mux, "/api/settings", settingsHandler)
	safeRegister(mux, "/api/settings/", settingsHandler)
	safeRegister(mux, "/api/router/settings", settingsHandler)
	safeRegister(mux, "/api/router/settings/", settingsHandler)
	safeRegister(mux, "/api/settings/database", HandleSettingsDatabase(db))
	safeRegister(mux, "/api/router/settings/database", HandleSettingsDatabase(db))

	// 5. API Quáº£n lÃ½ API Key cho Web UI & CLI Tools (Há»— trá»£ cáº£ /api/keys vÃ  /api/router/keys)
	keysHandler := HandleManageKeys(db)
	safeRegister(mux, "/api/keys", keysHandler)
	safeRegister(mux, "/api/keys/", keysHandler)
	safeRegister(mux, "/api/router/keys", keysHandler)
	safeRegister(mux, "/api/router/keys/", keysHandler)
	safeRegister(mux, "/api/router/keys/toggle", HandleToggleKey(db))

	// 5.1 API OAuth 2.0 PKCE & Handlers cho tất cả các nhà cung cấp AI
	oauthHandler := HandleOAuth(db)
	safeRegister(mux, "/api/oauth/", oauthHandler)
	safeRegister(mux, "/api/router/oauth/", oauthHandler)
	safeRegister(mux, "/api/oauth/poll", HandleOAuthPoll)
	safeRegister(mux, "/api/router/oauth/poll", HandleOAuthPoll)
	safeRegister(mux, "/callback", HandleOAuthCallbackHTML)
	safeRegister(mux, "/callback/", HandleOAuthCallbackHTML)

	// 6. API Danh sách tài khoản AI & Tương tác (Hỗ trợ đầy đủ trên cả router cục bộ /api/* và router cloud /api/router/*)
	providersHandler := HandleListProviders(db)
	safeRegister(mux, "/api/providers", providersHandler)
	safeRegister(mux, "/api/providers/", providersHandler)
	safeRegister(mux, "/api/router/providers", providersHandler)
	safeRegister(mux, "/api/router/providers/", providersHandler)
	safeRegister(mux, "/api/providers/toggle", HandleToggleProvider(db))
	safeRegister(mux, "/api/router/providers/toggle", HandleToggleProvider(db))
	safeRegister(mux, "/api/providers/test-batch", HandleBatchHealthCheck(db))
	safeRegister(mux, "/api/router/providers/test-batch", HandleBatchHealthCheck(db))
	safeRegister(mux, "/api/providers/health-check-all", HandleBatchHealthCheck(db))
	safeRegister(mux, "/api/router/providers/health-check-all", HandleBatchHealthCheck(db))

	// API Quotas hạn mức & làm mới (Hỗ trợ đầy đủ trên cả router cục bộ /api/quotas/* và router cloud /api/router/quotas/*)
	quotasHandler := HandleRefreshAllQuotas(db)
	safeRegister(mux, "/api/quotas", quotasHandler)
	safeRegister(mux, "/api/quotas/", quotasHandler)
	safeRegister(mux, "/api/router/quotas", quotasHandler)
	safeRegister(mux, "/api/router/quotas/", quotasHandler)
	safeRegister(mux, "/api/quotas/refresh", quotasHandler)
	safeRegister(mux, "/api/router/quotas/refresh", quotasHandler)
	safeRegister(mux, "/api/quotas/refresh-all", quotasHandler)
	safeRegister(mux, "/api/quotas/refresh-all/", quotasHandler)
	safeRegister(mux, "/api/router/quotas/refresh-all", quotasHandler)
	safeRegister(mux, "/api/router/quotas/refresh-all/", quotasHandler)

	// 6.0 API Available Models của Provider (/api/models/provider)
	providerModelsHandler := HandleProviderModels(db)
	safeRegister(mux, "/api/models/provider", providerModelsHandler)
	safeRegister(mux, "/api/models/provider/", providerModelsHandler)
	safeRegister(mux, "/api/router/models/provider", providerModelsHandler)
	safeRegister(mux, "/api/router/models/provider/", providerModelsHandler)

	// 6.0 API Thống kê công khai siêu nhẹ cho trang chủ Web (/api/stats/public, /api/v1/public/stats)
	publicStatsHandler := HandlePublicStats(db)
	safeRegister(mux, "/api/stats/public", publicStatsHandler)
	safeRegister(mux, "/api/v1/public/stats", publicStatsHandler)
	safeRegister(mux, "/api/v1/public/quota", publicStatsHandler)

	// 6.1 API Quota Hạn mức chi tiết (/api/usage/{id}, /api/usage/stats, /api/usage-stats)
	usageHandler := HandleAccountUsage(db)
	usageStatsHandler := HandleUsageStats(db)
	safeRegister(mux, "/api/usage", usageHandler)
	safeRegister(mux, "/api/usage/", usageHandler)
	safeRegister(mux, "/api/router/usage", usageHandler)
	safeRegister(mux, "/api/router/usage/", usageHandler)
	safeRegister(mux, "/api/usage/stats", usageStatsHandler)
	safeRegister(mux, "/api/usage-stats", usageStatsHandler)
	safeRegister(mux, "/api/router/usage/stats", usageStatsHandler)
	safeRegister(mux, "/api/router/usage-stats", usageStatsHandler)
	safeRegister(mux, "/api/usage/request-logs", HandleRequestDetails(db))
	safeRegister(mux, "/api/usage/request-details", HandleRequestDetails(db))
	safeRegister(mux, "/api/router/usage/request-logs", HandleRequestDetails(db))
	safeRegister(mux, "/api/router/usage/request-details", HandleRequestDetails(db))

	// 6.2 API Proxy Pools (/api/proxy-pools)
	proxyPoolsHandler := HandleProxyPools(db)
	safeRegister(mux, "/api/proxy-pools", proxyPoolsHandler)
	safeRegister(mux, "/api/proxy-pools/", proxyPoolsHandler)
safeRegister(mux, "/api/proxy-pools/test", proxyPoolsHandler)
	safeRegister(mux, "/api/proxy-pools/bind", HandleBindProxyPool(db))
	safeRegister(mux, "/api/router/proxy/switch", HandleSwitchProxy(db))
	safeRegister(mux, "/api/proxy-pools/switch", HandleSwitchProxy(db))

	// 6.2.1 API Combos & Optimizer
	combosHandler := HandleCombos(db)
	safeRegister(mux, "/api/combos", combosHandler)
	safeRegister(mux, "/api/combos/", combosHandler)
	safeRegister(mux, "/api/optimizer/stats", HandleOptimizerStats(db))
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

	// 6.3 Gemini Native Transparent Proxy cho Antigravity IDE (MITM qua cá»•ng 443 & hosts file)
	geminiProxyHandler := HandleGeminiNativeProxy(db)
	safeRegister(mux, "/v1internal:streamGenerateContent", geminiProxyHandler)
	safeRegister(mux, "/v1internal:generateContent", geminiProxyHandler)
	safeRegister(mux, "/v1internal:fetchAvailableModels", geminiProxyHandler)
	safeRegister(mux, "/v1internal:retrieveUserQuotaSummary", geminiProxyHandler)
	safeRegister(mux, "/v1internal:loadCodebaseContext", geminiProxyHandler)
	safeRegister(mux, "/v1internal:generateCompletion", geminiProxyHandler)
	safeRegister(mux, "/v1internal:chat", geminiProxyHandler)
	safeRegister(mux, "/v1internal/", geminiProxyHandler)
	safeRegister(mux, "/v1internal:", geminiProxyHandler)
	safeRegister(mux, "/v1beta/", geminiProxyHandler)
	log.Printf("[ENGINE] [GEMINI-PROXY] âœ” ÄÃ£ Ä‘Äƒng kÃ½ Transparent Gemini Proxy cho Antigravity IDE (upstream IP: %s)", upstreamRealHost)

	// MITM Dispatcher cho Cá»•ng 443: Báº¯t trá»n váº¹n request tá»« Antigravity/CloudCode IDE
	mitmDispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		host := strings.ToLower(r.Host)
		if strings.HasPrefix(path, "/v1internal") || strings.Contains(host, "cloudcode") {
			geminiProxyHandler(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	GlobalMitmDispatcher = mitmDispatcher

	mitmAliasesHandler := HandleMitmAliases(db)
	mitmStatusHandler := HandleMitmStatus(db, mitmDispatcher, uiDir)
	safeRegister(mux, "/api/cli-tools/antigravity-mitm/alias", mitmAliasesHandler)
	safeRegister(mux, "/api/cli-tools/antigravity-mitm/ca.crt", HandleMitmCACert(uiDir))
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

	// 8. API KÃ­ch hoáº¡t Ä‘á»“ng bá»™ thá»§ cÃ´ng tá»« data.libsql (náº¿u cÃ³ file)
	safeRegister(mux, "/api/router/sync-libsql", HandleSyncLibSQLEndpoint(db))


	// 9. API Äá»“ng bá»™, Cáº¥u hÃ¬nh MITM & DNS vÃ  BÃ¡o cÃ¡o dÃ nh riÃªng cho App PC / Termux / CMD
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
	safeRegister(mux, "/api/client/sync", HandleClientSync(db))
	safeRegister(mux, "/api/client/report", HandleClientReport(db))
	safeRegister(mux, "/api/sync", HandleClientSync(db))

	// 9.1 API Tra cá»©u Quota & Ping DNS trá»±c tiáº¿p qua CMD / Terminal / cURL
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

	// 9.2 API Heartbeat tÃ­n hiá»‡u káº¿t ná»‘i Live tá»« mÃ¡y khÃ¡ch CMD / Antigravity IDE
	clientHeartbeatHandler := HandleClientHeartbeat(db)
	safeRegister(mux, "/api/v1/client/heartbeat", clientHeartbeatHandler)
	safeRegister(mux, "/api/client/heartbeat", clientHeartbeatHandler)

	// 9.3 API Cáº­p nháº­t & Tra cá»©u Active Key trong RAM cho App Desktop / WebView2
	activeKeyHandler := HandleSetActiveKey(db)
	safeRegister(mux, "/api/v1/client/set_active_key", activeKeyHandler)
	safeRegister(mux, "/api/v1/client/active_key", activeKeyHandler)

	// 10. Cổng Gateway Base Endpoint /v1 (Chống lỗi 404 khi ping Base URL)
	safeRegister(mux, "/v1", HandleV1Base)
	safeRegister(mux, "/v1/", HandleV1Base)
	safeRegister(mux, "/api/v1", HandleV1Base)
	safeRegister(mux, "/api/v1/", HandleV1Base)

	// 10. Cổng Gateway Proxy trực tiếp chuẩn OpenAI
	chatHandler := HandleChatCompletions(db)
	safeRegister(mux, "/v1/chat/completions", chatHandler)
	safeRegister(mux, "/api/v1/chat/completions", chatHandler)

	safeRegister(mux, "/v1/models", HandleModels)
	safeRegister(mux, "/api/v1/models", HandleModels)

	// 10.1 API Billing Subscription & Usage chuáº©n OpenAI cho Antigravity IDE / Extension
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


	// 11. Phá»¥c vá»¥ tÄ©nh thÆ° má»¥c Logo nhÃ  cung cáº¥p (/providers/ vÃ  /router/providers/)
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
		log.Printf("[ENGINE] [OMNIROUTER] âœ” ÄÃ£ gáº¯n cá»•ng phá»¥c vá»¥ tÄ©nh 160+ Logo nhÃ  cung cáº¥p tá»«: %s", resolvedProvidersDir)
	}

	// 12. Giao diá»‡n Web Dashboard Quáº£n lÃ½ 9Router vá»›i Pre-compressed Gzip & ETag HTTP 304
	routerUIFile := filepath.Join(uiDir, "router", "index.html")
	routerHandler := func(w http.ResponseWriter, r *http.Request) {
		// 1. Phá»¥c vá»¥ tÃ i nguyÃªn tÄ©nh trong thÆ° má»¥c router/ (app.png, icons/, images/, i18n/, ...)
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

		// Náº¡p cache nÃ©n sáºµn má»™t láº§n duy nháº¥t
		routerGzipOnce.Do(func() {
			initRouterCache(routerUIFile)
		})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' 'unsafe-eval' https: data: blob:; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://challenges.cloudflare.com data: blob:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.tailwindcss.com; font-src 'self' https://fonts.gstatic.com https://cdnjs.cloudflare.com data:; img-src 'self' https: data: blob:; connect-src 'self' https: ws: wss: http://127.0.0.1:* http://localhost:*;")

		// Kiá»ƒm tra ETag HTTP 304 Not Modified
		// Kiểm tra ETag HTTP 304 Not Modified (Hỗ trợ cả Strong và Weak ETag W/ từ Cloudflare/Proxy)
		if routerETag != "" {
			w.Header().Set("ETag", routerETag)
			clientETag := strings.TrimSpace(r.Header.Get("If-None-Match"))
			if clientETag != "" {
				cleanClient := strings.Trim(clientETag, "W/\"")
				cleanServer := strings.Trim(routerETag, "W/\"")
				if clientETag == routerETag || cleanClient == cleanServer || strings.Contains(clientETag, cleanServer) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		w.Header().Set("Cache-Control", "no-cache, must-revalidate")

		// Phá»¥c vá»¥ Gzip nÃ©n sáºµn trá»±c tiáº¿p tá»« RAM náº¿u client há»— trá»£
		if len(routerGzipBytes) > 0 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(routerGzipBytes)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(routerGzipBytes)
			return
		}

		if len(routerRawBytes) > 0 {
			w.Header().Set("Content-Length", strconv.Itoa(len(routerRawBytes)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(routerRawBytes)
			return
		}

		if _, err := os.Stat(routerUIFile); err == nil {
			http.ServeFile(w, r, routerUIFile)
			return
		}

		// Fallback náº¿u chÆ°a copy file UI
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>9Router Gateway Dashboard</title><meta charset="utf-8"/></head>
<body style="background:#0f172a;color:#f8fafc;font-family:sans-serif;padding:40px;">
	<h1>âš¡ 9Router Master Gateway</h1>
	<p>Web Dashboard Ä‘ang khá»Ÿi Ä‘á»™ng...</p>
</body>
</html>`))
	}

	safeRegister(mux, "/router", routerHandler)
	safeRegister(mux, "/router/", routerHandler)
	safeRegister(mux, "/router/index.html", routerHandler)
	safeRegister(mux, "/client", routerHandler)
	safeRegister(mux, "/client/", routerHandler)
	safeRegister(mux, "/app", routerHandler)
	safeRegister(mux, "/share", routerHandler)
	safeRegister(mux, "/share-app", routerHandler)



	log.Println("[ENGINE] [OMNIROUTER] âœ” ÄÃ£ kÃ­ch hoáº¡t toÃ n bá»™ cá»•ng Ä‘á»‹nh tuyáº¿n Master Gateway")
}

