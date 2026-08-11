from dataclasses import dataclass
from decimal import Decimal
from typing import Literal

DEFAULT_QUOTA_MINUTES = Decimal("2000")
FULL_CI_LIMIT = Decimal("0.50")
MINIMAL_CI_LIMIT = Decimal("1.00")

QuotaState = Literal["full", "quota_limited", "quota_exhausted"]


@dataclass(frozen=True)
class QuotaDecision:
    state: QuotaState
    reason: str
    full_ci: bool
    minimal_ci: bool
    used_minutes: Decimal
    quota_minutes: Decimal
    usage_percent: Decimal


def evaluate_quota(
    used_minutes: Decimal,
    quota_minutes: Decimal = DEFAULT_QUOTA_MINUTES,
) -> QuotaDecision:
    """Evaluate which CI tier remains available for the current usage."""
    if not quota_minutes.is_finite() or quota_minutes <= 0:
        raise ValueError("quota_minutes must be positive and finite")

    usage = used_minutes / quota_minutes
    usage_percent = usage * 100
    if usage < FULL_CI_LIMIT:
        return QuotaDecision(
            "full",
            "within_quota",
            True,
            True,
            used_minutes,
            quota_minutes,
            usage_percent,
        )
    if usage < MINIMAL_CI_LIMIT:
        return QuotaDecision(
            "quota_limited",
            "actions_quota_threshold",
            False,
            True,
            used_minutes,
            quota_minutes,
            usage_percent,
        )
    return QuotaDecision(
        "quota_exhausted",
        "actions_quota_exhausted",
        False,
        False,
        used_minutes,
        quota_minutes,
        usage_percent,
    )
