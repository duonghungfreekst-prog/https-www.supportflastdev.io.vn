# HƯỚNG DẪN TRIỂN KHAI TOÀN DIỆN TRÊN CPANEL & SHARED HOSTING
## Tích Hợp Apache / LiteSpeed Web Server, .htaccess Reverse Proxy, WebSocket & WebDAV
### ⭐️ Dành cho SupportFlast Unified Suite (Go Gateway Engine + Web 3D + SQLite WAL)

---

> [!IMPORTANT]
> **MỤC TIÊU CỦA TÀI LIỆU NÀY**:
> Hướng dẫn triển khai trọn vẹn bộ ứng dụng **SupportFlast** lên môi trường **Shared Hosting** (chạy cPanel, DirectAdmin, Plesk, CloudLinux OS với Apache 2.4+ hoặc LiteSpeed Web Server).
> - **Chi phí cực thấp**: Tận dụng hosting giá rẻ ($1 - $3/tháng) mà vẫn vận hành được Go Backend Engine hiệu năng cao.
> - **Không cần quyền Root**: Tận dụng triệt để các công cụ sẵn có trên cPanel (File Manager, Cron Jobs, Terminal, AutoSSL, .htaccess).
> - **Hỗ trợ đầy đủ tính năng**: Reverse Proxy toàn bộ Web UI, REST API, truyền tải thời gian thực **WebSocket (`/ws`)**, và hệ thống lưu trữ tệp đám mây **WebDAV (`/webdav/`)**.

---

## 📑 MỤC LỤC CHI TIẾT

