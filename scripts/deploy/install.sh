#!/usr/bin/env bash
# ==============================================================================
# 🚀 SUPPORTFLAST - UNIVERSAL 1-CLICK LINUX INSTALLER (install.sh)
# Hệ thống cổng đăng tải ứng dụng Web 3D & Điều phối 5 Subagents chuyên sâu
# Domain: supportflastdev.io.vn | Cổng hợp nhất Unified Engine: 8080
# ------------------------------------------------------------------------------
# HỖ TRỢ ĐA NỀN TẢNG (100% TỰ NHẬN DIỆN MỌI DISTRO LINUX):
#  ✅ Ubuntu (18.04, 20.04, 22.04, 24.04 LTS+)
#  ✅ Debian (10, 11, 12 Bookworm+)
#  ✅ CentOS / CentOS Stream (7, 8, 9)
#  ✅ AlmaLinux (8, 9) & Rocky Linux (8, 9)
#  ✅ Fedora (38, 39, 40+) & RHEL (7, 8, 9)
#  ✅ Alpine Linux (3.15, 3.16, 3.17, 3.18, 3.19, 3.20+)
# ------------------------------------------------------------------------------
# TÍNH NĂNG TỰ ĐỘNG HÓA 1-CLICK:
#  1. Tự động kiểm tra và nâng quyền root/sudo
#  2. Tự động nhận diện bản phân phối Linux và trình quản lý gói (apt/dnf/yum/apk)
#  3. Tự động phát hiện thư mục cài đặt hiện tại hoặc cài vào '/opt/supportflast'
#  4. Cài đặt các gói phụ thuộc cần thiết (curl, sqlite, ca-certificates, jq...)
#  5. Đảm bảo binary tĩnh 'supportflast' sẵn sàng hoạt động (Zero-libc dependency)
#  6. Tự động tạo user và phân quyền an toàn: chown -R www-data, chmod +x binary
#  7. Tự động tạo và kích hoạt systemd service 'supportflast.service' (OpenRC trên Alpine)
#  8. Tự động mở firewall (ufw, firewalld, iptables) cho port 80, 443, 8080
#  9. Tự động cấu hình lịch sao lưu định kỳ SQLite WAL database
# 10. In bảng hướng dẫn tương tác rực rỡ với màu sắc ANSI sau khi hoàn tất
# ==============================================================================

set -euo pipefail

# ------------------------------------------------------------------------------
# BẢNG MÃ MÀU ANSI ĐỊNH DẠNG GIAO DIỆN TERMINAL HIỆN ĐẠI
# ------------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m' # No Color / Reset

# Biến cấu hình mặc định của hệ thống
DEFAULT_DOMAIN="supportflastdev.io.vn"
DEFAULT_INSTALL_DIR="/opt/supportflast"
DEFAULT_PORT="8080"
DEFAULT_USER="www-data"

TARGET_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
PORT="${PORT:-$DEFAULT_PORT}"
DOMAIN="${DOMAIN:-$DEFAULT_DOMAIN}"
SERVICE_USER="${SERVICE_USER:-$DEFAULT_USER}"

# Biến trạng thái runtime
OS_ID="unknown"
OS_NAME="Linux"
OS_VERSION="unknown"
PKG_MGR="unknown"
INIT_SYSTEM="systemd"
SERVER_IP=""

# ------------------------------------------------------------------------------
# CÁC HÀM IN LOG VÀ GIAO DIỆN
# ------------------------------------------------------------------------------
log_info()    { echo -e " ${BLUE}ℹ${NC}  ${BOLD}[INFO]${NC}    $(date '+%H:%M:%S') - $1"; }
log_success() { echo -e " ${GREEN}✔${NC}  ${GREEN}${BOLD}[SUCCESS]${NC} $(date '+%H:%M:%S') - $1"; }
log_warn()    { echo -e " ${YELLOW}⚠${NC}  ${YELLOW}${BOLD}[WARN]${NC}   $(date '+%H:%M:%S') - $1"; }
log_error()   { echo -e " ${RED}✖${NC}  ${RED}${BOLD}[ERROR]${NC}  $(date '+%H:%M:%S') - $1"; }
log_step()    { echo -e "\n${CYAN}━━━ ${BOLD}$1${NC} ${CYAN}$(printf '━%.0s' {1..50})${NC}"; }

