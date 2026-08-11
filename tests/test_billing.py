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
      {"product":"Actions", "unitType":"minutes", "discountAmount":1.8,
       "discountQuantity":999},
      {"product":"Packages", "unitType":"minutes", "discountAmount":50},
      {"product":"Actions", "unitType":"gigabytes", "discountAmount":50}
    ]}"""
    assert parse_used_minutes(body) == Decimal("300")


@pytest.mark.parametrize(
    "body",
    [
        b"not json",
        b"[]",
        b"{}",
        b'{"usageItems": {}}',
        b'{"usageItems": [null]}',
        b'{"usageItems":[{"product":1,"unitType":"minutes","discountAmount":2}]}',
        b'{"usageItems":[{"product":"Actions","unitType":"minutes"}]}',
        b'{"usageItems":[{"product":"Actions","unitType":"minutes","discountAmount":"2"}]}',
        b'{"usageItems":[{"product":"Actions","unitType":"minutes","discountAmount":NaN}]}',
    ],
)
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
    def __init__(self, response: FakeResponse) -> None:
        self.response = response
        self.request: urllib.request.Request | None = None

    def open(self, request: urllib.request.Request) -> FakeResponse:
        self.request = request
        return self.response


def test_fetch_uses_user_billing_endpoint_and_closes_response() -> None:
    response = FakeResponse(b'{"usageItems": []}')
    opener = RecordingOpener(response)
    assert (
        fetch_used_minutes(
            "owner/name",
            "secret",
            now=datetime(2026, 8, 11, tzinfo=UTC),
            opener=opener,
        )
        == 0
    )
    assert opener.request is not None
    assert opener.request.full_url == (
        "https://api.github.com/users/owner%2Fname/settings/billing/usage/summary"
        "?year=2026&month=8&product=Actions"
    )
    assert opener.request.get_header("Authorization") == "Bearer secret"
    assert opener.request.get_header("User-agent") == "github-actions-quota"
    assert response.closed
