# OmniRouter Client Node — Bản App Chia Sẻ Dữ Liệu & Điều Hướng DNS

Ứng dụng máy khách tinh gọn dành riêng cho các thành viên/máy phát triển cần sử dụng AI qua Antigravity IDE, GitHub Copilot, Cursor AI mà không cần can thiệp vào trang web quản trị của hệ thống.

---

## 4 TÍNH NĂNG CỐT LÕI (DUY NHẤT 4 TABS)

1. **Tab [1] Máy Chủ MITM**: Bật/tắt máy chủ proxy cục bộ cổng 443, tự động kiểm tra chứng chỉ SSL (Cert, Trusted, Server).
2. **Tab [2] DNS Intercept**: Bật/tắt chuyển hướng DNS cho Antigravity IDE, GitHub Copilot, Cursor AI về máy chủ MITM 127.0.0.1:443.
3. **Tab [3] Tab Tốc Độ**: Đo đạc độ trễ ping RTT, TTFT, tốc độ tạo token (tokens/s) và kiểm tra kết nối tới Web Master `https://supportflastdev.io.vn`.
4. **Tab [4] Nhập API & Quota/Skills**: Dán Client API Key nhận được từ người quản trị, bấm **"Đồng Bộ Dữ Liệu Từ Web Master"** để tự động kéo Token Quota và danh mục AI Skills về máy.

---

## CÁCH CÀI ĐẶT & SỬ DỤNG (1 CLICK)

### Cách 1: Cài Đặt Tự Động (Khuyên Dùng)
- Nhấp đúp chuột vào file `setup.bat`.
- File cài đặt sẽ tự động:
  1. Cài đặt chứng chỉ Root CA vào Windows.
  2. Tạo biểu tượng Shortcut **"OmniRouter Client Node"** ngoài Màn hình Desktop.
  3. Mở ngay ứng dụng dưới dạng cửa sổ App độc lập.

### Cách 2: Chạy Trực Tiếp (Portable)
- Nhấp đúp vào `run_client.bat` để mở ngay giao diện App Chia Sẻ mà không cần cài đặt.
- Hoặc mở trực tiếp file `index.html` trên bất kỳ trình duyệt nào.
