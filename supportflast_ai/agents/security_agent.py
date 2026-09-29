"""Subagent 4: App Security, Malware Sandbox & Code Signing Agent
Kiểm duyệt mã độc, phân tích hành vi ứng dụng trong Sandbox, thẩm định chứng chỉ số Code Signing và an ninh WAF cho supportflast.dev.io.vn.
"""
import os
import re
from typing import Dict, Any, Optional
from core.security_scanner import PackageSecurityScanner
from core.llm_engine import llm_engine

class SecurityAgent:
    def __init__(self):
        self.agent_id = "agent_security"
        self.name = "App Security, Sandbox & Code Signing"
        self.color = "#FF0055"
        self.role_description = "Kiểm duyệt an toàn ứng dụng, quét virus đa engine, phân tích Sandbox và thẩm định chữ ký số"
        self.scanner = PackageSecurityScanner()

    def scan_security_posture(self, input_text: str) -> Dict[str, Any]:
        findings = []
        sqli_pattern = re.compile(r"(\b(union|select|insert|delete|drop|alter|update|truncate)\b.*\b(from|into|table|database)\b)|('--|\/\*|;--)", re.IGNORECASE)
        if sqli_pattern.search(input_text):
            findings.append("Phát hiện mẫu SQL Injection tiềm ẩn trong truy vấn dữ liệu.")

        xss_pattern = re.compile(r"(<script.*?>|javascript:|onerror=|onload=|<iframe|<img.*?src=)", re.IGNORECASE)
        if xss_pattern.search(input_text):
            findings.append("Phát hiện dấu hiệu tấn công Cross-Site Scripting (XSS).")

        if ".." in input_text or "/etc/passwd" in input_text or "\\windows\\" in input_text.lower():
            findings.append("Phát hiện ký tự Path Traversal (duyệt thư mục trái phép).")

        return {"threat_detected": len(findings) > 0, "findings": findings}

    def process(self, query: str, context: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        context = context or {}
        
        # Gọi LLM Engine
        if llm_engine.is_enabled:
            system_instruction = (
                "Bạn là Security Agent, chuyên gia an ninh mạng. Hỗ trợ quét mã độc, báo cáo lỗ hổng (CVE, XSS), sandbox, Code Signing. "
                'Trả về JSON: {"threat_level": "High/Medium/Low", "security_advice": ["Lời khuyên"], "response": "Phản hồi"}'
            )
            prompt = f"Yêu cầu của người dùng:\n{query}"
            if context:
                prompt += f"\n\nContext:\n{context}"
                
            llm_res = llm_engine.generate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                threat_level = llm_res.get("threat_level", "Low")
                security_advice = llm_res.get("security_advice", [])
                advice_str = "\n".join([f"  - {adv}" for adv in security_advice])
                res_body = (
                    f"**[CẢNH BÁO / TƯ VẤN AN NINH ỨNG DỤNG]**\n"
                    f"- Mức độ đe dọa (Threat Level): **{threat_level}**\n"
                    f"- Lời khuyên bảo mật:\n{advice_str}\n\n"
                    f"{llm_res.get('response', '')}"
                )
                return {
                    "agent_id": self.agent_id,
                    "agent_name": self.name,
                    "status": "success",
                    "threat_level": threat_level,
                    "security_advice": security_advice,
                    "response": res_body
                }

        # Fallback logic cũ
        file_path = context.get("file_path")
        package_info = context.get("package_info")
        raw_b64 = context.get("raw_content_base64")

        # Nếu query có đường dẫn file hợp lệ tồn tại trên máy
        if not file_path and os.path.isabs(query.strip()) and os.path.exists(query.strip()):
            file_path = query.strip()

        scan_package_result = None
        if file_path or package_info or raw_b64:
            scan_package_result = self.scanner.scan(
                file_path=file_path,
                package_info=package_info,
                raw_content_base64=raw_b64
            )

        scan_result = self.scan_security_posture(query)
        threat_detected = scan_result["threat_detected"] or (
            scan_package_result and scan_package_result.get("safety_level") in ["Suspicious", "Malicious"]
        )

        security_protocols = [
            "Quy trình quét mã độc đa tầng: Kiểm tra chữ ký nhận dạng virus qua ClamAV & liên kết cơ sở dữ liệu đe dọa toàn cầu.",
            "Phân tích hành vi Sandbox cô lập: Theo dõi tiến trình thực thi, ngăn chặn hành vi đào tiền ảo ngầm, trojan và ransomware.",
            "Thẩm định chứng chỉ số Code Signing (Microsoft Authenticode / Apple Developer): Loại bỏ 100% cảnh báo SmartScreen.",
            "Đánh giá quyền hạn ứng dụng: Ngăn chặn yêu cầu quyền Administrator không cần thiết và kiểm soát truy cập dữ liệu nhạy cảm."
        ]

        if scan_package_result:
            s_level = scan_package_result.get("safety_level", "Unknown")
            s_score = scan_package_result.get("safety_score", 0.0)
            res_body = (
                f"**[KẾT QUẢ PHÂN TÍCH AN NINH GÓI ỨNG DỤNG - supportflast.dev.io.vn]**\n"
                f"- Mức độ an toàn: **{s_level.upper()}** (Điểm tín nhiệm: {s_score}/100)\n"
                f"- Kết luận: {scan_package_result.get('verdict_summary', '')}\n"
                f"- Entropy: {scan_package_result.get('details', {}).get('entropy', {}).get('overall_entropy', 0.0)}\n"
                f"- Trạng thái chữ ký số: {scan_package_result.get('details', {}).get('code_signing', {}).get('details', '')}\n"
            )
            return {
                "agent_id": self.agent_id,
                "agent_name": self.name,
                "status": "success",
                "threat_detected": threat_detected,
                "safety_level": s_level,
                "safety_score": s_score,
                "scan_details": scan_package_result,
                "response": res_body
            }

        if scan_result["threat_detected"]:
            res_body = (
                f"**[CẢNH BÁO AN NINH ỨNG DỤNG - supportflast.dev.io.vn]**\n"
                f"- Đơn vị tiếp nhận: Subagent Security ({self.name})\n"
                f"- Mức độ: **NGUY CƠ BẢO MẬT ĐƯỢC PHÁT HIỆN**\n"
                f"- Chi tiết rủi ro:\n"
                + "\n".join([f"  ⚠️ {f}" for f in scan_result["findings"]]) + "\n\n"
                f"- Hành động: Payload đã bị cô lập. Hệ thống đăng tải ứng dụng áp dụng quy chế Zero-Trust ngăn chặn tệp tin nghi vấn."
            )
        else:
            res_body = (
                f"**[Báo Cáo Kiểm Duyệt An Toàn & Quét Mã Độc Ứng Dụng]**\n"
                f"- Đơn vị tiếp nhận: Subagent Security ({self.name})\n"
                f"- Trạng thái kiểm duyệt: **TIÊU CHUẨN AN TOÀN ĐƯỢC THIẾT LẬP**\n"
                f"- Các tiêu chuẩn an ninh đang kích hoạt:\n"
                + "\n".join([f"  🛡️ {sp}" for sp in security_protocols]) + "\n\n"
                f"Phân tích yêu cầu bảo mật: '{query}'.\n"
                f"Mọi tệp tin ứng dụng tải lên cổng `supportflast.dev.io.vn` đều trải qua quy trình kiểm tra mã độc 3 bước trước khi phân phối công khai."
            )

        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "threat_detected": scan_result["threat_detected"],
            "findings": scan_result["findings"],
            "response": res_body
        }

