import os
import json
import logging
import google.generativeai as genai
from typing import Dict, Any, Optional

logger = logging.getLogger("supportflast_ai")

class LLMEngine:
    def __init__(self):
        self.api_key = os.getenv("GEMINI_API_KEY")
        self.is_enabled = bool(self.api_key)
        
        if self.is_enabled:
            genai.configure(api_key=self.api_key)
            self.model_flash = genai.GenerativeModel("gemini-1.5-flash")
            logger.info("LLM Engine initialized with Gemini API.")
        else:
            logger.warning("GEMINI_API_KEY is missing. LLM Engine is DISABLED. Falling back to rule-based logic.")

    def generate_json(self, prompt: str, system_instruction: str = None) -> Optional[Dict[str, Any]]:
        if not self.is_enabled:
            return None
            
        try:
            model = self.model_flash
            if system_instruction:
                model = genai.GenerativeModel(
                    "gemini-1.5-flash",
                    system_instruction=system_instruction
                )
            
            response = model.generate_content(
                prompt,
                generation_config=genai.GenerationConfig(
                    response_mime_type="application/json"
                )
            )
            
            return json.loads(response.text)
        except Exception as e:
            logger.error(f"LLM Error: {e}")
            return None

    def generate_text(self, prompt: str, system_instruction: str = None) -> Optional[str]:
        if not self.is_enabled:
            return None
            
        try:
            model = self.model_flash
            if system_instruction:
                model = genai.GenerativeModel(
                    "gemini-1.5-flash",
                    system_instruction=system_instruction
                )
            
            response = model.generate_content(prompt)
            return response.text
        except Exception as e:
            logger.error(f"LLM Error: {e}")
            return None

llm_engine = LLMEngine()
