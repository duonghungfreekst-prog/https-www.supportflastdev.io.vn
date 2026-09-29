# HƯỚNG DẪN TRIỂN KHAI TOÀN DIỆN CHO TÊN MIỀN SUPPORTFLASTDEV.IO.VN
## Cổng Đăng Tải Ứng Dụng & Điều Phối 5 Subagents Chuyên Trách
### ⭐️ Khẳng định 100% đã hợp nhất thành một bộ duy nhất - Đồng bộ toàn diện trên cổng 8080

---

> [!IMPORTANT]
> **THÔNG CÁO HỢP NHẤT TOÀN DIỆN (100% UNIFIED SUITE)**:
> Hệ thống **supportflast.dev** đã hoàn tất việc hợp nhất toàn bộ các phân hệ thành **MỘT BỘ DUY NHẤT**:
> - **Cổng phục vụ DUY NHẤT**: `8080` (Lắng nghe cả Web 3D Three.js UI, Kho Lưu Trữ Đám Mây `/storage`, Bộ Công Cụ `/tools`, SQLite WAL DB, Honeypot Traps, SIEM AI, và REST API Gateway).
> - **Tuyệt đối KHÔNG còn phụ thuộc vào dịch vụ ngoài**: Không cần chạy `cloudpool_engine.exe`, không cần mở cổng `8082`.
> - **Triển khai cực nhanh chỉ với 1 câu lệnh**: Người dùng có thể triển khai nguyên bộ hệ thống lên bất kỳ VPS Linux nào (Ubuntu, Debian, Rocky...) chỉ với lệnh:
>   ```bash
>   docker compose up -d
>   ```
>   hoặc chạy script tự động: `sudo ./deploy_vps.sh --docker` / `sudo ./deploy_vps.sh --binary`.

---

## 📑 MỤC LỤC HƯỚNG DẪN

