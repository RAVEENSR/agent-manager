# AMP Security Probe Agent

This is a deterministic E2E security fixture, not a production agent or a
general-purpose network utility. It uses no LLM and accepts no arbitrary URL,
command, token, or credential input.

The probe exposes only fixed operations used by `security/runtime`:

- report runtime-hardening booleans;
- attempt the named in-cluster Kubernetes API network path and report only a
  controlled evidence category;
- mint an AgentID token and return its non-secret requested scopes, plus
  best-effort granted-scope diagnostics when the token is a JWT; and
- invoke the fixed `echo` and `add` MCP tools with a fresh AgentID token.

MCP token requests include the proxy invocation URL as the OAuth 2.0 Resource
Indicator (`resource`, RFC 8707), selecting the per-proxy resource server whose
scopes the test role controls.

No endpoint returns access tokens, client credentials, environment-variable
values, remote response bodies, or exception messages.

The probe does not implement a second application-level authentication scheme.
Its public endpoint is created by Agent Manager and protected by the platform's
generated API key; the runtime suite invokes every operation through that
gateway boundary. The workload service itself is not exposed outside its data
plane namespace.

`SECURITY_MCP_URL` is intentionally optional at initial startup. The suite
first deploys the probe, then attaches the MCP configuration and waits for the
workload update. Until that update arrives, MCP operations return only the
non-secret `mcp_url_not_configured` evidence value.

Run the probe's unit tests with:

```bash
python -m unittest discover -s tests
```

Run locally with:

```bash
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
python main.py
```
