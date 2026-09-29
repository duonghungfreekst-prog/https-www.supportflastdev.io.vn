# Hướng Dẫn Tích Hợp API Đẩy Ứng Dụng Tự Động Lên SupportFlast App Hub
**Hệ thống cổng thông tin & kho ứng dụng chính thức: [supportflastdev.io.vn](https://supportflastdev.io.vn)**

---

## 1. Giới thiệu
Bộ công cụ này cung cấp giải pháp **1-Click / CI-CD** cho phép bạn tự động đóng gói và xuất bản ứng dụng từ bất kỳ thư mục dự án nào (C# WPF, Go, Rust, Python, Electron, Flutter, C++, v.v.) trực tiếp lên **SupportFlast App Hub**.

Không cần truy cập trình duyệt để đăng tải thủ công — chỉ cần chạy script hoặc gắn vào quy trình build của dự án.

---

## 2. Các thành phần trong thư mục `tools/`

| Tệp / Thư mục | Mô tả công dụng |
|:---|:---|
| `supportflast_uploader.py` | CLI Uploader viết bằng Python thuần, không phụ thuộc thư viện ngoài (zero-dependency). Tự động tính mã băm SHA-256, stream binary, xử lý multipart upload. |
| `app.yaml` | Tệp cấu hình thông số ứng dụng (tên, phiên bản, nền tảng, danh mục, file cài đặt, API Key). |
| `app.json` | Phiên bản JSON tương đương của `app.yaml` cho các dự án Web / Node.js. |
| `supportflast_deploy.bat` | Script 1-click chạy trên Windows (chỉ cần double click hoặc gõ lệnh). |
| `supportflast_deploy.sh` | Script 1-click chạy trên Linux / macOS / WSL. |
| `.github/workflows/supportflast_publish.yml` | Mẫu cấu hình GitHub Actions CI/CD để tự động phát hành ứng dụng mỗi khi gắn tag Git (`v1.0.0`). |

---

## 3. Cách gắn vào bất kỳ thư mục dự án nào

### Bước 1: Sao chép công cụ vào dự án
Copy 3 tệp sau từ `f:\supportflast.dev\tools\` vào thư mục gốc của dự án bạn muốn đăng:
1. `supportflast_uploader.py`
2. `app.yaml`
3. `supportflast_deploy.bat` (nếu dùng Windows) hoặc `supportflast_deploy.sh` (nếu dùng Linux/macOS)

### Bước 2: Chỉnh sửa `app.yaml` cho dự án của bạn
Mở file `app.yaml` và điền thông tin dự án:
```yaml
name: "Tên Ứng Dụng Của Bạn"
version: "1.0.0"
platform: "Windows 11 / 10" # hoặc macOS / Linux / Đa Nền Tảng
category: "Công cụ Lập trình & IDE" # hoặc Bảo mật, Tiện ích, Mạng & Hạ tầng, Đồ họa
desc: "Mô tả ngắn gọn về giải pháp, tính năng của phần mềm."
author: "Tên Dev / Nhóm Phát Triển"

# Đường dẫn file binary đã build xong (ví dụ file exe trong bin/Release hoặc dist/)
file_path: "bin/Release/net8.0/MyApp.exe"

# API Endpoint và Khóa Xác Thực
server_url: "https://supportflastdev.io.vn"  # hoặc http://127.0.0.1:8080 nếu test local
api_key: "sf_live_default_dev_token_2026"     # API Key của bạn
```

### Bước 3: Đẩy ứng dụng lên Hub

#### Cách A: Chạy 1-Click trên Windows
Double click vào `supportflast_deploy.bat` hoặc mở Terminal gõ:
```cmd
.\supportflast_deploy.bat
```

#### Cách B: Chạy trực tiếp bằng Python
```bash
python supportflast_uploader.py
```

#### Cách C: Ghi đè tham số trực tiếp qua dòng lệnh (CLI Override)
```bash
python supportflast_uploader.py \
  --file "dist/setup-v2.0.exe" \
  --name "Super App" \
  --version "2.0.0" \
  --token "sf_live_default_dev_token_2026"
```

---

## 4. Đặc tả API Endpoint (Dành cho cURL / Custom Script / C# / Go / Rust)

Nếu dự án của bạn muốn gọi trực tiếp HTTP API mà không dùng Python script:

### Endpoint: `POST /api/apps/publish`
- **URL**: `https://supportflastdev.io.vn/api/apps/publish` (hoặc `http://127.0.0.1:8080/api/apps/publish`)
- **Headers**:
  - `Authorization: Bearer <API_TOKEN>` hoặc `X-API-Key: <API_TOKEN>`
  - `Content-Type: multipart/form-data` (hoặc `application/json` nếu chỉ đăng metadata)

### Ví dụ bằng cURL (Tải file nhị phân):
```bash
curl -X POST https://supportflastdev.io.vn/api/apps/publish \
  -H "Authorization: Bearer sf_live_default_dev_token_2026" \
  -F "file=@dist/my-app-v1.0.0.exe" \
  -F "name=My Awesome App" \
  -F "version=1.0.0" \
  -F "platform=Windows 11 / 10" \
  -F "category=Công cụ Lập trình & IDE" \
  -F "desc=Ứng dụng phát triển tối tân cho kỹ sư." \
  -F "author=SupportFlast Dev"
```

### Ví dụ JSON Response khi thành công (HTTP 201 Created):
```json
{
  "status": "success",
  "message": "Ứng dụng và tệp nhị phân đã được xuất bản thành công lên SupportFlast App Hub!",
  "app": {
    "id": "APP-8421",
    "name": "My Awesome App",
    "version": "1.0.0",
    "platform": "Windows 11 / 10",
    "category": "Công cụ Lập trình & IDE",
    "desc": "Ứng dụng phát triển tối tân cho kỹ sư.",
    "file_name": "my-app-v1.0.0.exe",
    "size_bytes": 45182912,
    "size_formatted": "43.1 MB",
    "sha256": "8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918",
    "author": "SupportFlast Dev",
    "downloads": 0,
    "status": "Đã xuất bản",
    "published_at": "2026-09-24T08:18:00+07:00",
    "download_url": "/api/apps/download/APP-8421"
  }
}
```

---

## 5. Tự động sinh thêm API Key mới
Bạn có thể sinh thêm API key cho các thành viên trong đội phát triển hoặc cho từng server CI/CD riêng biệt:

```bash
curl -X POST https://supportflastdev.io.vn/api/keys/generate \
  -H "Authorization: Bearer sf_live_default_dev_token_2026" \
  -H "Content-Type: application/json" \
  -d '{"name": "CI/CD GitHub Actions Key", "expires_in_days": 365}'
```
Mỗi key sinh ra sẽ có tiền tố bảo mật `sf_live_...` và được mã hóa lưu trữ bền vững trong hệ thống.
