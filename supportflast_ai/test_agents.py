"""Unit test chuẩn thư viện tiêu chuẩn Python cho 10 Subagents của supportflast_ai
Bao gồm kiểm thử đơn vị, aprocess async coroutines và benchmark điều phối đồng thời 10 Subagents.
"""
import os
import sys
sys.path.insert(0, os.path.abspath(os.path.dirname(__file__)))

import unittest
import asyncio
import inspect
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

    def test_all_10_agents_have_aprocess(self):
        """Kiểm tra toàn bộ 10 subagents bắt buộc phải có hàm aprocess coroutine bất đồng bộ (Audit P1.2)."""
        for aid, agent in self.dispatcher.agents.items():
            self.assertTrue(hasattr(agent, "aprocess"), f"Agent {aid} thiếu method aprocess")
            self.assertTrue(inspect.iscoroutinefunction(agent.aprocess), f"Agent {aid}.aprocess không phải là coroutine function")

    def test_all_10_agents_aprocess_execution(self):
        """Kiểm tra gọi trực tiếp aprocess trên 10 agents bất đồng bộ."""
        async def _run_all():
            for aid, agent in self.dispatcher.agents.items():
                res = await agent.aprocess("Kiểm tra aprocess bất đồng bộ", {})
                self.assertEqual(res["status"], "success")
                self.assertEqual(res["agent_id"], aid)
        asyncio.run(_run_all())

    def test_dispatch_concurrent_method(self):
        """Kiểm tra gọi đồng thời 10 subagents qua ThreadPoolExecutor và kiểm tra độ trễ."""
        res = self.dispatcher.dispatch_concurrent("Kiểm tra sức khỏe toàn bộ hệ thống phân tán")
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["total_agents"], 10)
        self.assertIn("wall_clock_ms", res)
        self.assertIn("p95_ms", res)
        self.assertLess(res["wall_clock_ms"], 500.0)

    def test_adispatch_fast_mode(self):
        """Kiểm tra điều phối bất đồng bộ 1 agent nhanh (Nút thắt 1)."""
        res = asyncio.run(self.dispatcher.adispatch("Bị lỗi NullPointerException khi build", mode="fast"))
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["mode"], "fast")
        self.assertIn("wall_clock_ms", res)
        self.assertIn("router_ms", res)

    def test_adispatch_parallel_mode(self):
        """Kiểm tra fan-out song song 2-3 agents (Nút thắt 1 & 3)."""
        res = asyncio.run(self.dispatcher.adispatch("Bảo mật đóng gói APK và cấu hình CDN", mode="parallel"))
        self.assertIn(res["status"], ("success", "partial_success"))
        self.assertEqual(res["mode"], "parallel")
        self.assertGreaterEqual(res["total_agents"], 2)
        self.assertIn("agent_results", res)
        self.assertIn("speedup", res)

    def test_adispatch_deep_mode(self):
        """Kiểm tra điều phối chuyên sâu 10 agents đồng thời với deadline (Nút thắt 3)."""
        res = asyncio.run(self.dispatcher.adispatch("Kiểm tra sức khỏe toàn bộ hệ thống phân tán", mode="deep"))
        self.assertIn(res["status"], ("success", "partial_success"))
        self.assertEqual(res["mode"], "deep")
        self.assertEqual(res["total_agents"], 10)
        self.assertIn("agent_results", res)

    def test_llm_engine_health_check_offline_and_cache(self):
        """Kiểm tra cơ chế chẩn đoán kết nối model thực tế và Cache TTL 60s (Audit P1.5)."""
        from core.llm_engine import llm_engine
        health1 = asyncio.run(llm_engine.check_health())
        self.assertIn("status", health1)
        self.assertIn("code", health1)
        self.assertIn("model", health1)

        # Lần 2 phải trả về từ cache nếu chưa đổi thời gian
        health2 = asyncio.run(llm_engine.check_health())
        self.assertEqual(health2["status"], health1["status"])

    def test_subagents_list_truthful_model_status(self):
        """Kiểm tra metadata agent phản ánh trung thực trạng thái model thật (Mục 4)."""
        agents = self.dispatcher.list_subagents()
        self.assertEqual(len(agents), 10)
        for a in agents:
            self.assertIn("model_status", a)
            self.assertIn("model_name", a)
            self.assertIn("provider", a)

    def test_error_classification(self):
        """Kiểm tra phân loại mã lỗi API chính xác (Mục 4)."""
        from core.llm_engine import llm_engine
        self.assertEqual(llm_engine._classify_error("API_KEY_INVALID: Key not valid"), "API_KEY_INVALID")
        self.assertEqual(llm_engine._classify_error("404: models/gemini-3.8-flash not found"), "MODEL_NOT_FOUND")
        self.assertEqual(llm_engine._classify_error("ResourceExhausted: 429 Quota exceeded"), "QUOTA_EXCEEDED")
        self.assertEqual(llm_engine._classify_error("Client timeout after 4000ms"), "TIMEOUT")

if __name__ == "__main__":
    unittest.main()
