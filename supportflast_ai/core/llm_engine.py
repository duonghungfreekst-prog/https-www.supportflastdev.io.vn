import os
import json
import logging
import asyncio
import threading
import time
from typing import Dict, Any, Optional

logger = logging.getLogger("supportflast_ai")

# Quản lý giới hạn tải Concurrency toàn cục cho các lời gọi LLM (Nút thắt 2 & 3 - Audit P1.2)
_llm_semaphore: Optional[asyncio.Semaphore] = None
_sync_llm_semaphore: Optional[threading.Semaphore] = None

def get_llm_semaphore() -> asyncio.Semaphore:
    """Trả về asyncio.Semaphore cấp tiến trình để kiểm soát số lượng yêu cầu gọi LLM bất đồng bộ (MAX_INFLIGHT_LLM)."""
    global _llm_semaphore
    if _llm_semaphore is None:
        max_inflight = int(os.getenv("MAX_INFLIGHT_LLM", "8"))
        _llm_semaphore = asyncio.Semaphore(max_inflight)
    return _llm_semaphore

def get_sync_llm_semaphore() -> threading.Semaphore:
    """Trả về threading.Semaphore để kiểm soát số lượng yêu cầu gọi LLM đồng bộ (MAX_INFLIGHT_LLM)."""
    global _sync_llm_semaphore
    if _sync_llm_semaphore is None:
        max_inflight = int(os.getenv("MAX_INFLIGHT_LLM", "8"))
        _sync_llm_semaphore = threading.Semaphore(max_inflight)
    return _sync_llm_semaphore

