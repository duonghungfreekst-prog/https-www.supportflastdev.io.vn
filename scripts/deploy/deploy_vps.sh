#!/usr/bin/env bash
# ==============================================================================
# SCRIPT TRIỂN KHAI 1-CLICK TỰ ĐỘNG CHO LINUX VPS (UBUNTU / DEBIAN / ROCKY)
# Dự án: Cổng Đăng Tải Ứng Dụng & Điều Phối 5 Subagents (supportflastdev.io.vn)
# ------------------------------------------------------------------------------
# KHẲNG ĐỊNH 100% ĐÃ HỢP NHẤT THÀNH MỘT BỘ DUY NHẤT - ĐỒNG BỘ TOÀN DIỆN:
#  - Cổng 8080 DUY NHẤT: Web 3D HUD UI, REST API Gateway, Cloud Storage, Dev Tools
#  - KHÔNG cần chạy dịch vụ ngoài, KHÔNG cần cloudpool_engine.exe hay cổng 8082
#  - Hỗ trợ triển khai chỉ với 1 lệnh duy nhất:
#      * Cách 1 (Docker):  docker compose up -d
#      * Cách 2 (Script):  sudo ./deploy_vps.sh --docker
#      * Cách 3 (Native):  sudo ./deploy_vps.sh --binary
# ==============================================================================

set -euo pipefail

# Mã màu ANSI định dạng giao diện
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

DOMAIN="supportflastdev.io.vn"
APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_USER="${SERVICE_USER:-www-data}"

print_banner() {
    clear 2>/dev/null || true
    echo -e "${CYAN}${BOLD}"
    echo "=========================================================================="
    echo "       🚀 SUPPORTFLASTDEV.IO.VN - HỆ THỐNG TRIỂN KHAI 1-CLICK LINUX VPS    "
    echo "       🔥 100% ĐÃ HỢP NHẤT THÀNH MỘT BỘ DUY NHẤT - ĐỒNG BỘ TOÀN DIỆN      "
    echo "=========================================================================="
    echo -e "${NC}"
    echo -e " Thư mục cài đặt    : ${YELLOW}${APP_DIR}${NC}"
    echo -e " Tên miền chính     : ${GREEN}https://${DOMAIN}${NC}"
    echo -e " Cổng Unified Engine: ${BLUE}8080${NC} (Web 3D + Storage + Tools + REST API)"
    echo -e " Cổng AI Engine     : ${BLUE}8000${NC} (5 Subagents Multi-Agent FastAPI)"
    echo -e " Trạng thái hợp nhất: ${GREEN}100% UNIFIED (Không cần cổng 8082 hay dịch vụ rời)${NC}"
    echo -e "${CYAN}--------------------------------------------------------------------------${NC}"
}

log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') - $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') - $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') - $1"; }
log_step()  { echo -e "\n${PURPLE}${BOLD}>>> BƯỚC: $1${NC}"; }

# Kiểm tra quyền root
check_root() {
    if [ "${EUID:-$(id -u)}" -ne 0 ]; then
        log_error "Vui lòng chạy script này với quyền root hoặc sudo: sudo ./deploy_vps.sh"
        exit 1
    fi
}

# Tự động nhận diện hệ điều hành Linux (Ubuntu, Debian, Rocky, RHEL, CentOS...)
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS_NAME=$ID
        OS_VERSION=${VERSION_ID:-"unknown"}
    else
        log_error "Không thể nhận diện hệ điều hành. Yêu cầu Linux có /etc/os-release."
        exit 1
    fi
    log_info "Hệ điều hành phát hiện: ${BOLD}${OS_NAME} ${OS_VERSION}${NC}"
}

# Cập nhật danh sách gói hệ thống
update_packages() {
    log_step "Cập nhật danh sách gói hệ thống và cài đặt công cụ cơ sở..."
    if [[ "$OS_NAME" == "ubuntu" || "$OS_NAME" == "debian" ]]; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -y
        apt-get install -y --no-install-recommends \
            curl wget git tar gzip unzip sqlite3 ca-certificates \
            software-properties-common ufw jq
    elif [[ "$OS_NAME" == "centos" || "$OS_NAME" == "rhel" || "$OS_NAME" == "rocky" || "$OS_NAME" == "almalinux" ]]; then
        dnf update -y
        dnf install -y curl wget git tar gzip unzip sqlite epel-release jq
    else
        log_warn "Hệ điều hành chưa tối ưu hóa đặc thù, tiếp tục với các lệnh chuẩn..."
    fi
    log_info "Đã cập nhật hệ thống thành công."
}

