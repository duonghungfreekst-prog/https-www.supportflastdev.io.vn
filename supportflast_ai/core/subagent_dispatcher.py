import time
import concurrent.futures
try:
    import psutil
except ImportError:
    psutil = None
from typing import Dict, Any, List, Optional
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

    def list_subagents(self) -> List[Dict[str, Any]]:
        """Trả về thông tin metadata của 10 subagents để UI và di động hiển thị trực quan."""
        return [
            {
                "id": agent.agent_id,
                "name": agent.name,
                "color": agent.color,
                "description": agent.role_description,
                "status": "online"
            }
            for agent in self.agents.values()
        ]

    def dispatch(self, query: str, agent_id: str = None, context: Dict[str, Any] = None) -> Dict[str, Any]:
        """Điều phối truy vấn tới Subagent được chỉ định hoặc tự động qua Triage."""
        start_time = time.time()

        # Nếu không chỉ định agent hoặc agent không hợp lệ -> dùng Triage Agent
        if not agent_id or agent_id not in self.agents:
            triage_res = self.agents["agent_triage"].process(query, context)
            recommended_id = triage_res.get("recommended_agent")
            
            # Nếu triage khuyến nghị một agent chuyên trách, thực hiện xử lý chuyên sâu tiếp theo
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

        # Giám sát RAM tuân thủ Phần 7 (psutil memory logging)
        try:
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
        """Điều phối truy vấn đồng thời tới danh sách Subagents với Worker Pool kiểm soát an toàn."""
        target_ids = agent_ids or list(self.agents.keys())
        target_ids = [aid for aid in target_ids if aid in self.agents]
        
        start_wall = time.time()
        results: Dict[str, Any] = {}
        worker_count = min(max_workers, len(target_ids)) or 1

        def _execute_single(aid: str) -> tuple[str, Dict[str, Any]]:
            try:
                res = self.dispatch(query, agent_id=aid, context=context)
                return aid, res
            except Exception as e:
                return aid, {
                    "agent_id": aid,
                    "status": "error",
                    "error": str(e),
                    "execution_time_ms": 0.0
                }

        with concurrent.futures.ThreadPoolExecutor(max_workers=worker_count) as executor:
            future_to_agent = {executor.submit(_execute_single, aid): aid for aid in target_ids}
            for future in concurrent.futures.as_completed(future_to_agent, timeout=timeout_sec):
                aid = future_to_agent[future]
                try:
                    agent_id, res = future.result()
                    results[agent_id] = res
                except Exception as ex:
                    results[aid] = {
                        "agent_id": aid,
                        "status": "timeout_or_error",
                        "error": str(ex),
                        "execution_time_ms": 0.0
                    }

        wall_clock_ms = round((time.time() - start_wall) * 1000, 2)
        latencies = [r.get("execution_time_ms", 0.0) for r in results.values()]
        latencies_sorted = sorted(latencies)
        
        p95_ms = latencies_sorted[int(len(latencies_sorted) * 0.95)] if latencies_sorted else 0.0
        p99_ms = latencies_sorted[int(len(latencies_sorted) * 0.99)] if latencies_sorted else 0.0
        total_sequential_ms = round(sum(latencies), 2)
        speedup = round(total_sequential_ms / max(wall_clock_ms, 0.001), 2)

        mem_mb = 0.0
        try:
            if psutil:
                mem_mb = round(psutil.Process().memory_info().rss / (1024 * 1024), 2)
        except Exception:
            pass

        return {
            "status": "success",
            "total_agents": len(results),
            "wall_clock_ms": wall_clock_ms,
            "total_sequential_ms": total_sequential_ms,
            "speedup": speedup,
            "p95_ms": p95_ms,
            "p99_ms": p99_ms,
            "memory_usage_mb": mem_mb,
            "results": results
        }
