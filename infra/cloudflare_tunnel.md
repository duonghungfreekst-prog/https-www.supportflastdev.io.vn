# Hướng Dẫn Cấu Hình Cloudflare Tunnel (cloudflared)
## Cho Hệ Thống: `supportflastdev.io.vn`

---

## 1. Giới Thiệu & Lợi Ích Của Cloudflare Tunnel

**Cloudflare Tunnel** (trước đây là Argo Tunnel) tạo một đường hầm mã hóa hai chiều an toàn (outbound-only encrypted tunnel) giữa máy chủ VPS của bạn và mạng lưới Anycast toàn cầu của Cloudflare.

### Lợi ích cốt lõi:
- **Không cần mở port Modem / Firewall**: Bạn không cần NAT port 80 hay 443 trên router/firewall của VPS.
- **Không cần IP Tĩnh (Static IP)**: Máy chủ có thể nằm sau CGNAT, IP động của nhà mạng hoặc mạng nội bộ riêng biệt.
- **Ẩn hoàn toàn IP gốc (Origin IP Shielding)**: Kẻ tấn công không thể tìm ra địa chỉ IP thực của VPS qua các công cụ quét như Shodan hay Censys.
- **Chống DDoS Layer 3/4/7 tự động**: Mọi lưu lượng truy cập đều đi qua hệ thống lọc DDoS và Cloudflare WAF trước khi chạm tới VPS.
- **Tự động cấp phát SSL/TLS**: Cloudflare tự xử lý chứng chỉ HTTPS tại rìa mạng (Edge Certificate).

---

## 2. Kiến Trúc Luồng Dữ Liệu (Architecture Flow)

```
[ Khách truy cập / Client ]
            │
            ▼ (HTTPS / WSS - Port 443)
[ Cloudflare Anycast Edge Network ]
   ├─ Tự động chặn DDoS Layer 3/4/7
   ├─ Cloudflare WAF & Bot Fight Mode
   └─ SSL/TLS Termination
            │
            ▼ (Encrypted Quic / HTTP2 Tunnel - Outbound Only)
[ VPS Linux: cloudflared Daemon ]
            │
            ▼ (Local Proxy - 127.0.0.1)
[ Nginx Reverse Proxy (:80 / :443) ]
            │
            ▼ (Upstream: 127.0.0.1:8080)
[ Go Gateway Engine (supportflast) ]
            │
            ▼
[ Python AI Subagents & Local Data ]
```

---

## 3. Điều Kiện Tiên Quyết (Prerequisites)

1. Tên miền `supportflastdev.io.vn` đã được thêm vào tài khoản Cloudflare và chuyển Nameservers thành công.
2. Máy chủ VPS Linux (Ubuntu 20.04/22.04/24.04, Debian 11/12, CentOS/RHEL 8/9).
3. Đã cài đặt và chạy dịch vụ Nginx cùng Go Engine (`supportflast.service`) trên VPS.

---

## 4. Hướng Dẫn Cài Đặt Chi Tiết Từng Bước

### Bước 1: Cài đặt `cloudflared` trên Linux VPS

#### Dành cho Ubuntu / Debian:
```bash
# 1. Tải GPG key của Cloudflare
sudo mkdir -p --mode=0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null

# 2. Thêm repository vào apt sources
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/cloudflared.list

# 3. Cập nhật và cài đặt cloudflared
sudo apt-get update && sudo apt-get install -y cloudflared
```

#### Dành cho CentOS / RHEL / Rocky Linux:
```bash
# Tải và cài đặt trực tiếp qua RPM
sudo dnf install -y https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-x86_64.rpm
```

Kiểm tra cài đặt thành công:
```bash
cloudflared --version
```

---

### Bước 2: Xác thực tài khoản Cloudflare (Login)

Chạy lệnh sau trên terminal của VPS:
```bash
cloudflared tunnel login
```

- Hệ thống sẽ hiển thị một đường link trình duyệt (URL).
- Sao chép đường link đó và mở trên trình duyệt đã đăng nhập tài khoản Cloudflare của bạn.
- Chọn tên miền **`supportflastdev.io.vn`** và bấm **Authorize**.
- Sau khi ủy quyền thành công, file chứng chỉ `cert.pem` sẽ được tự động tải về thư mục:
  `/root/.cloudflared/cert.pem` (hoặc `~/.cloudflared/cert.pem`).

---

### Bước 3: Tạo Tunnel Định Danh

Chạy lệnh tạo tunnel có tên `supportflast-tunnel`:
```bash
cloudflared tunnel create supportflast-tunnel
```

Kết quả trả về sẽ có dạng:
```
Created tunnel supportflast-tunnel with id 12345678-abcd-ef01-2345-6789abcdef01
```
> **Lưu ý**: Hãy ghi lại chuỗi **Tunnel ID** (ví dụ: `12345678-abcd-ef01-2345-6789abcdef01`).
> File thông tin xác thực tương ứng sẽ nằm tại:
> `/root/.cloudflared/12345678-abcd-ef01-2345-6789abcdef01.json`

---

### Bước 4: Tạo Bản Ghi DNS Tự Động (DNS Routing)

Liên kết tên miền chính và tên miền phụ về Tunnel vừa tạo:
```bash
# 1. Trỏ domain chính
cloudflared tunnel route dns supportflast-tunnel supportflastdev.io.vn

# 2. Trỏ subdomain www
cloudflared tunnel route dns supportflast-tunnel www.supportflastdev.io.vn
```

