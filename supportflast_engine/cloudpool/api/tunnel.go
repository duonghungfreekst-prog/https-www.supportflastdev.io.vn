package api

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
)

var (
	tunnelMu  sync.Mutex
	tunnelCmd *exec.Cmd
	tunnelURL string
)

// handleTunnelStatus trả về trạng thái của đường hầm
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": true,
		"type":    "cloudflare_tunnel",
		"domain":  "supportflastdev.io.vn",
		"url":     "https://supportflastdev.io.vn",
		"urls": []string{
			"https://supportflastdev.io.vn",
			"https://www.supportflastdev.io.vn",
		},
		"message": "Cloudflare Zero Trust Tunnel 24/7 đang hoạt động trực tuyến",
	})
}

// handleTunnelStart bắt đầu kết nối SSH tunnel
func (s *Server) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	// Chỉ Admin mới có quyền bật Tunnel
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền thiết lập Truy cập từ xa", nil)
		return
	}

	tunnelMu.Lock()
	defer tunnelMu.Unlock()

	if tunnelCmd != nil && tunnelCmd.Process != nil && tunnelCmd.ProcessState == nil {
		// Đang chạy rồi
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"running": true,
			"url":     tunnelURL,
		})
		return
	}

	// Sử dụng SSH tích hợp sẵn trên Windows để tạo Tunnel tới localhost.run
	tunnelCmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ServerAliveInterval=30", "-R", "80:localhost:8080", "nokey@localhost.run")
	
	stdout, err := tunnelCmd.StdoutPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo đường ống (pipe) mạng", err)
		return
	}
	stderr, err := tunnelCmd.StderrPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lỗi tạo đường ống (pipe) mạng", err)
		return
	}

	if err := tunnelCmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "Không thể khởi động kết nối SSH. Hệ điều hành không hỗ trợ hoặc bị chặn.", err)
		return
	}

	tunnelURL = ""
	
	// Dùng Regex để quét link HTTPS sinh ra từ máy chủ
	urlRegex := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.lhr\.life`)
	
	scanOutput := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println("[TUNNEL]", line) // Log để monitor
			if match := urlRegex.FindString(line); match != "" {
				tunnelMu.Lock()
				tunnelURL = match
				tunnelMu.Unlock()
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("[TUNNEL] [WARN] Scanner error: %v", err)
		}
	}
	
	go scanOutput(stdout)
	go scanOutput(stderr)
	
	// Giải phóng tiến trình ngầm khi thoát
	go func() {
		_ = tunnelCmd.Wait()
		tunnelMu.Lock()
		tunnelCmd = nil
		tunnelURL = ""
		tunnelMu.Unlock()
	}()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": true,
		"message": "Đang kết nối đến Trạm trung chuyển. Xin chờ vài giây...",
	})
}

// handleTunnelStop tắt đường hầm
func (s *Server) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	// Chỉ Admin mới có quyền tắt
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chỉ Quản trị viên mới có quyền thiết lập Truy cập từ xa", nil)
		return
	}

	tunnelMu.Lock()
	defer tunnelMu.Unlock()

	if tunnelCmd != nil && tunnelCmd.Process != nil {
		_ = tunnelCmd.Process.Kill()
		tunnelCmd = nil
		tunnelURL = ""
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": false,
	})
}

