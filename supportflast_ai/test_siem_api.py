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

    def test_is_insecure_internal_secret(self):
        """Kiểm tra hàm phát hiện internal secret yếu hoặc placeholder"""
        from app import is_insecure_internal_secret
        self.assertTrue(is_insecure_internal_secret(""))
        self.assertTrue(is_insecure_internal_secret("CHANGE_ME_INTERNAL_SERVICE_SECRET"))
        self.assertTrue(is_insecure_internal_secret("sf_mobile_token_2026"))
        self.assertTrue(is_insecure_internal_secret("sf_mobile_engine_token_2026"))
        self.assertTrue(is_insecure_internal_secret("default_secret"))
        self.assertTrue(is_insecure_internal_secret("secret"))
        self.assertTrue(is_insecure_internal_secret("123456"))
        self.assertTrue(is_insecure_internal_secret("<GENERATE_SECURE_INTERNAL_SERVICE_SECRET>"))
        self.assertTrue(is_insecure_internal_secret("short"))
        # Secret mạnh tối thiểu 16 ký tự
        self.assertFalse(is_insecure_internal_secret("Super_Strong_Random_Internal_Secret_2026_Prod"))

    def test_verify_internal_auth_production_enforcement(self):
        """Kiểm tra cơ chế xác thực nội bộ trong môi trường Production"""
        import os
        from app import verify_internal_auth
        from unittest.mock import Mock

        # Giả lập request từ IP mạng (không phải testclient)
        req = Mock()
        req.client.host = "192.168.1.100"
        req.headers = {}

        orig_env = os.environ.get("ENV")
        orig_secret = os.environ.get("INTERNAL_SERVICE_SECRET")
        try:
            os.environ["ENV"] = "production"

            # 1. Secret rỗng trong production -> Từ chối
            os.environ["INTERNAL_SERVICE_SECRET"] = ""
            self.assertFalse(verify_internal_auth(req))

            # 2. Secret yếu trong production -> Từ chối
            os.environ["INTERNAL_SERVICE_SECRET"] = "sf_mobile_token_2026"
            self.assertFalse(verify_internal_auth(req))

            # 3. Secret mạnh, nhưng request không có header -> Từ chối
            valid_secret = "Very_Secure_Production_Internal_Token_2026!"
            os.environ["INTERNAL_SERVICE_SECRET"] = valid_secret
            self.assertFalse(verify_internal_auth(req))

            # 4. Token sai -> Từ chối
            req.headers = {"X-Internal-Token": "wrong_token_12345678"}
            self.assertFalse(verify_internal_auth(req))

            # 5. Token đúng qua X-Internal-Token -> Cho phép
            req.headers = {"X-Internal-Token": valid_secret}
            self.assertTrue(verify_internal_auth(req))

            # 6. Token đúng qua Authorization: Bearer -> Cho phép
            req.headers = {"Authorization": f"Bearer {valid_secret}"}
            self.assertTrue(verify_internal_auth(req))
        finally:
            if orig_env is not None:
                os.environ["ENV"] = orig_env
            else:
                os.environ.pop("ENV", None)
            if orig_secret is not None:
                os.environ["INTERNAL_SERVICE_SECRET"] = orig_secret
            else:
                os.environ.pop("INTERNAL_SERVICE_SECRET", None)

if __name__ == "__main__":
    unittest.main()