1. [Tổng Quan Kiến Trúc Hợp Nhất & Yêu Cầu Hạ Tầng](#1-tổng-quan-kiến-trúc-hợp-nhất--yêu-cầu-hạ-tầng)
2. [Triển Khai Lên Linux VPS (Ubuntu / Debian / Rocky Linux)](#2-triển-khai-lên-linux-vps-ubuntu--debian--rocky-linux)
   - [Phương án 1: Triển khai 1-Click bằng Docker Compose (Khuyên dùng - 1 lệnh duy nhất)](#phương-án-1-triển-khai-1-click-bằng-docker-compose-khuyên-dùng)
   - [Phương án 2: Triển khai Bare-Metal Native Binary (Siêu nhẹ ~15-20MB RAM)](#phương-án-2-triển-khai-bare-metal-native-binary-siêu-nhẹ-15-20mb-ram)
3. [Triển Khai Lên Cloud Serverless (Render, Fly.io, Railway)](#3-triển-khai-lên-cloud-serverless-render-flyio-railway)
   - [Triển khai trên Render Cloud (với render.yaml & Persistent Disk)](#31-triển-khai-trên-render-cloud)
   - [Triển khai trên Fly.io (với fly.toml & Fly Volume)](#32-triển-khai-trên-flyio)
4. [Tách Rời Kiến Trúc (Decoupled Frontend & Backend API)](#4-tách-rời-kiến-trúc-decoupled-frontend--backend-api)
   - [Frontend Web 3D trên Cloudflare Pages hoặc Vercel](#41-frontend-web-3d-trên-cloudflare-pages-hoặc-vercel)
   - [Backend Go Unified Engine trên Linux VPS](#42-backend-go-unified-engine-trên-linux-vps)
5. [Thiết Lập Tên Miền Chính Thức supportflastdev.io.vn Qua Cloudflare](#5-thiết-lập-tên-miền-chính-thức-supportflastdeviovn-qua-cloudflare)
   - [Cấu hình DNS Records (Proxied Orange Cloud)](#51-cấu-hình-dns-records)
   - [Mã hóa SSL/TLS Full (Strict)](#52-thiết-lập-chế-độ-mã-hóa-ssltls-full-strict)
   - [Cấu hình Bộ lọc Cloudflare WAF & Chống Tấn công DDoS](#53-cấu-hình-bộ-lọc-cloudflare-waf--chống-tấn-công-ddos)
   - [Tùy chọn Tàng Hình IP bằng Cloudflare Zero Trust Tunnel](#54-tùy-chọn-tàng-hình-ip-bằng-cloudflare-zero-trust-tunnel)
6. [Quy Trình Tự Động Sao Lưu & Khôi Phục Dữ Liệu SQLite WAL](#6-quy-trình-tự-động-sao-lưu--khôi-phục-dữ-liệu-sqlite-wal)
   - [Đặc thù SQLite WAL Mode & Nguyên tắc Hot-Backup an toàn](#61-đặc-thù-sqlite-wal-mode--nguyên-tắc-hot-backup-an-toàn)
   - [Tự động hóa sao lưu định kỳ với backup_sqlite.sh & Cron Job](#62-tự-động-hóa-sao-lưu-định-kỳ-với-backup_sqlitesh--cron-job)
   - [Quy trình Khôi phục Dữ liệu Khẩn cấp (RTO < 5 phút)](#63-quy-trình-khôi-phục-dữ-liệu-khẩn-cấp-rto--5-phút)
7. [Bảo Mật Cấp Hệ Điều Hành (OS Hardening) & Vận Hành Bền Bỉ](#7-bảo-mật-cấp-hệ-điều-hành-os-hardening--vận-hành-bền-bỉ)
8. [Bảng Tổng Hợp Lệnh Nhanh (Cheatsheet)](#8-bảng-tổng-hợp-lệnh-nhanh-cheatsheet)

---

## 1. TỔNG QUAN KIẾN TRÚC HỢP NHẤT & YÊU CẦU HẠ TẦNG

### 1.1 Sơ đồ phân tầng hệ thống hợp nhất
```
[ Người Dùng & Trình Duyệt Toàn Cầu ]
                 │ HTTPS (Port 443)
                 ▼
   [ Cloudflare Anycast CDN & WAF ] ──── (Chống DDoS, Rate Limit, WAF Rule)
                 │
                 ├── [ Cách A: Direct HTTPS tới Nginx / Caddy VPS ]
                 └── [ Cách B: Cloudflare Zero Trust Tunnel (cloudflared) ]
                             │
                             ▼
            ┌────────────────────────────────────────────────────────┐
            │                  LINUX VPS HẠ TẦNG                     │
            │                                                        │
            │  [ Nginx / Caddy Reverse Proxy ] (Cổng 80/443 SSL)     │
            │                │                                       │
            │                ▼ Forward tới Cổng 8080 DUY NHẤT        │
            │  [ SupportFlast Unified Core Engine ] ◄── (Port 8080)  │
            │     ├── Giao diện chính 3D HUD Web (supportflast_ui)   │
            │     ├── Kho Lưu Trữ Đám Mây Tích Hợp: /storage         │
            │     ├── Cổng Công Cụ Phát Triển: /tools                │
            │     ├── Bộ Lọc An Ninh Input Sanitizer & RBAC Auth     │
            │     ├── 11 Tuyến Decoy Honeypot & SIEM AI Collector    │
            │     ├── Cơ Sở Dữ Liệu SQLite WAL: data/supportflast.db │
            │     └── Fallback Engine Cache cho 5 Subagents AI       │
            │                │                                       │
            │                ▼ HTTP Proxy nội bộ (Port 8000)         │
            │  [ Python Multi-Agent AI Service ] ◄── (Port 8000)     │
            │     ├── Subagent 1: App Triage & Workflow              │
            │     ├── Subagent 2: Code & Build Diagnostics           │
            │     ├── Subagent 3: Global CDN & Infrastructure        │
            │     ├── Subagent 4: Security & Sandbox Scanner         │
            │     └── Subagent 5: Developer Billing & Licensing      │
            │                                                        │
            │  [ Cron Job Hot-Backup: backup_sqlite.sh ]             │
            │     └── Tự động snapshot SQLite lúc 02:00 sáng mỗi ngày│
            └────────────────────────────────────────────────────────┘
```

### 1.2 Yêu cầu cấu hình máy chủ VPS
| Thành phần | Cấu hình tối thiểu | Cấu hình khuyến nghị |
| :--- | :--- | :--- |
| **Hệ điều hành** | Ubuntu 20.04/22.04/24.04 LTS, Debian 11/12 | Ubuntu 24.04 LTS hoặc Debian 12 |
| **CPU** | 1 Core vCPU (x86_64 hoặc ARM64) | 2 Cores vCPU trở lên |
| **RAM** | 512 MB (Chế độ Native Binary) / 1.0 GB (Docker) | 2.0 GB - 4.0 GB RAM |
| **Ổ cứng (Disk)** | 10 GB SSD / NVMe | 25 GB - 40 GB NVMe |
| **Băng thông** | 100 Mbps Public IP | 1 Gbps Băng thông không giới hạn |
| **Cổng Firewall** | Cổng 22 (SSH), 80 (HTTP), 443 (HTTPS) *(Nếu dùng Cloudflare Tunnel: Chỉ mở SSH)* |

---

## 2. TRIỂN KHAI LÊN LINUX VPS (UBUNTU / DEBIAN / ROCKY LINUX)

Hệ thống đã chuẩn bị sẵn bộ công cụ tự động hóa hoàn chỉnh gồm file kịch bản **`deploy_vps.sh`**, tệp điều phối **`docker-compose.yml`**, **`Dockerfile`** chuẩn production và cấu hình Nginx mẫu.

---

### Phương án 1: Triển khai 1-Click bằng Docker Compose (Khuyên dùng)
> **Ưu điểm**: Đóng gói sẵn 100% môi trường Go, Alpine Linux siêu nhẹ (~30MB), Python 3.11, Caddy SSL tự động.
> **Hỗ trợ Multi-Arch Toàn Diện**: Dockerfile tối ưu hóa đa kiến trúc native cho cả **x86_64 (amd64)** và **ARM64 (aarch64)** (tương thích 100% với Oracle Cloud Free ARM Ampere A1, AWS Graviton, Apple Silicon). Sử dụng `modernc.org/sqlite` (pure Go `CGO_ENABLED=0`) cho binary tĩnh hoàn toàn và shell script wrapper tự động thích ứng biến `$PORT` động.

#### Bước 1: Kết nối SSH vào VPS và đồng bộ mã nguồn
```bash
# Đăng nhập vào VPS với quyền root
ssh root@YOUR_VPS_IP

# Tạo thư mục ứng dụng chuẩn trên VPS
mkdir -p /opt/supportflast.dev
cd /opt/supportflast.dev

# Tải hoặc đồng bộ toàn bộ mã nguồn dự án vào /opt/supportflast.dev
# (Ví dụ: clone git hoặc copy rsync)
git clone https://github.com/YOUR_ORGANIZATION/supportflast.dev.git .
```

#### Bước 2: Triển khai chỉ với 1 lệnh duy nhất!
Chỉ cần chạy lệnh sau tại thư mục `/opt/supportflast.dev`:
```bash
docker compose up -d
```
Hoặc chạy script triển khai tự động với cờ `--docker`:
```bash
chmod +x deploy_vps.sh backup_sqlite.sh entrypoint.sh
sudo ./deploy_vps.sh --docker
```

> **Mẹo biên dịch Multi-Arch với Docker Buildx**:
> ```bash
> # Build & Push image hỗ trợ cả amd64 và arm64
> docker buildx build --platform linux/amd64,linux/arm64 -t supportflast:latest .
> ```


#### Quá trình tự động thực hiện ngầm:
1. Docker tự động build 2 containers:
   - `supportflast-gateway`: Core Engine viết bằng Go, phục vụ toàn bộ Web UI, Storage `/storage`, Tools `/tools`, SQLite WAL trên cổng `8080`.
   - `supportflast-ai`: Dịch vụ 5 Subagents AI trên cổng `8000`.
2. Dữ liệu SQLite trong thư mục `./data` và các tệp cài đặt `./storage` được mount liên tục (Persistent Volume), đảm bảo không bao giờ mất dữ liệu khi reboot hoặc upgrade container.
3. Thiết lập cron job tự động sao lưu dữ liệu lúc 02:00 sáng.
4. Kiểm tra sức khỏe hệ thống (Health Check) tại `http://localhost:8080/api/health`.

#### Các lệnh quản trị Docker Compose nhanh:
```bash
# Kiểm tra trạng thái các container
docker compose ps

# Xem nhật ký hoạt động (Logs) theo thời gian thực
docker compose logs -f

# Khởi động lại dịch vụ
docker compose restart

# Nâng cấp phiên bản mới sau khi cập nhật mã nguồn
docker compose up -d --build
```

---

### Phương án 2: Triển khai Bare-Metal Native Binary (Siêu nhẹ ~15-20MB RAM)
> **Ưu điểm**: Tối ưu hóa hiệu năng tối đa, không cần cài Docker, phù hợp hoàn hảo với các VPS cấu hình thấp (RAM 512MB - 1GB).

#### Bước 1: Chạy lệnh triển khai tự động
```bash
cd /opt/supportflast.dev
chmod +x deploy_vps.sh backup_sqlite.sh
sudo ./deploy_vps.sh --binary
```

#### Quá trình script tự động thực hiện:
1. Tự động kiểm tra và cài đặt Go Compiler, Python3, python3-venv, Nginx và Sqlite3.
2. Biên dịch Go Engine ra tệp binary tĩnh Linux siêu nhẹ `supportflast` (không phụ thuộc CGO, tích hợp SQLite thuần Go):
   ```bash
   cd supportflast_engine
   CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" -trimpath -o supportflast .
   ```
3. Tạo môi trường ảo Python `venv` và cài đặt `requirements.txt` cho 5 Subagents AI.
4. Đăng ký và kích hoạt 2 Systemd Services chạy nền tự động bật cùng hệ thống:
   - `supportflast-engine.service` (Quản lý binary `supportflast` cổng 8080).
   - `supportflast-ai.service` (Quản lý Python FastAPI cổng 8000).
5. Thiết lập Nginx Reverse Proxy và lịch trình sao lưu SQLite định kỳ.

#### Quản lý dịch vụ qua Systemd:
```bash
# Kiểm tra trạng thái dịch vụ Core Engine
systemctl status supportflast-engine

# Xem logs trực tiếp của Core Engine
journalctl -u supportflast-engine -f

# Khởi động lại Core Engine
systemctl restart supportflast-engine
```

---

## 3. TRIỂN KHAI LÊN CLOUD SERVERLESS (RENDER, FLY.IO, RAILWAY, KOYEB, HEROKU)

> [!CAUTION]
> **Nguyên tắc sống còn đối với SQLite trên Serverless / PaaS**: Container Serverless là tạm thời (Stateless). Bắt buộc phải gắn **Persistent Disk / Volume** vào `/app/data` (và lưu kho packages tại `/app/data/storage`) để không bị mất dữ liệu cơ sở dữ liệu SQLite sau mỗi lần restart hoặc redeploy.

---

### 3.1 Triển khai trên Render Cloud
Dự án đã tích hợp sẵn tệp cấu hình **`render.yaml`** (Render Blueprint IaC):
1. Đăng nhập [Render Dashboard](https://dashboard.render.com/) -> Chọn **Blueprints** -> **New Blueprint Instance**.
2. Chọn repository chứa dự án `supportflast.dev`.
3. Render sẽ tự động nạp cấu hình `render.yaml`, nhận diện cổng `$PORT` động, khởi tạo dịch vụ `supportflast-engine` kèm **10GB Persistent Disk** gắn tại `/app/data`.
4. Trong phần **Custom Domains**, thêm tên miền `supportflastdev.io.vn` và cấu hình CNAME theo hướng dẫn.

---

### 3.2 Triển khai trên Fly.io
Dự án đã chuẩn bị sẵn file **`fly.toml`** tối ưu cho khu vực Đông Nam Á (Region Singapore: `sin`):
```bash
# Cài đặt Fly CLI và đăng nhập
curl -L https://fly.io/install.sh | sh
fly auth login

# Tạo ổ đĩa lưu trữ Persistent Volume 10GB gắn tại /app/data
fly volumes create supportflast_data --size 10 --region sin

# Triển khai ứng dụng (tự động nhận diện internal_port=8080 và health check)
fly deploy
```

---

### 3.3 Triển khai trên Railway
Dự án cung cấp tệp cấu hình chuẩn **`railway.json`**:
1. Đăng nhập [Railway](https://railway.com/) -> **New Project** -> **Deploy from GitHub repo**.
2. Railway tự động phát hiện `railway.json`, sử dụng Dockerfile builder, thiết lập `restartPolicyType: ON_FAILURE` với max 10 retries, và HTTP healthcheck `/api/health`.
3. Thêm Volume với Mount Path là `/app/data` trên dashboard để bảo lưu dữ liệu SQLite và Packages.
4. Cổng dịch vụ `$PORT` được Railway tự động phân bổ và engine tự động lắng nghe.

---

### 3.4 Triển khai trên Koyeb Cloud
Dự án cung cấp tệp cấu hình **`koyeb.yaml`**:
1. Cài đặt [Koyeb CLI](https://www.koyeb.com/docs/build-and-deploy/cli) hoặc liên kết qua Koyeb Web Console.
2. Khởi tạo dịch vụ với Persistent Volume:
   ```bash
   koyeb service create supportflast-engine --app supportflast --git github.com/YOUR_USER/supportflast.dev --git-branch main --docker-dockerfile Dockerfile --port 8080:http --route /:8080 --checks 8080:http:/api/health --volume supportflast-data:/app/data --region sin
   ```
3. Hoặc tải trực tiếp cấu hình qua `koyeb.yaml` để tự động hóa 1-click toàn bộ cấu hình.

---

### 3.5 Triển khai trên Heroku / Dokku / Scalingo
Dự án cung cấp tệp **`Procfile`**:
```procfile
web: ./supportflast
```
- **Heroku**: Sử dụng Heroku Container Registry (`heroku container:push web && heroku container:release web`). Biến `$PORT` được tự động nạp.
- **Dokku**: Đẩy repo lên remote dokku (`git push dokku main`). Gắn storage persistent:
  ```bash
  dokku storage:ensure-directory supportflast
  dokku storage:mount supportflast /var/lib/dokku/data/storage/supportflast:/app/data
  ```
- **Scalingo**: Tự động nhận diện `Procfile` và build container, định tuyến traffic tới cổng dịch vụ.

---

## 4. TÁCH RỜI KIẾN TRÚC (DECOUPLED FRONTEND & BACKEND API)

### 4.1 Frontend Web 3D trên Cloudflare Pages hoặc Vercel
- **Cloudflare Pages**:
  - Build output directory: `supportflast_ui`
  - Đính kèm tên miền `supportflastdev.io.vn`.
  - Định tuyến các request `/api/*` về máy chủ VPS thông qua Cloudflare Pages Function hoặc Worker.
- **Vercel**:
  - Triển khai thư mục `supportflast_ui` với tệp `vercel.json` rewrite `/api/:path*` về Backend VPS.

### 4.2 Backend Go Unified Engine trên Linux VPS
Khi Frontend chạy trên CDN hoặc sub-domain, Backend trên VPS tự động áp dụng CORS Whitelist được kiểm soát nghiêm ngặt qua biến môi trường `ALLOWED_ORIGINS`:
```bash
ALLOWED_ORIGINS="https://supportflastdev.io.vn,https://www.supportflastdev.io.vn"
```

---

## 5. THIẾT LẬP TÊN MIỀN CHÍNH THỨC SUPPORTFLASTDEV.IO.VN QUA CLOUDFLARE

Cloudflare đóng vai trò là "Lá chắn Anycast WAF vòng ngoài" bảo vệ máy chủ của bạn trước mọi cuộc tấn công DDoS và quét lỗ hổng.

---

### 5.1 Cấu hình DNS Records
Đăng nhập Cloudflare Dashboard -> Tên miền **`supportflastdev.io.vn`** -> **DNS** -> **Records**:

| Loại (Type) | Tên (Name) | Giá Trị (Target / IP) | Trạng Thái Proxy (Proxy Status) | TTL |
| :--- | :--- | :--- | :--- | :--- |
| **A** | `@` | `YOUR_VPS_PUBLIC_IP` | ☁️ **Proxied (Bật đám mây cam)** | Auto |
| **CNAME** | `www` | `supportflastdev.io.vn` | ☁️ **Proxied (Bật đám mây cam)** | Auto |
| **A** *(Nếu tách subdomain)* | `api` | `YOUR_VPS_PUBLIC_IP` | ☁️ **Proxied (Bật đám mây cam)** | Auto |

> [!WARNING]
> Luôn giữ trạng thái **Proxied (Đám mây màu cam)** để giấu IP thật của máy chủ VPS, chống tấn công DDoS trực diện.

---

### 5.2 Thiết lập Chế độ Mã hóa SSL/TLS Full (Strict)
1. Trong Cloudflare Dashboard, vào mục **SSL/TLS** -> **Overview**.
2. Chọn chế độ: **Full (Strict)** (Đảm bảo mã hóa an toàn từ Client -> Cloudflare Edge -> VPS Nginx/Caddy).
3. Bật **Always Use HTTPS** và cấu hình **HSTS** (Max-Age: 31536000, Include Subdomains, Preload).
4. Vào mục **Origin Server** -> Tạo chứng chỉ **Cloudflare Origin CA Certificate** (thời hạn 15 năm) và cài đặt vào Nginx VPS:
   - `/etc/ssl/certs/supportflastdev.io.vn.pem`
   - `/etc/ssl/private/supportflastdev.io.vn.key`

---

### 5.3 Cấu hình Bộ lọc Cloudflare WAF & Chống Tấn công DDoS
Dự án đã chuẩn bị sẵn bộ quy tắc WAF tại tệp **`cloudflare_waf_rules.json`**:
1. **Bot Fight Mode**: Bật tính năng chặn bot tự động.
2. **Rate Limiting Rule**:
   - URL: `supportflastdev.io.vn/api/*`
   - Giới hạn: Quá 120 yêu cầu / 1 phút từ cùng 1 IP -> Kích hoạt **Managed Challenge** hoặc **Block** trong 10 phút.
3. **Security Level**: Đặt mức **Medium** (hoặc **Under Attack** khi bị flood lưu lượng lớn).

---

### 5.4 Tùy chọn Tàng Hình IP bằng Cloudflare Zero Trust Tunnel
Nếu bạn không muốn mở cổng 80/443 trên Firewall VPS:
1. Cài đặt `cloudflared` trên Linux VPS:
   ```bash
   curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
   dpkg -i cloudflared.deb
   ```
2. Khởi chạy đường hầm với tệp cấu hình có sẵn **`cloudflared_config.yml`**:
   ```bash
   cloudflared tunnel --config /opt/supportflast.dev/cloudflared_config.yml run
   ```
*Khi đó, VPS hoàn toàn đóng mọi cổng Inbound web, chỉ kết nối an toàn qua Outbound tunnel tới Cloudflare Edge.*

---

## 6. QUY TRÌNH TỰ ĐỘNG SAO LƯU & KHÔI PHỤC DỮ LIỆU SQLITE WAL

---

### 6.1 Đặc thù SQLite WAL Mode & Nguyên tắc Hot-Backup an toàn
Hệ thống SupportFlast kích hoạt **SQLite Write-Ahead Logging (WAL)** để đạt hiệu năng ghi đọc đồng thời cao:
- `supportflast.db`: CSDL chính.
- `supportflast.db-wal`: Nhật ký giao dịch thời gian thực.
- `supportflast.db-shm`: Bộ nhớ chia sẻ Shared Memory index.

> [!CAUTION]
> **TUYỆT ĐỐI KHÔNG COPY TRỰC TIẾP FILE .DB BẰNG LỆNH `cp`** khi ứng dụng đang chạy!
> Phải sử dụng công cụ snapshot trực tuyến của SQLite:
> ```bash
> sqlite3 /opt/supportflast.dev/data/supportflast.db "VACUUM INTO '/tmp/snapshot.db';"
> ```
> Cơ chế này đảm bảo sao lưu trực tuyến mà **không làm gián đoạn hay khóa database (Zero Downtime)**.

---

### 6.2 Tự động hóa sao lưu định kỳ với backup_sqlite.sh & Cron Job
Kịch bản **`backup_sqlite.sh`** có sẵn trong dự án tự động:
1. Kiểm tra toàn vẹn CSDL trước khi sao lưu: `PRAGMA integrity_check;`.
2. Tạo snapshot sạch và thu gom rác bằng `VACUUM INTO`.
3. Kiểm tra lại bản snapshot: `PRAGMA quick_check;`.
4. Đóng gói metadata và nén `tar.gz` lưu vào `backups/sqlite/`.
5. Tự động xóa các bản sao lưu cũ hơn **14 ngày**.

#### Kích hoạt Cron Job hàng ngày lúc 02:00 sáng:
File `/etc/cron.d/supportflast_backup`:
```cron
0 2 * * * root /bin/bash /opt/supportflast.dev/backup_sqlite.sh >> /var/log/supportflast_backup.log 2>&1
```

---

### 6.3 Quy trình Khôi phục Dữ liệu Khẩn cấp (RTO < 5 phút)
Khi cần khôi phục lại dữ liệu từ bản sao lưu:
```bash
# Bước 1: Dừng tạm thời dịch vụ
systemctl stop supportflast-engine
# (Nếu dùng Docker: docker compose stop supportflast-gateway)

# Bước 2: Giải nén bản sao lưu mới nhất
mkdir -p /tmp/sf_restore
cd /opt/supportflast.dev/backups/sqlite
LATEST_BACKUP=$(ls -t supportflast_db_backup_*.tar.gz | head -n 1)
tar -xzf "$LATEST_BACKUP" -C /tmp/sf_restore

# Bước 3: Kiểm tra tính toàn vẹn của tệp khôi phục
sqlite3 /tmp/sf_restore/supportflast.db "PRAGMA integrity_check;"

# Bước 4: Thay thế file DB hiện tại (xóa sạch cả file wal và shm cũ)
rm -f /opt/supportflast.dev/data/supportflast.db-wal
rm -f /opt/supportflast.dev/data/supportflast.db-shm
cp /tmp/sf_restore/supportflast.db /opt/supportflast.dev/data/supportflast.db
chmod 644 /opt/supportflast.dev/data/supportflast.db

# Bước 5: Khởi động lại dịch vụ
systemctl start supportflast-engine
# (Nếu dùng Docker: docker compose start supportflast-gateway)

# Bước 6: Kiểm tra API sức khỏe
curl -s http://127.0.0.1:8080/api/health
```

---

## 7. BẢO MẬT CẤP HỆ ĐIỀU HÀNH (OS HARDENING) & VẬN HÀNH BỀN BỈ

1. **Cấu hình Firewall UFW**:
   ```bash
   ufw allow 22/tcp
   ufw allow 80/tcp
   ufw allow 443/tcp
   ufw --force enable
   ```
2. **Cài đặt Fail2ban chống Brute-Force SSH**:
   ```bash
   apt-get install -y fail2ban
   systemctl enable --now fail2ban
   ```
3. **Giám sát Tài nguyên RAM (Rule Phần 7.4)**:
   ```bash
   # Kiểm tra API sức khỏe hệ thống
   curl -s http://127.0.0.1:8080/api/health | jq .
   ```

---

## 8. BẢNG TỔNG HỢP LỆNH NHANH (CHEATSHEET)

| Nhu Cầu Vận Hành | Phương Án Docker Compose | Phương Án Native Binary |
| :--- | :--- | :--- |
| **Triển khai 1 lệnh** | `docker compose up -d` | `sudo ./deploy_vps.sh --binary` |
| **Dừng hệ thống** | `docker compose down` | `systemctl stop supportflast-engine supportflast-ai` |
| **Khởi động lại** | `docker compose restart` | `systemctl restart supportflast-engine supportflast-ai` |
| **Xem Log trực tiếp** | `docker compose logs -f` | `journalctl -u supportflast-engine -f` |
| **Kiểm tra Sức khỏe** | `curl http://127.0.0.1:8080/api/health` | `curl http://127.0.0.1:8080/api/health` |
| **Sao lưu tức thì** | `./backup_sqlite.sh` | `./backup_sqlite.sh` |
| **Địa chỉ Web UI** | `http://IP_VPS:8080` | `http://IP_VPS:8080` |
| **Kho Lưu Trữ Đám Mây** | `http://IP_VPS:8080/storage` | `http://IP_VPS:8080/storage` |
| **Cổng Công Cụ** | `http://IP_VPS:8080/tools` | `http://IP_VPS:8080/tools` |

---

*Hệ thống **SupportFlast** 100% hợp nhất thành một bộ duy nhất, sẵn sàng vận hành ổn định trên mọi hạ tầng VPS Linux!*
