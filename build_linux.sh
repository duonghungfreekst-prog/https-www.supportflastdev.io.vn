#!/usr/bin/env bash
# ==============================================================================
# SCRIPT BIÊN DỊCH CROSS-COMPILATION CHO LINUX (supportflastdev.io.vn)
# Dự án: Cổng Đăng Tải Ứng Dụng & Điều Phối 5 Subagents
# Tạo các binary Linux tĩnh (Static) độc lập cho cả x86_64 (amd64) và ARM64.
# ==============================================================================

set -euo pipefail

# Định dạng màu ANSI
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
PURPLE='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="${SCRIPT_DIR}/supportflast_engine"
OUTPUT_AMD64="${ENGINE_DIR}/supportflast-linux-amd64"
OUTPUT_ARM64="${ENGINE_DIR}/supportflast-linux-arm64"

echo -e "${PURPLE}${BOLD}"
echo "======================================================================="
echo "     🚀 SUPPORTFLAST - BIÊN DỊCH LINUX STATIC BINARIES"
echo "        Hỗ trợ kiến trúc: Linux x86_64 (amd64) và Linux ARM64"
echo "                  Tên miền: supportflastdev.io.vn"
echo "======================================================================="
echo -e "${NC}"

# 1. Kiểm tra Go
if ! command -v go &>/dev/null; then
    echo -e "${RED}[LỖI] Không tìm thấy Go trong hệ thống! Vui lòng cài đặt Go.${NC}"
    exit 1
fi

GO_VER=$(go version)
echo -e "${BLUE}[INFO] Trình biên dịch: ${GO_VER}${NC}\n"

cd "${ENGINE_DIR}"

# 2. Biên dịch Linux x86_64 (amd64)
echo -e "${YELLOW}>>> [1/2] Đang biên dịch Linux x86_64 (amd64)...${NC}"
echo "    GOOS=linux, GOARCH=amd64, CGO_ENABLED=0"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
    -trimpath \
    -o "${OUTPUT_AMD64}" .

chmod +x "${OUTPUT_AMD64}"
echo -e "${GREEN}[THÀNH CÔNG] Đã tạo file thực thi: ${OUTPUT_AMD64}${NC}\n"

# 3. Biên dịch Linux ARM64 (arm64)
echo -e "${YELLOW}>>> [2/2] Đang biên dịch Linux ARM64 (arm64)...${NC}"
echo "    GOOS=linux, GOARCH=arm64, CGO_ENABLED=0"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" \
    -trimpath \
    -o "${OUTPUT_ARM64}" .

chmod +x "${OUTPUT_ARM64}"
echo -e "${GREEN}[THÀNH CÔNG] Đã tạo file thực thi: ${OUTPUT_ARM64}${NC}\n"

cd "${SCRIPT_DIR}"

# 4. Hiển thị kết quả
echo -e "${PURPLE}${BOLD}======================================================================="
echo "           🎉 BIÊN DỊCH LINUX STATIC BINARIES THÀNH CÔNG!"
echo "=======================================================================${NC}"
echo -e "Các file nhị phân Linux tĩnh sẵn sàng triển khai:"

if [ -f "${OUTPUT_AMD64}" ]; then
    SIZE_AMD64=$(ls -lh "${OUTPUT_AMD64}" | awk '{print $5}')
    echo -e "  ${GREEN}✔ [x86_64 / amd64]:${NC} supportflast_engine/supportflast-linux-amd64 (${SIZE_AMD64})"
fi

if [ -f "${OUTPUT_ARM64}" ]; then
    SIZE_ARM64=$(ls -lh "${OUTPUT_ARM64}" | awk '{print $5}')
    echo -e "  ${GREEN}✔ [ARM64 / aarch64]:${NC} supportflast_engine/supportflast-linux-arm64 (${SIZE_ARM64})"
fi

echo ""
echo -e "${BLUE}======================================================================="
echo "                 HƯỚNG DẪN SỬ DỤNG TRÊN VPS / SERVER"
echo "=======================================================================${NC}"
echo "1. Upload binary phù hợp lên VPS (ví dụ: scp supportflast-linux-amd64 user@vps:/opt/)"
echo "2. Cấp quyền thực thi: chmod +x supportflast-linux-amd64"
echo "3. Chạy trực tiếp mà KHÔNG CẦN CÀI ĐẶT GO trên VPS:"
echo "   ./supportflast-linux-amd64"
echo -e "${BLUE}=======================================================================${NC}\n"
