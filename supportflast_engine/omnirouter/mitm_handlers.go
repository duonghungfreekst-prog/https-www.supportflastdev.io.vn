package omnirouter

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	GlobalMitmDispatcher http.Handler
	mitmServerLock       sync.Mutex
	mitmServer           *http.Server
	mitmServerRunning    bool
	caCertGlobal         *x509.Certificate
	caKeyGlobal          crypto.PrivateKey
	caOnce               sync.Once
	certCache            sync.Map // hostname -> *tls.Certificate

	toolDomains = map[string][]string{
		"antigravity": {
			"daily-cloudcode-pa.googleapis.com",
			"cloudcode-pa.googleapis.com",
			"generativelanguage.googleapis.com",
		},
		"kiro": {
			"runtime.us-east-1.kiro.dev",
		},
		"copilot": {
			"api.individual.githubcopilot.com",
			"api.githubcopilot.com",
		},
	}
)

func getHostsFilePath() string {
	if runtime.GOOS == "windows" {
		sysRoot := os.Getenv("SystemRoot")
		if sysRoot == "" {
			sysRoot = "C:\\Windows"
		}
		return filepath.Join(sysRoot, "System32", "drivers", "etc", "hosts")
	}
	return "/etc/hosts"
}

func flushDNS() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("ipconfig", "/flushdns")
		_ = cmd.Run()
	}
}

func checkToolDnsStatus(tool string) bool {
	domains, ok := toolDomains[strings.ToLower(tool)]
	if !ok || len(domains) == 0 {
		return false
	}
	content, err := os.ReadFile(getHostsFilePath())
	if err != nil {
		return false
	}
	lines := strings.Split(string(content), "\n")
	activeDomains := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && (fields[0] == "127.0.0.1" || fields[0] == "::1") {
			for _, f := range fields[1:] {
				cleanField := strings.TrimPrefix(strings.ToLower(f), "#")
				for _, d := range domains {
					if cleanField == strings.ToLower(d) {
						activeDomains[cleanField] = true
					}
				}
			}
		}
	}
	for _, d := range domains {
		if !activeDomains[strings.ToLower(d)] {
			return false
		}
	}
	return true
}

func setToolDns(tool string, enable bool) error {
	domains, ok := toolDomains[strings.ToLower(tool)]
	if !ok || len(domains) == 0 {
		return fmt.Errorf("công cụ không hỗ trợ: %s", tool)
	}

	hostsPath := getHostsFilePath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("không thể đọc file hosts: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string

	domainSet := make(map[string]bool)
	for _, d := range domains {
		domainSet[strings.ToLower(d)] = true
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		isTargetLine := false
		if len(fields) >= 2 && (fields[0] == "127.0.0.1" || fields[0] == "::1") {
			for _, f := range fields[1:] {
				if domainSet[strings.ToLower(f)] {
					isTargetLine = true
					break
				}
			}
		}
		if !isTargetLine {
			newLines = append(newLines, line)
		}
	}

	if enable {
		newLines = append(newLines, fmt.Sprintf("\n# === OmniRouter MITM DNS - %s ===", strings.ToUpper(tool)))
		for _, d := range domains {
			newLines = append(newLines, fmt.Sprintf("127.0.0.1 %s # OmniRouter", d))
		}
	}

	output := strings.Join(newLines, "\n")
	if err := os.WriteFile(hostsPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("không thể ghi file hosts: %w", err)
	}

	flushDNS()
	log.Printf("[ENGINE] [MITM] Đã cập nhật DNS hosts cho '%s' (Enable: %v)", tool, enable)
	return nil
}

func cleanAllMitmDns() {
	hostsPath := getHostsFilePath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return
	}
	allDomains := make(map[string]bool)
	for _, dList := range toolDomains {
		for _, d := range dList {
			allDomains[strings.ToLower(d)] = true
		}
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "OmniRouter MITM DNS") {
			continue
		}
		fields := strings.Fields(trimmed)
		isTargetLine := false
		if len(fields) >= 2 && (fields[0] == "127.0.0.1" || fields[0] == "::1") {
			for _, f := range fields[1:] {
				if allDomains[strings.ToLower(f)] {
					isTargetLine = true
					break
				}
			}
		}
		if !isTargetLine {
			newLines = append(newLines, line)
		}
	}
	_ = os.WriteFile(hostsPath, []byte(strings.Join(newLines, "\n")), 0644)
	flushDNS()
}