Lệnh này sẽ tự động tạo các bản ghi `CNAME` trên trang quản lý DNS Cloudflare trỏ về `<Tunnel-ID>.cfargotunnel.com`.

---

### Bước 5: Cấu Hình File `config.yml`

Tạo thư mục cấu hình chuẩn của hệ thống:
```bash
sudo mkdir -p /etc/cloudflared
```

Tạo file `/etc/cloudflared/config.yml` bằng lệnh:
```bash
sudo nano /etc/cloudflared/config.yml
```

Dán nội dung cấu hình sau vào (hãy thay `<TUNNEL-ID>` bằng ID thực tế của bạn):

```yaml
# ==============================================================================
# Cloudflare Tunnel Configuration cho supportflastdev.io.vn
# ==============================================================================
tunnel: 12345678-abcd-ef01-2345-6789abcdef01
credentials-file: /root/.cloudflared/12345678-abcd-ef01-2345-6789abcdef01.json

# Cấu hình giao thức kết nối tối ưu (QUIC/HTTP2)
protocol: quic

ingress:
  # 1. Domain chính trỏ về Nginx Reverse Proxy (Port 80 hoặc 443 nội bộ)
  - hostname: supportflastdev.io.vn
    service: http://127.0.0.1:80
    originRequest:
      connectTimeout: 30s
      noTLSVerify: true
      tcpKeepAlive: 60s

  # 2. Subdomain WWW trỏ về Nginx Reverse Proxy
  - hostname: www.supportflastdev.io.vn
    service: http://127.0.0.1:80
    originRequest:
      connectTimeout: 30s
      noTLSVerify: true
      tcpKeepAlive: 60s

  # 3. Kết nối trực tiếp Go Engine (Tùy chọn debug nội bộ)
  # - hostname: api.supportflastdev.io.vn
  #   service: http://127.0.0.1:8080

  # 4. Fallback mặc định cho các request không hợp lệ (Bắt buộc)
  - service: http_status:404
```

---

### Bước 6: Kiểm Tra Chạy Thử Nghiệm

Chạy thử tunnel để kiểm tra kết nối:
```bash
cloudflared tunnel --config /etc/cloudflared/config.yml run
```

Nếu xuất hiện các dòng log thông báo kết nối thành công tới 4 địa chỉ Cloudflare Data Centers (`Registered tunnel connection...`), nhấn `Ctrl + C` để dừng và tiến hành cài đặt chạy dịch vụ ngầm ở bước 7.

---

### Bước 7: Cài Đặt Làm Linux Systemd Service (Tự Động Khởi Động)

Để `cloudflared` luôn chạy nền và tự bật lại khi reboot VPS:

```bash
# 1. Cài đặt service vào systemd
sudo cloudflared --config /etc/cloudflared/config.yml service install

# 2. Khởi chạy và kích hoạt tự động cùng hệ thống
sudo systemctl daemon-reload
sudo systemctl enable --now cloudflared

# 3. Kiểm tra trạng thái hoạt động
sudo systemctl status cloudflared
```

---

## 5. Thắt Chặt Bảo Mật VPS (VPS Hardening)

Khi đã sử dụng Cloudflare Tunnel, máy chủ **KHÔNG CẦN** mở bất kỳ port HTTP/HTTPS nào ra Internet:

1. **Khóa Port 80 và 443 trên Firewall VPS (UFW)**:
   ```bash
   sudo ufw default deny incoming
   sudo ufw default allow outgoing
   sudo ufw allow 22/tcp comment 'SSH Port'
   sudo ufw enable
   ```
   *Lưu ý: Chỉ mở port SSH (22 hoặc custom port) để bạn quản trị VPS. Tất cả lưu lượng web đều đi an toàn qua Tunnel outbound.*

2. **Cấu hình SSL/TLS trên Cloudflare Dashboard**:
   - Truy cập **SSL/TLS** -> chọn chế độ **Full** hoặc **Full (Strict)** (nếu dùng self-signed cert hoặc Cloudflare Origin CA trên Nginx).
   - Bật **Always Use HTTPS**.
   - Bật **Automatic HTTPS Rewrites**.
   - Bật **HTTP/3 (with QUIC)** và **0-RTT Connection Resumption**.

---

## 6. Xử Lý Sự Cố Thường Gặp (Troubleshooting)

### A. Kiểm tra nhật ký hoạt động (Logs)
```bash
# Xem log trực tiếp của cloudflared
sudo journalctl -u cloudflared -f -n 100

# Xem log của Nginx
sudo tail -f /var/log/nginx/error.log

# Xem log của Go Engine
sudo journalctl -u supportflast -f -n 100
```

### B. Lỗi HTTP 502 Bad Gateway / Error 1033
- **Nguyên nhân**: Dịch vụ Nginx hoặc Go Engine trên VPS chưa được khởi động, hoặc `cloudflared` không kết nối được tới `127.0.0.1:80`.
- **Cách xử lý**:
  ```bash
  sudo systemctl restart nginx
  sudo systemctl restart supportflast
  curl -I http://127.0.0.1:80
  ```

### C. Lỗi WebSocket ngắt kết nối
- Đảm bảo trong `nginx.conf` đã có cấu hình `proxy_set_header Upgrade $http_upgrade;` và `proxy_set_header Connection $connection_upgrade;`.
- Kiểm tra trên Cloudflare Dashboard -> **Network** -> Bật tùy chọn **WebSockets**.

---
*Hoàn tất triển khai hạ tầng Web Server & Reverse Proxy chuẩn Production cho `supportflastdev.io.vn`.*
