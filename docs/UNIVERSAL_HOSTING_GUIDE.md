# 🌐 CẨM NANG HƯỚNG DẪN TRIỂN KHAI TOÀN NĂNG (UNIVERSAL HOSTING GUIDE)
## Dự án: Cổng Đăng Tải Ứng Dụng Hợp Nhất & Điều Phối 5 Subagents AI
### Tên miền chính thức: `supportflastdev.io.vn` | Kiến trúc: 100% UNIFIED SUITE

---

> [!IMPORTANT]
> **TUYÊN NGÔN HỢP NHẤT TOÀN DIỆN (100% UNIFIED & ZERO DEPENDENCY)**:
> Hệ thống **supportflast.dev** đã hoàn tất việc hợp nhất toàn bộ các phân hệ thành **MỘT BỘ DUY NHẤT**:
> - **Cổng phục vụ DUY NHẤT**: `8080` (Phục vụ Web 3D HUD Three.js, Cổng lưu trữ `/storage`, Bộ công cụ `/tools`, SQLite WAL DB, Honeypot Traps, SIEM AI và REST API Gateway).
> - **Tuyệt đối KHÔNG còn dịch vụ rời**: Không cần chạy `cloudpool_engine.exe`, không cần mở cổng phụ `8082`.
> - **Triển khai ở bất kỳ đâu**: Từ Shared Hosting cPanel giá rẻ, VPS Linux, Docker, Cloud PaaS cho đến Windows Server IIS doanh nghiệp.

---

## 📑 MỤC LỤC CHI TIẾT