class LLMEngine:
    def __init__(self):
        self.api_key = os.getenv("GEMINI_API_KEY", "").strip()
        self.is_enabled = bool(self.api_key)
        self.client = None
        self.legacy_model = None
        # Model ID chuẩn chính thức từ Google (mặc định gemini-3.8-flash, tuyệt đối không gắn đuôi -high vào model ID)
        self.model_name = os.getenv("GEMINI_MODEL", "gemini-3.8-flash").strip()
        self.last_error: Optional[str] = None
        self.last_error_code: Optional[str] = None
        self.is_healthy: bool = False
        # Cache kết quả kiểm tra sức khỏe để chống cạn kiệt Quota do probe liên tục (Audit P1.5)
        self._last_health_cache: Optional[Dict[str, Any]] = None
        self._last_health_check_time: float = 0.0
        self._health_cache_ttl: float = 60.0

        if self.is_enabled:
            # Ưu tiên SDK mới nhất: google.genai (Audit P1.4)
            try:
                from google import genai
                self.client = genai.Client(api_key=self.api_key)
                logger.info(f"[AI] LLM Engine initialized successfully with google.genai SDK (model={self.model_name}).")
            except ImportError:
                try:
                    import google.generativeai as legacy_genai
                    legacy_genai.configure(api_key=self.api_key)
                    self.legacy_model = legacy_genai.GenerativeModel(self.model_name)
                    logger.info(f"[AI] LLM Engine initialized with legacy google.generativeai SDK (model={self.model_name}).")
                except Exception as ex:
                    logger.error(f"[AI] Failed to initialize legacy LLM SDK: {ex}")
                    self.is_enabled = False
                    self.last_error = str(ex)
                    self.last_error_code = "SDK_INIT_ERROR"
            except Exception as e:
                logger.error(f"[AI] Failed to initialize google.genai client: {e}")
                self.is_enabled = False
                self.last_error = str(e)
                self.last_error_code = "CLIENT_INIT_ERROR"
        else:
            logger.info("[AI] GEMINI_API_KEY is not configured. Running in high-speed On-Device Rule-Based mode.")

    def _classify_error(self, err_str: str) -> str:
        """Phân loại lỗi chi tiết (API_KEY_INVALID, MODEL_NOT_FOUND, QUOTA_EXCEEDED, RATE_LIMITED, TIMEOUT, NETWORK_ERROR)."""
        s = err_str.lower()
        if "api_key_invalid" in s or "api key not valid" in s or "invalid api key" in s or "unauthenticated" in s:
            return "API_KEY_INVALID"
        elif "not_found" in s or "model not found" in s or ("models/" in s and "404" in s):
            return "MODEL_NOT_FOUND"
        elif "quota" in s or "resource_exhausted" in s or "429" in s:
            return "QUOTA_EXCEEDED"
        elif "rate limit" in s or "too many requests" in s:
            return "RATE_LIMITED"
        elif "timeout" in s or "deadline" in s:
            return "TIMEOUT"
        return "NETWORK_ERROR"

    async def check_health(self, force_refresh: bool = False) -> Dict[str, Any]:
        """Kiểm tra kết nối thực tế tới Gemini Model có Cache TTL 60s để chống cạn kiệt Quota (Audit P1.5)."""
        if not self.is_enabled:
            return {
                "status": "offline",
                "code": "API_KEY_MISSING",
                "message": "Chưa cấu hình GEMINI_API_KEY. Hệ thống đang chạy ở chế độ On-Device Rule-Based an toàn.",
                "model": self.model_name,
                "fallback_active": True,
                "sdk": "google-genai" if self.client else ("legacy-genai" if self.legacy_model else "none")
            }

        now = time.time()
        if not force_refresh and self._last_health_cache and (now - self._last_health_check_time < self._health_cache_ttl):
            res = dict(self._last_health_cache)
            res["cached"] = True
            res["cache_age_sec"] = round(now - self._last_health_check_time, 1)
            return res

        t0 = time.time()
        try:
            # Gửi 1 prompt ping siêu nhẹ với timeout 4s để kiểm tra tính sẵn sàng thực tế của model
            test_res = await asyncio.wait_for(
                self.agenerate_text("ping", system_instruction="Trả lời đúng một từ: PONG"),
                timeout=4.0
            )
            latency_ms = round((time.time() - t0) * 1000, 2)
            if test_res:
                self.is_healthy = True
                self.last_error = None
                self.last_error_code = None
                res = {
                    "status": "online",
                    "code": "OK",
                    "model": self.model_name,
                    "sdk": "google-genai" if self.client else "legacy-genai",
                    "latency_ms": latency_ms,
                    "fallback_active": False,
                    "cached": False
                }
            else:
                self.is_healthy = False
                err_code = self.last_error_code or "MODEL_EMPTY_RESPONSE"
                res = {
                    "status": "degraded",
                    "code": err_code,
                    "error": self.last_error or "Không nhận được phản hồi từ model",
                    "model": self.model_name,
                    "fallback_active": True,
                    "cached": False
                }
        except asyncio.TimeoutError:
            self.is_healthy = False
            self.last_error_code = "TIMEOUT"
            self.last_error = "Quá thời gian chờ khi kết nối Gemini API (Timeout 4s)"
            res = {
                "status": "degraded",
                "code": "TIMEOUT",
                "error": self.last_error,
                "model": self.model_name,
                "fallback_active": True,
                "cached": False
            }
        except Exception as ex:
            self.is_healthy = False
            err_str = str(ex)
            err_code = self._classify_error(err_str)
            self.last_error_code = err_code
            self.last_error = err_str
            res = {
                "status": "degraded",
                "code": err_code,
                "error": err_str,
                "model": self.model_name,
                "fallback_active": True,
                "cached": False
            }

        self._last_health_cache = res
        self._last_health_check_time = time.time()
        return res

    def generate_json(self, prompt: str, system_instruction: str = None) -> Optional[Dict[str, Any]]:
        """Gọi LLM đồng bộ trả về kết quả cấu trúc JSON có kiểm soát Semaphore đồng bộ (Audit P1.2)."""
        if not self.is_enabled:
            return None

        sem = get_sync_llm_semaphore()
        acquired = sem.acquire(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Sync LLM Semaphore timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            # 1. Dùng google.genai client hiện đại
            if self.client:
                try:
                    from google.genai import types
                    config = types.GenerateContentConfig(
                        response_mime_type="application/json",
                        system_instruction=system_instruction
                    )
                    response = self.client.models.generate_content(
                        model=self.model_name,
                        contents=prompt,
                        config=config
                    )
                    raw_text = response.text.strip()
                    if raw_text.startswith("```json"):
                        raw_text = raw_text[7:]
                    if raw_text.startswith("```"):
                        raw_text = raw_text[3:]
                    if raw_text.endswith("```"):
                        raw_text = raw_text[:-3]
                    self.is_healthy = True
                    return json.loads(raw_text.strip())
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] google.genai generate_json error: {e}")
                    return None

            # 2. Dùng legacy client nếu có
            if self.legacy_model:
                try:
                    import google.generativeai as legacy_genai
                    model = self.legacy_model
                    if system_instruction:
                        model = legacy_genai.GenerativeModel(
                            self.model_name,
                            system_instruction=system_instruction
                        )
                    response = model.generate_content(
                        prompt,
                        generation_config=legacy_genai.GenerationConfig(
                            response_mime_type="application/json"
                        )
                    )
                    self.is_healthy = True
                    return json.loads(response.text)
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] Legacy LLM Error: {e}")
                    return None

            return None
        finally:
            sem.release()

    async def agenerate_json(self, prompt: str, system_instruction: str = None) -> Optional[Dict[str, Any]]:
        """Gọi LLM bất đồng bộ thật sự (Async LLM) có kiểm soát Concurrency Semaphore (Nút thắt 2 & Audit P1.2)."""
        if not self.is_enabled:
            return None

        sem = get_llm_semaphore()
        async with sem:
            # 1. Dùng google.genai client hiện đại qua client.aio
            if self.client and hasattr(self.client, "aio") and hasattr(self.client.aio, "models"):
                try:
                    from google.genai import types
                    config = types.GenerateContentConfig(
                        response_mime_type="application/json",
                        system_instruction=system_instruction
                    )
                    response = await self.client.aio.models.generate_content(
                        model=self.model_name,
                        contents=prompt,
                        config=config
                    )
                    raw_text = response.text.strip()
                    if raw_text.startswith("```json"):
                        raw_text = raw_text[7:]
                    if raw_text.startswith("```"):
                        raw_text = raw_text[3:]
                    if raw_text.endswith("```"):
                        raw_text = raw_text[:-3]
                    self.is_healthy = True
                    return json.loads(raw_text.strip())
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] google.genai aio generate_json error: {e}")
                    return None

            # 2. Fallback an toàn qua threadpool nếu SDK chạy đồng bộ
            return await asyncio.to_thread(self.generate_json, prompt, system_instruction)

    def generate_text(self, prompt: str, system_instruction: str = None) -> Optional[str]:
        """Gọi LLM đồng bộ trả về kết quả dạng Text có kiểm soát Semaphore đồng bộ (Audit P1.2)."""
        if not self.is_enabled:
            return None

        sem = get_sync_llm_semaphore()
        acquired = sem.acquire(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Sync LLM Semaphore timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            # 1. Dùng google.genai client hiện đại
            if self.client:
                try:
                    from google.genai import types
                    config = types.GenerateContentConfig(
                        system_instruction=system_instruction
                    ) if system_instruction else None
                    response = self.client.models.generate_content(
                        model=self.model_name,
                        contents=prompt,
                        config=config
                    )
                    self.is_healthy = True
                    return response.text
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] google.genai generate_text error: {e}")
                    return None

            # 2. Dùng legacy client nếu có
            if self.legacy_model:
                try:
                    import google.generativeai as legacy_genai
                    model = self.legacy_model
                    if system_instruction:
                        model = legacy_genai.GenerativeModel(
                            self.model_name,
                            system_instruction=system_instruction
                        )
                    response = model.generate_content(prompt)
                    self.is_healthy = True
                    return response.text
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] Legacy LLM Error: {e}")
                    return None

            return None
        finally:
            sem.release()

    async def agenerate_text(self, prompt: str, system_instruction: str = None) -> Optional[str]:
        """Gọi LLM bất đồng bộ dạng Text có kiểm soát Concurrency Semaphore (Nút thắt 2 & Audit P1.2)."""
        if not self.is_enabled:
            return None

        sem = get_llm_semaphore()
        async with sem:
            if self.client and hasattr(self.client, "aio") and hasattr(self.client.aio, "models"):
                try:
                    from google.genai import types
                    config = types.GenerateContentConfig(
                        system_instruction=system_instruction
                    ) if system_instruction else None
                    response = await self.client.aio.models.generate_content(
                        model=self.model_name,
                        contents=prompt,
                        config=config
                    )
                    self.is_healthy = True
                    return response.text
                except Exception as e:
                    self.last_error = str(e)
                    self.last_error_code = self._classify_error(str(e))
                    logger.error(f"[AI] [{self.last_error_code}] google.genai aio generate_text error: {e}")
                    return None

            return await asyncio.to_thread(self.generate_text, prompt, system_instruction)

llm_engine = LLMEngine()
