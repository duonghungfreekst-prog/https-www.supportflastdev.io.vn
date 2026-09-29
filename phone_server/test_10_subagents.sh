#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SCRIPT KIỂM THỬ ĐỒNG THỜI 10 SUBAGENTS TRÊN MÁY CHỦ DI ĐỘNG (BENCHMARK)
# ==============================================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
PURPLE='\033[0;35m'
NC='\033[0m'

SERVER_URL="http://127.0.0.1:8080"

echo -e "${CYAN}======================================================================${NC}"
echo -e "${GREEN}   SUPPORTFLAST MOBILE - KIỂM THỬ ĐIỀU PHỐI 10 SUBAGENTS ĐỒNG THỜI   ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo -e "Đang kiểm tra kết nối tới máy chủ tại: ${YELLOW}${SERVER_URL}${NC} ..."

# Kiểm tra máy chủ có đang chạy không
if ! curl -s --connect-timeout 2 "${SERVER_URL}/api/health" >/dev/null 2>&1; then
    echo -e "${RED}[LỖI] Máy chủ chưa khởi chạy tại ${SERVER_URL}!${NC}"
    echo -e "Vui lòng mở tab Termux khác và chạy: ${YELLOW}bash run_phone_server.sh${NC}"
    exit 1
fi

echo -e "${GREEN}[OK] Máy chủ đang phản hồi tốt! Bắt đầu kiểm tra 10 Subagents...${NC}"
echo ""

# 1. Lấy danh sách 10 Subagents
echo -e "${CYAN}--- BƯỚC 1: LẤY DANH SÁCH METADATA 10 SUBAGENTS ---${NC}"
AGENTS_JSON=$(curl -s "${SERVER_URL}/api/agents")
TOTAL_AGENTS=$(echo "$AGENTS_JSON" | grep -o '"total_agents":[0-9]*' | cut -d':' -f2)
echo -e "Tổng số Subagents đăng ký: ${GREEN}${TOTAL_AGENTS} Subagents${NC}"
echo ""

# Danh sách 10 Subagents và câu lệnh test đặc thù
declare -a AGENT_IDS=(
    "agent_triage"
    "agent_tech"
    "agent_infra"
    "agent_security"
    "agent_billing"
    "agent_mobile"
    "agent_network"
    "agent_storage"
    "agent_analytics"
    "agent_automation"
)

declare -a AGENT_NAMES=(
    "Triage & Workflow"
    "Packaging Diagnostics"
    "CDN & Infrastructure"
    "Security & Sandbox"
    "Billing & Licensing"
    "Mobile Battery Guardian"
    "Network Connectivity"
    "Storage & SQLite Optimizer"
    "ARM Metrics Inspector"
    "Task Scheduler & Watchdog"
)

declare -a AGENT_QUERIES=(
    "Phân loại hồ sơ kiểm duyệt ứng dụng"
    "Chẩn đoán lỗi đóng gói installer APK và MSIX"
    "Kiểm tra hạ tầng mạng CDN và HTTP Resume"
    "Quét mã độc và thẩm định chữ ký số gói cài đặt"
    "Cấp phép bản quyền và license key cho lập trình viên"
    "Kiểm tra nhiệt độ SoC và tối ưu pin chạy ngầm"
    "Dò tìm IP LAN và thiết lập Cloudflare Tunnel"
    "Tối ưu SQLite WAL mode và dọn rác bộ nhớ Flash"
    "Đo đạc chỉ số QPS và benchmark CPU ARM"
    "Lập lịch sao lưu tự động và kiểm tra watchdog"
)

echo -e "${CYAN}--- BƯỚC 2: GỬI ĐỒNG THỜI 10 YÊU CẦU TỚI 10 SUBAGENTS (CONCURRENCY) ---${NC}"
START_TOTAL=$(date +%s%N)
TMP_DIR=$(mktemp -d)

# Bắn đồng thời 10 request qua background subshells (&)
for i in {0..9}; do
    AID="${AGENT_IDS[$i]}"
    ANAME="${AGENT_NAMES[$i]}"
    AQUERY="${AGENT_QUERIES[$i]}"
    OUT_FILE="$TMP_DIR/res_${AID}.json"
    TIME_FILE="$TMP_DIR/time_${AID}.txt"

    (
        T_START=$(date +%s%N)
        HTTP_CODE=$(curl -s -o "$OUT_FILE" -w "%{http_code}" -X POST "${SERVER_URL}/api/agents/chat" \
            -H "Content-Type: application/json" \
            -d "{\"agent_id\": \"$AID\", \"query\": \"$AQUERY\"}")
        T_END=$(date +%s%N)
        DURATION=$(( (T_END - T_START) / 1000000 ))
        echo "$HTTP_CODE:$DURATION" > "$TIME_FILE"
    ) &
done

# Chờ toàn bộ 10 subagents xử lý xong
wait

END_TOTAL=$(date +%s%N)
TOTAL_DURATION=$(( (END_TOTAL - START_TOTAL) / 1000000 ))

echo -e "${GREEN}Đã nhận phản hồi từ toàn bộ 10 Subagents!${NC}"
echo ""

# Hiển thị bảng kết quả chi tiết
echo -e "${PURPLE}----------------------------------------------------------------------${NC}"
printf "%-3s | %-18s | %-26s | %-8s | %-10s\n" "STT" "MÃ SUBAGENT" "CHỨC NĂNG" "KẾT QUẢ" "ĐỘ TRỄ"
echo -e "${PURPLE}----------------------------------------------------------------------${NC}"

ALL_SUCCESS=true

for i in {0..9}; do
    AID="${AGENT_IDS[$i]}"
    ANAME="${AGENT_NAMES[$i]}"
    OUT_FILE="$TMP_DIR/res_${AID}.json"
    TIME_FILE="$TMP_DIR/time_${AID}.txt"

    if [ -f "$TIME_FILE" ]; then
        HTTP_CODE=$(cat "$TIME_FILE" | cut -d':' -f1)
        DURATION=$(cat "$TIME_FILE" | cut -d':' -f2)
    else
        HTTP_CODE="000"
        DURATION="N/A"
    fi

    STATUS_STR="${RED}FAIL (${HTTP_CODE})${NC}"
    if [ "$HTTP_CODE" = "200" ]; then
        STATUS_STR="${GREEN}PASS (200)${NC}"
    else
        ALL_SUCCESS=false
    fi

    printf "%-3d | %-18s | %-26s | %-17b | %s ms\n" "$((i+1))" "$AID" "$ANAME" "$STATUS_STR" "$DURATION"
done

echo -e "${PURPLE}----------------------------------------------------------------------${NC}"
echo ""
echo -e "⏱️  Tổng thời gian xử lý toàn bộ 10 Subagents đồng thời: ${GREEN}${TOTAL_DURATION} ms${NC}"

# Dọn dẹp file tạm
rm -rf "$TMP_DIR"

echo ""
if [ "$ALL_SUCCESS" = true ]; then
    echo -e "${GREEN}======================================================================${NC}"
    echo -e "${GREEN}   CHÚC MỪNG! TOÀN BỘ 10 SUBAGENTS ĐÃ HOẠT ĐỘNG HOÀN HẢO ĐỒNG THỜI!   ${NC}"
    echo -e "${GREEN}   Máy chủ trên điện thoại của bạn đáp ứng tiêu chuẩn xử lý cực hạn.  ${NC}"
    echo -e "${GREEN}======================================================================${NC}"
else
    echo -e "${YELLOW}[CẢNH BÁO] Một số subagents gặp lỗi phản hồi, vui lòng kiểm tra log.${NC}"
fi
echo ""
