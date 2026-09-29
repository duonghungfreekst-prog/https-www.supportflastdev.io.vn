"""Unit test cho SIEM AI Threat Intelligence Engine (supportflast_ai/core/siem_engine.py)
"""
import unittest
from core.siem_engine import SIEMEngine

class TestSIEMEngine(unittest.TestCase):
    def setUp(self):
        self.engine = SIEMEngine()

    def test_normal_access_threat_score(self):
        """Truy cập bình thường: Threat Score 0 - 20 (Safe - Xanh)"""
        evt = {
            "event_type": "IP_ACCESS",
            "client_ip": "192.168.1.50",
            "path": "/api/apps",
            "method": "GET",
            "status_code": 200,
            "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"
        }
        res = self.engine.analyze_event(evt)
        self.assertLessEqual(res["threat_score"], 20)
        self.assertEqual(res["risk_level"], "SAFE")
        self.assertEqual(res["risk_color"], "green")
        self.assertEqual(res["action_recommended"], "ALLOW")

    def test_failed_login_warning_threat_score(self):
        """Đăng nhập sai nhiều lần / IP lạ: Threat Score 30 - 60 (Warning - Vàng)"""
        evt = {
            "event_type": "BRUTE_FORCE",
            "client_ip": "203.0.113.42",
            "path": "/api/auth/login",
            "method": "POST",
            "details": {
                "username": "developer1",
                "failures": 3,
                "is_locked": False
            }
        }
        res = self.engine.analyze_event(evt)
        self.assertGreaterEqual(res["threat_score"], 30)
        self.assertLessEqual(res["threat_score"], 60)
        self.assertIn(res["risk_level"], ["WARNING", "HIGH"])
        self.assertIn(res["risk_color"], ["yellow", "orange"])

    def test_honeypot_critical_threat_score(self):
        """Kích hoạt Honeypot: Threat Score 80 - 100 (Critical Attack - Đỏ)"""
        evt = {
            "event_type": "HONEYPOT_TRAP",
            "client_ip": "198.51.100.99",
            "path": "/.env",
            "method": "GET",
            "user_agent": "curl/7.68.0"
        }
        res = self.engine.analyze_event(evt)
        self.assertGreaterEqual(res["threat_score"], 80)
        self.assertLessEqual(res["threat_score"], 100)
        self.assertEqual(res["risk_level"], "CRITICAL")
        self.assertEqual(res["risk_color"], "red")
        self.assertIn("HONEYPOT_TRIGGERED", res["threat_indicators"])
        self.assertEqual(res["action_recommended"], "JAIL_IP_24H")

    def test_sqli_payload_critical_threat_score(self):
        """Payload SQLi: Threat Score 80 - 100 (Critical Attack - Đỏ)"""
        evt = {
            "event_type": "PAYLOAD_ATTACK",
            "client_ip": "198.51.100.88",
            "path": "/api/agents/chat",
            "method": "POST",
            "payload": "' UNION SELECT username, password_hash FROM users --"
        }
        res = self.engine.analyze_event(evt)
        self.assertGreaterEqual(res["threat_score"], 80)
        self.assertLessEqual(res["threat_score"], 100)
        self.assertEqual(res["risk_level"], "CRITICAL")
        self.assertEqual(res["risk_color"], "red")
        self.assertIn("SQLI_PAYLOAD_DETECTED", res["threat_indicators"])

    def test_xss_payload_critical_threat_score(self):
        """Payload XSS: Threat Score 80 - 100 (Critical Attack - Đỏ)"""
        evt = {
            "event_type": "PAYLOAD_ATTACK",
            "client_ip": "198.51.100.77",
            "path": "/api/reviews",
            "method": "POST",
            "payload": "<script>alert(document.cookie)</script>"
        }
        res = self.engine.analyze_event(evt)
        self.assertGreaterEqual(res["threat_score"], 80)
        self.assertLessEqual(res["threat_score"], 100)
        self.assertEqual(res["risk_level"], "CRITICAL")
        self.assertEqual(res["risk_color"], "red")
        self.assertIn("XSS_PAYLOAD_DETECTED", res["threat_indicators"])

    def test_alerts_query_and_filtering(self):
        """Kiểm tra truy vấn và lọc cảnh báo qua ring buffer"""
        # Tạo 3 sự kiện
        self.engine.analyze_event({"event_type": "IP_ACCESS", "client_ip": "1.1.1.1", "path": "/safe"})
        self.engine.analyze_event({"event_type": "HONEYPOT_TRAP", "client_ip": "2.2.2.2", "path": "/wp-admin"})
        self.engine.analyze_event({"event_type": "PAYLOAD_ATTACK", "client_ip": "3.3.3.3", "payload": "<script>eval()</script>"})

        alerts_res = self.engine.get_alerts(min_score=80)
        self.assertEqual(alerts_res["status"], "success")
        self.assertEqual(len(alerts_res["alerts"]), 2)

        stats = self.engine.get_system_stats()
        self.assertEqual(stats["metrics"]["total_events"], 3)
        self.assertEqual(stats["metrics"]["critical"], 2)

if __name__ == "__main__":
    unittest.main()