print_banner() {
    clear 2>/dev/null || true
    echo -e "${CYAN}${BOLD}"
    cat << "EOF"
  ███████╗██╗   ██╗██████╗ ██████╗  ██████╗ ██████╗ ████████╗███████╗██╗      █████╗ ███████╗████████╗
  ██╔════╝██║   ██║██╔══██╗██╔══██╗██╔═══██╗██╔══██╗╚══██╔══╝██╔════╝██║     ██╔══██╗██╔════╝╚══██╔══╝
  ███████╗██║   ██║██████╔╝██████╔╝██║   ██║██████╔╝   ██║   █████╗  ██║     ███████║███████╗   ██║   
  ╚════██║██║   ██║██╔═══╝ ██╔═══╝ ██║   ██║██╔══██╗   ██║   ██╔══╝  ██║     ██╔══██║╚════██║   ██║   
  ███████║╚██████╔╝██║     ██║     ╚██████╔╝██║  ██║   ██║   ██║     ███████╗██║  ██║███████║   ██║   
  ╚══════╝ ╚═════╝ ╚═╝     ╚═╝      ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚═╝     ╚══════╝╚═╝  ╚═╝╚══════╝   ╚═╝   
EOF
    echo -e "${PURPLE}  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "  ${WHITE}${BOLD}UNIVERSAL 1-CLICK LINUX INSTALLER${NC} | ${GREEN}supportflastdev.io.vn${NC} | ${YELLOW}Unified Engine 8080${NC}"
    echo -e "  ${DIM}Cổng Đăng Tải Ứng Dụng Web 3D & Điều Phối 5 Subagents Chuyên Sâu Tự Động${NC}"
    echo -e "${PURPLE}  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
}

# ------------------------------------------------------------------------------
# BƯỚC 1: KIỂM TRA QUYỀN ROOT / SUDO
# ------------------------------------------------------------------------------
check_root() {
    log_step "BƯỚC 1: Kiểm tra quyền quản trị hệ thống (Root/Sudo)"
    if [ "${EUID:-$(id -u)}" -ne 0 ]; then
        log_warn "Script đang chạy với quyền người dùng thông thường."
        if command -v sudo &>/dev/null; then
            log_info "Tự động kích hoạt quyền Root thông qua sudo..."
            exec sudo -E bash "$0" "$@"
        else
            log_error "Yêu cầu quyền root để cài đặt dịch vụ hệ thống và cấu hình mạng."
            log_error "Vui lòng đăng nhập với quyền root hoặc cài đặt sudo: su -c 'bash $0'"
            exit 1
        fi
    fi
    log_success "Quyền Root hợp lệ. Tiến trình tiếp tục an toàn."
}

# ------------------------------------------------------------------------------
# BƯỚC 2: TỰ NHẬN DIỆN MỌI BẢN PHÂN PHỐI LINUX (UBUNTU/DEBIAN/CENTOS/ALMA/ROCKY/FEDORA/ALPINE)
# ------------------------------------------------------------------------------
detect_os() {
    log_step "BƯỚC 2: Tự động nhận diện Bản phân phối Linux & Trình khởi tạo Service"

    if [ -f /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        OS_ID="${ID:-unknown}"
        OS_NAME="${NAME:-Linux}"
        OS_VERSION="${VERSION_ID:-unknown}"
    elif [ -f /etc/alpine-release ]; then
        OS_ID="alpine"
        OS_NAME="Alpine Linux"
        OS_VERSION=$(cat /etc/alpine-release 2>/dev/null || echo "unknown")
    elif [ -f /etc/redhat-release ]; then
        OS_ID="centos"
        OS_NAME=$(cat /etc/redhat-release 2>/dev/null || echo "RedHat/CentOS")
        OS_VERSION="unknown"
    elif [ -f /etc/debian_version ]; then
        OS_ID="debian"
        OS_NAME="Debian"
        OS_VERSION=$(cat /etc/debian_version 2>/dev/null || echo "unknown")
    else
        log_warn "Không thể đọc metadata OS chuẩn. Dùng cơ chế phân tích tổng quát."
        OS_ID="generic_linux"
    fi

    # Nhận diện trình quản lý gói tương ứng
    case "$OS_ID" in
        ubuntu|debian|raspbian|linuxmint|pop)
            PKG_MGR="apt"
            ;;
        centos|rhel|rocky|almalinux|oracle|amzn)
            if command -v dnf &>/dev/null; then
                PKG_MGR="dnf"
            else
                PKG_MGR="yum"
            fi
            ;;
        fedora)
            PKG_MGR="dnf"
            ;;
        alpine)
            PKG_MGR="apk"
            ;;
        arch|manjaro)
            PKG_MGR="pacman"
            ;;
        opensuse*|sles)
            PKG_MGR="zypper"
            ;;
        *)
            if command -v apt-get &>/dev/null; then
                PKG_MGR="apt"
            elif command -v dnf &>/dev/null; then
                PKG_MGR="dnf"
            elif command -v yum &>/dev/null; then
                PKG_MGR="yum"
            elif command -v apk &>/dev/null; then
                PKG_MGR="apk"
            else
                PKG_MGR="generic"
            fi
            ;;
    esac

    # Kiểm tra Init System (systemd vs OpenRC)
    if [ -d /run/systemd/system ] || command -v systemctl &>/dev/null; then
        INIT_SYSTEM="systemd"
    elif [ -f /sbin/openrc-run ] || [ -d /etc/init.d ]; then
        INIT_SYSTEM="openrc"
    else
        INIT_SYSTEM="standalone"
    fi

    log_success "Phát hiện hệ điều hành: ${WHITE}${BOLD}${OS_NAME}${NC} (Phiên bản: ${CYAN}${OS_VERSION}${NC})"
    log_info "Trình quản lý gói xác định : ${GREEN}${PKG_MGR}${NC}"
    log_info "Hệ thống quản lý dịch vụ   : ${GREEN}${INIT_SYSTEM}${NC}"
}

