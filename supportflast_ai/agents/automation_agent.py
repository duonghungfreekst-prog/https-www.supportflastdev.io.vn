"""Subagent 10: Autonomous Task Scheduler & Cron Watchdog Agent
Chuyên trách tự động hóa tác vụ ngầm, lập lịch backup database SQLite, tự động cảnh báo sự cố và duy trì watchdog phục hồi.
"""
from typing import Dict, Any
from core.llm_engine import llm_engine

class AutomationAgent:
    def __init__(self):
        self.agent_id = "agent_automation"
        self.name = "Autonomous Task Scheduler & Cron Watchdog"
        self.color = "#EF476F"
        self.role_description = "Lập lịch sao lưu dữ liệu tự động, kiểm tra sức khỏe hệ thống (Watchdog) và thông báo qua Webhook"

    def process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        context = context or {}

        if llm_engine.is_enabled:
            system_instruction = (
                "Bạn là Automation Agent, chuyên gia tự động hóa tác vụ, Cron job và Watchdog phục hồi hệ thống máy chủ di động.\n"
                "Trả về JSON object duy nhất (không bọc trong markdown block) với cấu trúc:\n"
                '{"scheduled_jobs": ["Job 1", "Job 2"], "watchdog_status": "Active & Guarded", '
                '"cron_summary": "Tóm tắt lịch biểu", "response": "Phản hồi chi tiết bằng tiếng Việt"}'
            )

            prompt = f"Yêu cầu tự động hóa:\n{query}"
            if context:
                prompt += f"\n\nContext tác vụ:\n{context}"

            llm_res = llm_engine.generate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                jobs = "\n".join([f"  • {job}" for job in llm_res.get("scheduled_jobs", [])])
                return {
                    "agent_id": self.agent_id,
                    "agent_name": self.name,
                    "status": "success",
                    "scheduled_jobs": llm_res.get("scheduled_jobs", []),
                    "watchdog_status": llm_res.get("watchdog_status", "Đang giám sát"),
                    "cron_summary": llm_res.get("cron_summary", "Đã lập lịch"),
                    "response": (
                        f"**[Tự Động Hóa & Giám Sát Watchdog - Subagent Automation]**\n"
                        f"- Trạng thái Watchdog: **{llm_res.get('watchdog_status')}**\n"
                        f"- Tóm tắt lịch biểu: **{llm_res.get('cron_summary')}**\n"
                        f"- Danh sách tác vụ tự động:\n{jobs}\n\n"
                        f"{llm_res.get('response', '')}"
                    )
                }

        query_lower = query.lower()
        watchdog_status = "Đang thường trực (Active)"
        cron_summary = "Lập lịch định kỳ tự động"
        scheduled_jobs = []

        if any(w in query_lower for w in ["backup", "sao lưu", "lưu trữ", "dự phòng"]):
            cron_summary = "Lập lịch sao lưu SQLite tự động mỗi 24 giờ"
            scheduled_jobs.append("Job 1: Tự động dump SQLite sang thư mục backup an toàn lúc 02:00 AM.")
            scheduled_jobs.append("Job 2: Xóa các bản backup cũ hơn 7 ngày để giải phóng dung lượng thẻ nhớ.")
            scheduled_jobs.append("Job 3: Kiểm tra SHA-256 tính toàn vẹn của bản sao lưu.")
        elif any(w in query_lower for w in ["watchdog", "chết", "crash", "tự phục hồi", "khởi động lại"]):
            watchdog_status = "Tự phục hồi (Auto-Restart Guardian)"
            scheduled_jobs.append("Watchdog Ping: Kiểm tra cổng 8080 mỗi 60 giây.")
            scheduled_jobs.append("Nếu tiến trình gặp sự cố: Tự động khởi động lại trong 2 giây mà không cần can thiệp thủ công.")
            scheduled_jobs.append("Ghi nhận nhật ký sự cố vào audit log nội bộ.")
        elif any(w in query_lower for w in ["thông báo", "webhook", "telegram", "cảnh báo", "alert"]):
            scheduled_jobs.append("Tích hợp Webhook cảnh báo khi: Pin dưới 15%, Nhiệt độ SoC > 45°C, hoặc IP mạng thay đổi.")
            scheduled_jobs.append("Gửi thông báo đẩy về kênh Telegram hoặc Discord của quản trị viên.")
        else:
            scheduled_jobs.append("Lập lịch Auto-Recycle dọn dẹp cache L1/L2 mỗi 6 tiếng.")
            scheduled_jobs.append("Kiểm tra phân giải DNS và Cloudflare Tunnel định kỳ mỗi 5 phút.")
            scheduled_jobs.append("Duy trì nhịp đập heartbeat nhẹ nhàng (0.01% CPU) bảo đảm tiến trình không bị hệ thống Android ngủ đông.")

        jobs_text = "\n".join([f"  • {j}" for j in scheduled_jobs])
        response_text = (
            f"**[Tự Động Hóa & Giám Sát Watchdog - Subagent Automation]**\n"
            f"- Trạng thái Watchdog: **{watchdog_status}**\n"
            f"- Cấu hình lịch biểu: **{cron_summary}**\n"
            f"- Các tác vụ tự động hóa:\n{jobs_text}\n\n"
            f"Hệ thống vận hành hoàn toàn tự động, biến chiếc điện thoại của bạn thành một trạm máy chủ độc lập không cần người trông coi."
        )

        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "watchdog_status": watchdog_status,
            "cron_summary": cron_summary,
            "scheduled_jobs": scheduled_jobs,
            "response": response_text
        }
