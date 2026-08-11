import json
import urllib.request
from datetime import UTC, datetime
from decimal import Decimal

import pytest

from github_actions_quota import fetch_used_minutes, parse_used_minutes
from github_actions_quota.billing import LINUX_MINUTE_PRICE


def test_mixed_runner_discounts_normalize_to_linux_minutes() -> None:
    body = json.dumps(
        {
            "usageItems": [
                {"product": "Actions", "unitType": "minutes", "discountAmount": 6.13},
                {"product": "Actions", "unitType": "minutes", "discountAmount": 5.87},
            ]
        }
    ).encode()
    assert Decimal("0.006") == LINUX_MINUTE_PRICE
    assert parse_used_minutes(body) == Decimal("2000")


def test_parser_uses_amounts_and_ignores_unrelated_usage() -> None:
    body = b"""{"usageItems": [
      {"product":"Actions", "unitType":"minutes", "discountAmount":1.8},
      {"product":"Packages", "unitType":"minutes", "discountAmount":50},
      {"product":"Actions", "unitType":"gigabytes", "discountAmount":50}
    ]}"""
    assert parse_used_minutes(body) == Decimal("300")


@pytest.mark.parametrize("body", [b"not json", b"[]", b"{}", b'{"usageItems": {}}'])
def test_parser_rejects_malformed_response(body: bytes) -> None:
    with pytest.raises(ValueError):
        parse_used_minutes(body)


class FakeResponse:
    def __init__(self, body: bytes) -> None:
        self.body = body
        self.closed = False

    def read(self) -> bytes:
        return self.body

    def close(self) -> None:
        self.closed = True


class RecordingOpener:
    def __init__(self, *bodies: bytes) -> None:
        self.responses = [FakeResponse(body) for body in bodies]
        self.requests: list[urllib.request.Request] = []

    def open(self, request: urllib.request.Request) -> FakeResponse:
        self.requests.append(request)
        return self.responses[len(self.requests) - 1]


@pytest.mark.parametrize(
    ("api_type", "resource"),
    [("User", "users"), ("Organization", "organizations")],
)
def test_fetch_resolves_owner_type_and_uses_matching_endpoint(
    api_type: str, resource: str
) -> None:
    opener = RecordingOpener(
        json.dumps({"type": api_type}).encode(), b'{"usageItems": []}'
    )
    assert (
        fetch_used_minutes(
            "owner/name", "secret", now=datetime(2026, 8, 11, tzinfo=UTC), opener=opener
        )
        == 0
    )
    assert opener.requests[0].full_url == "https://api.github.com/users/owner%2Fname"
    assert opener.requests[1].full_url == (
        f"https://api.github.com/{resource}/owner%2Fname/settings/billing/usage/summary"
        "?year=2026&month=8&product=Actions"
    )
    assert all(response.closed for response in opener.responses)
