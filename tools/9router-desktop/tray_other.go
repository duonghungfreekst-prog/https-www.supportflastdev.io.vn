//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// StartSystemTray cho các hệ điều hành không phải Windows
func StartSystemTray(client *DesktopClient) {
	log.Println("[INFO] Chế độ Terminal (Hệ điều hành non-Windows)")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Println("[INFO] Nhận tín hiệu dừng, thoát an toàn.")
	os.Exit(0)
}
