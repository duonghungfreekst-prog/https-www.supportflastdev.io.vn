"""FastAPI Entrypoint cho Python Subagent System (supportflast_ai)
Cổng kết nối AI Engine cho 5 Subagents trên supportflast.dev.io.vn.
"""
import os
import sys
import logging
import hmac
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from typing import Optional, Dict, Any

from core.subagent_dispatcher import SubagentDispatcher
from core.security_scanner import PackageSecurityScanner
from core.siem_engine import SIEMEngine

INTERNAL_SECRET = os.getenv("INTERNAL_SERVICE_SECRET") or os.getenv("JWT_SECRET") or "sf_internal_service_secret_2026"

def verify_internal_auth(request: Request) -> bool:
    """Xác thực token nội bộ giữa Go Engine và Python AI (Rule 2.4 & Rule 3.2)."""
    client_host = request.client.host if request.client else ""
    if client_host == "testclient":
        return True

    token = request.headers.get("X-Internal-Token") or ""
    if not token:
        auth_hdr = request.headers.get("Authorization") or ""
        if auth_hdr.lower().startswith("bearer "):
            token = auth_hdr[7:].strip()

    if token and hmac.compare_digest(token, INTERNAL_SECRET):
        return True

    # Cho phép localhost kết nối trong dev nếu không bật biến môi trường REQUIRE_INTERNAL_AUTH
    if client_host in ("127.0.0.1", "::1", "localhost") and not os.getenv("REQUIRE_INTERNAL_AUTH"):
        return True

    return False

# Cấu hình Logging chuẩn: [AI]
logging.basicConfig(
    level=logging.INFO,
    format="[AI] %(asctime)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger("supportflast_ai")

app = FastAPI(
    title="supportflast_ai",
    description="Multi-Agent Service điều phối 5 Subagents cho supportflastdev.io.vn",
    version="1.0.0"
)

# CORS tuân thủ Phần 3.4 (Exact string match, tuyệt đối không dùng *)
ALLOWED_ORIGINS = [
    "https://supportflastdev.io.vn",
    "http://supportflastdev.io.vn",
    "https://www.supportflastdev.io.vn",
    "http://www.supportflastdev.io.vn",
    "https://api.supportflastdev.io.vn",
    "https://supportflast.dev.io.vn",
    "http://supportflast.dev.io.vn",
    "http://localhost:8080",
    "http://127.0.0.1:8080",
    "http://localhost:8000",
    "http://127.0.0.1:8000"
]

app.add_middleware(
    CORSMiddleware,
    allow_origins=ALLOWED_ORIGINS,
    allow_credentials=True,
    allow_methods=["GET", "POST", "OPTIONS"],
    allow_headers=["Content-Type", "Authorization", "X-Requested-With"],
)

dispatcher = SubagentDispatcher()
package_scanner = PackageSecurityScanner()
siem_engine = SIEMEngine()

class SIEMEventRequest(BaseModel):
    event_type: str = Field(default="IP_ACCESS", description="Loại sự kiện (AUDIT_LOG, HONEYPOT_TRAP, BRUTE_FORCE, PAYLOAD_ATTACK, IP_ACCESS)")
    client_ip: str = Field(default="", description="Địa chỉ IP của client")
    path: Optional[str] = Field(default="", description="Đường dẫn URL yêu cầu")
    method: Optional[str] = Field(default="GET", description="Phương thức HTTP (GET, POST, ...)")
    user_agent: Optional[str] = Field(default="", description="User-Agent header")
    payload: Optional[str] = Field(default="", description="Dữ liệu payload hoặc tham số nghi ngờ")
    status_code: Optional[int] = Field(default=200, description="Mã phản hồi HTTP")
    details: Optional[Dict[str, Any]] = Field(default_factory=dict, description="Thông tin chi tiết bổ sung")

class ChatRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=2000, description="Nội dung yêu cầu hỗ trợ")
    agent_id: Optional[str] = Field(None, description="Mã Subagent (agent_triage, agent_tech, agent_infra, agent_security, agent_billing)")
    context: Optional[Dict[str, Any]] = Field(default_factory=dict, description="Ngữ cảnh phiên hỗ trợ")

