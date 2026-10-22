import os
import json
import logging
from typing import Dict, Any, Optional

logger = logging.getLogger("supportflast_ai")

class LLMEngine:
    def __init__(self):
        self.api_key = os.getenv("GEMINI_API_KEY")
        self.is_enabled = bool(self.api_key)
        self.client = None
        self.legacy_model = None
        self.model_name = os.getenv("GEMINI_MODEL", "gemini-3.8-flash-high")

        if self.is_enabled:
            # Ưu tiên SDK mới nhất: google.genai
            try:
                from google import genai
                self.client = genai.Client(api_key=self.api_key)
                logger.info("[AI] LLM Engine initialized successfully with google.genai SDK.")
            except ImportError:
                try:
                    import google.generativeai as legacy_genai
                    legacy_genai.configure(api_key=self.api_key)
                    self.legacy_model = legacy_genai.GenerativeModel("gemini-3.8-flash-high")
                    logger.info("[AI] LLM Engine initialized with legacy google.generativeai SDK.")
                except Exception as ex:
                    logger.error(f"[AI] Failed to initialize legacy LLM SDK: {ex}")
                    self.is_enabled = False
            except Exception as e:
                logger.error(f"[AI] Failed to initialize google.genai client: {e}")
                self.is_enabled = False
        else:
            logger.info("[AI] GEMINI_API_KEY is not configured. Running in high-speed On-Device Rule-Based mode.")

    def generate_json(self, prompt: str, system_instruction: str = None) -> Optional[Dict[str, Any]]:
        if not self.is_enabled:
            return None

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
                return json.loads(raw_text.strip())
            except Exception as e:
                logger.error(f"[AI] google.genai generate_json error: {e}")
                return None

        # 2. Dùng legacy client nếu có
        if self.legacy_model:
            try:
                import google.generativeai as legacy_genai
                model = self.legacy_model
                if system_instruction:
                    model = legacy_genai.GenerativeModel(
                        "gemini-3.8-flash-high",
                        system_instruction=system_instruction
                    )
                response = model.generate_content(
                    prompt,
                    generation_config=legacy_genai.GenerationConfig(
                        response_mime_type="application/json"
                    )
                )
                return json.loads(response.text)
            except Exception as e:
                logger.error(f"[AI] Legacy LLM Error: {e}")
                return None

        return None

    def generate_text(self, prompt: str, system_instruction: str = None) -> Optional[str]:
        if not self.is_enabled:
            return None

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
                return response.text
            except Exception as e:
                logger.error(f"[AI] google.genai generate_text error: {e}")
                return None

        # 2. Dùng legacy client nếu có
        if self.legacy_model:
            try:
                import google.generativeai as legacy_genai
                model = self.legacy_model
                if system_instruction:
                    model = legacy_genai.GenerativeModel(
                        "gemini-3.8-flash-high",
                        system_instruction=system_instruction
                    )
                response = model.generate_content(prompt)
                return response.text
            except Exception as e:
                logger.error(f"[AI] Legacy LLM Error: {e}")
                return None

        return None

llm_engine = LLMEngine()
