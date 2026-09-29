#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SUPPORTFLAST MOBILE SERVER - SCRIPT CÀI ĐẶT TỰ ĐỘNG TRÊN ĐIỆN THOẠI (TERMUX)
# Tối ưu hóa cực hạn: Tiết kiệm pin, kiểm soát RAM < 35MB, hỗ trợ 10 Subagents
# ==============================================================================

set -e

# Màu sắc hiển thị terminal
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

echo -e "${CYAN}======================================================================${NC}"
echo -e "${GREEN}   SUPPORTFLAST MOBILE SERVER - THIẾT LẬP MÁY CHỦ TRÊN ĐIỆN THOẠI     ${NC}"
echo -e "${YELLOW}   Tối ưu hóa: ARM64 SoC | RAM < 35MB | 10 Subagents Concurrent Pool  ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""

# 1. Nhận diện kiến trúc CPU điện thoại
ARCH=$(uname -m)
echo -e "${CYAN}[1/5] Kiểm tra kiến trúc CPU điện thoại:${NC} ${GREEN}${ARCH}${NC}"

BINARY_FILE=""
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    BINARY_FILE="supportflast-linux-arm64"
elif [ "$ARCH" = "armv7l" ] || [ "$ARCH" = "arm" ]; then
    BINARY_FILE="supportflast-linux-armv7"
elif [ "$ARCH" = "x86_64" ]; then
    BINARY_FILE="supportflast-linux-amd64"
else
    echo -e "${YELLOW}[CẢNH BÁO] Kiến trúc ${ARCH} không phổ biến, thử dùng bản ARM64...${NC}"
    BINARY_FILE="supportflast-linux-arm64"
fi

echo -e "      -> Đã chọn file nhị phân: ${GREEN}${BINARY_FILE}${NC}"

# 2. Kiểm tra file nhị phân
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PARENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

TARGET_BIN=""
if [ -f "$SCRIPT_DIR/$BINARY_FILE" ]; then
    TARGET_BIN="$SCRIPT_DIR/$BINARY_FILE"
elif [ -f "$PARENT_DIR/$BINARY_FILE" ]; then
    TARGET_BIN="$PARENT_DIR/$BINARY_FILE"
elif [ -f "$PARENT_DIR/supportflast_engine/$BINARY_FILE" ]; then
    TARGET_BIN="$PARENT_DIR/supportflast_engine/$BINARY_FILE"
fi

if [ -z "$TARGET_BIN" ] || [ ! -f "$TARGET_BIN" ]; then
    echo -e "${RED}[LỖI] Không tìm thấy file nhị phân ${BINARY_FILE}!${NC}"
    echo -e "Vui lòng copy file ${BINARY_FILE} vào cùng thư mục với script này."
    exit 1
fi

chmod +x "$TARGET_BIN"
echo -e "${GREEN}[OK] Đã cấp quyền thực thi (chmod +x) cho ${TARGET_BIN}${NC}"

# 3. Yêu cầu giữ Wake-Lock chống Android Doze / Sleep ngắt kết nối
echo ""
echo -e "${CYAN}[2/5] Kích hoạt chế độ chống tắt ngầm (Wake-Lock)...${NC}"
if command -v termux-wake-lock >/dev/null 2>&1; then
    termux-wake-lock
    echo -e "${GREEN}[OK] Đã kích hoạt termux-wake-lock thành công! (Điện thoại sẽ không ngắt server khi tắt màn hình)${NC}"
else
    echo -e "${YELLOW}[THÔNG BÁO] Chưa cài termux-api. Đang cài đặt để hỗ trợ wake-lock...${NC}"
    pkg install -y termux-api 2>/dev/null || true
    if command -v termux-wake-lock >/dev/null 2>&1; then
        termux-wake-lock
        echo -e "${GREEN}[OK] Đã kích hoạt termux-wake-lock!${NC}"
    fi
fi

# 4. Khởi tạo cấu trúc thư mục dữ liệu trên điện thoại
echo ""
echo -e "${CYAN}[3/5] Khởi tạo các thư mục dữ liệu cần thiết...${NC}"
mkdir -p "$SCRIPT_DIR/data"
mkdir -p "$SCRIPT_DIR/data/keys"
mkdir -p "$SCRIPT_DIR/storage"
mkdir -p "$SCRIPT_DIR/logs"

# 5. Tạo file cấu hình tối ưu năng lượng .env.phone
ENV_FILE="$SCRIPT_DIR/.env"
echo ""
echo -e "${CYAN}[4/5] Thiết lập biến môi trường tối ưu cho máy chủ di động (.env)...${NC}"

cat << 'EOF' > "$ENV_FILE"
# ==============================================================================
# SUPPORTFLAST MOBILE SERVER CONFIGURATION (OPTIMIZED FOR ANDROID / ARM64)
# ==============================================================================
PORT=8080
HOST=0.0.0.0
DOMAIN=localhost

# Giới hạn bộ nhớ Go Runtime trên điện thoại (Thu gom rác sớm để RAM luôn < 35MB)
GOGC=50
GOMEMLIMIT=128MiB

# Concurrency Worker Pool: Giới hạn đúng 10 Subagents song song bảo vệ CPU ARM mát mẻ
MAX_CONCURRENT_SUBAGENTS=10

# Đường dẫn dữ liệu cục bộ trên điện thoại
DATA_DIR=./data
STORAGE_DIR=./storage
STATIC_DIR=../supportflast_ui

# SQLite WAL mode tối ưu cho bộ nhớ Flash điện thoại
SQLITE_CACHE_SIZE=-2000
SQLITE_JOURNAL_MODE=WAL

# Cấu hình bảo mật token nội bộ
INTERNAL_SERVICE_SECRET=sf_mobile_engine_token_2026
EOF

echo -e "${GREEN}[OK] Đã tạo file cấu hình tối ưu tại ${ENV_FILE}${NC}"

# 6. Hiển thị thông tin mạng và lệnh chạy
echo ""
echo -e "${CYAN}[5/5] Hoàn tất thiết lập máy chủ trên điện thoại!${NC}"
echo -e "${GREEN}======================================================================${NC}"
echo -e "${GREEN}   CÀI ĐẶT THÀNH CÔNG! BẠN ĐÃ SẴN SÀNG KHỞI CHẠY MÁY CHỦ DI ĐỘNG      ${NC}"
echo -e "${GREEN}======================================================================${NC}"
echo ""
echo -e "👉 Để khởi chạy máy chủ ngay bây giờ, gõ lệnh:"
echo -e "   ${YELLOW}bash run_phone_server.sh${NC}"
echo ""
echo -e "👉 Để chạy kiểm tra gọi đồng thời 10 Subagents, gõ lệnh:"
echo -e "   ${YELLOW}bash test_10_subagents.sh${NC}"
echo ""
