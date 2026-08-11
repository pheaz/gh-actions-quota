from __future__ import annotations

import os
import sys
from decimal import Decimal, InvalidOperation
from pathlib import Path

from github_actions_quota import (
    DEFAULT_QUOTA_MINUTES,
    evaluate_quota,
    fetch_used_minutes,
)
from github_actions_quota.policy import QuotaDecision


def main() -> int:
    quota_text = os.environ.get(
        "GITHUB_ACTIONS_QUOTA_MINUTES", str(DEFAULT_QUOTA_MINUTES)
    )
    try:
        quota_minutes = Decimal(quota_text)
        token = _required_environment("GITHUB_ACTIONS_QUOTA_TOKEN")
        username = _required_environment("GITHUB_REPOSITORY_OWNER")
        decision = evaluate_quota(fetch_used_minutes(username, token), quota_minutes)
    except (InvalidOperation, ValueError, OSError) as error:
        _write_fail_closed(quota_text, error)
        return 0

    _append_environment_file("GITHUB_OUTPUT", _render_output(decision))
    if decision.state == "quota_limited":
        print(
            "::notice title=CI quota policy::Full CI skipped because Actions quota "
            f"usage is {_decimal_text(decision.usage_percent)}%."
        )
    elif decision.state == "quota_exhausted":
        print(
            "::notice title=CI quota policy::CI skipped because Actions quota "
            "is exhausted."
        )
    _append_environment_file("GITHUB_STEP_SUMMARY", _render_summary(decision))
    return 0


def _required_environment(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise ValueError(f"{name} is required")
    return value


def _write_fail_closed(quota_text: str, error: Exception) -> None:
    diagnostic = str(error).replace("\r", " ").replace("\n", " ")
    print(f"::warning title=CI quota policy::Billing usage unavailable: {diagnostic}")
    output = "\n".join(
        (
            "ci_state=unavailable",
            "ci_reason=billing_usage_unavailable",
            "full_ci=false",
            "minimal_ci=false",
            "used_minutes=unavailable",
            f"quota_minutes={quota_text}",
            "usage_percent=unavailable",
            "",
        )
    )
    _append_environment_file("GITHUB_OUTPUT", output)
    _append_environment_file(
        "GITHUB_STEP_SUMMARY",
        "## GitHub Actions quota\n\n"
        "State: `unavailable`\nReason: `billing_usage_unavailable`\n"
        "Full CI: `false`\nMinimal CI: `false`\n\n"
        f"Billing usage could not be determined. Diagnostic: `{diagnostic}`\n",
    )


def _render_output(decision: QuotaDecision) -> str:
    return "\n".join(
        (
            f"ci_state={decision.state}",
            f"ci_reason={decision.reason}",
            f"full_ci={_boolean(decision.full_ci)}",
            f"minimal_ci={_boolean(decision.minimal_ci)}",
            f"used_minutes={_decimal_text(decision.used_minutes)}",
            f"quota_minutes={_decimal_text(decision.quota_minutes)}",
            f"usage_percent={_decimal_text(decision.usage_percent)}",
            "",
        )
    )


def _render_summary(decision: QuotaDecision) -> str:
    return (
        "## GitHub Actions quota\n\n"
        f"State: `{decision.state}`\nReason: `{decision.reason}`\n"
        f"Usage: `{_decimal_text(decision.used_minutes)} / "
        f"{_decimal_text(decision.quota_minutes)} minutes "
        f"({_decimal_text(decision.usage_percent)}%)`\n"
        f"Full CI: `{_boolean(decision.full_ci)}`\n"
        f"Minimal CI: `{_boolean(decision.minimal_ci)}`\n"
    )


def _append_environment_file(variable: str, content: str) -> None:
    filename = os.environ.get(variable)
    if filename:
        with Path(filename).open("a", encoding="utf-8") as environment_file:
            environment_file.write(content)


def _decimal_text(number: Decimal) -> str:
    return format(number, "f")


def _boolean(value: bool) -> str:
    return str(value).lower()


if __name__ == "__main__":
    sys.exit(main())
