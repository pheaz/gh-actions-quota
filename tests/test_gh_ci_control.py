# /tests/test_gh_ci_control.py

from __future__ import annotations

import json
import urllib.request
from datetime import UTC, datetime
from decimal import Decimal
from pathlib import Path

import pytest

import gh_ci_control.__main__ as command
from gh_ci_control import (
    DEFAULT_QUOTA_MINUTES,
    LINUX_MINUTE_PRICE,
    decide_ci,
    parse_used_minutes,
)


@pytest.mark.parametrize(
    ("used", "expected"),
    [
        ("999.999", (True, True)),
        ("1000", (False, True)),
        ("1608.666666666", (False, True)),
        ("1999.999", (False, True)),
        ("2000", (False, False)),
        ("2000.001", (False, False)),
    ],
)
def test_decide_ci_at_quota_boundaries(used: str, expected: tuple[bool, bool]) -> None:
    assert decide_ci(Decimal(used), DEFAULT_QUOTA_MINUTES) == expected


def test_default_quota_is_2000_minutes() -> None:
    assert Decimal("2000") == DEFAULT_QUOTA_MINUTES


def test_decide_ci_accepts_configurable_quota() -> None:
    assert decide_ci(Decimal("250"), Decimal("1000")) == (True, True)


@pytest.mark.parametrize("quota", ["0", "-1"])
def test_decide_ci_rejects_non_positive_quota(quota: str) -> None:
    with pytest.raises(ValueError, match="greater than zero"):
        _ = decide_ci(Decimal("1"), Decimal(quota))


def test_parse_used_minutes_normalizes_supplied_mixed_runner_response() -> None:
    response = {
        "usageItems": [
            {
                "product": "Actions",
                "unitType": "minutes",
                "discountAmount": 6.13,
            },
            {
                "product": "Actions",
                "unitType": "minutes",
                "discountAmount": 5.87,
            },
        ]
    }

    used_minutes = parse_used_minutes(json.dumps(response).encode())

    assert used_minutes == Decimal("2000")
    assert decide_ci(used_minutes) == (False, False)


def test_parse_used_minutes_normalizes_linux_only_amount() -> None:
    response = b"""{
        "usageItems": [
            {"product": "Actions", "unitType": "minutes", "discountAmount": 3}
        ]
    }"""

    assert Decimal("0.006") == LINUX_MINUTE_PRICE
    assert parse_used_minutes(response) == Decimal("500")


def test_parse_used_minutes_normalizes_mixed_runner_amounts_not_quantities() -> None:
    response = b"""{
        "usageItems": [
            {"product": "Actions", "unitType": "minutes",
             "discountAmount": 0.6, "discountQuantity": 100},
            {"product": "Actions", "unitType": "minutes",
             "discountAmount": 1.2, "discountQuantity": 100}
        ]
    }"""

    assert parse_used_minutes(response) == Decimal("300")


def test_parse_used_minutes_ignores_other_products_and_units() -> None:
    response = b"""{
        "usageItems": [
            {"product": "Actions", "unitType": "minutes", "discountAmount": 0.072},
            {"product": "Packages", "unitType": "minutes", "discountAmount": 40},
            {"product": "Actions", "unitType": "gigabytes", "discountAmount": 50}
        ]
    }"""

    assert parse_used_minutes(response) == Decimal("12")


@pytest.mark.parametrize(
    "response",
    [
        b"not json",
        b"[]",
        b"{}",
        b'{"usageItems": {}}',
        b'{"usageItems": [null]}',
        b'{"usageItems": [{"product": 1, "unitType": "minutes", "discountAmount": 2}]}',
        b'{"usageItems": [{"product": "Actions", "unitType": "minutes"}]}',
        b'{"usageItems": [{"product": "Actions", "unitType": "minutes", "discountAmount": "2"}]}',
        b'{"usageItems": [{"product": "Actions", "unitType": "minutes", "discountAmount": NaN}]}',
        b'{"usageItems": [{"product": "Actions", "unitType": "minutes", "discountAmount": Infinity}]}',
    ],
)
def test_parse_used_minutes_rejects_malformed_responses(response: bytes) -> None:
    with pytest.raises(ValueError):
        _ = parse_used_minutes(response)


class FakeResponse:
    def __init__(self, body: bytes) -> None:
        self.body: bytes = body
        self.was_closed: bool = False

    def read(self) -> bytes:
        return self.body

    def close(self) -> None:
        self.was_closed = True


class RecordingOpener:
    def __init__(self, response: FakeResponse) -> None:
        self.response: FakeResponse = response
        self.request: urllib.request.Request | None = None

    def open(self, request: urllib.request.Request) -> FakeResponse:
        self.request = request
        return self.response


