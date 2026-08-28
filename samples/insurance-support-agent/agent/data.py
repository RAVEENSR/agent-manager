"""In-memory insurance fixtures. Mutated in place by file_claim."""

from __future__ import annotations

import itertools
import threading

POLICIES: dict[str, dict] = {
    "OZ-AUTO-4417": {
        "policy_number": "OZ-AUTO-4417",
        "holder": "Ada Lovelace",
        "product": "Motor",
        "vehicle": "2022 Volvo XC40",
        "status": "active",
        "premium_monthly": 62.40,
        "excess": 350,
        "renews_on": "2027-03-01",
        "cover": ["accidental damage", "third party", "fire and theft", "windscreen"],
    },
    "OZ-HOME-2280": {
        "policy_number": "OZ-HOME-2280",
        "holder": "Ada Lovelace",
        "product": "Home and contents",
        "address": "14 Marlow Gardens, Bristol",
        "status": "active",
        "premium_monthly": 28.15,
        "excess": 250,
        "renews_on": "2026-11-12",
        "cover": ["buildings", "contents", "escape of water", "accidental damage"],
    },
    "OZ-TRAV-9153": {
        "policy_number": "OZ-TRAV-9153",
        "holder": "Ada Lovelace",
        "product": "Travel (annual multi-trip)",
        "region": "Worldwide including USA",
        "status": "expires soon",
        "premium_monthly": 11.90,
        "excess": 100,
        "renews_on": "2026-09-30",
        "cover": ["medical", "cancellation", "baggage", "missed departure"],
    },
}

CLAIMS: dict[str, dict] = {
    "CLM-10432": {
        "claim_number": "CLM-10432",
        "policy_number": "OZ-AUTO-4417",
        "type": "Accidental damage",
        "description": "Rear bumper damaged in a car park",
        "amount_claimed": 840.00,
        "status": "in review",
        "filed_on": "2026-08-02",
        "next_step": "Assessor report expected by 2026-08-20",
    },
    "CLM-10876": {
        "claim_number": "CLM-10876",
        "policy_number": "OZ-HOME-2280",
        "type": "Escape of water",
        "description": "Leaking washing machine damaged kitchen flooring",
        "amount_claimed": 1450.00,
        "status": "settled",
        "filed_on": "2026-06-18",
        "next_step": "Paid on 2026-07-04",
    },
}

_NEXT_CLAIM_SEQ = itertools.count(10901)
_SEQ_LOCK = threading.Lock()


def next_claim_number() -> str:
    with _SEQ_LOCK:
        return f"CLM-{next(_NEXT_CLAIM_SEQ)}"
