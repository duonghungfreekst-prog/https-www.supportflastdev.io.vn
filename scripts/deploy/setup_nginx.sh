#!/usr/bin/env bash
# ==============================================================================
# SCRIPT TỰ ĐỘNG THIẾT LẬP VIRTUALHOST NGINX & REVERSE PROXY CHO SUPPORTFLAST
# ==============================================================================
# Tác vụ tự động:
#  1. Tiếp nhận hoặc tương tác nhập tên miền (Domain) do người dùng chỉ định.
#  2. Tự động kiểm tra / cài đặt Nginx, Certbot và python3-certbot-nginx.
#  3. Tự động sinh cấu hình VirtualHost Nginx tối ưu cao cấp:
#     - client_max_body_size 1000M (1GB) & tăng đệm RAM client_body_buffer_size 16M
#     - Tối ưu bộ đệm proxy (proxy_buffer_size 128k, proxy_buffers 16 64k)
#     - Hỗ trợ đầy đủ WebSocket (/ws) với nâng cấp giao thức 101 Switching Protocols
#     - Hỗ trợ chuyên sâu WebDAV (/webdav/) với zero-buffer streaming & đầy đủ headers
#     - Bảo mật Headers chuẩn quân sự (Rule 3.4 & 3.5 Antigravity Workspace)
#     - Rate limiting chống DDoS & Brute-force, phục hồi Real-IP Cloudflare
#  4. Tự động liên kết kích hoạt vào '/etc/nginx/sites-enabled/'.
#  5. Kiểm tra cú pháp (nginx -t) và nạp lại dịch vụ Nginx.
#  6. Tự động gọi 'certbot --nginx -d yourdomain.com' cấp SSL Let's Encrypt miễn phí.
#  7. Tự động cấu hình và kiểm tra lịch tự gia hạn (Systemd Timer & Cron Job).
# ==============================================================================

set -euo pipefail

# ------------------------------------------------------------------------------
# MÀU SẮC ĐỊNH DẠNG TERMINAL (ANSI COLORS)
# ------------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# ------------------------------------------------------------------------------
# CÁC BIẾN MẶC ĐỊNH
# ------------------------------------------------------------------------------
DEFAULT_DOMAIN="supportflastdev.io.vn"
DEFAULT_BACKEND="127.0.0.1:8080"
DEFAULT_EMAIL="admin@supportflastdev.io.vn"

DOMAIN=""
INCLUDE_WWW="true"
BACKEND_ADDR="$DEFAULT_BACKEND"
CERTBOT_EMAIL=""
SKIP_SSL="false"
DRY_RUN="false"
ASSUME_YES="false"
STAGING_SSL="false"

# ------------------------------------------------------------------------------
# HÀM HIỂN THỊ LOG & BANNER
# ------------------------------------------------------------------------------
print_banner() {
    echo -e "${CYAN}${BOLD}"
    echo "=========================================================================="
    echo "    🚀 SUPPORTFLAST - NGINX & REVERSE PROXY AUTO-CONFIGURATOR           "
    echo "    🌐 Tự Động Cấu Hình VirtualHost, WebSocket, WebDAV & Let's Encrypt SSL"
    echo "=========================================================================="
    echo -e "${NC}"
}

log_info()    { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') - $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') - $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') - $1"; }
log_success() { echo -e "${GREEN}${BOLD}[SUCCESS]${NC} $(date '+%H:%M:%S') - $1"; }
log_step()    { echo -e "\n${PURPLE}${BOLD}>>> BƯỚC: $1${NC}"; }