def test_fetch_used_minutes_uses_billing_endpoint_and_headers() -> None:
    response = FakeResponse(b'{"usageItems": []}')
    opener = RecordingOpener(response)

    result = command.fetch_used_minutes(
        "example-owner",
        "secret-token",
        now=datetime(2026, 8, 11, tzinfo=UTC),
        opener=opener,
    )

    assert result == 0
    assert opener.request is not None
    assert opener.request.full_url == (
        "https://api.github.com/users/example-owner/settings/billing/usage/summary"
        "?year=2026&month=8&product=Actions"
    )
    assert opener.request.get_header("Authorization") == "Bearer secret-token"
    assert opener.request.get_header("Accept") == "application/vnd.github+json"
    assert opener.request.get_header("X-github-api-version") == "2026-03-10"
    assert opener.request.get_header("User-agent") == "gh-ci-control"
    assert response.was_closed


def test_main_writes_configurable_quota_outputs(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output = tmp_path / "output"
    summary = tmp_path / "summary"
    monkeypatch.setenv("GH_CI_CONTROL_TOKEN", "secret-token")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "example-owner")
    monkeypatch.setenv("GH_CI_QUOTA_MINUTES", "3000")
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))

    def fetch_usage(username: str, token: str) -> Decimal:
        return Decimal("1200")

    monkeypatch.setattr(command, "fetch_used_minutes", fetch_usage)

    assert command.main() == 0
    assert output.read_text(encoding="utf-8").splitlines() == [
        "ci_state=full",
        "ci_reason=within_budget",
        "full_ci=true",
        "lock_ci=true",
        "used_minutes=1200",
        "quota_minutes=3000",
        "usage_percent=40.0",
    ]
    summary_text = summary.read_text(encoding="utf-8")
    assert "State: `full`" in summary_text
    assert "Reason: `within_budget`" in summary_text
    assert "Usage: `1200 / 3000 minutes (40.0%)`" in summary_text


@pytest.mark.parametrize(
    ("used", "expected_state", "expected_reason", "expected_notice"),
    [
        ("1000", "budget_limited", "actions_quota_threshold", "Full CI skipped"),
        ("1999.999", "budget_limited", "actions_quota_threshold", "Full CI skipped"),
        ("2000", "budget_exhausted", "actions_quota_exhausted", "CI skipped"),
    ],
)
def test_main_reports_budget_policy_state_and_notice(
    used: str,
    expected_state: str,
    expected_reason: str,
    expected_notice: str,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    output = tmp_path / "output"
    summary = tmp_path / "summary"
    monkeypatch.setenv("GH_CI_CONTROL_TOKEN", "secret-token")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "example-owner")
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))

    def fetch_usage(username: str, token: str) -> Decimal:
        return Decimal(used)

    monkeypatch.setattr(command, "fetch_used_minutes", fetch_usage)

    assert command.main() == 0
    output_text = output.read_text(encoding="utf-8")
    assert f"ci_state={expected_state}" in output_text
    assert f"ci_reason={expected_reason}" in output_text
    assert f"State: `{expected_state}`" in summary.read_text(encoding="utf-8")
    diagnostic = capsys.readouterr().out
    assert "::notice title=CI budget policy::" in diagnostic
    assert expected_notice in diagnostic
    assert "::warning" not in diagnostic


def test_api_failure_writes_fail_closed_outputs(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    output = tmp_path / "output"
    summary = tmp_path / "summary"
    monkeypatch.setenv("GH_CI_CONTROL_TOKEN", "secret-token")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "example-owner")
    monkeypatch.delenv("GH_CI_QUOTA_MINUTES", raising=False)
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))

    def fail_fetch(username: str, token: str) -> Decimal:
        raise OSError("billing API unavailable")

    monkeypatch.setattr(command, "fetch_used_minutes", fail_fetch)

    assert command.main() == 0
    assert output.read_text(encoding="utf-8").splitlines() == [
        "ci_state=unavailable",
        "ci_reason=billing_usage_unavailable",
        "full_ci=false",
        "lock_ci=false",
        "used_minutes=unavailable",
        "quota_minutes=2000",
        "usage_percent=unavailable",
    ]
    diagnostic = capsys.readouterr().out
    assert "::warning" in diagnostic
    assert "::notice" not in diagnostic
    summary_text = summary.read_text(encoding="utf-8")
    assert "State: `unavailable`" in summary_text
    assert "Reason: `billing_usage_unavailable`" in summary_text


@pytest.mark.parametrize("quota", ["invalid", "0", "-10", "NaN", "Infinity"])
def test_main_fails_closed_for_invalid_quota(
    quota: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output = tmp_path / "output"
    monkeypatch.setenv("GH_CI_CONTROL_TOKEN", "secret-token")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "example-owner")
    monkeypatch.setenv("GH_CI_QUOTA_MINUTES", quota)
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))

    assert command.main() == 0
    assert "full_ci=false\nlock_ci=false\n" in output.read_text(encoding="utf-8")
