"""AgentID token minting with strictly redacted public results."""

from __future__ import annotations

import base64
import json
import os
from dataclasses import dataclass

import httpx


SAFE_OAUTH_ERRORS = {
    "invalid_client",
    "invalid_request",
    "invalid_scope",
    "server_error",
    "temporarily_unavailable",
    "unauthorized_client",
}


@dataclass(slots=True)
class TokenResult:
    configured: bool
    token_minted: bool
    status_code: int | None
    requested_scopes: list[str]
    granted_scopes: list[str]
    oauth_error: str
    access_token: str

    def public(self) -> dict[str, object]:
        """Return only non-credential evidence safe for test output and logs."""

        return {
            "configured": self.configured,
            "token_minted": self.token_minted,
            "status_code": self.status_code,
            "requested_scopes": self.requested_scopes,
            # Best-effort diagnostic only. Opaque access tokens cannot expose
            # their granted scopes locally; MCP authorization is the
            # authoritative end-to-end enforcement check.
            "granted_scopes": self.granted_scopes,
            "oauth_error": self.oauth_error,
        }


def _jwt_scopes(token: str) -> list[str]:
    try:
        payload_segment = token.split(".")[1]
        payload_segment += "=" * (-len(payload_segment) % 4)
        payload = json.loads(base64.urlsafe_b64decode(payload_segment))
    except (IndexError, ValueError, json.JSONDecodeError):
        return []

    value = payload.get("scope", payload.get("scp", []))
    if isinstance(value, str):
        return sorted(set(value.split()))
    if isinstance(value, list):
        return sorted({scope for scope in value if isinstance(scope, str)})
    return []


def _normalized_scopes(value: str) -> list[str]:
    return sorted(set(value.split()))


def _token_request_form(requested_scopes: str, resource: str) -> dict[str, str]:
    form = {"grant_type": "client_credentials"}
    if requested_scopes:
        form["scope"] = requested_scopes
    if resource:
        # RFC 8707 selects the MCP proxy's resource server. Without it,
        # Thunder evaluates the request against its default resource server
        # and legitimately omits the proxy-specific permissions.
        form["resource"] = resource
    return form


async def mint_agent_token(resource: str = "") -> TokenResult:
    client_id = os.environ.get("AMP_AGENTID_CLIENT_ID", "")
    client_secret = os.environ.get("AMP_AGENTID_CLIENT_SECRET", "")
    token_endpoint = os.environ.get("AMP_AGENTID_TOKEN_ENDPOINT", "")
    requested_scopes = os.environ.get("AMP_AGENTID_SCOPES", "")
    requested_scope_list = _normalized_scopes(requested_scopes)
    configured = bool(client_id and client_secret and token_endpoint)
    if not configured:
        return TokenResult(
            configured=False,
            token_minted=False,
            status_code=None,
            requested_scopes=requested_scope_list,
            granted_scopes=[],
            oauth_error="not_configured",
            access_token="",
        )

    try:
        async with httpx.AsyncClient(
            timeout=10.0,
            trust_env=False,
        ) as client:
            response = await client.post(
                token_endpoint,
                data=_token_request_form(requested_scopes, resource),
                auth=httpx.BasicAuth(client_id, client_secret),
            )
    except httpx.HTTPError:
        return TokenResult(
            configured=True,
            token_minted=False,
            status_code=None,
            requested_scopes=requested_scope_list,
            granted_scopes=[],
            oauth_error="request_failed",
            access_token="",
        )

    oauth_error = ""
    token = ""
    try:
        body = response.json()
    except ValueError:
        body = {}
    if response.status_code == 200 and isinstance(body, dict):
        candidate = body.get("access_token", "")
        if isinstance(candidate, str):
            token = candidate
    elif isinstance(body, dict):
        candidate = body.get("error", "")
        if isinstance(candidate, str):
            oauth_error = (
                candidate if candidate in SAFE_OAUTH_ERRORS else "token_rejected"
            )

    return TokenResult(
        configured=True,
        token_minted=bool(token),
        status_code=response.status_code,
        requested_scopes=requested_scope_list,
        granted_scopes=_jwt_scopes(token),
        oauth_error=oauth_error,
        access_token=token,
    )
