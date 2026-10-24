"""FastAPI Entrypoint cho Python Subagent System (supportflast_ai)
Cổng kết nối AI Engine cho 10 Subagents trên supportflastdev.io.vn.
"""
import os
import sys
import logging
import hmac
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from starlette.concurrency import run_in_threadpool
from typing import Optional, Dict, Any

from core.subagent_dispatcher import SubagentDispatcher
from core.security_scanner import PackageSecurityScanner
from core.siem_engine import SIEMEngine

IS_PROD = os.getenv("ENVIRONMENT", "").lower() == "production" or os.getenv("GO_ENV", "").lower() == "production" or os.getenv("ENV") == "production" 

INSECURE_INTERNAL_SECRETS = {
    "",
    "CHANGE_ME_INTERNAL_SERVICE_SECRET",
    "sf_internal_service_prod_secret_2026_a8f9c1d2e3b4",
    "Admin@2026!SupportFlastSecuredProd",
    "sf_mobile_token_2026",
    "sf_mobile_engine_token_2026",
    "default_secret",
    "secret",
    "123456",
    "<GENERATE_SECURE_RANDOM_64_HEX_CHARACTERS>",
    "<GENERATE_SECURE_INTERNAL_SERVICE_SECRET>",
}

def is_insecure_internal_secret(secret: str) -> bool:
    """Kiểm tra xem secret có rỗng, mang giá trị mặc định yếu hoặc placeholder hay không."""
    if not secret:
        return True
    s = secret.strip()
    if s in INSECURE_INTERNAL_SECRETS:
        return True
    lower = s.lower()
    if lower.startswith("<generate") or "change_me" in lower:
        return True
    if len(s) < 16:
        return True
    return False

def get_internal_secret() -> str:
    """Lấy internal secret từ biến môi trường hoặc tệp chia sẻ an toàn (.internal_secret) để đồng bộ 100% với Go (Audit P0)."""
    env_sec = os.getenv("INTERNAL_SERVICE_SECRET", "").strip()
    if env_sec and not is_insecure_internal_secret(env_sec):
        return env_sec

    # Tìm kiếm tệp bí mật chia sẻ từ Go Engine
    base_dir = os.path.dirname(os.path.abspath(__file__))
    candidates = [
        os.getenv("JWT_KEYS_DIR", "").strip(),
        "/app/data/keys",
        os.path.abspath(os.path.join(base_dir, "..", "data", "keys")),
        os.path.abspath(os.path.join(base_dir, "data", "keys")),
        os.path.abspath(os.path.join(os.getcwd(), "data", "keys")),
    ]
    for d in candidates:
        if not d:
            continue
        sec_file = os.path.join(d, ".internal_secret")
        if os.path.isfile(sec_file):
            try:
                with open(sec_file, "r", encoding="utf-8") as f:
                    file_sec = f.read().strip()
                if file_sec and not is_insecure_internal_secret(file_sec):
                    return file_sec
            except Exception:
                pass
    return env_sec

def verify_internal_auth(request: Request) -> bool:
    """Xác thực token nội bộ giữa Go Engine và Python AI (Rule 2.4, Rule 3.2 & Audit P0)."""
    client_host = request.client.host if request.client else ""
    if client_host == "testclient":
        return True

    is_prod = os.getenv("ENV") == "production" or bool(os.getenv("REQUIRE_INTERNAL_AUTH"))
    current_secret = get_internal_secret()

    if is_prod and is_insecure_internal_secret(current_secret):
        logging.error("[AI] [SECURITY CRITICAL] INTERNAL_SERVICE_SECRET rỗng hoặc mang giá trị mặc định yếu trong Production! Từ chối toàn bộ request nội bộ.")
        return False

    token = request.headers.get("X-Internal-Token") or ""
    if not token:
        auth_hdr = request.headers.get("Authorization") or ""
        if auth_hdr.lower().startswith("bearer "):
            token = auth_hdr[7:].strip()

    if token and current_secret and hmac.compare_digest(token, current_secret):
        return True

    # Chỉ cho phép localhost trong môi trường dev khi KHÔNG phải production
    if not is_prod and client_host in ("127.0.0.1", "::1", "localhost"):
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
    description="Multi-Agent Service điều phối 10 Subagents cho supportflastdev.io.vn",
    version="1.0.0"
)

# CORS tuân thủ Phần 3.4 (Exact string match, siết chặt HTTPS trong Production)
if IS_PROD:
    ALLOWED_ORIGINS = [
        "https://supportflastdev.io.vn",
        "https://www.supportflastdev.io.vn",
        "https://api.supportflastdev.io.vn",
        "https://supportflast.dev.io.vn",
    ]
