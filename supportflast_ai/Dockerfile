# ==============================================================================
# Dockerfile cho Python Subagent AI Service (supportflast_ai)
# Điều phối 5 Subagents chuyên trách: Triage, Tech, Infra, Security, Billing
# Domain: supportflastdev.io.vn
# ==============================================================================

FROM python:3.11-slim

WORKDIR /app

# Thiết lập biến môi trường Python tối ưu
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HOST=0.0.0.0 \
    PORT=8000

# Cài đặt gói hệ thống tối thiểu cần thiết cho bảo mật và health check
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Cài đặt Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# Tạo user không đặc quyền để tăng cường an ninh container
RUN groupadd -r aiuser && useradd -r -g aiuser aiuser

# Copy mã nguồn dịch vụ AI
COPY . .

# Tạo thư mục phân tích tạm và phân quyền an toàn
RUN mkdir -p /app/tmp_scans /app/storage && \
    chown -R aiuser:aiuser /app && \
    chmod -R 755 /app

USER aiuser

EXPOSE 8000

# Kiểm tra trạng thái sức khỏe định kỳ
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://127.0.0.1:8000/api/health || exit 1

# Khởi chạy FastAPI thông qua Uvicorn ASGI Server
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "2"]