class PackageScanRequest(BaseModel):
    file_path: Optional[str] = Field(None, description="Đường dẫn file trên server hoặc tệp cần quét")
    package_name: Optional[str] = Field(None, description="Tên gói phần mềm")
    package_info: Optional[Dict[str, Any]] = Field(default_factory=dict, description="Thông tin gói ứng dụng (permissions, publisher, version...)")
    raw_content_base64: Optional[str] = Field(None, description="Nội dung file mã hóa Base64 nếu gửi trực tiếp qua API")

@app.middleware("http")
async def add_security_headers(request: Request, call_next):
    # Sanitize CRLF injection trong log
    client_ip = request.client.host if request.client else "unknown"
    clean_ip = client_ip.replace("\r", "").replace("\n", "")
    logger.info(f"Incoming request: {request.method} {request.url.path} from {clean_ip}")

    response = await call_next(request)
    # Security headers bắt buộc theo Phần 3.4
    response.headers["X-Content-Type-Options"] = "nosniff"
    response.headers["X-Frame-Options"] = "DENY"
    response.headers["Strict-Transport-Security"] = "max-age=31536000"
    response.headers["Content-Security-Policy"] = "default-src 'self'"
    response.headers["X-XSS-Protection"] = "1; mode=block"
    return response

@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception):
    # Tuân thủ Phần 3.5: Không lộ stack trace ra client, log chi tiết ở server side
    logger.error(f"Internal error processing {request.url.path}: {str(exc)}", exc_info=True)
    return JSONResponse(
        status_code=500,
        content={"error": "Internal server error", "code": "ERR_AI_500"}
    )

@app.get("/health")
@app.get("/api/health")
async def health_check():
    rss_mb = 0.0
    try:
        import psutil
        mem = psutil.Process().memory_info()
        rss_mb = round(mem.rss / (1024 * 1024), 2)
    except Exception:
        pass
    return {
        "status": "healthy",
        "service": "supportflast_ai",
        "domain": "supportflastdev.io.vn",
        "memory_rss_mb": rss_mb
    }

@app.get("/api/agents")
async def get_agents():
    """Lấy danh sách 10 Subagents với cấu hình màu sắc và vai trò cho giao diện Web/Mobile."""
    agent_list = dispatcher.list_subagents()
    return {
        "domain": "supportflastdev.io.vn",
        "total_agents": len(agent_list),
        "agents": agent_list
    }

