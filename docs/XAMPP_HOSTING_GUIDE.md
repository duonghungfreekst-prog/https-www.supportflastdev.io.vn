# HƯỚNG DẪN TRIỂN KHAI VÀ CHẠY SUPPORTFLAST TRÊN XAMPP (APACHE WINDOWS)

Tài liệu này hướng dẫn chi tiết cách chạy nền tảng **SupportFlast Monolith** song song hoặc tích hợp hoàn toàn vào máy chủ **XAMPP (Apache + PHP + MySQL)** trên Windows.

---

## I. NGUYÊN LÝ HOẠT ĐỘNG VỚI XAMPP

Trong kiến trúc XAMPP:
- **XAMPP Apache**: Đóng vai trò là Web Server mặt tiền (Front-facing Reverse Proxy) lắng nghe cổng `80` (HTTP) hoặc `8443` (HTTPS - Đã tránh hoàn toàn cổng `443` để loại bỏ 100% nguy cơ xung đột với các dịch vụ Skype/VMware/Node.js trên Windows).
- **SupportFlast Go Engine**: Đóng vai trò là Monolith Service chạy tại cổng nội bộ `8080` (hoặc cổng bất kỳ), chịu trách nhiệm xử lý API Gateway, Database SQLite, Lưu trữ ảo CloudPool (Google Drive + Local SSD), WebDAV RFC-4918, và Bảo mật đa tầng (RS256, WAF, SIEM).
- **Luồng dữ liệu**:
  ```
  Trình duyệt / Client (Port 80)
            │
            ▼
     XAMPP Apache (httpd.exe)
  [mod_proxy + mod_proxy_http + mod_proxy_wstunnel]
            │
            ▼ (ProxyPass nội bộ 127.0.0.1:8080)
  SupportFlast Monolith Engine (supportflast.exe)
            │
    ┌───────┴───────┬───────────────┐
    ▼               ▼               ▼
SQLite WAL    Virtual Storage    WebDAV RFC-4918
(supportflast.db) (Local + Drive) (/webdav/)
  ```

---

## II. PHƯƠNG ÁN 1: THIẾT LẬP TỰ ĐỘNG 1-CLICK (KHUYẾN NGHỊ)

Hệ thống đã chuẩn bị sẵn kịch bản tự động hóa hoàn toàn:

1. **Bước 1**: Nhấp đúp chuột vào file:
   ```cmd
   F:\supportflast.dev\setup_xampp.bat
   ```
2. **Bước 2**: Script sẽ tự động:
   - Tự nhận diện thư mục cài đặt XAMPP (`C:\xampp`, `D:\xampp`, `E:\xampp`, `F:\xampp`).
   - Tự động kích hoạt các module Apache cần thiết: `mod_proxy.so`, `mod_proxy_http.so`, `mod_proxy_wstunnel.so`, `mod_rewrite.so`, `mod_headers.so`.
   - Cài đặt tệp cấu hình VirtualHost chuyên dụng `httpd-supportflast.conf`.
   - Kiểm tra cú pháp (`Syntax OK`).
3. **Bước 3**: Khởi chạy hệ thống:
   - Nhấp đúp vào `RUN_FULL_SYSTEM.bat` để tự động bật Apache và SupportFlast Engine cùng lúc.
   - Trình duyệt sẽ tự động mở trang chủ tại `http://localhost`.

---

## III. PHƯƠNG ÁN 2: CẤU HÌNH THỦ CÔNG TRONG XAMPP

Nếu bạn muốn tự chỉnh sửa file cấu hình của XAMPP:

### 1. Bật các Module trong `apache\conf\httpd.conf`
Mở tệp `C:\xampp\apache\conf\httpd.conf` (hoặc đường dẫn XAMPP của bạn) bằng Notepad và tìm các dòng sau, bỏ dấu `#` ở đầu dòng:
```apache
LoadModule proxy_module modules/mod_proxy.so
LoadModule proxy_http_module modules/mod_proxy_http.so
LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so
LoadModule rewrite_module modules/mod_rewrite.so
LoadModule headers_module modules/mod_headers.so
```