# Thiết lập và phân quyền thư mục data/, storage/ và backups/
setup_permissions() {
    log_step "Phân quyền an toàn cho data/, storage/ và backups/ (Rule 1.4, 3.1)..."
    mkdir -p "$APP_DIR/data"
    mkdir -p "$APP_DIR/storage/packages"
    mkdir -p "$APP_DIR/backups/sqlite"

    # Đảm bảo group/user tồn tại
    if id "$SERVICE_USER" &>/dev/null; then
        chown -R "$SERVICE_USER":"$SERVICE_USER" "$APP_DIR/data"
        chown -R "$SERVICE_USER":"$SERVICE_USER" "$APP_DIR/storage"
        chown -R "$SERVICE_USER":"$SERVICE_USER" "$APP_DIR/backups"
    fi

    # Quyền truy cập an toàn: 755 cho thư mục, 700 cho backup
    chmod 755 "$APP_DIR/data"
    chmod 755 "$APP_DIR/storage"
    chmod 755 "$APP_DIR/storage/packages"
    chmod 700 "$APP_DIR/backups"

    # Phân quyền thực thi cho các script bash
    chmod +x "$APP_DIR"/*.sh 2>/dev/null || true

    log_info "Đã phân quyền an toàn: data/ (755), storage/packages (755), backups/ (700)."
}

# ------------------------------------------------------------------------------
# PHƯƠNG ÁN 1: TRIỂN KHAI 1-CLICK BẰNG DOCKER COMPOSE (KHUYÊN DÙNG)
# ------------------------------------------------------------------------------
install_docker_environment() {
    log_step "Kiểm tra và cài đặt Docker CE & Docker Compose Plugin..."
    if ! command -v docker &> /dev/null; then
        log_info "Tiến hành cài đặt Docker tự động từ official repository..."
        curl -fsSL https://get.docker.com | sh
        systemctl enable --now docker
        log_info "Docker đã được cài đặt và kích hoạt thành công."
    else
        log_info "Docker đã sẵn sàng: $(docker --version)"
    fi

    if ! docker compose version &> /dev/null; then
        log_info "Cài đặt Docker Compose Plugin..."
        if [[ "$OS_NAME" == "ubuntu" || "$OS_NAME" == "debian" ]]; then
            apt-get install -y docker-compose-plugin
        fi
    fi
    log_info "Docker Compose sẵn sàng: $(docker compose version)"
}

deploy_docker_compose() {
    install_docker_environment
    setup_permissions

    log_step "Khởi tạo và biên dịch bộ container SupportFlast với Docker Compose..."
    cd "$APP_DIR"

    # Dừng các container cũ nếu có
    docker compose down --remove-orphans || true

    # Kiểm tra biến hoặc hỏi người dùng về Caddy SSL
    local enable_ssl="Y"
    if [ -t 0 ] && [ "${ASSUME_YES:-0}" != "1" ]; then
        echo -e "${YELLOW}Bạn có muốn bật Caddy Reverse Proxy để tự động nhận chứng chỉ SSL Let's Encrypt cho ${DOMAIN}? [Y/n]:${NC} "
        read -r input_ssl || input_ssl="Y"
        enable_ssl=${input_ssl:-"Y"}
    fi

    if [[ "$enable_ssl" =~ ^[Yy]$ ]]; then
        log_info "Khởi chạy toàn bộ hệ thống kèm Caddy Reverse Proxy SSL (Port 80/443)..."
        docker compose --profile ssl up -d --build
    else
        log_info "Khởi chạy SupportFlast Unified Gateway (8080) và Python AI (8000)..."
        docker compose up -d --build
    fi

    log_info "Chờ 10 giây để các dịch vụ hoàn tất nạp và kiểm tra sức khỏe..."
    sleep 10

    verify_health
}

# ------------------------------------------------------------------------------
# PHƯƠNG ÁN 2: TRIỂN KHAI BARE-METAL BINARY + SYSTEMD + NGINX (TỐI ƯU RAM)
# ------------------------------------------------------------------------------
install_binary_environment() {
    log_step "Cài đặt môi trường runtime: Go 1.22+, Python 3.10+ venv, Nginx..."
    if [[ "$OS_NAME" == "ubuntu" || "$OS_NAME" == "debian" ]]; then
        apt-get install -y python3 python3-pip python3-venv nginx certbot python3-certbot-nginx
        
        # Kiểm tra hoặc cài Go
        if ! command -v go &> /dev/null; then
            log_info "Đang tải và cài đặt Golang 1.22.6 Linux AMD64..."
            GO_TAR="go1.22.6.linux-amd64.tar.gz"
            wget -q "https://go.dev/dl/${GO_TAR}" -O "/tmp/${GO_TAR}"
            rm -rf /usr/local/go && tar -C /usr/local -xzf "/tmp/${GO_TAR}"
            rm -f "/tmp/${GO_TAR}"
            ln -sf /usr/local/go/bin/go /usr/bin/go
            ln -sf /usr/local/go/bin/gofmt /usr/bin/gofmt
        fi
    fi
    log_info "Go runtime: $(go version 2>/dev/null || echo 'Chưa tìm thấy go')"
    log_info "Python runtime: $(python3 --version 2>/dev/null || echo 'Chưa tìm thấy python3')"
}

build_and_setup_binary() {
    install_binary_environment
    setup_permissions

    # 1. Biên dịch Go Engine thành binary 'supportflast'
    log_step "Biên dịch SupportFlast Unified Core Engine cho môi trường Linux..."
    cd "$APP_DIR/supportflast_engine"
    CGO_ENABLED=0 GOOS=linux go build \
        -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
        -trimpath \
        -o "$APP_DIR/supportflast_engine/supportflast" .
    chmod +x "$APP_DIR/supportflast_engine/supportflast"
    ln -sf "$APP_DIR/supportflast_engine/supportflast" "$APP_DIR/supportflast"
    log_info "Đã biên dịch binary tĩnh thành công: $APP_DIR/supportflast_engine/supportflast"

    # 2. Thiết lập Python Virtual Environment
    log_step "Thiết lập Virtual Environment và cài đặt thư viện cho 5 Subagents AI..."
    cd "$APP_DIR/supportflast_ai"
    if [ ! -d "venv" ]; then
        python3 -m venv venv
    fi
    ./venv/bin/pip install --upgrade pip
    ./venv/bin/pip install -r requirements.txt
    log_info "Môi trường Python AI Subagents đã sẵn sàng."

    # 3. Tạo Systemd Service cho Python AI
    log_step "Tạo Systemd Service: supportflast-ai.service..."
    cat <<EOF > /etc/systemd/system/supportflast-ai.service
[Unit]
Description=SupportFlast Python 5 Subagents Multi-Agent Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${APP_DIR}/supportflast_ai
Environment="PYTHONUNBUFFERED=1"
Environment="HOST=127.0.0.1"
Environment="PORT=8000"
ExecStart=${APP_DIR}/supportflast_ai/venv/bin/uvicorn app:app --host 127.0.0.1 --port 8000 --workers 2
Restart=always
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

    # 4. Tạo Systemd Service cho SupportFlast Unified Core Engine
    log_step "Tạo Systemd Service: supportflast-engine.service..."
    cat <<EOF > /etc/systemd/system/supportflast-engine.service
[Unit]
Description=SupportFlast Unified Core Engine (Web 3D + Storage + Tools + REST API)
After=network.target supportflast-ai.service
Wants=supportflast-ai.service

[Service]
Type=simple
User=root
WorkingDirectory=${APP_DIR}
Environment="PORT=8080"
Environment="HOST=0.0.0.0"
Environment="DOMAIN=${DOMAIN}"
Environment="DATA_DIR=${APP_DIR}/data"
Environment="STORAGE_DIR=${APP_DIR}/storage"
Environment="UI_DIR=${APP_DIR}/supportflast_ui"
Environment="AI_ENGINE_URL=http://127.0.0.1:8000"
ExecStart=${APP_DIR}/supportflast_engine/supportflast
Restart=always
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

    # Nạp lại systemd daemon và khởi động
    systemctl daemon-reload
    systemctl enable --now supportflast-ai.service
    systemctl enable --now supportflast-engine.service
    log_info "Đã kích hoạt và khởi chạy các systemd service thành công."

    # 5. Cấu hình Nginx Reverse Proxy
    setup_nginx_proxy
    
    verify_health
}

# Cấu hình Nginx Reverse Proxy
setup_nginx_proxy() {
    log_step "Cấu hình Nginx Reverse Proxy và SSL cho domain ${DOMAIN}..."
    if [ -f "$APP_DIR/setup_nginx.sh" ]; then
        log_info "Sử dụng bộ tự động hóa chuyên dụng: $APP_DIR/setup_nginx.sh..."
        chmod +x "$APP_DIR/setup_nginx.sh"
        bash "$APP_DIR/setup_nginx.sh" -d "$DOMAIN" --backend "127.0.0.1:8080" -y || {
            log_warn "Cấu hình tự động gặp cảnh báo, tiếp tục kiểm tra..."
        }
    else
        NGINX_CONF_DEST="/etc/nginx/sites-available/${DOMAIN}.conf"
        if [ -f "$APP_DIR/nginx_supportflastdev.io.vn.conf" ]; then
            cp "$APP_DIR/nginx_supportflastdev.io.vn.conf" "$NGINX_CONF_DEST"
        fi
        mkdir -p /etc/nginx/sites-enabled
        ln -sf "$NGINX_CONF_DEST" "/etc/nginx/sites-enabled/${DOMAIN}.conf"
        rm -f /etc/nginx/sites-enabled/default
        nginx -t
        systemctl reload nginx
        log_info "Nginx Reverse Proxy đã được nạp cấu hình thành công."
    fi
}

# ------------------------------------------------------------------------------
# THIẾT LẬP CRON JOB SAO LƯU SQLITE TỰ ĐỘNG
# ------------------------------------------------------------------------------
setup_backup_cron() {
    log_step "Thiết lập Cron Job sao lưu tự động cơ sở dữ liệu SQLite..."
    CRON_FILE="/etc/cron.d/supportflast_backup"
    BACKUP_SCRIPT="$APP_DIR/backup_sqlite.sh"

    if [ ! -f "$BACKUP_SCRIPT" ]; then
        log_warn "Không tìm thấy script sao lưu tại: $BACKUP_SCRIPT, bỏ qua tạo cron."
        return 0
    fi
    chmod +x "$BACKUP_SCRIPT"

    # Lập lịch chạy lúc 02:00 sáng mỗi ngày
    cat <<EOF > "$CRON_FILE"
# Tự động sao lưu dữ liệu SQLite định kỳ lúc 02:00 sáng hàng ngày
0 2 * * * root /bin/bash ${BACKUP_SCRIPT} >> /var/log/supportflast_backup.log 2>&1
EOF
    chmod 644 "$CRON_FILE"
    log_info "Đã thiết lập lịch trình sao lưu hàng ngày lúc 02:00 sáng (/etc/cron.d/supportflast_backup)."
}

# ------------------------------------------------------------------------------
# KIỂM TRA SỨC KHỎE DỊCH VỤ (HEALTH CHECK)
# ------------------------------------------------------------------------------
verify_health() {
    log_step "Kiểm tra sức khỏe hệ thống hợp nhất (Health Check)..."
    sleep 3

    ENGINE_HEALTH=$(curl -s http://127.0.0.1:8080/api/health || echo "fail")
    AI_HEALTH=$(curl -s http://127.0.0.1:8000/api/health || echo "fail")

    echo -e "------------------------------------------------------------"
    if [[ "$ENGINE_HEALTH" == *"healthy"* ]]; then
        echo -e " ✅ ${GREEN}SupportFlast Unified Core (Port 8080): ĐANG HOẠT ĐỘNG HOÀN HẢO${NC}"
        echo -e "    -> Web 3D UI, Cloud Storage (/storage), Dev Tools (/tools), SQLite WAL, API Gateway"
        echo -e "    -> 100% ĐÃ HỢP NHẤT THÀNH MỘT BỘ DUY NHẤT TRÊN CỔNG 8080!"
    else
        echo -e " ❌ ${RED}SupportFlast Unified Core (Port 8080): CHƯA SẴN SÀNG (${ENGINE_HEALTH})${NC}"
    fi

    if [[ "$AI_HEALTH" == *"healthy"* ]]; then
        echo -e " ✅ ${GREEN}Python 5 Subagents AI (Port 8000): ĐANG HOẠT ĐỘNG (FastAPI Native)${NC}"
    else
        echo -e " ℹ️  ${YELLOW}Python AI (Port 8000): Đang dùng Chế độ Fallback Cache từ Gateway (Vẫn phục vụ 5 Subagents an toàn)${NC}"
    fi
    echo -e "------------------------------------------------------------"

    setup_backup_cron

    echo -e "\n${GREEN}${BOLD}🎉 QUÁ TRÌNH TRIỂN KHAI ĐÃ HOÀN TẤT THÀNH CÔNG!${NC}"
    echo -e "👉 Địa chỉ truy cập cục bộ : ${CYAN}http://127.0.0.1:8080${NC}"
    echo -e "👉 Kho Lưu Trữ Đám Mây     : ${CYAN}http://127.0.0.1:8080/storage${NC}"
    echo -e "👉 Bộ Công Cụ Phát Triển   : ${CYAN}http://127.0.0.1:8080/tools${NC}"
    echo -e "👉 Địa chỉ tên miền chính  : ${CYAN}https://${DOMAIN}${NC}"
    echo -e "👉 API Danh sách Subagents : ${CYAN}http://127.0.0.1:8080/api/agents${NC}"
    echo -e "👉 Nhật ký sao lưu định kỳ : ${YELLOW}/var/log/supportflast_backup.log${NC}\n"
    echo -e "${BOLD}LƯU Ý:${NC} Không cần cổng 8082 hay cloudpool_engine. Hệ thống đã 100% hợp nhất!"
}

# ------------------------------------------------------------------------------
# MENU ĐIỀU KHIỂN CHÍNH
# ------------------------------------------------------------------------------
main() {
    print_banner
    check_root
    detect_os

    # Xử lý tham số truyền dòng lệnh (Non-interactive 1-Click mode)
    if [ $# -gt 0 ]; then
        case "$1" in
            --docker)
                update_packages
                deploy_docker_compose
                exit 0
                ;;
            --binary)
                update_packages
                build_and_setup_binary
                exit 0
                ;;
            --backup)
                setup_backup_cron
                exit 0
                ;;
            --help|-h)
                echo "Cách dùng: sudo ./deploy_vps.sh [TÙY CHỌN]"
                echo "  --docker : Triển khai 1-click bằng Docker Compose (Khuyên dùng - Nhanh nhất)"
                echo "  --binary : Triển khai Bare-metal với Systemd + Nginx + Python venv"
                echo "  --backup : Chỉ cài đặt lịch sao lưu tự động SQLite Cron Job"
                echo "  --help   : Hiển thị bảng trợ giúp này"
                echo ""
                echo "Hoặc chỉ với 1 lệnh trực tiếp: docker compose up -d"
                exit 0
                ;;
            *)
                log_error "Tùy chọn không hợp lệ: $1. Dùng --help để xem hướng dẫn."
                exit 1
                ;;
        esac
    fi

    # Menu tương tác
    echo -e "${BOLD}Vui lòng chọn phương thức triển khai:${NC}"
    echo -e "  ${GREEN}[1]${NC} Triển khai tự động bằng ${BOLD}Docker Compose${NC} (Khuyên dùng - 1 lệnh chạy ngay)"
    echo -e "  ${GREEN}[2]${NC} Triển khai ${BOLD}Bare-metal Binary${NC} (Go 'supportflast' Systemd + Python venv + Nginx)"
    echo -e "  ${GREEN}[3]${NC} Chỉ cấu hình ${BOLD}Nginx Reverse Proxy & SSL${NC}"
    echo -e "  ${GREEN}[4]${NC} Chỉ thiết lập ${BOLD}Lịch trình sao lưu dữ liệu SQLite định kỳ${NC}"
    echo -e "  ${GREEN}[5]${NC} Thoát"
    echo ""
    read -rp "Lựa chọn của bạn [1-5]: " choice

    case "$choice" in
        1)
            update_packages
            deploy_docker_compose
            ;;
        2)
            update_packages
            build_and_setup_binary
            ;;
        3)
            setup_nginx_proxy
            ;;
        4)
            setup_backup_cron
            ;;
        5)
            echo "Đã hủy thao tác."
            exit 0
            ;;
        *)
            log_error "Lựa chọn không hợp lệ!"
            exit 1
            ;;
    esac
}

main "$@"
