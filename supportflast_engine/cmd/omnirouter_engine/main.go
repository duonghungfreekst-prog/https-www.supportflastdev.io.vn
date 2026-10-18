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
	_ = flag.Bool("auto-mitm", false, "Tự động kích hoạt MITM Proxy")
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
	fmt.Println("    OMNIROUTER / SUPPORTFLAST AI GATEWAY - TIDB CLOUD EDITION       ")
	fmt.Println("=====================================================================")
	log.Printf("[ENGINE] Khởi động OmniRouter Engine v2.9.0 (PID: %d)", os.Getpid())
	log.Printf("[ENGINE] [CSDL] BỎ HOÀN TOÀN LIBSQL / SQLITE - TRỎ DỮ LIỆU LÊN TIDB CLOUD")

	// 1. Kết nối cơ sở dữ liệu phân tán TiDB Cloud Serverless (MySQL Protocol)
	tidbCfg := database.DefaultTiDBConfig()
	tidbCfg.AutoMigrate = true

	db, err := database.OpenTiDBConnection(tidbCfg)
	if err != nil {
		log.Printf("[ENGINE] [TIDB] [CẢNH BÁO] Không thể kết nối trực tiếp TiDB Cloud: %v. Đang kích hoạt chế độ thử lại ngầm...", err)
	} else {
		log.Printf("[ENGINE] [TIDB] ✔ Đã kết nối thành công tới TiDB Cloud Serverless (Host: %s:%d, DB: %s)",
			tidbCfg.Host, tidbCfg.Port, tidbCfg.Database)
	}

	// 2. Khởi tạo ServeMux và đăng ký toàn bộ 15+ cổng định tuyến OmniRouter
	mux := http.NewServeMux()
	omnirouter.RegisterOmniRouterRoutes(mux, db, true, baseDir)

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

	// Phục vụ file giao diện HTML trực tiếp cho Desktop App WebView2 và Browser
	serveHTML := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeFile(w, r, htmlCandidate)
	}
	safeRegister("/nexusroute_ai_next_gen_gateway.html", serveHTML)
	safeRegister("/index.html", serveHTML)

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

	// Phục vụ tĩnh các thư mục assets
	for _, folder := range []string{"icons", "images", "public"} {
		dirPath := filepath.Join(baseDir, folder)
		if fi, err := os.Stat(dirPath); err == nil && fi.IsDir() {
			fs := http.StripPrefix("/"+folder+"/", http.FileServer(http.Dir(dirPath)))
			safeRegisterHandler("/"+folder+"/", fs)
		}
	}

	// Route gốc fallback về trang giao diện nếu không khớp route API
	safeRegister("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			serveHTML(w, r)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		if db != nil {
			_ = db.Close()
		}
		log.Println("[ENGINE] Dọn dẹp hoàn tất. Thoát an toàn.")
		os.Exit(0)
	}()

	log.Printf("[ENGINE] ✔ OmniRouter Gateway Core đã sẵn sàng phục vụ trên cổng %d (Trỏ CSDL TiDB Cloud)", *portFlag)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[ENGINE] [FATAL] Lỗi khởi chạy Gateway Server: %v", err)
	}
}
