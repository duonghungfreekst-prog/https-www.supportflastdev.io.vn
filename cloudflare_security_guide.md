# CẨM NANG BẢO MẬT TOÀN DIỆN CLOUDFLARE CHO DOMAIN: `supportflastdev.io.vn`

Tài liệu hướng dẫn cấu hình 7 lớp bảo mật của **Cloudflare WAF & Zero-Trust Defense** để bảo vệ nền tảng đăng tải ứng dụng **supportflastdev.io.vn** trước mọi cuộc tấn công DDoS, quét lỗ hổng và rò rỉ mã độc.

---

## 🛡️ TỔNG QUAN KIẾN TRÚC BẢO MẬT 7 TẦNG

```
[ Người Dùng / Dev ]
         │
         ▼
[ 1. CLOUDFLARE EDGE (330+ Thành Phố Toàn Cầu) ]
 ├─ Anycast DDoS Shield (100+ Tbps Mitigation)
 ├─ WAF (Web Application Firewall) & OWASP Core Rules
 ├─ Bot Fight Mode & Cloudflare Turnstile Challenge
 ├─ SSL/TLS Encryption: Full (Strict) + TLS 1.3
 ├─ HSTS Preload & Automatic HTTPS Rewrites
 └─ Rate Limiting (Giới hạn tần suất request)
         │
         ▼ (Encrypted HTTPS / Cloudflare Tunnel)
[ 2. ORIGIN SERVER / ZERO-TRUST TUNNEL ]
 ├─ Ẩn 100% IP Thật (Origin IP Masking - Không mở Port Modem)
 ├─ Go Gateway Engine: Cloudflare IP Verification & Spoofing Guard
 ├─ Phục hồi Client Real IP từ CF-Connecting-IP
 └─ Token Bucket Rate Limiter & Sanitize CRLF / XSS / SQLi
         │
         ▼
[ 3. BẢO MẬT ỨNG DỤNG & SUBAGENTS ]
 └─ Sandbox kiểm định mã độc 70+ Antivirus trước khi phân phối
```

---

## 🚀 HƯỚNG DẪN CẤU HÌNH CLOUDFLARE DASHBOARD TỪNG BƯỚC

### BƯỚC 1: Bật Proxy Đám Mây Màu Cam (Orange Cloud)
Tại mục **DNS** -> **Records**:
1. Bản ghi `A` (`@` trỏ về IP Server): Chuyển trạng thái Proxy status sang **Proxied (Đám mây cam)**.
2. Bản ghi `CNAME` (`www` trỏ về `supportflastdev.io.vn`): Chuyển trạng thái sang **Proxied (Đám mây cam)**.
> **Lợi ích**: IP máy chủ thật của bạn sẽ được ẩn hoàn toàn. Mọi truy vấn từ Internet chỉ nhìn thấy IP của Cloudflare Anycast.

---

### BƯỚC 2: Cấu Hình SSL/TLS Sang Full (Strict) & TLS 1.3
Tại mục **SSL/TLS**:
1. **Overview**: Chọn chế độ **Full (strict)**.
   - Trình duyệt $\leftrightarrow$ Cloudflare: Mã hóa SSL Cloudflare Edge.
   - Cloudflare $\leftrightarrow$ Origin Server: Bắt buộc mã hóa SSL hợp lệ (Let's Encrypt qua Caddy/Nginx hoặc Cloudflare Origin CA).
2. **Edge Certificates**:
   - Bật **Always Use HTTPS** $\rightarrow$ ON.
   - Bật **HTTP Strict Transport Security (HSTS)**:
     + Max-Age: `1 year (31536000 seconds)`
     + Include subdomains: `ON`
     + Preload: `ON`
   - **Minimum TLS Version**: Chọn **TLS 1.3** (hoặc TLS 1.2 nếu cần hỗ trợ thiết bị cũ).
   - **Opportunistic Encryption**: `ON`.
   - **Automatic HTTPS Rewrites**: `ON`.

---

### BƯỚC 3: Cấu Hình WAF (Web Application Firewall) & Custom Rules
Tại mục **Security** -> **WAF** -> **Custom rules**:
Tạo các quy tắc bảo vệ:

#### Quy tắc 1: Bảo Vệ Cổng Đăng Tải /api/apps/publish
- **Rule Name**: `Protect-App-Publish-API`
- **Expression**:
  `(http.request.uri.path eq "/api/apps/publish" and not cf.client.bot)`
- **Action**: `Managed Challenge` (hoặc `Rate Limit`).

#### Quy tắc 2: Chặn Bad User-Agents & Tools Quét Lỗ Hổng
- **Rule Name**: `Block-Malicious-Scanners`
- **Expression**:
  `(http.user_agent contains "sqlmap" or http.user_agent contains "nikto" or http.user_agent contains "nmap" or http.user_agent contains "masscan" or http.user_agent contains "acunetix")`
- **Action**: `Block`.

#### Quy tắc 3: Bật OWASP Core Rule Set
- Tại mục **WAF** -> **Managed rules**:
  - Bật **Cloudflare Managed Ruleset**: Action = `Block`.
  - Bật **Cloudflare OWASP Core Ruleset**: Paranoia Level = 1, Anomaly Score >= 25 $\rightarrow$ `Block`.

---

### BƯỚC 4: Chống Bot Độc Hại & Quét Dữ Liệu
Tại mục **Security** -> **Bots**:
1. Bật **Bot Fight Mode**: `ON`.
   - Tự động phát hiện và thách thức các headless browser, bot cào dữ liệu trái phép.
2. (Tùy chọn) Bật **Super Bot Fight Mode** nếu dùng gói Cloudflare Pro/Business.

---

### BƯỚC 5: Thiết Lập Cloudflare Tunnel (Bảo Mật Tối Thượng - Zero Open Ports)
Nếu bạn chạy ứng dụng trên máy tính Windows hoặc server không có IP tĩnh:
1. File cấu hình đã được tạo sẵn tại: **`f:\supportflast.dev\cloudflared_config.yml`**.
2. Chạy lệnh:
   ```powershell
   cloudflared tunnel --config f:\supportflast.dev\cloudflared_config.yml run
   ```
> **Đặc điểm siêu việt**: Bạn **KHÔNG** cần mở bất kỳ cổng nào trên Modem/Router (Không cần Port Forwarding 80/443). Máy tính của bạn chỉ tạo kết nối outbound an toàn tới Cloudflare. Hacker không có cách nào quét được IP máy chủ của bạn!

---

## 💻 KIỂM TRA BẢO MẬT THỜI GIAN THỰC TỪ TERMINAL

Để kiểm tra các Security Headers và Cloudflare WAF đang hoạt động trên hệ thống:

```powershell
curl -I http://127.0.0.1:8080/api/security/cloudflare-status
```

Dữ liệu trả về sẽ hiển thị đầy đủ:
- `CF-Ray`: Mã định danh phiên bảo mật duy nhất từ Cloudflare Anycast PoP.
- `X-Edge-Shield`: `Cloudflare WAF + SupportFlast Zero-Trust`.
- `Strict-Transport-Security`: `max-age=31536000; includeSubDomains; preload`.
- `X-Content-Type-Options`: `nosniff`.
- `X-Frame-Options`: `DENY`.
- `Content-Security-Policy`: Đầy đủ quy chuẩn an ninh.
