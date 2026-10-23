package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"supportflast_engine/database"
	"supportflast_engine/omnirouter"
)

func main() {
	portFlag := flag.Int("port", 20128, "Cổng lắng nghe Gateway Core (mặc định 20128)")
	dirFlag := flag.String("dir", "", "Thư mục gốc của dự án (mặc định tự động nhận diện)")
	_ = flag.Int("mitm-port", 443, "Cổng lắng nghe MITM Proxy (mặc định 443)")
	_ = flag.Bool("auto-mitm", true, "Tự động kích hoạt MITM Proxy")
	_ = flag.String("host", "127.0.0.1", "Host lắng nghe Gateway")
	_ = flag.String("config", "", "Đường dẫn file config.json")
	_ = flag.Bool("strict-proxy", false, "Chế độ proxy nghiêm ngặt")
	flag.Parse()

	baseDir := *dirFlag
	if baseDir == "" {
		if exePath, err := os.Executable(); err == nil && exePath != "" {
			baseDir = filepath.Dir(exePath)
		} else {
			baseDir = "."
		}
	}

	fmt.Println("=====================================================================")
	fmt.Println("    OMNIROUTER / SUPPORTFLAST AI GATEWAY - AIVEN POSTGRESQL EDITION  ")
	fmt.Println("=====================================================================")
	log.Printf("[ENGINE] Khởi động OmniRouter Engine v3.0.0 (PID: %d)", os.Getpid())
	log.Printf("[ENGINE] [CSDL] KẾT NỐI TRỰC TIẾP 100%% VÀO AIVEN POSTGRESQL CLUSTER")

	// 1. Kết nối cơ sở dữ liệu Aiven PostgreSQL Cloud Cluster
	db, err := database.OpenPostgresConnection()
	if err != nil {
		log.Printf("[ENGINE] [POSTGRES] [CẢNH BÁO] Không thể kết nối trực tiếp Aiven PostgreSQL: %v. Tự động chuyển sang CSDL SQLite cục bộ...", err)
		sqlitePath := filepath.Join(baseDir, "data", "supportflast.db")
		sqliteDB, _, sqliteErr := database.OpenSQLiteConnection(sqlitePath)
		if sqliteErr != nil {
			log.Printf("[ENGINE] [SQLITE] [CẢNH BÁO] Khởi tạo SQLite fallback thất bại (%v). Chạy chế độ In-Memory cache.", sqliteErr)
			db = nil
		} else {
			db = sqliteDB
			log.Printf("[ENGINE] [SQLITE] ✔ Đã kích hoạt CSDL SQLite cục bộ tại '%s'", sqlitePath)
		}
	} else {
		log.Printf("[ENGINE] [POSTGRES] ✔ Đã kết nối thành công tới Aiven PostgreSQL Cloud Cluster!")
	}

	// 2. Khởi tạo ServeMux và đăng ký toàn bộ 15+ cổng định tuyến OmniRouter
	mux := http.NewServeMux()
	omnirouter.RegisterOmniRouterRoutes(mux, db, false, baseDir)

	// Tự động kích hoạt chuyển hướng DNS (Zero-Delay Intercept) và MITM Server cho Antigravity IDE ngay khi khởi động
	// Bảo đảm Antigravity IDE được định tuyến ngay lập tức qua ModelRouter9 từ giây đầu tiên,
	// ngăn chặn triệt để tình trạng request ban đầu bị lọt ra ngoài Google làm trừ quota tài khoản cá nhân.
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := omnirouter.AutoStartMitm(baseDir); err != nil {
			log.Printf("[ENGINE] [MITM] ⚠ Cảnh báo khởi động Zero-Delay Intercept: %v", err)
		} else {
			log.Printf("[ENGINE] [MITM] ✔ Đã tự động kích hoạt bảo vệ Quota tức thì (Zero-Delay Intercept) cho Antigravity IDE")
		}
	}()

	// Nạp trước Active Client Key vào RAM và kích hoạt luồng Live Telemetry Heartbeat
	go func() {
		time.Sleep(500 * time.Millisecond)
		activeKey := omnirouter.GetActiveClientKey(db)
		if activeKey != "" {
			log.Printf("[ENGINE] [KEY] ✔ Active Client Key đã nạp sẵn sàng: %s", omnirouter.MaskAPIKey(activeKey))
		}
		omnirouter.StartClientTelemetryHeartbeat(db)
	}()

	// Thêm cổng phục vụ file giao diện HTML trực tiếp cho Desktop App WebView2
	htmlCandidate := filepath.Join(baseDir, "nexusroute_ai_next_gen_gateway.html")
	if _, err := os.Stat(htmlCandidate); err != nil {
		htmlCandidate = filepath.Join(baseDir, "public", "nexusroute_ai_next_gen_gateway.html")
	}
	if _, err := os.Stat(htmlCandidate); err != nil {
		htmlCandidate = filepath.Join(baseDir, "supportflast_ui", "router", "index.html")
	}

	// Hàm đăng ký route an toàn chống panic nếu trùng pattern (Rule 12)
	safeRegister := func(pattern string, handler http.HandlerFunc) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ENGINE] [WARN] Bỏ qua đăng ký trùng route '%s': %v", pattern, r)
			}
		}()
		mux.HandleFunc(pattern, handler)
	}

	safeRegisterHandler := func(pattern string, handler http.Handler) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ENGINE] [WARN] Bỏ qua đăng ký trùng handler '%s': %v", pattern, r)
			}
		}()
		mux.Handle(pattern, handler)
	}

	safeRegister("/nexusroute_ai_next_gen_gateway.html", func(w http.ResponseWriter, r *http.Request) {
		omnirouter.HandleV1Base(w, r)
	})
	safeRegister("/index.html", func(w http.ResponseWriter, r *http.Request) {
		omnirouter.HandleV1Base(w, r)
	})

	// Phục vụ logo và tài nguyên ảnh
	appPngCandidate := filepath.Join(baseDir, "app.png")
	if _, err := os.Stat(appPngCandidate); err != nil {
		appPngCandidate = filepath.Join(baseDir, "public", "app.png")
	}
	if _, err := os.Stat(appPngCandidate); err != nil {
		appPngCandidate = filepath.Join(baseDir, "icons", "app.png")
	}
	serveLogo := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		http.ServeFile(w, r, appPngCandidate)
	}
	safeRegister("/app.png", serveLogo)
	safeRegister("/icons/app.png", serveLogo)
	safeRegister("/public/app.png", serveLogo)

	// Phục vụ hình nền Wallpaper Web App cho /v1 và frontend
	serveWallpaper := func(w http.ResponseWriter, r *http.Request) {
		candidates := []string{
			filepath.Join(baseDir, "modelrouter9-topology-dark.png"),
			filepath.Join(baseDir, "9router-topology-dark.png"),
			filepath.Join(baseDir, "omnirouter-topology-dark.png"),
			filepath.Join(baseDir, "public", "modelrouter9-topology-dark.png"),
			filepath.Join("supportflast_ui", "router", "modelrouter9-topology-dark.png"),
			filepath.Join("..", "supportflast_ui", "router", "modelrouter9-topology-dark.png"),
		}
		for _, cand := range candidates {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
				http.ServeFile(w, r, cand)
				return
			}
		}
		http.NotFound(w, r)
	}
	safeRegister("/modelrouter9-topology-dark.png", serveWallpaper)
	safeRegister("/9router-topology-dark.png", serveWallpaper)
	safeRegister("/omnirouter-topology-dark.png", serveWallpaper)
	safeRegister("/router/modelrouter9-topology-dark.png", serveWallpaper)

	// Phục vụ tĩnh các thư mục assets
	for _, folder := range []string{"icons", "images", "public"} {
		dirPath := filepath.Join(baseDir, folder)
		if fi, err := os.Stat(dirPath); err == nil && fi.IsDir() {
			fs := http.StripPrefix("/"+folder+"/", http.FileServer(http.Dir(dirPath)))
			safeRegisterHandler("/"+folder+"/", fs)
		}
	}

	// Route gốc: Khi truy cập cổng 20128 từ trình duyệt Web, ẩn toàn bộ nội dung, chỉ phục vụ hình nền Web App sạch
	safeRegister("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" || r.URL.Path == "/v1" || r.URL.Path == "/v1/" || r.URL.Path == "/api/v1" || r.URL.Path == "/api/v1/" {
			omnirouter.HandleV1Base(w, r)
			return
		}
		// Thử tìm file tĩnh tương ứng trong baseDir
		cleanRel := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		target := filepath.Join(baseDir, cleanRel)
		if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, target)
			return
		}
		http.NotFound(w, r)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", *portFlag),
		Handler: mux,
	}

	// 3. Xử lý tín hiệu tắt an toàn Graceful Shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[ENGINE] Nhận tín hiệu dừng... Đang dọn dẹp hệ thống...")
		omnirouter.CleanAllMitmDns()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		if db != nil {
			_ = db.Close()
		}
		log.Println("[ENGINE] Dọn dẹp hoàn tất. Thoát an toàn.")
		os.Exit(0)
	}()

	log.Printf("[ENGINE] ✔ OmniRouter Gateway Core đã sẵn sàng phục vụ trên cổng %d (Trỏ CSDL Aiven PostgreSQL)", *portFlag)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[ENGINE] [FATAL] Lỗi khởi chạy Gateway Server: %v", err)
	}
}
