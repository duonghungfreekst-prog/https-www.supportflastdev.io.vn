import os
import json
import logging
import asyncio
import threading
import time
from typing import Dict, Any, Optional, List, Tuple
import httpx

# Tự động nạp file .env từ thư mục gốc hoặc thư mục hiện tại (Audit Environment)
try:
    from dotenv import load_dotenv
    load_dotenv()
    _root_env = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".env"))
    if os.path.isfile(_root_env):
        load_dotenv(_root_env)
    _app_env = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".env"))
    if os.path.isfile(_app_env):
        load_dotenv(_app_env)
except ImportError:
    pass

logger = logging.getLogger("supportflast_ai")

def _parse_max_inflight() -> int:
    """Xác thực và chuyển đổi MAX_INFLIGHT_LLM sang số nguyên dương hợp lệ (> 0).
    Nếu <= 0 hoặc sai định dạng, tự động fallback an toàn về 8 để chống nghẽn hoặc sập limiter (Audit P1).
    """
    raw = os.getenv("MAX_INFLIGHT_LLM", "8").strip()
    try:
        val = int(raw)
        if val > 0:
            return val
        logger.warning(f"[AI] MAX_INFLIGHT_LLM={val} không hợp lệ (phải > 0). Tự động fallback về 8.")
        return 8
    except (ValueError, TypeError):
        logger.warning(f"[AI] MAX_INFLIGHT_LLM='{raw}' không phải số nguyên. Tự động fallback về 8.")
        return 8


class UnifiedConcurrencyLimiter:
    """Bộ kiểm soát Concurrency duy nhất trên toàn bộ tiến trình.
    Đảm bảo TẤT CẢ các đường gọi LLM (cả bất đồng bộ async lẫn đồng bộ sync của các Subagent)
    đều chia sẻ CHUNG một BoundedSemaphore duy nhất, tuân thủ nghiêm ngặt MAX_INFLIGHT_LLM (Audit Concurrency).
    """
    def __init__(self, max_inflight: Optional[int] = None):
        self.max_inflight = max_inflight or _parse_max_inflight()
        self._thread_sem = threading.BoundedSemaphore(self.max_inflight)
        self._lock = threading.Lock()
        self._active_count = 0
        self._peak_count = 0

    def acquire_sync(self, timeout: float = 10.0) -> bool:
        acquired = self._thread_sem.acquire(timeout=timeout)
        if acquired:
            with self._lock:
                self._active_count += 1
                if self._active_count > self._peak_count:
                    self._peak_count = self._active_count
        return acquired

    def release_sync(self):
        with self._lock:
            self._active_count = max(0, self._active_count - 1)
        try:
            self._thread_sem.release()
        except ValueError:
            pass

    async def acquire_async(self, timeout: float = 10.0) -> bool:
        return await asyncio.to_thread(self.acquire_sync, timeout)

    def release_async(self):
        self.release_sync()

    @property
    def active_inflight(self) -> int:
        with self._lock:
            return self._active_count

    @property
    def peak_inflight(self) -> int:
        with self._lock:
            return self._peak_count

_unified_limiter: Optional[UnifiedConcurrencyLimiter] = None

def get_unified_limiter() -> UnifiedConcurrencyLimiter:
    global _unified_limiter
    if _unified_limiter is None:
        _unified_limiter = UnifiedConcurrencyLimiter()
    return _unified_limiter

def get_llm_semaphore():
    """Hàm tương thích ngược trả về semaphore bất đồng bộ."""
    return get_unified_limiter()

def get_sync_llm_semaphore():
    """Hàm tương thích ngược trả về semaphore đồng bộ."""
    return get_unified_limiter()


