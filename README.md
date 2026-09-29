# SUPPORTFLASTDEV.IO.VN - CỔNG ĐĂNG TẢI ỨNG DỤNG

Hệ thống cổng thông tin và phân phối ứng dụng thế hệ mới dành riêng cho tên miền **supportflastdev.io.vn**, kết hợp giao diện Web 3D không gian tương tác cao (Three.js WebGL) và kiến trúc bảo mật đa lớp chuyên biệt.

---

## 🌟 TÍNH NĂNG NỔI BẬT

1. **Giao Diện Web 3D Không Gian Tương Tác**:
   - **Lõi Quantum Nexus Core** trung tâm tượng trưng cho cổng hỗ trợ `supportflastdev.io.vn`.
   - **5 Quả Cầu Năng Lượng Quỹ Đạo (Orbital Spheres)** đại diện cho 5 Subagents chuyên trách xoay quanh tâm.
   - **Tương tác chuột 3D trực quan**:
     - *Hover*: Quả cầu tự động phát sáng, hiển thị vầng hào quang và thông tin tóm tắt.
     - *Click*: Camera tự động di chuyển góc nhìn mượt mà (Lerp animation) tới Subagent, kích hoạt bảng điều khiển chuyên sâu.
     - *Orbit Drag & Zoom*: Người dùng tự do xoay 360 độ và phóng to/thu nhỏ không gian.
2. **5 Subagents Chuyên Trách Xử Lý Nhanh**:
   - **Subagent 1: Triage & Urgent Routing (`agent_triage`)**: Phân loại mức độ khẩn cấp (P1, P2, P3), đánh giá SLA tức thì (< 3 phút) và định tuyến thông minh.
   - **Subagent 2: Technical & Code Diagnostics (`agent_tech`)**: Chẩn đoán lỗi mã nguồn, stack trace, phân tích cú pháp và gỡ lỗi hệ thống.
   - **Subagent 3: Infrastructure, Network & DNS (`agent_infra`)**: Kiểm tra DNS thực tế, SSL/TLS, uptime và độ trễ hạ tầng cho `supportflastdev.io.vn`.
   - **Subagent 4: Security & Incident Response (`agent_security`)**: Rà soát nguy cơ lỗ hổng (SQLi, XSS, Path Traversal), kiểm tra bộ Security Headers và phản ứng sự cố an ninh.
   - **Subagent 5: Account & Billing Concierge (`agent_billing`)**: Hỗ trợ bản quyền, các gói đăng ký dịch vụ nhanh và tra cứu hợp đồng.
3. **Bảo Mật Cấp Doanh Nghiệp (Enterprise Security)**:
   - Go Engine tích hợp bộ lọc chống tấn công SQL Injection, XSS, Path Traversal và Log Injection (CRLF).
   - Rust Core cung cấp cơ chế mã hóa AES-256-GCM và Constant-Time Comparison chống tấn công Timing Side-Channel.
   - Toàn bộ HTTP Security Headers chuẩn OWASP: `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Strict-Transport-Security`, `Content-Security-Policy`.

---

## 🏛️ KIẾN TRÚC HỆ THỐNG

Toàn bộ hệ thống được phân rã thành 4 module độc lập, chuyên biệt:

```
f:\supportflast.dev\
├── supportflast_ui/           # [Frontend Web 3D] Three.js, Glassmorphism HUD, Responsive SPA
│   ├── index.html
│   ├── css/style.css
│   └── js/
│       ├── scene3d.js         # Three.js 3D Engine, Raycaster, Orbit Controls, Particles
│       └── app.js             # Dispatcher, UI State, REST/WebSocket Client
├── supportflast_engine/       # [Go Gateway] API Gateway, Input Sanitizer, Security Middleware
│   ├── main.go
│   └── security/
│       ├── sanitizer.go       # Bộ lọc an ninh, CORS exact match, Security Headers
│       └── sanitizer_test.go
├── supportflast_ai/           # [Python AI] Runtime của 5 Subagents chuyên sâu
│   ├── app.py                 # FastAPI service điều phối
│   ├── test_agents.py         # Bộ kiểm thử 6 tests cho 5 Subagents
│   ├── agents/                # Mã nguồn của từng Subagent
│   │   ├── triage_agent.py
│   │   ├── tech_agent.py
│   │   ├── infra_agent.py
│   │   ├── security_agent.py
│   │   └── billing_agent.py
│   └── core/
│       └── subagent_dispatcher.py
├── supportflast_core/         # [Rust Core] Mã hóa AES-256-GCM & Constant-Time Security
│   ├── Cargo.toml
│   └── src/lib.rs
├── Makefile                   # Tập hợp lệnh build, test, clean
├── run_supportflast.bat       # Script khởi động đồng bộ 1-click
└── README.md
```

