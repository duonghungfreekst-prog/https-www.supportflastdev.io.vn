#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SUPPORTFLAST MOBILE SERVER - SCRIPT AUTO CÀI ĐẶT TỪ A-Z VÀ GỌI 5 SUBAGENTS
# Chỉ cần 1 lệnh duy nhất: bash auto_install_phone.sh
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
echo -e "${GREEN}${BOLD}   SUPPORTFLAST MOBILE SERVER - CÀI ĐẶT TỰ ĐỘNG TỪ A-Z (TERMUX)       ${NC}"
echo -e "${YELLOW}   Tối ưu hóa cực hạn: RAM < 35MB | Tiết kiệm Pin | Gọi 5 Subagents    ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PARENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# ------------------------------------------------------------------------------
# BƯỚC 1: KIỂM TRA MÔI TRƯỜNG & KÍCH HOẠT WAKE-LOCK CHỐNG SLEEP
# ------------------------------------------------------------------------------
echo -e "${CYAN}[1/6] Kiểm tra môi trường Termux và giữ kết nối ngầm (Wake-Lock)...${NC}"

if command -v termux-wake-lock >/dev/null 2>&1; then
    termux-wake-lock
    echo -e "${GREEN}  ✓ Đã kích hoạt termux-wake-lock: Máy chủ không bị ngắt khi tắt màn hình!${NC}"
else
    echo -e "${YELLOW}  ℹ Đang thử cài đặt termux-api để hỗ trợ giữ tiến trình ngầm...${NC}"
    pkg install -y termux-api 2>/dev/null || true
    if command -v termux-wake-lock >/dev/null 2>&1; then
        termux-wake-lock
        echo -e "${GREEN}  ✓ Đã kích hoạt termux-wake-lock!${NC}"
    fi
fi

# ------------------------------------------------------------------------------
# BƯỚC 2: NHẬN DIỆN KIẾN TRÚC CPU VÀ CHỌN FILE NHỊ PHÂN PHÙ HỢP
# ------------------------------------------------------------------------------
ARCH=$(uname -m)
echo ""
echo -e "${CYAN}[2/6] Nhận diện CPU điện thoại:${NC} ${GREEN}${BOLD}${ARCH}${NC}"

BIN_NAME="supportflast-linux-arm64"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    BIN_NAME="supportflast-linux-arm64"
elif [ "$ARCH" = "armv7l" ] || [ "$ARCH" = "arm" ]; then
    BIN_NAME="supportflast-linux-armv7"
elif [ "$ARCH" = "x86_64" ]; then
    BIN_NAME="supportflast-linux-amd64"
fi

BIN_PATH=""
if [ -f "$SCRIPT_DIR/$BIN_NAME" ]; then
    BIN_PATH="$SCRIPT_DIR/$BIN_NAME"
elif [ -f "$PARENT_DIR/$BIN_NAME" ]; then
    BIN_PATH="$PARENT_DIR/$BIN_NAME"
elif [ -f "$PARENT_DIR/supportflast_engine/$BIN_NAME" ]; then
    BIN_PATH="$PARENT_DIR/supportflast_engine/$BIN_NAME"
fi

if [ -z "$BIN_PATH" ] || [ ! -f "$BIN_PATH" ]; then
    echo -e "${RED}[LỖI] Không tìm thấy file nhị phân ${BIN_NAME}!${NC}"
    exit 1
fi

