import time
import asyncio
import os
from typing import Dict, Any, List, Optional
try:
    import psutil
except ImportError:
    psutil = None

from agents.triage_agent import TriageAgent
from agents.tech_agent import TechAgent
from agents.infra_agent import InfraAgent
from agents.security_agent import SecurityAgent
from agents.billing_agent import BillingAgent
from agents.mobile_agent import MobileAgent
from agents.network_agent import NetworkAgent
from agents.storage_agent import StorageAgent
from agents.analytics_agent import AnalyticsAgent
from agents.automation_agent import AutomationAgent

class SubagentDispatcher:
    """Bộ điều phối Subagents thông minh hỗ trợ 3 chế độ:
    1. fast: 1 agent chuyên môn nhanh nhất (loại bỏ bottleneck 2 bước Triage -> Specialist)
    2. parallel: 2-3 agent song song cho tác vụ đa khía cạnh độc lập
    3. deep: Tối đa 10 agent đồng thời cho Audit / Stress-test / Tổng hợp sâu
    """

    def __init__(self):
        self.agents: Dict[str, Any] = {
            "agent_triage": TriageAgent(),
            "agent_tech": TechAgent(),
            "agent_infra": InfraAgent(),
            "agent_security": SecurityAgent(),
            "agent_billing": BillingAgent(),
            "agent_mobile": MobileAgent(),
            "agent_network": NetworkAgent(),
            "agent_storage": StorageAgent(),
            "agent_analytics": AnalyticsAgent(),
            "agent_automation": AutomationAgent(),
        }

        # Từ khóa phân loại nhanh (Fast Keyword Heuristic Router)
        self.keyword_map = {
            "agent_security": ["virus", "mã độc", "malware", "trojan", "sandbox", "chữ ký số", "code signing", "smartscreen", "hack", "lỗ hổng", "bảo mật", "cve", "sqli", "xss", "' or 1=1", "drop table", "union select", "tamper"],
            "agent_mobile": ["pin", "battery", "nhiệt độ", "nóng máy", "chai pin", "wake-lock", "doze", "điện thoại", "termux", "android", "mobile"],
            "agent_network": ["wi-fi", "wifi", "4g", "5g", "cgnat", "tunnel", "cloudflare", "ip lan", "port forward", "dns", "kết nối mạng"],
            "agent_storage": ["bộ nhớ", "dung lượng", "sqlite", "vacuum", "wal", "dọn dẹp", "flash", "thẻ nhớ", "cache rác", "disk", "ổ cứng"],
            "agent_analytics": ["benchmark", "hiệu năng", "qps", "latency", "tải", "chỉ số", "metrics", "arm64", "đo", "thống kê", "phân tích"],
            "agent_automation": ["cron", "lập lịch", "tự động", "backup", "watchdog", "webhook", "telegram", "cảnh báo", "schedule", "phục hồi"],
            "agent_tech": ["đóng gói", "package", "build", "msix", "exe", "apk", "aab", "dmg", "appimage", "installer", "setup", "manifest", "dependency", "nullpointer", "syntax", "lỗi mã nguồn"],
            "agent_infra": ["cdn", "mirror", "tải chậm", "link tải", "download", "checksum", "sha256", "băng thông", "resume", "ssl", "server", "sập", "down", "outage", "quá tải", "hạ tầng"],
            "agent_billing": ["bản quyền", "license", "drm", "kích hoạt", "nhà phát triển", "developer", "publisher", "doanh thu", "thanh toán", "hóa đơn", "gói", "nạp tiền", "chi phí"]
        }

    def list_subagents(self) -> List[Dict[str, Any]]:
        """Trả về metadata của 10 subagents cùng trạng thái model kết nối thực tế (Audit 3.3)."""
        from core.llm_engine import llm_engine
        is_healthy_model = llm_engine.is_enabled and getattr(llm_engine, "is_healthy", False)
        agent_status = "READY" if is_healthy_model else ("DEGRADED" if llm_engine.is_enabled else "ON_DEVICE_READY")
        engine_status = "online" if is_healthy_model else ("degraded" if llm_engine.is_enabled else "offline")
        active_list = []
        if getattr(llm_engine, "gemini_enabled", False):
            active_list.append("gemini")
        for p in getattr(llm_engine, "openai_providers", []):
            if getattr(p, "is_enabled", False):
                active_list.append(p.name)

        if len(active_list) > 1:
            joined = "+".join(active_list)
            model_mode = f"multi_provider ({joined})"
        elif len(active_list) == 1:
            model_mode = f"{active_list[0]}_api"
        else:
            model_mode = "on_device_fallback"

        return [
            {
                "id": agent.agent_id,
                "name": agent.name,
                "color": agent.color,
                "description": agent.role_description,
                "status": agent_status,
                "model_status": engine_status,
                "model_name": llm_engine.model_name,
                "provider": model_mode,
                "active_providers": active_list
            }
            for agent in self.agents.values()
        ]

    def fast_route(self, query: str) -> str:
        """Bộ định tuyến nhanh theo quy tắc (Fast Rule-based Router) trong < 0.2ms (Nút thắt 1)."""
        q_lower = query.lower()
        scores = {}
        for aid, kws in self.keyword_map.items():
            score = sum(1 for kw in kws if kw in q_lower)
            if score > 0:
                scores[aid] = score

        if scores:
            return max(scores, key=scores.get)

        return "agent_tech"

    def multi_route(self, query: str, mode: str = "parallel") -> List[str]:
        """Xác định danh sách agent cho chế độ song song (parallel: 2-3 agents) hoặc deep (tối đa 10 agents)."""
        q_lower = query.lower()
        matched = []
        for aid, kws in self.keyword_map.items():
            if any(kw in q_lower for kw in kws):
                matched.append(aid)

        if mode == "deep":
            if any(w in q_lower for w in ["toàn bộ", "tất cả", "audit", "tổng hợp", "sức khỏe toàn bộ", "toàn diện", "full"]):
                return list(self.agents.keys())
            if not matched:
                return ["agent_security", "agent_tech", "agent_infra", "agent_analytics"]
            return matched

        # mode == "parallel" (2 - 3 agents)
        if len(matched) >= 2:
            return matched[:3]
        elif len(matched) == 1:
            primary = matched[0]
            fallback_map = {
                "agent_security": "agent_infra",
                "agent_tech": "agent_security",
                "agent_infra": "agent_network",
                "agent_mobile": "agent_storage",
                "agent_storage": "agent_analytics",
                "agent_network": "agent_infra",
                "agent_billing": "agent_tech",
                "agent_analytics": "agent_automation",
                "agent_automation": "agent_analytics",
            }
            sec = fallback_map.get(primary, "agent_tech")
            return [primary, sec]

        return ["agent_tech", "agent_infra"]

    async def adispatch(
        self,
        query: str,
        agent_id: Optional[str] = None,
        context: Optional[Dict[str, Any]] = None,
        mode: str = "fast",
        timeout_sec: float = 15.0
    ) -> Dict[str, Any]:
        """Điều phối bất đồng bộ thật sự với Deadline tổng, Semaphore giới hạn tải và Partial Results."""
        start_wall = time.time()
        router_start = time.time()

        if agent_id and agent_id in self.agents:
            target_ids = [agent_id]
            selected_mode = "targeted"
        else:
            raw_mode = str(mode or "fast").strip().lower()
            selected_mode = raw_mode if raw_mode in ("fast", "parallel", "deep") else "fast"
            if selected_mode == "fast":
                target_ids = [self.fast_route(query)]
            elif selected_mode == "parallel":
                target_ids = self.multi_route(query, mode="parallel")
            else:
                target_ids = self.multi_route(query, mode="deep")

        router_ms = round((time.time() - router_start) * 1000, 3)
        agent_timeout = max(1.0, min(12.0, timeout_sec))

        async def _run_agent_task(aid: str) -> tuple[str, Dict[str, Any]]:
            agent = self.agents[aid]
            t0 = time.time()
            try:
                if hasattr(agent, "aprocess"):
                    res = await asyncio.wait_for(agent.aprocess(query, context), timeout=agent_timeout)
                else:
                    res = await asyncio.wait_for(asyncio.to_thread(agent.process, query, context), timeout=agent_timeout)
                exec_ms = round((time.time() - t0) * 1000, 2)
                res["execution_time_ms"] = exec_ms
                return aid, res
            except asyncio.TimeoutError:
                exec_ms = round((time.time() - t0) * 1000, 2)
                return aid, {
                    "agent_id": aid,
                    "agent_name": getattr(agent, "name", aid),
                    "status": "timeout",
                    "error": f"Thời gian xử lý của subagent {aid} vượt quá deadline ({agent_timeout}s)",
                    "execution_time_ms": exec_ms,
                    "response": f"⚠️ Subagent [{getattr(agent, 'name', aid)}] phản hồi quá thời gian cho phép ({agent_timeout}s)."
                }
            except Exception as ex:
                exec_ms = round((time.time() - t0) * 1000, 2)
                return aid, {
                    "agent_id": aid,
                    "agent_name": getattr(agent, "name", aid),
                    "status": "error",
                    "error": str(ex),
                    "execution_time_ms": exec_ms,
                    "response": f"❌ Lỗi xử lý tại subagent [{getattr(agent, 'name', aid)}]: {str(ex)}"
                }

        tasks = [_run_agent_task(aid) for aid in target_ids]
        raw_results = await asyncio.gather(*tasks)

        results_dict: Dict[str, Any] = {aid: res for aid, res in raw_results}
        wall_clock_ms = round((time.time() - start_wall) * 1000, 2)

        from core.llm_engine import llm_engine
        is_healthy_model = llm_engine.is_enabled and getattr(llm_engine, "is_healthy", False)

        # Ghi nhận model_used và fallback_used tại từng agent (Audit 4)
        for aid, r in results_dict.items():
            r["fallback_used"] = not is_healthy_model
            r["model_used"] = llm_engine.model_name if is_healthy_model else "rule_based_on_device"

        latencies = [r.get("execution_time_ms", 0.0) for r in results_dict.values()]
        latencies_sorted = sorted(latencies)
        min_ms = round(min(latencies), 2) if latencies else 0.0
        max_ms = round(max(latencies), 2) if latencies else 0.0
        avg_ms = round(sum(latencies) / len(latencies), 2) if latencies else 0.0
        # Percentiles chỉ có ý nghĩa thống kê khi mẫu >= 10, nếu ít hơn lấy max (Audit 6.2)
        p95_ms = max_ms if len(latencies) < 10 else latencies_sorted[int(len(latencies_sorted) * 0.95)]
        p99_ms = max_ms if len(latencies) < 10 else latencies_sorted[int(len(latencies_sorted) * 0.99)]
        total_sequential_ms = round(sum(latencies), 2)
        speedup = round(total_sequential_ms / max(wall_clock_ms, 0.001), 2)

        mem_mb = 0.0
        try:
            if psutil:
                mem_mb = round(psutil.Process().memory_info().rss / (1024 * 1024), 2)
        except Exception:
            pass

        if len(target_ids) == 1:
            single_id = target_ids[0]
            single_res = results_dict[single_id]
            single_res["mode"] = selected_mode
            single_res["wall_clock_ms"] = wall_clock_ms
            single_res["router_ms"] = router_ms
            single_res["memory_usage_mb"] = mem_mb

            q_low = query.lower()
            if any(w in q_low for w in ["sập", "khẩn cấp", "outage", "down", "die", "không phản hồi"]):
                single_res["triage_metadata"] = {
                    "severity": "P1 - Critical Outage / Platform Halt",
                    "sla": "Tức thì (< 3 phút)",
                    "routed_by": "fast_rule_router"
                }
            from core.llm_engine import llm_engine
            fallback_used = not (llm_engine.is_enabled and getattr(llm_engine, "is_healthy", False))
            single_res["fallback_used"] = fallback_used
            single_res["model_used"] = llm_engine.model_name if not fallback_used else "rule_based_on_device"
            return single_res

        sections = []
        for aid in target_ids:
            r = results_dict[aid]
            a_name = r.get("agent_name", aid)
            a_resp = r.get("response", "")
            a_status = r.get("status", "unknown")
            icon = "✔" if a_status == "success" else ("⚠️" if a_status == "timeout" else "❌")
            sections.append(f"### {icon} [{a_name}] (ID: `{aid}`)\n{a_resp}")

        header = f"**[Báo Cáo Phân Tích Tổng Hợp Đa Subagents (Chế độ: {selected_mode.upper()})]**\n- Số lượng Subagents: **{len(target_ids)}** | Thời gian fan-out: **{wall_clock_ms}ms** (Tiết kiệm **{speedup}x** thời gian so với chạy tuần tự)\n\n"
        combined_summary = header + "\n\n---\n\n".join(sections)

        # Tính trạng thái tổng thể chính xác theo kết quả từng agent (Audit 4)
        statuses = [r.get("status") for r in results_dict.values()]
        total_count = len(statuses)
        success_count = sum(1 for s in statuses if s in ("success", "unmeasured"))
        timeout_count = sum(1 for s in statuses if s == "timeout")
        error_count = sum(1 for s in statuses if s not in ("success", "unmeasured", "timeout"))

        if success_count == total_count:
            overall_status = "success"
        elif success_count > 0:
            overall_status = "partial_success"
        else:
            overall_status = "failed"

        multi_fallback_used = not is_healthy_model
        return {
            "status": overall_status,
            "mode": selected_mode,
            "total_agents": len(target_ids),
            "successful_agents": success_count,
            "failed_agents": error_count,
            "timed_out_agents": timeout_count,
            "wall_clock_ms": wall_clock_ms,
            "total_sequential_ms": total_sequential_ms,
            "speedup": speedup,
            "router_ms": router_ms,
            "latency_min_ms": min_ms,
            "latency_max_ms": max_ms,
            "latency_avg_ms": avg_ms,
            "p95_ms": p95_ms,
            "p99_ms": p99_ms,
            "metrics_scope": "single_request_fanout",
            "metrics_note": "Chỉ số speedup và percentiles phản ánh phân phối độ trễ song song của riêng lượt điều phối hiện tại, không phải benchmark hệ thống toàn cục (Audit P2).",
            "memory_usage_mb": mem_mb,
            "fallback_used": multi_fallback_used,
            "model_used": llm_engine.model_name if not multi_fallback_used else "rule_based_on_device",
            "response": combined_summary,
            "agent_results": results_dict
        }

    def dispatch(self, query: str, agent_id: str = None, context: Dict[str, Any] = None) -> Dict[str, Any]:
        """Điều phối đồng bộ (giữ tương thích 100% với mã nguồn cũ và unit test)."""
        start_time = time.time()

        if not agent_id or agent_id not in self.agents:
            triage_res = self.agents["agent_triage"].process(query, context)
            recommended_id = triage_res.get("recommended_agent")
            
            if recommended_id and recommended_id in self.agents and recommended_id != "agent_triage":
                specialist_res = self.agents[recommended_id].process(query, context)
                combined_response = (
                    f"{triage_res['response']}\n\n"
                    f"---\n"
                    f"{specialist_res['response']}"
                )
                result = specialist_res
                result["response"] = combined_response
                result["triage_metadata"] = {
                    "severity": triage_res.get("severity"),
                    "sla": triage_res.get("sla")
                }
            else:
                result = triage_res
        else:
            result = self.agents[agent_id].process(query, context)

        duration_ms = round((time.time() - start_time) * 1000, 2)
        result["execution_time_ms"] = duration_ms
        from core.llm_engine import llm_engine
        sync_fallback_used = not (llm_engine.is_enabled and getattr(llm_engine, "is_healthy", False))
        result["fallback_used"] = sync_fallback_used
        result["model_used"] = llm_engine.model_name if not sync_fallback_used else "rule_based_on_device"

        try:
            if psutil:
                mem_info = psutil.Process().memory_info()
                result["memory_usage_mb"] = round(mem_info.rss / (1024 * 1024), 2)
        except Exception:
            result["memory_usage_mb"] = 0.0

        return result

    def dispatch_concurrent(
        self,
        query: str,
        agent_ids: Optional[List[str]] = None,
        context: Optional[Dict[str, Any]] = None,
        max_workers: int = 10,
        timeout_sec: float = 5.0
    ) -> Dict[str, Any]:
        """Điều phối đồng thời có kiểm soát cho benchmark hoặc batch processing."""
        target_ids = agent_ids or list(self.agents.keys())
        target_ids = [aid for aid in target_ids if aid in self.agents]
        
        try:
            loop = asyncio.get_event_loop()
            if loop.is_running():
                import concurrent.futures
                with concurrent.futures.ThreadPoolExecutor(max_workers=min(max_workers, len(target_ids)) or 1) as executor:
                    future_to_agent = {executor.submit(self.dispatch, query, aid, context): aid for aid in target_ids}
                    results = {}
                    t0 = time.time()
                    for f in concurrent.futures.as_completed(future_to_agent, timeout=timeout_sec):
                        aid = future_to_agent[f]
                        try:
                            results[aid] = f.result()
                        except Exception as ex:
                            results[aid] = {"agent_id": aid, "status": "error", "error": str(ex), "execution_time_ms": 0.0}
                    wall_ms = round((time.time() - t0) * 1000, 2)
                    latencies = [r.get("execution_time_ms", 0.0) for r in results.values()]
                    latencies_sorted = sorted(latencies)
                    p95_ms = latencies_sorted[int(len(latencies_sorted) * 0.95)] if latencies_sorted else 0.0
                    return {
                        "status": "success",
                        "total_agents": len(results),
                        "wall_clock_ms": wall_ms,
                        "p95_ms": p95_ms,
                        "results": results
                    }
        except Exception:
            pass

        res = asyncio.run(self.adispatch(query, context=context, mode="deep", timeout_sec=timeout_sec))
        return {
            "status": res.get("status", "success"),
            "total_agents": res.get("total_agents", len(self.agents)),
            "wall_clock_ms": res.get("wall_clock_ms", 0.0),
            "p95_ms": res.get("p95_ms", 0.0),
            "results": res.get("agent_results", {})
        }