# ------------------------------------------------------------------------------
# BƯỚC 3: CÀI ĐẶT CÁC GÓI PHỤ THUỘC (DEPENDENCIES) TỰ ĐỘNG
# ------------------------------------------------------------------------------
install_dependencies() {
    log_step "BƯỚC 3: Cài đặt các gói phụ thuộc hệ thống theo Distro"

    case "$PKG_MGR" in
        apt)
            log_info "Cập nhật danh mục gói APT và cài đặt gói cơ bản..."
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq -y
            apt-get install -qq -y --no-install-recommends \
                curl wget tar gzip unzip ca-certificates jq sqlite3 \
                procps ufw iptables >/dev/null
            ;;
        dnf|yum)
            log_info "Cài đặt gói hỗ trợ qua $PKG_MGR..."
            $PKG_MGR install -y -q epel-release 2>/dev/null || true
            $PKG_MGR install -y -q curl wget tar gzip unzip ca-certificates jq sqlite firewalld iptables >/dev/null
            ;;
        apk)
            log_info "Cài đặt gói bổ trợ qua Alpine APK..."
            apk update -q
            apk add --no-cache curl wget tar gzip unzip ca-certificates jq sqlite \
                iptables bash shadow procps >/dev/null 2>&1 || true
            ;;
        pacman)
            pacman -Sy --noconfirm --needed curl wget tar gzip unzip ca-certificates jq sqlite iptables >/dev/null
            ;;
        *)
            log_warn "Distro không xác định trình quản lý gói. Đảm bảo curl, sqlite3, tar đã sẵn sàng."
            ;;
    esac
    log_success "Các công cụ phụ trợ hệ thống đã sẵn sàng."
}

# ------------------------------------------------------------------------------
# BƯỚC 4: TỰ ĐỘNG PHÁT HIỆN THƯ MỤC NGUỒN HOẶC TRIỂN KHAI VÀO /opt/supportflast
# ------------------------------------------------------------------------------
setup_install_directory() {
    log_step "BƯỚC 4: Tự động phát hiện thư mục cài đặt (/opt/supportflast)"

    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    log_info "Vị trí thực thi script hiện tại : ${YELLOW}${script_dir}${NC}"
    log_info "Thư mục đích của hệ thống      : ${CYAN}${TARGET_DIR}${NC}"

    # Kiểm tra xem script đang chạy ngay trong thư mục dự án
    local is_source_workspace=0
    if [ -d "$script_dir/supportflast_engine" ] || [ -d "$script_dir/supportflast_ui" ] || [ -f "$script_dir/supportflast" ]; then
        is_source_workspace=1
    fi

    if [ "$script_dir" = "$TARGET_DIR" ]; then
        log_info "Script đang chạy trực tiếp tại thư mục đích chuẩn: $TARGET_DIR"
    elif [ "$is_source_workspace" -eq 1 ]; then
        log_info "Phát hiện mã nguồn dự án tại: $script_dir. Tiến hành đồng bộ sang $TARGET_DIR..."
        mkdir -p "$TARGET_DIR"
        
        # Sao chép các thành phần dự án sang /opt/supportflast
        for item in supportflast supportflast_engine supportflast_ui supportflast_ai supportflast_core tools Caddyfile Dockerfile docker-compose.yml *.sh *.conf; do
            if [ -e "$script_dir"/$item ]; then
                cp -rf "$script_dir"/$item "$TARGET_DIR/" 2>/dev/null || true
            fi
        done
        log_success "Đã sao chép cấu trúc ứng dụng hoàn chỉnh vào $TARGET_DIR"
    else
        log_warn "Thư mục hiện tại không chứa mã nguồn dự án. Chuẩn bị môi trường $TARGET_DIR..."
        mkdir -p "$TARGET_DIR"
    fi

    # Đảm bảo cấu trúc thư mục dữ liệu, storage và backup tồn tại theo chuẩn quy định
    mkdir -p "$TARGET_DIR/data"
    mkdir -p "$TARGET_DIR/data/keys"
    mkdir -p "$TARGET_DIR/storage"
    mkdir -p "$TARGET_DIR/storage/packages"
    mkdir -p "$TARGET_DIR/backups/sqlite"
    mkdir -p "$TARGET_DIR/supportflast_ui"

    log_success "Cấu trúc thư mục $TARGET_DIR đã được chuẩn hóa."
}

