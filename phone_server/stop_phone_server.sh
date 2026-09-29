#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SUPPORTFLAST MOBILE SERVER - SCRIPT DỪNG MÁY CHỦ AN TOÀN TRÊN ĐIỆN THOẠI
# ==============================================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${CYAN}Đang tìm các tiến trình máy chủ SupportFlast đang chạy...${NC}"

PIDS=$(pgrep -f "supportflast-linux" 2>/dev/null || true)

if [ -z "$PIDS" ]; then
    echo -e "${YELLOW}Không có tiến trình máy chủ nào đang hoạt động.${NC}"
    exit 0
fi

for PID in $PIDS; do
    echo -e "Đang dừng tiến trình PID: ${YELLOW}$PID${NC}..."
    kill $PID 2>/dev/null || kill -9 $PID 2>/dev/null || true
done

sleep 1
REMAINING=$(pgrep -f "supportflast-linux" 2>/dev/null || true)
if [ -z "$REMAINING" ]; then
    echo -e "${GREEN}✓ Đã dừng toàn bộ máy chủ SupportFlast an toàn!${NC}"
else
    echo -e "${RED}Vẫn còn tiến trình $REMAINING, đang cưỡng chế dừng...${NC}"
    kill -9 $REMAINING 2>/dev/null || true
    echo -e "${GREEN}✓ Đã cưỡng chế dừng thành công!${NC}"
fi