1. [Tổng Quan Kiến Trúc Reverse Proxy Trên Shared Hosting](#1-tổng-quan-kiến-trúc-reverse-proxy-trên-shared-hosting)
2. [Đóng Gói Binary Go Engine Cho Linux (Cross-Compilation Không Cần CGO)](#2-đóng-gói-binary-go-engine-cho-linux-cross-compilation-không-cần-cgo)
3. [Tổ Chức Thư Mục & Tải Lên Qua cPanel File Manager](#3-tổ-chức-thư-mục--tải-lên-qua-cpanel-file-manager)
4. [Phân Quyền Thực Thi Binary & Bảo Vệ Database (chmod 755 & chmod 700)](#4-phân-quyền-thực-thi-binary--bảo-vệ-database-chmod-755--chmod-700)
5. [Thiết Lập Tệp Cấu Hình Reverse Proxy .htaccess](#5-thiết-lập-tệp-cấu-hình-reverse-proxy-htaccess)
6. [Các Giải Pháp Duy Trì Tiến Trình Nền (Background Daemon Chạy Bền Bỉ)](#6-các-giải-pháp-duy-trì-tiến-trình-nền-background-daemon-chạy-bền-bỉ)
   - [Phương án 1: cPanel Cron Job + Watchdog Script (Khuyên dùng - 100% Shared Host đều hỗ trợ)](#phương-án-1-cpanel-cron-job--watchdog-script-khuyên-dùng)
   - [Phương án 2: cPanel Terminal (SSH) với nohup / tmux](#phương-án-2-cpanel-terminal-ssh-với-nohup--tmux)
   - [Phương án 3: Setup Node.js App / Phusion Passenger Wrapper (Cứu cánh khi host cấm mở port socket)](#phương-án-3-setup-nodejs-app--phusion-passenger-wrapper)
7. [Cấu Hình Chứng Chỉ SSL/TLS Miễn Phí với cPanel AutoSSL](#7-cấu-hình-chứng-chỉ-ssltls-miễn-phí-với-cpanel-autossl)
8. [Kiểm Tra & Tối Ưu Kết Nối WebSocket (/ws) & WebDAV (/webdav/)](#8-kiểm-tra--tối-ưu-kết-nối-websocket-ws--webdav-webdav)
9. [Xử Lý Sự Cố Thường Gặp (Troubleshooting & FAQs)](#9-xử-lý-sự-cố-thường-gặp-troubleshooting--faqs)
10. [Checklist Kiểm Tra Hoàn Tất Trước Khi Bàn Giao](#10-checklist-kiểm-tra-hoàn-tất-trước-khi-bàn-giao)

---

## 1. TỔNG QUAN KIẾN TRÚC REVERSE PROXY TRÊN SHARED HOSTING

### 1.1 Sơ đồ luồng xử lý
Trên Shared Hosting, cổng `80` (HTTP) và `443` (HTTPS) do Web Server chính của hệ thống quản lý (thường là Apache hoặc LiteSpeed). Người dùng thông thường không có quyền root để chiếm hai cổng này. Do đó, mô hình kiến trúc chuẩn là:

```
[ Trình duyệt / Ứng dụng Client ]
                │
                ▼ HTTPS (Port 443) - Cấp bởi cPanel AutoSSL
┌───────────────────────────────────────────────────────────────┐
│               MÁY CHỦ SHARED HOSTING (cPanel / CloudLinux)    │
│                                                               │
│   [ Apache 2.4+ / LiteSpeed Web Server ]                      │
│      ├── Quản lý SSL/TLS Handshake                            │
│      ├── Nén dữ liệu Gzip (mod_deflate) / Brotli (mod_brotli) │
│      └── Đọc cấu hình từ: /home/username/public_html/.htaccess │
│                                                               │
│             │ Chuyển tiếp (Reverse Proxy qua mod_proxy / [P])  │
│             ▼ Cục bộ nội bộ: http://127.0.0.1:8080            │
│                                                               │
│   [ SupportFlast Unified Go Engine ] (Binary chạy ngầm)       │
│      ├── Port lắng nghe cục bộ: 127.0.0.1:8080               │
│      ├── Giao diện Web 3D Three.js HUD (supportflast_ui)      │
│      ├── Hệ thống REST API Gateway (/api/...)                 │
│      ├── Kết nối thời gian thực WebSocket (/ws)               │
│      ├── Máy chủ lưu trữ tệp đám mây WebDAV (/webdav/)        │
│      └── Cơ sở dữ liệu cục bộ: data/supportflast.db (SQLite)  │
│                                                               │
│   [ Watchdog Cron Job ] ──── (Tự động kiểm tra & hồi sinh)    │
└───────────────────────────────────────────────────────────────┘
```

### 1.2 Ưu điểm vượt trội của giải pháp này
- **Tận dụng 100% tài nguyên**: Go Engine được biên dịch sang mã máy thuần (Native Binary), chạy siêu nhẹ chỉ tốn khoảng **15 - 25 MB RAM**, nằm gọn trong giới hạn LVE của CloudLinux (thường cho phép từ 512MB đến 1GB RAM).
- **SQLite Không Cần MySQL**: Tránh quá tải MySQL connection pool trên shared host. SQLite ở chế độ WAL cho phép đọc ghi cực nhanh.
- **Tự động cấp SSL**: Sử dụng trực tiếp AutoSSL tích hợp sẵn trên cPanel (Let's Encrypt hoặc Sectigo Comodo), không cần cài đặt Certbot thủ công.

---

## 2. ĐÓNG GÓI BINARY GO ENGINE CHO LINUX (CROSS-COMPILATION KHÔNG CẦN CGO)

Dự án SupportFlast Engine sử dụng thư viện SQLite thuần Go (`modernc.org/sqlite`), hoàn toàn không phụ thuộc vào GCC hay CGO. Do đó, bạn có thể biên dịch trực tiếp từ máy phát triển (Windows / macOS) ra file thực thi chạy trên máy chủ Linux mà không cần cài đặt thêm bất kỳ công cụ phức tạp nào.

### 2.1 Biên dịch trên Windows (PowerShell)
Mở PowerShell tại thư mục gốc dự án (`f:\supportflast.dev\supportflast_engine`) và chạy các lệnh sau:

```powershell
# Di chuyển vào thư mục mã nguồn Go Engine
cd f:\supportflast.dev\supportflast_engine

# Thiết lập biến môi trường Cross-compile cho Linux 64-bit
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="amd64"

# Tiến hành biên dịch và tối ưu loại bỏ debug symbols (-s -w để file nhỏ nhất)
go build -ldflags="-s -w" -o supportflast .

# Kiểm tra file đã tạo
Get-Item supportflast
```

> [!TIP]
> File binary tạo ra mang tên `supportflast` (không có phần mở rộng `.exe`), dung lượng khoảng 20-30MB. File này hoàn toàn độc lập và tương thích với hầu hết các bản phân phối Linux như CloudLinux, CentOS 7/8/9, AlmaLinux, Rocky Linux, Ubuntu, Debian.

---

## 3. TỔ CHỨC THƯ MỤC & TẢI LÊN QUA CPANEL FILE MANAGER

Để đảm bảo an toàn tuyệt đối, chúng ta áp dụng mô hình phân tách thư mục:
- **Thư mục Web Root (`/home/username/public_html/`)**: Chỉ chứa file `.htaccess` và các tài nguyên web tĩnh công khai.
- **Thư mục Ứng dụng Nội bộ (`/home/username/app_supportflast/`)**: Nằm ngoài thư mục `public_html`, người dùng bên ngoài không thể truy cập trực tiếp bằng trình duyệt. Chứa file binary `supportflast`, thư mục `data/` (SQLite database), và các script vận hành.

### 3.1 Cây thư mục tối ưu trên cPanel Hosting
```
/home/username/
├── app_supportflast/                   <-- [BẢO MẬT] Nằm ngoài public_html
│   ├── supportflast                    <-- Binary Go Engine vừa build (chmod 755)
│   ├── watchdog.sh                     <-- Script kiểm tra & hồi sinh tiến trình (chmod 755)
│   ├── data/                           <-- Thư mục chứa CSDL SQLite (chmod 700)
│   │   ├── supportflast.db
│   │   ├── supportflast.db-wal
│   │   └── supportflast.db-shm
│   └── storage/                        <-- Thư mục chứa file WebDAV / CloudPool
│
└── public_html/                        <-- [WEB ROOT] Thư mục công khai
    ├── .htaccess                       <-- File cấu hình Reverse Proxy & Security
    └── supportflast_ui/                <-- Giao diện Web 3D (HTML, JS, CSS, Assets)
```

### 3.2 Các bước tải lên qua cPanel File Manager (Quản lý Tệp)

1. **Nén các tệp cần thiết trên máy tính**:
   - Nén file binary `supportflast`, thư mục `supportflast_ui/` thành file `deploy.zip`.
2. **Đăng nhập cPanel**:
   - Truy cập vào trang quản trị cPanel (ví dụ: `https://yourdomain.com:2083`).
   - Vào mục **Files (Tệp)** ➔ **File Manager (Bộ quản lý tệp)**.
3. **Tạo thư mục ứng dụng nội bộ**:
   - Tại thư mục gốc người dùng (`/home/username/`), nhấn **+ Folder** để tạo thư mục mới: `app_supportflast`.
   - Mở thư mục `app_supportflast`, nhấn nút **Upload (Tải lên)** để tải file `deploy.zip` lên.
   - Nhấn chuột phải vào `deploy.zip` ➔ chọn **Extract (Giải nén)**.
4. **Cấu hình thư mục `public_html`**:
   - Chuyển vào thư mục `public_html/`.
   - Đảm bảo tính năng **Show Hidden Files (Hiển thị tệp ẩn - dotfiles)** đã được bật trong mục **Settings (Cài đặt)** ở góc trên bên phải của File Manager.
   - Tải file `.htaccess` lên thư mục `public_html/` (hoặc tạo file mới đặt tên `.htaccess` và dán nội dung vào).
   - Đưa thư mục `supportflast_ui/` vào `public_html/` để Web Server phục vụ nhanh hoặc cho Go Engine phục vụ.

---

## 4. PHÂN QUYỀN THỰC THI BINARY & BẢO VỆ DATABASE (CHMOD 755 & CHMOD 700)

Trên Linux, một file muốn chạy được dưới dạng chương trình (executable) phải được cấp quyền thực thi (`x`).

### 4.1 Cấp quyền qua cPanel File Manager (Giao diện đồ họa)
1. Trong File Manager, duyệt đến file `/home/username/app_supportflast/supportflast`.
2. Nhấp chuột phải vào file ➔ chọn **Change Permissions (Đổi quyền)** (hoặc phím tắt nhấn vào cột Permissions).
3. Đánh dấu các ô để đạt giá trị `755`:
   - **User (Chủ sở hữu)**: Đọc (R) ✅, Ghi (W) ✅, Thực thi (X) ✅ (7)
   - **Group (Nhóm)**: Đọc (R) ✅, Thực thi (X) ✅ (5)
   - **World/Others (Khác)**: Đọc (R) ✅, Thực thi (X) ✅ (5)
   - Nhấn **Change Permissions**.
4. Tiếp tục nhấp chuột phải vào thư mục `data/` ➔ chọn **Change Permissions** ➔ chỉnh thành `700` (Chỉ chủ sở hữu được đọc, ghi, thực thi, chặn toàn bộ người dùng khác trên cùng máy chủ shared hosting).

### 4.2 Cấp quyền qua cPanel Terminal (Nếu gói host có Terminal / SSH)
Nếu hosting có mở tính năng **Terminal**, bạn chỉ cần mở ra và gõ lệnh:
```bash
# Cấp quyền thực thi cho file chạy và script giám sát
chmod 755 /home/username/app_supportflast/supportflast
chmod 755 /home/username/app_supportflast/watchdog.sh

# Khóa chặt quyền cho thư mục dữ liệu SQLite
chmod 700 /home/username/app_supportflast/data
```

---

## 5. THIẾT LẬP TỆP CẤU HÌNH REVERSE PROXY .htaccess

File `.htaccess` đặt tại thư mục `/home/username/public_html/.htaccess` là trái tim điều phối toàn bộ hệ thống. Dưới đây là nội dung chuẩn hóa hoàn chỉnh:

```apache
# ==============================================================================
# SUPPORTFLAST.DEV - TẬP TIN CẤU HÌNH .htaccess CHUẨN CPANEL & SHARED HOSTING
# ==============================================================================
# Tương thích tối ưu cho: Apache 2.4+, LiteSpeed Web Server (LSWS), OpenLiteSpeed,
# CloudLinux (cPanel / DirectAdmin / Plesk).
# ==============================================================================

# ------------------------------------------------------------------------------
# 1. CẤU HÌNH MÁY CHỦ CƠ BẢN
# ------------------------------------------------------------------------------
Options -Indexes -MultiViews +FollowSymLinks
ServerSignature Off
AddDefaultCharset UTF-8

# ------------------------------------------------------------------------------
# 2. BẢO MẬT: CHẶN TRUY CẬP TRỰC TIẾP CÁC FILE NHẠY CẢM VÀ MÃ NGUỒN
# ------------------------------------------------------------------------------
<FilesMatch "(^\.|\.(env|sqlite|sqlite3|db|db-wal|db-shm|sql|log|bak|backup|old|conf|cfg|ini|json|toml|yaml|yml|sh|bat|ps1|go|mod|sum|rs|py|cs|csproj|sln|dll|exe|dockerignore|gitattributes|gitignore|lock))$">
    <IfModule mod_authz_core.c>
        Require all denied
    </IfModule>
    <IfModule !mod_authz_core.c>
        Order deny,allow
        Deny from all
    </IfModule>
</FilesMatch>

<FilesMatch "^(Dockerfile|docker-compose\.yml|Caddyfile|Makefile|fly\.toml|render\.yaml|cloudflared_config\.yml|nginx_.*\.conf|README\.md|.*_GUIDE\.md)$">
    <IfModule mod_authz_core.c>
        Require all denied
    </IfModule>
    <IfModule !mod_authz_core.c>
        Order deny,allow
        Deny from all
    </IfModule>
</FilesMatch>

# ------------------------------------------------------------------------------
# 3. THIẾT LẬP BỘ SECURITY HEADERS (CHUẨN OWASP & ANTIGRAVITY PHẦN 3.4)
# ------------------------------------------------------------------------------
<IfModule mod_headers.c>
    Header always set X-Content-Type-Options "nosniff"
    Header always set X-Frame-Options "DENY"
    Header always set X-XSS-Protection "1; mode=block"
    Header always set Referrer-Policy "strict-origin-when-cross-origin"
    Header always set Permissions-Policy "geolocation=(), camera=(), microphone=(), payment=(), usb=()"
    Header always set Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
    Header always set Content-Security-Policy "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self' ws: wss: http: https:; frame-ancestors 'none';"
    Header unset Server
    Header unset X-Powered-By
    Header unset X-AspNet-Version
</IfModule>

# ------------------------------------------------------------------------------
# 4. TỐI ƯU HÓA NÉN DỮ LIỆU BĂNG THÔNG CAO (BROTLI & GZIP DEFLATE)
# ------------------------------------------------------------------------------
<IfModule mod_brotli.c>
    AddOutputFilterByType BROTLI_COMPRESS text/html text/plain text/xml text/css text/javascript application/javascript application/json application/xml image/svg+xml
</IfModule>
<IfModule mod_deflate.c>
    AddOutputFilterByType DEFLATE text/html text/plain text/xml text/css text/javascript application/javascript application/json application/xml image/svg+xml
</IfModule>

# ------------------------------------------------------------------------------
# 5. ĐIỀU PHỐI ĐƯỜNG DẪN & REVERSE PROXY TỚI GO BACKEND (PORT 8080)
# ------------------------------------------------------------------------------
<IfModule mod_rewrite.c>
    RewriteEngine On
    RewriteBase /

    # 5.1. BẢO TỒN XÁC THỰC AUTOSSL / LET'S ENCRYPT (BẮT BUỘC ĐỂ KHÔNG LỖI SSL)
    RewriteCond %{REQUEST_URI} ^/\.well-known/acme-challenge/.*$ [NC]
    RewriteRule ^ - [L]

    # 5.2. TỰ ĐỘNG CHUYỂN HƯỚNG TẤT CẢ TRAFFIC SANG HTTPS AN TOÀN
    RewriteCond %{HTTPS} !=on
    RewriteCond %{HTTP:X-Forwarded-Proto} !https
    RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [L,R=301]

    # 5.3. CHẶN TRUY CẬP VÀO CÁC THƯ MỤC HỆ THỐNG NỘI BỘ
    RewriteRule ^(data|supportflast_ai|supportflast_core|supportflast_engine|infra|tests|tools)(/.*)?$ - [F,L,NC]

    # 5.4. GẮN HEADER CHUYỂN TIẾP CHO GO BACKEND
    <IfModule mod_headers.c>
        RequestHeader set X-Real-IP %{REMOTE_ADDR}s
        RequestHeader set X-Forwarded-For %{REMOTE_ADDR}s
        RequestHeader set X-Forwarded-Proto "https" env=HTTPS
        RequestHeader set X-Forwarded-Port "443" env=HTTPS
    </IfModule>

    # 5.5. REVERSE PROXY CHO KẾT NỐI WEBSOCKET (/ws HOẶC CONNECTION: UPGRADE)
    RewriteCond %{HTTP:Upgrade} =websocket [NC,OR]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^(.*)$ ws://127.0.0.1:8080/$1 [P,L]

    RewriteRule ^ws(/.*)?$ ws://127.0.0.1:8080/ws$1 [P,L]

    # 5.6. REVERSE PROXY CHO MÁY CHỦ LƯU TRỮ WEBDAV (/webdav/)
    # Giữ nguyên các HTTP Method: PROPFIND, PROPPATCH, MKCOL, COPY, MOVE, LOCK, UNLOCK
    RewriteCond %{REQUEST_METHOD} ^(PROPFIND|PROPPATCH|MKCOL|COPY|MOVE|LOCK|UNLOCK|OPTIONS|PUT|DELETE)$ [OR]
    RewriteCond %{REQUEST_URI} ^/webdav(/.*)?$ [NC]
    RewriteRule ^(.*)$ http://127.0.0.1:8080/$1 [P,L]

    # 5.7. CATCH-ALL REVERSE PROXY CHO TOÀN BỘ WEB UI & REST API GATEWAY
    RewriteCond %{REQUEST_FILENAME} !-f
    RewriteCond %{REQUEST_FILENAME} !-d
    RewriteRule ^(.*)$ http://127.0.0.1:8080/$1 [P,L]

    # Fallback cho trang chủ gốc (/)
    RewriteRule ^$ http://127.0.0.1:8080/ [P,L]
</IfModule>
```

> [!NOTE]
> **Nếu nhà cung cấp Hosting yêu cầu đổi cổng**:
> Nếu cổng `8080` bị người dùng khác trên cùng máy chủ sử dụng, bạn chỉ cần thay đổi số `8080` trong `.htaccess` (ví dụ thành `8085` hoặc `9050`) và chạy file binary với biến môi trường `PORT=8085`.

---

## 6. CÁC GIẢI PHÁP DUY TRÌ TIẾN TRÌNH NỀN (BACKGROUND DAEMON CHẠY BỀN BỈ)

Trên Shared Hosting, cơ chế quản lý tiến trình (CloudLinux LVE Killer) sẽ định kỳ dọn dẹp hoặc tắt các tiến trình không hoạt động hoặc khi phiên SSH ngắt kết nối. Để ứng dụng Go Engine hoạt động liên tục 24/7/365, chúng ta áp dụng các giải pháp dưới đây:

### Phương Án 1: cPanel Cron Job + Watchdog Script (Khuyên Dùng Nhất)

Đây là giải pháp kinh điển và ổn định nhất, hoạt động trên **100% các loại Shared Hosting**.

#### Bước 1: Tạo file kịch bản giám sát `watchdog.sh`
Tạo file `/home/username/app_supportflast/watchdog.sh` với nội dung sau (nhớ thay thế `username` bằng tài khoản cPanel của bạn):

```bash
#!/bin/bash
# ==============================================================================
# WATCHDOG SCRIPT CHO SUPPORTFLAST GO ENGINE TRÊN SHARED HOSTING
# ==============================================================================
APP_DIR="/home/username/app_supportflast"
APP_BIN="supportflast"
LOG_FILE="$APP_DIR/engine.log"
PORT=8080

# Chuyển vào thư mục ứng dụng
cd "$APP_DIR" || exit 1

# Kiểm tra xem tiến trình supportflast có đang hoạt động không
PID=$(pgrep -u "$(whoami)" -x "$APP_BIN")

if [ -z "$PID" ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Cảnh báo: $APP_BIN chưa chạy. Đang khởi động lại trên cổng $PORT..." >> "$LOG_FILE"
    
    # Thiết lập biến môi trường và chạy ứng dụng dưới nền bằng nohup
    export PORT="$PORT"
    export GIN_MODE="release"
    nohup "$APP_DIR/$APP_BIN" >> "$LOG_FILE" 2>&1 &
    
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Đã khởi động thành công với PID $!" >> "$LOG_FILE"
else
    # Ứng dụng vẫn đang chạy ổn định
    exit 0
fi
```

Cấp quyền thực thi cho file:
```bash
chmod 755 /home/username/app_supportflast/watchdog.sh
```

#### Bước 2: Thiết lập Cron Job trên cPanel
1. Đăng nhập cPanel ➔ Tìm mục **Advanced (Nâng cao)** ➔ Nhấn vào **Cron Jobs (Công việc định kỳ)**.
2. Tại mục **Add New Cron Job (Thêm công việc định kỳ mới)**:
   - **Common Settings (Cài đặt chung)**: Chọn **Once Per Minute (* * * * *)** hoặc mỗi 2-5 phút một lần (`*/2 * * * *`).
   - **Command (Dòng lệnh)**:
     ```bash
     /home/username/app_supportflast/watchdog.sh >/dev/null 2>&1
     ```
   - Nhấn **Add New Cron Job**.
3. Thêm một Cron Job khi máy chủ reboot (Tùy chọn):
   - **Command**:
     ```bash
     @reboot /home/username/app_supportflast/watchdog.sh >/dev/null 2>&1
     ```

> [!TIP]
> Nhờ kịch bản này, nếu máy chủ bảo trì khởi động lại hoặc tiến trình vô tình bị tắt, tối đa 60 giây sau kịch bản Watchdog sẽ tự động phát hiện và khởi động lại ứng dụng tức thì mà bạn không cần phải can thiệp thủ công.

---

### Phương Án 2: cPanel Terminal (SSH) với nohup / tmux

Nếu gói hosting của bạn cho phép mở **Terminal (Giao diện dòng lệnh)** trong cPanel:
1. Mở tính năng **Terminal** trên giao diện cPanel.
2. Di chuyển vào thư mục ứng dụng và kích hoạt lệnh chạy ngầm:
   ```bash
   cd /home/username/app_supportflast
   nohup ./supportflast > engine.log 2>&1 &
   ```
3. Kiểm tra tiến trình đang chạy:
   ```bash
   ps aux | grep supportflast
   curl -I http://127.0.0.1:8080/api/system/status
   ```

---

### Phương Án 3: Setup Node.js App / Phusion Passenger Wrapper

Một số nhà cung cấp Shared Hosting giá rẻ chặn lệnh chạy file binary trực tiếp qua cron job hoặc chặn `mod_proxy` trong `.htaccess`. Bạn có thể tận dụng tính năng **Setup Node.js App** (chạy trên Phusion Passenger) có sẵn trong cPanel để làm Reverse Proxy Wrapper và nuôi Go Binary!

1. Vào cPanel ➔ **Software (Phần mềm)** ➔ **Setup Node.js App**.
2. Nhấn **Create Application**:
   - **Node.js version**: Chọn bản mới nhất (vd: 18.x hoặc 20.x).
   - **Application mode**: Production.
   - **Application root**: `app_proxy`.
   - **Application URL**: Chọn domain của bạn.
   - Nhấn **Create**.
3. Mở file `app.js` được cPanel sinh ra trong thư mục `app_proxy/` và thay bằng mã sau:
   ```javascript
   const http = require('http');
   const { spawn } = require('child_process');
   const path = require('path');

   // 1. Tự động spawn tiến trình Go Engine
   const goBin = path.join('/home/username/app_supportflast', 'supportflast');
   const goProcess = spawn(goBin, [], {
       detached: true,
       stdio: 'ignore',
       env: { ...process.env, PORT: '8080' }
   });
   goProcess.unref();

   // 2. HTTP Proxy đơn giản chuyển tiếp traffic vào Go Engine
   const server = http.createServer((clientReq, clientRes) => {
       const options = {
           hostname: '127.0.0.1',
           port: 8080,
           path: clientReq.url,
           method: clientReq.method,
           headers: clientReq.headers
       };

       const proxy = http.request(options, (res) => {
           clientRes.writeHead(res.statusCode, res.headers);
           res.pipe(clientRes, { end: true });
       });

       clientReq.pipe(proxy, { end: true });
       proxy.on('error', (err) => {
           clientRes.writeHead(502, { 'Content-Type': 'text/plain' });
           clientRes.end('SupportFlast Backend Starting up... Please refresh.');
       });
   });

   server.listen(process.env.PORT || 3000);
   ```
4. Nhấn **Restart** trên giao diện Setup Node.js App. Phusion Passenger sẽ giữ cho tiến trình này luôn sống vĩnh viễn!

---

## 7. CẤU HÌNH CHỨNG CHỈ SSL/TLS MIỄN PHÍ VỚI CPANEL AUTOSSL

cPanel tích hợp sẵn hệ thống **AutoSSL** tự động kiểm tra và cấp chứng chỉ số SSL/TLS DV (Domain Validated) hoàn toàn miễn phí từ Let's Encrypt hoặc Sectigo Comodo.

### 7.1 Các bước kích hoạt AutoSSL
1. Đăng nhập cPanel ➔ Tìm mục **Security (Bảo mật)** ➔ Chọn **SSL/TLS Status**.
2. Tại bảng danh sách tên miền:
   - Đánh dấu chọn tên miền chính (`yourdomain.com`) và tên miền phụ `www.yourdomain.com`.
   - Nhấn nút **Run AutoSSL** ở phía trên.
3. Hệ thống sẽ gửi yêu cầu cấp phát chứng chỉ thông qua giao thức ACME HTTP-01 Challenge.
   - Nhờ có quy tắc ngoại lệ trong file `.htaccess`:
     ```apache
     RewriteCond %{REQUEST_URI} ^/\.well-known/acme-challenge/.*$ [NC]
     RewriteRule ^ - [L]
     ```
   - Máy chủ Let's Encrypt sẽ đọc file xác thực thành công 100% mà không bị chặn bởi bộ lọc hay bị đẩy vào Go Backend.
4. Sau 2 - 5 phút, biểu tượng ổ khóa cạnh tên miền sẽ chuyển sang **màu xanh lá cây (AutoSSL Domain Validated)**.

### 7.2 Ép buộc HTTPS trên cPanel
1. Vào cPanel ➔ **Domains (Miền)** ➔ Chọn mục **Domains**.
2. Tìm dòng tên miền của bạn, bật công tắc **Force HTTPS Redirect** sang trạng thái **ON**.
*(Hoặc giữ nguyên cấu hình Force HTTPS có sẵn trong file `.htaccess` của chúng ta).*

---

## 8. KIỂM TRA & TỐI ƯU KẾT NỐI WEBSOCKET (/ws) & WEBDAV (/webdav/)

### 8.1 Kiểm tra kết nối thời gian thực WebSocket (`/ws`)
File `.htaccess` đã được cấu hình nhận diện cờ `HTTP:Upgrade` và `HTTP:Connection upgrade` để chuyển tiếp gói tin sang giao thức `ws://127.0.0.1:8080/`.

**Cách kiểm tra trực tiếp trên trình duyệt (F12 Console):**
Mở trang web của bạn trên Google Chrome / Firefox, bấm `F12` ➔ chọn thẻ **Console** và dán đoạn mã sau:
```javascript
const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const socket = new WebSocket(`${wsProtocol}//${window.location.host}/ws`);

socket.onopen = () => console.log('✅ WebSocket kết nối thành công tới Go Engine!');
socket.onmessage = (event) => console.log('📩 Nhận dữ liệu từ Backend:', event.data);
socket.onerror = (err) => console.error('❌ Lỗi WebSocket:', err);
```
Nếu console xuất hiện dòng `✅ WebSocket kết nối thành công`, kết nối hai chiều thời gian thực đã hoạt động trơn tru.

---

### 8.2 Kiểm tra & Kết nối Đĩa Mạng WebDAV (`/webdav/`)
Hệ thống CloudPool trong SupportFlast Go Engine hỗ trợ giao thức WebDAV RFC-4918, cho phép người dùng gắn trực tiếp bộ nhớ đám mây vào máy tính như một ổ đĩa cứng (Network Drive).

#### Kết nối trên Windows Explorer:
1. Mở **This PC** ➔ Bấm vào dấu ba chấm `...` trên thanh công cụ (hoặc chuột phải vào vùng trống) ➔ Chọn **Add a network location (Thêm vị trí mạng)**.
2. Nhấn **Next** ➔ Nhập địa chỉ WebDAV:
   ```
   https://yourdomain.com/webdav/
   ```
3. Nhập Tên đăng nhập và Mật khẩu tài khoản SupportFlast của bạn.
4. Một ổ đĩa mạng mới sẽ xuất hiện, cho phép bạn copy, paste, kéo thả tệp tin trực tiếp từ máy tính lên máy chủ hosting.

#### Kết nối trên macOS Finder:
1. Mở **Finder** ➔ Nhấn tổ hợp phím `Cmd + K` (Connect to Server).
2. Nhập URL: `https://yourdomain.com/webdav/` ➔ Nhấn **Connect**.
3. Nhập tài khoản và mật khẩu để gắn ổ đĩa.

---

## 9. XỬ LÝ SỰ CỐ THƯỜNG GẶP (TROUBLESHOOTING & FAQS)

| Hiện tượng lỗi | Nguyên nhân gốc rễ | Cách khắc phục triệt để |
| :--- | :--- | :--- |
| **500 Internal Server Error** | Cú pháp `.htaccess` có module mà Apache trên máy chủ chưa kích hoạt hoặc bị chặn (vd: `mod_proxy`). | Mở file `.htaccess`, kiểm tra các thẻ `<IfModule>`. Đảm bảo các dòng liên quan đến `mod_proxy` nằm trong thẻ an toàn. Nếu hosting chặn `Options +FollowSymLinks`, đổi thành `Options +SymLinksIfOwnerMatch`. |
| **503 Service Unavailable** / **502 Bad Gateway** | File binary `supportflast` chưa được khởi chạy hoặc bị chết, hoặc đang lắng nghe trên cổng khác 8080. | Kiểm tra file log `/home/username/app_supportflast/engine.log`. Chạy script `watchdog.sh` bằng tay để kiểm tra xem binary có ném lỗi gì không. |
| **Lỗi: Permission Denied** | File binary chưa được cấp quyền thực thi `chmod +x` / `755`. | Chạy lệnh `chmod 755 /home/username/app_supportflast/supportflast` qua Terminal hoặc chỉnh quyền qua File Manager. |
| **Lỗi: Port Already in Use (Địa chỉ đã được dùng)** | Một người dùng khác trên cùng máy chủ Shared Hosting đã chiếm cổng `8080`. | Đổi cổng sang số khác (ví dụ: `8088` hoặc `9090`). Cập nhật biến môi trường trong `watchdog.sh` (`export PORT=8088`) và đổi `8080` thành `8088` trong file `.htaccess`. |
| **Lỗi: CloudLinux Memory Limit Exceeded** | Tiến trình vượt quá giới hạn RAM của gói Hosting (thường là 512MB hoặc 1GB). | Go Engine chạy thuần chỉ tiêu thụ 15-25MB RAM. Hãy kiểm tra xem có tiến trình nào bị rò rỉ không hoặc chạy lệnh `pkill -u $(whoami) supportflast` rồi để watchdog khởi động lại sạch sẽ. |
| **AutoSSL báo lỗi HTTP-01 Challenge Failed** | Rule rewrite của `.htaccess` đã chặn hoặc proxy nhầm thư mục xác thực của cPanel. | Đảm bảo 2 dòng sau luôn nằm ở ĐẦU khối `mod_rewrite.c`:<br>`RewriteCond %{REQUEST_URI} ^/\.well-known/acme-challenge/.*$ [NC]`<br>`RewriteRule ^ - [L]` |

---

## 10. CHECKLIST KIỂM TRA HOÀN TẤT TRƯỚC KHI BÀN GIAO

Trước khi công bố website vào sử dụng chính thức, bạn hãy kiểm tra từng mục trong danh sách sau:

- [ ] **Cross-compile**: Binary `supportflast` đã được build cho Linux 64-bit với `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`.
- [ ] **Upload & Cấu trúc**: Binary đặt trong `/home/username/app_supportflast/`, file `.htaccess` đặt trong `/home/username/public_html/`.
- [ ] **Phân quyền**: File binary đã `chmod 755`, thư mục SQLite `data/` đã `chmod 700`.
- [ ] **Daemon Watchdog**: File `watchdog.sh` đã được cấp quyền `755` và cấu hình chạy định kỳ trong cPanel Cron Jobs (`* * * * *`).
- [ ] **SSL / HTTPS**: cPanel AutoSSL đã cấp chứng chỉ thành công và hiển thị ổ khóa xanh an toàn.
- [ ] **Proxy Routing**: Truy cập tên miền hiển thị giao diện Web 3D bình thường, các API `/api/system/status` trả về mã `200 OK`.
- [ ] **WebSocket**: Thử nghiệm kết nối `/ws` thành công hai chiều.
- [ ] **WebDAV**: Thử nghiệm phương thức `PROPFIND` vào `/webdav/` xác thực thành công.
- [ ] **Bảo mật**: Thử truy cập trực tiếp `https://yourdomain.com/data/supportflast.db` hoặc `https://yourdomain.com/.env` và đảm bảo kết quả trả về là **403 Forbidden** (Bị từ chối truy cập).
