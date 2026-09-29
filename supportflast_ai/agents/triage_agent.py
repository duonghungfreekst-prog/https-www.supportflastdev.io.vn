"""Subagent 1: App Submission Triage & Workflow Agent
Tiếp nhận hồ sơ ứng dụng, phân loại SLA kiểm duyệt và định tuyến quy trình nộp app cho supportflast.dev.io.vn.
"""
from typing import Dict, Any
from core.llm_engine import llm_engine

class TriageAgent:
    def __init__(self):
        self.agent_id = "agent_triage"
        self.name = "App Submission Triage & Workflow"
        self.color = "#FF9900"
        self.role_description = "Tiếp nhận hồ sơ ứng dụng, phân loại SLA kiểm duyệt và định tuyến quy trình nộp app"

    def process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        # Gọi LLM Engine
        if llm_engine.is_enabled:
            system_instruction = (
                "Bạn là Triage Agent của hệ thống supportflast.dev.io.vn.\n"
                "Nhiệm vụ: Phân loại yêu cầu hỗ trợ của người dùng, xác định mức độ ưu tiên (severity), SLA, "
                "và agent chuyên trách phù hợp (agent_tech, agent_infra, agent_security, agent_billing).\n"
                "Trả về JSON object duy nhất (không bọc trong markdown block) với cấu trúc:\n"
                '{"severity": "P1/P2/P3 - Mô tả", "sla": "Thời gian SLA", "recommended_agent": "ID của agent phù hợp", '
                '"response": "Câu trả lời chào mừng và xác nhận đã phân loại yêu cầu"}'
            )
            
            prompt = f"Yêu cầu của người dùng:\n{query}"
            if context:
                prompt += f"\n\nContext:\n{context}"
                
            llm_res = llm_engine.generate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                return {
                    "agent_id": self.agent_id,
                    "agent_name": self.name,
                    "status": "success",
                    "severity": llm_res.get("severity", "P3 - Standard"),
                    "sla": llm_res.get("sla", "15 phút"),
                    "recommended_agent": llm_res.get("recommended_agent", "agent_tech"),
                    "response": f"**[Cổng Điều Phối & Phân Loại Đăng Tải Ứng Dụng]**\n- Nền tảng: `supportflast.dev.io.vn`\n- Mức độ ưu tiên phân loại: **{llm_res.get('severity')}**\n- Cam kết SLA: **{llm_res.get('sla')}**\n- Subagent đề xuất: **{llm_res.get('recommended_agent')}**\n\n{llm_res.get('response', '')}"
                }

        # Fallback rule-based nếu không có LLM
        query_lower = query.lower()
        severity = "P3 - Standard App Review"
        sla = "15 phút"
        recommended_agent = "agent_tech"

        urgent_outage = ["sập", "down", "outage", "khẩn cấp", "crash", "tắc nghẽn", "die", "lỗi nghiêm trọng"]
        security_keywords = ["virus", "mã độc", "malware", "trojan", "sandbox", "chữ ký số", "code signing", "smartscreen", "hack", "lỗ hổng", "bảo mật", "cve", "sqli", "xss"]
        infra_keywords = ["cdn", "mirror", "tải chậm", "link tải", "download", "checksum", "sha256", "băng thông", "resume", "dns", "ssl", "server"]
        tech_packaging = ["đóng gói", "package", "build", "msix", "exe", "apk", "aab", "dmg", "appimage", "installer", "setup", "manifest", "dependency", "nullpointer", "syntax"]
        billing_license = ["bản quyền", "license", "drm", "kích hoạt", "nhà phát triển", "developer", "publisher", "doanh thu", "thanh toán", "hóa đơn", "gói", "nạp tiền"]
        mobile_keywords = ["pin", "battery", "nhiệt độ", "nóng máy", "chai pin", "wake-lock", "doze", "điện thoại", "termux", "android"]
        network_keywords = ["wi-fi", "wifi", "4g", "5g", "cgnat", "tunnel", "cloudflare", "ip lan", "port forward"]
        storage_keywords = ["bộ nhớ", "dung lượng", "sqlite", "vacuum", "wal", "dọn dẹp", "flash", "thẻ nhớ", "cache rác"]
        analytics_keywords = ["benchmark", "hiệu năng", "qps", "latency", "tải", "chỉ số", "metrics", "arm64", "đo"]
        automation_keywords = ["cron", "lập lịch", "tự động", "backup", "watchdog", "webhook", "telegram", "cảnh báo"]

        if any(w in query_lower for w in urgent_outage):
            severity = "P1 - Critical Outage / Platform Halt"
            sla = "Tức thì (< 3 phút)"
            recommended_agent = "agent_infra" if any(w in query_lower for w in infra_keywords) else "agent_tech"
        elif any(w in query_lower for w in security_keywords):
            severity = "P1 - Security & Malware Alert"
            sla = "< 5 phút"
            recommended_agent = "agent_security"
        elif any(w in query_lower for w in mobile_keywords):
            severity = "P2 - Mobile Hardware & Thermal Alert"
            sla = "< 5 phút"
            recommended_agent = "agent_mobile"
        elif any(w in query_lower for w in network_keywords):
            severity = "P2 - Mobile Connectivity & Tunnel"
            sla = "< 5 phút"
            recommended_agent = "agent_network"
        elif any(w in query_lower for w in storage_keywords):
            severity = "P2 - Flash Storage & SQLite Optimization"
            sla = "< 10 phút"
            recommended_agent = "agent_storage"
        elif any(w in query_lower for w in analytics_keywords):
            severity = "P3 - ARM Performance & Metrics"
            sla = "< 15 phút"
            recommended_agent = "agent_analytics"
        elif any(w in query_lower for w in automation_keywords):
            severity = "P3 - Task Automation & Watchdog"
            sla = "< 15 phút"
            recommended_agent = "agent_automation"
        elif any(w in query_lower for w in tech_packaging):
            severity = "P2 - Packaging & Compilation Issue"
            sla = "< 10 phút"
            recommended_agent = "agent_tech"
        elif any(w in query_lower for w in infra_keywords):
            severity = "P2 - CDN & Distribution Bottleneck"
            sla = "< 10 phút"
            recommended_agent = "agent_infra"
        elif any(w in query_lower for w in billing_license):
            severity = "P3 - Developer Account & Licensing"
            sla = "< 15 phút"
            recommended_agent = "agent_billing"

        response_text = (
            f"**[Cổng Điều Phối & Phân Loại Đăng Tải Ứng Dụng (Fallback)]**\n"
            f"- Nền tảng: `supportflast.dev.io.vn`\n"
            f"- Mức độ ưu tiên phân loại: **{severity}**\n"
            f"- Cam kết SLA phản hồi quy trình: **{sla}**\n"
            f"- Subagent chuyên trách đề xuất: **{recommended_agent}**\n\n"
            f"Đã mở quy trình kiểm duyệt cho yêu cầu: '{query}'. "
            f"Yêu cầu đã được chuyển tới chuyên gia phụ trách để hỗ trợ phát hành ứng dụng an toàn và nhanh nhất."
        )

        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "severity": severity,
            "sla": sla,
            "recommended_agent": recommended_agent,
            "response": response_text
        }