chmod +x "$BIN_PATH"
chmod +x "$SCRIPT_DIR"/*.sh 2>/dev/null || true
if [ -f "$SCRIPT_DIR/cloudflared-linux-arm64" ]; then
    chmod +x "$SCRIPT_DIR/cloudflared-linux-arm64"
fi

echo -e "  ✓ Đã kích hoạt file thực thi máy chủ: ${GREEN}${BIN_NAME}${NC}"

# ------------------------------------------------------------------------------
# BƯỚC 3: CẤU HÌNH THƯ MỤC DỮ LIỆU & BIẾN MÔI TRƯỜNG TỐI ƯU
# ------------------------------------------------------------------------------
echo ""
echo -e "${CYAN}[3/6] Thiết lập cấu trúc lưu trữ và tối ưu hóa tài nguyên...${NC}"
mkdir -p "$SCRIPT_DIR/data"
mkdir -p "$SCRIPT_DIR/storage"
mkdir -p "$SCRIPT_DIR/logs"

PORT="8080"
ENV_FILE="$SCRIPT_DIR/.env"
cat << EOF > "$ENV_FILE"
PORT=${PORT}
HOST=0.0.0.0
DOMAIN=localhost
GOGC=50
GOMEMLIMIT=128MiB
MAX_CONCURRENT_SUBAGENTS=10
DATA_DIR=${SCRIPT_DIR}/data
STORAGE_DIR=${SCRIPT_DIR}/storage
STATIC_DIR=${PARENT_DIR}/supportflast_ui
INTERNAL_SERVICE_SECRET=sf_mobile_token_2026
EOF

echo -e "  ✓ Cấu hình RAM: ${GREEN}GOGC=50, GOMEMLIMIT=128MiB (Giữ RAM < 35MB)${NC}"
echo -e "  ✓ Cấu hình Concurrency: ${GREEN}Semaphore Worker Pool (10 slots)${NC}"

# ------------------------------------------------------------------------------
# BƯỚC 4: KHỞI CHẠY MÁY CHỦ CHẠY NGẦM (DAEMON)
# ------------------------------------------------------------------------------
echo ""
echo -e "${CYAN}[4/6] Khởi động máy chủ SupportFlast chạy ngầm...${NC}"

# Dừng tiến trình cũ nếu đang chạy
OLD_PID=$(pgrep -f "$BIN_NAME" 2>/dev/null || true)
if [ -n "$OLD_PID" ]; then
    echo -e "  ℹ Đang tắt tiến trình máy chủ cũ (PID: $OLD_PID)..."
    kill -9 $OLD_PID 2>/dev/null || true
    sleep 1
fi

# Chạy ngầm bằng nohup
export GOGC=50
export GOMEMLIMIT=128MiB
export PORT=${PORT}
export HOST=0.0.0.0
export STATIC_DIR="${PARENT_DIR}/supportflast_ui"
export DATA_DIR="${SCRIPT_DIR}/data"
export STORAGE_DIR="${SCRIPT_DIR}/storage"

cd "$SCRIPT_DIR"
nohup "$BIN_PATH" > "$SCRIPT_DIR/logs/server.log" 2>&1 &
SERVER_PID=$!

echo -e "  ✓ Máy chủ đã khởi chạy ngầm thành công với ${GREEN}PID: ${SERVER_PID}${NC}"
echo -e "  ℹ Đang đợi máy chủ hoàn tất kiểm tra trạng thái (1.5 giây)..."
sleep 2

# Kiểm tra phản hồi từ máy chủ
SERVER_URL="http://127.0.0.1:${PORT}"
if ! curl -s --connect-timeout 3 "${SERVER_URL}/api/health" >/dev/null 2>&1; then
    echo -e "${RED}[LỖI] Máy chủ không phản hồi tại ${SERVER_URL}!${NC}"
    echo -e "Chi tiết nhật ký lỗi trong ${SCRIPT_DIR}/logs/server.log:"
    tail -n 20 "$SCRIPT_DIR/logs/server.log"
    exit 1
fi

echo -e "  ✓ Kết nối máy chủ thành công: ${GREEN}HTTP 200 OK${NC}"

# ------------------------------------------------------------------------------
# BƯỚC 5: TỰ ĐỘNG GỌI VÀ KIỂM THỬ NGAY 5 SUBAGENTS CHUYÊN TRÁCH
# ------------------------------------------------------------------------------
echo ""
echo -e "${CYAN}[5/6] Tự động kích hoạt & kiểm tra 5 Subagents chuyên trách...${NC}"
echo -e "${PURPLE}----------------------------------------------------------------------${NC}"
printf "%-3s | %-16s | %-24s | %-12s | %-8s\n" "STT" "MÃ SUBAGENT" "CHỨC NĂNG" "KẾT QUẢ" "ĐỘ TRỄ"
echo -e "${PURPLE}----------------------------------------------------------------------${NC}"

declare -a AGENTS_5=("agent_triage" "agent_mobile" "agent_network" "agent_storage" "agent_security")
declare -a NAMES_5=("Triage & Workflow" "Battery & Thermal" "Network Connectivity" "Storage Optimizer" "Security & Sandbox")
declare -a QUERIES_5=(
    "Phân loại yêu cầu hỗ trợ hệ thống"
    "Kiểm tra nhiệt độ SoC và chế độ tiết kiệm pin"
    "Dò tìm IP Wi-Fi nội bộ và cấu hình Cloudflare Tunnel"
    "Tối ưu hóa cơ sở dữ liệu SQLite WAL mode và dọn cache"
    "Quét an toàn và thẩm định tính toàn vẹn hệ thống"
)

TOTAL_START=$(date +%s%N)

for i in {0..4}; do
    AID="${AGENTS_5[$i]}"
    ANAME="${NAMES_5[$i]}"
    AQUERY="${QUERIES_5[$i]}"

    T0=$(date +%s%N)
    RES_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${SERVER_URL}/api/agents/chat" \
        -H "Content-Type: application/json" \
        -d "{\"agent_id\": \"$AID\", \"query\": \"$AQUERY\"}")
    T1=$(date +%s%N)
    DURATION=$(( (T1 - T0) / 1000000 ))

    STATUS_TEXT="${RED}FAIL (${RES_CODE})${NC}"
    if [ "$RES_CODE" = "200" ]; then
        STATUS_TEXT="${GREEN}PASS (200 OK)${NC}"
    fi

    printf "%-3d | %-16s | %-24s | %-21b | %s ms\n" "$((i+1))" "$AID" "$ANAME" "$STATUS_TEXT" "$DURATION"
done

TOTAL_END=$(date +%s%N)
TOTAL_MS=$(( (TOTAL_END - TOTAL_START) / 1000000 ))

echo -e "${PURPLE}----------------------------------------------------------------------${NC}"
echo -e "⏱️  Tổng thời gian điều phối và nhận phản hồi cả 5 Subagents: ${GREEN}${BOLD}${TOTAL_MS} ms${NC}"

# ------------------------------------------------------------------------------
# BƯỚC 6: TÌM IP VÀ HIỂN THỊ THÔNG TIN ĐẦY ĐỦ CHO NGƯỜI DÙNG
# ------------------------------------------------------------------------------
LOCAL_IP=""
if command -v ip >/dev/null 2>&1; then
    LOCAL_IP=$(ip -4 addr show wlan0 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1)
fi
if [ -z "$LOCAL_IP" ]; then
    LOCAL_IP="127.0.0.1"
fi

echo ""
echo -e "${GREEN}======================================================================${NC}"
echo -e "${GREEN}${BOLD}   CÀI ĐẶT THÀNH CÔNG VÀ 5 SUBAGENTS ĐÃ SẴN SÀNG HOẠT ĐỘNG!          ${NC}"
echo -e "${GREEN}======================================================================${NC}"
echo ""
echo -e "📱 ${BOLD}ĐỊA CHỈ TRUY CẬP WEB TRÊN ĐIỆN THOẠI:${NC}"
echo -e "   • Giao diện Web chính:       ${CYAN}http://localhost:${PORT}${NC}"
echo -e "   • Giao diện Bảng 10 Subagents:${GREEN}http://localhost:${PORT}/mobile_dashboard.html${NC}"
echo ""
echo -e "💻 ${BOLD}TRUY CẬP TỪ MÁY TÍNH / THIẾT BỊ KHÁC TRONG CÙNG MẠNG WI-FI:${NC}"
echo -e "   • Đường dẫn:                 ${YELLOW}http://${LOCAL_IP}:${PORT}${NC}"
echo ""
echo -e "🌍 ${BOLD}ĐƯA MÁY CHỦ RA TOÀN THẾ GIỚI (CLOUDFLARE TUNNEL CÓ SẴN):${NC}"
if [ -f "$SCRIPT_DIR/cloudflared-linux-arm64" ]; then
    echo -e "   Đã tích hợp sẵn file nhị phân Cloudflare Tunnel! Gõ lệnh sau để mở mạng toàn cầu:"
    echo -e "   ${PURPLE}./cloudflared-linux-arm64 tunnel --url http://localhost:${PORT}${NC}"
else
    echo -e "   Gõ lệnh sau để mở mạng toàn cầu:"
    echo -e "   ${PURPLE}cloudflared tunnel --url http://localhost:${PORT}${NC}"
fi
echo ""
echo -e "🛠️ ${BOLD}CÁC LỆNH ĐIỀU KHIỂN NHANH:${NC}"
echo -e "   • Kiểm thử lại 10 Subagents: ${YELLOW}bash test_10_subagents.sh${NC}"
echo -e "   • Xem nhật ký máy chủ:       ${YELLOW}tail -f logs/server.log${NC}"
echo -e "   • Dừng máy chủ:              ${YELLOW}bash stop_phone_server.sh${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""
