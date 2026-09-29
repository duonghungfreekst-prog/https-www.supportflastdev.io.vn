# QUY TRÌNH CHUẨN QUẢN LÝ CƠ SỞ DỮ LIỆU BẢO MẬT CAO
## SUPPORTFLAST ENTERPRISE PLATFORM
**Phiên bản**: v2.6.0 Enterprise | **Chuẩn an ninh**: ISO/IEC 27001, OWASP Top 10, Zero Data Loss Standard

---

## 1. TỔNG QUAN KIẾN TRÚC DATABASE 3 TẦNG (3-TIER RESILIENCE)

Để loại bỏ hoàn toàn nguy cơ mất mát dữ liệu và đảm bảo hiệu năng phục vụ hàng triệu người dùng, hệ thống SupportFlast áp dụng mô hình lưu trữ 3 tầng phân tán độc lập:

```
[ Người Dùng & Quản Trị Viên ]
              │
              ▼
   [ Cloudflare Edge WAF & Turnstile CAPTCHA ] (Chặn DDoS L3/L4/L7 & Bot)
              │
              ▼ (TLS 1.3 / Argo Tunnel)
     [ SupportFlast Go Engine ] (Rate Limit + Prepared Statement + Connection Pool)
              │
    ┌─────────┴───────────────────────┐
    ▼                                 ▼
┌──────────────────────────────┐    ┌──────────────────────────────┐
│  TẦNG 1: PRIMARY CLOUD DB     │    │  TẦNG 2: LOCAL REPLICA       │
│  TiDB Cloud Serverless       │    │  SQLite WAL High-Speed       │
│  - Multi-AZ Raft (3 bản sao) │    │  - supportflast.db           │
│  - Auto-failover             │    │  - cloudpool_metadata.db     │
│  - Bắt buộc TLS 1.2+         │    │  - Đọc/ghi siêu tốc nội bộ   │
└──────────────────────────────┘    └──────────────────────────────┘
              │                               │
              └───────────────┬───────────────┘
                              ▼
               ┌──────────────────────────────┐
               │  TẦNG 3: COLD DISASTER STORE │
               │  Google Drive Auto-Backup    │
               │  - Mã hóa AES-256-GCM        │
               │  - Timestamp định danh riêng │
               │  - Chống ghi đè tuyệt đối    │
               └──────────────────────────────┘
```

1. **Tầng 1 (Primary Cloud DB)**: **TiDB Cloud Serverless**
   - Hệ quản trị cơ sở dữ liệu phân tán chuẩn MySQL wire protocol đặt tại trung tâm dữ liệu AWS Singapore.
   - Dữ liệu được sao chép phân tán qua 3 Availability Zones bằng thuật toán đồng thuận Raft (TiKV Engine). Khi 1 datacenter gặp sự cố, hệ thống tự động chuyển vùng trong tích tắc mà không gián đoạn dịch vụ.
   - Toàn bộ kết nối ra vào bắt buộc mã hóa qua **TLS 1.2+**.

2. **Tầng 2 (Local Replica & Cache)**: **SQLite WAL Mode**
   - Lưu trữ trực tiếp tại `data/supportflast.db` và `data/cloudpool_metadata.db`.
   - Chạy ở chế độ **Write-Ahead Logging (WAL)**: Cho phép hàng nghìn luồng đọc đồng thời mà không bị nghẽn (Lock-free Read).
   - Đóng vai trò bản sao cục bộ giúp hệ thống khởi động ngay lập tức và có thể hoạt động ngoại tuyến khi tuyến cáp quang quốc tế gặp sự cố.

3. **Tầng 3 (Off-site Cold Disaster Recovery)**: **Google Drive Backup**
   - Bản sao lưu được đóng gói nén ZIP, mã hóa bí mật với **AES-256-GCM**.
   - Tên file chứa dấu thời gian độc nhất: `supportflast_backup_YYYYMMDD_HHMMSS.zip`. Tuyệt đối không ghi đè lên các bản sao lưu cũ.
   - Lưu trữ trên tài khoản Google Drive độc lập (`duongmanhhung9900@gmail.com`).

---

## 2. QUY TRÌNH CHỐNG HACK & BẢO MẬT DỮ LIỆU (ANTI-HACK STANDARD)