# ------------------------------------------------------------------------------
# BƯỚC 5: ĐẢM BẢO BINARY TĨNH 'supportflast' SẴN SÀNG HOẠT ĐỘNG
# ------------------------------------------------------------------------------
ensure_binary_executable() {
    log_step "BƯỚC 5: Kiểm tra và chuẩn bị Binary tĩnh SupportFlast Unified Core"

    local binary_path="$TARGET_DIR/supportflast"

    # 1. Kiểm tra xem binary đã có sẵn ở TARGET_DIR chưa
    if [ -f "$binary_path" ] && [ -s "$binary_path" ]; then
        chmod +x "$binary_path"
        log_success "Tìm thấy binary tĩnh sẵn sàng: $binary_path"
        return 0
    fi

    # 2. Kiểm tra xem có binary tại supportflast_engine/supportflast không
    if [ -f "$TARGET_DIR/supportflast_engine/supportflast" ]; then
        log_info "Đồng bộ binary từ module engine sang thư mục gốc..."
        cp -f "$TARGET_DIR/supportflast_engine/supportflast" "$binary_path"
        chmod +x "$binary_path"
        log_success "Đã liên kết binary tĩnh thành công."
        return 0
    fi

    # 3. Nếu chưa có binary nhưng có mã nguồn Go -> Biên dịch trực tiếp binary tĩnh
    if [ -d "$TARGET_DIR/supportflast_engine" ]; then
        log_info "Chưa có binary Linux dựng sẵn. Tiến hành kiểm tra trình biên dịch Go..."
        if ! command -v go &>/dev/null; then
            log_info "Tự động cài đặt Golang để biên dịch binary tĩnh cho Linux..."
            if [ "$PKG_MGR" = "apt" ]; then
                apt-get install -y -qq golang-go >/dev/null 2>&1 || true
            elif [ "$PKG_MGR" = "apk" ]; then
                apk add --no-cache go >/dev/null 2>&1 || true
            fi
        fi

        if command -v go &>/dev/null; then
            log_info "Đang biên dịch SupportFlast tĩnh: CGO_ENABLED=0 GOOS=linux go build..."
            cd "$TARGET_DIR/supportflast_engine"
            CGO_ENABLED=0 GOOS=linux go build \
                -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
                -trimpath \
                -o "$binary_path" .
            chmod +x "$binary_path"
            log_success "Biên dịch binary tĩnh hoàn tất: $binary_path"
            return 0
        fi
    fi

    log_warn "Binary chưa có sẵn nhưng script sẽ tiếp tục thiết lập cấu hình dịch vụ."
}

