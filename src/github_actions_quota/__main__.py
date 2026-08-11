# /src/gh_ci_control/__main__.py

from __future__ import annotations

import os
import sys
import urllib.parse
import urllib.request
from datetime import UTC, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Protocol

from gh_ci_control import DEFAULT_QUOTA_MINUTES, decide_ci, parse_used_minutes

API_VERSION = "2026-03-10"
FULL_STATE = ("full", "within_budget")
BUDGET_LIMITED_STATE = ("budget_limited", "actions_quota_threshold")
BUDGET_EXHAUSTED_STATE = ("budget_exhausted", "actions_quota_exhausted")
UNAVAILABLE_STATE = ("unavailable", "billing_usage_unavailable")


class HttpResponse(Protocol):
    def read(self) -> bytes: ...

    def close(self) -> None: ...


class HttpOpener(Protocol):
    def open(self, request: urllib.request.Request) -> HttpResponse: ...


def fetch_used_minutes(
    username: str,
    token: str,
    *,
    now: datetime | None = None,
    opener: HttpOpener | None = None,
) -> Decimal:
    current_time = now or datetime.now(UTC)
    query = urllib.parse.urlencode(
        {"year": current_time.year, "month": current_time.month, "product": "Actions"}
    )
    quoted_username = urllib.parse.quote(username, safe="")
    url = f"https://api.github.com/users/{quoted_username}/settings/billing/usage/summary?{query}"
    request = urllib.request.Request(  # noqa: S310 -- Die URL verwendet fest HTTPS.
        url,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": API_VERSION,
            "User-Agent": "gh-ci-control",
        },
    )
    selected_opener = opener or urllib.request.build_opener()
    response = selected_opener.open(request)
    try:
        return parse_used_minutes(response.read())
    finally:
        _ = response.close()


def main() -> int:
    quota_text = os.environ.get("GH_CI_QUOTA_MINUTES", str(DEFAULT_QUOTA_MINUTES))
    try:
        quota_minutes = Decimal(quota_text)
        if not quota_minutes.is_finite():
            raise ValueError("CI quota must be finite")
        token = _required_environment("GH_CI_CONTROL_TOKEN")
        username = _required_environment("GITHUB_REPOSITORY_OWNER")
        used_minutes = fetch_used_minutes(username, token)
        full_ci, lock_ci = decide_ci(used_minutes, quota_minutes)
    except (InvalidOperation, ValueError, OSError) as error:
        _write_fail_closed(quota_text, error)
        return 0

    usage_percent = used_minutes / quota_minutes * 100
    ci_state, ci_reason = _controller_state(full_ci, lock_ci)
    output = _render_output(
        ci_state,
        ci_reason,
        full_ci,
        lock_ci,
        used_minutes,
        quota_minutes,
        usage_percent,
    )
    _append_environment_file("GITHUB_OUTPUT", output)
    if not full_ci:
        if lock_ci:
            print(
                "".join(
                    (
                        "::notice title=CI budget policy::Full CI skipped because ",
                        "Actions quota usage is ",
                        f"{_decimal_text(usage_percent)}%.",
                    )
                )
            )
        else:
            print(
                "::notice title=CI budget policy::CI skipped because Actions quota "
                + "is exhausted."
            )
    _append_environment_file(
        "GITHUB_STEP_SUMMARY",
        "".join(
            (
                "## CI quota control\n\n",
                f"State: `{ci_state}`\n",
                f"Reason: `{ci_reason}`\n",
                f"Usage: `{_decimal_text(used_minutes)} / {_decimal_text(quota_minutes)} ",
                f"minutes ({_decimal_text(usage_percent)}%)`\n",
                f"Full CI: `{_boolean(full_ci)}`\n",
                f"Lock check: `{_boolean(lock_ci)}`\n",
            )
        ),
    )
    return 0


def _required_environment(name: str) -> str:
    setting = os.environ.get(name)
    if not setting:
        raise ValueError(f"{name} is required")
    return setting


def _write_fail_closed(quota_text: str, error: Exception) -> None:
    diagnostic = str(error).replace("\r", " ").replace("\n", " ")
    print(f"::warning title=CI quota control::Billing usage unavailable: {diagnostic}")
    output = "\n".join(
        (
            f"ci_state={UNAVAILABLE_STATE[0]}",
            f"ci_reason={UNAVAILABLE_STATE[1]}",
            "full_ci=false",
            "lock_ci=false",
            "used_minutes=unavailable",
            f"quota_minutes={quota_text}",
            "usage_percent=unavailable",
            "",
        )
    )
    _append_environment_file("GITHUB_OUTPUT", output)
    _append_environment_file(
        "GITHUB_STEP_SUMMARY",
        "".join(
            (
                "## CI quota control\n\n",
                f"State: `{UNAVAILABLE_STATE[0]}`\n",
                f"Reason: `{UNAVAILABLE_STATE[1]}`\n",
                "Usage: `unavailable`\n",
                "Full CI: `false`\n",
                "Lock check: `false`\n\n",
                "Billing usage could not be determined. Full CI and the lock check were ",
                f"disabled to protect the quota. Diagnostic: `{diagnostic}`\n",
            )
        ),
    )


def _render_output(
    ci_state: str,
    ci_reason: str,
    full_ci: bool,
    lock_ci: bool,
    used_minutes: Decimal,
    quota_minutes: Decimal,
    usage_percent: Decimal,
) -> str:
    return "\n".join(
        (
            f"ci_state={ci_state}",
            f"ci_reason={ci_reason}",
            f"full_ci={_boolean(full_ci)}",
            f"lock_ci={_boolean(lock_ci)}",
            f"used_minutes={_decimal_text(used_minutes)}",
            f"quota_minutes={_decimal_text(quota_minutes)}",
            f"usage_percent={_decimal_text(usage_percent)}",
            "",
        )
    )


def _controller_state(full_ci: bool, lock_ci: bool) -> tuple[str, str]:
    if full_ci:
        return FULL_STATE
    if lock_ci:
        return BUDGET_LIMITED_STATE
    return BUDGET_EXHAUSTED_STATE


def _append_environment_file(variable: str, content: str) -> None:
    filename = os.environ.get(variable)
    if filename:
        with Path(filename).open("a", encoding="utf-8") as environment_file:
            _ = environment_file.write(content)


def _decimal_text(number: Decimal) -> str:
    return format(number, "f")


def _boolean(state: bool) -> str:
    return str(state).lower()


if __name__ == "__main__":
    sys.exit(main())