---

## 🚀 HƯỚNG DẪN KHỞI ĐỘNG VÀ TRIỂN KHAI

### 1. Khởi động nhanh (1-Click)
Nhấp đúp chuột vào file `run_supportflast.bat` tại thư mục gốc của dự án hoặc mở PowerShell/CMD và chạy:
```powershell
.\run_supportflast.bat
```
Hệ thống sẽ tự động khởi động:
- Python Subagent AI Service trên cổng `8000`
- Go Gateway Engine & Web 3D trên cổng `8080`
- Tự động mở trình duyệt tại `http://localhost:8080`

### 2. Sử dụng Makefile
- **Chạy kiểm thử toàn bộ hệ thống (Rust + Go + Python)**:
  ```bash
  make test
  ```
- **Biên dịch toàn bộ hệ thống**:
  ```bash
  make build
  ```

---

## 🌐 HƯỚNG DẪN ĐÍNH TÊN MIỀN: `supportflastdev.io.vn`

Hệ thống đã được thiết lập sẵn sàng để liên kết trực tiếp với tên miền **`supportflastdev.io.vn`**. Bạn có thể lựa chọn 1 trong các phương án sau:

### Phương Án 1: Kiểm Tra Cục Bộ Ngay Trên Máy Tính (Local Hosts Test)
Chỉ cần chạy script:
- Bấm chuột phải vào file **`f:\supportflast.dev\setup_local_dns.bat`** -> Chọn **Run as administrator**.
- Mở trình duyệt và truy cập: **`http://supportflastdev.io.vn:8080`**

### Phương Án 2: Sử Dụng Cloudflare Tunnel (Khuyên Dùng Cho Máy Local / VPS Không Cần Mở Port)
1. Đăng ký/ủy quyền quản lý tên miền `supportflastdev.io.vn` trên Cloudflare Dashboard.
2. Tải công cụ `cloudflared` trên máy chủ:
   ```powershell
   winget install Cloudflare.cloudflared
   cloudflared tunnel login
   cloudflared tunnel create supportflastdev
   cloudflared tunnel route dns supportflastdev supportflastdev.io.vn
   ```
3. Khởi chạy bằng file cấu hình đã chuẩn bị sẵn:
   ```powershell
   cloudflared tunnel --config f:\supportflast.dev\cloudflared_config.yml run
   ```
   *Cloudflare sẽ tự động cấp chứng chỉ SSL HTTPS miễn phí và bảo vệ CDN.*

### Phương Án 3: Sử Dụng Caddy Web Server (Tự Động Cấp SSL Let's Encrypt / ZeroSSL)
Tệp **`f:\supportflast.dev\Caddyfile`** đã được cấu hình tối ưu. Khi trỏ DNS bản ghi A về IP máy chủ:
```powershell
caddy run --config f:\supportflast.dev\Caddyfile
```

### Phương Án 5: Triển Khai 1-Click Tự Động Lên Linux VPS (Khuyên Dùng)
Hệ thống cung cấp sẵn script tự động hóa **`deploy_vps.sh`** cho Ubuntu 22.04 / 24.04 LTS:
```bash
# Cấp quyền thực thi và triển khai tự động bằng Docker Compose
chmod +x deploy_vps.sh backup_sqlite.sh
sudo ./deploy_vps.sh --docker

# Hoặc triển khai Bare-Metal Binary với Systemd & Nginx (Tối ưu RAM 1GB)
sudo ./deploy_vps.sh --binary
```

> 📖 **Xem hướng dẫn chi tiết toàn diện tại**: [`HOSTING_DEPLOYMENT_GUIDE.md`](HOSTING_DEPLOYMENT_GUIDE.md)
> Bao gồm: Triển khai VPS Linux, Cloud Serverless (Render/Fly.io/Railway), Cloudflare Pages Frontend, WAF Security và Quy trình sao lưu SQLite WAL định kỳ.

---

## 🛡️ AN TOÀN & BẢO VỆ TÀI NGUYÊN (Zero Hanging Tasks & OOM Guard)
- Toàn bộ mã nguồn dự án được lưu trữ tuyệt đối trên phân vùng làm việc `f:\supportflast.dev\`, không ghi file vào ổ C hệ thống.
- Cấu chế ngắt kết nối an toàn (Graceful Shutdown) và kiểm soát bộ nhớ RAM (< 500MB cảnh báo tự động).
- Tuân thủ quy tắc quản lý tác vụ: Không để tiến trình ngầm treo vô hạn sau khi kiểm tra.
- Tự động sao lưu dữ liệu SQLite an toàn (Zero Downtime) qua script `backup_sqlite.sh` lập lịch lúc 02:00 sáng hàng ngày.
