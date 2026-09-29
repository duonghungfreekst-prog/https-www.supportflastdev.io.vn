"""Subagent 5: Developer Licensing, Monetization & Billing Concierge Agent
Xử lý thông tin bản quyền phần mềm, License Key, gói tài khoản Nhà Phát Triển và chính sách doanh thu ứng dụng cho supportflast.dev.io.vn.
"""
from typing import Dict, Any
from core.llm_engine import llm_engine

class BillingAgent:
    def __init__(self):
        self.agent_id = "agent_billing"
        self.name = "Developer Licensing & Monetization Concierge"
        self.color = "#BF00FF"
        self.role_description = "Hỗ trợ tài khoản Nhà phát triển, cấp phép License Key bản quyền, đối soát doanh thu và hóa đơn"

    def process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        # Gọi LLM Engine
        if llm_engine.is_enabled:
            system_instruction = (
                "Bạn là Billing Agent, xử lý tài khoản Developer, doanh thu, thanh toán (Visa, Master, Stripe), bản quyền (DRM). "
                "Trả về JSON: {\"category\": \"Subscription/DRM/Payment\", \"solution_steps\": [\"Bước 1\"], \"response\": \"Phản hồi\"}"
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
                    "category": llm_res.get("category", "Subscription/DRM/Payment"),
                    "solution_steps": llm_res.get("solution_steps", []),
                    "response": f"**[Quản Lý Bản Quyền Phần Mềm & Gói Nhà Phát Triển]**\n- Phân loại: **{llm_res.get('category')}**\n\n{llm_res.get('response', '')}"
                }

        # Fallback logic cũ
        publisher_plans = [
            {
                "tier": "Indie Developer (Miễn phí)",
                "sla": "< 15 phút",
                "quota": "Đăng tải 5 ứng dụng, CDN cộng đồng 100GB/tháng",
                "drm": "Xác thực bản quyền tiêu chuẩn",
                "price": "0 VNĐ (Hỗ trợ cộng đồng Dev)"
            },
            {
                "tier": "Pro Studio Publisher",
                "sla": "< 5 phút",
                "quota": "Không giới hạn số lượng app, CDN Anycast 5TB/tháng",
                "drm": "Hệ thống License Key online/offline + Khóa phần cứng HWID",
                "price": "690.000 VNĐ / Tháng (Chia sẻ doanh thu 88/12)"
            },
            {
                "tier": "Enterprise Publisher",
                "sla": "Ưu tiên Tức thì (< 60s)",
                "quota": "Băng thông CDN riêng không giới hạn, Private Store nội bộ",
                "drm": "Tùy biến DRM cao cấp, hỗ trợ ký số EV Code Signing",
                "price": "Liên hệ theo quy mô doanh nghiệp (Xuất hóa đơn VAT)"
            }
        ]

        res_body = (
            f"**[Quản Lý Bản Quyền Phần Mềm & Gói Nhà Phát Triển - supportflast.dev.io.vn]**\n"
            f"- Đơn vị tiếp nhận: Subagent Billing ({self.name})\n"
            f"- Chính sách phân phối: Bảo vệ quyền sở hữu trí tuệ 100% qua cơ chế mã hóa Rust Core\n"
            f"- Các gói tài khoản Nhà Phát Triển hiện hành:\n"
            + "\n".join([f"  • **{p['tier']}** ({p['price']}):\n    - Hạn ngạch: {p['quota']}\n    - Giải pháp DRM: {p['drm']}" for p in publisher_plans]) + "\n\n"
            f"Chi tiết giải đáp: '{query}'.\n"
            f"Nếu bạn cần tích hợp API kích hoạt License Key tự động vào ứng dụng hoặc xuất hóa đơn VAT điện tử, hãy gửi yêu cầu kèm mã Nhà phát hành (Publisher ID)."
        )

        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "available_plans": publisher_plans,
            "response": res_body
        }
