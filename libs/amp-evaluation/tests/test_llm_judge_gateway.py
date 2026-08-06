# Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

"""
Gateway-routing tests for LLM-as-judge.

When the framework is configured with a gateway base URL and API key, the
judge must route every completion through that gateway: the base URL is passed
as ``api_base`` and the key is injected as a per-provider auth header via
``client_args``. No actual LLM calls are made — the LLM client is mocked.
"""

import json
import os
import sys
from unittest.mock import MagicMock, patch

import pytest

# Mock the LLM client module so base.py's import resolves to a stub we control.
_mock_any_llm = MagicMock()
sys.modules.setdefault("any_llm", _mock_any_llm)

from amp_evaluation.config import reload_config  # noqa: E402
from amp_evaluation.evaluators.base import JudgeOutput, LLMAsJudgeEvaluator  # noqa: E402
from amp_evaluation.trace import TokenUsage, Trace, TraceMetrics  # noqa: E402


def _make_trace() -> Trace:
    return Trace(
        trace_id="t-1",
        input="What is AI?",
        output="AI is artificial intelligence.",
        metrics=TraceMetrics(
            total_duration_ms=100.0,
            token_usage=TokenUsage(input_tokens=10, output_tokens=20, total_tokens=30),
        ),
        spans=[],
    )


def _response(score: float = 0.9, explanation: str = "ok") -> MagicMock:
    resp = MagicMock()
    resp.choices = [MagicMock()]
    resp.choices[0].message.content = json.dumps({"score": score, "explanation": explanation})
    return resp


class _SimpleJudge(LLMAsJudgeEvaluator):
    name = "simple-judge"

    def build_prompt(self, trace: Trace, task=None) -> str:
        return f"Evaluate: {trace.output}"


@pytest.fixture(autouse=True)
def _isolate_gateway_env():
    """Ensure a clean gateway config around each test, then restore."""
    keys = ("AMP_LLM_JUDGE_API_BASE", "AMP_LLM_JUDGE_API_KEY")
    saved = {k: os.environ.get(k) for k in keys}
    for k in keys:
        os.environ.pop(k, None)
    reload_config()
    yield
    for k, v in saved.items():
        if v is None:
            os.environ.pop(k, None)
        else:
            os.environ[k] = v
    reload_config()


@patch("any_llm.completion")
def test_gateway_config_routes_via_api_base_and_auth_header(mock_completion):
    mock_completion.return_value = _response()
    os.environ["AMP_LLM_JUDGE_API_BASE"] = "https://gateway.example/v1"
    os.environ["AMP_LLM_JUDGE_API_KEY"] = "secret-key"
    reload_config()

    _SimpleJudge(model="anthropic/claude-sonnet-4-6").evaluate(_make_trace())

    mock_completion.assert_called_once()
    kwargs = mock_completion.call_args.kwargs
    # Structured output is requested via the Pydantic model (portable across
    # providers) rather than {"type": "json_object"} (rejected by some, e.g.
    # Anthropic).
    assert kwargs["response_format"] is JudgeOutput
    assert kwargs["api_base"] == "https://gateway.example/v1"
    assert kwargs["client_args"] == {"default_headers": {"api-key": "secret-key"}}
    # The provider SDK requires a non-empty api_key to construct even though the
    # gateway authenticates via the api-key header; a placeholder is passed and
    # the real key is never sent as the bearer token.
    assert kwargs["api_key"]
    assert kwargs["api_key"] != "secret-key"


@patch("any_llm.completion")
def test_without_gateway_config_no_api_base_or_client_args(mock_completion):
    mock_completion.return_value = _response()

    _SimpleJudge(model="openai/gpt-4o-mini").evaluate(_make_trace())

    mock_completion.assert_called_once()
    kwargs = mock_completion.call_args.kwargs
    assert "api_base" not in kwargs
    assert "client_args" not in kwargs


@patch("any_llm.completion")
def test_bedrock_omits_response_format(mock_completion):
    # any-llm's Bedrock provider rejects response_format; it must be omitted and
    # the JSON-instruction prompt + Pydantic validation used instead.
    mock_completion.return_value = _response()
    _SimpleJudge(model="bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0").evaluate(_make_trace())
    assert "response_format" not in mock_completion.call_args.kwargs


