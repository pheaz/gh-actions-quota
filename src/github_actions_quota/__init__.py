# /src/gh_ci_control/__init__.py

from __future__ import annotations

import json
from decimal import Decimal
from typing import cast

DEFAULT_QUOTA_MINUTES = Decimal("2000")
LINUX_MINUTE_PRICE = Decimal("0.006")
FULL_CI_LIMIT = Decimal("0.50")
LOCK_CI_LIMIT = Decimal("1.00")


def decide_ci(
    used_minutes: Decimal, quota_minutes: Decimal = DEFAULT_QUOTA_MINUTES
) -> tuple[bool, bool]:
    if quota_minutes <= 0:
        raise ValueError("CI quota must be greater than zero")

    usage = used_minutes / quota_minutes
    return usage < FULL_CI_LIMIT, usage < LOCK_CI_LIMIT


def parse_used_minutes(response_body: bytes) -> Decimal:
    try:
        payload = cast(
            object,
            json.loads(response_body, parse_float=Decimal, parse_int=Decimal),
        )
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise ValueError("GitHub billing response is not valid JSON") from error

    summary = _require_mapping(payload, "GitHub billing response")
    usage_items = _require_list(
        summary.get("usageItems"), "GitHub billing response usageItems"
    )

    used_amount = Decimal(0)
    for index, candidate in enumerate(usage_items):
        usage_item = _require_mapping(candidate, f"usageItems[{index}]")
        product = _require_string(
            usage_item.get("product"), f"usageItems[{index}].product"
        )
        unit_type = _require_string(
            usage_item.get("unitType"), f"usageItems[{index}].unitType"
        )
        if product == "Actions" and unit_type == "minutes":
            discount_amount = _require_decimal(
                usage_item.get("discountAmount"),
                f"usageItems[{index}].discountAmount",
            )
            used_amount += discount_amount
    return used_amount / LINUX_MINUTE_PRICE


def _require_mapping(candidate: object, location: str) -> dict[str, object]:
    if not isinstance(candidate, dict):
        raise ValueError(f"{location} must be an object")

    result: dict[str, object] = {}
    for key, member in cast(dict[object, object], candidate).items():
        if not isinstance(key, str):
            raise ValueError(f"{location} contains a non-string key")
        result[key] = member
    return result


def _require_list(candidate: object, location: str) -> list[object]:
    if not isinstance(candidate, list):
        raise ValueError(f"{location} must be an array")
    return list(cast(list[object], candidate))


def _require_string(candidate: object, location: str) -> str:
    if not isinstance(candidate, str):
        raise ValueError(f"{location} must be a string")
    return candidate


def _require_decimal(candidate: object, location: str) -> Decimal:
    if not isinstance(candidate, Decimal):
        raise ValueError(f"{location} must be a number")
    if not candidate.is_finite():
        raise ValueError(f"{location} must be finite")
    return candidate