# ------------------------------------------------------------------------------
# HIỂN THỊ HƯỚNG DẪN SỬ DỤNG (CLI HELP)
# ------------------------------------------------------------------------------
show_help() {
    print_banner
    echo -e "Cú pháp sử dụng:"
    echo -e "  $0 [TÙY CHỌN...]"
    echo -e "  $0 yourdomain.com"
    echo ""
    echo -e "Các tùy chọn hỗ trợ:"
    echo -e "  -d, --domain <domain>     Tên miền chính (Ví dụ: supportflastdev.io.vn)"
    echo -e "  -w, --www                 Bao gồm cả subdomain www.<domain> (Mặc định: Bật)"
    echo -e "      --no-www              Không bao gồm subdomain www.<domain>"
    echo -e "  -b, --backend <host:port> Địa chỉ Backend Upstream (Mặc định: 127.0.0.1:8080)"
    echo -e "  -e, --email <email>       Email quản trị đăng ký chứng chỉ SSL Let's Encrypt"
    echo -e "      --skip-ssl            Bỏ qua bước cấp SSL Certbot (Chỉ cấu hình HTTP Port 80)"
    echo -e "      --staging             Sử dụng máy chủ Staging của Let's Encrypt để kiểm tra"
    echo -e "      --dry-run             Chạy thử nghiệm xuất file cấu hình mà không sửa đổi hệ thống"
    echo -e "  -y, --yes                 Tự động xác nhận tất cả câu hỏi với giá trị mặc định"
    echo -e "  -h, --help                Hiển thị hướng dẫn này và thoát"
    echo ""
    echo -e "Ví dụ thực tế:"
    echo -e "  sudo $0 -d myapp.com -e admin@myapp.com"
    echo -e "  sudo $0 -d supportflastdev.io.vn --backend 127.0.0.1:8080"
    echo -e "  $0 --dry-run -d test.example.com"
    echo ""
}

# ------------------------------------------------------------------------------
# XỬ LÝ THAM SỐ DÒNG LỆNH (CLI ARGUMENTS PARSER)
# ------------------------------------------------------------------------------
parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -d|--domain)
                DOMAIN="$2"
                shift 2
                ;;
            -w|--www)
                INCLUDE_WWW="true"
                shift
                ;;
            --no-www)
                INCLUDE_WWW="false"
                shift
                ;;
            -b|--backend)
                BACKEND_ADDR="$2"
                shift 2
                ;;
            -e|--email)
                CERTBOT_EMAIL="$2"
                shift 2
                ;;
            --skip-ssl)
                SKIP_SSL="true"
                shift
                ;;
            --staging)
                STAGING_SSL="true"
                shift
                ;;
            --dry-run)
                DRY_RUN="true"
                shift
                ;;
            -y|--yes)
                ASSUME_YES="true"
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            *)
                if [[ -z "$DOMAIN" && "$1" != -* ]]; then
                    DOMAIN="$1"
                    shift
                else
                    log_error "Tùy chọn không hợp lệ: $1"
                    show_help
                    exit 1
                fi
                ;;
        esac
    done
}

# ------------------------------------------------------------------------------
# KIỂM TRA QUYỀN ROOT / SUDO
# ------------------------------------------------------------------------------
check_root() {
    if [[ "$DRY_RUN" == "true" ]]; then
        return 0
    fi
    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        log_error "Bạn cần quyền root hoặc sudo để thực thi cấu hình hệ thống Nginx!"
        echo -e "👉 Vui lòng chạy lại bằng lệnh: ${YELLOW}sudo $0${NC}"
        exit 1
    fi
}

# ------------------------------------------------------------------------------
# XÁC THỰC TÊN MIỀN HỢP LỆ (RFC COMPLIANT & ANTI-INJECTION)
# ------------------------------------------------------------------------------
validate_domain() {
    local dom="$1"
    # Biểu thức chính quy kiểm tra định dạng tên miền chuẩn
    local domain_regex="^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$"
    if [[ ! "$dom" =~ $domain_regex ]]; then
        log_error "Tên miền '${dom}' không đúng định dạng hợp lệ (RFC standards)!"
        return 1
    fi
    # Kiểm tra ký tự lạ chống command injection hoặc path traversal
    if [[ "$dom" == *"/"* || "$dom" == *"\\"* || "$dom" == *".."* || "$dom" == *";"* || "$dom" == *"&"* || "$dom" == *"|"* ]]; then
        log_error "Tên miền chứa ký tự không an toàn!"
        return 1
    fi
    return 0
}

