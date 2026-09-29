"""Subagent 2: Application Packaging & Compilation Diagnostics Agent
Chẩn đoán kỹ thuật đóng gói, sửa lỗi build pipeline và tối ưu gói cài đặt ứng dụng cho supportflast.dev.io.vn.
"""
from typing import Dict, Any
from core.llm_engine import llm_engine

class TechAgent:
    def __init__(self):
        self.agent_id = "agent_tech"
        self.name = "App Packaging & Build Diagnostics"
        self.color = "#00F0FF"
        self.role_description = "Hỗ trợ kỹ thuật đóng gói (MSIX/EXE/APK/DMG), tối ưu build pipeline và gỡ lỗi installer"

    def process(self, query: str, context: Dict[str, Any] = None) -> Dict[str, Any]:
        if llm_engine.is_enabled:
            system_instruction = (
                "Bạn là Tech Agent, chuyên gia hỗ trợ kỹ thuật đóng gói (Windows MSIX/EXE, Android APK/AAB, macOS DMG, Linux AppImage) và sửa lỗi build pipeline. "
                "Trả về một JSON object có format: "
                '{"detected_issues": ["Vấn đề 1"], "suggestions": ["Giải pháp 1"], "response": "Câu trả lời chào mừng/tổng hợp định dạng Markdown"}'
            )
            
            prompt = f"Yêu cầu của người dùng:\n{query}"
            if context:
                prompt += f"\n\nContext:\n{context}"
                
            llm_res = llm_engine.generate_json(prompt, system_instruction=system_instruction)
            if llm_res:
                detected_issues = llm_res.get("detected_issues", [])
                suggestions = llm_res.get("suggestions", [])
                
                res_body = (
                    f"**[Chẩn Đoán Kỹ Thuật Đóng Gói Ứng Dụng]**\n"
                    f"- Đơn vị tiếp nhận: Subagent Tech ({self.name})\n"
                    f"- Vấn đề nhận diện:\n"
                    + "\n".join([f"  • {issue}" for issue in detected_issues]) + "\n\n"
                    f"- Hướng dẫn kỹ thuật chuẩn mực:\n"
                    + "\n".join([f"  {idx+1}. {sug}" for idx, sug in enumerate(suggestions)]) + "\n\n"
                    f"{llm_res.get('response', '')}\n\n"
                    f"Hệ thống `supportflast.dev.io.vn` hỗ trợ CI/CD pipeline tự động build và ký số cho ứng dụng của bạn."
                )

                return {
                    "agent_id": self.agent_id,
                    "agent_name": self.name,
                    "status": "success",
                    "detected_issues": detected_issues,
                    "suggestions": suggestions,
                    "response": res_body
                }

        # Fallback rule-based nếu không có LLM
        query_lower = query.lower()
        detected_issues = []
        suggestions = []

        # 1. Nhóm nghiệp vụ Đóng gói Windows (MSIX, EXE, NSIS, Inno Setup)
        if any(k in query_lower for k in ["msix", "inno setup", "nsis", "installer", "setup.exe", "đóng gói windows"]):
            detected_issues.append("Yêu cầu đóng gói trình cài đặt Windows (MSIX / Inno Setup / NSIS).")
            suggestions.append("Sử dụng Inno Setup hoặc MSIX Packaging Tool: Nhúng đầy đủ manifest và cấu hình auto-update.")
            suggestions.append("Bổ sung script kiểm tra phụ thuộc: Tự động tải Visual C++ Redistributable và .NET Desktop Runtime nếu máy đích chưa cài đặt.")

        # 2. Nhóm nghiệp vụ Android (APK, AAB, Bundle)
        if any(k in query_lower for k in ["apk", "aab", "android bundle", "gradle build", "đóng gói android"]):
            detected_issues.append("Quy trình xuất bản ứng dụng di động Android (APK / Android App Bundle - AAB).")
            suggestions.append("Xuất bản bằng định dạng `.aab` có bật ProGuard/R8 để tối ưu dung lượng mã nguồn và bảo vệ byte-code.")
            suggestions.append("Cấu hình Play Feature Delivery / Asset Delivery nếu ứng dụng có asset vượt quá 150MB.")

        # 3. Nhóm nghiệp vụ macOS & Linux (DMG, PKG, AppImage, DEB)
        if any(k in query_lower for k in ["dmg", "pkg", "macos", "appimage", "deb", "linux"]):
            detected_issues.append("Yêu cầu đóng gói cho hệ điều hành macOS (DMG/PKG) hoặc Linux (AppImage/DEB).")
            suggestions.append("macOS: Bắt buộc chạy lệnh `codesign` với Developer ID và gửi Apple Notarization Service (`xcrun notarytool`) để Gatekeeper cho phép mở app.")
            suggestions.append("Linux: Ưu tiên định dạng AppImage để đóng gói độc lập, chạy trên mọi bản phân phối mà không gặp xung đột thư viện `glibc`.")

        # 4. Nhóm lỗi lập trình & Runtime phổ biến
        if any(k in query_lower for k in ["nullpointer", "nullreference", "none"]):
            detected_issues.append("Lỗi tham chiếu con trỏ rỗng (Null Pointer Dereference).")
            suggestions.append("Thêm guard clause kiểm tra `null` hoặc sử dụng optional chaining trước khi truy xuất dữ liệu.")

        if any(k in query_lower for k in ["oom", "out of memory", "dung lượng lớn", "quá nặng"]):
            detected_issues.append("Dung lượng file gói vượt tiêu chuẩn hoặc rò rỉ bộ nhớ runtime.")
            suggestions.append("Tách tài nguyên lớn (video, âm thanh) lên CDN tải động, sử dụng nén UPX cho binary và loại bỏ debug symbols (`strip --strip-unneeded`).")

        if not detected_issues:
            detected_issues.append("Yêu cầu tư vấn đóng gói và kiểm thử ứng dụng.")
            suggestions.append("Vui lòng cung cấp nền tảng đích (Windows/Android/macOS/Linux) và framework (WPF/Go/Flutter/Electron) để nhận kịch bản build chính xác.")

        res_body = (
            f"**[Chẩn Đoán Kỹ Thuật Đóng Gói Ứng Dụng (Fallback)]**\n"
            f"- Đơn vị tiếp nhận: Subagent Tech ({self.name})\n"
            f"- Vấn đề nhận diện:\n"
            + "\n".join([f"  • {issue}" for issue in detected_issues]) + "\n\n"
            f"- Hướng dẫn kỹ thuật chuẩn mực:\n"
            + "\n".join([f"  {idx+1}. {sug}" for idx, sug in enumerate(suggestions)]) + "\n\n"
            f"Hệ thống `supportflast.dev.io.vn` hỗ trợ CI/CD pipeline tự động build và ký số cho ứng dụng của bạn."
        )

        return {
            "agent_id": self.agent_id,
            "agent_name": self.name,
            "status": "success",
            "detected_issues": detected_issues,
            "suggestions": suggestions,
            "response": res_body
        }