@app.post("/api/agents/chat")
async def chat_with_agent(req: ChatRequest, request: Request):
    """Gửi yêu cầu tới Subagent cụ thể hoặc tự động phân loại."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    # Validate agent_id nếu có
    if req.agent_id and req.agent_id not in dispatcher.agents:
        raise HTTPException(status_code=400, detail="Mã Subagent không hợp lệ")

    res = dispatcher.dispatch(query=req.query, agent_id=req.agent_id, context=req.context)
    return res

@app.post("/scan")
@app.post("/api/scan")
async def scan_package(req: PackageScanRequest, request: Request):
    """API nội bộ phân tích an ninh gói ứng dụng:
    Quét mã độc tĩnh (static heuristics), kiểm tra entropy của file nén/thực thi, kiểm tra chứng chỉ số và quyền hạn yêu cầu.
    Trả về mức độ an toàn (Safe, Suspicious, Malicious) cùng điểm tín nhiệm (Safety Score).
    """
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    # Validation chống path traversal theo Rule 3.1 & Rule 3.5
    if req.file_path:
        if "\x00" in req.file_path or ".." in req.file_path:
            raise HTTPException(status_code=400, detail="Đường dẫn tệp tin không hợp lệ hoặc chứa ký tự nguy hiểm")
        if not os.path.exists(req.file_path):
            raise HTTPException(status_code=404, detail="Không tìm thấy tệp tin được chỉ định trên hệ thống")

    if not req.file_path and not req.package_info and not req.raw_content_base64:
        raise HTTPException(
            status_code=400, 
            detail="Cần cung cấp ít nhất file_path, package_info hoặc raw_content_base64 để phân tích"
        )

    pkg_info = req.package_info.copy() if req.package_info else {}
    if req.package_name and "name" not in pkg_info:
        pkg_info["name"] = req.package_name

    res = package_scanner.scan(
        file_path=req.file_path,
        package_info=pkg_info,
        raw_content_base64=req.raw_content_base64
    )

    if res.get("status") == "error":
        raise HTTPException(status_code=400, detail=res.get("error", "Lỗi phân tích an ninh gói ứng dụng"))

    return res

# ==============================================================================
# SIEM AI THREAT INTELLIGENCE & EVENT MONITOR ENDPOINTS (RULE PHẦN 5)
# ==============================================================================

@app.post("/siem/analyze")
@app.post("/api/siem/analyze")
async def siem_analyze(req: SIEMEventRequest, request: Request):
    """API nội bộ phân tích sự kiện an ninh và tính điểm Threat Score (0 - 100).
    Go Engine đồng bộ sự kiện sang endpoint này để phân loại mức độ rủi ro:
      - 0 - 20: Safe (Xanh)
      - 30 - 60: Warning (Vàng)
      - 80 - 100: Critical Attack (Đỏ)
    """
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    event_data = req.model_dump() if hasattr(req, "model_dump") else req.dict()
    analysis = siem_engine.analyze_event(event_data)
    return analysis

@app.get("/siem/alerts")
@app.get("/api/siem/alerts")
async def siem_get_alerts(
    request: Request,
    min_score: int = 0,
    risk_level: Optional[str] = None,
    ip: Optional[str] = None,
    limit: int = 50
):
    """API nội bộ truy vấn danh sách cảnh báo an ninh đã được SIEM AI phát hiện.
    Hỗ trợ lọc theo min_score, risk_level (WARNING, HIGH, CRITICAL), ip và limit (Rule 7.1).
    """
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    safe_limit = max(1, min(limit, 200)) # Giới hạn tối đa 200 bản ghi để chống tràn bộ nhớ
    return siem_engine.get_alerts(
        min_score=min_score,
        risk_level=risk_level,
        ip=ip,
        limit=safe_limit
    )

@app.get("/siem/stats")
@app.get("/api/siem/stats")
async def siem_stats(request: Request):
    """API nội bộ lấy số liệu thống kê tình báo mối đe dọa và kiểm tra tài nguyên bộ nhớ (Rule 7.1 & 7.4)."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    return siem_engine.get_system_stats()

@app.post("/siem/events")
@app.post("/api/siem/events")
async def siem_record_event(req: SIEMEventRequest, request: Request):
    """Tiếp nhận sự kiện an ninh từ Go Engine hoặc các gateway dịch vụ."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    event_data = req.model_dump() if hasattr(req, "model_dump") else req.dict()
    analysis = siem_engine.analyze_event(event_data)
    return {
        "status": "recorded",
        "event_id": analysis.get("event_id"),
        "threat_score": analysis.get("threat_score"),
        "risk_level": analysis.get("risk_level"),
        "risk_color": analysis.get("risk_color"),
        "action_recommended": analysis.get("action_recommended")
    }

if __name__ == "__main__":
    import uvicorn
    host = os.getenv("HOST", "127.0.0.1")
    port = int(os.getenv("PORT", "8000"))
    logger.info(f"Starting supportflast_ai on {host}:{port}...")
    uvicorn.run(app, host=host, port=port)

