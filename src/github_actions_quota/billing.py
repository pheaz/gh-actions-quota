from __future__ import annotations

import json
import urllib.parse
import urllib.request
from datetime import UTC, datetime
from decimal import Decimal
from typing import Protocol, cast

API_VERSION = "2026-03-10"
LINUX_MINUTE_PRICE = Decimal("0.006")


class HttpResponse(Protocol):
    def read(self) -> bytes: ...

    def close(self) -> None: ...


class HttpOpener(Protocol):
    def open(self, request: urllib.request.Request) -> HttpResponse: ...


def parse_used_minutes(response_body: bytes) -> Decimal:
    """Parse billed discounts as Linux-equivalent included Actions minutes."""
    try:
        payload: object = json.loads(
            response_body,
            parse_float=Decimal,
            parse_int=Decimal,
            parse_constant=_reject_json_constant,
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
            used_amount += _require_decimal(
                usage_item.get("discountAmount"),
                f"usageItems[{index}].discountAmount",
            )
    return used_amount / LINUX_MINUTE_PRICE


def fetch_used_minutes(
    owner: str,
    token: str,
    *,
    owner_type: str | None = None,
    now: datetime | None = None,
    opener: HttpOpener | None = None,
) -> Decimal:
    """Fetch the repository owner's current Actions billing usage."""
    selected_opener: HttpOpener = opener or cast(
        "HttpOpener", urllib.request.build_opener()
    )
    resolved_type = owner_type or fetch_owner_type(owner, token, opener=selected_opener)
    current_time = now or datetime.now(UTC)
    query = urllib.parse.urlencode(
        {"year": current_time.year, "month": current_time.month, "product": "Actions"}
    )
    owner_path = urllib.parse.quote(owner, safe="")
    if resolved_type == "user":
        resource = f"users/{owner_path}"
    elif resolved_type == "organization":
        resource = f"organizations/{owner_path}"
    else:
        raise ValueError(f"unsupported GitHub owner type: {resolved_type}")
    request = _request(
        f"https://api.github.com/{resource}/settings/billing/usage/summary?{query}",
        token,
    )
    response = selected_opener.open(request)
    try:
        return parse_used_minutes(response.read())
    finally:
        response.close()


def fetch_owner_type(
    owner: str, token: str, *, opener: HttpOpener | None = None
) -> str:
    """Resolve a GitHub login to ``user`` or ``organization`` explicitly."""
    selected_opener: HttpOpener = opener or cast(
        "HttpOpener", urllib.request.build_opener()
    )
    owner_path = urllib.parse.quote(owner, safe="")
    response = selected_opener.open(
        _request(f"https://api.github.com/users/{owner_path}", token)
    )
    try:
        payload: object = json.loads(response.read())
    finally:
        response.close()
    account = _require_mapping(payload, "GitHub owner response")
    account_type = _require_string(account.get("type"), "GitHub owner response type")
    normalized = account_type.lower()
    if normalized not in {"user", "organization"}:
        raise ValueError(f"unsupported GitHub owner type: {account_type}")
    return normalized


def _request(url: str, token: str) -> urllib.request.Request:
    return urllib.request.Request(  # noqa: S310 -- URL is fixed to HTTPS.
        url,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": API_VERSION,
            "User-Agent": "github-actions-quota",
        },
    )


def _reject_json_constant(value: str) -> Decimal:
    raise ValueError(f"GitHub billing response contains invalid number {value}")


def _require_mapping(candidate: object, location: str) -> dict[str, object]:
    if not isinstance(candidate, dict):
        raise ValueError(f"{location} must be an object")
    untyped_mapping = cast("dict[object, object]", candidate)
    result: dict[str, object] = {}
    for key, member in untyped_mapping.items():
        if not isinstance(key, str):
            raise ValueError(f"{location} contains a non-string key")
        result[key] = member
    return result


def _require_list(candidate: object, location: str) -> list[object]:
    if not isinstance(candidate, list):
        raise ValueError(f"{location} must be an array")
    return list(cast("list[object]", candidate))


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