### 2. Cấu hình Reverse Proxy trong `apache\conf\extra\httpd-vhosts.conf`
Thêm đoạn cấu hình sau vào cuối file `C:\xampp\apache\conf\extra\httpd-vhosts.conf`:
```apache
<VirtualHost *:80>
    ServerName localhost
    ServerAlias 127.0.0.1 supportflast.local

    ProxyPreserveHost On
    RequestHeader set X-Forwarded-Proto "http"
    RequestHeader set X-Forwarded-Port "80"

    # Cho phép upload file lớn tới 2GB
    LimitRequestBody 2147483648

    # WebSocket Proxy cho AI Assistant
    ProxyPass /ws/ ws://127.0.0.1:8080/ws/
    ProxyPassReverse /ws/ ws://127.0.0.1:8080/ws/

    # WebDAV RFC-4918 Reverse Proxy
    ProxyPass /webdav/ http://127.0.0.1:8080/webdav/
    ProxyPassReverse /webdav/ http://127.0.0.1:8080/webdav/

    # Chuyển tiếp toàn bộ Web UI & API
    ProxyPass / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/

    ErrorLog "logs/supportflast_error.log"
    CustomLog "logs/supportflast_access.log" combined
</VirtualHost>
```

---

## IV. PHƯƠNG ÁN 3: CHẠY TRONG THƯ MỤC `htdocs` QUA PHP GATEWAY BRIDGE

Nếu bạn không có quyền sửa `httpd.conf` hoặc chỉ muốn copy thư mục vào `htdocs`:
1. Tạo thư mục `C:\xampp\htdocs\supportflast`.
2. Copy tệp `f:\supportflast.dev\tools\xampp_htdocs_bridge\index.php` vào `C:\xampp\htdocs\supportflast\index.php`.
3. Khi bạn truy cập `http://localhost/supportflast/`, tệp `index.php` sẽ:
   - Tự động kiểm tra và khởi chạy `supportflast.exe` nếu backend chưa bật.
   - Sử dụng PHP cURL để chuyển tiếp toàn bộ request sang `http://127.0.0.1:8080/` trong suốt.

---

## V. KIỂM TRA VÀ XÁC NHẬN HOẠT ĐỘNG (VERIFICATION)

Sau khi khởi chạy Apache trên XAMPP:

| Kiểm tra | Địa chỉ URL | Kết quả mong đợi |
| :--- | :--- | :--- |
| **Giao diện Web** | `http://localhost/` | Hiển thị Hub Trung Tâm SupportFlast đầy đủ giao diện |
| **Kiểm tra Sức khỏe** | `http://localhost/api/health` | Trả về JSON `{"status": "healthy", "service": "supportflast_engine"}` |
| **Chẩn đoán Hosting** | `http://localhost/api/system/diagnostics` | Điểm số `100% Cloud-Ready (6/6 Passed)` |
| **Ổ đĩa Mạng WebDAV** | `http://localhost/webdav/` | Trả về HTTP `200 OK` (hoặc `401 Unauthorized` nếu chưa đăng nhập) |
| **Giao diện Quản trị Lưu trữ** | `http://localhost/storage/` | Đăng nhập tài khoản `admin` / `Admin@2026!SupportFlast` |

---

## VI. KHẮC PHỤC SỰ CỐ (TROUBLESHOOTING)

1. **Cổng 80 bị ứng dụng khác chiếm dụng (như IIS hoặc Skype)**:
   - Trong XAMPP `httpd.conf`, đổi `Listen 80` thành `Listen 8088`.
   - Trong `httpd-vhosts.conf`, đổi `<VirtualHost *:80>` thành `<VirtualHost *:8088>`.
   - Truy cập qua `http://localhost:8088/`.
2. **Lỗi `Cannot load mod_proxy_http.so`**:
   - Chạy lệnh `httpd -t` trong thư mục `xampp\apache\bin` để kiểm tra lỗi cú pháp chính xác.
   - Đảm bảo `mod_proxy.so` được nạp trước `mod_proxy_http.so`.