@patch("any_llm.completion")
def test_non_bedrock_includes_response_format(mock_completion):
    mock_completion.return_value = _response()
    _SimpleJudge(model="openai/gpt-4o-mini").evaluate(_make_trace())
    assert mock_completion.call_args.kwargs["response_format"] is JudgeOutput


# ---------------------------------------------------------------------------
# Per-provider header injection. The api-key header must reach every provider's
# client, but the constructor kwarg differs by SDK (verified against real
# clients): default_headers for OpenAI/Anthropic, http_options for Gemini, a
# custom async http client for Mistral, and base_url+default_headers for Groq
# (whose any-llm provider ignores api_base).
# ---------------------------------------------------------------------------


def _set_gateway(base="https://gw.example/v1", key="secret-key"):
    os.environ["AMP_LLM_JUDGE_API_BASE"] = base
    os.environ["AMP_LLM_JUDGE_API_KEY"] = key
    reload_config()


def test_gateway_kwargs_openai_uses_default_headers():
    _set_gateway()
    saved = os.environ.get("GATEWAY_API_KEY")
    os.environ.pop("GATEWAY_API_KEY", None)
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("openai/gpt-4o-mini")
        assert kw["api_base"] == "https://gw.example/v1"
        assert kw["api_key"] == "gateway"
        assert kw["client_args"] == {"default_headers": {"api-key": "secret-key"}}
    finally:
        if saved is None:
            os.environ.pop("GATEWAY_API_KEY", None)
        else:
            os.environ["GATEWAY_API_KEY"] = saved


def test_gateway_kwargs_generic_uses_env_placeholder_as_sdk_api_key():
    # GATEWAY_API_KEY (if set) becomes the SDK's own placeholder api_key —
    # still not the real gateway secret, which stays confined to the
    # default_headers "api-key" header.
    _set_gateway()
    saved = os.environ.get("GATEWAY_API_KEY")
    os.environ["GATEWAY_API_KEY"] = "generic-placeholder"
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("openai/gpt-4o-mini")
        assert kw["api_key"] == "generic-placeholder"
        assert kw["client_args"] == {"default_headers": {"api-key": "secret-key"}}
    finally:
        if saved is None:
            os.environ.pop("GATEWAY_API_KEY", None)
        else:
            os.environ["GATEWAY_API_KEY"] = saved


def test_gateway_kwargs_anthropic_uses_default_headers():
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("anthropic/claude-haiku-4-5")
    assert kw["api_base"] == "https://gw.example/v1"
    assert kw["client_args"] == {"default_headers": {"api-key": "secret-key"}}


def test_gateway_kwargs_gemini_uses_http_options():
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("gemini/gemini-2.5-flash")
    assert kw["api_base"] == "https://gw.example/v1"
    assert kw["client_args"] == {"http_options": {"headers": {"api-key": "secret-key"}}}


def test_gateway_kwargs_mistral_uses_async_client_headers():
    httpx = pytest.importorskip("httpx")  # skip when the mistral extra is absent
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("mistral/mistral-small-latest")
    client = kw["client_args"]["async_client"]
    assert isinstance(client, httpx.AsyncClient)
    assert client.headers["api-key"] == "secret-key"


def test_gateway_kwargs_groq_routes_via_base_url():
    _set_gateway()
    saved = os.environ.get("GROQ_API_KEY")
    os.environ.pop("GROQ_API_KEY", None)
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("groq/llama-3.3-70b-versatile")
        assert kw["client_args"]["base_url"] == "https://gw.example/v1"
        assert kw["client_args"]["default_headers"] == {"api-key": "secret-key"}
        # The real gateway secret must travel only via default_headers above —
        # the SDK's own api_key is a placeholder, never the gateway key (it
        # would otherwise also be sent as a bearer token).
        assert kw["api_key"] == "gateway"
        assert kw["api_key"] != "secret-key"
    finally:
        if saved is None:
            os.environ.pop("GROQ_API_KEY", None)
        else:
            os.environ["GROQ_API_KEY"] = saved


