from decimal import Decimal

import pytest

from github_actions_quota import DEFAULT_QUOTA_MINUTES, QuotaUsage, calculate_usage


def test_calculate_usage_uses_default_quota() -> None:
    assert calculate_usage(Decimal("1200")) == QuotaUsage(
        Decimal("1200"), DEFAULT_QUOTA_MINUTES, Decimal("60.0")
    )


def test_calculate_usage_uses_configurable_quota() -> None:
    assert calculate_usage(Decimal("250"), Decimal("1000")).usage_percent == Decimal(
        "25"
    )


@pytest.mark.parametrize("quota", ["0", "-1", "NaN", "Infinity", "-Infinity"])
def test_calculate_usage_rejects_invalid_quota(quota: str) -> None:
    with pytest.raises(ValueError, match="positive and finite"):
        calculate_usage(Decimal(1), Decimal(quota))
