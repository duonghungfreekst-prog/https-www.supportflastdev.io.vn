#!/bin/sh
set -e

# ==============================================================================
# SupportFlast Go Monolith Engine - Universal Multi-Arch Entrypoint Wrapper
# Tự động nhận diện và thích ứng biến $PORT động (Cloud Run, Render, Railway, Fly.io, VPS)
# Kiến trúc: Static Pure-Go Binary (modernc.org/sqlite, CGO_ENABLED=0, Non-Root UID 10001)
# ==============================================================================

# 1. Nhận diện cổng PORT động từ môi trường hoặc fallback về 8080
if [ -n "$PORT" ]; then
    echo "[ENTRYPOINT] Detected dynamic environment PORT=${PORT}"
else
    export PORT="8080"
    echo "[ENTRYPOINT] PORT not set, defaulting to 8080"
fi

# 2. Đảm bảo HOST luôn lắng nghe 0.0.0.0 trong container
export HOST="${HOST:-0.0.0.0}"

# 2.1 Bảo vệ bộ nhớ Go Engine chống OOM trên môi trường Cloud (Rule 7.1)
export GOMEMLIMIT="${GOMEMLIMIT:-384MiB}"
export GOGC="${GOGC:-80}"
echo "[ENTRYPOINT] Memory limits: GOMEMLIMIT=${GOMEMLIMIT} | GOGC=${GOGC}"

# 3. Đảm bảo cấu trúc thư mục dữ liệu và phân quyền khả dụng
DATA_DIR="${DATA_DIR:-/app/data}"
STORAGE_DIR="${STORAGE_DIR:-/app/storage}"
JWT_KEYS_DIR="${JWT_KEYS_DIR:-${DATA_DIR}/keys}"

mkdir -p "$DATA_DIR" "$STORAGE_DIR" "$JWT_KEYS_DIR" 2>/dev/null || true

echo "[ENTRYPOINT] System parameters: PORT=${PORT} | HOST=${HOST} | USER=$(id -un 2>/dev/null || echo appuser):$(id -gn 2>/dev/null || echo appgroup) (UID:$(id -u 2>/dev/null || echo 10001))"
echo "[ENTRYPOINT] Directories: DATA_DIR=${DATA_DIR} | STORAGE_DIR=${STORAGE_DIR}"

# 3.1. Đánh giá chiến lược lưu trữ & kiểm tra khả năng tự phục hồi (Zero-Downtime Resilience)
DB_DRIVER="${DB_DRIVER:-tidb}"
CLOUDPOOL_DB_DRIVER="${CLOUDPOOL_DB_DRIVER:-$DB_DRIVER}"
echo "[ENTRYPOINT] Database drivers: DB_DRIVER=${DB_DRIVER} | CLOUDPOOL_DB_DRIVER=${CLOUDPOOL_DB_DRIVER}"

if [ "$DB_DRIVER" = "tidb" ] || [ "$DB_DRIVER" = "mysql" ]; then
    echo "[ENTRYPOINT] Primary Storage: TiDB Cloud Serverless (${TIDB_HOST:-gateway01.ap-southeast-1.prod.aws.tidbcloud.com}:${TIDB_PORT:-4000}/${TIDB_DATABASE:-supportflast})"
    echo "[ENTRYPOINT] Resilience: Auto-fallback to embedded SQLite enabled if TiDB Cloud is unreachable"
else
    echo "[ENTRYPOINT] Primary Storage: Embedded SQLite (${DATA_DIR}/supportflast.db)"
fi

# Kiểm tra tính sẵn sàng của file seed dữ liệu ban đầu
if [ -f "${DATA_DIR}/apps.json" ]; then
    echo "[ENTRYPOINT] Seed Metadata: ${DATA_DIR}/apps.json is READY (Auto-Bootstrap enabled)"
fi
if [ -f "${DATA_DIR}/keys.json" ]; then
    echo "[ENTRYPOINT] Key Metadata: ${DATA_DIR}/keys.json is READY"
fi

# 4. Kiểm tra sự tồn tại và quyền thực thi của binary
APP_BIN="/app/supportflast"
if [ ! -x "$APP_BIN" ] && [ -x "./supportflast" ]; then
    APP_BIN="./supportflast"
fi

# 5. Khởi chạy binary với signal trapping và truyền cờ linh hoạt
# Nếu không truyền tham số, hoặc tham số đầu tiên là binary chính
if [ $# -eq 0 ] || [ "$1" = "./supportflast" ] || [ "$1" = "supportflast" ] || [ "$1" = "/app/supportflast" ]; then
    if [ $# -gt 1 ]; then
        shift
        echo "[ENTRYPOINT] Executing ${APP_BIN} with arguments: $*"
        exec "$APP_BIN" "$@"
    else
        echo "[ENTRYPOINT] Executing ${APP_BIN} on port ${PORT}..."
        exec "$APP_BIN"
    fi
fi

# Nếu tham số bắt đầu bằng dấu gạch ngang (flags ví dụ -h, -v)
if [ "${1#-}" != "$1" ]; then
    echo "[ENTRYPOINT] Executing ${APP_BIN} with flags: $*"
    exec "$APP_BIN" "$@"
fi

# Nếu người dùng truyền lệnh tùy biến (ví dụ: sh, /bin/sh, wget, ...)
echo "[ENTRYPOINT] Executing custom command: $*"
exec "$@"
