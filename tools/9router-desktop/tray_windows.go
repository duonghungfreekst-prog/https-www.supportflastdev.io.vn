//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW      = modUser32.NewProc("CreateWindowExW")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procLoadIconW           = modUser32.NewProc("LoadIconW")
	procLoadImageW          = modUser32.NewProc("LoadImageW")
	procShell_NotifyIconW   = modShell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = modKernel32.NewProc("GetModuleHandleW")
)

const (
	WM_USER         = 0x0400
	WM_TRAY_MSG     = WM_USER + 1
	WM_LBUTTONUP    = 0x0202
	WM_RBUTTONUP    = 0x0205
	WM_COMMAND      = 0x0111
	WM_DESTROY      = 0x0002

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	MF_STRING       = 0x00000000
	MF_SEPARATOR    = 0x00000800
	TPM_RIGHTBUTTON = 0x0002

	ID_OPEN_UI      = 1001
	ID_TOGGLE_PROXY = 1002
	ID_SYNC_NOW     = 1003
	ID_EXIT         = 1004

	IDI_APPLICATION = 32512

	IMAGE_ICON      = 1
	LR_LOADFROMFILE = 0x00000010
	LR_DEFAULTSIZE  = 0x00000040
)

type WNDCLASSEXW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type NOTIFYICONDATAW struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          [16]byte
	hBalloonIcon      uintptr
}

type POINT struct {
	X int32
	Y int32
}

type MSG struct {
	hWnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      POINT
}

var (
	globalClient *DesktopClient
	trayHwnd     uintptr
	nid          NOTIFYICONDATAW
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyUTF16(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	copy(dst, u)
}

// openDesktopWindow mở cửa sổ ứng dụng desktop độc lập (Edge App Window, Chrome App hoặc Browser)
func openDesktopWindow(port int) {
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	profileDir := filepath.Join(os.TempDir(), "OmniRouter_Client_Profile")

	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`),
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `CocCoc\Browser\Application\browser.exe`),
	}

	for _, browserPath := range candidates {
		if _, err := os.Stat(browserPath); err == nil {
			cmd := exec.Command(browserPath,
				fmt.Sprintf("--app=%s", url),
				"--window-size=1260,860",
				fmt.Sprintf("--user-data-dir=%s", profileDir),
				"--disable-pinch",
			)
			_ = cmd.Start()
			return
		}
	}

	// Fallback mở trình duyệt qua rundll32 không hiện cửa sổ console
	rundll := filepath.Join(os.Getenv("SystemRoot"), "System32", "rundll32.exe")
	cmd := exec.Command(rundll, "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}

func trayWndProc(hWnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case WM_TRAY_MSG:
		switch lParam {
		case WM_LBUTTONUP:
			// Nhấp chuột trái: Mở lại cửa sổ Desktop GUI
			if globalClient != nil {
				openDesktopWindow(globalClient.config.LocalPort)
			}
		case WM_RBUTTONUP:
			// Nhấp chuột phải: Hiện Menu Khay Hệ Thống
			hMenu, _, _ := procCreatePopupMenu.Call()
			if hMenu != 0 {
				var statusText string
				if globalClient.isProxyEnabled() {
					statusText = "🟢 Trạng thái: Đang BẬT Proxy"
				} else {
					statusText = "🔴 Trạng thái: Đang TẮT Proxy"
				}

				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_OPEN_UI), uintptr(unsafe.Pointer(utf16Ptr("⚡ Mở Bảng Điều Khiển OmniRouter"))))
				procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_TOGGLE_PROXY), uintptr(unsafe.Pointer(utf16Ptr(statusText))))
				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_SYNC_NOW), uintptr(unsafe.Pointer(utf16Ptr("🔄 Đồng Bộ Token & Quota Ngay"))))
				procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_EXIT), uintptr(unsafe.Pointer(utf16Ptr("❌ Thoát Hoàn Toàn"))))

				var pt POINT
				procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				procSetForegroundWindow.Call(hWnd)
				procTrackPopupMenu.Call(hMenu, uintptr(TPM_RIGHTBUTTON), uintptr(pt.X), uintptr(pt.Y), 0, hWnd, 0)
			}
		}
		return 0

	case WM_COMMAND:
		switch wParam {
		case ID_OPEN_UI:
			if globalClient != nil {
				openDesktopWindow(globalClient.config.LocalPort)
			}
		case ID_TOGGLE_PROXY:
			if globalClient != nil {
				newState := !globalClient.isProxyEnabled()
				globalClient.setProxyEnabled(newState)
				log.Printf("[TRAY] Proxy đã chuyển sang: %v", newState)
			}
		case ID_SYNC_NOW:
			if globalClient != nil {
				go globalClient.syncWithRender()
			}
		case ID_EXIT:
			procShell_NotifyIconW.Call(uintptr(NIM_DELETE), uintptr(unsafe.Pointer(&nid)))
			procPostQuitMessage.Call(0)
			os.Exit(0)
		}
		return 0

	case WM_DESTROY:
		procShell_NotifyIconW.Call(uintptr(NIM_DELETE), uintptr(unsafe.Pointer(&nid)))
		procPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(hWnd, uintptr(msg), wParam, lParam)
	return r
}

// StartSystemTray khởi tạo System Tray và chạy Message Loop trên Windows
func StartSystemTray(client *DesktopClient) {
	globalClient = client

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className := utf16Ptr("OmniRouterClientTrayClass")

	wndClass := WNDCLASSEXW{
		cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hInst,
		lpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClass)))

	trayHwnd, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16Ptr("OmniRouter Tray Window"))),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)

	// Thử load icon từ file app.ico cạnh file thực thi
	var hIcon uintptr
	execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	icoPath := filepath.Join(execDir, "app.ico")
	if _, err := os.Stat(icoPath); err == nil {
		hIconRet, _, _ := procLoadImageW.Call(
			0,
			uintptr(unsafe.Pointer(utf16Ptr(icoPath))),
			uintptr(IMAGE_ICON),
			0, 0,
			uintptr(LR_LOADFROMFILE|LR_DEFAULTSIZE),
		)
		if hIconRet != 0 {
			hIcon = hIconRet
		}
	}
	if hIcon == 0 {
		hIcon, _, _ = procLoadIconW.Call(0, uintptr(IDI_APPLICATION))
	}

	nid = NOTIFYICONDATAW{
		cbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
		hWnd:             trayHwnd,
		uID:              1,
		uFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		uCallbackMessage: WM_TRAY_MSG,
		hIcon:            hIcon,
	}
	copyUTF16(nid.szTip[:], "OmniRouter Client — Node Chia Sẻ & Smart DNS")

	procShell_NotifyIconW.Call(uintptr(NIM_ADD), uintptr(unsafe.Pointer(&nid)))
	log.Println("[TRAY] ✔ Biểu tượng OmniRouter Client đã xuất hiện dưới Khay Hệ Thống (System Tray)")

	// Tự động mở cửa sổ giao diện khi khởi động
	go openDesktopWindow(client.config.LocalPort)

	// Message Loop của Windows
	var msg MSG
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