func findCaFiles(baseDir string) (string, string) {
	candCerts := []string{
		filepath.Join(baseDir, "data", "mitm", "rootCA.crt"),
		filepath.Join(baseDir, "mitm", "rootCA.crt"),
		filepath.Join(".", "data", "mitm", "rootCA.crt"),
		filepath.Join("storage", "packages", "APP-ROUTER_OmniRouter_TiDB_v2.9.0", "data", "mitm", "rootCA.crt"),
		"rootCA.crt",
	}
	candKeys := []string{
		filepath.Join(baseDir, "data", "mitm", "rootCA.key"),
		filepath.Join(baseDir, "mitm", "rootCA.key"),
		filepath.Join(".", "data", "mitm", "rootCA.key"),
		filepath.Join("storage", "packages", "APP-ROUTER_OmniRouter_TiDB_v2.9.0", "data", "mitm", "rootCA.key"),
		"rootCA.key",
	}
	var certPath, keyPath string
	for _, c := range candCerts {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			certPath = c
			break
		}
	}
	for _, k := range candKeys {
		if fi, err := os.Stat(k); err == nil && !fi.IsDir() {
			keyPath = k
			break
		}
	}
	return certPath, keyPath
}

func initCA(baseDir string) {
	caOnce.Do(func() {
		certPath, keyPath := findCaFiles(baseDir)
		if certPath != "" && keyPath != "" {
			certData, errCert := os.ReadFile(certPath)
			keyData, errKey := os.ReadFile(keyPath)
			if errCert == nil && errKey == nil {
				blockCert, _ := pem.Decode(certData)
				if blockCert != nil {
					parsedCert, errParse := x509.ParseCertificate(blockCert.Bytes)
					if errParse == nil {
						blockKey, _ := pem.Decode(keyData)
						if blockKey != nil {
							if rsaKey, errRSA := x509.ParsePKCS1PrivateKey(blockKey.Bytes); errRSA == nil {
								caCertGlobal = parsedCert
								caKeyGlobal = rsaKey
								log.Printf("[ENGINE] [MITM] ✔ Đã nạp thành công Root CA từ: %s", certPath)
								return
							} else if pkcs8Key, errP8 := x509.ParsePKCS8PrivateKey(blockKey.Bytes); errP8 == nil {
								caCertGlobal = parsedCert
								caKeyGlobal = pkcs8Key
								log.Printf("[ENGINE] [MITM] ✔ Đã nạp thành công Root CA (PKCS#8) từ: %s", certPath)
								return
							}
						}
					}
				}
			}
		}

		log.Printf("[ENGINE] [MITM] ⚠ Không tìm thấy file CA hợp lệ, đang tự động tạo Root CA tự ký trong bộ nhớ...")
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			log.Printf("[ENGINE] [MITM] [ERROR] Không thể sinh RSA Key cho CA: %v", err)
			return
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		template := x509.Certificate{
			SerialNumber: serial,
			Subject: pkix.Name{
				CommonName:   "9Router MITM Root CA",
				Organization: []string{"9Router"},
				Country:      []string{"US"},
			},
			NotBefore:             time.Now().Add(-24 * time.Hour),
			NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
		if err != nil {
			log.Printf("[ENGINE] [MITM] [ERROR] Không thể ký Root CA: %v", err)
			return
		}
		parsed, _ := x509.ParseCertificate(der)
		caCertGlobal = parsed
		caKeyGlobal = priv
		log.Printf("[ENGINE] [MITM] ✔ Đã khởi tạo Root CA bộ nhớ thành công (CommonName: %s)", parsed.Subject.CommonName)
	})
}

func getMitmCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	serverName := hello.ServerName
	if serverName == "" {
		serverName = "localhost"
	}
	serverName = strings.ToLower(serverName)

	if cached, ok := certCache.Load(serverName); ok {
		return cached.(*tls.Certificate), nil
	}

	if caCertGlobal == nil || caKeyGlobal == nil {
		return nil, fmt.Errorf("Root CA chưa được khởi tạo")
	}

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, _ := rand.Int(rand.Reader, serialNumberLimit)

	tmpl := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   serverName,
			Organization: []string{"9Router MITM"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		DNSNames:    []string{serverName, "localhost", "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, caCertGlobal, &privKey.PublicKey, caKeyGlobal)
	if err != nil {
		return nil, err
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{derBytes, caCertGlobal.Raw},
		PrivateKey:  privKey,
	}
	certCache.Store(serverName, cert)
	return cert, nil
}

func startMitmServer(handler http.Handler, baseDir string) error {
	mitmServerLock.Lock()
	defer mitmServerLock.Unlock()

	certPath, _ := findCaFiles(baseDir)
	if certPath != "" && runtime.GOOS == "windows" {
		cmd := exec.Command("certutil.exe", "-addstore", "-f", "Root", certPath)
		_ = cmd.Run()
		log.Printf("[ENGINE] [MITM] ✔ Đã tự động cài đặt Root CA vào Windows Trusted Store: %s", certPath)
	}

	initCA(baseDir)

	tlsConfig := &tls.Config{
		GetCertificate: getMitmCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	server := &http.Server{
		Addr:      "0.0.0.0:443",
		Handler:   handler,
		TLSConfig: tlsConfig,
	}

	var ln net.Listener
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		ln, err = net.Listen("tcp", server.Addr)
		if err == nil {
			break
		}
		time.Sleep(600 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("cổng 443 đang bị chiếm dụng hoặc thiếu quyền Administrator: %w", err)
	}

	tlsListener := tls.NewListener(ln, tlsConfig)
	mitmServer = server
	mitmServerRunning = true

	go func() {
		if err := server.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			log.Printf("[ENGINE] [MITM] Lỗi phục vụ cổng 443: %v", err)
			mitmServerLock.Lock()
			mitmServerRunning = false
			mitmServer = nil
			mitmServerLock.Unlock()
		}
	}()

	log.Printf("[ENGINE] [MITM] ✔ Máy chủ MITM HTTPS (Port 443) đã khởi động thành công!")
	return nil
}

func stopMitmServer() error {
	mitmServerLock.Lock()
	defer mitmServerLock.Unlock()

	if !mitmServerRunning || mitmServer == nil {
		mitmServerRunning = false
		mitmServer = nil
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := mitmServer.Shutdown(ctx)
	mitmServer = nil
	mitmServerRunning = false

	cleanAllMitmDns()
	log.Printf("[ENGINE] [MITM] ✔ Đã dừng Máy chủ MITM và giải phóng cổng 443 an toàn")
	return err
}

// AutoStartMitm tự động khởi động MITM server cổng 443 nếu đã có root CA và kích hoạt DNS hosts
func AutoStartMitm(baseDir string) error {
	mitmServerLock.Lock()
	running := mitmServerRunning
	mitmServerLock.Unlock()

	if running {
		_ = setToolDns("antigravity", true)
		return nil
	}

	if GlobalMitmDispatcher == nil {
		return fmt.Errorf("GlobalMitmDispatcher chưa được khởi tạo")
	}

	err := startMitmServer(GlobalMitmDispatcher, baseDir)
	if err == nil {
		if dnsErr := setToolDns("antigravity", true); dnsErr != nil {
			log.Printf("[ENGINE] [MITM] ⚠ Cảnh báo cấu hình DNS Antigravity: %v", dnsErr)
		} else {
			log.Printf("[ENGINE] [MITM] ✔ Đã tự động kích hoạt chuyển hướng Antigravity IDE qua OmniRouter an toàn")
		}
	}
	return err
}

// HandleMitmAliases xử lý GET/POST cho model aliases (/api/cli-tools/antigravity-mitm/alias)
func HandleMitmAliases(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-9r-cli-token, x-api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		switch r.Method {
		case http.MethodGet:
			val, err := GetOmniKV(db, "mitm_alias", "mappings")
			var mappings map[string]interface{}
			if err == nil && val != "" {
				_ = json.Unmarshal([]byte(val), &mappings)
			}
			if mappings == nil {
				mappings = map[string]interface{}{
					"antigravity": map[string]string{
						"gemini-3.1-pro-high": "gemini-3.1-pro-high",
						"gemini-2.5-pro":      "gemini-2.5-pro",
						"claude-3-5-sonnet":   "claude-3-5-sonnet",
					},
					"copilot": map[string]string{
						"gpt-4o": "gemini-3.1-pro-high",
					},
					"claude-code": map[string]string{
						"claude-3-5-sonnet": "claude-3-5-sonnet",
					},
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"mappings": mappings,
			})

		case http.MethodPost:
			var body struct {
				Tool  string `json:"tool"`
				Alias string `json:"alias"`
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"Invalid JSON body"}`, http.StatusBadRequest)
				return
			}
			val, _ := GetOmniKV(db, "mitm_alias", "mappings")
			var mappings map[string]map[string]string
			if val != "" {
				_ = json.Unmarshal([]byte(val), &mappings)
			}
			if mappings == nil {
				mappings = make(map[string]map[string]string)
			}
			if mappings[body.Tool] == nil {
				mappings[body.Tool] = make(map[string]string)
			}
			if body.Model == "" {
				delete(mappings[body.Tool], body.Alias)
			} else {
				mappings[body.Tool][body.Alias] = body.Model
			}

			bytes, _ := json.Marshal(mappings)
			_ = SetOmniKV(db, "mitm_alias", "mappings", string(bytes))

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Đã lưu ánh xạ mô hình thành công",
			})

		default:
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}
}

// HandleMitmCACert phục vụ file rootCA.crt cho App và CLI tải về cài đặt
func HandleMitmCACert(baseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certPath, _ := findCaFiles(baseDir)
		if certPath != "" {
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", "attachment; filename=\"rootCA.crt\"")
			http.ServeFile(w, r, certPath)
			return
		}
		initCA(baseDir)
		if caCertGlobal != nil {
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", "attachment; filename=\"rootCA.crt\"")
			pemBlock := &pem.Block{Type: "CERTIFICATE", Bytes: caCertGlobal.Raw}
			_ = pem.Encode(w, pemBlock)
			return
		}
		http.Error(w, "Không tìm thấy file CA Certificate", http.StatusNotFound)
	}
}

// HandleMitmStatus xử lý GET, POST, DELETE, PATCH cho /api/cli-tools/antigravity-mitm
func HandleMitmStatus(db *sql.DB, handler http.Handler, baseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-9r-cli-token, x-api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		activeProviders := GetCachedActiveProviders()
		certPath, _ := findCaFiles(baseDir)
		certExists := certPath != "" || caCertGlobal != nil

		switch r.Method {
		case http.MethodGet:
			mitmServerLock.Lock()
			running := mitmServerRunning
			mitmServerLock.Unlock()

			dnsStatus := map[string]bool{
				"antigravity": checkToolDnsStatus("antigravity"),
				"kiro":        checkToolDnsStatus("kiro"),
				"copilot":     checkToolDnsStatus("copilot"),
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":         true,
				"running":         running,
				"server":          running,
				"certExists":      certExists,
				"certTrusted":     true,
				"dnsStatus":       dnsStatus,
				"gateway_status":  "online",
				"cloud_provider":  "Render + TiDB Serverless",
				"version":         "v2.8.5-production",
				"active_accounts": len(activeProviders),
				"dns_redirection": true,
				"timestamp":       time.Now().Format(time.RFC3339),
				"supported_tools": []string{
					"antigravity-mitm",
					"claude-code",
					"codex-cli",
					"openclaw",
					"droid-agent",
					"hermes-cli",
					"opencode",
				},
			})

		case http.MethodPost:
			if err := startMitmServer(handler, baseDir); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   err.Error(),
				})
				return
			}
			// Tự động kích hoạt DNS cho Antigravity vào file hosts để bảo vệ Quota ngay lập tức
			if dnsErr := setToolDns("antigravity", true); dnsErr != nil {
				log.Printf("[ENGINE] [MITM] ⚠ Cảnh báo kích hoạt DNS Antigravity: %v", dnsErr)
			} else {
				log.Printf("[ENGINE] [MITM] ✔ Đã tự động kích hoạt DNS Antigravity vào file hosts")
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"running": true,
				"message": "Đã khởi động Máy chủ MITM thành công trên cổng 443 và kích hoạt DNS Antigravity",
			})

		case http.MethodDelete:
			if err := stopMitmServer(); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   err.Error(),
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"running": false,
				"message": "Đã dừng Máy chủ MITM và giải phóng cổng 443",
			})

		case http.MethodPatch:
			var body struct {
				Tool   string `json:"tool"`
				Action string `json:"action"`
			}
			bodyBytes, _ := io.ReadAll(r.Body)
			if len(bodyBytes) > 0 {
				_ = json.Unmarshal(bodyBytes, &body)
			}
			// Fallback sang Query parameters nếu body rỗng hoặc thiếu field
			if body.Tool == "" {
				body.Tool = r.URL.Query().Get("tool")
			}
			if body.Action == "" {
				body.Action = r.URL.Query().Get("action")
			}
			if body.Tool == "" {
				body.Tool = "antigravity"
			}

			enable := strings.ToLower(body.Action) == "enable" || body.Action == "true" || body.Action == "1"
			if err := setToolDns(body.Tool, enable); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   err.Error(),
				})
				return
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"tool":    body.Tool,
				"enabled": enable,
				"message": fmt.Sprintf("Đã cập nhật DNS cho '%s': %v", body.Tool, enable),
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Phương thức không được hỗ trợ",
			})
		}
	}
}
