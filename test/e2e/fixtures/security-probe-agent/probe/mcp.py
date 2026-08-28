"""Call two fixed harmless MCP tools using a freshly minted AgentID token."""

from __future__ import annotations

import os
from typing import Any

import httpx

from probe.identity import mint_agent_token


TOOL_ARGUMENTS: dict[str, dict[str, Any]] = {
    "echo": {"message": "amp-security-probe"},
    "add": {"a": 1, "b": 2},
}


async def probe_mcp_tool(tool: str) -> dict[str, object]:
    if tool not in TOOL_ARGUMENTS:
        return {
            "tool": tool,
            "phase": "validation",
            "token_minted": False,
            "http_status": None,
            "authorized": False,
            "result_received": False,
            "error": "unknown_tool",
        }

    proxy_url = os.environ.get("SECURITY_MCP_URL", "")
    if not proxy_url:
        return {
            "tool": tool,
            "phase": "configuration",
            "token_minted": False,
            "http_status": None,
            "authorized": False,
            "result_received": False,
            "error": "mcp_url_not_configured",
        }

    token = await mint_agent_token(resource=proxy_url)
    if not token.access_token:
        public = token.public()
        return {
            "tool": tool,
            "phase": "token",
            "token_minted": False,
            "http_status": public["status_code"],
            "authorized": False,
            "result_received": False,
            "error": public["oauth_error"] or "token_unavailable",
        }

    headers = {
        "Authorization": f"Bearer {token.access_token}",
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    }
    initialize = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-06-18",
            "capabilities": {},
            "clientInfo": {"name": "amp-security-probe", "version": "1.0"},
        },
    }

    try:
        async with httpx.AsyncClient(timeout=15.0, trust_env=False) as client:
            init_response = await client.post(proxy_url, headers=headers, json=initialize)
            if init_response.status_code != 200:
                return _mcp_result(tool, "initialize", init_response.status_code, False)

            session_id = init_response.headers.get("Mcp-Session-Id", "")
            if session_id:
                headers["Mcp-Session-Id"] = session_id
                await client.post(
                    proxy_url,
                    headers=headers,
                    json={"jsonrpc": "2.0", "method": "notifications/initialized"},
                )

            call_response = await client.post(
                proxy_url,
                headers=headers,
                json={
                    "jsonrpc": "2.0",
                    "id": 2,
                    "method": "tools/call",
                    "params": {
                        "name": tool,
                        "arguments": TOOL_ARGUMENTS[tool],
                    },
                },
            )
    except httpx.HTTPError:
        return {
            "tool": tool,
            "phase": "request",
            "token_minted": True,
            "http_status": None,
            "authorized": False,
            "result_received": False,
            "error": "request_failed",
        }

    result_received = False
    if call_response.status_code == 200:
        try:
            payload = call_response.json()
            result_received = isinstance(payload, dict) and "result" in payload
        except ValueError:
            # A streamable-HTTP MCP response can be text/event-stream. HTTP 200
            # still proves the gateway authorized and forwarded the tool call.
            result_received = True
    return _mcp_result(
        tool,
        "tool_call",
        call_response.status_code,
        result_received,
    )


def _mcp_result(
    tool: str,
    phase: str,
    status: int,
    result_received: bool,
) -> dict[str, object]:
    return {
        "tool": tool,
        "phase": phase,
        "token_minted": True,
        "http_status": status,
        "authorized": phase == "tool_call" and status == 200,
        "result_received": result_received,
        "error": "",
    }
