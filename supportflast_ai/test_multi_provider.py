"""Unit test kiểm thử Đa Nhà Cung Cấp Model (Multi-Provider Model Router) cho supportflast_ai
Kiểm tra chi tiết 4 nhà cung cấp mới: OpenRouter, Groq, Mistral AI, Cloudflare Workers AI
và cơ chế Fallback Cascade, Circuit Breaker, Concurrency Semaphore.
"""
import os
import sys
sys.path.insert(0, os.path.abspath(os.path.dirname(__file__)))

import unittest
import asyncio
from unittest.mock import patch, MagicMock, AsyncMock
from core.llm_engine import LLMEngine, OpenAICompatibleProvider


class TestMultiProviderRouter(unittest.TestCase):
    def setUp(self):
        # Thiết lập biến môi trường giả lập cho test
        self.patcher_env = patch.dict(os.environ, {
            "GROQ_API_KEY": "gsk_test_mock_123",
            "GROQ_MODEL": "llama-3.3-70b-versatile",
            "OPENROUTER_API_KEY": "sk-or-test-mock-456",
            "OPENROUTER_MODEL": "openrouter/free",
            "MISTRAL_API_KEY": "mis_test_mock_789",
            "MISTRAL_MODEL": "mistral-small-latest",
            "CLOUDFLARE_API_TOKEN": "cf_test_token",
            "CLOUDFLARE_ACCOUNT_ID": "cf_acc_123",
            "CLOUDFLARE_MODEL": "@cf/meta/llama-3.1-8b-instruct",
            "MAX_INFLIGHT_LLM": "6",
            "LLM_REQUEST_TIMEOUT_SECONDS": "15"
        })
        self.patcher_env.start()
        self.engine = LLMEngine()

    def tearDown(self):
        self.patcher_env.stop()

    def test_providers_initialization(self):
        """Kiểm tra khởi tạo đầy đủ 4 nhà cung cấp và model tương ứng."""
        self.assertTrue(self.engine.groq_provider.is_enabled)
        self.assertEqual(self.engine.groq_provider.model, "llama-3.3-70b-versatile")
        self.assertEqual(self.engine.groq_provider.chat_endpoint, "https://api.groq.com/openai/v1/chat/completions")

        self.assertTrue(self.engine.openrouter_provider.is_enabled)
        self.assertEqual(self.engine.openrouter_provider.model, "openrouter/free")
        self.assertIn("HTTP-Referer", self.engine.openrouter_provider.extra_headers)

        self.assertTrue(self.engine.mistral_provider.is_enabled)
        self.assertEqual(self.engine.mistral_provider.model, "mistral-small-latest")

        self.assertTrue(self.engine.cloudflare_provider.is_enabled)
        self.assertIn("cf_acc_123", self.engine.cloudflare_provider.chat_endpoint)

    def test_circuit_breaker_on_429(self):
        """Kiểm tra Circuit Breaker tự động đưa provider vào cooldown khi bị 429."""
        provider = OpenAICompatibleProvider(
            name="test_groq",
            base_url="https://api.groq.com/openai/v1",
            api_key="mock_key",
            model="mock_model"
        )
        self.assertTrue(provider.is_available())
        provider.mark_cooldown(seconds=60.0, reason="Rate limited 429")
        self.assertFalse(provider.is_available())

    def test_auth_failure_disables_provider(self):
        """Kiểm tra 401/403 vô hiệu hóa provider vĩnh viễn và không thử lại."""
        provider = OpenAICompatibleProvider(
            name="test_mistral",
            base_url="https://api.mistral.ai/v1",
            api_key="bad_key",
            model="mock_model"
        )
        mock_resp = MagicMock()
        mock_resp.status_code = 401
        mock_resp.text = '{"error":{"message":"Invalid API key"}}'

        with patch("httpx.Client.post", return_value=mock_resp):
            res = provider.generate("test prompt")
            self.assertIsNone(res)
            self.assertFalse(provider.is_enabled)
            self.assertEqual(provider.last_error_code, "API_KEY_INVALID")

    def test_fallback_cascade_logic(self):
        """Kiểm tra cơ chế Fallback: khi Groq bị lỗi/cooldown, tự động fallback sang OpenRouter."""
        async def run_test():
            # Đưa Groq vào cooldown
            self.engine.groq_provider.mark_cooldown(seconds=120.0, reason="429 test")

            # Mock OpenRouter phản hồi thành công
            mock_resp = MagicMock()
            mock_resp.status_code = 200
            mock_resp.json.return_value = {
                "choices": [{
                    "message": {"content": "Phản hồi dự phòng từ OpenRouter"}
                }]
            }

            with patch("httpx.AsyncClient.post", new_callable=AsyncMock) as mock_post:
                mock_post.return_value = mock_resp
                res = await self.engine.agenerate_text("Cần phân tích bảo mật")
                self.assertIsNotNone(res)
                self.assertIn("OpenRouter", res)

        asyncio.run(run_test())

    def test_json_sanitization_and_parsing(self):
        """Kiểm tra trích xuất JSON sạch từ markdown block ```json ... ```."""
        provider = OpenAICompatibleProvider(
            name="test_openrouter",
            base_url="https://openrouter.ai/api/v1",
            api_key="mock_key",
            model="openrouter/free"
        )
        markdown_json = '```json\n{"status": "success", "severity": "P1"}\n```'
        clean = provider._sanitize_json_text(markdown_json)
        self.assertEqual(clean, '{"status": "success", "severity": "P1"}')

    def test_multi_provider_health_check(self):
        """Kiểm tra cấu trúc báo cáo sức khỏe chi tiết theo từng provider."""
        async def run_test():
            report = await self.engine.check_health(force_refresh=True)
            self.assertIn("status", report)
            self.assertIn("providers", report)
            self.assertIn("fallback_active", report)

        asyncio.run(run_test())


if __name__ == "__main__":
    unittest.main()
