#!/usr/bin/env bash
# ==============================================================================
# Script Tự Động Sao Lưu Dữ Liệu SQLite Định Kỳ (Zero-Downtime Hot Backup)
# Dự án: supportflastdev.io.vn
# Tuân thủ: SQLite Online Backup API, WAL Safe, Integrity Verification, Auto Retention
# ==============================================================================

set -euo pipefail

# Màu hiển thị terminal
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()  { echo -e "[BACKUP] $(date '+%Y-%m-%d %H:%M:%S') ${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "[BACKUP] $(date '+%Y-%m-%d %H:%M:%S') ${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "[BACKUP] $(date '+%Y-%m-%d %H:%M:%S') ${RED}[ERROR]${NC} $1"; }

# ------------------------------------------------------------------------------
# 1. Cấu hình đường dẫn và biến môi trường
# ------------------------------------------------------------------------------
APP_DIR="${APP_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
DATA_DIR="${DATA_DIR:-$APP_DIR/data}"
DB_FILE="${DB_FILE:-$DATA_DIR/supportflast.db}"
BACKUP_ROOT="${BACKUP_ROOT:-$APP_DIR/backups/sqlite}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"

TIMESTAMP=$(date '+%Y%m%d_%H%M%S')
TEMP_WORK_DIR=$(mktemp -d -t sf-backup-XXXXXX)
BACKUP_TAR="$BACKUP_ROOT/supportflast_db_backup_$TIMESTAMP.tar.gz"

cleanup() {
    rm -rf "$TEMP_WORK_DIR"
}
trap cleanup EXIT

log_info "Bắt đầu tiến trình sao lưu cơ sở dữ liệu cho supportflastdev.io.vn..."
log_info "Thư mục làm việc: $APP_DIR"
log_info "Tệp CSDL SQLite: $DB_FILE"
log_info "Thư mục lưu trữ bản sao lưu: $BACKUP_ROOT"

# Tạo thư mục chứa backup nếu chưa có
mkdir -p "$BACKUP_ROOT"

# Kiểm tra sự tồn tại của database
if [ ! -f "$DB_FILE" ]; then
    log_error "Không tìm thấy file CSDL tại: $DB_FILE"
    exit 1
fi

# Đảm bảo công cụ sqlite3 đã được cài đặt
if ! command -v sqlite3 &> /dev/null; then
    log_error "Lệnh 'sqlite3' chưa được cài đặt trên hệ thống. Hãy cài đặt: sudo apt-get install -y sqlite3"
    exit 1
fi

# ------------------------------------------------------------------------------
# 2. Kiểm tra tính toàn vẹn (Integrity Check) trước khi sao lưu
# ------------------------------------------------------------------------------
log_info "Đang kiểm tra tính toàn vẹn CSDL (PRAGMA integrity_check)..."
INTEGRITY_CHECK=$(sqlite3 "$DB_FILE" "PRAGMA integrity_check;" 2>&1 || true)
if [ "$INTEGRITY_CHECK" != "ok" ]; then
    log_error "Phát hiện CSDL bị lỗi toàn vẹn trước khi sao lưu: $INTEGRITY_CHECK"
    log_error "Tiến trình sao lưu bị hủy để tránh ghi đè dữ liệu hỏng vào kho lưu trữ!"
    exit 2
fi
log_info "Kiểm tra toàn vẹn thành công: $INTEGRITY_CHECK"

# ------------------------------------------------------------------------------
# 3. Thực hiện Hot Backup an toàn (WAL Safe - Zero Downtime)
# Tuyệt đối không dùng lệnh 'cp' trực tiếp vì có thể mất transaction trong file -wal
# ------------------------------------------------------------------------------
TEMP_DB_BACKUP="$TEMP_WORK_DIR/supportflast.db"
log_info "Đang tạo bản chụp Snapshot an toàn thông qua SQLite Online Backup API..."

# Thử nghiệm VACUUM INTO trước (nhanh và dọn dẹp phân mảnh), nếu phiên bản sqlite3 cũ hơn 3.27 thì dùng .backup
if sqlite3 "$DB_FILE" "VACUUM INTO '$TEMP_DB_BACKUP';" 2>/dev/null; then
    log_info "Thực thi VACUUM INTO thành công (Đã tối ưu hóa và chống phân mảnh)."
else
    log_warn "Hệ thống sử dụng SQLite Online Backup API (.backup) dự phòng..."
    sqlite3 "$DB_FILE" ".backup '$TEMP_DB_BACKUP'"
fi

# Kiểm tra file snapshot vừa tạo
if [ ! -f "$TEMP_DB_BACKUP" ] || [ ! -s "$TEMP_DB_BACKUP" ]; then
    log_error "Bản snapshot CSDL rỗng hoặc không được tạo thành công!"
    exit 3
fi

# Xác minh lại tính toàn vẹn của tệp snapshot vừa tạo
VERIFY_BACKUP=$(sqlite3 "$TEMP_DB_BACKUP" "PRAGMA quick_check;" 2>&1 || true)
if [ "$VERIFY_BACKUP" != "ok" ]; then
    log_error "Bản snapshot CSDL không vượt qua kiểm tra quick_check: $VERIFY_BACKUP"
    exit 4
fi

# Sao chép thêm các tệp JSON cấu hình/metadata nếu có trong data/
if [ -d "$DATA_DIR" ]; then
    cp -u "$DATA_DIR"/*.json "$TEMP_WORK_DIR/" 2>/dev/null || true
fi

# Ghi metadata của lần sao lưu
cat <<EOF > "$TEMP_WORK_DIR/backup_metadata.json"
{
  "project": "supportflastdev.io.vn",
  "backup_timestamp": "$TIMESTAMP",
  "created_at_utc": "$(date -u '+%Y-%m-%dT%H:%M:%SZ')",
  "source_db_size_bytes": $(wc -c < "$DB_FILE"),
  "snapshot_size_bytes": $(wc -c < "$TEMP_DB_BACKUP"),
  "integrity_status": "ok",
  "retention_policy_days": $RETENTION_DAYS
}
EOF

# ------------------------------------------------------------------------------
# 4. Nén lưu trữ nén cao (.tar.gz)
# ------------------------------------------------------------------------------
log_info "Đang nén dữ liệu sao lưu thành định dạng tar.gz..."
tar -czf "$BACKUP_TAR" -C "$TEMP_WORK_DIR" .

BACKUP_SIZE=$(du -h "$BACKUP_TAR" | awk '{print $1}')
log_info "${GREEN}Tạo bản sao lưu thành công!${NC} Tệp: $BACKUP_TAR (Kích thước: $BACKUP_SIZE)"

# ------------------------------------------------------------------------------
# 5. Xoay vòng bản sao lưu (Auto Retention - Xóa bản cũ hơn RETENTION_DAYS ngày)
# ------------------------------------------------------------------------------
log_info "Dọn dẹp các bản sao lưu cũ hơn $RETENTION_DAYS ngày..."
DELETED_COUNT=0
while IFS= read -r old_file; do
    if [ -n "$old_file" ]; then
        rm -f "$old_file"
        log_warn "Đã xóa bản sao lưu cũ: $(basename "$old_file")"
        DELETED_COUNT=$((DELETED_COUNT + 1))
    fi
done < <(find "$BACKUP_ROOT" -name "supportflast_db_backup_*.tar.gz" -type f -mtime "+$RETENTION_DAYS")

log_info "Hoàn tất chính sách lưu trữ. Số tệp đã dọn dẹp: $DELETED_COUNT"

# ------------------------------------------------------------------------------
# 6. Hướng dẫn phục hồi nhanh (Disaster Recovery Quick Info)
# ------------------------------------------------------------------------------
log_info "------------------------------------------------------------"
log_info "HƯỚNG DẪN KHÔI PHỤC KHI CẦN (DISASTER RECOVERY):"
log_info "  1. Dừng hệ thống: systemctl stop supportflast-engine (hoặc docker compose down)"
log_info "  2. Giải nén bản backup:"
log_info "     tar -xzf $BACKUP_TAR -C /tmp/sf_restore"
log_info "  3. Thay thế file db:"
log_info "     cp /tmp/sf_restore/supportflast.db $DB_FILE"
log_info "  4. Khởi động lại: systemctl start supportflast-engine (hoặc docker compose up -d)"
log_info "------------------------------------------------------------"
log_info "${GREEN}Tiến trình sao lưu hoàn tất 100% không gián đoạn dịch vụ.${NC}"
exit 0
