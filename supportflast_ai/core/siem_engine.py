"""SIEM AI Threat Intelligence & Real-time Event Monitor (supportflast_ai)
Tuân thủ Rule Phần 5 (SIEM AI) và Rule 7.1 (Tối ưu hiệu năng, chống tràn bộ nhớ).
"""
import re
import time
import logging
from collections import deque
from functools import lru_cache
from typing import Dict, Any, List, Optional, Generator, Tuple

# Logger chuẩn với tiền tố [SIEM] theo Rule 4.4
logger = logging.getLogger("supportflast_siem")
if not logger.handlers:
    handler = logging.StreamHandler()
    handler.setFormatter(logging.Formatter("[SIEM] %(asctime)s - %(levelname)s - %(message)s"))
    logger.addHandler(handler)
logger.setLevel(logging.INFO)

# Cấu hình ngưỡng Threat Score và Giới hạn bộ nhớ theo Rule 7.1
MAX_EVENT_BUFFER_SIZE = 2000    # Giới hạn số lượng sự kiện trong RAM (Ring Buffer)
MAX_ALERT_BUFFER_SIZE = 1000    # Giới hạn số lượng cảnh báo trong RAM
MAX_TRACKED_IPS = 5000          # Giới hạn số IP được theo dõi lịch sử
IP_HISTORY_TTL_SECONDS = 900    # 15 phút theo dõi cửa sổ trượt (Sliding Window)

# Heuristic Regex Signatures cho các dạng tấn công
SQLI_PATTERNS = [
    re.compile(r"(?i)\b(union\s+select|select\s+.*\s+from|insert\s+into|drop\s+table|delete\s+from|update\s+.*\s+set)\b"),
    re.compile(r"(?i)(--|\/\*|\*\/|;\s*--|;\s*drop|'\s*or\s+'?1'?\s*=\s*'?1)"),
    re.compile(r"(?i)\b(exec(\s|\+)+(s|x)p\w+|benchmark\s*\(|sleep\s*\(|waitfor\s+delay)\b"),
    re.compile(r"(?i)('\s*or\s+1=1\b|'\s*having\s+1=1\b|'\s*group\s+by\b)"),
]

XSS_PATTERNS = [
    re.compile(r"(?i)<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>"),
    re.compile(r"(?i)(javascript\s*:|vbscript\s*:|data\s*:\s*text\/html)"),
    re.compile(r"(?i)(onerror\s*=|onload\s*=|onclick\s*=|onmouseover\s*=|eval\s*\(|alert\s*\()"),
    re.compile(r"(?i)<(iframe|object|embed|svg|applet|meta|link|base)\b[^>]*>"),
]

PATH_TRAVERSAL_PATTERNS = [
    re.compile(r"(\.\./|\.\.\\|%2e%2e%2f|%2e%2e\/|\.\.%2f)"),
    re.compile(r"(?i)(etc/passwd|win\.ini|windows/system32|boot\.ini)"),
]

KNOWN_SCANNER_UA_PATTERNS = [
    re.compile(r"(?i)\b(sqlmap|nikto|nmap|masscan|dirbuster|gobuster|wpscan|acunetix|nessus|openvas|hydra|zgrab|nuclei)\b"),
    re.compile(r"(?i)\b(python-requests|aiohttp|curl|wget)\b"),
]

HONEYPOT_DECOYS = {
    "/.env",
    "/wp-admin",
    "/wp-admin/",
    "/wp-login.php",
    "/phpmyadmin",
    "/phpmyadmin/",
    "/api/admin/shell",
    "/.git/config",
    "/.git/",
    "/config.json",
    "/actuator/health",
    "/actuator/heapdump",
    "/actuator/",
    "/admin.php",
    "/shell",
    "/xmlrpc.php",
    "/.aws/credentials",
    "/.ssh/id_rsa",
}

@lru_cache(maxsize=512)
def _check_regex_signatures(text: str) -> Tuple[bool, bool, bool]:
    """Sử dụng lru_cache cho hàm pure để tối ưu CPU và tránh lặp tính toán (Rule 7.2).
    Trả về: (has_sqli, has_xss, has_path_traversal)
    """
    if not text:
        return False, False, False
    
    has_sqli = any(p.search(text) for p in SQLI_PATTERNS)
    has_xss = any(p.search(text) for p in XSS_PATTERNS)
    has_path = any(p.search(text) for p in PATH_TRAVERSAL_PATTERNS)
    return has_sqli, has_xss, has_path

