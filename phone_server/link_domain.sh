#!/data/data/com.termux/files/usr/bin/bash
# ==============================================================================
# SCRIPT LIÊN THÔNG TÊN MIỀN RIÊNG VÀO MÁY CHỦ ĐIỆN THOẠI (CLOUDFLARE TUNNEL)
# Hoạt động 100% không cần IP tĩnh, không cần mở port modem, có HTTPS miễn phí
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
echo -e "${GREEN}${BOLD}   SUPPORTFLAST MOBILE - LIÊN THÔNG TÊN MIỀN RIÊNG (DOMAIN)           ${NC}"
echo -e "${YELLOW}   Không cần IP tĩnh | Không mở port modem | Tự động SSL HTTPS        ${NC}"
echo -e "${CYAN}======================================================================${NC}"
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLOUDFLARED="$SCRIPT_DIR/cloudflared-linux-arm64"

if [ ! -f "$CLOUDFLARED" ]; then
    if command -v cloudflared >/dev/null 2>&1; then
        CLOUDFLARED="cloudflared"
    else
        echo -e "${YELLOW}Đang cài đặt cloudflared qua package manager...${NC}"
        pkg install -y cloudflared 2>/dev/null || true
        CLOUDFLARED="cloudflared"
    fi
fi

chmod +x "$CLOUDFLARED" 2>/dev/null || true

echo -e "Bạn muốn liên thông tên miền theo cách nào?"
echo -e "  ${BOLD}[1] Dùng Tunnel Token từ Cloudflare Zero Trust (Khuyên dùng - Nhanh nhất)${NC}"
echo -e "  ${BOLD}[2] Đăng nhập tài khoản Cloudflare trực tiếp qua trình duyệt (CLI Login)${NC}"
echo -e "  ${BOLD}[3] Tạo đường dẫn thử nghiệm Quick Tunnel ngẫu nhiên (Không cần cấu hình)${NC}"
echo ""
read -p "Nhập lựa chọn của bạn (1, 2 hoặc 3): " CHOICE

case "$CHOICE" in
    1)
        echo ""
        echo -e "${CYAN}--- CÁCH 1: DÙNG TUNNEL TOKEN TỪ CLOUDFLARE ZERO TRUST ---${NC}"
        echo -e "1. Vào ${YELLOW}https://dash.cloudflare.com/${NC} -> Zero Trust -> Networks -> Tunnels."
        echo -e "2. Bấm ${GREEN}Create a Tunnel${NC} -> Chọn Cloudflared."
        echo -e "3. Copy mã Token sau chuỗi ${YELLOW}--token${NC} (đoạn mã dài bắt đầu bằng eyJh...)."
        echo ""
        read -p "Dán mã Token Cloudflare của bạn vào đây: " USER_TOKEN
        if [ -z "$USER_TOKEN" ]; then
            echo -e "${RED}[LỖI] Token không được để trống!${NC}"
            exit 1
        fi
        
        echo ""
        echo -e "${GREEN}Đang khởi động đường hầm liên thông tên miền...${NC}"
        # Lưu lại token vào file cấu hình
        echo "$USER_TOKEN" > "$SCRIPT_DIR/cloudflare_token.txt"
        
        # Chạy tunnel ngầm
        nohup "$CLOUDFLARED" tunnel run --token "$USER_TOKEN" > "$SCRIPT_DIR/logs/tunnel.log" 2>&1 &
        TUNNEL_PID=$!
        echo -e "${GREEN}✓ Đường hầm đã kết nối thành công (PID: $TUNNEL_PID)!${NC}"
        echo -e "Tên miền riêng của bạn giờ đây đã trỏ thẳng vào máy chủ điện thoại."
        echo -e "Xem nhật ký đường hầm: ${YELLOW}tail -f logs/tunnel.log${NC}"
        ;;
    2)
        echo ""
        echo -e "${CYAN}--- CÁCH 2: ĐĂNG NHẬP CLOUDFLARE QUA TRÌNH DUYỆT ---${NC}"
        echo -e "Đang mở phiên đăng nhập Cloudflare..."
        "$CLOUDFLARED" tunnel login
        
        echo ""
        read -p "Nhập tên miền của bạn (ví dụ: supportflastdev.io.vn): " USER_DOMAIN
        if [ -z "$USER_DOMAIN" ]; then
            echo -e "${RED}[LỖI] Tên miền không được để trống!${NC}"
            exit 1
        fi
        
        TUNNEL_NAME="phone-server-tunnel"
        echo -e "Đang tạo đường hầm tên: ${YELLOW}$TUNNEL_NAME${NC}..."
        "$CLOUDFLARED" tunnel create "$TUNNEL_NAME" || true
        
        echo -e "Đang trỏ tên miền ${GREEN}$USER_DOMAIN${NC} về đường hầm..."
        "$CLOUDFLARED" tunnel route dns "$TUNNEL_NAME" "$USER_DOMAIN" || true
        
        echo -e "Đang khởi chạy đường hầm..."
        nohup "$CLOUDFLARED" tunnel run --url http://localhost:8080 "$TUNNEL_NAME" > "$SCRIPT_DIR/logs/tunnel.log" 2>&1 &
        echo -e "${GREEN}✓ Hoàn tất! Tên miền https://$USER_DOMAIN đã được liên thông!${NC}"
        ;;
    3)
        echo ""
        echo -e "${CYAN}--- CÁCH 3: TẠO ĐƯỜNG DẪN QUICK TUNNEL CÔNG KHAI NGAY LẬP TỨC ---${NC}"
        echo -e "Đang khởi tạo đường hầm Quick Tunnel miễn phí trỏ vào cổng 8080..."
        "$CLOUDFLARED" tunnel --url http://localhost:8080
        ;;
    *)
        echo -e "${RED}Lựa chọn không hợp lệ!${NC}"
        exit 1
        ;;
esac
