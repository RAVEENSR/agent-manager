"""Named network probes for destinations that must be sandbox-blocked."""

from __future__ import annotations

import ipaddress
import os

import httpx


def classify_network_failure(
    error: httpx.HTTPError,
    *,
    verified_cluster_target: bool,
) -> tuple[str, str]:
    """Classify transport evidence without exposing exception or target details.

    A connect timeout proves that no connection was established. A connect
    rejection is also blocking evidence when the destination came from
    Kubernetes' injected API-service address: that service is known to be live,
    while policy implementations may deny with either DROP or REJECT behavior.
    Errors after connection establishment remain indeterminate and fail the
    security assertion.
    """

    if isinstance(error, httpx.ConnectTimeout):
        return "blocked", "connect_timeout"
    if isinstance(error, httpx.ConnectError):
        if verified_cluster_target:
            return "blocked", "connect_rejected"
        return "indeterminate", "unverified_connect_error"
    if isinstance(error, httpx.PoolTimeout):
        return "indeterminate", "pool_timeout"
    if isinstance(error, httpx.ReadTimeout):
        return "indeterminate", "read_timeout"
    if isinstance(error, httpx.WriteTimeout):
        return "indeterminate", "write_timeout"
    if isinstance(error, httpx.ProtocolError):
        return "indeterminate", "protocol_error"
    return "indeterminate", "transport_error"


def _verified_cluster_api_host(host: str, injected_host: str | None) -> bool:
    """Require the target to be Kubernetes-injected and a literal IP address."""

    if not injected_host or host != injected_host:
        return False
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


async def probe_kubernetes_api() -> dict[str, object]:
    """Attempt the in-cluster API without credentials and report reachability only.

    Any HTTP response proves the network path was reachable, even a 401 or 403.
    A timeout or rejection while establishing a connection to Kubernetes'
    injected, known-live API-service address proves that connectivity was
    denied. Errors after connection establishment remain indeterminate.
    Error messages and destination values are deliberately never returned.
    """

    injected_host = os.environ.get("KUBERNETES_SERVICE_HOST")
    host = injected_host or "kubernetes.default.svc"
    port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
    verified_cluster_target = _verified_cluster_api_host(host, injected_host)
    try:
        timeout = httpx.Timeout(connect=3.0, read=3.0, write=3.0, pool=3.0)
        async with httpx.AsyncClient(
            timeout=timeout,
            verify=False,
            trust_env=False,
        ) as client:
            response = await client.get(f"https://{host}:{port}/version")
        return {
            "target": "kubernetes-api",
            "outcome": "reachable",
            "evidence": "http_response",
            "http_status": response.status_code,
        }
    except httpx.HTTPError as error:
        outcome, evidence = classify_network_failure(
            error,
            verified_cluster_target=verified_cluster_target,
        )
        return {
            "target": "kubernetes-api",
            "outcome": outcome,
            "evidence": evidence,
            "http_status": None,
        }
