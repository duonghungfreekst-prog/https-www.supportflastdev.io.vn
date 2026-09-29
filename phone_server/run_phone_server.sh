#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SUPPORTFLAST MOBILE SERVER - SCRIPT KHỞI CHẠY MÁY CHỦ TRÊN ĐIỆN THOẠI
# ==============================================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
PURPLE='\033[0;35m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PARENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 1. Tự động tìm binary phù hợp
ARCH=$(uname -m)
BINARY_FILE=""
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    BINARY_FILE="supportflast-linux-arm64"
elif [ "$ARCH" = "armv7l" ] || [ "$ARCH" = "arm" ]; then
    BINARY_FILE="supportflast-linux-armv7"
else
    BINARY_FILE="supportflast-linux-arm64"
fi

BIN_PATH=""
if [ -f "$SCRIPT_DIR/$BINARY_FILE" ]; then
    BIN_PATH="$SCRIPT_DIR/$BINARY_FILE"
elif [ -f "$PARENT_DIR/$BINARY_FILE" ]; then
    BIN_PATH="$PARENT_DIR/$BINARY_FILE"
elif [ -f "$PARENT_DIR/supportflast_engine/$BINARY_FILE" ]; then
    BIN_PATH="$PARENT_DIR/supportflast_engine/$BINARY_FILE"
fi

if [ -z "$BIN_PATH" ]; then
    echo -e "${YELLOW}[CẢNH BÁO] Chưa tìm thấy $BINARY_FILE. Đang chạy script setup...${NC}"
    bash "$SCRIPT_DIR/setup_phone_server.sh"
    BIN_PATH="$SCRIPT_DIR/$BINARY_FILE"
fi

# 2. Duy trì Wake-Lock
if command -v termux-wake-lock >/dev/null 2>&1; then
    termux-wake-lock
fi

# 3. Tìm địa chỉ IP của điện thoại trong mạng Wi-Fi
LOCAL_IP=""
if command -v ip >/dev/null 2>&1; then
    LOCAL_IP=$(ip -4 addr show wlan0 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1)
fi
if [ -z "$LOCAL_IP" ] && command -v ifconfig >/dev/null 2>&1; then
    LOCAL_IP=$(ifconfig wlan0 2>/dev/null | grep 'inet ' | awk '{print $2}' | sed 's/addr://')
fi
if [ -z "$LOCAL_IP" ]; then
    LOCAL_IP="127.0.0.1"
fi

PORT="8080"
if [ -f "$SCRIPT_DIR/.env" ]; then
    export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
fi

echo -e "${CYAN}======================================================================${NC}"
echo -e "${GREEN}   SUPPORTFLAST MOBILE SERVER - ĐANG KHỞI CHẠY TRÊN ĐIỆN THOẠI        ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo -e "📱 Thiết bị:       ${YELLOW}Android ($(uname -m))${NC}"
echo -e "🚀 Trình chạy:     ${GREEN}${BINARY_FILE}${NC}"
echo -e "⚡ Quản lý RAM:    ${GREEN}GOGC=50 (Kiểm soát nghiêm ngặt < 35MB)${NC}"
echo -e "🤖 Subagents Pool: ${PURPLE}10 Subagents Đồng Thời (Worker Pool Limit: 10)${NC}"
echo ""
echo -e "🌐 ĐỊA CHỈ TRUY CẬP TRÊN ĐIỆN THOẠI & MẠNG NỘI BỘ (WI-FI):"
echo -e "   • Trên điện thoại này:       ${GREEN}http://localhost:${PORT}${NC}"
echo -e "   • Bảng điều khiển 10 Agents: ${GREEN}http://localhost:${PORT}/mobile_dashboard.html${NC}"
echo -e "   • Từ máy tính/thiết bị khác: ${YELLOW}http://${LOCAL_IP}:${PORT}${NC}"
echo ""
echo -e "🌍 ĐƯA RA INTERNET TOÀN CẦU (MIỄN PHÍ, KHÔNG CẦN MỞ PORT MODEM):"
echo -e "   Mở thêm 1 tab Termux mới và gõ lệnh sau:"
echo -e "   ${CYAN}cloudflared tunnel --url http://localhost:${PORT}${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo -e "${YELLOW}Nhấn Ctrl + C để dừng máy chủ bất kỳ lúc nào.${NC}"
echo ""

# 4. Xuất các biến môi trường tối ưu hóa tài nguyên cực hạn cho máy di động
export GOGC=50
export GOMEMLIMIT=128MiB
export STATIC_DIR="$PARENT_DIR/supportflast_ui"
export DATA_DIR="$SCRIPT_DIR/data"
export STORAGE_DIR="$SCRIPT_DIR/storage"

cd "$SCRIPT_DIR"
exec "$BIN_PATH"