### 2.1. Triệt Tiêu 100% Lỗ Hổng SQL Injection (SQLi)
- **Nguyên tắc cốt lõi**: Tuyệt đối không bao giờ nối chuỗi tạo câu lệnh SQL (`fmt.Sprintf`, `+`, `concat`).
- **Chuẩn thực thi**: 100% truy vấn trên toàn bộ hệ thống phải sử dụng **Prepared Statements (`db.Prepare`)** và **Parameterized Queries (`?`)**.
- **Bộ lọc kiểm soát đầu vào**: Mọi dữ liệu từ người dùng gửi lên đều đi qua bộ lọc `security.ValidateInput` chặn đứng các mẫu tấn công:
  - `UNION SELECT`, `' OR 1=1`, `; DROP TABLE`, `--`, `/* */`
  - Cross-Site Scripting (XSS): `<script>`, `onload=`, `javascript:`
  - Path Traversal: `../`, `..\`, `..%2f`

### 2.2. Bảo Vệ Thông Tin Định Danh & Mật Khẩu
- Mật khẩu người dùng được băm bằng thuật toán **BCrypt với Cost Factor = 12**, đảm bảo khả năng chống vét cạn (Brute-Force) và Rainbow Tables bằng phần cứng GPU chuyên dụng.
- Tuyệt đối không lưu mật khẩu ở dạng văn bản rõ (Plaintext).
- **Constant-Time Comparison**: Mọi phép so sánh token, mã băm mật khẩu và HMAC đều sử dụng hàm so sánh thời gian hằng số `subtle.ConstantTimeCompare` để loại bỏ hoàn toàn tấn công rò rỉ thời gian (Timing Side-Channel Attacks).

### 2.3. Che Giấu Chi Tiết Lỗi Hệ Thống (Information Disclosure Shield)
- Tuân thủ nghiêm ngặt **Quy tắc 3.5**: Tuyệt đối không trả về Stack Trace, tên bảng, tên cột hoặc thông báo lỗi database cho phía máy khách (Client).
- Máy khách chỉ nhận mã lỗi chung chuẩn mực:
  ```json
  {"error": "Yêu cầu không hợp lệ hoặc dữ liệu không tồn tại", "code": "ERR_DATABASE_OPERATION"}
  ```
- Chi tiết lỗi kỹ thuật được ghi riêng vào nhật ký bảo mật phía máy chủ để phục vụ công tác điều tra số (Forensics).

### 2.4. Hệ Thống Giám Sát Nhật Ký Kiểm Toán (Audit Logs & Security Events)
- Mọi hành vi quản trị, đăng nhập, đổi mật khẩu, hoặc truy cập trái phép đều được ghi nhận vào 2 bảng chuyên dụng:
  - `audit_logs`: Lưu vết người dùng, hành động, IP nguồn (đã lọc chống CRLF Log Injection), User Agent, thời gian chính xác.
  - `security_events`: Ghi nhận các sự kiện đe dọa an ninh, kích hoạt cơ chế tự động khóa IP vào danh sách đen.

---

## 3. QUY TRÌNH CHỐNG DoS / DDoS & CẠN KIỆT TÀI NGUYÊN (ANTI-DDoS STANDARD)

Tấn công từ chối dịch vụ (DDoS) vào database thường xảy ra dưới dạng: làm cạn kiệt số lượng kết nối (Connection Exhaustion) hoặc gửi các câu truy vấn nặng khiến CPU/RAM bị nghẽn (Slow Query DoS). Hệ thống SupportFlast triển khai 4 chốt chặn phòng thủ:

### 3.1. Chốt Chặn 1: Cloudflare Edge CDN WAF & Turnstile CAPTCHA
- **Cloudflare Edge**: Hấp thụ toàn bộ lưu lượng tấn công mạng L3/L4 (SYN Flood, UDP Flood) và L7 (HTTP Flood) tại 300+ thành phố trên toàn cầu trước khi đến máy chủ.
- **Turnstile CAPTCHA**: Xác thực danh tính trình duyệt không xâm lấn, loại bỏ 100% lưu lượng bot tự động dò quét lỗ hổng hoặc spam request.

### 3.2. Chốt Chặn 2: Phân Tầng Giới Hạn Tần Suất (3-Tier Rate Limiting)
Hệ thống Go Engine kiểm soát lưu lượng theo 3 tầng độc lập:
1. **Tầng Toàn cục (Global Rate Limit)**: Giới hạn tối đa 1200 yêu cầu / phút trên mỗi địa chỉ IP. Vượt ngưỡng sẽ trả về HTTP 429 Too Many Requests.
2. **Tầng Xác thực (Authentication Lockout)**: Giới hạn đăng nhập sai tối đa 5 lần liên tiếp. Khi đạt ngưỡng, tự động khóa IP trong **15 phút**.
3. **Tầng Quản trị (Admin Protection)**: Giới hạn các endpoint nhạy cảm (quản lý người dùng, cấu hình) tối đa 30 yêu cầu / phút.

### 3.3. Chốt Chặn 3: Kiểm Soát Bể Kết Nối Cơ Sở Dữ Liệu (Connection Pool Governance)
Để ngăn chặn tình trạng hàng ngàn request đồng thời chiếm dụng kết nối làm sập Database Server:
- `MaxOpenConns`: Cố định tối đa **25 kết nối mở đồng thời**. Mọi request vượt quá sẽ được xếp hàng đợi (queue) thay vì mở thêm kết nối vô hạn.
- `MaxIdleConns`: Giữ tối đa **10 kết nối nhàn rỗi sẵn sàng** để phản hồi tức thì mà không phải thực hiện bắt tay TCP/TLS lại.
- `ConnMaxLifetime`: Giới hạn tuổi thọ kết nối tối đa **5 phút**, tự động giải phóng và tái tạo kết nối mới, chống rò rỉ socket mạng.
- `ConnMaxIdleTime`: Kết nối nhàn rỗi quá **3 phút** sẽ được tự động đóng lại.

### 3.4. Chốt Chặn 4: Ngắt Kết Nối Truy Vấn Treo (Query Timeout Guard)
- Bất kỳ câu truy vấn cơ sở dữ liệu nào đều phải được gán ngữ cảnh giới hạn thời gian qua `context.WithTimeout(ctx, 5*time.Second)`.
- Nếu câu truy vấn chạy quá 5 giây (do mạng nghẽn hoặc tải cao), hệ thống sẽ chủ động ngắt kết nối ngay lập tức, ngăn ngừa tình trạng tích tụ câu lệnh treo gây tê liệt CPU và tràn bộ nhớ RAM (OOM).

---

## 4. QUY TRÌNH CHỐNG MẤT DỮ LIỆU TUYỆT ĐỐI (ZERO DATA LOSS POLICY)

### 4.1. Nguyên Tắc An Toàn
1. **Không ghi đè (No Overwrite)**: Mọi bản sao lưu xuất ra phải có dấu thời gian `YYYYMMDD_HHMMSS` riêng biệt.
2. **Kiểm tra trước khi thao tác (Integrity Pre-check)**: Luôn chạy `PRAGMA integrity_check` và `PRAGMA wal_checkpoint(TRUNCATE)` trước khi xuất dữ liệu.
3. **Đồng bộ Idempotent**: Quy trình đẩy dữ liệu lên TiDB Cloud sử dụng cú pháp `ON DUPLICATE KEY UPDATE` và `INSERT IGNORE`, đảm bảo đồng bộ nhiều lần không gây trùng lặp hay ghi đè sai dữ liệu.

---

## 5. HƯỚNG DẪN VẬN HÀNH BẰNG BỘ CÔNG CỤ CHUẨN (CLI RUNBOOK)

Hệ thống cung cấp công cụ tự động hóa toàn diện tại `tools/db_security_manager.py`.

### 5.1. Kiểm Toán An Ninh Cơ Sở Dữ Liệu
Kiểm tra kết nối TLS, chế độ WAL, tính toàn vẹn file CSDL, kiểm tra tài khoản người dùng, rà soát nhật ký an ninh:
```powershell
python tools/db_security_manager.py audit
```

### 5.2. Chạy Sao Lưu 3 Tầng Lên Google Drive
Thực hiện toàn bộ quy trình: Checkpoint WAL -> Xuất Snapshot JSON 18 bảng -> Nén mã hóa ZIP timestamp -> Đẩy lên Google Drive:
```powershell
python tools/db_security_manager.py backup
```

### 5.3. Đồng Bộ Dữ Liệu Sang TiDB Cloud
Đồng bộ an toàn toàn bộ 18 bảng dữ liệu từ máy chủ nội bộ lên TiDB Cloud Serverless:
```powershell
python tools/db_security_manager.py sync
```

### 5.4. Chạy Toàn Bộ Chu Trình Bảo Trì Định Kỳ
Chạy liên hoàn Audit -> Sync TiDB -> Backup Google Drive:
```powershell
python tools/db_security_manager.py all
```

---

## 6. KỊCH BẢN ỨNG PHÓ VÀ KHÔI PHỤC THẢM HỌA (DISASTER RECOVERY RUNBOOK)

| Tình huống sự cố | Triệu chứng | Quy trình xử lý chuẩn |
| :--- | :--- | :--- |
| **Bị tấn công DDoS** | CPU tăng cao, lượng request từ nhiều IP lạ | 1. Bật chế độ "Under Attack Mode" trên Cloudflare Dashboard.<br>2. Kiểm tra `audit_logs` để tìm dải IP tấn công.<br>3. Thêm quy tắc IP Block trên Cloudflare WAF. |
| **Mất kết nối quốc tế** | Không truy cập được TiDB Cloud | 1. Hệ thống tự động chuyển vùng đọc/ghi sang SQLite WAL nội bộ.<br>2. Sau khi đường truyền khôi phục, chạy `python tools/db_security_manager.py sync` để đẩy dữ liệu mới lên Cloud. |
| **Lỗi hỏng CSDL cục bộ** | SQLite báo file corrupted | 1. Khôi phục từ bản Snapshot gần nhất bằng lệnh:<br>`python tools/restore_snapshots.py`<br>2. Hoặc tải file ZIP gần nhất từ Google Drive (`SupportFlast_Backups`) về giải nén vào thư mục `data/`. |
| **Phát hiện truy cập trái phép** | Có tài khoản lạ hoặc hành động lạ trong audit log | 1. Chạy ngay `python tools/db_security_manager.py audit`.<br>2. Đổi mật khẩu tài khoản Admin chuẩn.<br>3. Khóa IP của kẻ tấn công trong bảng `security_events`. |

---
*Tài liệu được ban hành chính thức cho toàn bộ đội ngũ vận hành hệ thống SupportFlast Platform.*