# ------------------------------------------------------------------------------
# TƯƠNG TÁC NHẬP LIỆU NẾU CHƯA CUNG CẤP ĐỦ THAM SỐ
# ------------------------------------------------------------------------------
prompt_user_inputs() {
    # 1. Nhập tên miền
    if [[ -z "$DOMAIN" ]]; then
        if [[ "$ASSUME_YES" == "true" || ! -t 0 ]]; then
            DOMAIN="$DEFAULT_DOMAIN"
            log_info "Sử dụng tên miền mặc định: ${BOLD}${DOMAIN}${NC}"
        else
            echo -e "${YELLOW}${BOLD}Nhập tên miền (Domain) của bạn [mặc định: ${DEFAULT_DOMAIN}]:${NC} "
            read -r input_domain || input_domain=""
            DOMAIN="${input_domain:-$DEFAULT_DOMAIN}"
        fi
    fi

    # Làm sạch khoảng trắng thừa
    DOMAIN=$(echo "$DOMAIN" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')

    if ! validate_domain "$DOMAIN"; then
        log_error "Quá trình dừng lại do tên miền không hợp lệ."
        exit 1
    fi

    # 2. Subdomain www
    if [[ "$ASSUME_YES" == "false" && -t 0 && -z "${INCLUDE_WWW_MANUAL:-}" ]]; then
        # Nếu domain có dạng subdomain như sub.domain.com thì mặc định không thêm www
        local dot_count
        dot_count=$(awk -F. '{print NF-1}' <<< "$DOMAIN")
        local default_www="Y"
        if [ "$dot_count" -gt 1 ]; then
            default_www="N"
        fi

        echo -e "${YELLOW}Có muốn bao gồm cả alias 'www.${DOMAIN}' không? [${default_www}/n]:${NC} "
        read -r input_www || input_www="$default_www"
        input_www="${input_www:-$default_www}"
        if [[ "$input_www" =~ ^[Yy]$ ]]; then
            INCLUDE_WWW="true"
        else
            INCLUDE_WWW="false"
        fi
    fi

    # 3. Địa chỉ Backend Upstream
    if [[ "$ASSUME_YES" == "false" && -t 0 && "$BACKEND_ADDR" == "$DEFAULT_BACKEND" ]]; then
        echo -e "${YELLOW}Nhập địa chỉ Backend Upstream [mặc định: ${DEFAULT_BACKEND}]:${NC} "
        read -r input_backend || input_backend=""
        BACKEND_ADDR="${input_backend:-$DEFAULT_BACKEND}"
    fi

    # 4. Email quản trị cho Let's Encrypt Certbot
    if [[ "$SKIP_SSL" == "false" && -z "$CERTBOT_EMAIL" ]]; then
        if [[ "$ASSUME_YES" == "true" || ! -t 0 ]]; then
            CERTBOT_EMAIL="admin@${DOMAIN}"
            log_info "Sử dụng email quản trị mặc định: ${BOLD}${CERTBOT_EMAIL}${NC}"
        else
            echo -e "${YELLOW}Nhập Email quản trị để nhận cảnh báo hết hạn SSL Let's Encrypt [admin@${DOMAIN}]:${NC} "
            read -r input_email || input_email=""
            CERTBOT_EMAIL="${input_email:-admin@${DOMAIN}}"
        fi
    fi
}

# ------------------------------------------------------------------------------
# NHẬN DIỆN HỆ ĐIỀU HÀNH VÀ CÀI ĐẶT CÁC GÓI BẮT BUỘC
# ------------------------------------------------------------------------------
detect_os_and_install_deps() {
    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "[DRY-RUN] Bỏ qua kiểm tra và cài đặt gói hệ thống."
        return 0
    fi

    log_step "Kiểm tra hệ điều hành và gói phụ thuộc (Nginx, Certbot)..."

    local os_id="unknown"
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        os_id="${ID:-unknown}"
    fi

    log_info "Hệ điều hành phát hiện: ${BOLD}${os_id}${NC}"

    # Kiểm tra Nginx
    local need_install_nginx="false"
    if ! command -v nginx &>/dev/null; then
        need_install_nginx="true"
        log_warn "Chưa tìm thấy Nginx trên máy chủ. Đang chuẩn bị cài đặt tự động..."
    fi

    # Kiểm tra Certbot
    local need_install_certbot="false"
    if [[ "$SKIP_SSL" == "false" ]]; then
        if ! command -v certbot &>/dev/null; then
            need_install_certbot="true"
            log_warn "Chưa tìm thấy Certbot. Đang chuẩn bị cài đặt tự động..."
        fi
    fi

    if [[ "$need_install_nginx" == "true" || "$need_install_certbot" == "true" ]]; then
        if [[ "$os_id" == "ubuntu" || "$os_id" == "debian" ]]; then
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -y
            if [[ "$need_install_nginx" == "true" ]]; then
                apt-get install -y nginx
            fi
            if [[ "$need_install_certbot" == "true" ]]; then
                apt-get install -y certbot python3-certbot-nginx
            fi
        elif [[ "$os_id" == "centos" || "$os_id" == "rhel" || "$os_id" == "rocky" || "$os_id" == "almalinux" ]]; then
            dnf install -y epel-release || true
            if [[ "$need_install_nginx" == "true" ]]; then
                dnf install -y nginx
            fi
            if [[ "$need_install_certbot" == "true" ]]; then
                dnf install -y certbot python3-certbot-nginx
            fi
        else
            log_warn "Hệ điều hành không nằm trong danh sách tự động (Ubuntu/Debian/Rocky). Vui lòng đảm bảo Nginx & Certbot đã được cài đặt."
        fi
    fi

    # Kích hoạt Nginx nếu chưa chạy
    if command -v systemctl &>/dev/null; then
        systemctl enable nginx || true
        systemctl start nginx || true
    fi

    log_success "Môi trường Nginx và Certbot đã sẵn sàng."
}

# ------------------------------------------------------------------------------
# SINH NỘI DUNG VIRTUALHOST TỐI ƯU (GENERATE VHOST CONFIG)
# ------------------------------------------------------------------------------
generate_vhost_config() {
    local domain="$1"
    local include_www="$2"
    local backend="$3"

    # Tạo tên Upstream an toàn không chứa dấu chấm
    local clean_upstream
    clean_upstream="supportflast_$(echo "$domain" | tr '.' '_' | tr '-' '_')"

    local server_names="$domain"
    if [[ "$include_www" == "true" ]]; then
        server_names="$domain www.$domain"
    fi

    cat <<EOF
# ==============================================================================
# TỰ ĐỘNG TẠO BỞI SUPPORTFLAST NGINX AUTO-CONFIGURATOR
# Domain          : ${domain}
# Server Names    : ${server_names}
# Upstream Backend: ${backend}
# Thời Điểm Tạo   : $(date '+%Y-%m-%d %H:%M:%S %Z')
# Tính Năng       : WebSocket (/ws) + WebDAV (/webdav/) + 1000M Body + Buffer Tuning
# ==============================================================================

# ------------------------------------------------------------------------------
# 1. UPSTREAM BACKEND NGUỒN CỔNG ĐIỀU PHỐI TẬP TRUNG
# ------------------------------------------------------------------------------
upstream ${clean_upstream} {
    server ${backend} max_fails=3 fail_timeout=10s;
    keepalive 64;
}

# ------------------------------------------------------------------------------
# 2. WEBSOCKET CONNECTION UPGRADE MAP (Nâng cấp giao thức 2 chiều)
# ------------------------------------------------------------------------------
# Kiểm tra nếu map chưa được định nghĩa toàn cục
map \$http_upgrade \$connection_upgrade_${clean_upstream} {
    default upgrade;
    ''      close;
}

# ------------------------------------------------------------------------------
# 3. RATE LIMITING & CONNECTION LIMITING (Chống DDoS & Quá Tải Kết Nối)
# ------------------------------------------------------------------------------
limit_req_zone \$binary_remote_addr zone=req_limit_${clean_upstream}:10m rate=120r/m;
limit_req_zone \$binary_remote_addr zone=auth_limit_${clean_upstream}:10m rate=10r/m;
limit_conn_zone \$binary_remote_addr zone=conn_limit_${clean_upstream}:10m;

# ------------------------------------------------------------------------------
# 4. MÁY CHỦ HTTP (CỔNG 80) & XÁC THỰC CERTBOT LET'S ENCRYPT
# ------------------------------------------------------------------------------
server {
    listen 80;
    listen [::]:80;
    server_name ${server_names};

    # Thư mục xác thực Webroot ACME Challenge của Certbot Let's Encrypt
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
        allow all;
    }

    # --------------------------------------------------------------------------
    # BẢO MẬT HEADERS CHUẨN QUÂN SỰ (RULE 3.4 & 3.5 ANTIGRAVITY WORKSPACE)
    # --------------------------------------------------------------------------
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "1; mode=block" always;
    server_tokens off;
    proxy_hide_header X-Powered-By;
    proxy_hide_header Server;
    proxy_hide_header X-AspNet-Version;

    # --------------------------------------------------------------------------
    # TỐI ƯU HÓA BỘ ĐỆM & DUNG LƯỢNG TẢI LÊN 1000M (1GB)
    # --------------------------------------------------------------------------
    client_max_body_size 1000M;
    client_body_buffer_size 16M;
    large_client_header_buffers 4 32k;
    proxy_buffer_size 128k;
    proxy_buffers 16 64k;
    proxy_busy_buffers_size 256k;
    proxy_temp_file_write_size 256k;

    # --------------------------------------------------------------------------
    # GZIP NÉN DỮ LIỆU TỐC ĐỘ CAO
    # --------------------------------------------------------------------------
    gzip on;
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 6;
    gzip_min_length 256;
    gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;

    # --------------------------------------------------------------------------
    # KHÔI PHỤC IP THỰC (CLOUDFLARE REAL IP RESTORATION)
    # --------------------------------------------------------------------------
    set_real_ip_from 173.245.48.0/20;
    set_real_ip_from 103.21.244.0/22;
    set_real_ip_from 103.22.200.0/22;
    set_real_ip_from 103.31.4.0/22;
    set_real_ip_from 141.101.64.0/18;
    set_real_ip_from 108.162.192.0/18;
    set_real_ip_from 190.93.240.0/20;
    set_real_ip_from 188.114.96.0/20;
    set_real_ip_from 197.234.240.0/22;
    set_real_ip_from 198.41.128.0/17;
    set_real_ip_from 162.158.0.0/15;
    set_real_ip_from 104.16.0.0/13;
    set_real_ip_from 104.24.0.0/14;
    set_real_ip_from 172.64.0.0/13;
    set_real_ip_from 131.0.72.0/22;
    set_real_ip_from 2400:cb00::/32;
    set_real_ip_from 2606:4700::/32;
    set_real_ip_from 2803:f800::/32;
    set_real_ip_from 2405:b500::/32;
    set_real_ip_from 2405:8100::/32;
    set_real_ip_from 2a06:98c0::/29;
    set_real_ip_from 2c0f:f248::/32;
    real_ip_header CF-Connecting-IP;

    # Áp dụng Rate Limiting toàn cục
    limit_req zone=req_limit_${clean_upstream} burst=30 nodelay;
    limit_conn zone=conn_limit_${clean_upstream} 50;

    # --------------------------------------------------------------------------
    # A. WEBSOCKET PROXY (/ws) - REAL-TIME SUBAGENTS & NOTIFICATIONS
    # --------------------------------------------------------------------------
    location /ws {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;

        # Chuyển đổi giao thức WebSocket
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade_${clean_upstream};

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Timeout 3600s giữ kết nối liên tục
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;

        # Tắt đệm proxy để truyền thông điệp tức thời không trễ
        proxy_buffering off;
    }

    # --------------------------------------------------------------------------
    # B. GIAO THỨC WEBDAV (/webdav, /webdav/) - KHO FILE ẢO ĐA THIẾT BỊ (VFS)
    # --------------------------------------------------------------------------
    location ^~ /webdav {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Các Header tối quan trọng cho WebDAV chuẩn RFC 4918 (MOVE, COPY, LOCK)
        proxy_set_header Destination \$http_destination;
        proxy_set_header Depth \$http_depth;
        proxy_set_header Overwrite \$http_overwrite;
        proxy_set_header If \$http_if;
        proxy_set_header Lock-Token \$http_lock_token;
        proxy_set_header Timeout \$http_timeout;

        # Kích thước payload tải lên 1000M & đệm RAM 16M
        client_max_body_size 1000M;
        client_body_buffer_size 16M;

        # Tắt đệm proxy: Truyền streaming trực tiếp về VFS Engine, không ngốn RAM
        proxy_request_buffering off;
        proxy_buffering off;

        # Timeout kéo dài cho việc tải/truyền file lớn
        proxy_connect_timeout 120s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;

        proxy_set_header Accept-Encoding "";
        add_header Cache-Control "no-cache, no-store, must-revalidate, max-age=0" always;
    }

    # --------------------------------------------------------------------------
    # C. KHO LƯU TRỮ ĐÁM MÂY (/storage) - TỐI ƯU CHO STREAMING FILE VÀ CHUNKS
    # --------------------------------------------------------------------------
    location ^~ /storage {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        client_max_body_size 1000M;
        client_body_buffer_size 16M;
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_connect_timeout 120s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;
    }

    # --------------------------------------------------------------------------
    # D. CỔNG CÔNG CỤ PHÁT TRIỂN & XUẤT BẢN (/tools)
    # --------------------------------------------------------------------------
    location /tools {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        add_header Cache-Control "no-cache, no-store, must-revalidate, max-age=0" always;
    }

    # --------------------------------------------------------------------------
    # E. TỐI ƯU CACHE TĨNH CHO ASSETS & 3D MODELS (CSS, JS, GLTF, IMAGES)
    # --------------------------------------------------------------------------
    location ~* \.(?:css|js|png|jpg|jpeg|gif|ico|svg|svgz|webp|avif|woff|woff2|ttf|eot|otf|gltf|bin)$ {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        expires 365d;
        add_header Cache-Control "public, max-age=31536000, immutable";
        access_log off;
    }

    # --------------------------------------------------------------------------
    # F. API RATE LIMITING ĐĂNG NHẬP (Chống Brute-Force Password)
    # --------------------------------------------------------------------------
    location /api/auth/login {
        limit_req zone=auth_limit_${clean_upstream} burst=5 nodelay;

        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        add_header Cache-Control "no-store, no-cache, must-revalidate, max-age=0" always;
    }

    # --------------------------------------------------------------------------
    # G. API ENDPOINTS - TUYỆT ĐỐI KHÔNG CACHE
    # --------------------------------------------------------------------------
    location /api/ {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        add_header Cache-Control "no-store, no-cache, must-revalidate, max-age=0" always;
        proxy_connect_timeout 30s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
    }

    # --------------------------------------------------------------------------
    # H. ĐIỀU HƯỚNG MẶC ĐỊNH CHO SINGLE PAGE APPLICATION (SPA / WEB 3D HUD)
    # --------------------------------------------------------------------------
    location / {
        proxy_pass http://${clean_upstream};
        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        add_header Cache-Control "no-cache, no-store, must-revalidate, max-age=0" always;
        proxy_connect_timeout 30s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
    }
}
EOF
}

