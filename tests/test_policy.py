from decimal import Decimal

import pytest

from github_actions_quota import DEFAULT_QUOTA_MINUTES, evaluate_quota


@pytest.mark.parametrize(
    ("used", "state", "full", "minimal"),
    [
        ("999.999", "full", True, True),
        ("1000", "quota_limited", False, True),
        ("1999.999", "quota_limited", False, True),
        ("2000", "quota_exhausted", False, False),
        ("2000.001", "quota_exhausted", False, False),
    ],
)
def test_evaluate_quota_boundaries(
    used: str, state: str, full: bool, minimal: bool
) -> None:
    decision = evaluate_quota(Decimal(used))
    assert decision.state == state
    assert decision.full_ci is full
    assert decision.minimal_ci is minimal
    assert decision.used_minutes == Decimal(used)
    assert decision.quota_minutes == DEFAULT_QUOTA_MINUTES


def test_evaluate_quota_uses_configurable_quota_and_reports_percentage() -> None:
    decision = evaluate_quota(Decimal("250"), Decimal("1000"))
    assert decision.state == "full"
    assert decision.reason == "within_quota"
    assert decision.usage_percent == Decimal("25.00")


@pytest.mark.parametrize("quota", ["0", "-1", "NaN", "Infinity", "-Infinity"])
def test_evaluate_quota_rejects_invalid_quota(quota: str) -> None:
    with pytest.raises(ValueError, match="positive and finite"):
        evaluate_quota(Decimal(1), Decimal(quota))
