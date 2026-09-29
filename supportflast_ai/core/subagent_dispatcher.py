import time
try:
    import psutil
except ImportError:
    psutil = None
from typing import Dict, Any, List
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

        # Giám sát RAM tuân thủ Phần 7.4 (psutil memory logging)
        try:
            mem_info = psutil.Process().memory_info()
            result["memory_usage_mb"] = round(mem_info.rss / (1024 * 1024), 2)
        except Exception:
            result["memory_usage_mb"] = 0.0

        return result
