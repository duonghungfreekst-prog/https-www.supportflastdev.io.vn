"""Unit test chuẩn thư viện tiêu chuẩn Python cho 10 Subagents của supportflast_ai
Bao gồm kiểm thử đơn vị và benchmark điều phối đồng thời 10 Subagents.
"""
import unittest
from core.subagent_dispatcher import SubagentDispatcher

class TestSubagents(unittest.TestCase):
    def setUp(self):
        self.dispatcher = SubagentDispatcher()

    def test_dispatcher_list_10_subagents(self):
        agents = self.dispatcher.list_subagents()
        self.assertEqual(len(agents), 10)
        ids = [a["id"] for a in agents]
        expected_ids = [
            "agent_triage", "agent_tech", "agent_infra", "agent_security", "agent_billing",
            "agent_mobile", "agent_network", "agent_storage", "agent_analytics", "agent_automation"
        ]
        for eid in expected_ids:
            self.assertIn(eid, ids)

    def test_triage_agent_urgent(self):
        res = self.dispatcher.dispatch("Hệ thống bị sập khẩn cấp server không phản hồi")
        self.assertEqual(res["status"], "success")
        is_p1 = "P1" in res.get("severity", "") or "P1" in str(res.get("triage_metadata", {}))
        self.assertTrue(is_p1)

    def test_tech_agent_syntax(self):
        res = self.dispatcher.dispatch("Bị lỗi NullPointerException khi gọi API", agent_id="agent_tech")
        self.assertEqual(res["status"], "success")
        self.assertGreater(len(res["detected_issues"]), 0)

    def test_security_agent_sqli_detection(self):
        res = self.dispatcher.dispatch("' OR 1=1; DROP TABLE users; --", agent_id="agent_security")
        self.assertEqual(res["status"], "success")
        self.assertTrue(res["threat_detected"])

    def test_billing_agent_plans(self):
        res = self.dispatcher.dispatch("Tôi muốn đăng ký gói hỗ trợ kỹ thuật nhanh", agent_id="agent_billing")
        self.assertEqual(res["status"], "success")
        self.assertGreater(len(res["available_plans"]), 0)

    def test_mobile_agent_battery(self):
        res = self.dispatcher.dispatch("Điện thoại bị nóng máy và hao pin khi chạy ngầm", agent_id="agent_mobile")
        self.assertEqual(res["status"], "success")
        self.assertIn("recommendations", res)
        self.assertGreater(len(res["recommendations"]), 0)

    def test_network_agent_tunnel(self):
        res = self.dispatcher.dispatch("Làm sao đưa máy chủ điện thoại ra Internet bằng Cloudflare Tunnel?", agent_id="agent_network")
        self.assertEqual(res["status"], "success")
        self.assertIn("network_tips", res)
        self.assertGreater(len(res["network_tips"]), 0)

    def test_storage_agent_sqlite(self):
        res = self.dispatcher.dispatch("Tối ưu hóa SQLite WAL mode và dọn dẹp bộ nhớ máy", agent_id="agent_storage")
        self.assertEqual(res["status"], "success")
        self.assertIn("storage_actions", res)
        self.assertGreater(len(res["storage_actions"]), 0)

    def test_analytics_agent_benchmark(self):
        res = self.dispatcher.dispatch("Đo hiệu năng và benchmark gọi đồng thời 10 subagents", agent_id="agent_analytics")
        self.assertEqual(res["status"], "success")
        self.assertIn("metrics_summary", res)
        self.assertGreater(len(res["metrics_summary"]), 0)

    def test_automation_agent_backup(self):
        res = self.dispatcher.dispatch("Lập lịch backup tự động và kích hoạt watchdog tự phục hồi", agent_id="agent_automation")
        self.assertEqual(res["status"], "success")
        self.assertIn("scheduled_jobs", res)
        self.assertGreater(len(res["scheduled_jobs"]), 0)

    def test_concurrent_10_subagents_execution(self):
        """Kiểm tra gọi liên tục hoặc đồng thời toàn bộ 10 subagents."""
        all_ids = [
            "agent_triage", "agent_tech", "agent_infra", "agent_security", "agent_billing",
            "agent_mobile", "agent_network", "agent_storage", "agent_analytics", "agent_automation"
        ]
        results = []
        for aid in all_ids:
            r = self.dispatcher.dispatch(f"Kiểm tra trạng thái hoạt động của {aid}", agent_id=aid)
            self.assertEqual(r["status"], "success")
            self.assertIn("execution_time_ms", r)
            results.append(r)
        self.assertEqual(len(results), 10)

if __name__ == "__main__":
    unittest.main()