1. [Tổng Quan Kiến Trúc & Gói Phân Phối Universal Release](#1-tổng-quan-kiến-trúc--gói-phân-phối-universal-release)
2. [Triển Khai Trên cPanel / DirectAdmin / Shared Hosting](#2-triển-khai-trên-cpanel--directadmin--shared-hosting)
   - [Phương án A: Chạy Binary Daemon & Reverse Proxy qua .htaccess (Khuyên dùng)](#phương-án-a-chạy-binary-daemon--reverse-proxy-qua-htaccess)
   - [Phương án B: Triển khai Decoupled Web 3D Frontend tại public_html](#phương-án-b-triển-khai-decoupled-web-3d-frontend-tại-public_html)
   - [Phương án C: Tích hợp qua cPanel Application Manager / Setup Python-Node App](#phương-án-c-tích-hợp-qua-cpanel-application-manager)
3. [Triển Khai Trên Linux VPS (Ubuntu / Debian / CentOS / Rocky Linux)](#3-triển-khai-trên-linux-vps-ubuntu--debian--centos--rocky-linux)
   - [Cách 1: Triển khai 1-Click bằng Docker Compose (Khuyên dùng)](#cách-1-triển-khai-1-click-bằng-docker-compose-khuyên-dùng)
   - [Cách 2: Triển khai Bare-Metal Native Systemd Service (Siêu nhẹ ~15MB RAM)](#cách-2-triển-khai-bare-metal-native-systemd-service-siêu-nhẹ-15mb-ram)
   - [Cấu hình Web Server Nginx & Caddy tự động cấp SSL Let's Encrypt](#cấu-hình-web-server-nginx--caddy-tự-động-cấp-ssl-lets-encrypt)
4. [Triển Khai Bằng Docker & Docker Compose Chuẩn Production](#4-triển-khai-bằng-docker--docker-compose-chuẩn-production)
5. [Triển Khai Lên Nền Tảng Cloud PaaS (Render, Railway, Fly.io, Heroku)](#5-triển-khai-lên-nền-tảng-cloud-paas-render-railway-flyio-heroku)
   - [5.1 Triển khai trên Render Cloud (với render.yaml & Disk)](#51-triển-khai-trên-render-cloud)
   - [5.2 Triển khai trên Fly.io (với fly.toml & Volumes)](#52-triển-khai-trên-flyio)
   - [5.3 Triển khai trên Railway.app (với railway.json)](#53-triển-khai-trên-railwayapp)
   - [5.4 Triển khai trên Heroku / Dokku (với Procfile)](#54-triển-khai-trên-heroku--dokku)
6. [Triển Khai Trên Windows Server & IIS (Internet Information Services)](#6-triển-khai-trên-windows-server--iis-internet-information-services)
   - [Phương án 1: Quản trị tự động bằng HttpPlatformHandler (Khuyên dùng)](#phương-án-1-quản-trị-tự-động-bằng-httpplatformhandler)
   - [Phương án 2: Reverse Proxy qua URL Rewrite & ARR](#phương-án-2-reverse-proxy-qua-url-rewrite--arr)
   - [Phương án 3: Chạy Windows Service ngầm vĩnh viễn với NSSM](#phương-án-3-chạy-windows-service-ngầm-vĩnh-viễn-với-nssm)
7. [Cấu Hình Tên Miền Chính Thức supportflastdev.io.vn & Cloudflare](#7-cấu-hình-tên-miền-chính-thức-supportflastdeviovn--cloudflare)
8. [Quy Trình Tự Động Sao Lưu & Khôi Phục Dữ Liệu SQLite WAL](#8-quy-trình-tự-động-sao-lưu--khôi-phục-dữ-liệu-sqlite-wal)
9. [Bảng Tra Cứu Lệnh Nhanh (Master Cheatsheet)](#9-bảng-tra-cứu-lệnh-nhanh-master-cheatsheet)

---

## 1. TỔNG QUAN KIẾN TRÚC & GÓI PHÂN PHỐI UNIVERSAL RELEASE

### 1.1 Sơ đồ dòng chảy dữ liệu hợp nhất (Single Port 8080)

```
[ Người Dùng Toàn Cầu ]
         │ HTTPS (Port 443)
         ▼
[ Cloudflare CDN & WAF ] ──── Chống DDoS, Rate Limit, WAF OWASP
         │
         ├──► Direct HTTPS / Reverse Proxy (Port 80/443)
         └──► Cloudflare Zero Trust Tunnel (Tàng hình IP)
                     │
                     ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   MÁY CHỦ HOSTING BẤT KỲ                               │
│  (cPanel / VPS Linux / Docker / Cloud PaaS / Windows Server IIS)       │
│                                                                        │
│   [ Cổng Phục Vụ Hợp Nhất DUY NHẤT: 8080 ]                             │
│   ┌────────────────────────────────────────────────────────────────┐   │
│   │           SupportFlast Unified Core Engine                     │   │
│   │                                                                │   │
│   │  ├── 🌐 Web 3D HUD Interface (Three.js WebGL)                 │   │
│   │  ├── 📁 Kho lưu trữ đám mây tích hợp: /storage                 │   │
│   │  ├── 🛠️ Cổng công cụ phát triển: /tools                       │   │
│   │  ├── 🛡️ Bộ lọc an ninh Input Sanitizer & RBAC Auth             │   │
│   │  ├── 🪤 11 Tuyến Decoy Honeypot & SIEM AI Collector            │   │
│   │  ├── 🗄️ CSDL SQLite WAL Chuẩn Doanh Nghiệp (supportflast.db)   │   │
│   │  └── ⚡ Bộ điều phối 5 Subagents AI (Fallback + Proxy 8000)   │   │
│   └────────────────────────────────────────────────────────────────┘   │
│                                │                                       │
│                                ▼ Port 8000 nội bộ                      │
│   [ Dịch Vụ Python AI 5 Subagents ] (Tự động fallback nếu không bật)   │
└────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Thành phần bên trong gói `supportflast-hosting-package.zip`

Gói phân phối được tạo tự động bởi `package_release.bat` (hoặc `python package_release.py`) bao gồm đầy đủ mọi tài nguyên:
- **`supportflast.exe`**: Binary thực thi chuẩn cho hệ điều hành Windows x64.
- **`supportflast_linux_amd64`**: Binary thực thi độc lập cho hệ điều hành Linux x64 (không phụ thuộc glibc/cgo).
- **`supportflast_core.dll`**: Thư viện lõi mã hóa và bảo mật.
- **`supportflast_ui/`**: Toàn bộ giao diện Web 3D HUD, CSS, JavaScript, Web Worker.
- **`supportflast_engine/`**: Toàn bộ mã nguồn Engine viết bằng Go hiệu năng cao.
- **`supportflast_ai/`**: Bộ máy điều phối 5 Subagents AI bằng Python FastAPI.
- **`install.sh` & `deploy_vps.sh`**: Bộ cài đặt 1-Click tự động cho Linux VPS.
- **`.htaccess`**: Cấu hình Apache / LiteSpeed / cPanel tối ưu bảo mật và rewrite.
- **`web.config`**: Cấu hình IIS cho Windows Server với HttpPlatformHandler / ARR.
- **`Dockerfile` & `docker-compose.yml`**: Bộ cấu hình chạy container chuẩn production.
- **`render.yaml`, `fly.toml`, `Procfile`, `railway.json`**: Cấu hình triển khai PaaS.
- **`backup_sqlite.sh`**: Kịch bản hot-backup CSDL SQLite an toàn trực tuyến.

---

## 2. TRIỂN KHAI TRÊN CPANEL / DIRECTADMIN / SHARED HOSTING

Shared Hosting là môi trường phổ biến và tiết kiệm chi phí nhất. Hệ thống hỗ trợ 3 phương án linh hoạt tuỳ thuộc vào gói hosting bạn sở hữu:

```
                  ┌─────────────────────────────────────────┐
                  │          SHARED HOSTING / CPANEL        │
                  │                                         │
    Browser ─────►│  Apache/LiteSpeed Web Server            │
                  │  ├── public_html/                       │
                  │  │     ├── .htaccess (Security + Proxy) │
                  │  │     ├── index.html (Web 3D UI)       │
                  │  │     └── tools.html                   │
                  │                                         │
                  │  [ Background Daemon hoặc Cronjob ]     │
                  │  └── ./supportflast_linux_amd64         │
                  └─────────────────────────────────────────┘
```

### Phương án A: Chạy Binary Daemon & Reverse Proxy qua .htaccess (Khuyên dùng)
*Áp dụng khi gói cPanel/DirectAdmin có hỗ trợ SSH Terminal hoặc Cron Job.*

#### Bước 1: Tải mã nguồn lên cPanel
1. Đăng nhập vào cPanel ➔ Mở **File Manager** (Trình quản lý tệp).
2. Tạo thư mục nằm ngoài `public_html` để bảo mật, ví dụ: `/home/username/supportflast_app`.
3. Tải tệp `supportflast-hosting-package.zip` lên và nhấn **Extract** (Giải nén).
4. Đảm bảo cấp quyền thực thi cho binary Linux:
   - Trong cPanel Terminal (hoặc SSH):
     ```bash
     chmod +x /home/username/supportflast_app/supportflast_linux_amd64
     ```

#### Bước 2: Thiết lập Daemon chạy ngầm liên tục qua Cron Job
Trên Shared Hosting không có quyền root, ta duy trì tiến trình bằng Cron Job tự khởi động nếu bị tắt:
1. Trong cPanel, tìm mục **Cron Jobs** (Tác vụ định kỳ).
2. Chọn tần suất chạy: **Mỗi 5 phút** (`*/5 * * * *`).
3. Nhập lệnh kiểm tra và tự hồi sinh:
   ```bash
   pgrep -f supportflast_linux_amd64 > /dev/null || (cd /home/username/supportflast_app && nohup ./supportflast_linux_amd64 > /dev/null 2>&1 &)
   ```
4. Nhấn **Add New Cron Job**. Tiến trình sẽ tự động bật và luôn chạy trên cổng `8080`.

#### Bước 3: Cấu hình Reverse Proxy qua `.htaccess`
Sao chép file `.htaccess` có sẵn trong gói vào thư mục `public_html/`. Đảm bảo có đoạn chuyển tiếp:
```apache
<IfModule mod_rewrite.c>
    RewriteEngine On
    RewriteBase /
    
    # Bắt buộc HTTPS
    RewriteCond %{HTTPS} off
    RewriteRule ^(.*)$ https://%{HTTP_HOST}%{REQUEST_URI} [L,R=301]
    
    # Reverse Proxy API & Storage sang engine cục bộ
    RewriteRule ^api/(.*)$ http://127.0.0.1:8080/api/$1 [P,L]
    RewriteRule ^storage/(.*)$ http://127.0.0.1:8080/storage/$1 [P,L]
    RewriteRule ^tools/(.*)$ http://127.0.0.1:8080/tools/$1 [P,L]
    RewriteRule ^ws/(.*)$ ws://127.0.0.1:8080/ws/$1 [P,L]
</IfModule>
```

---

### Phương án B: Triển khai Decoupled Web 3D Frontend tại public_html
*Áp dụng khi Shared Hosting bị giới hạn nghiêm ngặt không cho chạy binary.*

1. Trong File Manager cPanel, mở thư mục `public_html/`.
2. Sao chép toàn bộ tệp trong thư mục `supportflast_ui/` (gồm `index.html`, `tools.html`, thư mục `css/`, `js/`) và tệp `.htaccess` vào thẳng `public_html/`.
3. Mở file `js/app.js` (hoặc cấu hình API), trỏ địa chỉ API về VPS hoặc Serverless Backend của bạn (ví dụ: `https://api.supportflastdev.io.vn`).
4. Giao diện 3D Three.js và bộ công cụ chạy mượt mà 100% nhờ CDN và LiteSpeed Web Server nén Gzip.

---

### Phương án C: Tích hợp qua cPanel Application Manager
*Áp dụng khi cPanel có tính năng "Setup Python App" hoặc "Setup Node.js App".*
1. Truy cập **Setup Python App** trong cPanel.
2. Chọn Python 3.10 hoặc 3.11.
3. Đặt **Application Root**: `/home/username/supportflast_app`.
4. Đặt **Application Startup File**: `supportflast_ai/app.py`.
5. Nhấn **Run Pip Install** với `requirements.txt`.
6. Nhấn **Restart Application** để kích hoạt cụm AI Subagents.

---

## 3. TRIỂN KHAI TRÊN LINUX VPS (UBUNTU / DEBIAN / CENTOS / ROCKY LINUX)

Hệ điều hành tương thích: **Ubuntu 20.04/22.04/24.04 LTS**, **Debian 11/12**, **CentOS 8/9 Stream**, **Rocky Linux 8/9**, **AlmaLinux**, **Fedora**.

---

### Cách 1: Triển khai 1-Click bằng Docker Compose (Khuyên dùng)
> **Ưu điểm**: Độc lập hoàn toàn, không lo xung đột thư viện, tự động khởi động cùng VPS, tích hợp sẵn cron backup.

#### Bước 1: Tải gói phát hành hoặc clone vào VPS
```bash
# Đăng nhập SSH với quyền root
ssh root@YOUR_VPS_IP

# Tạo thư mục cài đặt
mkdir -p /opt/supportflast.dev
cd /opt/supportflast.dev

# Tải gói cài đặt (hoặc scp từ máy tính của bạn)
# scp supportflast-hosting-package.zip root@YOUR_VPS_IP:/opt/supportflast.dev/
unzip supportflast-hosting-package.zip
```

#### Bước 2: Chạy đúng 1 lệnh cài đặt duy nhất!
```bash
chmod +x install.sh deploy_vps.sh backup_sqlite.sh
sudo ./install.sh --docker
```
Script sẽ tự động:
1. Kiểm tra cấu hình phần cứng (RAM, CPU, Ổ cứng).
2. Tự động cài đặt Docker và Docker Compose plugin nếu máy chưa có.
3. Build các container ứng dụng (Core Engine & Python AI).
4. Khởi động hệ thống trên cổng `8080`.
5. Tạo cronjob hot-backup SQLite WAL hàng ngày lúc 02:00 sáng.
6. Xác thực Health Check tại `http://localhost:8080/api/health`.

---

### Cách 2: Triển khai Bare-Metal Native Systemd Service (Siêu nhẹ ~15MB RAM)
> **Ưu điểm**: Không cần cài Docker, sử dụng binary `supportflast_linux_amd64` biên dịch tĩnh thuần Go, tiêu thụ tài nguyên cực thấp, thích hợp cho VPS cấu hình yếu (512MB RAM).

Chạy lệnh cài đặt với cờ `--binary`:
```bash
sudo ./install.sh --binary
```

Script sẽ tự động tạo file dịch vụ `/etc/systemd/system/supportflast.service`:
```ini
[Unit]
Description=SupportFlast Unified Core Engine Service
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/opt/supportflast.dev
ExecStart=/opt/supportflast.dev/supportflast_linux_amd64
Restart=always
RestartSec=5s
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Kích hoạt và kiểm tra trạng thái dịch vụ:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now supportflast
sudo systemctl status supportflast
```

---

### Cấu hình Web Server Nginx & Caddy tự động cấp SSL Let's Encrypt

#### Lựa chọn A: Caddy Server (Khuyên dùng - Tự động 100% không cần Certbot)
File `Caddyfile` đã có sẵn trong gói:
```caddy
supportflastdev.io.vn {
    reverse_proxy 127.0.0.1:8080
    encode zstd gzip

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        X-XSS-Protection "1; mode=block"
    }
}
```
Chỉ cần chạy: `sudo caddy run --config Caddyfile` hoặc `sudo systemctl start caddy`. Caddy sẽ tự động đăng ký và gia hạn chứng chỉ SSL Let's Encrypt hoàn toàn miễn phí.

#### Lựa chọn B: Nginx Reverse Proxy
File cấu hình mẫu `nginx_supportflastdev.io.vn.conf` có sẵn:
```bash
sudo cp nginx_supportflastdev.io.vn.conf /etc/nginx/sites-available/supportflastdev.io.vn.conf
sudo ln -s /etc/nginx/sites-available/supportflastdev.io.vn.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

# Đăng ký chứng chỉ SSL tự động với Certbot
sudo certbot --nginx -d supportflastdev.io.vn --non-interactive --agree-tos -m admin@supportflastdev.io.vn
```

---

## 4. TRIỂN KHAI BẰNG DOCKER & DOCKER COMPOSE CHUẨN PRODUCTION

### 4.1 Cấu hình `docker-compose.yml`
```yaml
services:
  gateway:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: supportflast-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - GIN_MODE=release
      - AI_SERVICE_URL=http://ai:8000
    volumes:
      - ./data:/app/data
      - ./storage:/app/storage
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/api/health"]
      interval: 30s
      timeout: 5s
      retries: 3
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

  ai:
    build:
      context: ./supportflast_ai
      dockerfile: Dockerfile
    container_name: supportflast-ai
    restart: unless-stopped
    ports:
      - "127.0.0.1:8000:8000"
    environment:
      - PORT=8000
      - PYTHONUNBUFFERED=1
```

### 4.2 Thao tác quản trị nhanh:
```bash
# Khởi động nền
docker compose up -d

# Xem log thời gian thực
docker compose logs -f gateway

# Cập nhật phiên bản mới không gián đoạn
docker compose build
docker compose up -d --no-deps gateway
```

---

## 5. TRIỂN KHAI LÊN NỀN TẢNG CLOUD PAAS (RENDER, RAILWAY, FLY.IO, HEROKU)

### 5.1 Triển khai trên Render Cloud
1. Đẩy mã nguồn lên kho Git (GitHub / GitLab).
2. Đăng nhập vào [Render.com Dashboard](https://dashboard.render.com).
3. Chọn **New ➔ Blueprint** và liên kết với kho Git của bạn.
4. Render sẽ tự động đọc file **`render.yaml`** đã cấu hình sẵn:
   - Tự tạo Web Service chạy Docker.
   - Gắn ổ đĩa lưu trữ bền vững **Persistent Disk** mount tại `/app/data` (1GB) để lưu SQLite không lo mất dữ liệu khi restart.
5. Nhấn **Apply**. Dịch vụ sẽ online trong vòng 2 phút.

---

### 5.2 Triển khai trên Fly.io
Hệ thống đã có sẵn file `fly.toml`:
```bash
# Cài đặt Flyctl CLI nếu chưa có
curl -L https://fly.io/install.sh | sh

# Đăng nhập Fly.io
fly auth login

# Tạo ổ đĩa lưu trữ bền vững cho SQLite Database
fly volumes create supportflast_data --size 1 --region sin

# Triển khai ứng dụng
fly deploy
```

---

### 5.3 Triển khai trên Railway.app
File `railway.json` được thiết kế tương thích tuyệt đối:
1. Truy cập [Railway.app](https://railway.app) ➔ **New Project** ➔ **Deploy from GitHub repo**.
2. Railway tự động phát hiện `Dockerfile` và tệp `railway.json`.
3. Thiết lập biến môi trường `PORT=8080`.
4. Thêm **Volume** gắn vào đường dẫn `/app/data`.
5. Ứng dụng tự động deploy và cấp phát tên miền `*.up.railway.app` miễn phí có sẵn SSL.

---

### 5.4 Triển khai trên Heroku / Dokku
Sử dụng tệp **`Procfile`**:
```procfile
web: ./supportflast_linux_amd64
```
Chỉ cần thực hiện lệnh:
```bash
heroku create supportflast-app
git push heroku main
```

---

## 6. TRIỂN KHAI TRÊN WINDOWS SERVER & IIS (INTERNET INFORMATION SERVICES)

Thích hợp cho hạ tầng máy chủ nội bộ hoặc doanh nghiệp sử dụng Windows Server 2016, 2019, 2022.

```
                      ┌────────────────────────────────────────┐
                      │          WINDOWS SERVER 2022           │
                      │                                        │
    Người dùng ──────►│  IIS (Cổng 80/443 SSL)                 │
                      │  ├── web.config                        │
                      │  │     ├── Security Headers OWASP      │
                      │  │     └── HttpPlatformHandler         │
                      │  │              │                      │
                      │  ▼              ▼ Tự spawn & quản trị  │
                      │  [ supportflast.exe ] ◄── (Port ngẫu   │
                      │  └── CSDL: data\supportflast.db  nhiên)│
                      └────────────────────────────────────────┘
```

### Phương án 1: Quản trị tự động bằng HttpPlatformHandler (Khuyên dùng)
Module chính thức từ Microsoft cho phép IIS quản lý vòng đời ứng dụng Go/Node/Python:

#### Bước 1: Cài đặt module HttpPlatformHandler trên IIS
1. Tải và cài đặt module **HttpPlatformHandler v1.2** từ trang chủ Microsoft:
   [Microsoft Download HttpPlatformHandler](https://www.iis.net/downloads/microsoft/httpplatformhandler)
2. Khởi động lại IIS: `iisreset`.

#### Bước 2: Thiết lập Site trên IIS
1. Mở **IIS Manager** (`inetmgr.exe`).
2. Nhấp chuột phải vào **Sites** ➔ **Add Website...**
   - **Site name**: `supportflast.dev`
   - **Physical path**: `C:\inetpub\supportflast.dev` (hoặc thư mục chứa gói trên ổ D/F).
   - **Binding**: Chọn IP và Port (80 / 443).
3. Đảm bảo file **`web.config`** đã nằm ở thư mục gốc của trang web. IIS sẽ tự động đọc cấu hình:
   ```xml
   <httpPlatform processPath=".\supportflast.exe"
                 arguments=""
                 startupTimeLimit="60"
                 stdoutLogEnabled="true"
                 stdoutLogFile=".\logs\httpplatform-stdout.log">
     <environmentVariables>
       <environmentVariable name="PORT" value="%HTTP_PLATFORM_PORT%" />
     </environmentVariables>
   </httpPlatform>
   ```
4. Cấp quyền ghi (Modify) cho tài khoản `IIS_IUSRS` tại thư mục `data\` và `storage\`.

---

### Phương án 2: Reverse Proxy qua URL Rewrite & ARR
1. Cài đặt 2 Extension trên IIS: **Application Request Routing (ARR)** và **URL Rewrite**.
2. Trong IIS Manager, nhấn vào **Application Request Routing Cache** ➔ Chọn **Server Proxy Settings...** ➔ Tích chọn **Enable proxy**.
3. Chạy `supportflast.exe` ở cổng `8080`.
4. Trong `web.config`, bật rule Rewrite chuyển tiếp toàn bộ truy vấn về `http://127.0.0.1:8080/`.

---

### Phương án 3: Chạy Windows Service ngầm vĩnh viễn với NSSM
Nếu không muốn phụ thuộc vào IIS, bạn có thể biến `supportflast.exe` thành dịch vụ Windows Service tự khởi động cùng máy tính bằng công cụ **NSSM** (Non-Sucking Service Manager):

```powershell
# 1. Tải nssm.exe và cài đặt dịch vụ
nssm install SupportFlastService "F:\supportflast.dev\supportflast.exe"
nssm set SupportFlastService AppDirectory "F:\supportflast.dev"
nssm set SupportFlastService DisplayName "SupportFlast Unified Core Service"
nssm set SupportFlastService Description "Dịch vụ điều phối hợp nhất supportflast.dev trên cổng 8080"
nssm set SupportFlastService Start SERVICE_AUTO_START

# 2. Khởi động dịch vụ
nssm start SupportFlastService

# 3. Mở cổng 8080 trên Windows Firewall qua PowerShell
New-NetFirewallRule -DisplayName "SupportFlast Engine 8080" -Direction Inbound -LocalPort 8080 -Protocol TCP -Action Allow
```

---

## 7. CẤU HÌNH TÊN MIỀN CHÍNH THỨC SUPPORTFLASTDEV.IO.VN & CLOUDFLARE

Nhằm đạt mức an ninh và tốc độ tối ưu toàn cầu, hệ thống khuyến nghị cấu hình qua mạng lưới Cloudflare:

```
[ Người Dùng ] ──► [ Cloudflare Anycast CDN ] ──► [ Máy Chủ Gốc ]
                         │
                         ├─ SSL/TLS: Full (Strict)
                         ├─ WAF: OWASP Ruleset + Anti-DDoS
                         └─ Zero Trust Tunnel: cloudflared (Ẩn 100% IP máy chủ)
```

### 7.1 Cấu hình DNS Records
Truy cập Cloudflare Dashboard ➔ Chọn tên miền `supportflastdev.io.vn`:
| Loại (Type) | Tên (Name) | Giá trị (Target/IP) | Proxy Status |
| :--- | :--- | :--- | :--- |
| **A** | `@` (root) | `YOUR_SERVER_PUBLIC_IP` | 🟠 Proxied (Bật đám mây cam) |
| **CNAME** | `www` | `supportflastdev.io.vn` | 🟠 Proxied (Bật đám mây cam) |
| **CNAME** | `api` | `supportflastdev.io.vn` | 🟠 Proxied (Bật đám mây cam) |

### 7.2 Thiết lập SSL/TLS Full (Strict)
1. Trong mục **SSL/TLS** ➔ **Overview**: Chọn chế độ **Full (Strict)**.
2. Trong mục **Edge Certificates**:
   - Bật **Always Use HTTPS**: On.
   - Bật **Automatic HTTPS Rewrites**: On.
   - Bật **Minimum TLS Version**: TLS 1.2 hoặc 1.3.

### 7.3 Tàng hình IP Máy Chủ Bằng Cloudflare Zero Trust Tunnel
Để máy chủ không cần mở cổng 80/443 ra Internet, loại bỏ hoàn toàn nguy cơ bị dò quét IP gốc:
```bash
# 1. Cài đặt cloudflared trên Linux VPS
curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared.deb

# 2. Đăng nhập và tạo Tunnel
cloudflared tunnel login
cloudflared tunnel create supportflast-tunnel

# 3. Sử dụng file cấu hình cloudflared_config.yml có sẵn trong gói:
sudo cp cloudflared_config.yml /etc/cloudflared/config.yml

# 4. Kích hoạt dịch vụ chạy nền
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

---

## 8. QUY TRÌNH TỰ ĐỘNG SAO LƯU & KHÔI PHỤC DỮ LIỆU SQLITE WAL

### 8.1 Đặc thù SQLite WAL Mode
Hệ thống sử dụng SQLite cấu hình ở chế độ **WAL (Write-Ahead Logging)** để đạt hiệu năng xử lý hàng chục nghìn truy vấn đồng thời:
- Dữ liệu giao dịch được ghi liên tục vào 2 tệp phụ trợ: `supportflast.db-wal` và `supportflast.db-shm`.
- **CẢNH BÁO**: Tuyệt đối KHÔNG dùng lệnh `cp` copy tệp thô khi hệ thống đang chạy vì có thể làm hỏng trạng thái CSDL.
- **GIẢI PHÁP**: Sử dụng script `backup_sqlite.sh` đi kèm để thực hiện **Online Vacuum Backup** an toàn tuyệt đối.

### 8.2 Tự động sao lưu định kỳ với Cron Job
Script `backup_sqlite.sh` đã được cấu hình tự động khi chạy `install.sh`:
```bash
# Kiểm tra lịch sao lưu trong crontab
crontab -l

# Dòng tự động được thêm: Sao lưu mỗi ngày vào lúc 02:00 sáng
0 2 * * * /opt/supportflast.dev/backup_sqlite.sh >> /var/log/supportflast_backup.log 2>&1
```

Bạn cũng có thể chạy sao lưu thủ công bất kỳ lúc nào:
```bash
sudo ./backup_sqlite.sh
```
Bản sao lưu sẽ được nén dưới định dạng `supportflast_backup_YYYYMMDD_HHMMSS.db.gz` và tự động xoay vòng (chỉ giữ 7 ngày gần nhất để tiết kiệm đĩa).

### 8.3 Quy trình Khôi phục Thảm họa (RTO < 3 phút)
Khi cần khôi phục lại dữ liệu từ một bản sao lưu:
```bash
# 1. Tạm dừng dịch vụ
sudo systemctl stop supportflast || docker compose stop gateway

# 2. Giải nén bản sao lưu đè vào thư mục data
gunzip -c /opt/supportflast.dev/data/backups/supportflast_backup_20260924_020000.db.gz > /opt/supportflast.dev/data/supportflast.db

# 3. Dọn dẹp tệp nhật ký WAL cũ
rm -f /opt/supportflast.dev/data/supportflast.db-wal /opt/supportflast.dev/data/supportflast.db-shm

# 4. Khởi động lại dịch vụ
sudo systemctl start supportflast || docker compose start gateway
```

---

## 9. BẢNG TRA CỨU LỆNH NHANH (MASTER CHEATSHEET)

| Tác vụ | Câu lệnh Linux / Docker | Câu lệnh Windows |
| :--- | :--- | :--- |
| **Đóng gói phân phối** | `python3 package_release.py` | `.\package_release.bat` |
| **Cài đặt 1-Click Docker** | `sudo ./install.sh --docker` | `docker compose up -d` |
| **Cài đặt 1-Click Bare-Metal** | `sudo ./install.sh --binary` | `.\run_supportflast.bat` |
| **Kiểm tra Health Check** | `curl -i http://localhost:8080/api/health` | `curl.exe -i http://localhost:8080/api/health` |
| **Xem trạng thái dịch vụ** | `systemctl status supportflast` | `nssm status SupportFlastService` |
| **Xem logs thời gian thực**| `journalctl -u supportflast -f` | `Get-Content logs\*.log -Wait` |
| **Sao lưu SQLite tức thì** | `sudo ./backup_sqlite.sh` | Chạy copy SQLite qua CLI |
| **Khởi động lại toàn bộ** | `docker compose restart` | Khởi động lại service qua Service Manager |
| **Cài đặt XAMPP 1-Click** | N/A (Windows/Mac) | `.\setup_xampp.bat` |
| **Khởi động cùng XAMPP / Toàn hệ thống** | N/A (Windows/Mac) | `.\RUN_FULL_SYSTEM.bat` |

---

## 10. TRIỂN KHAI VỚI XAMPP (APACHE WINDOWS LOCAL DEV & SERVER)

SupportFlast tương thích 100% với môi trường **XAMPP (Apache + PHP + MySQL)**:

### 10.1 Cơ chế tích hợp
- XAMPP Apache (cổng `80` hoặc `443`) làm Reverse Proxy phía trước thông qua `mod_proxy` và `mod_proxy_http`.
- SupportFlast Go Monolith Engine chạy tại cổng `8080` xử lý toàn bộ Web UI, WebDAV RFC-4918, SQLite và API Gateway.
- Hệ thống hỗ trợ cả chế độ VirtualHost (`http://localhost` / `http://supportflast.local`) lẫn chế độ Sub-path (`http://localhost/supportflast/`).

### 10.2 Cách cài đặt 1-Click
1. Chạy file `setup_xampp.bat`: Tự động tìm thư mục XAMPP, kích hoạt module proxy và nạp file cấu hình `infra/httpd-supportflast.conf`.
2. Chạy file `RUN_FULL_SYSTEM.bat`: Tự động nhận diện Apache và khởi động SupportFlast Engine cùng lúc, tự mở trình duyệt tại `http://localhost`.

Chi tiết xem tại tài liệu chuyên sâu: [docs/XAMPP_HOSTING_GUIDE.md](file:///f:/supportflast.dev/docs/XAMPP_HOSTING_GUIDE.md).

---
**HỆ THỐNG SUPPORTFLAST.DEV - ĐÃ SẴN SÀNG TRIỂN KHAI TRÊN MỌI HẠ TẦNG TOÀN CẦU!**
