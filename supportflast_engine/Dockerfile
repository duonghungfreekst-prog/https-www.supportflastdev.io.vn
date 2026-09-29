# ==============================================================================
# SupportFlast Go Engine - Standalone Dockerfile (supportflast_engine)
# Kiến trúc: Multi-stage build tối ưu, bảo mật non-root (Rule 1.4, 2.2, 7.1)
# Domain: supportflastdev.io.vn | Port: 8080
# ==============================================================================

# --- Stage 1: Build binary thuần bằng Golang Alpine ---
FROM golang:alpine AS builder

WORKDIR /build

# Cài đặt git, ca-certificates và tzdata
RUN apk add --no-cache git ca-certificates tzdata
ENV GOTOOLCHAIN=auto

# Copy các tệp dependency
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy mã nguồn Go
COPY . .

# Biên dịch binary tĩnh 100% không phụ thuộc CGO (pure Go với modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
    -trimpath \
    -o supportflast .

# --- Stage 2: Runtime image tối giản (Alpine) ---
FROM alpine:latest AS runner

# Cài đặt timezone, chứng chỉ SSL và curl phục vụ health check
RUN apk add --no-cache ca-certificates tzdata curl sqlite \
    && cp /usr/share/zoneinfo/Asia/Ho_Chi_Minh /etc/localtime \
    && echo "Asia/Ho_Chi_Minh" > /etc/timezone

WORKDIR /app

# Tạo non-root user (appuser:appgroup) với UID/GID 10001
RUN addgroup -S -g 10001 appgroup && \
    adduser -S -u 10001 -G appgroup -h /app -s /sbin/nologin appuser

# Tạo cấu trúc thư mục dữ liệu và phân phối ứng dụng
RUN mkdir -p /app/data /app/data/keys /app/storage/packages /app/supportflast_ui

# Copy binary từ build stage
COPY --from=builder --chown=appuser:appgroup /build/supportflast /app/supportflast

# Phân quyền cho appuser
RUN chmod +x /app/supportflast && \
    chown -R appuser:appgroup /app && \
    chmod -R 755 /app && \
    chmod -R 775 /app/data /app/storage

# Chuyển sang user bảo mật
USER appuser:appgroup

# Biến môi trường mặc định
ENV PORT=8080 \
    HOST=0.0.0.0 \
    DOMAIN=supportflastdev.io.vn \
    DATA_DIR=/app/data \
    STORAGE_DIR=/app/storage \
    UI_DIR=/app/supportflast_ui \
    STATIC_DIR=/app/supportflast_ui \
    JWT_KEYS_DIR=/app/data/keys \
    AI_ENGINE_URL=http://supportflast-ai:8000 \
    TZ=Asia/Ho_Chi_Minh

# Mở duy nhất 1 cổng 8080
EXPOSE 8080

# Health check container
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://127.0.0.1:8080/api/health || exit 1

# Khởi chạy dịch vụ
CMD ["./supportflast"]