def test_gateway_kwargs_groq_ignores_env_key_when_gateway_configured():
    # This branch only runs once a gateway is configured, so base_url always
    # points at the gateway — GROQ_API_KEY must NOT leak into the SDK's own
    # api_key even when set, or a real Groq credential would be sent to the
    # gateway as a bearer token. The placeholder wins regardless.
    _set_gateway()
    saved = os.environ.get("GROQ_API_KEY")
    os.environ["GROQ_API_KEY"] = "groq-direct-key"
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("groq/llama-3.3-70b-versatile")
        assert kw["api_key"] == "gateway"
        assert kw["api_key"] != "groq-direct-key"
        assert kw["client_args"]["default_headers"] == {"api-key": "secret-key"}
    finally:
        if saved is None:
            os.environ.pop("GROQ_API_KEY", None)
        else:
            os.environ["GROQ_API_KEY"] = saved


def test_gateway_kwargs_azureopenai_passes_real_key_and_pinned_api_version():
    # Azure OpenAI sends api_key in the api-key header natively, so the real
    # gateway key is passed through directly. The api-version is pinned (the
    # SDK default "preview" 404s against the deployment-path URL).
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("azureopenai/gpt-4o-mini")
    assert kw == {
        "api_base": "https://gw.example/v1",
        "api_key": "secret-key",
        "client_args": {"api_version": "2024-10-21"},
    }


def test_gateway_kwargs_azure_foundry_defaults_to_sdk_api_version():
    # Foundry (azure-ai-inference) uses its own api-version; we don't pin one by
    # default (empty config), so no client_args is emitted — the SDK default applies.
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("azure/some-model")
    assert kw == {"api_base": "https://gw.example/v1", "api_key": "secret-key"}


def test_gateway_kwargs_azureopenai_api_version_is_configurable():
    _set_gateway()
    os.environ["AMP_LLM_JUDGE_AZURE_OPENAI_API_VERSION"] = "2025-01-01-preview"
    reload_config()
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("azureopenai/gpt-4o-mini")
        assert kw["client_args"] == {"api_version": "2025-01-01-preview"}
    finally:
        os.environ.pop("AMP_LLM_JUDGE_AZURE_OPENAI_API_VERSION", None)
        reload_config()


def test_gateway_kwargs_azure_foundry_api_version_is_configurable():
    _set_gateway()
    os.environ["AMP_LLM_JUDGE_AZURE_FOUNDRY_API_VERSION"] = "2024-05-01-preview"
    reload_config()
    try:
        kw = LLMAsJudgeEvaluator._gateway_kwargs("azure/some-model")
        assert kw["client_args"] == {"api_version": "2024-05-01-preview"}
    finally:
        os.environ.pop("AMP_LLM_JUDGE_AZURE_FOUNDRY_API_VERSION", None)
        reload_config()


def test_gateway_kwargs_bedrock_injects_header_via_boto3_client():
    boto3 = pytest.importorskip("boto3")  # noqa: F841 -- skip when bedrock extra absent
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("bedrock/anthropic.claude-3-5-sonnet")
    client = kw["client_args"]["client"]
    assert client.meta.endpoint_url == "https://gw.example/v1"

    # The before-send handler injects the gateway api-key header.
    class _Req:
        headers: dict = {}

    req = _Req()
    client.meta.events.emit("before-send.bedrock-runtime", request=req)
    assert req.headers["api-key"] == "secret-key"


def test_close_gateway_client_closes_mistral_async_client():
    httpx = pytest.importorskip("httpx")
    client = httpx.AsyncClient()
    LLMAsJudgeEvaluator._close_gateway_client({"client_args": {"async_client": client}})
    assert client.is_closed


def test_close_gateway_client_is_noop_without_a_client():
    # Providers that use default_headers (no async_client/client) must not error.
    LLMAsJudgeEvaluator._close_gateway_client({"client_args": {"default_headers": {"api-key": "x"}}})
    LLMAsJudgeEvaluator._close_gateway_client({})


def test_gateway_kwargs_supports_colon_separator():
    _set_gateway()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("gemini:gemini-2.5-flash")
    assert kw["client_args"] == {"http_options": {"headers": {"api-key": "secret-key"}}}


def test_gateway_kwargs_no_key_defers_to_provider_env():
    os.environ["AMP_LLM_JUDGE_API_BASE"] = "https://gw.example/v1"
    os.environ.pop("AMP_LLM_JUDGE_API_KEY", None)
    reload_config()
    kw = LLMAsJudgeEvaluator._gateway_kwargs("openai/gpt-4o-mini")
    assert kw == {"api_base": "https://gw.example/v1"}