class OpenAICompatibleProvider:
    """Adapter dùng chung cho các nhà cung cấp mô hình tương thích OpenAI (OpenRouter, Groq, Mistral, Cloudflare, OmniRouter)."""

    def __init__(
        self,
        name: str,
        base_url: str,
        api_key: str,
        model: str,
        extra_headers: Optional[Dict[str, str]] = None,
        timeout_sec: float = 30.0,
        retry_max: int = 2
    ):
        self.name = name
        self.base_url = base_url.rstrip("/")
        if not self.base_url.endswith("/chat/completions"):
            self.chat_endpoint = f"{self.base_url}/chat/completions"
        else:
            self.chat_endpoint = self.base_url
        self.api_key = api_key.strip()
        self.model = model.strip()
        self.extra_headers = extra_headers or {}
        self.timeout_sec = timeout_sec
        self.retry_max = retry_max

        self.is_enabled = bool(self.api_key)
        self.is_healthy: bool = False
        self.last_error: Optional[str] = None
        self.last_error_code: Optional[str] = None
        self.cooldown_until: float = 0.0
        self.failure_count: int = 0

    def is_available(self) -> bool:
        """Kiểm tra xem provider có được kích hoạt và không đang trong thời gian cooldown không."""
        if not self.is_enabled:
            return False
        if time.time() < self.cooldown_until:
            return False
        return True

    def mark_cooldown(self, seconds: float = 60.0, reason: str = ""):
        """Đưa provider vào cooldown khi gặp lỗi 429 hoặc quá tải."""
        self.cooldown_until = time.time() + seconds
        self.failure_count += 1
        logger.warning(f"[AI] [{self.name.upper()}] Tạm thời đưa vào cooldown {seconds:.0f}s: {reason}")

    def _build_headers(self) -> Dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}"
        }
        headers.update(self.extra_headers)
        return headers

    def _build_payload(self, prompt: str, system_instruction: Optional[str] = None, json_mode: bool = False) -> Dict[str, Any]:
        messages = []
        if system_instruction:
            messages.append({"role": "system", "content": system_instruction})
        messages.append({"role": "user", "content": prompt})

        payload = {
            "model": self.model,
            "messages": messages,
            "temperature": 0.3 if json_mode else 0.7,
        }
        if json_mode:
            payload["response_format"] = {"type": "json_object"}
        return payload

    def _sanitize_json_text(self, text: str) -> str:
        s = text.strip()
        if s.startswith("```json"):
            s = s[7:]
        elif s.startswith("```"):
            s = s[3:]
        if s.endswith("```"):
            s = s[:-3]
        return s.strip()

    async def agenerate(self, prompt: str, system_instruction: Optional[str] = None, json_mode: bool = False) -> Optional[Any]:
        """Gọi bất đồng bộ tới upstream tương thích OpenAI với Retry có kiểm soát và Circuit Breaker."""
        if not self.is_available():
            return None

        headers = self._build_headers()
        payload = self._build_payload(prompt, system_instruction, json_mode)

        attempt = 0
        while attempt <= self.retry_max:
            attempt += 1
            try:
                async with httpx.AsyncClient(timeout=self.timeout_sec) as client:
                    resp = await client.post(self.chat_endpoint, json=payload, headers=headers)

                # 1. Xử lý 401 / 403 (Sai API Key) -> Khóa provider ngay, KHÔNG retry
                if resp.status_code in (401, 403):
                    self.is_healthy = False
                    self.is_enabled = False
                    self.last_error_code = "API_KEY_INVALID"
                    self.last_error = f"Xác thực thất bại HTTP {resp.status_code}: {resp.text[:200]}"
                    logger.error(f"[AI] [{self.name.upper()}] {self.last_error}")
                    return None

                # 2. Xử lý 429 (Hết hạn mức / Quota / Rate limit)
                if resp.status_code == 429:
                    self.is_healthy = False
                    self.last_error_code = "QUOTA_EXCEEDED"
                    retry_after = 60.0
                    ra_hdr = resp.headers.get("retry-after")
                    if ra_hdr and ra_hdr.isdigit():
                        retry_after = float(ra_hdr)
                    self.mark_cooldown(seconds=retry_after, reason=f"429 Quota Exceeded (Retry-After: {retry_after}s)")
                    return None

                # 3. Lỗi 5xx hoặc mã khác
                if resp.status_code != 200:
                    err_msg = f"HTTP {resp.status_code}: {resp.text[:200]}"
                    self.last_error = err_msg
                    self.last_error_code = f"HTTP_{resp.status_code}"
                    if attempt <= self.retry_max:
                        await asyncio.sleep(0.5 * attempt)
                        continue
                    self.mark_cooldown(seconds=30.0, reason=err_msg)
                    return None

                # 4. Thành công HTTP 200 OK
                data = resp.json()
                choices = data.get("choices", [])
                if not choices:
                    return None
                content = choices[0].get("message", {}).get("content", "")
                self.is_healthy = True
                self.failure_count = 0
                self.last_error = None
                self.last_error_code = None

                if json_mode:
                    sanitized = self._sanitize_json_text(content)
                    return json.loads(sanitized)
                return content

            except httpx.TimeoutException:
                self.last_error_code = "TIMEOUT"
                self.last_error = f"Timeout sau {self.timeout_sec}s"
                if attempt <= self.retry_max:
                    await asyncio.sleep(0.5 * attempt)
                    continue
                self.mark_cooldown(seconds=30.0, reason="Timeout liên tục")
                return None
            except Exception as ex:
                self.last_error_code = "NETWORK_ERROR"
                self.last_error = str(ex)
                if attempt <= self.retry_max:
                    await asyncio.sleep(0.5 * attempt)
                    continue
                self.mark_cooldown(seconds=30.0, reason=str(ex))
                return None

        return None

    def generate(self, prompt: str, system_instruction: Optional[str] = None, json_mode: bool = False) -> Optional[Any]:
        """Gọi đồng bộ tới upstream tương thích OpenAI."""
        if not self.is_available():
            return None

        headers = self._build_headers()
        payload = self._build_payload(prompt, system_instruction, json_mode)

        attempt = 0
        while attempt <= self.retry_max:
            attempt += 1
            try:
                with httpx.Client(timeout=self.timeout_sec) as client:
                    resp = client.post(self.chat_endpoint, json=payload, headers=headers)

                if resp.status_code in (401, 403):
                    self.is_healthy = False
                    self.is_enabled = False
                    self.last_error_code = "API_KEY_INVALID"
                    self.last_error = f"Xác thực thất bại HTTP {resp.status_code}: {resp.text[:200]}"
                    return None

                if resp.status_code == 429:
                    self.is_healthy = False
                    self.last_error_code = "QUOTA_EXCEEDED"
                    self.mark_cooldown(seconds=60.0, reason="429 Quota Exceeded")
                    return None

                if resp.status_code != 200:
                    if attempt <= self.retry_max:
                        time.sleep(0.5 * attempt)
                        continue
                    self.mark_cooldown(seconds=30.0, reason=f"HTTP {resp.status_code}")
                    return None

                data = resp.json()
                choices = data.get("choices", [])
                if not choices:
                    return None
                content = choices[0].get("message", {}).get("content", "")
                self.is_healthy = True
                self.failure_count = 0
                self.last_error = None
                self.last_error_code = None

                if json_mode:
                    sanitized = self._sanitize_json_text(content)
                    return json.loads(sanitized)
                return content
            except Exception as ex:
                if attempt <= self.retry_max:
                    time.sleep(0.5 * attempt)
                    continue
                self.mark_cooldown(seconds=30.0, reason=str(ex))
                return None
        return None


