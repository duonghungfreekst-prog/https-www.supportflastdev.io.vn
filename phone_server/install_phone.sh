#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SUPPORTFLAST - ONE-LINER UNIVERSAL AUTO INSTALLER CHO ĐIỆN THOẠI ANDROID
# Tự động từ A-Z: Tìm file -> Giải nén -> Cấu hình -> Chạy ngầm -> Gọi 5 Subagent
# ==============================================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
PURPLE='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

clear
echo -e "${CYAN}======================================================================${NC}"
echo -e "${GREEN}${BOLD}   SUPPORTFLAST MOBILE - TRÌNH CÀI ĐẶT 1-CHẠM DUY NHẤT TỪ A-Z         ${NC}"
echo -e "${YELLOW}   Tự tìm gói cài -> Giải nén -> Khởi chạy ngầm -> Gọi 5 Subagents    ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""

# 1. Cài đặt các công cụ cơ bản nếu chưa có
echo -e "${CYAN}[1/5] Kiểm tra công cụ cần thiết (curl, unzip, tar)...${NC}"
pkg install -y unzip curl tar >/dev/null 2>&1 || true

# 2. Tìm kiếm file gói nén supportflast-phone-server.zip
echo -e "${CYAN}[2/5] Đang tự động dò tìm gói cài đặt trên điện thoại...${NC}"

ZIP_CANDIDATES=(
    "$HOME/storage/shared/Download/supportflast-phone-server.zip"
    "/sdcard/Download/supportflast-phone-server.zip"
    "/storage/emulated/0/Download/supportflast-phone-server.zip"
    "$HOME/supportflast-phone-server.zip"
    "./supportflast-phone-server.zip"
    "../supportflast-phone-server.zip"
)

FOUND_ZIP=""
for z in "${ZIP_CANDIDATES[@]}"; do
    if [ -f "$z" ]; then
        FOUND_ZIP="$z"
        break
    fi
done

DEST_DIR="$HOME/phone_server"

# Nếu đã giải nén sẵn trong phone_server thì dùng luôn
if [ -f "$DEST_DIR/auto_install_phone.sh" ]; then
    echo -e "${GREEN}  ✓ Đã phát hiện thư mục cài đặt sẵn sàng tại: ${DEST_DIR}${NC}"
elif [ -n "$FOUND_ZIP" ]; then
    echo -e "${GREEN}  ✓ Đã tìm thấy gói cài đặt tại: ${FOUND_ZIP}${NC}"
    echo -e "${CYAN}  -> Đang tự động giải nén vào ${DEST_DIR}...${NC}"
    mkdir -p "$DEST_DIR"
    unzip -qo "$FOUND_ZIP" -d "$DEST_DIR"
else
    # Nếu chưa có file zip, thử tải tự động qua mạng local nếu có host
    echo -e "${YELLOW}  ℹ Chưa thấy file zip trong Download. Đang thử tải từ mạng nội bộ...${NC}"
    mkdir -p "$DEST_DIR"
    if curl -s --connect-timeout 2 "http://192.168.3.22:8080/supportflast-phone-server.zip" -o "$DEST_DIR/pkg.zip" 2>/dev/null && [ -s "$DEST_DIR/pkg.zip" ]; then
        unzip -qo "$DEST_DIR/pkg.zip" -d "$DEST_DIR"
        rm -f "$DEST_DIR/pkg.zip"
        echo -e "${GREEN}  ✓ Tải và giải nén thành công từ mạng nội bộ!${NC}"
    else
        echo -e "${RED}[LỖI] Không tìm thấy file supportflast-phone-server.zip trong thư mục Download!${NC}"
        echo -e "Vui lòng chép file ${YELLOW}supportflast-phone-server.zip${NC} vào thư mục ${CYAN}Download${NC} của điện thoại rồi chạy lại lệnh này."
        exit 1
    fi
fi

# 3. Chuyển vào thư mục và thực thi kịch bản A-Z
cd "$DEST_DIR"
chmod +x *.sh supportflast-* cloudflared-* 2>/dev/null || true

# Thực thi kịch bản auto install và gọi 5 Subagent
bash auto_install_phone.sh
