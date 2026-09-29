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

// handleTunnelStatus tráº£ vá» tráº¡ng thÃ¡i cá»§a Ä‘Æ°á»ng háº§m
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

// handleTunnelStart báº¯t Ä‘áº§u káº¿t ná»‘i SSH tunnel
func (s *Server) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	// Chá»‰ Admin má»›i cÃ³ quyá»n báº­t Tunnel
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thiáº¿t láº­p Truy cáº­p tá»« xa", nil)
		return
	}

	tunnelMu.Lock()
	defer tunnelMu.Unlock()

	if tunnelCmd != nil && tunnelCmd.Process != nil && tunnelCmd.ProcessState == nil {
		// Äang cháº¡y rá»“i
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"running": true,
			"url":     tunnelURL,
		})
		return
	}

	// Sá»­ dá»¥ng SSH tÃ­ch há»£p sáºµn trÃªn Windows Ä‘á»ƒ táº¡o Tunnel tá»›i localhost.run
	tunnelCmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ServerAliveInterval=30", "-R", "80:localhost:8080", "nokey@localhost.run")
	
	stdout, err := tunnelCmd.StdoutPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o Ä‘Æ°á»ng á»‘ng (pipe) máº¡ng", err)
		return
	}
	stderr, err := tunnelCmd.StderrPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Lá»—i táº¡o Ä‘Æ°á»ng á»‘ng (pipe) máº¡ng", err)
		return
	}

	if err := tunnelCmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "KhÃ´ng thá»ƒ khá»Ÿi Ä‘á»™ng káº¿t ná»‘i SSH. Há»‡ Ä‘iá»u hÃ nh khÃ´ng há»— trá»£ hoáº·c bá»‹ cháº·n.", err)
		return
	}

	tunnelURL = ""
	
	// DÃ¹ng Regex Ä‘á»ƒ quÃ©t link HTTPS sinh ra tá»« mÃ¡y chá»§
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
	
	// Giáº£i phÃ³ng tiáº¿n trÃ¬nh ngáº§m khi thoÃ¡t
	go func() {
		_ = tunnelCmd.Wait()
		tunnelMu.Lock()
		tunnelCmd = nil
		tunnelURL = ""
		tunnelMu.Unlock()
	}()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": true,
		"message": "Äang káº¿t ná»‘i Ä‘áº¿n Tráº¡m trung chuyá»ƒn. Xin chá» vÃ i giÃ¢y...",
	})
}

// handleTunnelStop táº¯t Ä‘Æ°á»ng háº§m
func (s *Server) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	// Chá»‰ Admin má»›i cÃ³ quyá»n táº¯t
	user := s.getUserFromRequest(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Chá»‰ Quáº£n trá»‹ viÃªn má»›i cÃ³ quyá»n thiáº¿t láº­p Truy cáº­p tá»« xa", nil)
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

