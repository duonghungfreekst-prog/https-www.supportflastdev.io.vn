"""API Integration test cho SIEM AI Threat Intelligence endpoints trên FastAPI (supportflast_ai/app.py)
"""
import unittest
from starlette.testclient import TestClient
from app import app, siem_engine

class TestSIEMAPI(unittest.TestCase):
    def setUp(self):
        self.client = TestClient(app)
        # Reset ring buffer giữa các lần test
        siem_engine.events_buffer.clear()
        siem_engine.alerts_buffer.clear()

    def test_post_siem_analyze_normal(self):
        """Kiểm tra API /siem/analyze cho truy cập thông thường (Safe - Xanh: 0 - 20)"""
        res = self.client.post("/siem/analyze", json={
            "event_type": "IP_ACCESS",
            "client_ip": "10.0.0.15",
            "path": "/api/apps",
            "method": "GET",
            "status_code": 200
        })
        self.assertEqual(res.status_code, 200)
        data = res.json()
        self.assertLessEqual(data["threat_score"], 20)
        self.assertEqual(data["risk_level"], "SAFE")
        self.assertEqual(data["risk_color"], "green")
        self.assertEqual(data["action_recommended"], "ALLOW")

    def test_post_siem_analyze_failed_logins(self):
        """Kiểm tra API /siem/analyze cho đăng nhập sai lặp lại (Warning - Vàng: 30 - 60)"""
        res = self.client.post("/siem/analyze", json={
            "event_type": "BRUTE_FORCE",
            "client_ip": "198.51.100.5",
            "path": "/api/auth/login",
            "method": "POST",
            "details": {
                "failures": 3,
                "is_locked": False,
                "username": "admin"
            }
        })
        self.assertEqual(res.status_code, 200)
        data = res.json()
        self.assertGreaterEqual(data["threat_score"], 30)
        self.assertLessEqual(data["threat_score"], 60)
        self.assertIn(data["risk_level"], ["WARNING", "HIGH"])
        self.assertIn(data["risk_color"], ["yellow", "orange"])

    def test_post_siem_analyze_honeypot_trap(self):
        """Kiểm tra API /siem/analyze khi kích hoạt Honeypot (Critical - Đỏ: 80 - 100)"""
        res = self.client.post("/siem/analyze", json={
            "event_type": "HONEYPOT_TRAP",
            "client_ip": "203.0.113.88",
            "path": "/.env",
            "method": "GET",
            "user_agent": "curl/7.81.0"
        })
        self.assertEqual(res.status_code, 200)
        data = res.json()
        self.assertGreaterEqual(data["threat_score"], 80)
        self.assertLessEqual(data["threat_score"], 100)
        self.assertEqual(data["risk_level"], "CRITICAL")
        self.assertEqual(data["risk_color"], "red")
        self.assertIn("HONEYPOT_TRIGGERED", data["threat_indicators"])

    def test_post_siem_analyze_sqli_payload(self):
        """Kiểm tra API /siem/analyze khi phát hiện SQL Injection payload (Critical - Đỏ: 80 - 100)"""
        res = self.client.post("/siem/analyze", json={
            "event_type": "PAYLOAD_ATTACK",
            "client_ip": "203.0.113.99",
            "path": "/api/agents/chat",
            "method": "POST",
            "payload": "' OR 1=1; DROP TABLE users; --"
        })
        self.assertEqual(res.status_code, 200)
        data = res.json()
        self.assertGreaterEqual(data["threat_score"], 80)
        self.assertLessEqual(data["threat_score"], 100)
        self.assertEqual(data["risk_level"], "CRITICAL")
        self.assertEqual(data["risk_color"], "red")
        self.assertIn("SQLI_PAYLOAD_DETECTED", data["threat_indicators"])

    def test_get_siem_alerts_and_stats(self):
        """Kiểm tra API /siem/alerts và /siem/stats"""
        # Tạo 1 alert Honeypot
        self.client.post("/siem/analyze", json={
            "event_type": "HONEYPOT_TRAP",
            "client_ip": "1.2.3.4",
            "path": "/wp-login.php"
        })

        # Truy vấn alerts
        alerts_res = self.client.get("/siem/alerts?min_score=80")
        self.assertEqual(alerts_res.status_code, 200)
        alerts_data = alerts_res.json()
        self.assertEqual(alerts_data["status"], "success")
        self.assertGreaterEqual(len(alerts_data["alerts"]), 1)
        self.assertEqual(alerts_data["alerts"][0]["client_ip"], "1.2.3.4")

        # Truy vấn stats
        stats_res = self.client.get("/siem/stats")
        self.assertEqual(stats_res.status_code, 200)
        stats_data = stats_res.json()
        self.assertEqual(stats_data["service"], "supportflast_siem_ai")
        self.assertIn("ring_buffers", stats_data)
        self.assertIn("metrics", stats_data)
        self.assertGreaterEqual(stats_data["metrics"]["honeypot_triggers"], 1)

    def test_security_headers_present(self):
        """Kiểm tra Security Headers theo Rule 3.4"""
        res = self.client.get("/siem/stats")
        self.assertEqual(res.headers.get("X-Content-Type-Options"), "nosniff")
        self.assertEqual(res.headers.get("X-Frame-Options"), "DENY")
        self.assertIn("max-age=31536000", res.headers.get("Strict-Transport-Security", ""))

if __name__ == "__main__":
    unittest.main()
