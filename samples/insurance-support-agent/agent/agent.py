"""Strands agent construction."""

from __future__ import annotations

from strands import Agent
from strands.models.openai import OpenAIModel

from config import Config
from system_prompt import SYSTEM_PROMPT
from tools import (
    file_claim,
    get_claim_status,
    list_claims,
    list_policies,
    lookup_policy,
)


def build_agent(cfg: Config) -> Agent:
    client_args: dict[str, str] = {"api_key": cfg.openai_api_key}
    if cfg.openai_base_url:
        client_args["base_url"] = cfg.openai_base_url

    model = OpenAIModel(client_args=client_args, model_id=cfg.openai_model)

    return Agent(
        model=model,
        tools=[
            list_policies,
            list_claims,
            lookup_policy,
            get_claim_status,
            file_claim,
        ],
        system_prompt=SYSTEM_PROMPT.format(company=cfg.company_name),
        callback_handler=None,
    )
