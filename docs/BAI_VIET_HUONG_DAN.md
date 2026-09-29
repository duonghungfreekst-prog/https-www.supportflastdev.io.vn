# 📖 BÀI VIẾT HƯỚNG DẪN CÀI ĐẶT & SỬ DỤNG TOÀN TẬP
## AI EQUALIZER PRO & AUDIO PROCESSOR v2.2.0

> **Ứng dụng:** AI Equalizer Pro & Audio Processor (Mã ID: `APP-7290`)  
> **Nền tảng hỗ trợ:** Android 8.0+ (APK) • Web Audio HUD (Chrome, Edge, Cốc Cốc, Firefox)  
> **Mã API Bản Quyền:** `sf_live_db1ead71775e5129ed0533574b8ca7ae317791b7e77d9290`  
> **Cổng phân phối:** https://supportflastdev.io.vn  

---

## 📱 PHẦN 1: HƯỚNG DẪN CÀI ĐẶT TRÊN ĐIỆN THOẠI ANDROID

### Bước 1: Tải tệp tin cài đặt APK
- Truy cập vào đường dẫn: `https://supportflastdev.io.vn/api/apps/download/APP-7290` hoặc tải trực tiếp file **`AIEqualizerPro-v2.2.apk`** về thiết bị di động của bạn.

### Bước 2: Cho phép cài đặt ứng dụng từ nguồn ngoài
- Mở file APK vừa tải về trong thư mục **Tải về (Download)**.
- Khi màn hình bảo mật Android xuất hiện thông báo *"Vì lý do bảo mật, điện thoại không được phép cài đặt các ứng dụng không xác định từ nguồn này"*:
  1. Nhấn **Cài đặt (Settings)**.
  2. Bật công tắc **"Cho phép từ nguồn này" (Allow from this source)**.
  3. Quay lại và bấm **Cài đặt (Install)**.

### Bước 3: Cấp quyền âm thanh & Tối ưu hóa pin chạy ngầm (Quan trọng)
Để trải nghiệm âm thanh không bị gián đoạn khi tắt màn hình:
1. **Quyền Microphone (Ghi âm):** Ứng dụng chỉ sử dụng quyền này để phân tích phổ âm thanh trực tiếp (Visualizer) khi người dùng chọn chế độ Thu Mic hoặc Live HUD. Chọn **"Khi dùng ứng dụng"**.
2. **Tắt tối ưu hóa pin (Chống tắt ngầm Doze Mode):**
   - Vào **Cài đặt máy** ➔ **Ứng dụng** ➔ **AI Equalizer Pro**.
   - Mục **Pin (Battery)** ➔ Chọn **"Không hạn chế" (Unrestricted)**.
   - Với các dòng máy Xiaomi/POCO: Bật thêm quyền **"Tự khởi chạy" (Autostart)**.

---

## 💻 PHẦN 2: HƯỚNG DẪN SỬ DỤNG GIAO DIỆN WEB AUDIO HUD TRÊN MÁY TÍNH

Phiên bản Web HUD được tối ưu hóa bằng công nghệ WebAudio API và Three.js WebGL, hoạt động mượt mà không cần cài đặt phức tạp:

### 1. Khởi chạy Web HUD
- Giải nén file `APP-7290_AIEqualizerPro_v2.2.0_FullPackage.zip`.
- Mở thư mục `web/` và nhấp đúp vào file `index.html` bằng trình duyệt Google Chrome, Cốc Cốc hoặc Microsoft Edge.

### 2. Các nguồn phát âm thanh (Input Sources)
- **Tab Tải Nhạc (Local File):** Kéo thả trực tiếp file nhạc MP3, FLAC, WAV vào khung phát.
- **Tab Synth (Synth Ambient):** Tự động phát các bản nhạc nền thư giãn không lời (Chillwave, Deep Space) do thuật toán tổng hợp âm thanh tự sinh.
- **Tab Thu Mic (Mic Capture):** Thu âm và lọc âm trực tiếp từ micro tai nghe hoặc webcam.
- **Tab Thu Hệ Thống (System Audio):** Lọc âm thời gian thực cho YouTube, Spotify, Phim, Game:
  - *Cách làm:* Bấm nút **"BẬT LỌC ÂM HỆ THỐNG"** ➔ Chọn tab trình duyệt đang phát nhạc (ví dụ Tab YouTube) ➔ **Bắt buộc TÍCH CHỌN ô "Chia sẻ âm thanh hệ thống" (Share tab audio)** ➔ Bấm **Chia sẻ**.

### 3. Tinh chỉnh 10 Băng Tần EQ & 4 Hiệu Ứng DSP
- **10 Băng Tần:** Kéo các thanh trượt từ 32Hz đến 16kHz để cân bằng âm thanh theo sở thích.
  - *Mẹo:* Nhấp đúp chuột vào bất kỳ thanh trượt nào để đưa về mức cân bằng 0dB.
- **Preamp:** Tăng cường âm lượng tổng thể đầu vào.
- **Siêu Trầm (Bass Boost):** Bật lên từ 50% - 80% khi nghe nhạc EDM, Hip-Hop, Remix để cảm nhận âm trầm rung chuyển.
- **Không Gian 3D (3D Spatial):** Mở rộng trường âm thanh nổi (Stereo Widening), tạo cảm giác ca sĩ đang biểu diễn trong không gian rộng lớn quanh bạn.
- **Độ Vang (Reverb):** Bổ sung độ vang phòng hòa nhạc (Concert Hall).

### 4. Phím tắt tiện lợi (Shortcuts)
- **Phím Space (Cách):** Tạm dừng / Tiếp tục phát nhạc.
- **Ctrl + R:** Khôi phục (Reset) toàn bộ cài đặt EQ và hiệu ứng về mặc định.

---

## 🔑 PHẦN 3: HƯỚNG DẪN DÀNH CHO LẬP TRÌNH VIÊN (DEVELOPER GUIDE)

### Tích hợp mã API: `sf_live_db1ead71775e5129ed0533574b8ca7ae317791b7e77d9290`

Ứng dụng hỗ trợ kết nối trực tiếp với máy chủ SupportFlast Hub để đồng bộ Presets lên đám mây CloudPool:

#### 1. Cấu hình biến môi trường (`.env`):
```env
VITE_SUPPORTFLAST_API_KEY=sf_live_db1ead71775e5129ed0533574b8ca7ae317791b7e77d9290
VITE_SUPPORTFLAST_API_URL=https://supportflastdev.io.vn
VITE_APP_ID=APP-7290
```

#### 2. Gọi API kiểm tra bản quyền & trạng thái ứng dụng:
```bash
curl -X GET https://supportflastdev.io.vn/api/apps \
  -H "Authorization: Bearer sf_live_db1ead71775e5129ed0533574b8ca7ae317791b7e77d9290" \
  -H "Content-Type: application/json"
```

#### 3. Đồng bộ Preset lên đám mây:
```bash
curl -X POST https://supportflastdev.io.vn/api/cloudpool/sync-preset \
  -H "Authorization: Bearer sf_live_db1ead71775e5129ed0533574b8ca7ae317791b7e77d9290" \
  -H "Content-Type: application/json" \
  -d '{
    "app_id": "APP-7290",
    "preset_name": "Rock Concert Ultra Bass",
    "bands": [5.5, 4.0, 2.0, 0.0, -1.0, 1.5, 3.0, 4.5, 6.0, 5.0],
    "effects": {"bass_boost": 80, "spatial_3d": 75, "reverb": 40}
  }'
```

---
*Chúc bạn có những giây phút thưởng thức âm thanh đỉnh cao cùng AI Equalizer Pro v2.2.0!*
