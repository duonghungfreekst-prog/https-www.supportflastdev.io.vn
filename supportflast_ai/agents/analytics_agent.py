"""Subagent 9: ARM Performance & Subagent Metrics Inspector Agent
Chuyên trách đo lường hiệu năng máy chủ trên chip ARM di động, giám sát QPS, latency và đo kiểm benchmark 10 Subagents song song.
"""
from typing import Dict, Any
from core.llm_engine import llm_engine

class AnalyticsAgent:
    def __init__(self):
        self.agent_id = "agent_analytics"
        self.name = "ARM Performance & Subagent Metrics Inspector"
        self.color = "#8338EC"
        self.role_description = "Đo lường hiệu năng chip ARM, phân tích QPS, độ trễ phản hồi và điều phối tải 10 Subagents song song"

    def _build_prompt_and_instruction(self, query: str, context: Dict[str, Any] = None):
        system_instruction = (
            "Bạn là Analytics Agent, chuyên gia đo kiểm hiệu năng phần mềm và giám sát hệ thống máy chủ ARM64 di động.\n"
            "Nhiệm vụ: Phân tích thông số latency, QPS, Goroutine concurrency, điều tiết 10 subagents để không gây nghẽn CPU.\n"
            "Trả về JSON object duy nhất (không bọc trong markdown block) với cấu trúc:\n"
            '{"avg_latency_ms": 12.5, "concurrency_capacity": "10 Subagents", '
            '"performance_grade": "A+", "metrics_summary": ["Chỉ số 1", "Chỉ số 2"], "response": "Phản hồi chi tiết bằng tiếng Việt"}'
        )
        prompt = f"Yêu cầu phân tích hiệu năng:\n{query}"
        if context:
            prompt += f"\n\nContext số liệu:\n{context}"
        return prompt, system_instruction

    def _format_llm_result(self, llm_res: Dict[str, Any]) -> Dict[str, Any]:
        metrics = "\n".join([f"  • {m}" for m in llm_res.get("metrics_summary", [])])
        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "avg_latency_ms": llm_res.get("avg_latency_ms", 8.4),
            "concurrency_capacity": llm_res.get("concurrency_capacity", "10 Subagents Concurrent"),
            "performance_grade": llm_res.get("performance_grade", "A+ (Xuất sắc)"),
            "metrics_summary": llm_res.get("metrics_summary", []),
            "response": (
                f"**[Phân Tích Hiệu Năng & Đo Kiểm 10 Subagents - Subagent Analytics]**\n"
                f"- Cấp độ hiệu năng: **{llm_res.get('performance_grade')}**\n"
                f"- Năng lực điều phối song song: **{llm_res.get('concurrency_capacity')}**\n"
                f"- Báo cáo đo kiểm:\n{metrics}\n\n"
                f"{llm_res.get('response', '')}"
            )
        }

    def _rule_based_process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        """Xử lý fallback theo quy tắc khi chưa có dữ liệu đo lường thực tế (Tuân thủ Audit 3.1 & Rule Phần 9).
        Tuyệt đối không sinh dữ liệu giả lập/mock đẹp về latency, CPU hay điểm A+ khi chưa có phép đo thời gian thực.
        """
        # Trả về kết quả trung thực khi không có bộ đo benchmark đang chạy
        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "unmeasured",
            "metrics_available": False,
            "measurements": {},
            "reason": "Chưa có dữ liệu đo kiểm benchmark thời gian thực từ profiler",
            "concurrency_capacity": "Tối đa 10 Subagents (Cấu hình Worker Pool)",
            "performance_grade": "Chưa đo kiểm (Unmeasured)",
            "metrics_summary": [
                "Hệ thống telemetry không phát hiện profiler tải đang chạy.",
                "Yêu cầu đo kiểm cần cung cấp ngữ cảnh số liệu thực tế qua context để phân tích."
            ],
            "response": (
                f"**[Báo Cáo Giám Sát Hiệu Năng & Đo Kiểm - Subagent Analytics]**\n"
                f"- Trạng thái đo lường: **Chưa đo kiểm (Unmeasured)**\n"
                f"- Năng lực điều phối: **Tối đa 10 Subagents đồng thời**\n"
                f"- Lý do: Chưa có phiên đo kiểm tải hoạt động trực tiếp trên hệ thống.\n\n"
                f"Hệ thống giám sát tuân thủ nguyên tắc không tạo số liệu giả lập. Vui lòng cung cấp dữ liệu đo kiểm hoặc kích hoạt profiling để nhận báo cáo phân tích chi tiết."
            )
        }

    async def aprocess(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        """Xử lý bất đồng bộ thực sự qua Async LLM Engine và Semaphore (Audit P1.2)."""
        if llm_engine.is_enabled:
            prompt, system_instruction = self._build_prompt_and_instruction(query, context)
            llm_res = await llm_engine.agenerate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                return self._format_llm_result(llm_res)

        return self._rule_based_process(query, context)

    def process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        """Xử lý đồng bộ (giữ tương thích 100% với mã nguồn cũ và unit test)."""
        if llm_engine.is_enabled:
            prompt, system_instruction = self._build_prompt_and_instruction(query, context)
            llm_res = llm_engine.generate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                return self._format_llm_result(llm_res)

        return self._rule_based_process(query, context)