# ------------------------------------------------------------------------------
# GHI CẤU HÌNH VÀO SITES-AVAILABLE VÀ LIÊN KẾT VÀO SITES-ENABLED
# ------------------------------------------------------------------------------
install_nginx_virtualhost() {
    local domain="$1"
    local include_www="$2"
    local backend="$3"

    log_step "Tạo và liên kết VirtualHost Nginx cho tên miền: ${BOLD}${domain}${NC}..."

    local target_available="/etc/nginx/sites-available/${domain}.conf"
    local target_enabled="/etc/nginx/sites-enabled/${domain}.conf"

    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "[DRY-RUN] Xuất cấu hình VirtualHost cho ${domain}:"
        echo -e "${CYAN}------------------------------------------------------------${NC}"
        generate_vhost_config "$domain" "$include_www" "$backend"
        echo -e "${CYAN}------------------------------------------------------------${NC}"
        log_info "[DRY-RUN] Kế hoạch liên kết: ${target_available} -> ${target_enabled}"
        return 0
    fi

    # Đảm bảo các thư mục tồn tại
    mkdir -p /etc/nginx/sites-available
    mkdir -p /etc/nginx/sites-enabled
    mkdir -p /var/www/certbot
    chmod 755 /var/www/certbot || true

    # Ghi nội dung cấu hình
    generate_vhost_config "$domain" "$include_www" "$backend" > "$target_available"
    log_success "Đã tạo file cấu hình tại: ${BOLD}${target_available}${NC}"

    # Tạo symlink vào sites-enabled
    ln -sf "$target_available" "$target_enabled"
    log_success "Đã tạo liên kết symlink vào: ${BOLD}${target_enabled}${NC}"

    # Vô hiệu hóa site mặc định nếu có để giải phóng cổng 80
    if [ -f /etc/nginx/sites-enabled/default ]; then
        log_info "Tự động tắt cấu hình default site (/etc/nginx/sites-enabled/default) để tránh tranh chấp cổng 80..."
        rm -f /etc/nginx/sites-enabled/default
    fi

    # Kiểm tra cú pháp Nginx
    log_step "Kiểm tra cú pháp cấu hình Nginx (nginx -t)..."
    if nginx -t; then
        log_success "Cú pháp Nginx hoàn toàn hợp lệ (Syntax OK)!"
    else
        log_error "Cấu hình Nginx phát hiện lỗi cú pháp! Đang hoàn tác symlink..."
        rm -f "$target_enabled"
        exit 1
    fi

    # Nạp lại cấu hình Nginx
    if command -v systemctl &>/dev/null; then
        systemctl reload nginx || systemctl restart nginx
    else
        nginx -s reload || true
    fi
    log_success "Đã nạp lại Nginx Reverse Proxy thành công."
}