class LLMEngine:
    """Bộ điều phối Model Đa Nhà Cung Cấp (Multi-Provider Engine) cho 10 Subagents.
    Tích hợp 6 nguồn model: OmniRouter Gateway, Google Gemini, Groq, OpenRouter, Mistral AI và Cloudflare Workers AI.
    Hỗ trợ cơ chế Fallback Cascade, Unified Concurrency Limiter, Circuit Breaker và Cache Health Check.
    """

    def __init__(self):
        # 1. Thiết lập chung
        self.timeout_sec = float(os.getenv("LLM_REQUEST_TIMEOUT_SECONDS", "30.0"))
        self.retry_max = int(os.getenv("LLM_RETRY_MAX", "2"))

        # 2. Provider 1: OmniRouter Gateway (Go Backend Local/Internal Service)
        omnirouter_base = os.getenv("OMNIROUTER_BASE_URL", "").strip()
        omnirouter_key = os.getenv("OMNIROUTER_API_KEY", "").strip()
        omnirouter_model = os.getenv("OMNIROUTER_MODEL", "gemini-3.8-flash-high").strip()
        if not omnirouter_key:
            omnirouter_key = os.getenv("MASTER_ADMIN_KEY", "sk-supportflastdev.io.vn-master-live").strip()
        self.omnirouter_provider = OpenAICompatibleProvider(
            name="omnirouter",
            base_url=omnirouter_base or "http://127.0.0.1:20128/v1",
            api_key=omnirouter_key if (omnirouter_base or os.getenv("OMNIROUTER_ENABLED") == "true") else "",
            model=omnirouter_model,
            timeout_sec=self.timeout_sec,
            retry_max=self.retry_max
        )

        # 3. Provider 2: Google Gemini (Native SDK)
        self.gemini_key = os.getenv("GEMINI_API_KEY", "").strip()
        self.gemini_model = os.getenv("GEMINI_MODEL", "gemini-3.8-flash").strip()
        self.gemini_client = None
        self.gemini_legacy_model = None
        self.gemini_enabled = bool(self.gemini_key)

        if self.gemini_enabled:
            try:
                from google import genai
                self.gemini_client = genai.Client(api_key=self.gemini_key)
                logger.info(f"[AI] [GEMINI] Initialized successfully with google.genai (model={self.gemini_model}).")
            except ImportError:
                try:
                    import google.generativeai as legacy_genai
                    legacy_genai.configure(api_key=self.gemini_key)
                    self.gemini_legacy_model = legacy_genai.GenerativeModel(self.gemini_model)
                    logger.info(f"[AI] [GEMINI] Initialized with legacy google.generativeai (model={self.gemini_model}).")
                except Exception as ex:
                    logger.error(f"[AI] [GEMINI] Failed to initialize legacy SDK: {ex}")
                    self.gemini_enabled = False
            except Exception as e:
                logger.error(f"[AI] [GEMINI] Failed to initialize google.genai: {e}")
                self.gemini_enabled = False

        # 4. Provider 3: Groq (Ultra-Fast Inference - OpenAI-compatible)
        groq_key = os.getenv("GROQ_API_KEY", "").strip()
        groq_base = os.getenv("GROQ_BASE_URL", "https://api.groq.com/openai/v1").strip()
        groq_model = os.getenv("GROQ_MODEL", "llama-3.3-70b-versatile").strip()
        self.groq_provider = OpenAICompatibleProvider(
            name="groq",
            base_url=groq_base,
            api_key=groq_key,
            model=groq_model,
            timeout_sec=min(self.timeout_sec, 20.0),
            retry_max=self.retry_max
        )

        # 5. Provider 4: OpenRouter (Multi-Model & Free Catalog - OpenAI-compatible)
        openrouter_key = os.getenv("OPENROUTER_API_KEY", "").strip()
        openrouter_base = os.getenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1").strip()
        openrouter_model = os.getenv("OPENROUTER_MODEL", "openrouter/free").strip()
        self.openrouter_provider = OpenAICompatibleProvider(
            name="openrouter",
            base_url=openrouter_base,
            api_key=openrouter_key,
            model=openrouter_model,
            extra_headers={
                "HTTP-Referer": "https://supportflastdev.io.vn",
                "X-Title": "SupportFlastDev AI Subagents"
            },
            timeout_sec=self.timeout_sec,
            retry_max=self.retry_max
        )

        # 6. Provider 5: Mistral AI (Independent Fallback - OpenAI-compatible)
        mistral_key = os.getenv("MISTRAL_API_KEY", "").strip()
        mistral_base = os.getenv("MISTRAL_BASE_URL", "https://api.mistral.ai/v1").strip()
        mistral_model = os.getenv("MISTRAL_MODEL", "mistral-small-latest").strip()
        self.mistral_provider = OpenAICompatibleProvider(
            name="mistral",
            base_url=mistral_base,
            api_key=mistral_key,
            model=mistral_model,
            timeout_sec=self.timeout_sec,
            retry_max=self.retry_max
        )

        # 7. Provider 6: Cloudflare Workers AI (Daily Compute Budget - OpenAI-compatible)
        cf_token = os.getenv("CLOUDFLARE_API_TOKEN", "").strip()
        cf_account = os.getenv("CLOUDFLARE_ACCOUNT_ID", "").strip()
        cf_base = os.getenv("CLOUDFLARE_BASE_URL", "").strip()
        if not cf_base and cf_account:
            cf_base = f"https://api.cloudflare.com/client/v4/accounts/{cf_account}/ai/v1"
        elif not cf_base:
            cf_base = "https://api.cloudflare.com/client/v4/accounts/unknown/ai/v1"
        cf_model = os.getenv("CLOUDFLARE_MODEL", "@cf/meta/llama-3.1-8b-instruct").strip()
        self.cloudflare_provider = OpenAICompatibleProvider(
            name="cloudflare",
            base_url=cf_base,
            api_key=cf_token,
            model=cf_model,
            timeout_sec=self.timeout_sec,
            retry_max=self.retry_max
        )

        # Danh sách các nhà cung cấp OpenAI-compatible
        self.openai_providers = [
            self.omnirouter_provider,
            self.groq_provider,
            self.openrouter_provider,
            self.mistral_provider,
            self.cloudflare_provider
        ]

        # Kiểm tra tính sẵn sàng tổng thể: có ít nhất 1 nguồn API hoạt động
        self.is_enabled = self.gemini_enabled or any(p.is_enabled for p in self.openai_providers)

        # Xác định model đại diện chính
        if self.gemini_enabled:
            self.model_name = self.gemini_model
        elif self.groq_provider.is_enabled:
            self.model_name = self.groq_provider.model
        elif self.openrouter_provider.is_enabled:
            self.model_name = self.openrouter_provider.model
        elif self.mistral_provider.is_enabled:
            self.model_name = self.mistral_provider.model
        elif self.cloudflare_provider.is_enabled:
            self.model_name = self.cloudflare_provider.model
        elif self.omnirouter_provider.is_enabled:
            self.model_name = self.omnirouter_provider.model
        else:
            self.model_name = "on-device-rule-based"

        self.last_error: Optional[str] = None
        self.last_error_code: Optional[str] = None
        self.is_healthy: bool = False
        self._last_health_cache: Optional[Dict[str, Any]] = None
        self._last_health_check_time: float = 0.0
        self._health_cache_ttl: float = 60.0

        active_names = []
        if self.omnirouter_provider.is_enabled:
            active_names.append("OmniRouter")
        if self.gemini_enabled:
            active_names.append("Gemini")
        for p in [self.groq_provider, self.openrouter_provider, self.mistral_provider, self.cloudflare_provider]:
            if p.is_enabled:
                active_names.append(p.name.capitalize())

        if active_names:
            logger.info(f"[AI] Multi-Provider LLM Engine activated with: {', '.join(active_names)}.")
        else:
            logger.info("[AI] No external LLM keys configured. Running in high-speed On-Device Rule-Based mode.")

    def _classify_error(self, err_str: str) -> str:
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

    def _get_ordered_providers(self, mode: str = "fast") -> List[Tuple[str, Any]]:
        """Sắp xếp thứ tự gọi provider theo chế độ (fast / parallel / deep / default)."""
        ordered = []
        # Chế độ fast: Ưu tiên Groq (độ trễ thấp nhất) -> OmniRouter -> Gemini -> OpenRouter -> Mistral -> Cloudflare
        if mode == "fast" and self.groq_provider.is_available():
            ordered.append(("groq", self.groq_provider))

        if self.omnirouter_provider.is_available():
            ordered.append(("omnirouter", self.omnirouter_provider))

        if self.gemini_enabled:
            ordered.append(("gemini", None))

        for p in self.openai_providers:
            if (p.name, p) not in ordered and p.is_available():
                ordered.append((p.name, p))

        return ordered

    async def _agenerate_gemini(self, prompt: str, system_instruction: Optional[str] = None, json_mode: bool = False) -> Optional[Any]:
        """Gọi bất đồng bộ tới Google Gemini."""
        if not self.gemini_enabled:
            return None

        # 1. Dùng google.genai client hiện đại
        if self.gemini_client and hasattr(self.gemini_client, "aio") and hasattr(self.gemini_client.aio, "models"):
            try:
                from google.genai import types
                cfg_kwargs = {}
                if json_mode:
                    cfg_kwargs["response_mime_type"] = "application/json"
                if system_instruction:
                    cfg_kwargs["system_instruction"] = system_instruction

                config = types.GenerateContentConfig(**cfg_kwargs) if cfg_kwargs else None
                response = await self.gemini_client.aio.models.generate_content(
                    model=self.gemini_model,
                    contents=prompt,
                    config=config
                )
                raw_text = response.text.strip()
                if json_mode:
                    if raw_text.startswith("```json"):
                        raw_text = raw_text[7:]
                    elif raw_text.startswith("```"):
                        raw_text = raw_text[3:]
                    if raw_text.endswith("```"):
                        raw_text = raw_text[:-3]
                    return json.loads(raw_text.strip())
                return raw_text
            except Exception as e:
                logger.error(f"[AI] [GEMINI] Error: {e}")
                self.last_error = str(e)
                self.last_error_code = self._classify_error(str(e))
                return None

        # 2. Fallback sang threadpool nếu chạy SDK đồng bộ
        return await asyncio.to_thread(self._generate_gemini_sync, prompt, system_instruction, json_mode)

    def _generate_gemini_sync(self, prompt: str, system_instruction: Optional[str] = None, json_mode: bool = False) -> Optional[Any]:
        """Gọi đồng bộ tới Google Gemini."""
        if not self.gemini_enabled:
            return None

        if self.gemini_client:
            try:
                from google.genai import types
                cfg_kwargs = {}
                if json_mode:
                    cfg_kwargs["response_mime_type"] = "application/json"
                if system_instruction:
                    cfg_kwargs["system_instruction"] = system_instruction
                config = types.GenerateContentConfig(**cfg_kwargs) if cfg_kwargs else None
                response = self.gemini_client.models.generate_content(
                    model=self.gemini_model,
                    contents=prompt,
                    config=config
                )
                raw_text = response.text.strip()
                if json_mode:
                    if raw_text.startswith("```json"):
                        raw_text = raw_text[7:]
                    elif raw_text.startswith("```"):
                        raw_text = raw_text[3:]
                    if raw_text.endswith("```"):
                        raw_text = raw_text[:-3]
                    return json.loads(raw_text.strip())
                return raw_text
            except Exception as e:
                logger.error(f"[AI] [GEMINI_SYNC] Error: {e}")
                return None

        if self.gemini_legacy_model:
            try:
                import google.generativeai as legacy_genai
                model = self.gemini_legacy_model
                if system_instruction:
                    model = legacy_genai.GenerativeModel(self.gemini_model, system_instruction=system_instruction)
                gen_cfg = legacy_genai.GenerationConfig(response_mime_type="application/json") if json_mode else None
                response = model.generate_content(prompt, generation_config=gen_cfg)
                if json_mode:
                    return json.loads(response.text)
                return response.text
            except Exception as e:
                logger.error(f"[AI] [GEMINI_LEGACY] Error: {e}")
                return None

        return None

    async def agenerate_text(self, prompt: str, system_instruction: Optional[str] = None, mode: str = "fast") -> Optional[str]:
        """Gọi LLM bất đồng bộ dạng Text qua Cascade Fallback với Unified Concurrency Limiter."""
        if not self.is_enabled:
            return None

        limiter = get_unified_limiter()
        acquired = await limiter.acquire_async(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Async LLM Limiter timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            providers = self._get_ordered_providers(mode=mode)
            for prov_name, prov_obj in providers:
                try:
                    if prov_name == "gemini":
                        res = await self._agenerate_gemini(prompt, system_instruction, json_mode=False)
                    else:
                        res = await prov_obj.agenerate(prompt, system_instruction, json_mode=False)

                    if res:
                        self.is_healthy = True
                        return res
                except Exception as ex:
                    logger.warning(f"[AI] Provider {prov_name} failed: {ex}. Falling back to next provider...")
            return None
        finally:
            limiter.release_async()

    async def agenerate_json(self, prompt: str, system_instruction: Optional[str] = None, mode: str = "fast") -> Optional[Dict[str, Any]]:
        """Gọi LLM bất đồng bộ dạng JSON qua Cascade Fallback với Unified Concurrency Limiter."""
        if not self.is_enabled:
            return None

        limiter = get_unified_limiter()
        acquired = await limiter.acquire_async(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Async LLM Limiter timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            providers = self._get_ordered_providers(mode=mode)
            for prov_name, prov_obj in providers:
                try:
                    if prov_name == "gemini":
                        res = await self._agenerate_gemini(prompt, system_instruction, json_mode=True)
                    else:
                        res = await prov_obj.agenerate(prompt, system_instruction, json_mode=True)

                    if res and isinstance(res, dict):
                        self.is_healthy = True
                        return res
                except Exception as ex:
                    logger.warning(f"[AI] Provider {prov_name} (json) failed: {ex}. Falling back to next provider...")
            return None
        finally:
            limiter.release_async()

    def generate_text(self, prompt: str, system_instruction: Optional[str] = None, mode: str = "fast") -> Optional[str]:
        """Gọi LLM đồng bộ dạng Text có kiểm soát qua Unified Concurrency Limiter."""
        if not self.is_enabled:
            return None

        limiter = get_unified_limiter()
        acquired = limiter.acquire_sync(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Sync LLM Limiter timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            providers = self._get_ordered_providers(mode=mode)
            for prov_name, prov_obj in providers:
                try:
                    if prov_name == "gemini":
                        res = self._generate_gemini_sync(prompt, system_instruction, json_mode=False)
                    else:
                        res = prov_obj.generate(prompt, system_instruction, json_mode=False)

                    if res:
                        self.is_healthy = True
                        return res
                except Exception as ex:
                    logger.warning(f"[AI] Provider {prov_name} failed: {ex}. Falling back...")
            return None
        finally:
            limiter.release_sync()

    def generate_json(self, prompt: str, system_instruction: Optional[str] = None, mode: str = "fast") -> Optional[Dict[str, Any]]:
        """Gọi LLM đồng bộ dạng JSON có kiểm soát qua Unified Concurrency Limiter."""
        if not self.is_enabled:
            return None

        limiter = get_unified_limiter()
        acquired = limiter.acquire_sync(timeout=10.0)
        if not acquired:
            logger.warning("[AI] Sync LLM Limiter timeout - Hệ thống đang đạt ngưỡng tối đa MAX_INFLIGHT_LLM.")
            return None

        try:
            providers = self._get_ordered_providers(mode=mode)
            for prov_name, prov_obj in providers:
                try:
                    if prov_name == "gemini":
                        res = self._generate_gemini_sync(prompt, system_instruction, json_mode=True)
                    else:
                        res = prov_obj.generate(prompt, system_instruction, json_mode=True)

                    if res and isinstance(res, dict):
                        self.is_healthy = True
                        return res
                except Exception as ex:
                    logger.warning(f"[AI] Provider {prov_name} (json) failed: {ex}. Falling back...")
            return None
        finally:
            limiter.release_sync()

    async def check_health(self, force_refresh: bool = False) -> Dict[str, Any]:
        """Kiểm tra kết nối thực tế tới tất cả các nhà cung cấp có Cache TTL 60s để chống cạn kiệt Quota."""
        if not self.is_enabled:
            return {
                "status": "offline",
                "code": "NO_PROVIDERS_CONFIGURED",
                "message": "Chưa cấu hình API Key của nhà cung cấp nào. Hệ thống đang chạy ở chế độ On-Device Rule-Based an toàn.",
                "model": "on-device-rule-based",
                "fallback_active": True,
                "providers": {}
            }

        now = time.time()
        if not force_refresh and self._last_health_cache and (now - self._last_health_check_time < self._health_cache_ttl):
            res = dict(self._last_health_cache)
            res["cached"] = True
            res["cache_age_sec"] = round(now - self._last_health_check_time, 1)
            return res

        providers_status = {}
        any_online = False

        # 1. Kiểm tra Gemini
        if self.gemini_enabled:
            t0 = time.time()
            try:
                test_gemini = await asyncio.wait_for(
                    self._agenerate_gemini("ping", system_instruction="Trả lời đúng 1 từ: PONG"),
                    timeout=4.0
                )
                lat = round((time.time() - t0) * 1000, 2)
                if test_gemini:
                    providers_status["gemini"] = {"status": "online", "model": self.gemini_model, "latency_ms": lat}
                    any_online = True
                else:
                    providers_status["gemini"] = {"status": "degraded", "model": self.gemini_model, "error": "Empty response"}
            except Exception as ex:
                providers_status["gemini"] = {"status": "degraded", "model": self.gemini_model, "error": str(ex)}

        # 2. Kiểm tra các OpenAI-compatible providers
        for p in self.openai_providers:
            if p.is_enabled:
                t0 = time.time()
                try:
                    test_res = await asyncio.wait_for(
                        p.agenerate("ping", system_instruction="Reply with 1 word: PONG"),
                        timeout=4.0
                    )
                    lat = round((time.time() - t0) * 1000, 2)
                    if test_res:
                        providers_status[p.name] = {"status": "online", "model": p.model, "latency_ms": lat}
                        any_online = True
                    else:
                        providers_status[p.name] = {"status": "degraded", "model": p.model, "error": p.last_error or "Empty response"}
                except Exception as ex:
                    providers_status[p.name] = {"status": "degraded", "model": p.model, "error": str(ex)}

        self.is_healthy = any_online
        res = {
            "status": "online" if any_online else "degraded",
            "code": "OK" if any_online else "ALL_PROVIDERS_UNAVAILABLE",
            "model": self.model_name,
            "fallback_active": not any_online,
            "cached": False,
            "providers": providers_status
        }
        self._last_health_cache = res
        self._last_health_check_time = time.time()
        return res

llm_engine = LLMEngine()