class IPHistoryTracker:
    """Theo dõi lịch sử nguy cơ của IP trong cửa sổ trượt (Sliding Window 15 phút).
    Tự động dọn dẹp các IP quá hạn để chống tràn bộ nhớ (Rule 7.1).
    """
    def __init__(self, ttl_seconds: int = IP_HISTORY_TTL_SECONDS, max_entries: int = MAX_TRACKED_IPS):
        self.ttl_seconds = ttl_seconds
        self.max_entries = max_entries
        self._history: Dict[str, Dict[str, Any]] = {}
        self._last_cleanup = time.time()

    def record_event(self, ip: str, score: int, event_type: str):
        if not ip:
            return
        
        now = time.time()
        # Dọn dẹp định kỳ sau mỗi 60 giây nếu đạt ngưỡng kích thước
        if now - self._last_cleanup > 60 or len(self._history) >= self.max_entries:
            self.cleanup(now)

        entry = self._history.get(ip)
        if not entry or (now - entry["last_seen"] > self.ttl_seconds):
            self._history[ip] = {
                "first_seen": now,
                "last_seen": now,
                "event_count": 1,
                "max_score": score,
                "total_score": score,
                "event_types": {event_type: 1},
                "consecutive_failures": 1 if event_type in ("BRUTE_FORCE", "FAILED_LOGIN") else 0,
            }
        else:
            entry["last_seen"] = now
            entry["event_count"] += 1
            if score > entry["max_score"]:
                entry["max_score"] = score
            entry["total_score"] += score
            entry["event_types"][event_type] = entry["event_types"].get(event_type, 0) + 1
            if event_type in ("BRUTE_FORCE", "FAILED_LOGIN"):
                entry["consecutive_failures"] += 1
            else:
                entry["consecutive_failures"] = max(0, entry["consecutive_failures"] - 1)

    def get_ip_reputation(self, ip: str) -> Dict[str, Any]:
        if not ip or ip not in self._history:
            return {"reputation": "CLEAN", "risk_bonus": 0, "event_count": 0}
        
        entry = self._history[ip]
        count = entry["event_count"]
        max_s = entry["max_score"]
        failures = entry.get("consecutive_failures", 0)

        # Tính toán bonus điểm rủi ro dựa trên lịch sử
        risk_bonus = 0
        if failures >= 5:
            risk_bonus += 35
        elif failures >= 3:
            risk_bonus += 20
        elif failures >= 2:
            risk_bonus += 10

        if count > 20:
            risk_bonus += 20
        elif count > 10:
            risk_bonus += 10

        if max_s >= 80:
            risk_bonus += 20

        reputation = "CLEAN"
        if max_s >= 80 or risk_bonus >= 30:
            reputation = "MALICIOUS"
        elif max_s >= 40 or risk_bonus >= 15:
            reputation = "SUSPICIOUS"

        return {
            "reputation": reputation,
            "risk_bonus": min(40, risk_bonus),
            "event_count": count,
            "max_score": max_s,
            "consecutive_failures": failures
        }

    def cleanup(self, now: Optional[float] = None):
        """Xóa các bản ghi đã hết hạn trong cửa sổ trượt (Rule 7.1)."""
        current = now or time.time()
        expired_keys = [k for k, v in self._history.items() if (current - v["last_seen"]) > self.ttl_seconds]
        for k in expired_keys:
            del self._history[k]
        
        # Nếu vẫn còn quá nhiều bản ghi, xóa 20% bản ghi cũ nhất
        if len(self._history) >= self.max_entries:
            sorted_keys = sorted(self._history.keys(), key=lambda k: self._history[k]["last_seen"])
            to_remove = sorted_keys[: len(sorted_keys) // 5]
            for k in to_remove:
                del self._history[k]
        
        self._last_cleanup = current

class SIEMEngine:
    """Bộ Phân Tích Nhật Ký An Ninh Thông Minh (SIEM AI Threat Intelligence Engine).
    Tiếp nhận, chuẩn hóa, phân tích nguy cơ theo thời gian thực và quản lý cảnh báo.
    """
    def __init__(self):
        # Ring buffers cố định kích thước chống tràn bộ nhớ (Rule 7.1)
        self.events_buffer: deque = deque(maxlen=MAX_EVENT_BUFFER_SIZE)
        self.alerts_buffer: deque = deque(maxlen=MAX_ALERT_BUFFER_SIZE)
        self.ip_tracker = IPHistoryTracker()
        
        # Thống kê tổng quan
        self.stats = {
            "total_events_processed": 0,
            "total_alerts_generated": 0,
            "safe_events": 0,
            "warning_events": 0,
            "high_events": 0,
            "critical_events": 0,
            "honeypot_traps": 0,
            "sqli_detected": 0,
            "xss_detected": 0,
            "brute_force_detected": 0,
            "start_time": time.time()
        }
        logger.info("SIEM AI Engine initialized successfully with memory ring buffers")

    def _is_honeypot_path(self, path: str) -> bool:
        if not path:
            return False
        clean = path.strip().lower()
        if clean in HONEYPOT_DECOYS:
            return True
        for decoy in HONEYPOT_DECOYS:
            if clean.startswith(decoy):
                return True
        return False

    def analyze_event(self, event_data: Dict[str, Any]) -> Dict[str, Any]:
        """Thuật toán phân tích thông minh tính toán Threat Score (0 - 100).
        Thang điểm:
          + 0 - 20: Safe (Xanh) - Truy cập bình thường
          + 30 - 60: Warning (Vàng) - Đăng nhập sai nhiều lần / IP lạ / Scanner
          + 80 - 100: Critical (Đỏ) - Kích hoạt Honeypot / Payload SQLi / XSS / Khóa tài khoản
        """
        self.stats["total_events_processed"] += 1

        event_type = str(event_data.get("event_type", "IP_ACCESS")).upper()
        client_ip = str(event_data.get("client_ip", "")).strip()
        path = str(event_data.get("path", "")).strip()
        method = str(event_data.get("method", "GET")).upper()
        user_agent = str(event_data.get("user_agent", "")).strip()
        payload = str(event_data.get("payload", "")).strip()
        status_code = int(event_data.get("status_code", 200))
        details = event_data.get("details", {}) or {}

        # 1. Phân tích Heuristic trên các trường văn bản
        text_to_scan = f"{path} {payload} {str(details)}"
        has_sqli, has_xss, has_path_traversal = _check_regex_signatures(text_to_scan)

        # Kiểm tra scanner tool trong User-Agent
        is_known_scanner = any(p.search(user_agent) for p in KNOWN_SCANNER_UA_PATTERNS)
        is_honeypot = self._is_honeypot_path(path) or (event_type == "HONEYPOT_TRAP")

        # 2. Tính điểm cơ bản (Base Score) theo Rule
        base_score = 0
        threat_indicators: List[str] = []

        if is_honeypot:
            base_score = max(base_score, 90)
            threat_indicators.append("HONEYPOT_TRIGGERED")
            self.stats["honeypot_traps"] += 1

        if has_sqli:
            base_score = max(base_score, 95)
            threat_indicators.append("SQLI_PAYLOAD_DETECTED")
            self.stats["sqli_detected"] += 1

        if has_xss:
            base_score = max(base_score, 88)
            threat_indicators.append("XSS_PAYLOAD_DETECTED")
            self.stats["xss_detected"] += 1

        if has_path_traversal:
            base_score = max(base_score, 85)
            threat_indicators.append("PATH_TRAVERSAL_DETECTED")

        # Đánh giá theo Event Type
        if event_type == "HONEYPOT_TRAP":
            base_score = max(base_score, 92)
            if "HONEYPOT_TRIGGERED" not in threat_indicators:
                threat_indicators.append("HONEYPOT_TRIGGERED")

        elif event_type == "BRUTE_FORCE":
            failures = details.get("failures", details.get("attempts", 1))
            is_locked = details.get("is_locked", False)
            if is_locked or failures >= 5:
                base_score = max(base_score, 85)
                threat_indicators.append("BRUTE_FORCE_LOCKOUT")
            elif failures >= 3:
                base_score = max(base_score, 55)
                threat_indicators.append("MULTIPLE_LOGIN_FAILURES")
            else:
                base_score = max(base_score, 35)
                threat_indicators.append("FAILED_LOGIN_ATTEMPT")
            self.stats["brute_force_detected"] += 1

        elif event_type == "AUDIT_LOG":
            action = details.get("action", "").lower()
            if any(k in action for k in ["delete", "drop", "revoke", "lock", "ban", "update_config", "system_deploy"]):
                base_score = max(base_score, 25)
                threat_indicators.append("PRIVILEGED_ADMIN_ACTION")
            else:
                base_score = max(base_score, 5)
                threat_indicators.append("NORMAL_AUDIT_LOG")

        elif event_type == "IP_ACCESS":
            if status_code in (401, 403):
                base_score = max(base_score, 30)
                threat_indicators.append("UNAUTHORIZED_ACCESS_ATTEMPT")
            elif status_code == 404 and is_honeypot:
                base_score = max(base_score, 85)
            else:
                base_score = max(base_score, 10)
                threat_indicators.append("NORMAL_TRAFFIC")

        # Kiểm tra Scanner Tool
        if is_known_scanner:
            base_score = max(base_score, 50)
            threat_indicators.append("AUTOMATED_SCANNER_UA")

        # 3. Phân tích Threat Intelligence qua IP History Tracker
        ip_rep = self.ip_tracker.get_ip_reputation(client_ip)
        risk_bonus = ip_rep.get("risk_bonus", 0)

        # Tính tổng điểm Threat Score (giới hạn 0 - 100)
        final_score = min(100, max(0, base_score + risk_bonus))

        # 4. Phân cấp mức độ rủi ro (Risk Level) theo màu sắc quy định
        if final_score >= 80:
            risk_level = "CRITICAL"
            risk_color = "red"
            action_recommended = "JAIL_IP_24H"
            self.stats["critical_events"] += 1
        elif final_score >= 60:
            risk_level = "HIGH"
            risk_color = "orange"
            action_recommended = "RATE_LIMIT_CHALLENGE"
            self.stats["high_events"] += 1
        elif final_score >= 25:
            risk_level = "WARNING"
            risk_color = "yellow"
            action_recommended = "MONITOR_LOG"
            self.stats["warning_events"] += 1
        else:
            risk_level = "SAFE"
            risk_color = "green"
            action_recommended = "ALLOW"
            self.stats["safe_events"] += 1

        # Cập nhật lịch sử IP tracker
        self.ip_tracker.record_event(client_ip, final_score, event_type)

        # Tạo Event Record
        event_id = f"evt-{int(time.time() * 1000)}-{self.stats['total_events_processed']}"
        now_iso = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

        # Tạo tóm tắt phân tích dễ hiểu
        summary = self._generate_summary(event_type, client_ip, path, threat_indicators, final_score)

        analysis_result = {
            "event_id": event_id,
            "event_type": event_type,
            "client_ip": client_ip,
            "path": path,
            "method": method,
            "threat_score": final_score,
            "risk_level": risk_level,
            "risk_color": risk_color,
            "action_recommended": action_recommended,
            "threat_indicators": threat_indicators,
            "ip_reputation": ip_rep.get("reputation", "CLEAN"),
            "summary": summary,
            "timestamp": now_iso,
            "details": details
        }

        # Lưu trữ vào Ring Buffer (Rule 7.1)
        self.events_buffer.append(analysis_result)

        # Nếu điểm nguy cơ cao (>= 30) -> tạo Cảnh báo an ninh (Alert)
        if final_score >= 30:
            alert = {
                "alert_id": f"alt-{event_id}",
                "event_id": event_id,
                "client_ip": client_ip,
                "threat_score": final_score,
                "risk_level": risk_level,
                "risk_color": risk_color,
                "summary": summary,
                "action_recommended": action_recommended,
                "threat_indicators": threat_indicators,
                "timestamp": now_iso,
                "path": path,
                "method": method
            }
            self.alerts_buffer.append(alert)
            self.stats["total_alerts_generated"] += 1

            # Log chuẩn SIEM theo Rule 4.4: [SIEM]
            logger.warning(
                f"[ALERT] Level={risk_level} (Score={final_score}) | IP={client_ip} | "
                f"Action={action_recommended} | Indicators={threat_indicators} | Summary={summary}"
            )
        else:
            logger.info(f"[SAFE] Score={final_score} | IP={client_ip} | Path={path}")

        # Dọn dẹp biến tạm giải phóng RAM (Rule 7.1)
        del text_to_scan
        del threat_indicators

        return analysis_result

    def _generate_summary(self, event_type: str, ip: str, path: str, indicators: List[str], score: int) -> str:
        if "HONEYPOT_TRIGGERED" in indicators:
            return f"Phát hiện truy cập bẫy mật Honeypot '{path}' từ IP {ip} (Điểm nguy cơ: {score}/100)"
        if "SQLI_PAYLOAD_DETECTED" in indicators:
            return f"Phát hiện mẫu tiêm nhiễm cơ sở dữ liệu SQL Injection từ IP {ip} (Điểm nguy cơ: {score}/100)"
        if "XSS_PAYLOAD_DETECTED" in indicators:
            return f"Phát hiện mẫu mã độc kịch bản chéo XSS từ IP {ip} (Điểm nguy cơ: {score}/100)"
        if "BRUTE_FORCE_LOCKOUT" in indicators:
            return f"Tài khoản hoặc IP {ip} bị khóa do liên tục đăng nhập sai vượt ngưỡng Brute-force (Điểm nguy cơ: {score}/100)"
        if "MULTIPLE_LOGIN_FAILURES" in indicators:
            return f"Cảnh báo đăng nhập thất bại lặp lại nhiều lần từ IP {ip} (Điểm nguy cơ: {score}/100)"
        if "AUTOMATED_SCANNER_UA" in indicators:
            return f"Phát hiện công cụ quét tự động (Automated Scanner) từ IP {ip}"
        if score >= 30:
            return f"Hoạt động an ninh đáng ngờ từ IP {ip} trên đường dẫn '{path}' (Điểm: {score}/100)"
        return f"Yêu cầu truy cập an toàn hợp lệ từ IP {ip}"

    def get_alerts_stream(self, min_score: int = 0, risk_level: Optional[str] = None, 
                          ip: Optional[str] = None, limit: int = 50) -> Generator[Dict[str, Any], None, None]:
        """Sử dụng generator/yield thay vì nạp toàn bộ list lớn vào bộ nhớ (Rule 7.1)."""
        count = 0
        lvl_filter = risk_level.upper() if risk_level else None
        
        # Duyệt ngược từ mới nhất
        for alert in reversed(self.alerts_buffer):
            if count >= limit:
                break
            if alert["threat_score"] < min_score:
                continue
            if lvl_filter and alert["risk_level"] != lvl_filter:
                continue
            if ip and alert["client_ip"] != ip:
                continue
            yield alert
            count += 1

    def get_alerts(self, min_score: int = 0, risk_level: Optional[str] = None, 
                   ip: Optional[str] = None, limit: int = 50) -> Dict[str, Any]:
        """Truy vấn danh sách cảnh báo có giới hạn số lượng và phân trang an toàn."""
        alerts = list(self.get_alerts_stream(min_score=min_score, risk_level=risk_level, ip=ip, limit=limit))
        return {
            "status": "success",
            "total_alerts_stored": len(self.alerts_buffer),
            "returned_count": len(alerts),
            "alerts": alerts,
            "filter": {
                "min_score": min_score,
                "risk_level": risk_level,
                "ip": ip,
                "limit": limit
            }
        }

    def get_system_stats(self) -> Dict[str, Any]:
        """Lấy số liệu phân tích và kiểm tra tài nguyên bộ nhớ theo Rule 7.1/7.4."""
        rss_mb = 0.0
        try:
            mem = psutil.Process().memory_info()
            rss_mb = round(mem.rss / (1024 * 1024), 2)
        except Exception:
            pass

        return {
            "status": "active",
            "service": "supportflast_siem_ai",
            "uptime_seconds": int(time.time() - self.stats["start_time"]),
            "memory_rss_mb": rss_mb,
            "ring_buffers": {
                "events_stored": len(self.events_buffer),
                "events_max": MAX_EVENT_BUFFER_SIZE,
                "alerts_stored": len(self.alerts_buffer),
                "alerts_max": MAX_ALERT_BUFFER_SIZE,
                "tracked_ips_count": len(self.ip_tracker._history),
                "tracked_ips_max": MAX_TRACKED_IPS
            },
            "metrics": {
                "total_events": self.stats["total_events_processed"],
                "total_alerts": self.stats["total_alerts_generated"],
                "safe": self.stats["safe_events"],
                "warning": self.stats["warning_events"],
                "high": self.stats["high_events"],
                "critical": self.stats["critical_events"],
                "honeypot_triggers": self.stats["honeypot_traps"],
                "sqli_detected": self.stats["sqli_detected"],
                "xss_detected": self.stats["xss_detected"],
                "brute_force_detected": self.stats["brute_force_detected"]
            }
        }
