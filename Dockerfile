# ==============================================================================
# SupportFlast Go Monolith Engine - Universal Multi-Arch Production Dockerfile
# Kiến trúc: Go Monolith (Hub, Virtual Storage, WebDAV, SIEM Client, Honeypot)
# Cấu trúc: Multi-stage build tối ưu kích thước, bảo mật non-root (Rule 1.4, 2.2, 7.1)
# Nền tảng: Hỗ trợ Multi-Arch (amd64 / arm64) tương thích Apple Silicon, Graviton, Oracle ARM
# Domain: supportflastdev.io.vn | Port: 8080 (hoặc PORT động từ Cloud Environment)
# ==============================================================================

# ------------------------------------------------------------------------------
# Stage 1: Builder (Golang Alpine Multi-Stage Multi-Arch Build)
# ------------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

# Tự động nhận diện kiến trúc đích từ Docker Buildx (amd64 / arm64 / v7 / ...)
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

# Cài đặt build tools, ca-certificates và tzdata
RUN apk add --no-cache ca-certificates git tzdata

# Bật tự động nhận diện toolchain tương thích phiên bản go.mod
ENV GOTOOLCHAIN=auto

# Tận dụng triệt để Docker layer caching: copy go.mod và go.sum trước
COPY supportflast_engine/go.mod supportflast_engine/go.sum ./
RUN go mod download && go mod verify

# Copy toàn bộ mã nguồn module supportflast_engine (Go Monolith Engine)
# Bao gồm: main.go, cache, cloudpool (vfs, storage, webdav, api), database, registry, security
COPY supportflast_engine/ ./

# Biên dịch binary tĩnh tối ưu cho môi trường Linux đa kiến trúc (Multi-Arch):
# - CGO_ENABLED=0 kết hợp modernc.org/sqlite (pure Go) tạo binary tĩnh 100%, không phụ thuộc libc hay thư viện ngoài
# - GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}: Hỗ trợ biên dịch chéo tức thì cho amd64 (x86_64) và arm64 (aarch64)
# - -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0": Loại bỏ symbol table và debug info
# - -trimpath: Xóa dấu vết đường dẫn file của host build để tăng cường bảo mật
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
    -trimpath \
    -o /build/supportflast .

# ------------------------------------------------------------------------------
# Stage 2: Runner (Alpine Production Minimal Multi-Arch Runtime Image)
# ------------------------------------------------------------------------------
FROM alpine:3.20 AS runner

# Cài đặt các gói tối cần thiết:
# - ca-certificates: HTTPS outbound API calls
# - tzdata: Múi giờ Việt Nam (Asia/Ho_Chi_Minh)
# - sqlite: Hỗ trợ kiểm tra/quản lý database SQLite cục bộ
# - wget & curl: Kiểm tra sức khỏe container (HEALTHCHECK) và gỡ lỗi kết nối
RUN apk --no-cache add ca-certificates tzdata sqlite wget curl \
    && cp /usr/share/zoneinfo/Asia/Ho_Chi_Minh /etc/localtime \
    && echo "Asia/Ho_Chi_Minh" > /etc/timezone

# Thiết lập non-root user (appuser:appgroup) với UID/GID 10001 cố định để tăng cường an ninh container (Rule 3)
RUN addgroup -S -g 10001 appgroup && \
    adduser -S -u 10001 -G appgroup -h /app -s /bin/sh appuser

WORKDIR /app

# Tạo cấu trúc thư mục cần thiết
RUN mkdir -p /app/data /app/data/keys /app/storage/packages /app/supportflast_ui

# Copy binary Go Monolith Engine đã biên dịch từ stage builder
COPY --from=builder --chown=appuser:appgroup /build/supportflast /app/supportflast

# Copy shell script wrapper nhận diện biến $PORT động
COPY --chown=appuser:appgroup entrypoint.sh /app/entrypoint.sh

# Copy toàn bộ giao diện Web UI (gồm cả thư mục con storage/ cho CloudPool Virtual Storage)
COPY --chown=appuser:appgroup supportflast_ui /app/supportflast_ui

# Copy cấu hình schema DDL và JSON metadata từ data/ (loại trừ toàn bộ database tĩnh .db/.sqlite nhờ .dockerignore)
# Khi container khởi chạy trên Render/Production, engine sẽ kết nối TiDB Cloud hoặc tự động khởi tạo CSDL mới tại /app/data
COPY --chown=appuser:appgroup data /app/data

# Copy thư mục storage/ (chứa các gói phần mềm packages được phân phối qua CDN)
COPY --chown=appuser:appgroup storage /app/storage

# Phân quyền chặt chẽ & Bảo vệ dữ liệu runtime:
# - Đảm bảo dọn dẹp sạch mọi file .db, .sqlite tĩnh nếu vô tình lọt vào build context (zero baked DB)
# - Chuẩn hóa line endings (LF) cho entrypoint wrapper
# - Cấp quyền thực thi cho binary Go Monolith Engine và entrypoint.sh
# - Cấp quyền đọc/ghi cho appuser vào /app/data (SQLite WAL/SHM, Keys) và /app/storage
RUN rm -f /app/data/*.db /app/data/*.sqlite* /app/data/*.db-wal /app/data/*.db-shm /app/data/*.db-journal 2>/dev/null || true && \
    sed -i 's/\r$//' /app/entrypoint.sh && \
    chmod +x /app/supportflast /app/entrypoint.sh && \
    chown -R appuser:appgroup /app && \
    chmod -R 755 /app && \
    chmod -R 775 /app/data /app/storage

# Chuyển sang non-root user bảo mật (UID 10001 - tuân thủ Rule 3.1 & Container Security Best Practices)
USER appuser:appgroup

# Các biến môi trường mặc định chuẩn cho Go Monolith Gateway Engine
ENV PORT=8080 \
    HOST=0.0.0.0 \
    DOMAIN=supportflastdev.io.vn \
    DATA_DIR=/app/data \
    STORAGE_DIR=/app/storage \
    UI_DIR=/app/supportflast_ui \
    STATIC_DIR=/app/supportflast_ui \
    JWT_KEYS_DIR=/app/data/keys \
    DB_DRIVER=tidb \
    CLOUDPOOL_DB_DRIVER=tidb \
    TIDB_PORT=4000 \
    TIDB_TLS=true \
    AI_ENGINE_URL=http://supportflast-ai:8000 \
    GOMEMLIMIT=384MiB \
    GOGC=80 \
    TZ=Asia/Ho_Chi_Minh

# Expose duy nhất 1 cổng dịch vụ 8080 (Go Monolith tích hợp toàn bộ Hub, Storage, WebDAV, Tools, SIEM)
EXPOSE 8080

# Healthcheck kiểm tra định kỳ trạng thái endpoint /api/health qua wget với nhận diện PORT động
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:${PORT:-8080}/api/health || curl -f http://127.0.0.1:${PORT:-8080}/api/health || exit 1

# Khởi chạy bằng shell script wrapper nhận diện biến $PORT động
ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["./supportflast"]
