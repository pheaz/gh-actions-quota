from dataclasses import dataclass
from decimal import Decimal

DEFAULT_QUOTA_MINUTES = Decimal("2000")


@dataclass(frozen=True)
class QuotaUsage:
    used_minutes: Decimal
    quota_minutes: Decimal
    usage_percent: Decimal


def calculate_usage(
    used_minutes: Decimal,
    quota_minutes: Decimal = DEFAULT_QUOTA_MINUTES,
) -> QuotaUsage:
    """Calculate the percentage of the included quota that has been used."""
    if not quota_minutes.is_finite() or quota_minutes <= 0:
        raise ValueError("quota_minutes must be positive and finite")

    return QuotaUsage(
        used_minutes=used_minutes,
        quota_minutes=quota_minutes,
        usage_percent=used_minutes / quota_minutes * 100,
    )
