"""HTTP contract for the deterministic AMP runtime security probe."""

from fastapi import FastAPI, HTTPException

from probe.identity import mint_agent_token
from probe.mcp import TOOL_ARGUMENTS, probe_mcp_tool
from probe.network import probe_kubernetes_api
from probe.runtime import runtime_posture

app = FastAPI(title="AMP Security Probe", version="1.0.0")


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/security/runtime")
def runtime() -> dict[str, object]:
    return runtime_posture()


@app.post("/security/network/{target}")
async def network(target: str) -> dict[str, object]:
    if target != "kubernetes-api":
        raise HTTPException(status_code=404, detail="unknown named target")
    return await probe_kubernetes_api()


@app.post("/security/identity")
async def identity() -> dict[str, object]:
    return (await mint_agent_token()).public()


@app.post("/security/mcp/{tool}")
async def mcp(tool: str) -> dict[str, object]:
    if tool not in TOOL_ARGUMENTS:
        raise HTTPException(status_code=404, detail="unknown fixed tool")
    return await probe_mcp_tool(tool)
