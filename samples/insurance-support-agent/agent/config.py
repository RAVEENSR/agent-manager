"""Instance configuration, read from env at startup."""

from __future__ import annotations

import os
from dataclasses import dataclass


def _env(name: str, default: str | None = None) -> str:
    val = os.environ.get(name) or default
    if val is None:
        raise RuntimeError(f"Missing required env var: {name}")
    return val


@dataclass(frozen=True)
class Config:
    openai_api_key: str
    openai_base_url: str
    openai_model: str
    company_name: str

    @classmethod
    def from_env(cls) -> "Config":
        return cls(
            openai_api_key=_env("OPENAI_API_KEY"),
            openai_base_url=_env("OPENAI_BASE_URL", ""),
            openai_model=_env("OPENAI_MODEL", "gpt-4o-mini"),
            company_name=_env("COMPANY_NAME", "O2 Insurance"),
        )
