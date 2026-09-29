//go:build windows

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
)

type supportflastWindowsService struct {
	server *http.Server
	isTLS  bool
}

func (s *supportflastWindowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	go func() {
		var err error
		if s.isTLS {
			err = s.server.ListenAndServeTLS("", "")
		} else {
			err = s.server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[ENGINE] [SERVICE] Server failed: %v", err)
		}
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for req := range r {
		switch req.Cmd {
		case svc.Interrogate:
			changes <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = s.server.Shutdown(ctx)
			cancel()
			return
		default:
			log.Printf("[ENGINE] [SERVICE] Unexpected control request #%d", req.Cmd)
		}
	}
	return
}

func runServer(server *http.Server) error {
	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		log.Println("[ENGINE] [SERVICE] Phát hiện khởi động từ Windows Service Control Manager - Chạy dưới dạng Windows Service...")
		return svc.Run("SupportFlastEngine", &supportflastWindowsService{server: server, isTLS: false})
	}

	// Chạy dạng Console/Background với cơ chế bắt tín hiệu dừng an toàn (Graceful Shutdown)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		return err
	case sig := <-sigChan:
		log.Printf("[ENGINE] [SIGNAL] Nhận tín hiệu dừng (%v), đang tắt server an toàn...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}

func runTLSServer(server *http.Server) error {
	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		log.Println("[ENGINE] [SERVICE] Phát hiện khởi động từ Windows Service Control Manager - Chạy TLS dưới dạng Windows Service...")
		return svc.Run("SupportFlastEngine", &supportflastWindowsService{server: server, isTLS: true})
	}

	// Chạy dạng Console/Background với cơ chế bắt tín hiệu dừng an toàn (Graceful Shutdown)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServeTLS("", "")
	}()

	select {
	case err := <-serverErr:
		return err
	case sig := <-sigChan:
		log.Printf("[ENGINE] [SIGNAL] Nhận tín hiệu dừng (%v), đang tắt TLS server an toàn...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}
