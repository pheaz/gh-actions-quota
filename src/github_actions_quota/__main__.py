from __future__ import annotations

import json
import os
import subprocess
import sys
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import cast

from github_actions_quota import (
    DEFAULT_QUOTA_MINUTES,
    calculate_usage,
    fetch_used_minutes,
)
from github_actions_quota.billing import fetch_owner_type
from github_actions_quota.usage import QuotaUsage


def main() -> int:
    quota_text = os.environ.get("ACTIONS_QUOTA_MINUTES", str(DEFAULT_QUOTA_MINUTES))
    try:
        quota_minutes = Decimal(quota_text)
        # Validate configuration even when public Actions minutes are unmetered.
        calculate_usage(Decimal(0), quota_minutes)
        public_context = _public_actions_context()
        owner = _billing_owner()
        if public_context is not None:
            owner_type = public_context or "unavailable"
            usage = calculate_usage(Decimal(0), quota_minutes)
            _write_success(usage, owner, owner_type, unmetered=True)
            return 0

        token = _authentication_token()
        owner_type = fetch_owner_type(owner, token)
        usage = calculate_usage(
            fetch_used_minutes(owner, token, owner_type=owner_type), quota_minutes
        )
    except (InvalidOperation, ValueError, OSError, subprocess.SubprocessError) as error:
        _write_fail_closed(quota_text, error)
        return 0

    _write_success(usage, owner, owner_type)
    return 0


def _authentication_token() -> str:
    token = os.environ.get("ACTIONS_QUOTA_TOKEN")
    if token:
        return token
    if os.environ.get("GITHUB_ACTIONS") == "true":
        raise ValueError("ACTIONS_QUOTA_TOKEN is required in GitHub Actions")
    result = subprocess.run(
        ["gh", "auth", "token"],  # noqa: S607
        check=True,
        capture_output=True,
        text=True,
    )
    token = result.stdout.strip()
    if not token:
        raise ValueError("gh auth token returned an empty token")
    return token


def _billing_owner() -> str:
    owner = os.environ.get("GITHUB_REPOSITORY_OWNER")
    if owner:
        return owner
    if os.environ.get("GITHUB_ACTIONS") == "true":
        raise ValueError("GITHUB_REPOSITORY_OWNER is required in GitHub Actions")
    result = subprocess.run(
        ["gh", "repo", "view", "--json", "owner", "--jq", ".owner.login"],  # noqa: S607
        check=True,
        capture_output=True,
        text=True,
    )
    owner = result.stdout.strip()
    if not owner:
        raise ValueError("could not resolve the current repository owner")
    return owner


def _public_actions_context() -> str | None:
    if os.environ.get("GITHUB_ACTIONS") != "true":
        return None
    visibility = os.environ.get("GITHUB_REPOSITORY_VISIBILITY")
    owner_type = ""
    event_path = os.environ.get("GITHUB_EVENT_PATH")
    if event_path:
        with Path(event_path).open(encoding="utf-8") as event_file:
            event = cast("dict[str, object]", json.load(event_file))
        repository = event.get("repository")
        if isinstance(repository, dict):
            if repository.get("private") is False:
                visibility = "public"
            owner = repository.get("owner")
            if isinstance(owner, dict) and isinstance(owner.get("type"), str):
                owner_type = cast("str", owner["type"]).lower()
    return owner_type if visibility == "public" else None


def _write_success(
    usage: QuotaUsage, owner: str, owner_type: str, *, unmetered: bool = False
) -> None:
    output = "\n".join(
        (
            "usage_available=true",
            f"used_minutes={_decimal_text(usage.used_minutes)}",
            f"quota_minutes={_decimal_text(usage.quota_minutes)}",
            f"usage_percent={_decimal_text(usage.usage_percent)}",
            f"billing_owner={owner}",
            f"billing_owner_type={owner_type}",
            "",
        )
    )
    _append_environment_file("GITHUB_OUTPUT", output)
    note = (
        "Standard GitHub-hosted runners are free for this public repository; "
        "included-minute quota is not applicable.\n"
        if unmetered
        else ""
    )
    _append_environment_file(
        "GITHUB_STEP_SUMMARY",
        "## GitHub Actions quota\n\n"
        f"Usage: `{_decimal_text(usage.used_minutes)} / "
        f"{_decimal_text(usage.quota_minutes)} minutes "
        f"({_decimal_text(usage.usage_percent)}%)`\n"
        f"Billing owner: `{owner}` (`{owner_type}`)\n\n{note}",
    )


def _write_fail_closed(quota_text: str, error: Exception) -> None:
    diagnostic = str(error).replace("\r", " ").replace("\n", " ")
    print(f"::warning title=Actions quota::Billing usage unavailable: {diagnostic}")
    output = "\n".join(
        (
            "usage_available=false",
            "used_minutes=unavailable",
            f"quota_minutes={quota_text}",
            "usage_percent=unavailable",
            "",
        )
    )
    _append_environment_file("GITHUB_OUTPUT", output)
    _append_environment_file(
        "GITHUB_STEP_SUMMARY",
        "## GitHub Actions quota\n\nUsage: `unavailable`\n\n"
        f"Billing usage could not be determined. Diagnostic: `{diagnostic}`\n",
    )


def _append_environment_file(variable: str, content: str) -> None:
    filename = os.environ.get(variable)
    if filename:
        with Path(filename).open("a", encoding="utf-8") as environment_file:
            environment_file.write(content)


def _decimal_text(number: Decimal) -> str:
    return format(number, "f")


if __name__ == "__main__":
    sys.exit(main())