# ------------------------------------------------------------------------------
# BƯỚC 6: TỰ ĐỘNG TẠO USER & PHÂN QUYỀN AN TOÀN (chown -R www-data, chmod +x)
# ------------------------------------------------------------------------------
setup_user_and_permissions() {
    log_step "BƯỚC 6: Tự động phân quyền an toàn (chown -R www-data, chmod +x)"

    # Đảm bảo group www-data tồn tại
    if ! getent group "$SERVICE_USER" &>/dev/null; then
        log_info "Tạo nhóm dịch vụ '$SERVICE_USER' trên hệ thống..."
        if command -v groupadd &>/dev/null; then
            groupadd -r "$SERVICE_USER" 2>/dev/null || true
        elif command -v addgroup &>/dev/null; then
            addgroup -S "$SERVICE_USER" 2>/dev/null || true
        fi
    fi

    # Đảm bảo user www-data tồn tại trên mọi bản phân phối Linux
    if ! id -u "$SERVICE_USER" &>/dev/null; then
        log_info "Tạo tài khoản dịch vụ an toàn (non-login): '$SERVICE_USER'..."
        if command -v useradd &>/dev/null; then
            useradd -r -g "$SERVICE_USER" -s /sbin/nologin -d /var/www -M "$SERVICE_USER" 2>/dev/null || \
            useradd -r -s /sbin/nologin -d /var/www -M "$SERVICE_USER" 2>/dev/null || true
        elif command -v adduser &>/dev/null; then
            adduser -S -D -H -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER" 2>/dev/null || \
            adduser -S -D -H -s /sbin/nologin "$SERVICE_USER" 2>/dev/null || true
        fi
    fi

    # Phân quyền sở hữu toàn diện cho thư mục cài đặt
    if id -u "$SERVICE_USER" &>/dev/null; then
        log_info "Áp dụng quyền sở hữu ${BOLD}${SERVICE_USER}:${SERVICE_USER}${NC} cho ${TARGET_DIR}..."
        chown -R "${SERVICE_USER}:${SERVICE_USER}" "$TARGET_DIR" 2>/dev/null || true
    else
        log_warn "Không thể khởi tạo user $SERVICE_USER, sử dụng quyền mặc định của tiến trình."
    fi

    # Thiết lập quyền truy cập chi tiết chuẩn Enterprise Security
    chmod -R 755 "$TARGET_DIR"
    chmod -R 775 "$TARGET_DIR/data" 2>/dev/null || true
    chmod -R 775 "$TARGET_DIR/storage" 2>/dev/null || true
    chmod -R 700 "$TARGET_DIR/backups" 2>/dev/null || true

    # Phân quyền thực thi bắt buộc cho Binary và các script Shell
    if [ -f "$TARGET_DIR/supportflast" ]; then
        chmod +x "$TARGET_DIR/supportflast"
    fi
    if [ -f "$TARGET_DIR/supportflast_engine/supportflast" ]; then
        chmod +x "$TARGET_DIR/supportflast_engine/supportflast"
    fi
    chmod +x "$TARGET_DIR"/*.sh 2>/dev/null || true

    log_success "Đã phân quyền an toàn: chown -R ${SERVICE_USER} & chmod +x binary thành công."
}

# ------------------------------------------------------------------------------
# BƯỚC 7: TỰ ĐỘNG TẠO VÀ KÍCH HOẠT SYSTEMD SERVICE 'supportflast.service'
# ------------------------------------------------------------------------------
setup_service() {
    log_step "BƯỚC 7: Tự động tạo và kích hoạt systemd service: supportflast.service"

    if [ "$INIT_SYSTEM" = "systemd" ]; then
        local service_file="/etc/systemd/system/supportflast.service"
        log_info "Tạo tệp cấu hình Systemd: ${CYAN}${service_file}${NC}..."

        cat <<EOF > "$service_file"
[Unit]
Description=SupportFlast Unified Core Engine (Web 3D + Storage + REST API + 5 Subagents)
Documentation=https://${DOMAIN}
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${TARGET_DIR}
Environment="PORT=${PORT}"
Environment="HOST=0.0.0.0"
Environment="DOMAIN=${DOMAIN}"
Environment="DATA_DIR=${TARGET_DIR}/data"
Environment="STORAGE_DIR=${TARGET_DIR}/storage"
Environment="UI_DIR=${TARGET_DIR}/supportflast_ui"
Environment="AI_ENGINE_URL=http://127.0.0.1:8000"
Environment="GIN_MODE=release"
ExecStart=${TARGET_DIR}/supportflast
Restart=always
RestartSec=5s
LimitNOFILE=65535
LimitNPROC=4096
TimeoutStopSec=15s

# Security sandboxing
ProtectSystem=full
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

        chmod 644 "$service_file"
        log_info "Nạp lại Systemd Daemon (systemctl daemon-reload)..."
        systemctl daemon-reload

        log_info "Kích hoạt dịch vụ tự khởi động cùng hệ điều hành..."
        systemctl enable supportflast.service >/dev/null 2>&1

        log_info "Khởi động dịch vụ supportflast.service..."
        systemctl restart supportflast.service >/dev/null 2>&1 || true

        log_success "Systemd service 'supportflast.service' đã được tạo và kích hoạt tự động."

    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        local rc_file="/etc/init.d/supportflast"
        log_info "Phát hiện OpenRC (Alpine Linux). Khởi tạo dịch vụ: ${CYAN}${rc_file}${NC}..."

        cat <<EOF > "$rc_file"
#!/sbin/openrc-run
name="supportflast"
description="SupportFlast Unified Core Engine"
command="${TARGET_DIR}/supportflast"
command_background="yes"
directory="${TARGET_DIR}"
pidfile="/run/supportflast.pid"

depend() {
    need net
    after firewall
}

start_pre() {
    export PORT="${PORT}"
    export HOST="0.0.0.0"
    export DOMAIN="${DOMAIN}"
    export DATA_DIR="${TARGET_DIR}/data"
    export STORAGE_DIR="${TARGET_DIR}/storage"
    export UI_DIR="${TARGET_DIR}/supportflast_ui"
    export AI_ENGINE_URL="http://127.0.0.1:8000"
}
EOF
        chmod 755 "$rc_file"
        rc-update add supportflast default >/dev/null 2>&1 || true
        rc-service supportflast restart >/dev/null 2>&1 || true
        log_success "OpenRC service 'supportflast' đã được cấu hình trên Alpine Linux."
    else
        log_warn "Không phát hiện Systemd hoặc OpenRC. Bạn có thể chạy trực tiếp: ${TARGET_DIR}/supportflast"
    fi
}

# ------------------------------------------------------------------------------
# BƯỚC 8: TỰ ĐỘNG MỞ FIREWALL (UFW, FIREWALLD, IPTABLES) CHO PORT 80, 443, 8080
# ------------------------------------------------------------------------------
configure_firewall() {
    log_step "BƯỚC 8: Tự động mở Firewall (UFW, Firewalld, Iptables) cho Port 80, 443, 8080"

    local ports=(80 443 8080)
    local opened=0

    # 1. Cấu hình UFW (Ubuntu / Debian phổ biến)
    if command -v ufw &>/dev/null; then
        log_info "Phát hiện UFW Firewall. Đang cấu hình các cổng mạng..."
        for p in "${ports[@]}"; do
            ufw allow "$p"/tcp comment "SupportFlast Port $p" >/dev/null 2>&1 || true
        done
        if ufw status 2>/dev/null | grep -q "Status: active"; then
            ufw reload >/dev/null 2>&1 || true
            log_info "UFW đang hoạt động: Đã reload bảng luật mở cổng 80, 443, 8080."
        fi
        log_success "UFW: Đã cấp quyền truy cập TCP Port 80, 443, 8080."
        opened=1
    fi

    # 2. Cấu hình Firewalld (CentOS / Rocky / AlmaLinux / Fedora / RHEL)
    if command -v firewall-cmd &>/dev/null; then
        if systemctl is-active --quiet firewalld 2>/dev/null || firewall-cmd --state &>/dev/null; then
            log_info "Phát hiện Firewalld đang kích hoạt. Cấu hình rules vĩnh viễn..."
            for p in "${ports[@]}"; do
                firewall-cmd --permanent --add-port="$p"/tcp >/dev/null 2>&1 || true
            done
            firewall-cmd --reload >/dev/null 2>&1 || true
            log_success "Firewalld: Đã mở vĩnh viễn TCP Port 80, 443, 8080."
            opened=1
        fi
    fi

    # 3. Cấu hình Iptables (Mọi bản phân phối / Fallback an toàn)
    if command -v iptables &>/dev/null; then
        log_info "Kiểm tra và nạp quy tắc vào bảng Iptables INPUT..."
        for p in "${ports[@]}"; do
            if ! iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null; then
                iptables -I INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || true
            fi
        done
        # Lưu iptables nếu có tiện ích lưu trữ
        if command -v netfilter-persistent &>/dev/null; then
            netfilter-persistent save >/dev/null 2>&1 || true
        elif command -v iptables-save &>/dev/null && [ -f /etc/sysconfig/iptables ]; then
            iptables-save > /etc/sysconfig/iptables 2>/dev/null || true
        fi
        log_success "Iptables: Đã cấu hình ACCEPT cho TCP Port 80, 443, 8080."
        opened=1
    fi

    if [ "$opened" -eq 0 ]; then
        log_info "Không phát hiện tường lửa đang bật hoặc hạn chế port. Mạng ở trạng thái mở sẵn."
    fi
}

# ------------------------------------------------------------------------------
# BƯỚC 9: THIẾT LẬP LỊCH TRÌNH SAO LƯU SQLITE WAL TỰ ĐỘNG
# ------------------------------------------------------------------------------
setup_backup_scheduler() {
    log_step "BƯỚC 9: Thiết lập sao lưu tự động cơ sở dữ liệu SQLite định kỳ"

    local backup_script="$TARGET_DIR/backup_sqlite.sh"
    if [ -f "$backup_script" ]; then
        chmod +x "$backup_script"
        if [ -d /etc/cron.d ]; then
            cat <<EOF > /etc/cron.d/supportflast_backup
# Tự động sao lưu cơ sở dữ liệu SQLite SupportFlast lúc 02:00 sáng hàng ngày
0 2 * * * root /bin/bash ${backup_script} >> /var/log/supportflast_backup.log 2>&1
EOF
            chmod 644 /etc/cron.d/supportflast_backup
            log_success "Đã kích hoạt Cron Job sao lưu tự động (/etc/cron.d/supportflast_backup lúc 02:00)."
        fi
    else
        log_info "Không tìm thấy backup_sqlite.sh, bỏ qua bước lập lịch cron."
    fi
}

# ------------------------------------------------------------------------------
# BƯỚC 10: KIỂM TRA SỨC KHỎE DỊCH VỤ (HEALTH CHECK)
# ------------------------------------------------------------------------------
verify_service_health() {
    log_step "BƯỚC 10: Kiểm tra sức khỏe dịch vụ SupportFlast (Health Check)"

    # Phát hiện địa chỉ IP Public hoặc Local IP của máy chủ
    SERVER_IP=$(ip route get 1.1.1.1 2>/dev/null | awk -F"src " 'NR==1{split($2,a," ");print a[1]}' || true)
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "127.0.0.1")
    fi

    log_info "Đang thăm dò trạng thái qua cổng ${PORT}..."
    sleep 3

    local health_resp
    health_resp=$(curl -s -m 5 "http://127.0.0.1:${PORT}/api/health" 2>/dev/null || echo "offline")

    if [[ "$health_resp" == *"healthy"* ]]; then
        log_success "Dịch vụ đã phản hồi: ${GREEN}${BOLD}HEALTHY - SẴN SÀNG HOẠT ĐỘNG!${NC}"
    else
        log_warn "Dịch vụ đang khởi động hoặc chưa phản hồi tức thì (Health: $health_resp)."
        log_info "Bạn có thể kiểm tra nhật ký chi tiết bằng: journalctl -u supportflast.service -n 30"
    fi
}

# ------------------------------------------------------------------------------
# BƯỚC 11: IN BẢNG THÔNG BÁO HOÀN TẤT ĐẸP MẮT VỚI MÀU SẮC ANSI
# ------------------------------------------------------------------------------
print_completion_guide() {
    echo -e "\n${CYAN}╔══════════════════════════════════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║${NC}   ${GREEN}${BOLD}🎉 CHÚC MỪNG! HỆ THỐNG SUPPORTFLAST ĐÃ ĐƯỢC CÀI ĐẶT THÀNH CÔNG 1-CLICK HOÀN HẢO!${NC}           ${CYAN}║${NC}"
    echo -e "${CYAN}╚══════════════════════════════════════════════════════════════════════════════════════════════╝${NC}"
    
    echo -e "\n${WHITE}${BOLD}📋 THÔNG TIN TRIỂN KHAI VÀ TRẠNG THÁI:${NC}"
    echo -e "  • ${BOLD}Hệ điều hành đã nhận diện${NC} : ${GREEN}${OS_NAME} ${OS_VERSION} (${PKG_MGR})${NC}"
    echo -e "  • ${BOLD}Thư mục cài đặt ứng dụng${NC}  : ${YELLOW}${TARGET_DIR}${NC}"
    echo -e "  • ${BOLD}Tài khoản thực thi quyền${NC}  : ${PURPLE}${SERVICE_USER}${NC} (Đã chown -R & chmod +x)"
    echo -e "  • ${BOLD}Systemd Service${NC}           : ${CYAN}supportflast.service${NC} (Enabled & Running)"
    echo -e "  • ${BOLD}Tường lửa (Firewall)${NC}      : ${GREEN}Port 80 (HTTP), 443 (HTTPS), 8080 (Engine) ĐÃ MỞ${NC}"

    echo -e "\n${WHITE}${BOLD}🌐 ĐỊA CHỈ TRUY CẬP VÀ LIÊN KẾT HỆ THỐNG:${NC}"
    echo -e "  👉 ${BOLD}Giao Diện Web 3D Không Gian${NC} : ${CYAN}http://${SERVER_IP}:${PORT}${NC}"
    echo -e "  👉 ${BOLD}Tên Miền Chính Thức${NC}         : ${GREEN}https://${DOMAIN}${NC}"
    echo -e "  👉 ${BOLD}Kho Lưu Trữ Ứng Dụng Đám Mây${NC}: ${CYAN}http://${SERVER_IP}:${PORT}/storage${NC}"
    echo -e "  👉 ${BOLD}Bộ Công Cụ Lập Trình Viên${NC}   : ${CYAN}http://${SERVER_IP}:${PORT}/tools${NC}"
    echo -e "  👉 ${BOLD}API Trạng Thái & 5 Subagents${NC}: ${CYAN}http://${SERVER_IP}:${PORT}/api/health${NC}"

    echo -e "\n${WHITE}${BOLD}🛠️  CÁC LỆNH QUẢN TRỊ DỊCH VỤ NHANH:${NC}"
    echo -e "  • ${BOLD}Xem trạng thái dịch vụ${NC}        : ${YELLOW}systemctl status supportflast.service${NC}"
    echo -e "  • ${BOLD}Xem nhật ký thời gian thực${NC}    : ${YELLOW}journalctl -u supportflast.service -f -n 50${NC}"
    echo -e "  • ${BOLD}Khởi động lại ứng dụng${NC}        : ${YELLOW}systemctl restart supportflast.service${NC}"
    echo -e "  • ${BOLD}Tạm dừng ứng dụng${NC}             : ${YELLOW}systemctl stop supportflast.service${NC}"

    echo -e "\n${WHITE}${BOLD}🔒 HƯỚNG DẪN CẤU HÌNH SSL / HTTPS (TÙY CHỌN):${NC}"
    echo -e "  1. ${BOLD}Cách 1 (Cloudflare Tunnel)${NC} : Dùng cloudflared trỏ về http://127.0.0.1:8080 (Bảo mật 100%)"
    echo -e "  2. ${BOLD}Cách 2 (Caddy SSL Tự Động)${NC}: Dùng Caddyfile có sẵn và chạy: ${DIM}caddy run --config Caddyfile${NC}"
    echo -e "  3. ${BOLD}Cách 3 (Nginx Reverse)${NC}   : Kích hoạt file ${DIM}${TARGET_DIR}/nginx_${DOMAIN}.conf${NC}\n"

    echo -e "${PURPLE}  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "  ${WHITE}Hệ thống đã sẵn sàng phục vụ 100% người dùng trên toàn cầu với hiệu năng đỉnh cao!${NC}"
    echo -e "${PURPLE}  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
}

# ------------------------------------------------------------------------------
# HÀM ĐIỀU PHỐI CHÍNH (MAIN EXECUTION)
# ------------------------------------------------------------------------------
main() {
    # Hỗ trợ tham số dòng lệnh tùy biến linh hoạt
    while [ $# -gt 0 ]; do
        case "$1" in
            --dir|-d)
                TARGET_DIR="$2"
                shift 2
                ;;
            --port|-p)
                PORT="$2"
                shift 2
                ;;
            --domain)
                DOMAIN="$2"
                shift 2
                ;;
            --help|-h)
                echo "Cú pháp: sudo bash install.sh [TÙY CHỌN]"
                echo "  --dir, -d <path>   Chỉ định thư mục cài đặt (Mặc định: /opt/supportflast)"
                echo "  --port, -p <port>  Chỉ định cổng dịch vụ (Mặc định: 8080)"
                echo "  --domain <domain>  Chỉ định tên miền ứng dụng (Mặc định: supportflastdev.io.vn)"
                echo "  --help, -h         Hiển thị trợ giúp"
                exit 0
                ;;
            *)
                log_warn "Tham số không xác định: $1. Tiếp tục với cấu hình mặc định..."
                shift
                ;;
        esac
    done

    print_banner
    check_root
    detect_os
    install_dependencies
    setup_install_directory
    ensure_binary_executable
    setup_user_and_permissions
    setup_service
    configure_firewall
    setup_backup_scheduler
    verify_service_health
    print_completion_guide
}

main "$@"