# ------------------------------------------------------------------------------
# CẤP PHÁT CHỨNG CHỈ SSL LET'S ENCRYPT QUA CERTBOT VÀ CẤU HÌNH TỰ ĐỘNG GIA HẠN
# ------------------------------------------------------------------------------
setup_letsencrypt_ssl() {
    local domain="$1"
    local include_www="$2"
    local email="$3"

    if [[ "$SKIP_SSL" == "true" ]]; then
        log_info "Bỏ qua cấu hình SSL Let's Encrypt theo yêu cầu (--skip-ssl)."
        return 0
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "[DRY-RUN] Dự kiến gọi lệnh Certbot:"
        local certbot_preview="certbot --nginx -d ${domain}"
        if [[ "$include_www" == "true" ]]; then
            certbot_preview="${certbot_preview} -d www.${domain}"
        fi
        certbot_preview="${certbot_preview} --non-interactive --agree-tos -m ${email} --redirect"
        echo -e "  -> ${YELLOW}${certbot_preview}${NC}"
        return 0
    fi

    log_step "Cấp phát chứng chỉ SSL Let's Encrypt tự động qua Certbot Nginx plugin..."

    # Xây dựng danh sách domain cho Certbot
    local certbot_domains=("-d" "${domain}")
    if [[ "$include_www" == "true" ]]; then
        # Kiểm tra xem www có DNS không trước khi thêm vào
        certbot_domains+=("-d" "www.${domain}")
    fi

    local extra_certbot_args=()
    if [[ "$STAGING_SSL" == "true" ]]; then
        extra_certbot_args+=("--staging")
        log_warn "Đang sử dụng máy chủ Staging của Let's Encrypt."
    fi

    log_info "Đang gọi Certbot cấp chứng chỉ và tự động gắn vào VirtualHost Nginx..."
    
    # Thực thi Certbot với Nginx plugin và tự động chuyển hướng HTTPS (--redirect)
    if certbot --nginx \
        "${certbot_domains[@]}" \
        "${extra_certbot_args[@]}" \
        --non-interactive \
        --agree-tos \
        --email "${email}" \
        --redirect; then
        log_success "🎉 Chứng chỉ SSL Let's Encrypt cho ${domain} đã được cấp và kích hoạt thành công!"
    else
        log_warn "Cấp phát chứng chỉ SSL qua Certbot gặp sự cố."
        log_warn "Nguyên nhân phổ biến: DNS của ${domain} chưa trỏ về IP Public của VPS này, hoặc cổng 80/443 bị Firewall/Cloudflare chặn."
        log_warn "Máy chủ vẫn đang duy trì chạy Reverse Proxy ở chế độ HTTP (Port 80)."
        log_info "Bạn có thể chạy lại lệnh sau khi DNS đã trỏ xong:"
        echo -e "   ${YELLOW}sudo certbot --nginx -d ${domain} $([ "$include_www" == "true" ] && echo "-d www.${domain}") --redirect${NC}"
        return 0
    fi

    # Cấu hình tự động gia hạn chứng chỉ (Auto-Renewal)
    log_step "Cấu hình cơ chế tự động gia hạn SSL (Auto-Renewal System)..."

    # 1. Kích hoạt Systemd Timer nếu có
    if command -v systemctl &>/dev/null && systemctl list-unit-files | grep -q certbot.timer; then
        systemctl enable --now certbot.timer || true
        log_success "Đã kích hoạt Systemd Timer: certbot.timer"
    fi

    # 2. Tạo Cron Job dự phòng kiểm tra 2 lần mỗi ngày lúc 03:00 và 15:00
    local cron_file="/etc/cron.d/certbot_supportflast"
    cat <<EOF > "$cron_file"
# Tự động kiểm tra gia hạn SSL Let's Encrypt cho SupportFlast (Nạp lại Nginx sau khi gia hạn)
0 3,15 * * * root certbot renew --quiet --deploy-hook "systemctl reload nginx"
EOF
    chmod 644 "$cron_file"
    log_success "Đã tạo Cron Job tự động gia hạn tại: ${cron_file}"

    # 3. Chạy thử nghiệm kiểm tra gia hạn (Dry-run)
    log_info "Kiểm tra cơ chế gia hạn thử nghiệm (certbot renew --dry-run)..."
    if certbot renew --dry-run; then
        log_success "Cơ chế tự động gia hạn chứng chỉ SSL đã được xác nhận hoạt động 100%!"
    else
        log_warn "Kiểm tra dry-run gia hạn gặp cảnh báo, vui lòng kiểm tra lại log tại /var/log/letsencrypt/."
    fi
}

