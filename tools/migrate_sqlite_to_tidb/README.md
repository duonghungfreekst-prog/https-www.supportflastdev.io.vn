# Công Cụ Di Chuyển Dữ Liệu SQLite Sang TiDB Cloud

Công cụ độc lập được viết bằng Go để đọc dữ liệu từ cơ sở dữ liệu SQLite cục bộ (`data/supportflast.db`) và di chuyển toàn bộ dữ liệu lên cụm máy chủ phân tán **TiDB Cloud Serverless** với cơ chế an toàn, phân lô (batching) và chống trùng lặp dữ liệu (Upsert).

---

## 🚀 Tính Năng Chính

1. **Độc lập và gọn nhẹ**: Viết bằng Go thuần, tự quản lý module và kết nối CSDL qua driver `github.com/go-sql-driver/mysql` và `modernc.org/sqlite` (không cần CGO).
2. **Tự động tạo bảng DDL tương thích TiDB**: Tự động sinh bảng và chỉ mục tối ưu cho TiDB Cloud (charset `utf8mb4_unicode_ci`, khóa chính VARCHAR).
3. **Chống trùng lặp tuyệt đối (Idempotency)**:
   - Sử dụng cú pháp `INSERT INTO ... ON DUPLICATE KEY UPDATE` (chế độ mặc định `upsert`).
   - Cập nhật các trường dữ liệu mới nhất từ SQLite mà không phát sinh lỗi khóa trùng lặp.
   - Hỗ trợ chế độ `ignore` (`INSERT IGNORE`) và `replace` (`REPLACE INTO`).
4. **Phân lô xử lý (Batch Processing)**: Chia nhỏ bản ghi theo batch (mặc định 100 bản ghi/lô) giúp tối ưu hiệu năng mạng và tránh tràn bộ nhớ.
5. **Chế độ kiểm tra an toàn (Dry-Run)**: Cho phép quét và thống kê toàn bộ bản ghi nguồn từ SQLite trước khi thực hiện chuyển đổi lên đám mây.
6. **Đối chiếu dữ liệu tự động (Verification Audit)**: So sánh số lượng bản ghi giữa SQLite và TiDB Cloud ngay sau khi hoàn tất.

---

## 📋 Danh Sách Các Bảng Được Di Chuyển

| Bảng | Mô Tả | Khóa Chính |
| :--- | :--- | :--- |
| `users` | Người dùng, phân quyền, mật khẩu hash | `id` |
| `apps` | Siêu dữ liệu ứng dụng, lượt tải, liên kết | `id` |
| `reviews` | Đánh giá và nhận xét cộng đồng | `id` |
| `system_releases` | Phiên bản phát hành hệ thống | `version` |
| `audit_logs` | Nhật ký bảo mật và kiểm toán hệ thống | `id` |
| `api_keys` | Khóa API hệ thống | `id` |
| `security_events` | Sự kiện an ninh và nhật ký chặn IP | `id` |

---

## 🛠️ Hướng Dẫn Sử Dụng

### 1. Kiểm tra trước với chế độ Dry-Run
```powershell
cd tools\migrate_sqlite_to_tidb
go run . -dry-run
```

### 2. Thực hiện di chuyển chính thức
Đảm bảo đã cấu hình `TIDB_PASSWORD` trong file `.env` hoặc truyền trực tiếp qua cờ `-password`:

```powershell
cd tools\migrate_sqlite_to_tidb

# Cách 1: Tự động nạp thông tin từ file .env
go run .

# Cách 2: Truyền trực tiếp mật khẩu
go run . -password "MatKhauTiDBCloudCuaBan"

# Cách 3: Tùy chỉnh kích thước lô và chế độ
go run . -batch 200 -mode upsert
```

### 3. Danh sách các cờ tham số (Flags)

| Cờ | Mặc Định | Ý Nghĩa |
| :--- | :--- | :--- |
| `-sqlite` | `data/supportflast.db` | Đường dẫn tới file SQLite nguồn |
| `-env` | `.env` | Đường dẫn tới file biến môi trường |
| `-host` | `gateway01.ap-southeast-1.prod.aws.tidbcloud.com` | Địa chỉ máy chủ TiDB Cloud |
| `-port` | `4000` | Cổng kết nối TiDB Cloud |
| `-user` | `vjwru2Q7m5oBvH2.root` | Tên tài khoản TiDB Cloud |
| `-password`| *(lấy từ .env)* | Mật khẩu truy cập TiDB Cloud |
| `-database`| `supportflast` | Tên cơ sở dữ liệu TiDB Cloud |
| `-tls` | `true` | Bật mã hóa TLS khi kết nối |
| `-batch` | `100` | Số lượng bản ghi trong mỗi lô chèn |
| `-mode` | `upsert` | Chế độ chèn: `upsert`, `ignore`, `replace` |
| `-dry-run` | `false` | Chỉ kiểm tra dữ liệu, không ghi vào TiDB |
| `-drop-tables` | `false` | Xóa bảng trên TiDB trước khi tạo lại |
| `-verbose` | `false` | Hiển thị log chi tiết |