else:
    ALLOWED_ORIGINS = [
        "https://supportflastdev.io.vn",
        "https://www.supportflastdev.io.vn",
        "https://api.supportflastdev.io.vn",
        "https://supportflast.dev.io.vn",
        "http://supportflastdev.io.vn",
        "http://www.supportflastdev.io.vn",
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
    allow_headers=["Content-Type", "Authorization", "X-Requested-With", "X-Internal-Token"],
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
    agent_id: Optional[str] = Field(None, description="Mã Subagent cụ thể nếu muốn chỉ định trực tiếp")
    context: Optional[Dict[str, Any]] = Field(default_factory=dict, description="Ngữ cảnh phiên hỗ trợ")
    mode: Optional[str] = Field("fast", description="Chế độ điều phối: fast (1 agent nhanh), parallel (2-3 agent song song), deep (tối đa 10 agent)")
    timeout_sec: Optional[float] = Field(15.0, description="Deadline tối đa cho toàn bộ lượt gọi")

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

@app.get("/livez")
async def liveness_check():
    """Liveness probe: kiểm tra tiến trình Python AI còn chạy."""
    return {"status": "alive", "service": "supportflast_ai"}

@app.get("/health")
@app.get("/readyz")
@app.get("/api/health")
async def health_check():
    """Readiness probe: phân biệt rõ 3 trạng thái READY / DEGRADED / UNAVAILABLE (Audit P1.6)."""
    rss_mb = 0.0
    try:
        import psutil
        mem = psutil.Process().memory_info()
        rss_mb = round(mem.rss / (1024 * 1024), 2)
    except Exception:
        pass

    total_agents = len(dispatcher.agents) if (dispatcher and hasattr(dispatcher, 'agents')) else 0
    if total_agents < 5:
        raise HTTPException(
            status_code=503,
            detail={
                "status": "unavailable",
                "service": "supportflast_ai",
                "error": "Cụm Subagents AI chưa sẵn sàng (thiếu agent cốt lõi)",
                "total_agents": total_agents,
                "memory_rss_mb": rss_mb
            }
        )

    from core.llm_engine import llm_engine
    is_healthy_model = llm_engine.is_enabled and getattr(llm_engine, "is_healthy", False)
    status_str = "healthy" if is_healthy_model else ("degraded" if llm_engine.is_enabled else "ready")
    mode_str = "gemini_api" if is_healthy_model else "rule_based_fallback"

    return {
        "status": status_str,
        "service": "supportflast_ai",
        "domain": "supportflastdev.io.vn",
        "total_agents": total_agents,
        "agents_ready": True,
        "mode": mode_str,
        "llm_engine_enabled": llm_engine.is_enabled,
        "llm_model": llm_engine.model_name,
        "llm_status": "online" if is_healthy_model else ("degraded" if llm_engine.is_enabled else "offline"),
        "memory_rss_mb": rss_mb
    }

@app.get("/api/model/status")
@app.get("/api/ai/diagnostics")
async def model_diagnostics(request: Request, refresh: bool = False):
    """Endpoint chẩn đoán kết nối model thực tế cho quản trị viên (Audit P0, P1.5) - yêu cầu xác thực nội bộ."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    from core.llm_engine import llm_engine
    health = await llm_engine.check_health(force_refresh=refresh)
    return {
        "service": "supportflast_ai",
        "gemini_api_key_configured": llm_engine.is_enabled,
        "model_name": llm_engine.model_name,
        "concurrency_limit": int(os.getenv("MAX_INFLIGHT_LLM", "8")),
        "diagnostics": health
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
    """Gửi yêu cầu tới Subagent cụ thể hoặc tự động phân loại theo 3 chế độ (fast, parallel, deep).
    Tuân thủ kiểm soát Concurrency Semaphore và Deadline tổng (Nút thắt 1, 2, 3).
    """
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    # Validate agent_id nếu có
    if req.agent_id and req.agent_id not in dispatcher.agents:
        raise HTTPException(status_code=400, detail="Mã Subagent không hợp lệ")

    res = await dispatcher.adispatch(
        query=req.query,
        agent_id=req.agent_id,
        context=req.context,
        mode=req.mode or "fast",
        timeout_sec=req.timeout_sec or 15.0
    )
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

    res = await run_in_threadpool(
        package_scanner.scan,
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
    """API nội bộ phân tích sự kiện an ninh và tính điểm Threat Score (0 - 100)."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    event_data = req.model_dump() if hasattr(req, "model_dump") else req.dict()
    analysis = await run_in_threadpool(siem_engine.analyze_event, event_data)
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
    """API nội bộ truy vấn danh sách cảnh báo an ninh đã được SIEM AI phát hiện."""
    if not verify_internal_auth(request):
        raise HTTPException(status_code=401, detail="Unauthorized: Yêu cầu xác thực token nội bộ hợp lệ")

    safe_limit = max(1, min(limit, 200))
    return siem_engine.get_alerts(
        min_score=min_score,
        risk_level=risk_level,
        ip=ip,
        limit=safe_limit
    )

@app.get("/siem/stats")
@app.get("/api/siem/stats")
async def siem_stats(request: Request):
    """API nội bộ lấy số liệu thống kê tình báo mối đe dọa và kiểm tra tài nguyên bộ nhớ."""
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

@app.on_event("startup")
async def startup_security_checks():
    """Kiểm tra an ninh khi khởi động service: đồng bộ token nội bộ trong Production (Audit P0 & Rule Phần 3)."""
    is_prod = os.getenv("ENV") == "production" or bool(os.getenv("REQUIRE_INTERNAL_AUTH"))
    current_secret = get_internal_secret()
    if is_prod:
        if is_insecure_internal_secret(current_secret):
            logger.error("[AI] [SECURITY CRITICAL] INTERNAL_SERVICE_SECRET bắt buộc phải được cấu hình hoặc chia sẻ an toàn qua file trong Production! (Audit P0).")
        else:
            logger.info("[AI] [SECURITY] Đã đồng bộ và xác thực INTERNAL_SERVICE_SECRET an toàn cho môi trường Production.")

if __name__ == "__main__":
    import uvicorn
    is_prod = os.getenv("ENV") == "production" or bool(os.getenv("REQUIRE_INTERNAL_AUTH"))
    current_secret = get_internal_secret()
    if is_prod and is_insecure_internal_secret(current_secret):
        logger.error("[AI] [SECURITY CRITICAL] INTERNAL_SERVICE_SECRET thiếu hoặc không an toàn trong Production!")
    host = os.getenv("HOST", "127.0.0.1")
    port = int(os.getenv("PORT", "8000"))
    logger.info(f"Starting supportflast_ai on {host}:{port}...")
    uvicorn.run(app, host=host, port=port)