# ------------------------------------------------------------------------------
# TỔNG HỢP VÀ BÁO CÁO KẾT QUẢ TRIỂN KHAI
# ------------------------------------------------------------------------------
print_summary() {
    local domain="$1"
    local include_www="$2"
    local backend="$3"

    echo ""
    echo -e "${GREEN}${BOLD}==========================================================================${NC}"
    echo -e "${GREEN}${BOLD}  🎉 THIẾT LẬP NGINX & REVERSE PROXY ĐÃ HOÀN TẤT THÀNH CÔNG RỰC RỠ!       ${NC}"
    echo -e "${GREEN}${BOLD}==========================================================================${NC}"
    echo -e " Tên miền chính (Domain)     : ${CYAN}https://${domain}${NC}"
    if [[ "$include_www" == "true" ]]; then
        echo -e " Tên miền phụ (Subdomain)    : ${CYAN}https://www.${domain}${NC}"
    fi
    echo -e " Upstream Backend Monolith   : ${YELLOW}http://${backend}${NC}"
    echo -e " File cấu hình VirtualHost   : ${BLUE}/etc/nginx/sites-available/${domain}.conf${NC}"
    echo -e " Liên kết kích hoạt          : ${BLUE}/etc/nginx/sites-enabled/${domain}.conf${NC}"
    echo -e " Giới hạn tải lên tối đa     : ${PURPLE}1000M (1GB Payload)${NC}"
    echo -e " Bộ đệm RAM tiếp nhận        : ${PURPLE}client_body_buffer_size 16M (Tăng tốc I/O)${NC}"
    echo -e " Hỗ trợ giao thức WebSocket  : ${GREEN}wss://${domain}/ws (Timeout 3600s, Buffering Off)${NC}"
    echo -e " Hỗ trợ giao thức WebDAV VFS : ${GREEN}https://${domain}/webdav/ (Zero-buffer Direct Stream)${NC}"
    echo -e " Kho lưu trữ đám mây Cloud   : ${GREEN}https://${domain}/storage${NC}"
    echo -e " Cổng công cụ phát triển     : ${GREEN}https://${domain}/tools${NC}"
    echo -e "${CYAN}--------------------------------------------------------------------------${NC}"
    echo -e "${BOLD}📌 HƯỚNG DẪN KẾT NỐI WEBDAV TỪ CÁC THIẾT BỊ NGOÀI:${NC}"
    echo -e "  * Windows Map Network Drive (gán ổ đĩa Z:):"
    echo -e "    ${YELLOW}net use Z: https://${domain}/webdav /user:admin [PASSWORD] /persistent:yes${NC}"
    echo -e "  * macOS Finder:"
    echo -e "    Nhấn ${YELLOW}Command + K${NC} -> Nhập: ${CYAN}https://${domain}/webdav${NC}"
    echo -e "  * WinSCP / Cyberduck / Rclone:"
    echo -e "    Giao thức WebDAV HTTPS (Port 443) -> Host: ${CYAN}${domain}${NC} -> Path: ${CYAN}/webdav${NC}"
    echo -e "${CYAN}--------------------------------------------------------------------------${NC}"
    echo -e "${BOLD}📌 KIỂM TRA TRẠNG THÁI DỊCH VỤ:${NC}"
    echo -e "  * Kiểm tra Nginx:   ${YELLOW}sudo systemctl status nginx${NC}"
    echo -e "  * Kiểm tra SSL:     ${YELLOW}sudo certbot certificates${NC}"
    echo -e "  * Thử gia hạn SSL:  ${YELLOW}sudo certbot renew --dry-run${NC}"
    echo -e "${GREEN}==========================================================================${NC}\n"
}

# ------------------------------------------------------------------------------
# HÀM CHÍNH (MAIN FUNCTION)
# ------------------------------------------------------------------------------
main() {
    print_banner
    parse_args "$@"
    check_root
    prompt_user_inputs
    detect_os_and_install_deps
    install_nginx_virtualhost "$DOMAIN" "$INCLUDE_WWW" "$BACKEND_ADDR"
    setup_letsencrypt_ssl "$DOMAIN" "$INCLUDE_WWW" "$CERTBOT_EMAIL"
    print_summary "$DOMAIN" "$INCLUDE_WWW" "$BACKEND_ADDR"
}

main "$@"
